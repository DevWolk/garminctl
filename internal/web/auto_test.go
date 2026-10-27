package web

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/garmin"
)

type probeDevice struct {
	device.Device
	stat func(context.Context) error
}

func (d probeDevice) Stat(ctx context.Context, _ string) (device.Entry, error) {
	return device.Entry{}, d.stat(ctx)
}

func probeService(t *testing.T, stat func(context.Context) error) (*Server, *garmin.Service) {
	t.Helper()

	root := t.TempDir()
	lazy := garmin.NewLazy(func(context.Context) (device.Device, error) {
		d, err := device.NewDirFS(root)
		return probeDevice{Device: d, stat: stat}, err
	})
	t.Cleanup(lazy.Drop)

	svc, err := lazy.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	return &Server{svc: lazy}, svc
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
			if !s.probe(t.Context(), true) || s.svc.Current() != svc {
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
	if s.probe(t.Context(), false) || s.svc.Current() != nil {
		t.Fatal("probe retained a broken transport")
	}
}

func TestProbeLateDeadlineRetainsSession(t *testing.T) {
	t.Parallel()

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

	if !<-result || s.svc.Current() != svc {
		t.Fatal("late deadline dropped the session after waiting for the device")
	}
}

func TestSkippedProbePreservesEitherPreviousObservation(t *testing.T) {
	t.Parallel()

	called := false
	s, _ := probeService(t, func(context.Context) error { called = true; return nil })

	lease, err := s.svc.Acquire(t.Context())
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
