package main

import (
	"fmt"

	"github.com/kolide/fleet/server/service"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

func clientFromCLI(c *cli.Context) (*service.Client, error) {
	if err := makeConfigIfNotExists(c.String("config")); err != nil {
		return nil, errors.Wrapf(err, "error verifying that config exists at %s", c.String("config"))
	}

	config, err := readConfig(c.String("config"))
	if err != nil {
		return nil, err
	}

	cc, ok := config.Contexts[c.String("context")]
	if !ok {
		return nil, fmt.Errorf("context %q is not found", c.String("context"))
	}

	if cc.Address == "" {
		return nil, errors.New("set the Fleet API address with: fleetctl config set --address https://localhost:8080")
	}

	fleet, err := service.NewClient(cc.Address, cc.IgnoreTLS)
	if err != nil {
		return nil, errors.Wrap(err, "error creating Fleet API client handler")
	}

	t, err := getConfigValue(c, "token")
	if err != nil {
		return nil, errors.Wrap(err, "error getting token from the config")
	}

	if token, ok := t.(string); ok {
		fleet.SetToken(token)
	} else {
		return nil, errors.Errorf("token config value was not a string: %+v", t)
	}

	return fleet, nil
}
