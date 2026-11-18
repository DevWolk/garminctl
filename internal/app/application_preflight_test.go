package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/containers/connect"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/ship/paths"
)

type preflightLocalSource struct {
	target application.DownloadTarget
	calls  atomic.Int32
}

func (f *preflightLocalSource) Inspect(ctx context.Context) (application.LocalInventory, error) {
	f.calls.Add(1)
	root := filepath.Dir(f.target.CLIPath)

	desktop, cli, manager := "garmin-desktop", "garminctl", "dpkg"
	if f.target.OS == "windows" {
		desktop, cli, manager = "garmin-desktop.exe", "garminctl.exe", "registry"
	}

	owner := application.OwnershipEvidence{Manager: manager, Package: "garminctl", Root: root}

	return application.LocalInventory{
		OS: f.target.OS, Arch: f.target.Arch,
		Desktop: application.BinaryObservation{Path: filepath.Join(root, desktop), Directory: root, Name: desktop, Ownership: owner},
		CLI:     application.BinaryObservation{Path: f.target.CLIPath, Directory: root, Name: cli, Version: f.target.CLIVersion, Ownership: owner},
	}, ctx.Err()
}

type preflightNativeFixture struct {
	backend         *applicationBackendFixture
	reader          *applicationNativeReader
	current, update *application.DownloadResult
	pipeline        applicationPackageDownload
	local           *preflightLocalSource
	external        atomic.Int32
}

func applicationPreflightFixture(t *testing.T) *preflightNativeFixture {
	t.Helper()
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv("LOCALAPPDATA", cacheRoot)

	f := &preflightNativeFixture{}
	pipeline, target := downloadPipeline(t, applicationDownloadTag, false)
	pipeline.stageDirectory = applicationStageCache()
	pipeline.packageDirectory = filepath.Join(paths.Cache(), "application-packages")
	f.pipeline = pipeline
	current, _ := downloadPipeline(t, downloadTestBuild, false)
	current.stageDirectory, current.packageDirectory = pipeline.stageDirectory, pipeline.packageDirectory

	var err error

	f.current, err = current.Download(t.Context(), application.Release{Tag: downloadTestBuild}, target, nil)
	if err != nil {
		t.Fatal(err)
	}

	f.update, err = pipeline.Download(t.Context(), application.Release{Tag: applicationDownloadTag}, target, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Retaining fixtures needs no backend; start its bounded lifetime only when
	// the scenario is ready to exercise maintenance and HTTP operations.
	f.backend = serveApplicationBackendVersion(t, downloadTestBuild)

	endpoint, err := url.Parse(f.backend.address)
	if err != nil {
		t.Fatal(err)
	}

	f.local = &preflightLocalSource{target: target}
	client := &http.Client{Transport: downloadTestHTTP(func(*http.Request) (*http.Response, error) {
		f.external.Add(1)
		return nil, errors.New("preflight must not fetch release metadata")
	})}

	f.reader, err = f.backend.control.nativeReader(ApplicationReadConfig{Inventory: ApplicationInventoryConfig{DesktopVersion: downloadTestBuild, BackendURL: endpoint, BackendOwned: true, LocalSource: f.local}, ReleaseClient: client, HistoryDirectory: filepath.Join(t.TempDir(), "history")}, pipeline.key)
	if err != nil {
		t.Fatal(err)
	}

	return f
}

func (f *preflightNativeFixture) request() application.PreflightRequest {
	return application.PreflightRequest{Tag: f.update.Tag, CacheID: f.update.CacheID}
}

func (f *preflightNativeFixture) assertClean(t *testing.T) {
	t.Helper()
	assertDownloadOperationsClean(t, f.pipeline)

	if f.external.Load() != 0 {
		t.Fatal("offline preflight contacted the release feed")
	}

	for _, id := range []string{f.current.CacheID, f.update.CacheID} {
		if _, err := os.Stat(filepath.Join(f.pipeline.packageDirectory, id, "entry.json")); err != nil {
			t.Fatalf("retained package removed: %v", err)
		}
	}

	f.backend.status(t, "/api/settings", http.StatusOK)
}

func TestApplicationPreflightUsesOfflineCachedCLIForBothPackages(t *testing.T) { //nolint:paralleltest // production composition uses isolated process account/cache paths.
	f := applicationPreflightFixture(t)

	prepared, err := f.reader.Preflight(t.Context(), f.request())
	if err != nil || prepared == nil {
		t.Fatalf("preflight=%v %v", prepared, err)
	}

	defer func() {
		if err := prepared.Close(); err != nil {
			t.Error(err)
		}
	}()

	info := prepared.Info()
	if info.Current.ID != f.current.CacheID || info.Update.ID != f.update.CacheID || info.Inventory.Backend.Version != downloadTestBuild || f.local.calls.Load() != 2 {
		t.Fatalf("snapshot=%+v reads=%d", info, f.local.calls.Load())
	}

	operations, err := filepath.Glob(filepath.Join(f.pipeline.stageDirectory, "stage-*"))
	if err != nil || len(operations) != 2 {
		t.Fatalf("independent verified copies=%v %v", operations, err)
	}

	f.backend.status(t, "/api/settings", http.StatusConflict)

	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}

	f.assertClean(t)
}

func TestApplicationPreflightRejectsCorruptionInEitherRetainedPackage(t *testing.T) { //nolint:paralleltest // fixture changes process account/cache paths.
	//nolint:paralleltest // Sequential cases isolate process account/cache paths.
	for _, test := range []struct {
		name               string
		current, signature bool
	}{
		{name: "current package", current: true},
		{name: "update package"},
		{name: "current signature", current: true, signature: true},
		{name: "update signature", signature: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := applicationPreflightFixture(t)

			id := f.update.CacheID
			if test.current {
				id = f.current.CacheID
			}

			corruptPreflightPackage(t, f.pipeline.packageDirectory, id, test.signature)

			result, err := f.reader.Preflight(t.Context(), f.request())
			if err == nil || result != nil {
				t.Fatalf("corrupt package accepted: %v %v", result, err)
			}

			t.Logf("preflight refusal: %v", err)

			f.assertClean(t)
		})
	}
}

func corruptPreflightPackage(t *testing.T, directory, id string, signature bool) {
	t.Helper()

	root, err := os.OpenRoot(filepath.Join(directory, id))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	entries, err := os.ReadDir(filepath.Join(directory, id))
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if entry.Name() == "entry.json" || ((filepath.Ext(entry.Name()) == ".sig") != signature) {
			continue
		}

		file, err := root.OpenFile(entry.Name(), os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := file.Close(); err != nil {
				t.Error(err)
			}
		}()

		data, err := io.ReadAll(file)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := file.WriteAt(bytes.Repeat([]byte{0}, len(data)), 0); err != nil {
			t.Fatal(err)
		}

		return
	}

	t.Fatal("fixture package/signature was not found")
}

func TestApplicationPreflightBusyJobPreventsAnyInventoryOrPackageWork(t *testing.T) { //nolint:paralleltest // fixture changes process account/cache paths.
	f := applicationPreflightFixture(t)
	started, finish := make(chan struct{}), make(chan struct{})

	finishJob := sync.OnceFunc(func() { close(finish) })
	defer finishJob()

	job, _, err := f.backend.runtime.Sync.Start(t.Context(), connect.SyncOptions{}, func(ctx context.Context, _ connect.Progress) (*connect.SyncResult, error) {
		close(started)

		select {
		case <-finish:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	awaitApplicationBarrier(t, started)

	if result, err := f.reader.Preflight(t.Context(), f.request()); !errors.Is(err, lifecycle.ErrBusy) || result != nil || f.local.calls.Load() != 0 {
		t.Fatalf("busy result=%v err=%v inventory=%d", result, err, f.local.calls.Load())
	}

	finishJob()

	if _, err := job.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}

	f.assertClean(t)
}

func TestApplicationPreflightReusedAndStoppedBackendRemainUncontrolled(t *testing.T) { //nolint:paralleltest // fixture changes process account/cache paths.
	f := applicationPreflightFixture(t)

	f.reader.maintenance = f.backend.control.maintenanceFor(f.backend.address, false)
	if result, err := f.reader.Preflight(t.Context(), f.request()); result != nil || err == nil || f.local.calls.Load() != 0 {
		t.Fatalf("reused=%v %v", result, err)
	}

	f.assertClean(t)
	f.reader.maintenance = f.backend.control.maintenanceFor(f.backend.address, true)
	f.backend.cancel()
	awaitApplicationBarrier(t, f.backend.done)

	if result, err := f.reader.Preflight(t.Context(), f.request()); result != nil || !errors.Is(err, lifecycle.ErrStopped) || f.local.calls.Load() != 0 {
		t.Fatalf("stale=%v %v", result, err)
	}

	assertDownloadOperationsClean(t, f.pipeline)
}

type cancellingPreflightStore struct {
	applicationPackageStore
	cancel context.CancelFunc
}

func (s cancellingPreflightStore) Open(ctx context.Context, target application.DownloadTarget, id string) (application.VerifiedPackage, error) {
	verified, err := s.applicationPackageStore.Open(ctx, target, id)
	s.cancel()

	return verified, err
}

func TestApplicationPreflightCancellationClosesRealVerifiedCopy(t *testing.T) { //nolint:paralleltest // fixture changes process account/cache paths.
	f := applicationPreflightFixture(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	store := cancellingPreflightStore{key: f.pipeline.key, stageDirectory: f.pipeline.stageDirectory, packageDirectory: f.pipeline.packageDirectory, cancel: cancel}

	action := application.NewPreflightAction(f.reader, store, f.reader.TryMaintenance)
	if result, err := action.Run(ctx, f.request()); !errors.Is(err, context.Canceled) || result != nil {
		t.Fatalf("canceled=%v %v", result, err)
	}

	f.assertClean(t)
}
