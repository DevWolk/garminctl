package garmin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
)

func TestLegacyFacadeTracksSessionWithoutRetainingRetiredEpochs(t *testing.T) {
	t.Parallel()

	l := NewLazy(func(context.Context) (device.Device, error) { return &leaseDevice{}, nil })
	first := acquireLease(t, l)

	firstSession, firstEpoch := l.Manager().Snapshot()
	if first.Service().session != firstSession || l.Current() != first.Service() {
		t.Fatal("legacy facade and Manager do not share the same Session")
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}

	if err := l.DropAt(first.Epoch()); err != nil {
		t.Fatal(err)
	}

	next := acquireLease(t, l)
	if next.Service() == first.Service() || next.Service().session == firstSession {
		t.Fatal("new connection reused a retired facade/Session")
	}

	// Complete a legacy Current snapshot delayed since the prior epoch. It may
	// return an unleased old facade but must not replace the current cache.
	old := l.facade(firstSession, firstEpoch)
	if old.session != firstSession || l.Current() != next.Service() {
		t.Fatal("late unleased snapshot evicted the current facade")
	}

	if err := next.Release(); err != nil {
		t.Fatal(err)
	}

	closeOwner(t, l)

	_ = l.facade(firstSession, firstEpoch)
	if l.facadeSource != nil || l.facadeValue != nil || l.Current() != nil {
		t.Fatal("closed bridge retained or republished a facade")
	}
}

func TestLegacyAndSessionLeasesShareAdmissionAndMutationCache(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	l := NewLazy(func(context.Context) (device.Device, error) { return device.NewDirFS(root) })

	t.Cleanup(func() { closeOwner(t, l) })

	first := acquireLease(t, l)
	requireLegacyLeaseFile(t, first, "old")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	if blocked, err := l.Manager().Acquire(ctx); blocked != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Session acquired an independent slot: lease=%v error=%v", blocked, err)
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}

	core, err := l.Manager().Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = core.Release() })

	if core.Session() != first.Service().session || core.Epoch() != first.Epoch() || l.Manager().Gate() != l.Gate() {
		t.Fatal("Session and legacy leases do not share one connection/admission")
	}

	if err := core.Session().WriteFile(core.Context(), "file", []byte("new")); err != nil {
		t.Fatal(err)
	}

	if err := core.Release(); err != nil {
		t.Fatal(err)
	}

	next := acquireLease(t, l)
	if next.Service() != first.Service() {
		t.Fatal("same connection created another feature facade")
	}

	requireLegacyLeaseFile(t, next, "new")
}

func TestExtractedAdaptersShareOneEpochSchemaCache(t *testing.T) {
	t.Parallel()

	l := NewLazy(func(context.Context) (device.Device, error) { return &leaseDevice{}, nil })

	t.Cleanup(func() { closeOwner(t, l) })

	core, err := l.Manager().Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	firstSession, firstEpoch := core.Session(), core.Epoch()

	cache := l.AppSchemaCacheFor(firstSession)
	if cache != l.AppSchemaCacheFor(firstSession) || cache != l.Current().schemas {
		t.Fatal("extracted adapter did not use the canonical current-epoch cache")
	}

	if err := core.Release(); err != nil {
		t.Fatal(err)
	}

	legacy := acquireLease(t, l)
	if cache != legacy.Service().schemas {
		t.Fatal("legacy and extracted adapters acquired separate caches")
	}

	if err := legacy.Release(); err != nil {
		t.Fatal(err)
	}

	if err := l.DropAt(firstEpoch); err != nil {
		t.Fatal(err)
	}

	next, err := l.Manager().Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := next.Release(); err != nil {
			t.Error(err)
		}
	}()

	fresh := l.AppSchemaCacheFor(next.Session())
	if fresh == cache || fresh != l.Current().schemas {
		t.Fatal("new Session reused the previous epoch's schema cache")
	}

	_ = l.AppSchemaCacheFor(firstSession)
	if fresh != l.Current().schemas {
		t.Fatal("late state lookup displaced the current epoch's cache")
	}
}

func requireLegacyLeaseFile(t *testing.T, lease *Lease, want string) {
	t.Helper()

	if got, err := lease.Service().ReadCached(lease.Context(), "file"); err != nil || string(got) != want {
		t.Fatalf("leased cached read = %q, %v; want %q", got, err, want)
	}
}
