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

const (
	installTestTag        = "v4.5.6"
	installStopFailed     = "stop failed"
	installPhaseCanceling = "canceling"
)

type installWindow struct {
	current  func() context.Context
	owned    bool
	active   atomic.Int32
	exited   chan struct{}
	tooEarly atomic.Bool
}

func (w *installWindow) Acquire() (context.Context, func(), error) {
	if err := w.current().Err(); err != nil {
		return nil, nil, err
	}

	w.active.Add(1)

	return w.current(), sync.OnceFunc(func() { w.active.Add(-1) }), nil
}

func (w *installWindow) Backend() (string, bool, error) { return "http://127.0.0.1:8787", w.owned, nil }

func (w *installWindow) ExitForUpdate() { w.tooEarly.Store(w.active.Load() != 0); close(w.exited) }

type installReader struct {
	downloadReader
	install func(context.Context, application.PreflightRequest, application.InstallConsent, func(string)) (*application.InstallHandoffResult, error)
}

func (r *installReader) Install(ctx context.Context, req application.PreflightRequest, consent application.InstallConsent, p func(string)) (*application.InstallHandoffResult, error) {
	return r.install(ctx, req, consent, p)
}

func installationFixture(t *testing.T, r *installReader) (*applicationnative.Service, *installWindow) {
	t.Helper()
	w := &installWindow{current: t.Context, owned: true, exited: make(chan struct{})}
	r.run = func(context.Context, application.DownloadRequest, func(application.DownloadProgress)) (*application.DownloadResult, error) {
		return nil, errors.New("unexpected concurrent download")
	}
	s := applicationnative.NewService(w, func(string, bool) (applicationnative.Reader, error) { return r, nil })

	return s, w
}

func installationRequest() application.PreflightRequest {
	return application.PreflightRequest{Tag: installTestTag, CacheID: "retained-id"}
}

func installationConsent() application.InstallConsent {
	return application.InstallConsent{Confirmed: true, EditsResolved: true}
}

func waitInstallation(t *testing.T, s *applicationnative.Service) *applicationnative.InstallationState {
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

		if !state.Active {
			return state
		}

		select {
		case <-bound.Done():
			t.Fatal("installation did not join")
		case <-ticker.C:
		}
	}
}

func TestNativeInstallRequiresConsentAndOwnedBackend(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"no consent", "unsaved edits", "reused backend"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			calls := atomic.Int32{}
			r := &installReader{install: func(context.Context, application.PreflightRequest, application.InstallConsent, func(string)) (*application.InstallHandoffResult, error) {
				calls.Add(1)
				return nil, nil
			}}
			s, w := installationFixture(t, r)
			consent := installationConsent()

			switch name {
			case "no consent":
				consent.Confirmed = false
			case "unsaved edits":
				consent.EditsResolved = false
			case "reused backend":
				w.owned = false
			}

			if value, err := s.StartInstallation(installationRequest(), consent); err == nil || value != nil || calls.Load() != 0 || w.active.Load() != 0 {
				t.Fatalf("admitted=%+v %v calls=%d", value, err, calls.Load())
			}
		})
	}
}

func TestNativeInstallCancelRetainsOwnershipUntilCleanup(t *testing.T) {
	t.Parallel()

	entered, cleanup := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(cleanup) })
	t.Cleanup(finish)

	r := &installReader{install: func(ctx context.Context, req application.PreflightRequest, _ application.InstallConsent, p func(string)) (*application.InstallHandoffResult, error) {
		if req != installationRequest() {
			return nil, errors.New("request changed")
		}

		p("backing-up")
		close(entered)
		<-ctx.Done()
		<-cleanup

		return nil, ctx.Err()
	}}
	s, w := installationFixture(t, r)

	first, err := s.StartInstallation(installationRequest(), installationConsent())
	if err != nil {
		t.Fatal(err)
	}

	<-entered

	canceled, err := s.CancelInstallation(first.ID)
	if err != nil || !canceled.Active || canceled.Phase != installPhaseCanceling {
		t.Fatalf("cancel=%+v %v", canceled, err)
	}

	if _, err := s.StartInstallation(installationRequest(), installationConsent()); err == nil {
		t.Fatal("second installer admitted before cleanup")
	}

	if _, err := s.StartDownload(installTestTag, false); err == nil {
		t.Fatal("download admitted before installer cleanup")
	}

	if w.active.Load() != 1 {
		t.Fatal("window scope released before cleanup")
	}

	finish()

	if state := waitInstallation(t, s); state.Phase != "canceled" || state.Result != nil {
		t.Fatalf("terminal=%+v", state)
	}

	select {
	case <-w.exited:
		t.Fatal("canceled preparation quit window")
	default:
	}
}

func TestNativeInstallExitsAfterReleaseEvenForUncertainCommit(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"accepted", "lost acknowledgement", installStopFailed} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			entered, finish := make(chan struct{}), make(chan struct{})
			release := sync.OnceFunc(func() { close(finish) })
			t.Cleanup(release)

			r := &installReader{install: func(_ context.Context, _ application.PreflightRequest, _ application.InstallConsent, p func(string)) (*application.InstallHandoffResult, error) {
				p("handoff")
				close(entered)
				<-finish

				return installationOutcome(name)
			}}
			s, w := installationFixture(t, r)

			first, err := s.StartInstallation(installationRequest(), installationConsent())
			if err != nil {
				t.Fatal(err)
			}

			<-entered

			if _, err := s.CancelInstallation(first.ID); err == nil {
				t.Fatal("handoff was offered cancellation")
			}

			release()

			if name != installStopFailed {
				select {
				case <-w.exited:
				case <-time.After(time.Second):
					t.Fatal("window did not exit for committed helper")
				}
			}

			state := waitInstallation(t, s)
			if _, err := s.StartInstallation(installationRequest(), installationConsent()); err == nil {
				t.Fatal("stopped or uncertain handoff replayed")
			}

			assertInstallationOutcomePhase(t, name, state)

			if _, err := s.StartDownload("v1.3.0", false); err == nil {
				t.Fatal("new download admitted after stop")
			}

			if name == installStopFailed {
				select {
				case <-w.exited:
					t.Fatal("window quit without commit")
				default:
				}

				return
			}

			if w.tooEarly.Load() {
				t.Fatal("window exit held its own operation scope")
			}

			state.Result.To = "mutated"

			current, err := s.InstallationStatus()
			if err != nil || current.Result.To != installTestTag {
				t.Fatalf("aliased state=%+v %v", current, err)
			}
		})
	}
}

// assertInstallationOutcomePhase keeps a lost acknowledgement apart from an accepted handoff.
func assertInstallationOutcomePhase(t *testing.T, name string, state *applicationnative.InstallationState) {
	t.Helper()

	want, failed := name, false

	switch name {
	case "lost acknowledgement":
		want, failed = "uncertain", true
	case installStopFailed:
		want, failed = "failed", true
	}

	if state.Phase != want || (state.Error != "") != failed || !state.StopRequested {
		t.Fatalf("terminal state=%+v, want phase %s", state, want)
	}
}

func installationOutcome(name string) (*application.InstallHandoffResult, error) {
	value := &application.InstallHandoffResult{From: "v1.0.0", To: installTestTag, State: application.HandoffAccepted}

	switch name {
	case "lost acknowledgement":
		value.State = application.HandoffCommitAttempted
		return value, errors.New("reply lost")
	case installStopFailed:
		value.State = application.HandoffStopRequested
		return value, errors.New(installStopFailed)
	default:
		return value, nil
	}
}

func TestNativeInstallReportsExpiredPreparationInOneSentence(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		r := &installReader{install: func(ctx context.Context, _ application.PreflightRequest, _ application.InstallConsent, p func(string)) (*application.InstallHandoffResult, error) {
			p("backing-up")
			<-ctx.Done()
			// Each layer below joins the same expired deadline to its own error.
			return nil, errors.Join(ctx.Err(), ctx.Err(), ctx.Err(), ctx.Err())
		}}
		s, w := installationFixture(t, r)

		if _, err := s.StartInstallation(installationRequest(), installationConsent()); err != nil {
			t.Fatal(err)
		}

		time.Sleep(26 * time.Minute)
		synctest.Wait()

		state, err := s.InstallationStatus()
		if err != nil || state.Active || state.Phase != "failed" {
			t.Fatalf("expired preparation=%+v %v", state, err)
		}

		if strings.Contains(state.Error, "context deadline exceeded") || strings.Contains(state.Error, "\n") || !strings.Contains(state.Error, "ran out of time") || !strings.Contains(state.Error, "nothing was installed") {
			t.Fatalf("expired preparation is shown as %q", state.Error)
		}

		select {
		case <-w.exited:
			t.Fatal("expired preparation quit the window")
		default:
		}
	})
}

type installRecordsReader struct {
	installReader
	outcome *application.InstallationOutcome
	err     error
}

func (r *installRecordsReader) LastInstallation(context.Context) (*application.InstallationOutcome, error) {
	return r.outcome, r.err
}

// A failure after handoff happens while the application is closed; the next
// start is the first chance to show it.
func TestNativeIdleStatusShowsLastIncompleteInstallation(t *testing.T) {
	t.Parallel()

	failed := &application.InstallationOutcome{Tag: installTestTag, Status: application.InstallationFailed, Error: "exit status 126", Directory: "/cache/stage-1"}

	for _, tc := range []struct {
		name    string
		outcome *application.InstallationOutcome
		err     error
		shown   bool
		running string
	}{
		{"failed", failed, nil, true, ""},
		// Setup run by hand installed the target or a later version since.
		{"target running", failed, nil, false, installTestTag},
		{"later running", failed, nil, false, "v4.6.0"},
		{"older running", failed, nil, true, "v4.5.5"},
		// A rollback from v4.6.0 that failed leaves v4.6.0 running.
		{"rollback failed", &application.InstallationOutcome{Tag: installTestTag, Status: application.InstallationFailed, From: "v4.6.0"}, nil, true, "v4.6.0"},
		{"unknown", &application.InstallationOutcome{Tag: installTestTag, Status: application.InstallationUnknown}, nil, true, ""},
		{"installed", &application.InstallationOutcome{Tag: installTestTag, Status: application.InstallationInstalled}, nil, false, ""},
		{"none", nil, nil, false, ""},
		{"unreadable", nil, errors.New("record is damaged"), false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := &installRecordsReader{outcome: tc.outcome, err: tc.err}
			if tc.running != "" {
				r.info = func(context.Context) (*application.InventoryResult, error) {
					return &application.InventoryResult{Desktop: application.BinaryVersion{Version: tc.running}, CLI: application.BinaryVersion{Version: tc.running}}, nil
				}
			}

			w := &installWindow{current: t.Context, owned: true, exited: make(chan struct{})}
			s := applicationnative.NewService(w, func(string, bool) (applicationnative.Reader, error) { return r, nil })

			state, err := s.InstallationStatus()
			if err != nil || state.Phase != "idle" || (state.LastResult != "") != tc.shown {
				t.Fatalf("idle state=%+v %v", state, err)
			}

			if tc.shown && state.LastResult != tc.outcome.Summary() {
				t.Fatalf("last result=%q", state.LastResult)
			}
		})
	}
}
