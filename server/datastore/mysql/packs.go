package mysql

import (
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/kolide/fleet/server/kolide"
	"github.com/pkg/errors"
)

func (d *Datastore) ApplyPackSpecs(specs []*kolide.PackSpec) (err error) {
	tx, err := d.db.Beginx()
	if err != nil {
		return errors.Wrap(err, "begin ApplyPackSpec transaction")
	}

	defer func() {
		if err != nil {
			rbErr := tx.Rollback()
			// It seems possible that there might be a case in
			// which the error we are dealing with here was thrown
			// by the call to tx.Commit(), and the docs suggest
			// this call would then result in sql.ErrTxDone.
			if rbErr != nil && rbErr != sql.ErrTxDone {
				panic(fmt.Sprintf("got err '%s' rolling back after err '%s'", rbErr, err))
			}
		}
	}()

	for _, spec := range specs {
		err = applyPackSpec(tx, spec)
		if err != nil {
			return errors.Wrapf(err, "applying pack '%s'", spec.Name)
		}
	}

	err = tx.Commit()
	return errors.Wrap(err, "commit transaction")
}

func applyPackSpec(tx *sqlx.Tx, spec *kolide.PackSpec) error {
	// Insert/update pack
	query := `
		INSERT INTO packs (name, description, platform)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE
			name = VALUES(name),
			description = VALUES(description),
			platform = VALUES(platform),
			deleted = false
	`
	if _, err := tx.Exec(query, spec.Name, spec.Description, spec.Platform); err != nil {
		return errors.Wrap(err, "insert/update pack")
	}

	// Get Pack ID
	// This is necessary because MySQL last_insert_id does not return a value
	// if no update was made.
	var packID uint
	query = "SELECT id FROM packs WHERE name = ?"
	if err := tx.Get(&packID, query, spec.Name); err != nil {
		return errors.Wrap(err, "getting pack ID")
	}

	// Delete existing scheduled queries for pack
	query = "DELETE FROM scheduled_queries WHERE pack_id = ?"
	if _, err := tx.Exec(query, packID); err != nil {
		return errors.Wrap(err, "delete existing scheduled queries")
	}

	// Insert new scheduled queries for pack
	for _, q := range spec.Queries {
		query = `
			INSERT INTO scheduled_queries (
				pack_id, query_name, name, description, ` + "`interval`" + `,
				snapshot, removed, shard, platform, version
			)
			VALUES (
				?, ?, ?, ?, ?,
				?, ?, ?, ?, ?
			)
		`
		_, err := tx.Exec(query,
			packID, q.QueryName, q.Name, q.Description, q.Interval,
			q.Snapshot, q.Removed, q.Shard, q.Platform, q.Version,
		)
		switch {
		case isChildForeignKeyError(err):
			return errors.Errorf("cannot schedule unknown query '%s'", q.QueryName)
		case err != nil:
			return errors.Wrapf(err, "adding query %s referencing %s", q.Name, q.QueryName)
		}
	}

	// Delete existing targets
	query = "DELETE FROM pack_targets WHERE pack_id = ?"
	if _, err := tx.Exec(query, packID); err != nil {
		return errors.Wrap(err, "delete existing targets")
	}

	// Insert targets
	for _, l := range spec.Targets.Labels {
		query = `
			INSERT INTO pack_targets (pack_id, type, target_id)
			VALUES (?, ?, (SELECT id FROM labels WHERE name = ?))
		`
		if _, err := tx.Exec(query, packID, kolide.TargetLabel, l); err != nil {
			return errors.Wrap(err, "adding label to pack")
		}
	}

	return nil
}

func (d *Datastore) GetPackSpecs() (specs []*kolide.PackSpec, err error) {
	tx, err := d.db.Beginx()
	if err != nil {
		return nil, errors.Wrap(err, "begin GetPackSpecs transaction")
	}

	defer func() {
		if err != nil {
			rbErr := tx.Rollback()
			// It seems possible that there might be a case in
			// which the error we are dealing with here was thrown
			// by the call to tx.Commit(), and the docs suggest
			// this call would then result in sql.ErrTxDone.
			if rbErr != nil && rbErr != sql.ErrTxDone {
				panic(fmt.Sprintf("got err '%s' rolling back after err '%s'", rbErr, err))
			}
		}
	}()

	// Get basic specs
	query := "SELECT id, name, description, platform FROM packs"
	if err := tx.Select(&specs, query); err != nil {
		return nil, errors.Wrap(err, "get packs")
	}

	// Load targets
	for _, spec := range specs {
		query = `
SELECT l.name
FROM labels l JOIN pack_targets pt
WHERE pack_id = ? AND pt.type = ? AND pt.target_id = l.id
`
		if err := tx.Select(&spec.Targets.Labels, query, spec.ID, kolide.TargetLabel); err != nil {
			return nil, errors.Wrap(err, "get pack targets")
		}
	}

	// Load queries
	for _, spec := range specs {
		query = `
SELECT
query_name, name, description, ` + "`interval`" + `,
snapshot, removed, shard, platform, version
FROM scheduled_queries
WHERE pack_id = ?
`
		if err := tx.Select(&spec.Queries, query, spec.ID); err != nil {
			return nil, errors.Wrap(err, "get pack queries")
		}
	}

	err = tx.Commit()
	if err != nil {
		return nil, errors.Wrap(err, "commit transaction")
	}

	return specs, nil
}

func (d *Datastore) PackByName(name string, opts ...kolide.OptionalArg) (*kolide.Pack, bool, error) {
	db := d.getTransaction(opts)
	sqlStatement := `
		SELECT *
			FROM packs
			WHERE name = ? AND NOT deleted
	`
	var pack kolide.Pack
	err := db.Get(&pack, sqlStatement, name)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, errors.Wrap(err, "fetching packs by name")
	}

	return &pack, true, nil
}

// DeletePack soft deletes a kolide.Pack so that it won't show up in results
func (d *Datastore) DeletePack(pid uint) error {
	return d.deleteEntity("packs", pid)
}

// Pack fetch kolide.Pack with matching ID
func (d *Datastore) Pack(pid uint) (*kolide.Pack, error) {
	query := `SELECT * FROM packs WHERE id = ? AND NOT deleted`
	pack := &kolide.Pack{}
	err := d.db.Get(pack, query, pid)
	if err == sql.ErrNoRows {
		return nil, notFound("Pack").WithID(pid)
	} else if err != nil {
		return nil, errors.Wrap(err, "getting pack")
	}

	return pack, nil
}

// ListPacks returns all kolide.Pack records limited and sorted by kolide.ListOptions
func (d *Datastore) ListPacks(opt kolide.ListOptions) ([]*kolide.Pack, error) {
	query := `SELECT * FROM packs WHERE NOT deleted`
	packs := []*kolide.Pack{}
	err := d.db.Select(&packs, appendListOptionsToSQL(query, opt))
	if err != nil && err != sql.ErrNoRows {
		return nil, errors.Wrap(err, "listing packs")
	}
	return packs, nil
}

// ListLabelsForPack will return a list of kolide.Label records associated with kolide.Pack
func (d *Datastore) ListLabelsForPack(pid uint) ([]*kolide.Label, error) {
	query := `
	SELECT
		l.id,
		l.created_at,
		l.updated_at,
		l.name
	FROM
		labels l
	JOIN
		pack_targets pt
	ON
		pt.target_id = l.id
	WHERE
		pt.type = ?
			AND
		pt.pack_id = ?
	AND NOT l.deleted
	`

	labels := []*kolide.Label{}

	if err := d.db.Select(&labels, query, kolide.TargetLabel, pid); err != nil && err != sql.ErrNoRows {
		return nil, errors.Wrap(err, "listing labels for pack")
	}

	return labels, nil
}

func (d *Datastore) ListPacksForHost(hid uint) ([]*kolide.Pack, error) {
	query := `
		SELECT DISTINCT p.*
		FROM packs p
		JOIN pack_targets pt
		JOIN label_query_executions lqe
		ON (
		  p.id = pt.pack_id
		  AND pt.target_id = lqe.label_id
		  AND pt.type = ?
		  AND lqe.matches
		)
		WHERE lqe.host_id = ? AND NOT p.disabled
	`

	packs := []*kolide.Pack{}
	if err := d.db.Select(&packs, query, kolide.TargetLabel, hid); err != nil && err != sql.ErrNoRows {
		return nil, errors.Wrap(err, "listing hosts in pack")
	}
	return packs, nil
}

func (d *Datastore) ListHostsInPack(pid uint, opt kolide.ListOptions) ([]uint, error) {
	query := `
		SELECT DISTINCT h.id
		FROM hosts h
		JOIN pack_targets pt
		JOIN label_query_executions lqe
		ON (
		  pt.target_id = lqe.label_id
		  AND lqe.host_id = h.id
		  AND lqe.matches
		  AND pt.type = ?
		) OR (
		  pt.target_id = h.id
		  AND pt.type = ?
		)
		WHERE pt.pack_id = ?
	`

	hosts := []uint{}
	if err := d.db.Select(&hosts, appendListOptionsToSQL(query, opt), kolide.TargetLabel, kolide.TargetHost, pid); err != nil && err != sql.ErrNoRows {
		return nil, errors.Wrap(err, "listing hosts in pack")
	}
	return hosts, nil
}

func (d *Datastore) ListExplicitHostsInPack(pid uint, opt kolide.ListOptions) ([]uint, error) {
	query := `
		SELECT DISTINCT h.id
		FROM hosts h
		JOIN pack_targets pt
		ON (
		  pt.target_id = h.id
		  AND pt.type = ?
		)
		WHERE pt.pack_id = ?
	`
	hosts := []uint{}
	if err := d.db.Select(&hosts, appendListOptionsToSQL(query, opt), kolide.TargetHost, pid); err != nil && err != sql.ErrNoRows {
		return nil, errors.Wrap(err, "listing explicit hosts in pack")
	}
	return hosts, nil

}
