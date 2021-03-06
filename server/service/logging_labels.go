package service

import (
	"context"
	"time"

	"github.com/kolide/fleet/server/kolide"
)

func (mw loggingMiddleware) ListLabels(ctx context.Context, opt kolide.ListOptions) ([]*kolide.Label, error) {
	var (
		labels []*kolide.Label
		err    error
	)

	defer func(begin time.Time) {
		_ = mw.logger.Log(
			"method", "ListLabels",
			"err", err,
			"took", time.Since(begin),
		)
	}(time.Now())

	labels, err = mw.Service.ListLabels(ctx, opt)
	return labels, err
}

func (mw loggingMiddleware) GetLabel(ctx context.Context, id uint) (*kolide.Label, error) {
	var (
		label *kolide.Label
		err   error
	)

	defer func(begin time.Time) {
		_ = mw.logger.Log(
			"method", "GetLabel",
			"err", err,
			"took", time.Since(begin),
		)
	}(time.Now())

	label, err = mw.Service.GetLabel(ctx, id)
	return label, err
}

func (mw loggingMiddleware) DeleteLabel(ctx context.Context, name string) error {
	var (
		err error
	)

	defer func(begin time.Time) {
		_ = mw.logger.Log(
			"method", "DeleteLabel",
			"err", err,
			"took", time.Since(begin),
		)
	}(time.Now())

	err = mw.Service.DeleteLabel(ctx, name)
	return err
}
