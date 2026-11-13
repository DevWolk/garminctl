package native_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
	applicationnative "github.com/DevWolk/garminctl/internal/containers/application/ui/native"
)

const (
	installTestTag    = "v4.5.6"
	installStopFailed = "stop failed"
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
	if err != nil || !canceled.Active || canceled.Phase != "canceling" {
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
