package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestMaintenanceAndWorkHaveOneAdmissionDecision(t *testing.T) {
	t.Parallel()

	for _, job := range []bool{false, true} {
		t.Run(map[bool]string{false: "operation", true: "job"}[job], func(t *testing.T) {
			t.Parallel()

			for range 100 {
				var gate Gate

				start := make(chan struct{})

				type result struct {
					release func()
					err     error
				}

				work, maintenance := make(chan result, 1), make(chan result, 1)

				go func() {
					<-start

					enter := gate.Enter
					if job {
						enter = gate.EnterJob
					}

					release, err := enter(t.Context())
					work <- result{release, err}
				}()
				go func() { <-start; release, err := gate.TryMaintenance(t.Context()); maintenance <- result{release, err} }()

				close(start)

				w, m := <-work, <-maintenance
				// Keep the winning reservation alive until both contenders return.
				if w.err == nil {
					if !errors.Is(m.err, ErrBusy) {
						t.Fatalf("maintenance admitted beside work: %v", m.err)
					}

					w.release()
				} else {
					if !errors.Is(w.err, ErrMaintenance) || m.err != nil {
						t.Fatalf("work=%v maintenance=%v", w.err, m.err)
					}

					m.release()
				}

				if got := gate.Snapshot(); got != (Snapshot{State: Running}) {
					t.Fatalf("reservation leaked: %+v", got)
				}
			}
		})
	}
}

func TestQueuedOperationsAndJobsPreventMaintenanceAndJoinShutdown(t *testing.T) {
	t.Parallel()

	var gate Gate

	active, err := gate.Enter(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	queued, err := gate.Enter(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	job, err := gate.EnterJob(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := gate.TryMaintenance(t.Context()); !errors.Is(err, ErrBusy) {
		t.Fatalf("maintenance with reserved work: %v", err)
	}

	gate.Stop()
	active()
	queued()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	if err := gate.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown did not await registered job: %v", err)
	}

	if got := gate.Snapshot(); got != (Snapshot{State: Stopping, Jobs: 1}) {
		t.Fatalf("premature drain: %+v", got)
	}

	job()

	if err := gate.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}

	if got := gate.Snapshot(); got != (Snapshot{State: Closed}) {
		t.Fatalf("drained state: %+v", got)
	}
}

func TestReleaseIsConcurrentAndIdempotent(t *testing.T) {
	t.Parallel()

	var gate Gate

	release, err := gate.Enter(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	var workers sync.WaitGroup
	for range 16 {
		workers.Go(release)
	}

	workers.Wait()

	resume, err := gate.TryMaintenance(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	for range 16 {
		workers.Go(resume)
	}

	workers.Wait()

	if got := gate.Snapshot(); got != (Snapshot{State: Running}) {
		t.Fatalf("duplicate release changed counts/state: %+v", got)
	}
}

func TestMaintenanceResumeCannotReopenStoppedGate(t *testing.T) {
	t.Parallel()

	var gate Gate

	resume, err := gate.TryMaintenance(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := gate.Enter(t.Context()); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("operation admitted during maintenance: %v", err)
	}

	if _, err := gate.EnterJob(t.Context()); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("job admitted during maintenance: %v", err)
	}

	gate.Stop()
	resume()
	resume()

	for _, enter := range []func(context.Context) (func(), error){gate.Enter, gate.EnterJob, gate.TryMaintenance} {
		if _, err := enter(t.Context()); !errors.Is(err, ErrStopped) {
			t.Fatalf("stopped Gate reopened: %v", err)
		}
	}

	if err := gate.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}

	if got := gate.Snapshot(); got != (Snapshot{State: Closed}) {
		t.Fatalf("late resume: %+v", got)
	}
}

func TestCancelledAdmissionLeavesNoPermit(t *testing.T) {
	t.Parallel()

	var gate Gate

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	for _, enter := range []func(context.Context) (func(), error){gate.Enter, gate.EnterJob, gate.TryMaintenance} {
		if _, err := enter(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled admission: %v", err)
		}
	}

	resume, err := gate.TryMaintenance(t.Context())
	if err != nil {
		t.Fatalf("cancel leaked a reservation: %v", err)
	}

	resume()
}

func TestWaitRequiresStopAndMaintenanceCanResume(t *testing.T) {
	t.Parallel()

	var gate Gate
	if err := gate.Wait(t.Context()); !errors.Is(err, ErrNotStopped) {
		t.Fatalf("waiting while new work is allowed: %v", err)
	}

	resume, err := gate.TryMaintenance(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if err := gate.Wait(t.Context()); !errors.Is(err, ErrNotStopped) {
		t.Fatalf("maintenance mistaken for shutdown: %v", err)
	}

	resume()

	release, err := gate.EnterJob(t.Context())
	if err != nil {
		t.Fatalf("maintenance did not resume admission: %v", err)
	}

	release()
	// An old owner's repeated cleanup cannot release a new maintenance ticket.
	newResume, err := gate.TryMaintenance(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	resume()

	if _, err := gate.Enter(t.Context()); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("old cleanup released new maintenance: %v", err)
	}

	newResume()
}
