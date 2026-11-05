package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/app/process"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestConnectCloudsShareRuntimeMessageClaims(t *testing.T) {
	t.Parallel()

	open := func(context.Context) (device.Device, error) { return nil, device.ErrDisconnected }
	first, second := NewRuntime(t.Context(), open), NewRuntime(t.Context(), open)

	t.Cleanup(func() {
		for _, runtime := range []*process.Runtime{first, second} {
			if err := runtime.Close(context.WithoutCancel(t.Context())); err != nil {
				t.Error(err)
			}
		}
	})

	// connectConfig hands every Cloud of one runtime the runtime's registry.
	claims := connectConfig(first).Claims
	if claims == nil || claims != connectConfig(first).Claims || claims != first.MessageClaims() {
		t.Fatal("clouds of one runtime do not share its message claims")
	}

	if connectConfig(second).Claims == claims {
		t.Fatal("two runtimes share message claims")
	}
}

func TestConnectForAndJobConnectDeliverQueuedMessageOnce(t *testing.T) {
	t.Parallel()

	root := testdump.ConnectRoot(t)
	watch := NewRuntime(t.Context(), func(context.Context) (device.Device, error) { return device.NewDirFS(root) })

	t.Cleanup(func() {
		if err := watch.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})

	// The fake Connect lists the message as new on every request, as a listing
	// taken before another pull's acknowledgement would.
	account := testdump.ConnectClient(t, "")

	lease, err := watch.Manager().Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	for i := range 2 {
		if _, err := ConnectFor(watch, account)(lease.Session()).Pull(lease.Context(), nil); err != nil {
			t.Fatalf("bounded pull %d: %v", i, err)
		}
	}

	epoch := lease.Epoch()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	if _, err := JobConnect(watch, account, epoch).Pull(t.Context(), nil); err != nil {
		t.Fatalf("job pull: %v", err)
	}

	written, err := os.ReadDir(filepath.Join(root, "GARMIN", "NewFiles"))
	if err != nil {
		t.Fatal(err)
	}

	if len(written) != 1 {
		t.Fatalf("queued message written %d times", len(written))
	}
}
