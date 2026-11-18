package wpd

import (
	"context"
	"errors"
	"fmt"

	"github.com/DevWolk/garminctl/internal/ship/device"
)

// reopen publishes at most one replacement bridge. Slow process startup never
// holds d.mu: Close can cancel it and a waiting caller can cancel its own wait.
func (d *Device) reopen(ctx context.Context, previous *Bridge) (*Bridge, error) {
	for {
		d.mu.Lock()
		if d.closed {
			d.mu.Unlock()
			return nil, fmt.Errorf("%w: WPD device closed", device.ErrDisconnected)
		}

		if d.bridge != previous {
			b := d.bridge
			d.mu.Unlock()

			return b, nil
		}

		if done := d.reopening; done != nil {
			d.mu.Unlock()

			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		openCtx, cancel := context.WithCancel(ctx)
		d.reopening = make(chan struct{})
		d.cancelReopen = cancel
		d.mu.Unlock()

		defer cancel()

		return d.openReplacement(openCtx, previous)
	}
}

func (d *Device) openReplacement(ctx context.Context, previous *Bridge) (*Bridge, error) {
	defer func() {
		d.mu.Lock()
		close(d.reopening)
		d.reopening = nil
		d.cancelReopen = nil
		d.mu.Unlock()
	}()

	if err := previous.Close(); err != nil {
		return nil, err
	}

	open := d.open
	if open == nil {
		open = Open
	}

	fresh, err := open(ctx, d.info.ID)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	if d.closed || ctx.Err() != nil {
		d.mu.Unlock()

		closeErr := fresh.Close()

		return nil, fmt.Errorf("WPD reopen cancelled: %w", errors.Join(device.ErrDisconnected, ctx.Err(), closeErr))
	}

	d.bridge = fresh.bridge
	d.reconnects++
	d.mu.Unlock()

	return fresh.bridge, nil
}
