package native_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
	applicationnative "github.com/DevWolk/garminctl/internal/containers/application/ui/native"
)

type nativeRollbackReader struct {
	installReader
	prepare func(context.Context, application.RollbackRequest) (applicationnative.RollbackPreparation, error)
	rows    []application.RollbackOption
}

func (r *nativeRollbackReader) RetainedRollbacks(context.Context, bool) ([]application.RollbackOption, error) {
	return r.rows, nil
}

func (r *nativeRollbackReader) PrepareRollback(ctx context.Context, request application.RollbackRequest) (applicationnative.RollbackPreparation, error) {
	return r.prepare(ctx, request)
}

type nativeRollbackPrepared struct {
	view  application.RollbackView
	run   func(context.Context, application.RollbackConsent) (*application.RollbackResult, error)
	close func() error
	// preparing receives the context that owns the preparation until Close.
	preparing func(context.Context)
}

// rollbackWindow adds the close postponement a native window lends to confirmed work.
type rollbackWindow struct {
	*installWindow
	holds   atomic.Int32
	refusal error
	// leased records a hold that ended while the operation still held its Scope lease.
	leased atomic.Bool
}

func (w *rollbackWindow) HoldClose() (func(), error) {
	if w.refusal != nil {
		return nil, w.refusal
	}

	w.holds.Add(1)

	return sync.OnceFunc(func() {
		w.leased.Store(w.active.Load() != 0)
		w.holds.Add(-1)
	}), nil
}

func (p *nativeRollbackPrepared) View() application.RollbackView { return p.view }
func (p *nativeRollbackPrepared) Run(ctx context.Context, c application.RollbackConsent) (*application.RollbackResult, error) {
	return p.run(ctx, c)
}
func (p *nativeRollbackPrepared) Close() error { return p.close() }

func nativeRollbackFixture(t *testing.T, p *nativeRollbackPrepared) (*applicationnative.Service, *installWindow) {
	t.Helper()

	s, w := nativeRollbackWindowFixture(t, p)

	return s, w.installWindow
}

func nativeRollbackWindowFixture(t *testing.T, p *nativeRollbackPrepared) (*applicationnative.Service, *rollbackWindow) {
	t.Helper()
	w := &rollbackWindow{installWindow: &installWindow{current: t.Context, owned: true, exited: make(chan struct{})}}
	r := &nativeRollbackReader{prepare: func(ctx context.Context, _ application.RollbackRequest) (applicationnative.RollbackPreparation, error) {
		if p.preparing != nil {
			p.preparing(ctx)
		}

		return p, nil
	}}
	r.install = func(context.Context, application.PreflightRequest, application.InstallConsent, func(string)) (*application.InstallHandoffResult, error) {
		return nil, errors.New("unexpected second installation")
	}
	s := applicationnative.NewService(w, func(string, bool) (applicationnative.Reader, error) { return r, nil })

	return s, w
}

func nativeRollbackReview() application.RollbackView {
	return application.RollbackView{Data: application.DataRestoreView{ReviewID: strings.Repeat("a", 64), TargetSnapshotID: strings.Repeat("b", 64), SafetySnapshotID: strings.Repeat("c", 64), From: "v2.0.0", To: "v1.0.0", Changes: []application.RestoreChange{{Area: "config", Path: "garmin-connect.json", Operation: "replace", Bytes: 12}}}}
}

func waitRollbackReview(t *testing.T, s *applicationnative.Service) *applicationnative.InstallationState {
	t.Helper()

	bound, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		state, err := s.InstallationStatus()
		if err != nil {
			t.Fatal(err)
		}

		if state.Phase == "review" {
			return state
		}

		select {
		case <-bound.Done():
			t.Fatalf("review not ready: %+v", state)
		case <-ticker.C:
		}
	}
}

func nativeRollbackConsent(review *application.DataRestoreView) application.RollbackConsent {
	return application.RollbackConsent{Installation: application.InstallConsent{Confirmed: true, EditsResolved: true}, Data: application.RestoreConsent{Confirmed: true, EditsResolved: true, ReviewID: review.ReviewID}}
}

func TestNativeRollbackWaitsForExactReviewAndReleasesBeforeExit(t *testing.T) {
	t.Parallel()

	var runs, closes atomic.Int32

	p := &nativeRollbackPrepared{view: nativeRollbackReview(), close: func() error { closes.Add(1); return nil }, run: func(_ context.Context, _ application.RollbackConsent) (*application.RollbackResult, error) {
		runs.Add(1)
		return &application.RollbackResult{Handoff: application.InstallHandoffResult{From: "v2.0.0", To: "v1.0.0", State: application.HandoffAccepted}, Data: &application.DataRestoreResult{Completed: true, Replaced: 1}}, nil
	}}
	s, w := nativeRollbackFixture(t, p)

	initial, err := s.StartRollback(application.RollbackRequest{})
	if err != nil {
		t.Fatal(err)
	}

	state := assertNativeRollbackReview(t, s, w, &runs, initial.ID)
	if _, err := s.ConfirmRollback(initial.ID, nativeRollbackConsent(state.Review)); err != nil {
		t.Fatal(err)
	}

	select {
	case <-w.exited:
	case <-time.After(time.Second):
		t.Fatal("rollback did not exit")
	}

	if w.tooEarly.Load() || closes.Load() != 1 || runs.Load() != 1 {
		t.Fatalf("exit retained scope or preparation: closes=%d runs=%d early=%v", closes.Load(), runs.Load(), w.tooEarly.Load())
	}

	if _, err := s.ConfirmRollback(initial.ID, nativeRollbackConsent(state.Review)); err == nil {
		t.Fatal("confirmation replayed")
	}
}

func TestNativeRollbackCancelJoinsReviewCleanup(t *testing.T) {
	t.Parallel()

	cleanup := make(chan struct{})
	finish := sync.OnceFunc(func() { close(cleanup) })
	t.Cleanup(finish)

	p := &nativeRollbackPrepared{view: nativeRollbackReview(), run: func(context.Context, application.RollbackConsent) (*application.RollbackResult, error) {
		t.Error("canceled review ran installer")
		return nil, nil
	}, close: func() error { <-cleanup; return nil }}
	s, w := nativeRollbackFixture(t, p)

	first, err := s.StartRollback(application.RollbackRequest{})
	if err != nil {
		t.Fatal(err)
	}

	waitRollbackReview(t, s)

	state, err := s.CancelInstallation(first.ID)
	if err != nil || !state.Active || state.Phase != installPhaseCanceling {
		t.Fatalf("cancel=%+v %v", state, err)
	}

	if _, err := s.StartRollback(application.RollbackRequest{}); err == nil || w.active.Load() != 1 {
		t.Fatal("cleanup released slot early")
	}

	finish()

	if state := waitInstallation(t, s); state.Phase != "canceled" || w.active.Load() != 0 {
		t.Fatalf("cleanup did not join: %+v scope=%d", state, w.active.Load())
	}
}

func assertNativeRollbackReview(t *testing.T, s *applicationnative.Service, w *installWindow, runs *atomic.Int32, id uint64) *applicationnative.InstallationState {
	t.Helper()

	state := waitRollbackReview(t, s)
	if state.Kind != "rollback" || !state.Active || !state.CanCancel || state.Review == nil || runs.Load() != 0 || w.active.Load() != 1 {
		t.Fatalf("review=%+v runs=%d active=%d", state, runs.Load(), w.active.Load())
	}

	state.Review.Changes[0].Path = "review-caller-change"

	state = waitRollbackReview(t, s)
	if state.Review.Changes[0].Path == "review-caller-change" {
		t.Fatal("review aliases service state")
	}

	if _, err := s.StartInstallation(installationRequest(), installationConsent()); err == nil {
		t.Fatal("installer bypassed rollback slot")
	}

	bad := nativeRollbackConsent(state.Review)

	bad.Data.ReviewID = "other"
	if value, err := s.ConfirmRollback(id, bad); err == nil || value != nil || runs.Load() != 0 {
		t.Fatal("wrong review confirmed")
	}

	return state
}

func TestNativeRollbackWindowCloseCancelsReviewAndJoinsCleanup(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(t.Context())
	defer cancel()

	cleanup, entered := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(cleanup) })
	t.Cleanup(finish)

	p := &nativeRollbackPrepared{view: nativeRollbackReview(), run: func(context.Context, application.RollbackConsent) (*application.RollbackResult, error) {
		t.Error("window cancellation ran rollback")
		return nil, nil
	}, close: func() error { close(entered); <-cleanup; return nil }}
	s, w := nativeRollbackFixture(t, p)
	w.current = func() context.Context { return parent }

	if _, err := s.StartRollback(application.RollbackRequest{}); err != nil {
		t.Fatal(err)
	}

	waitRollbackReview(t, s)
	cancel()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("window cancellation did not close preparation")
	}

	if w.active.Load() != 1 {
		t.Fatal("window scope released before preparation joined")
	}

	finish()

	bound, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for w.active.Load() != 0 {
		select {
		case <-bound.Done():
			t.Fatal("window scope did not join")
		case <-ticker.C:
		}
	}

	select {
	case <-w.exited:
		t.Fatal("canceled review requested installer exit")
	default:
	}
}

func TestNativeRollbackRefusesReusedOwnerAndUnconfirmedReview(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"reused", "install consent", "data consent", "unresolved installation edits", "stale operation"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := &nativeRollbackPrepared{view: nativeRollbackReview(), run: func(context.Context, application.RollbackConsent) (*application.RollbackResult, error) {
				t.Error("refused review ran rollback")
				return nil, nil
			}, close: func() error { return nil }}

			s, w := nativeRollbackFixture(t, p)
			if name == "reused" {
				w.owned = false
				if value, err := s.StartRollback(application.RollbackRequest{}); value != nil || err == nil || w.active.Load() != 0 {
					t.Fatalf("reused owner admitted=%+v %v", value, err)
				}

				return
			}

			first, err := s.StartRollback(application.RollbackRequest{})
			if err != nil {
				t.Fatal(err)
			}

			state := waitRollbackReview(t, s)
			consent := nativeRollbackConsent(state.Review)
			id := first.ID

			switch name {
			case "install consent":
				consent.Installation.Confirmed = false
			case "data consent":
				consent.Data.Confirmed = false
			case "unresolved installation edits":
				consent.Installation.EditsResolved = false
			case "stale operation":
				id++
			}

			if value, err := s.ConfirmRollback(id, consent); value != nil || err == nil {
				t.Fatalf("unconfirmed rollback=%+v %v", value, err)
			}

			if _, err := s.CancelInstallation(first.ID); err != nil {
				t.Fatal(err)
			}

			waitInstallation(t, s)
		})
	}
}

func TestNativeRollbackConfirmedWorkOutlivesAWindowCloseRequest(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		window, closeWindow := context.WithCancel(t.Context())
		defer closeWindow()

		// Each reports whether the context lent to that phase has ended.
		var preparation, work func() error

		running, finished := make(chan struct{}), make(chan struct{})
		finish := sync.OnceFunc(func() { close(finished) })

		defer finish()

		p := &nativeRollbackPrepared{view: nativeRollbackReview(), close: func() error { return nil }, preparing: func(ctx context.Context) { preparation = ctx.Err }, run: func(ctx context.Context, _ application.RollbackConsent) (*application.RollbackResult, error) {
			work = ctx.Err

			close(running)
			<-finished // stands for owned stop, data restore and installer commit

			return &application.RollbackResult{Handoff: application.InstallHandoffResult{State: application.HandoffAccepted}, Data: &application.DataRestoreResult{Attempted: true, Completed: true, Replaced: 1}}, ctx.Err()
		}}
		s, w := nativeRollbackWindowFixture(t, p)
		w.current = func() context.Context { return window }

		initial, err := s.StartRollback(application.RollbackRequest{})
		if err != nil {
			t.Fatal(err)
		}

		synctest.Wait()

		review, err := s.InstallationStatus()
		if err != nil || review.Phase != "review" || w.holds.Load() != 0 {
			t.Fatalf("review=%+v holds=%d %v", review, w.holds.Load(), err)
		}

		if _, err := s.ConfirmRollback(initial.ID, nativeRollbackConsent(review.Review)); err != nil {
			t.Fatal(err)
		}

		<-running

		if w.holds.Load() != 1 {
			t.Fatal("confirmed work did not postpone window close")
		}

		closeWindow()
		synctest.Wait()

		if work() != nil || preparation() != nil {
			t.Fatalf("window close reached confirmed work: work=%v preparation=%v", work(), preparation())
		}

		finish()
		<-w.exited
		synctest.Wait()

		if w.tooEarly.Load() || w.holds.Load() != 0 || w.leased.Load() {
			t.Fatalf("exit or close hold preceded Scope release: early=%v holds=%d leased=%v", w.tooEarly.Load(), w.holds.Load(), w.leased.Load())
		}
	})
}

func TestNativeRollbackWorkDeadlineStartsAtConfirmation(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var budget time.Duration

		p := &nativeRollbackPrepared{view: nativeRollbackReview(), close: func() error { return nil }, run: func(ctx context.Context, _ application.RollbackConsent) (*application.RollbackResult, error) {
			if deadline, ok := ctx.Deadline(); ok {
				budget = time.Until(deadline)
			}

			select {
			case <-ctx.Done():
			case <-time.After(time.Minute): // stop, restore and commit outlast the review deadline
			}

			return &application.RollbackResult{Handoff: application.InstallHandoffResult{State: application.HandoffAccepted}}, ctx.Err()
		}}
		s, w := nativeRollbackFixture(t, p)

		initial, err := s.StartRollback(application.RollbackRequest{})
		if err != nil {
			t.Fatal(err)
		}

		synctest.Wait()

		review, err := s.InstallationStatus()
		if err != nil || review.Phase != "review" {
			t.Fatalf("review=%+v %v", review, err)
		}

		time.Sleep(25*time.Minute - 2*time.Second) // the user reads the review

		if _, err := s.ConfirmRollback(initial.ID, nativeRollbackConsent(review.Review)); err != nil {
			t.Fatal(err)
		}

		<-w.exited
		synctest.Wait()

		final, err := s.InstallationStatus()
		if err != nil || final.Result == nil || final.Result.State != application.HandoffAccepted || final.Error != "" || budget != 25*time.Minute {
			t.Fatalf("late confirmation ran on the review deadline: budget=%v final=%+v %v", budget, final, err)
		}
	})
}

func TestNativeRollbackLeavesDataAloneWhenTheWindowCannotBeHeld(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"already closing", "no postponement"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := &nativeRollbackPrepared{view: nativeRollbackReview(), close: func() error { return nil }, run: func(context.Context, application.RollbackConsent) (*application.RollbackResult, error) {
				t.Error("rollback ran without holding the window open")
				return nil, nil
			}}

			if name == "no postponement" {
				w := &installWindow{current: t.Context, owned: true, exited: make(chan struct{})}
				s := applicationnative.NewService(w, func(string, bool) (applicationnative.Reader, error) { return new(nativeRollbackReader), nil })

				if value, err := s.StartRollback(application.RollbackRequest{}); value != nil || err == nil || w.active.Load() != 0 {
					t.Fatalf("rollback admitted=%+v %v", value, err)
				}

				return
			}

			s, w := nativeRollbackWindowFixture(t, p)
			w.refusal = context.Canceled

			initial, err := s.StartRollback(application.RollbackRequest{})
			if err != nil {
				t.Fatal(err)
			}

			review := waitRollbackReview(t, s)
			if _, err := s.ConfirmRollback(initial.ID, nativeRollbackConsent(review.Review)); err != nil {
				t.Fatal(err)
			}

			if state := waitInstallation(t, s); state.Active || state.StopRequested || state.Restore != nil {
				t.Fatalf("state=%+v", state)
			}

			select {
			case <-w.exited:
				t.Fatal("refused rollback requested installer exit")
			default:
			}
		})
	}
}
