package web

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

type probeDevice struct {
	device.Device
	stat func(context.Context) error
}

func (d probeDevice) Stat(ctx context.Context, _ string) (device.Entry, error) {
	return device.Entry{}, d.stat(ctx)
}

func probeService(t *testing.T, stat func(context.Context) error) (*Server, *watchsession.Session) {
	t.Helper()

	root := t.TempDir()
	manager := watchsession.NewManager(func(context.Context) (device.Device, error) {
		d, err := device.NewDirFS(root)
		return probeDevice{Device: d, stat: stat}, err
	})
	t.Cleanup(manager.Drop)

	lease, err := manager.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	sess := lease.Session()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	s := &Server{manager: manager}
	s.features.WatchModel = func(context.Context, *watchsession.Session) (string, error) { return "", nil }

	return s, sess
}

func TestProbeRetainsSessionOnNonDisconnectErrors(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "busy", err: device.ErrBusy},
		{name: "pre-I/O deadline", err: context.DeadlineExceeded},
		{name: "pre-I/O cancellation", err: context.Canceled},
		{name: "domain error", err: errors.New("invalid bridge label")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, svc := probeService(t, func(context.Context) error { return tt.err })
			if !s.probe(t.Context(), true) || s.manager.Current() != svc {
				t.Fatal("probe discarded a usable session")
			}

			if s.probe(t.Context(), false) {
				t.Fatal("failed probe invented a newly confirmed connection")
			}
		})
	}
}

func TestProbeDropsTypedDisconnect(t *testing.T) {
	t.Parallel()

	s, _ := probeService(t, func(context.Context) error {
		return errors.Join(device.ErrDisconnected, context.DeadlineExceeded)
	})
	if s.probe(t.Context(), false) || s.manager.Current() != nil {
		t.Fatal("probe retained a broken transport")
	}
}

func TestProbeLateDeadlineRetainsSession(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		s, svc := probeService(t, func(ctx context.Context) error {
			close(entered)
			<-release // models an ordinary device mutex that ignores cancellation

			return ctx.Err()
		})

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		defer cancel()

		result := make(chan bool, 1)
		go func() { result <- s.probe(ctx, true) }()

		<-entered
		<-ctx.Done()
		close(release)

		if !<-result || s.manager.Current() != svc {
			t.Fatal("late deadline dropped the session after waiting for the device")
		}
	})
}

func TestSkippedProbePreservesEitherPreviousObservation(t *testing.T) {
	t.Parallel()

	called := false
	s, _ := probeService(t, func(context.Context) error { called = true; return nil })

	lease, err := s.manager.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := lease.Release(); err != nil {
			t.Error(err)
		}
	}()

	if !s.probe(t.Context(), true) || s.probe(t.Context(), false) || called {
		t.Fatal("busy probe touched USB or changed previous observation")
	}
}

func TestPollConnectionConfirmsEachEpochOnce(t *testing.T) {
	t.Parallel()
	s, _ := probeService(t, func(context.Context) error { return nil })

	var state watchState

	first, _, ok := s.pollConnection(t.Context(), &state)
	if !ok || first == 0 {
		t.Fatal("API-opened connection was not observed")
	}

	if _, _, ok := s.pollConnection(t.Context(), &state); ok {
		t.Fatal("same epoch repeated connection actions")
	}

	if err := s.manager.DropAt(first); err != nil {
		t.Fatal(err)
	}

	if _, _, ok := s.pollConnection(t.Context(), &state); ok {
		t.Fatal("missing connection was reported connected")
	}

	lease, err := s.manager.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	second := lease.Epoch()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	observed, _, ok := s.pollConnection(t.Context(), &state)
	if !ok || observed != second || second == first {
		t.Fatalf("replacement epoch not observed: first=%d current=%d observed=%d", first, second, observed)
	}
}

func TestPollConnectionSkippedProbeDoesNotInventConnection(t *testing.T) {
	t.Parallel()
	s, _ := probeService(t, func(context.Context) error { return nil })

	lease, err := s.manager.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	var state watchState
	if _, _, ok := s.pollConnection(t.Context(), &state); ok || state.connected {
		t.Fatal("busy probe invented connection observation")
	}

	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	if _, _, ok := s.pollConnection(t.Context(), &state); !ok {
		t.Fatal("idle confirmed epoch was not observed")
	}
}
