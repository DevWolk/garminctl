package watchsession

import (
	"context"
	"errors"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/device"
)

type cacheDevice struct {
	device.Device
	sample func() int
	reads  int
	err    error
}

func (d *cacheDevice) Reconnects() int {
	if d.sample != nil {
		return d.sample()
	}

	return 0
}

func (d *cacheDevice) Read(context.Context, string) ([]byte, error) {
	d.reads++
	return []byte("data"), d.err
}

func TestReconnectSamplingDoesNotHoldCacheMutex(t *testing.T) {
	t.Parallel()

	d := &cacheDevice{}
	sess := New(d)
	d.sample = func() int {
		if !sess.cacheMu.TryLock() {
			t.Error("sampling the transport while holding the cache mutex violates lock ordering")
			return 0
		}

		sess.cacheMu.Unlock()

		return 0
	}

	for range 2 {
		if _, err := sess.ReadThrough(t.Context(), "file", true); err != nil {
			t.Fatal(err)
		}
	}

	if _, current := sess.CacheGeneration(); !current {
		t.Fatal("current reconnect observation rejected")
	}
}

func TestCacheGenerationRejectsOutOfOrderReconnectObservation(t *testing.T) {
	t.Parallel()

	count := 2
	d := &cacheDevice{sample: func() int { return count }}
	sess := New(d)

	first, current := sess.CacheGeneration()
	if !current {
		t.Fatal("first observation rejected")
	}

	count = 1 // a sample taken before another caller observed reconnect 2

	if generation, valid := sess.CacheGeneration(); valid || generation != first {
		t.Fatalf("out-of-order sample: generation=%d current=%v", generation, valid)
	}

	count = 2

	if generation, valid := sess.CacheGeneration(); !valid || generation != first {
		t.Fatalf("generation advanced twice for the same reconnect: %d, %v", generation, valid)
	}
}

func TestFileCacheInvalidationScopeAndErrors(t *testing.T) {
	t.Parallel()

	d := &cacheDevice{}
	sess := New(d)
	read := func(p string) {
		t.Helper()

		if _, err := sess.ReadThrough(t.Context(), p, true); err != nil {
			t.Fatal(err)
		}
	}
	read("FileA")
	read("filea")
	read("FileB")
	sess.Invalidate("FILEA")
	read("fileb")
	read("filea")

	if d.reads != 3 {
		t.Fatalf("case-insensitive path invalidation changed scope: reads=%d", d.reads)
	}

	sess.Refresh()

	d.err = device.ErrDisconnected
	if _, err := sess.ReadThrough(t.Context(), "filea", true); !errors.Is(err, device.ErrDisconnected) || err.Error() != "read filea: watch disconnected" {
		t.Fatalf("read error contract changed: %v", err)
	}

	d.err = nil

	read("filea")

	if d.reads != 5 {
		t.Fatalf("failed read entered cache: reads=%d", d.reads)
	}
}
