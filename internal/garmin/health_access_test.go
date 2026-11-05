package garmin

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestHealthAccessBorrowsFacadeAndKeepsIdentityPerCaller(t *testing.T) {
	t.Parallel()
	root := testdump.HealthRoot(t)
	opens := 0
	watch := NewLazy(func(context.Context) (device.Device, error) {
		opens++
		return device.NewDirFS(root)
	})
	t.Cleanup(watch.Drop)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	lease, err := watch.Manager().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = lease.Release() }()

	client := testdump.HealthClient(t, "")
	delegate := client.HTTP.Transport
	profiles := 0
	client.HTTP.Transport = syncTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == socialProfilePath {
			profiles++
		}

		return delegate.RoundTrip(req)
	})
	access := watch.HealthAccessFor(lease.Session(), client)

	cache := watch.AppSchemaCacheFor(lease.Session())
	if access.Client != client || cache != watch.Current().schemas {
		t.Fatal("health access replaced the canonical account/facade cache")
	}

	for range 2 {
		if name, err := access.DisplayName(ctx); err != nil || name != "fixture-health" {
			t.Fatalf("identity=%q error=%v", name, err)
		}
	}

	if profiles != 1 {
		t.Fatalf("one caller fetched %d profiles", profiles)
	}

	next := watch.HealthAccessFor(lease.Session(), client)
	if _, err := next.DisplayName(ctx); err != nil || profiles != 2 {
		t.Fatalf("next caller reused account-lifetime profile: calls=%d error=%v", profiles, err)
	}

	if dx, err := access.DeviceXML(ctx); err != nil || dx.UnitID == 0 {
		t.Fatalf("scoped metadata=%+v error=%v", dx, err)
	}

	if opens != 1 || cache != watch.Current().schemas {
		t.Fatal("health access reopened the watch or replaced its facade cache")
	}
}
