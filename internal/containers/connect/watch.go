package connect

import (
	"context"
	"errors"
	"fmt"

	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/deviceinfo"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// NewJobCloud performs sync without retaining a request's operation lease.
// Sync binds its lifetime to epoch and acquires only bounded device groups.
// Bounded actions use NewCloud with their already leased Session.
func NewJobCloud(watch *watchsession.Manager, epoch watchsession.ConnectionEpoch, client *gc.Client, configs ...Config) *Cloud {
	c := NewCloud(nil, client, configs...)
	c.watch, c.epoch = watch, epoch

	return c
}

func (c *Cloud) withWatch(ctx context.Context, body func(context.Context, *watchsession.Session) error) error {
	if c.watch == nil {
		if c.session == nil {
			return fmt.Errorf("%w: no watch connection", device.ErrDisconnected)
		}

		return body(ctx, c.session)
	}

	lease, err := c.watch.AcquireAt(ctx, c.epoch)
	if err != nil {
		return err
	}

	err = body(lease.Context(), lease.Session())
	if errors.Is(err, device.ErrDisconnected) {
		err = errors.Join(err, c.watch.DropAt(lease.Epoch()))
	}

	return errors.Join(err, lease.Release())
}

func (c *Cloud) deviceXML(ctx context.Context) (*deviceinfo.DeviceXML, error) {
	var dx *deviceinfo.DeviceXML

	err := c.withWatch(ctx, func(ctx context.Context, sess *watchsession.Session) error {
		var err error

		dx, err = sess.DeviceXML(ctx)

		return err
	})

	return dx, err
}

func (c *Cloud) list(ctx context.Context, dir string) ([]device.Entry, error) {
	var entries []device.Entry

	err := c.withWatch(ctx, func(ctx context.Context, sess *watchsession.Session) error {
		var err error

		entries, err = sess.List(ctx, dir)

		return err
	})

	return entries, err
}

func (c *Cloud) read(ctx context.Context, path string) ([]byte, error) {
	var data []byte

	err := c.withWatch(ctx, func(ctx context.Context, sess *watchsession.Session) error {
		var err error

		data, err = sess.ReadFile(ctx, path)

		return err
	})

	return data, err
}

func (c *Cloud) invalidateApps(ctx context.Context) error {
	return c.withWatch(ctx, func(_ context.Context, sess *watchsession.Session) error {
		gen := sess.Invalidate(apps.PathAppsConfig)
		if c.invalidated != nil {
			c.invalidated(sess, gen)
		}

		return nil
	})
}
