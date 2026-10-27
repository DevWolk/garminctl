package garmin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
)

func managedActivityTest(t *testing.T, count int) (*ActivityCoordinator, *activityBarrierDevice, []device.Entry) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	dev, entries := activityTestDevice(t, count)
	watch := NewLazy(func(context.Context) (device.Device, error) { return dev, nil })
	decoder := NewActivityCoordinator(t.Context(), watch)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()

		if err := decoder.Close(ctx); err != nil {
			t.Error(err)
		}

		if err := watch.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	return decoder, dev, entries
}

func blockedActivityJob(t *testing.T, c *ActivityCoordinator, dev *activityBarrierDevice, entry device.Entry, gate <-chan struct{}) *activityDecodeJob {
	t.Helper()

	dev.gate, dev.blocked = gate, "GARMIN/Activity/"+entry.Name

	out, err := c.Activities(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}

	if out.Pending == 0 {
		t.Fatal("activity was not scheduled")
	}

	waitActivityBarrier(t, dev.entered)
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, job := range c.jobs {
		return job
	}

	t.Fatal("blocked decoder has no job owner")

	return nil
}

func activityJobError(t *testing.T, job *activityDecodeJob) error {
	t.Helper()

	select {
	case <-job.done:
		return job.err
	case <-time.After(5 * time.Second):
		t.Fatal("decoder did not stop")
	}

	return nil
}

func TestManagedActivitiesYieldBetweenFiles(t *testing.T) { //nolint:paralleltest // helper isolates process-wide cache environment
	c, dev, entries := managedActivityTest(t, 2)

	gate := make(chan struct{})
	defer close(gate)

	dev.trace = make(chan string, 3)
	job := blockedActivityJob(t, c, dev, entries[0], gate)
	done := make(chan error, 1)

	go func() {
		lease, err := c.watch.Acquire(t.Context())
		if err != nil {
			done <- err
			return
		}

		dev.trace <- "independent operation"

		done <- lease.Release()
	}()

	waitActivityOperations(t, c.watch, 2)

	gate <- struct{}{}

	awaitActivityResult(t, done)

	if err := activityJobError(t, job); err != nil {
		t.Fatal(err)
	}

	first, second, third := <-dev.trace, <-dev.trace, <-dev.trace
	if first != "GARMIN/Activity/"+entries[0].Name || second != "independent operation" || third != "GARMIN/Activity/"+entries[1].Name {
		t.Fatalf("operation did not run between file groups: %q %q %q", first, second, third)
	}

	if snapshot := c.watch.Gate().Snapshot(); snapshot.Jobs != 0 || snapshot.Operations != 0 {
		t.Fatalf("finished decoder leaked admission: %+v", snapshot)
	}
}

func waitActivityOperations(t *testing.T, watch *Lazy, want int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()

	for watch.Gate().Snapshot().Operations != want {
		select {
		case <-ctx.Done():
			t.Fatal("operation did not reserve its lease")
		case <-tick.C:
		}
	}
}

func TestManagedActivitiesWaiterCancellationKeepsJob(t *testing.T) { //nolint:paralleltest // helper isolates process-wide cache environment
	c, dev, entries := managedActivityTest(t, 1)

	gate := make(chan struct{})
	defer close(gate)

	job := blockedActivityJob(t, c, dev, entries[0], gate)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	if _, err := c.Activities(ctx, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter cancellation: %v", err)
	}

	if c.watch.Gate().Snapshot().Jobs != 1 {
		t.Fatal("waiter released decoder ownership")
	}

	gate <- struct{}{}

	if err := activityJobError(t, job); err != nil {
		t.Fatal(err)
	}

	out, err := c.Activities(t.Context(), true)
	if err != nil || out.Pending != 0 || len(out.Activities) != 1 {
		t.Fatalf("completed result: %+v, %v", out, err)
	}
}

func TestManagedActivitiesStopOnEpochChange(t *testing.T) { //nolint:paralleltest // helper isolates process-wide cache environment
	c, dev, entries := managedActivityTest(t, 1)

	gate := make(chan struct{})
	defer close(gate)

	job := blockedActivityJob(t, c, dev, entries[0], gate)
	c.mu.Lock()

	var epoch ConnectionEpoch
	for key := range c.jobs {
		epoch = key.epoch
	}
	c.mu.Unlock()

	if err := c.watch.DropAt(epoch); err != nil {
		t.Fatal(err)
	}

	err := activityJobError(t, job)
	if !errors.Is(err, ErrSessionChanged) || !errors.Is(err, device.ErrDisconnected) {
		t.Fatalf("changed epoch was not reported: %v", err)
	}

	if c.watch.Current() != nil {
		t.Fatal("decoder opened a replacement connection")
	}
}

func TestManagedActivitiesShutdownRetainsUnfinishedJob(t *testing.T) { //nolint:paralleltest // helper isolates process-wide cache environment
	c, dev, entries := managedActivityTest(t, 1)

	gate := make(chan struct{})
	defer close(gate)

	dev.ignoreCancel = true
	job := blockedActivityJob(t, c, dev, entries[0], gate)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	if err := c.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unfinished shutdown: %v", err)
	}

	if snapshot := c.watch.Gate().Snapshot(); snapshot.Jobs != 1 || snapshot.Operations != 1 {
		t.Fatalf("unfinished decoder ownership lost: %+v", snapshot)
	}

	gate <- struct{}{}

	if err := activityJobError(t, job); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled job: %v", err)
	}

	if err := c.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	if c.watch.Gate().Snapshot().Jobs != 0 {
		t.Fatal("shutdown leaked job ownership")
	}
}
