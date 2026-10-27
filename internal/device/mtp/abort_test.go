package mtp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
)

type abortWire struct {
	testWire
	entered chan struct{}
	exited  chan struct{}
}

func (w *abortWire) ReadContext(ctx context.Context, _ []byte) (int, error) {
	close(w.entered)
	defer close(w.exited)

	<-ctx.Done()

	return 0, ctx.Err()
}

func TestAbortInterruptsMTPWithoutTransactionLockOrHandleClose(t *testing.T) {
	t.Parallel()

	w := &abortWire{entered: make(chan struct{}), exited: make(chan struct{})}

	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &Device{protocol: protocol{wire: w, lifetime: lifetime}, abort: cancel}
	done := make(chan error, 1)

	go func() {
		d.mu.Lock()
		_, _, err := d.protocol.exchange(t.Context(), 0x1009, []uint32{42}, nil)
		d.mu.Unlock()

		done <- err
	}()

	<-w.entered
	d.Abort()

	select {
	case err := <-done:
		if !errors.Is(err, device.ErrDisconnected) || !errors.Is(err, context.Canceled) {
			t.Fatalf("aborted transaction: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Abort waited for transaction mutex")
	}

	if w.closed {
		t.Fatal("Abort closed handles during transfer cancellation")
	}

	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	if !w.closed {
		t.Fatal("owner did not close handles after transfer unwound")
	}
}
