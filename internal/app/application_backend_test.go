package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/app/process"
	"github.com/DevWolk/garminctl/internal/containers/connect"
	"github.com/DevWolk/garminctl/internal/desktop"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/testdump"
)

type applicationAcceptListener struct {
	net.Listener
	once  sync.Once
	ready chan struct{}
}

func (l *applicationAcceptListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.ready) })
	return l.Listener.Accept()
}

type applicationBackendFixture struct {
	control *ApplicationBackend
	address string
	runtime *process.Runtime
	cancel  context.CancelFunc
	done    chan struct{}
	client  *http.Client
}

func serveApplicationBackend(t *testing.T) *applicationBackendFixture {
	t.Helper()
	return serveApplicationBackendVersion(t, "v1.2.3")
}

func serveApplicationBackendVersion(t *testing.T, version string) *applicationBackendFixture {
	t.Helper()
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("APPDATA", config)

	// The fixture owns cancellation through cleanup. An unrelated wall-clock
	// expiry can tear down a healthy backend while cached CLI setup is still
	// running on a loaded runner; shutdown itself has a bounded join below.
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))

	var listen net.ListenConfig

	listener, err := listen.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		t.Fatal(err)
	}

	root := testdump.Root(t)
	ready := &applicationAcceptListener{Listener: listener, ready: make(chan struct{})}

	fixture := &applicationBackendFixture{
		control: new(ApplicationBackend), address: "http://" + listener.Addr().String(),
		cancel: cancel, done: make(chan struct{}), client: &http.Client{Transport: new(http.Transport), Timeout: 5 * time.Second},
	}
	go func() {
		defer close(fixture.done)

		if err := fixture.control.ServeListener(ctx, ready, func(context.Context) (device.Device, error) { return device.NewDirFS(root) }, false, version); err != nil {
			t.Errorf("owned backend: %v", err)
		}
	}()

	t.Cleanup(func() {
		fixture.client.CloseIdleConnections()
		fixture.cancel()
		awaitApplicationBarrier(t, fixture.done)
	})
	awaitApplicationBarrier(t, ready.ready)
	fixture.control.mu.Lock()
	fixture.runtime = fixture.control.runtime
	fixture.control.mu.Unlock()

	return fixture
}

func awaitApplicationBarrier(t *testing.T, done <-chan struct{}) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("application backend barrier was not reached")
	}
}

func (f *applicationBackendFixture) status(t *testing.T, path string, want int) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.address+path, nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != want {
		t.Fatalf("%s: status=%d body=%s err=%v", path, response.StatusCode, body, err)
	}
}

func (f *applicationBackendFixture) reader(t *testing.T) *applicationNativeReader {
	t.Helper()

	endpoint, err := url.Parse(f.address)
	if err != nil {
		t.Fatal(err)
	}

	reader, err := f.control.nativeReader(ApplicationReadConfig{Inventory: ApplicationInventoryConfig{DesktopVersion: "v1.2.3", BackendURL: endpoint, BackendOwned: true}}, "")
	if err != nil {
		t.Fatal(err)
	}

	return reader
}

func TestApplicationBackendMaintenanceGuardsProductionHTTPAndMCP(t *testing.T) { //nolint:paralleltest // production composition reads isolated process account paths.
	f := serveApplicationBackend(t)

	client, err := mcp.NewClient(&mcp.Implementation{Name: "maintenance-test", Version: "test"}, nil).Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: f.address + "/mcp", HTTPClient: f.client}, nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()

	maintenance := f.reader(t).TryMaintenance

	release, err := maintenance(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	f.status(t, "/api/settings", http.StatusConflict)
	f.status(t, "/api/server", http.StatusOK)

	result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "watch_settings"})
	if err != nil || result == nil || !result.IsError || len(result.Content) == 0 {
		t.Fatalf("maintenance MCP result=%+v error=%v", result, err)
	}

	message, ok := result.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(message.Text, lifecycle.ErrMaintenance.Error()) {
		t.Fatalf("MCP rejected for another reason: %+v", result)
	}

	if again, err := maintenance(t.Context()); !errors.Is(err, lifecycle.ErrMaintenance) || again != nil {
		t.Fatalf("second maintenance=%v", err)
	}

	release()
	f.status(t, "/api/settings", http.StatusOK)

	result, err = client.CallTool(t.Context(), &mcp.CallToolParams{Name: "watch_settings"})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("resumed MCP=%+v %v", result, err)
	}
}

func TestApplicationBackendMaintenanceRejectsJobBetweenLeases(t *testing.T) { //nolint:paralleltest // production composition reads isolated process account paths.
	f := serveApplicationBackend(t)
	if f.runtime == nil {
		t.Fatal("owned server did not bind its runtime")
	}

	started, finish := make(chan struct{}), make(chan struct{})

	finishJob := sync.OnceFunc(func() { close(finish) })
	defer finishJob()

	job, _, err := f.runtime.Sync.Start(t.Context(), connect.SyncOptions{}, func(ctx context.Context, _ connect.Progress) (*connect.SyncResult, error) {
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

	if snapshot := f.runtime.Manager().Gate().Snapshot(); snapshot.Operations != 0 || snapshot.Jobs != 1 {
		t.Fatalf("job fixture holds a device lease: %+v", snapshot)
	}

	maintenance := f.reader(t).TryMaintenance
	if release, err := maintenance(t.Context()); !errors.Is(err, lifecycle.ErrBusy) || release != nil {
		t.Fatalf("job did not prevent maintenance: %v", err)
	}

	f.status(t, "/api/settings", http.StatusOK)
	finishJob()

	if _, err := job.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}

	release, err := maintenance(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	release()
}

func TestApplicationBackendMaintenanceAfterExitAndWrongAddress(t *testing.T) { //nolint:paralleltest // production composition reads isolated process account paths.
	f := serveApplicationBackend(t)
	if release, err := f.control.maintenanceFor("http://127.0.0.1:1", true)(t.Context()); err == nil || release != nil {
		t.Fatal("maintenance accepted a different backend address")
	}

	maintenance := f.reader(t).TryMaintenance

	release, err := maintenance(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	f.cancel()
	awaitApplicationBarrier(t, f.done)
	release()

	if late, err := maintenance(t.Context()); !errors.Is(err, lifecycle.ErrStopped) || late != nil {
		t.Fatalf("stale callback admitted work: %v", err)
	}

	if f.runtime == nil || f.runtime.Manager().Gate().Snapshot().State != lifecycle.Closed {
		t.Fatal("late release reopened the stopped runtime")
	}
}

func TestApplicationBackendReusedServerCreatesNoRuntime(t *testing.T) { //nolint:paralleltest // production composition reads isolated process account paths.
	f := serveApplicationBackend(t)
	other := new(ApplicationBackend)

	var starts atomic.Int32

	endpoint, err := url.Parse(f.address)
	if err != nil {
		t.Fatal(err)
	}

	reused, err := desktop.StartBackend(t.Context(), endpoint.Host, func(ctx context.Context, listener net.Listener) error {
		starts.Add(1)
		return other.ServeListener(ctx, listener, nil, false, "test")
	})
	if err != nil {
		t.Fatal(err)
	}

	if release, err := other.maintenanceFor(f.address, false)(t.Context()); err == nil || release != nil {
		t.Fatal("reused backend accepted maintenance")
	}

	if starts.Load() != 0 || other.runtime != nil || other.started {
		t.Fatal("reused backend constructed an owner")
	}

	if err := reused.Close(); err != nil {
		t.Fatal(err)
	}

	f.status(t, "/api/settings", http.StatusOK)
}

func TestApplicationBackendRejectsSecondServeWithoutReplacingOwner(t *testing.T) { //nolint:paralleltest // production composition reads isolated process account paths.
	f := serveApplicationBackend(t)

	var config net.ListenConfig

	listener, err := config.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	if err := f.control.ServeListener(t.Context(), listener, nil, false, "other"); err == nil {
		t.Fatal("control accepted a second runtime")
	}

	if connection, err := config.Listen(t.Context(), "tcp", listener.Addr().String()); err != nil {
		t.Fatalf("rejected listener was not closed: %v", err)
	} else if err := connection.Close(); err != nil {
		t.Fatal(err)
	}

	f.status(t, "/api/settings", http.StatusOK)

	release, err := f.reader(t).TryMaintenance(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	release()
}

func TestApplicationBackendReaderRegistrationCreatesNoOwners(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		owned bool
	}{
		{name: "owned", owned: true},
		{name: "reused"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			control := new(ApplicationBackend)

			reader, err := control.nativeReader(ApplicationReadConfig{Inventory: ApplicationInventoryConfig{BackendOwned: test.owned}}, "")
			if err != nil {
				t.Fatal(err)
			}

			if control.started || control.runtime != nil {
				t.Fatal("reader constructed a backend owner")
			}

			if resume, err := control.maintenanceFor("", test.owned)(t.Context()); err == nil || resume != nil {
				t.Fatal("unbound backend accepted maintenance")
			}

			if resume, err := reader.TryMaintenance(t.Context()); err == nil || resume != nil {
				t.Fatal("reader bypassed missing backend ownership")
			}
		})
	}
}

func TestApplicationBackendMaintenanceCancellationAndQueuedLeases(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		runtime := NewRuntime(t.Context(), func(context.Context) (device.Device, error) { return &maintenanceTestDevice{}, nil })
		defer func() {
			if err := runtime.Close(context.WithoutCancel(t.Context())); err != nil {
				t.Error(err)
			}
		}()

		control := &ApplicationBackend{runtime: runtime, address: "owned", started: true}
		maintenance := control.maintenanceFor("owned", true)
		checkMaintenanceQueue(t, runtime, maintenance)
		checkMaintenanceCancellation(t, runtime, maintenance)
	})
}

type maintenanceTestDevice struct{ device.Device }

func (*maintenanceTestDevice) Close() error { return nil }

func checkMaintenanceQueue(t *testing.T, runtime *process.Runtime, maintenance func(context.Context) (func(), error)) {
	t.Helper()

	first, err := runtime.Manager().Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := first.Release(); err != nil {
			t.Error(err)
		}
	}()

	if release, err := maintenance(t.Context()); !errors.Is(err, lifecycle.ErrBusy) || release != nil {
		t.Fatalf("active lease did not prevent maintenance: %v", err)
	}

	queued, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)

	go func() {
		lease, err := runtime.Manager().Acquire(queued)
		if lease != nil {
			err = errors.Join(err, lease.Release())
		}

		done <- err
	}()

	synctest.Wait()

	if snapshot := runtime.Manager().Gate().Snapshot(); snapshot.Operations != 2 {
		t.Fatalf("waiter did not reserve admission: %+v", snapshot)
	}

	if release, err := maintenance(t.Context()); !errors.Is(err, lifecycle.ErrBusy) || release != nil {
		t.Fatalf("queued operations did not prevent maintenance: %v", err)
	}

	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation=%v", err)
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
}

func checkMaintenanceCancellation(t *testing.T, runtime *process.Runtime, maintenance func(context.Context) (func(), error)) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())

	release, err := maintenance(ctx)
	if err != nil {
		t.Fatal(err)
	}

	cancel()
	synctest.Wait()

	if snapshot := runtime.Manager().Gate().Snapshot(); snapshot.State != lifecycle.Running {
		t.Fatalf("cancelled preparation retained maintenance: %+v", snapshot)
	}

	if denied, err := maintenance(ctx); !errors.Is(err, context.Canceled) || denied != nil {
		t.Fatalf("cancelled request acquired maintenance: %v", err)
	}

	second, err := maintenance(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	release()

	if snapshot := runtime.Manager().Gate().Snapshot(); snapshot.State != lifecycle.Maintenance {
		t.Fatal("stale release ended another maintenance ticket")
	}

	runtime.Stop()
	second()

	if snapshot := runtime.Manager().Gate().Snapshot(); snapshot.State != lifecycle.Closed {
		t.Fatal("late release reopened stopping runtime")
	}
}
