package connect

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

func testSyncCoordinator(parent context.Context, t *testing.T) (*SyncCoordinator, *lifecycle.Gate) {
	t.Helper()

	gate := new(lifecycle.Gate)
	c := NewSyncCoordinator(parent, gate)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := c.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	return c, gate
}

func TestSyncCoordinatorSharesHTTPConflictAndCompatibleMCPWaiter(t *testing.T) {
	t.Parallel()
	c, gate := testSyncCoordinator(t.Context(), t)
	entered, finish := make(chan struct{}), make(chan struct{})

	var runs atomic.Int32

	options := SyncOptions{Upload: true, Download: true, Epoch: 7}
	run := func(ctx context.Context, progress Progress) (*SyncResult, error) {
		runs.Add(1)
		close(entered)
		progress("one runner")

		select {
		case <-finish:
			return &SyncResult{Pulled: []PulledFile{{Name: "sample"}}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	job, started, err := c.Start(t.Context(), options, run)
	if err != nil || !started {
		t.Fatalf("first start: %v %t", err, started)
	}

	<-entered

	joined := assertSyncCompatibility(t, c, job, options, run)

	waitCtx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := joined.Wait(waitCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}

	if !job.Snapshot().Running || runs.Load() != 1 || gate.Snapshot().Jobs != 1 {
		t.Fatal("waiter cancellation affected shared job or admission")
	}

	if _, err := gate.TryMaintenance(t.Context()); !errors.Is(err, lifecycle.ErrBusy) {
		t.Fatalf("maintenance ignored active job: %v", err)
	}

	close(finish)

	result, err := job.Wait(t.Context())
	if err != nil || result.Pulled[0].Name != "sample" || gate.Snapshot().Jobs != 0 {
		t.Fatalf("job result/admission: %+v %v", result, err)
	}
}

func assertSyncCompatibility(t *testing.T, c *SyncCoordinator, job *SyncJob, options SyncOptions, run SyncRunner) *SyncJob {
	t.Helper()

	if _, _, err := c.Start(t.Context(), options, run); !errors.Is(err, ErrSyncBusy) {
		t.Fatalf("HTTP start joined instead of conflicting: %v", err)
	}

	joined, newJob, err := c.JoinOrStart(t.Context(), options, run)
	if err != nil || newJob || joined != job {
		t.Fatalf("compatible waiter: %v %t same=%t", err, newJob, joined == job)
	}

	for _, other := range []SyncOptions{{Upload: false, Download: true, Epoch: 7}, {Upload: true, Download: false, Epoch: 7}, {Upload: true, Download: true, Epoch: 8}} {
		if _, _, err := c.JoinOrStart(t.Context(), other, run); !errors.Is(err, ErrSyncBusy) {
			t.Fatalf("conflicting waiter joined: %v", err)
		}
	}

	return joined
}

func TestSyncCoordinatorConcurrentStartsRunOnlyOneJob(t *testing.T) {
	t.Parallel()
	c, _ := testSyncCoordinator(t.Context(), t)
	start, finish := make(chan struct{}), make(chan struct{})

	var runs atomic.Int32

	run := func(ctx context.Context, _ Progress) (*SyncResult, error) {
		runs.Add(1)

		select {
		case <-finish:
			return &SyncResult{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	options := SyncOptions{Upload: true, Epoch: 1}

	type result struct {
		job     *SyncJob
		started bool
		err     error
	}

	results := make(chan result, 16)

	for i := range 16 {
		go func() {
			<-start

			begin := c.Start
			if i%2 == 0 {
				begin = c.JoinOrStart
			}

			job, started, err := begin(t.Context(), options, run)
			results <- result{job, started, err}
		}()
	}

	close(start)

	var winner *SyncJob

	starts := 0

	for range 16 {
		r := <-results
		if r.err != nil && !errors.Is(r.err, ErrSyncBusy) {
			t.Fatal(r.err)
		}

		if r.started {
			starts++
			winner = r.job
		}
	}

	if starts != 1 {
		t.Fatalf("concurrent starts=%d", starts)
	}

	close(finish)

	if _, err := winner.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}

	if runs.Load() != 1 {
		t.Fatalf("concurrent runners=%d", runs.Load())
	}
}

func TestSyncJobKeepsItsOwnResultAndDefensiveCopies(t *testing.T) {
	t.Parallel()
	c, _ := testSyncCoordinator(t.Context(), t)

	var lateProgress Progress

	first, _, err := c.Start(t.Context(), SyncOptions{Epoch: 1}, func(_ context.Context, progress Progress) (*SyncResult, error) {
		lateProgress = progress
		progress("first log")

		return &SyncResult{Upload: &UploadResult{Files: []UploadedFile{{Path: "first upload"}}}, Pulled: []PulledFile{{Name: "first download"}}, Errors: []string{"first detail"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := first.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	result.Upload.Files[0].Path = "changed upload"
	result.Pulled[0].Name = "changed download"
	result.Errors[0] = "changed detail"
	view := first.Snapshot()
	view.Log[0] = "changed log"
	view.Result.Pulled[0].Name = "changed snapshot"

	lateProgress("late log ignored")

	secondErr := errors.New("second job failed")

	second, _, err := c.Start(t.Context(), SyncOptions{Epoch: 2}, func(context.Context, Progress) (*SyncResult, error) {
		return &SyncResult{Pulled: []PulledFile{{Name: "second download"}}}, secondErr
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := second.Wait(t.Context()); !errors.Is(err, secondErr) {
		t.Fatal(err)
	}

	old, err := first.Wait(t.Context())
	if err != nil || old.Upload.Files[0].Path != "first upload" || old.Pulled[0].Name != "first download" || old.Errors[0] != "first detail" {
		t.Fatalf("old waiter read mutated/new result: %+v %v", old, err)
	}

	if view := first.Snapshot(); len(view.Log) != 1 || view.Log[0] != "first log" || view.Running {
		t.Fatalf("final snapshot mutated: %+v", view)
	}

	if latest := c.Snapshot(); latest.Result.Pulled[0].Name != "second download" || latest.Error != secondErr.Error() {
		t.Fatalf("latest snapshot: %+v", latest)
	}
}

func TestSyncJobUsesRuntimeLifetimeAndExistingDeadline(t *testing.T) {
	t.Parallel()

	runtimeCtx, stopRuntime := context.WithCancel(context.Background())
	defer stopRuntime()

	c, gate := testSyncCoordinator(runtimeCtx, t)

	callerCtx, stopCaller := context.WithCancel(t.Context())
	defer stopCaller()

	workerContext := make(chan context.Context, 1)

	job, _, err := c.Start(callerCtx, SyncOptions{Epoch: 1}, func(ctx context.Context, _ Progress) (*SyncResult, error) {
		workerContext <- ctx

		<-ctx.Done()

		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := <-workerContext

	stopCaller()

	if ctx.Err() != nil {
		t.Fatal("caller cancellation cancelled runtime job")
	}

	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) < 119*time.Minute || time.Until(deadline) > 2*time.Hour {
		t.Fatal("existing two-hour sync bound was lost")
	}

	stopRuntime()

	if _, err := job.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("runtime cancellation: %v", err)
	}

	if gate.Snapshot().Jobs != 0 {
		t.Fatal("job completion leaked admission")
	}
}

func TestSyncCoordinatorCloseTimeoutDoesNotReleaseRunningOwner(t *testing.T) {
	t.Parallel()
	c, gate := testSyncCoordinator(t.Context(), t)
	entered, finish := make(chan struct{}), make(chan struct{})

	job, _, err := c.Start(t.Context(), SyncOptions{}, func(context.Context, Progress) (*SyncResult, error) {
		close(entered)
		<-finish

		return &SyncResult{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	<-entered

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	if err := c.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unjoined job reported closed: %v", err)
	}

	if _, err := gate.TryMaintenance(t.Context()); !errors.Is(err, lifecycle.ErrBusy) {
		t.Fatalf("timed-out join released job: %v", err)
	}

	close(finish)

	if err := c.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := job.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, _, err := c.Start(t.Context(), SyncOptions{}, nil); !errors.Is(err, lifecycle.ErrStopped) {
		t.Fatalf("closed coordinator restarted: %v", err)
	}
}

func TestSyncCoordinatorRejectedStartsLeakNoAdmission(t *testing.T) {
	t.Parallel()
	c, gate := testSyncCoordinator(t.Context(), t)

	resume, err := gate.TryMaintenance(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := c.Start(t.Context(), SyncOptions{}, nil); !errors.Is(err, lifecycle.ErrMaintenance) {
		t.Fatalf("start during maintenance: %v", err)
	}

	resume()

	if _, _, err := c.Start(t.Context(), SyncOptions{}, nil); err == nil {
		t.Fatal("nil runner accepted")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, _, err := c.Start(ctx, SyncOptions{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled start: %v", err)
	}

	if state := gate.Snapshot(); state.Jobs != 0 || state.Operations != 0 {
		t.Fatalf("rejected start leaked permit: %+v", state)
	}
}
