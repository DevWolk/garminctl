package native_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
	applicationnative "github.com/DevWolk/garminctl/internal/containers/application/ui/native"
)

type downloadReader struct {
	readerFixture
	disabled string
	run      func(context.Context, application.DownloadRequest, func(application.DownloadProgress)) (*application.DownloadResult, error)
}

func (r *downloadReader) DownloadDisabledReason() string { return r.disabled }
func (r *downloadReader) Download(ctx context.Context, request application.DownloadRequest, progress func(application.DownloadProgress)) (*application.DownloadResult, error) {
	return r.run(ctx, request, progress)
}

func waitDownload(t *testing.T, s *applicationnative.Service) *applicationnative.DownloadState {
	t.Helper()

	timer := time.NewTimer(time.Second)
	defer timer.Stop()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		state, err := s.DownloadStatus()
		if err != nil {
			t.Fatal(err)
		}

		if !state.Active {
			return state
		}

		select {
		case <-timer.C:
			t.Fatal("download did not finish")
		case <-ticker.C:
		}
	}
}

func nativeDownload(t *testing.T, reader *downloadReader) *applicationnative.Service {
	t.Helper()
	return applicationnative.NewService(&windowFixture{current: t.Context}, func(string, bool) (applicationnative.Reader, error) { return reader, nil })
}

func TestNativeDownloadCancellationWaitsForCleanupAndRejectsSecondStart(t *testing.T) {
	t.Parallel()

	entered, cleanup := make(chan struct{}), make(chan struct{})
	releaseCleanup := sync.OnceFunc(func() { close(cleanup) })
	t.Cleanup(releaseCleanup)

	reader := &downloadReader{run: func(ctx context.Context, request application.DownloadRequest, p func(application.DownloadProgress)) (*application.DownloadResult, error) {
		if request.Tag != "v1.2.0" || request.IncludePrerelease {
			t.Error("selection changed")
		}

		p(application.DownloadProgress{Phase: "downloading", Written: 2, Total: 3})
		close(entered)
		<-ctx.Done()
		<-cleanup

		return nil, ctx.Err()
	}}
	service := nativeDownload(t, reader)

	initial, err := service.StartDownload("v1.2.0", false)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter")
	}

	if _, err := service.StartDownload("v1.2.0", false); err == nil {
		t.Fatal("second download admitted")
	}

	canceling, err := service.CancelDownload(initial.ID)
	if err != nil || !canceling.Active || canceling.Phase != installPhaseCanceling {
		t.Fatalf("cancel=%+v %v", canceling, err)
	}

	current, err := service.DownloadStatus()
	if err != nil || !current.Active || current.Phase != installPhaseCanceling {
		t.Fatalf("cleanup bypassed: %+v %v", current, err)
	}

	releaseCleanup()

	if state := waitDownload(t, service); state.Phase != "canceled" || state.Result != nil {
		t.Fatalf("terminal=%+v", state)
	}
}

func TestNativeDownloadSnapshotsAndStaleCancelDoNotChangeSuccessor(t *testing.T) {
	t.Parallel()

	var callbacks []func(application.DownloadProgress)

	reader := &downloadReader{run: func(_ context.Context, request application.DownloadRequest, p func(application.DownloadProgress)) (*application.DownloadResult, error) {
		callbacks = append(callbacks, p)
		return &application.DownloadResult{Tag: request.Tag, CacheID: "cache", Digest: "digest", Bytes: 3}, nil
	}}
	service := nativeDownload(t, reader)

	first, err := service.StartDownload("v1.2.0", false)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := waitDownload(t, service)
	if snapshot.Phase != "ready" || snapshot.Result == nil {
		t.Fatalf("ready=%+v", snapshot)
	}

	snapshot.Result.Tag = "mutated"

	unchanged, err := service.DownloadStatus()
	if err != nil || unchanged.Result.Tag != "v1.2.0" {
		t.Fatalf("snapshot alias=%+v %v", unchanged, err)
	}

	second, err := service.StartDownload("v1.3.0", false)
	if err != nil {
		t.Fatal(err)
	}

	if first.ID == second.ID {
		t.Fatal("operation ID reused")
	}

	if _, err := service.CancelDownload(first.ID); err == nil {
		t.Fatal("stale cancellation accepted")
	}

	terminal := waitDownload(t, service)

	callbacks[0](application.DownloadProgress{Phase: "downloading", Written: 99, Total: 100})

	current, err := service.DownloadStatus()
	if err != nil || current.ID != second.ID || current.Phase != "ready" || current.Result.Tag != terminal.Result.Tag {
		t.Fatalf("stale callback changed successor=%+v %v", current, err)
	}
}

func TestNativeDownloadDisabledAndWindowCancellation(t *testing.T) {
	t.Parallel()
	service := nativeDownload(t, &downloadReader{disabled: "No compiled key"})

	state, err := service.DownloadStatus()
	if err != nil || state.DisabledReason != "No compiled key" {
		t.Fatalf("disabled=%+v %v", state, err)
	}

	if _, err := service.StartDownload("v1.2.0", false); err == nil {
		t.Fatal("missing key started a worker")
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	window := &windowFixture{current: func() context.Context { return ctx }}
	entered := make(chan struct{})
	reader := &downloadReader{run: func(ctx context.Context, _ application.DownloadRequest, _ func(application.DownloadProgress)) (*application.DownloadResult, error) {
		close(entered)
		<-ctx.Done()

		return nil, ctx.Err()
	}}

	service = applicationnative.NewService(window, func(string, bool) (applicationnative.Reader, error) { return reader, nil })
	if _, err := service.StartDownload("v1.2.0", false); err != nil {
		t.Fatal(err)
	}

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("not entered")
	}

	cancel()

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for window.releases.Load() != 1 {
		select {
		case <-deadline.C:
			t.Fatal("window lease not released after cancel")
		case <-ticker.C:
		}
	}

	if _, err := service.DownloadStatus(); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed window admitted: %v", err)
	}
}
