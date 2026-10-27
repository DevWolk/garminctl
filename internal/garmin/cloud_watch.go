package garmin

import (
	"context"
	"errors"
	"fmt"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/gc"
)

// NewJobCloud performs sync without retaining a request's operation lease.
// Sync binds its lifetime to epoch and acquires only bounded device groups.
// Legacy bounded actions use NewCloud with their already leased Service.
func NewJobCloud(watch *Lazy, epoch ConnectionEpoch, client *gc.Client) *Cloud {
	c := NewCloud(nil, client)
	c.watch, c.epoch = watch, epoch

	return c
}

func (c *Cloud) withWatch(ctx context.Context, body func(context.Context, *Service) error) error {
	if c.watch == nil {
		if c.svc == nil {
			return fmt.Errorf("%w: no watch connection", device.ErrDisconnected)
		}

		return body(ctx, c.svc)
	}

	lease, err := c.watch.AcquireAt(ctx, c.epoch)
	if err != nil {
		return err
	}

	err = body(lease.Context(), lease.Service())
	if errors.Is(err, device.ErrDisconnected) {
		err = errors.Join(err, c.watch.DropAt(lease.Epoch()))
	}

	return errors.Join(err, lease.Release())
}

func (c *Cloud) deviceXML(ctx context.Context) (*DeviceXML, error) {
	var dx *DeviceXML

	err := c.withWatch(ctx, func(ctx context.Context, svc *Service) error {
		var err error

		dx, err = svc.DeviceXML(ctx)

		return err
	})

	return dx, err
}

func (c *Cloud) list(ctx context.Context, dir string) ([]device.Entry, error) {
	var entries []device.Entry

	err := c.withWatch(ctx, func(ctx context.Context, svc *Service) error {
		var err error

		entries, err = svc.List(ctx, dir)

		return err
	})

	return entries, err
}

func (c *Cloud) read(ctx context.Context, path string) ([]byte, error) {
	var data []byte

	err := c.withWatch(ctx, func(ctx context.Context, svc *Service) error {
		var err error

		data, err = svc.ReadFile(ctx, path)

		return err
	})

	return data, err
}

func (c *Cloud) invalidateApps(ctx context.Context) error {
	return c.withWatch(ctx, func(_ context.Context, svc *Service) error { svc.invalidate(PathAppsConfig); return nil })
}
