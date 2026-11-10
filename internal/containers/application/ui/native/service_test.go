package native_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
	applicationnative "github.com/DevWolk/garminctl/internal/containers/application/ui/native"
)

type windowFixture struct {
	current  func() context.Context
	releases atomic.Int32
}

func (w *windowFixture) Acquire() (context.Context, func(), error) {
	if w.current == nil {
		return nil, nil, errors.New("window not started")
	}

	if err := w.current().Err(); err != nil {
		return nil, nil, err
	}

	return w.current(), func() { w.releases.Add(1) }, nil
}
func (*windowFixture) Backend() (string, bool, error) { return "http://127.0.0.1:8787", false, nil }

type readerFixture struct {
	refreshes   []bool
	prereleases []bool
	info        func(context.Context) (*application.InventoryResult, error)
}

func (r *readerFixture) Info(ctx context.Context) (*application.InventoryResult, error) {
	if r.info != nil {
		return r.info(ctx)
	}

	return &application.InventoryResult{Desktop: application.BinaryVersion{Version: "native-build"}}, nil
}

func (r *readerFixture) History(_ context.Context, prerelease, refresh bool) (*application.ReleaseHistoryResult, error) {
	r.refreshes = append(r.refreshes, refresh)
	r.prereleases = append(r.prereleases, prerelease)

	return &application.ReleaseHistoryResult{}, nil
}

func TestNativeReadServiceUsesOneReaderAndSeparatesSavedFromRefresh(t *testing.T) {
	t.Parallel()
	window := &windowFixture{current: t.Context}
	reader := &readerFixture{}
	created := 0
	service := applicationnative.NewService(window, func(address string, owned bool) (applicationnative.Reader, error) {
		created++

		if address != "http://127.0.0.1:8787" || owned {
			t.Fatal("backend identity changed")
		}

		return reader, nil
	})

	if created != 0 {
		t.Fatal("constructor performed work")
	}

	if result, err := service.Info(); err != nil || result == nil || result.Desktop.Version != "native-build" {
		t.Fatalf("result=%+v error=%v", result, err)
	}

	if _, err := service.History(false); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Check(true); err != nil {
		t.Fatal(err)
	}

	assertReadPolicy(t, reader)

	if created != 1 || window.releases.Load() != 3 {
		t.Fatalf("created=%d releases=%d reader=%+v", created, window.releases.Load(), reader)
	}
}

func TestNativeReadServiceRejectsBeforeStartupAndAfterClose(t *testing.T) {
	t.Parallel()

	window := &windowFixture{}
	created := 0

	service := applicationnative.NewService(window, func(string, bool) (applicationnative.Reader, error) { created++; return &readerFixture{}, nil })
	if _, err := service.Info(); err == nil || created != 0 {
		t.Fatalf("pre-start error=%v created=%d", err, created)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	window.current = func() context.Context { return ctx }

	if _, err := service.Check(false); !errors.Is(err, context.Canceled) || created != 0 {
		t.Fatalf("closed error=%v created=%d", err, created)
	}
}

func TestNativeReadServiceJoinsScopeAfterCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	window := &windowFixture{current: func() context.Context { return ctx }}
	entered := make(chan struct{})
	reader := &readerFixture{info: func(ctx context.Context) (*application.InventoryResult, error) {
		close(entered)
		<-ctx.Done()

		return nil, ctx.Err()
	}}
	service := applicationnative.NewService(window, func(string, bool) (applicationnative.Reader, error) { return reader, nil })
	done := make(chan error, 1)

	go func() { _, err := service.Info(); done <- err }()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("reader not entered")
	}

	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) || window.releases.Load() != 1 {
		t.Fatalf("error=%v releases=%d", err, window.releases.Load())
	}
}

func assertReadPolicy(t *testing.T, reader *readerFixture) {
	t.Helper()

	if len(reader.refreshes) != 2 || reader.refreshes[0] || !reader.refreshes[1] || reader.prereleases[0] || !reader.prereleases[1] {
		t.Fatalf("read/refresh policy=%+v", reader)
	}
}
