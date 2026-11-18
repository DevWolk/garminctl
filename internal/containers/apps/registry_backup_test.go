package apps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/paths"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

type registryWriteDevice struct {
	device.Device
	writes int
	before func() error
	fail   error
}

func (d *registryWriteDevice) Write(ctx context.Context, path string, data []byte) error {
	d.writes++
	if d.before != nil {
		if err := d.before(); err != nil {
			return err
		}
	}

	if d.fail != nil {
		return d.fail
	}

	return d.Device.Write(ctx, path, data)
}

func registryFixture(t *testing.T) (*registryWriteDevice, []byte, []App) {
	t.Helper()

	d, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = d.Close() })

	out, err := d.Read(t.Context(), PathAppsConfig)
	if err != nil {
		t.Fatal(err)
	}

	apps, err := ParseAppsConfig(out)
	if err != nil {
		t.Fatal(err)
	}

	counts := make(map[string]int)
	for _, app := range apps {
		counts[app.Name]++
	}

	var targets []App

	_, err = rewriteApps(out, func(app App, fields []pbField) ([]pbField, error) {
		if counts[app.Name] == 1 && setHiddenFlag(fields, app.Hidden) {
			targets = append(targets, app)
		}

		return fields, nil
	})
	if err != nil || len(targets) < 2 {
		t.Fatalf("fixture needs two unambiguous entries with hidden flags: %v, %d", err, len(targets))
	}

	return &registryWriteDevice{Device: d}, out, targets[:2]
}

func registryCache(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	t.Setenv("LOCALAPPDATA", root)

	return paths.Cache()
}

func assertRegistryBytes(t *testing.T, path string, want []byte) {
	t.Helper()

	got, err := os.ReadFile(path) //nolint:gosec // test-owned snapshot path
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("registry bytes at %s differ: %v", path, err)
	}
}

func TestSetAppHiddenBacksUpBeforeEachQueuedChange(t *testing.T) { //nolint:paralleltest // registryCache changes process cache-directory environment
	cache := registryCache(t)
	d, out, targets := registryFixture(t)
	sess := watchsession.New(d)

	d.before = func() error {
		copies, err := filepath.Glob(filepath.Join(cache, "OUT.BIN-*", "OUT.BIN"))
		if err != nil || len(copies) != d.writes {
			return errors.New("device mutation attempted before publishing a distinct backup")
		}

		for _, snapshot := range copies {
			assertRegistryBytes(t, snapshot, out)
		}

		return nil
	}

	var previous string

	for _, app := range targets {
		backup, err := (VisibilityAction{}).Run(t.Context(), sess, app.Name, !app.Hidden)
		if err != nil {
			t.Fatal(err)
		}

		if backup == previous || backup == "" {
			t.Fatal("backup reused or missing")
		}

		previous = backup
		assertRegistryBytes(t, backup, out)
		assertRegistryBackupProtection(t, backup)
	}

	assertQueuedRegistryChanges(t, d, out, targets)
}

func assertQueuedRegistryChanges(t *testing.T, d device.Device, out []byte, targets []App) {
	t.Helper()

	pending, err := d.Read(t.Context(), PathAppsIn)
	if err != nil {
		t.Fatal(err)
	}

	changes, err := (PendingRegistryAction{}).Run(t.Context(), watchsession.New(d))
	if err != nil || len(changes) != len(targets) {
		t.Fatalf("queued changes = %+v, error = %v", changes, err)
	}

	restored, err := rewriteApps(pending, func(app App, fields []pbField) ([]pbField, error) {
		for _, target := range targets {
			if app.Name == target.Name {
				if app.Hidden == target.Hidden {
					t.Errorf("queued visibility change lost for %s", app.Name)
				}

				setHiddenFlag(fields, target.Hidden)
			}
		}

		return fields, nil
	})
	if err != nil || !bytes.Equal(restored, out) {
		t.Fatalf("restoring two flags did not recover all original bytes/order: %v", err)
	}

	current, err := d.Read(t.Context(), PathAppsConfig)
	if err != nil || !bytes.Equal(current, out) {
		t.Fatalf("OUT.BIN changed: %v", err)
	}
}

func TestSetAppHiddenBackupFailureDoesNotWrite(t *testing.T) { //nolint:paralleltest // registryCache changes process cache-directory environment
	cache := registryCache(t)

	d, out, targets := registryFixture(t)
	if err := d.Device.Write(t.Context(), PathAppsIn, out); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(cache, []byte("blocked cache directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	backup, err := (VisibilityAction{}).Run(t.Context(), watchsession.New(d), targets[0].Name, !targets[0].Hidden)
	if err == nil || backup != "" || d.writes != 0 {
		t.Fatalf("backup failure: path=%q err=%v writes=%d", backup, err, d.writes)
	}

	for _, path := range []string{PathAppsConfig, PathAppsIn} {
		got, err := d.Read(t.Context(), path)
		if err != nil || !bytes.Equal(got, out) {
			t.Fatalf("%s changed after backup failure: %v", path, err)
		}
	}
}

func TestSetAppHiddenWriteFailureRetainsBackup(t *testing.T) { //nolint:paralleltest // registryCache changes process cache-directory environment
	registryCache(t)
	d, out, targets := registryFixture(t)
	d.fail = errors.New("synthetic device write failure")

	backup, err := (VisibilityAction{}).Run(t.Context(), watchsession.New(d), targets[0].Name, !targets[0].Hidden)
	if !errors.Is(err, d.fail) || backup == "" {
		t.Fatalf("write failure lost its backup: %q, %v", backup, err)
	}

	assertRegistryBytes(t, backup, out)
}

func TestRegistryBackupReadbackRejectsMissingAndDifferentBytes(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "OUT.BIN")
	if err := verifyRegistryBackup(path, []byte("expected")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing backup accepted: %v", err)
	}

	if err := os.WriteFile(path, []byte("truncated"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := verifyRegistryBackup(path, []byte("expected")); err == nil {
		t.Fatal("different backup bytes accepted")
	}
}

func TestRegistrySnapshotFailedReplacementDoesNotTouchExistingCopy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	target := filepath.Join(dir, "OUT.BIN")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(target, "keep")
	if err := os.WriteFile(marker, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := writeSnapshotFile(dir, "OUT.BIN", []byte("new")); err == nil {
		t.Fatal("replaced a directory with a registry file")
	}

	assertRegistryBytes(t, marker, []byte("previous"))
}

type registryReadDevice struct {
	device.Device
	path string
	fail error
}

func (d *registryReadDevice) Read(ctx context.Context, path string) ([]byte, error) {
	if path == d.path {
		return nil, d.fail
	}

	return d.Device.Read(ctx, path)
}

func TestSetAppHiddenPendingReadErrorAborts(t *testing.T) { //nolint:paralleltest // registryCache changes process cache-directory environment
	cache := registryCache(t)
	d, out, targets := registryFixture(t)
	busy := errors.New("synthetic transport failure")
	sess := watchsession.New(&registryReadDevice{Device: d, path: PathAppsIn, fail: busy})

	backup, err := (VisibilityAction{}).Run(t.Context(), sess, targets[0].Name, !targets[0].Hidden)
	if !errors.Is(err, busy) || backup != "" || d.writes != 0 {
		t.Fatalf("pending read failure: path=%q err=%v writes=%d", backup, err, d.writes)
	}

	if snapshots, _ := filepath.Glob(filepath.Join(cache, "OUT.BIN-*")); len(snapshots) != 0 {
		t.Fatalf("backup written before the pending registry was read: %v", snapshots)
	}

	// A missing IN.BIN still means an empty queue.
	sess = watchsession.New(&registryReadDevice{Device: d, path: PathAppsIn, fail: device.ErrNotFound})
	if _, err := (VisibilityAction{}).Run(t.Context(), sess, targets[0].Name, !targets[0].Hidden); err != nil || d.writes != 1 {
		t.Fatalf("missing pending registry: err=%v writes=%d", err, d.writes)
	}

	assertQueuedRegistryChanges(t, d, out, targets[:1])
}

func TestSetAppHiddenSnapshotsPendingRegistry(t *testing.T) { //nolint:paralleltest // registryCache changes process cache-directory environment
	registryCache(t)
	d, out, targets := registryFixture(t)
	sess := watchsession.New(d)

	first, err := (VisibilityAction{}).Run(t.Context(), sess, targets[0].Name, !targets[0].Hidden)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(filepath.Dir(first), "IN.BIN")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first change had no pending registry, snapshot holds IN.BIN: %v", err)
	}

	queued, err := d.Read(t.Context(), PathAppsIn)
	if err != nil {
		t.Fatal(err)
	}

	second, err := (VisibilityAction{}).Run(t.Context(), sess, targets[1].Name, !targets[1].Hidden)
	if err != nil {
		t.Fatal(err)
	}

	assertRegistryBytes(t, second, out)
	assertRegistryBytes(t, filepath.Join(filepath.Dir(second), "IN.BIN"), queued)
	assertRegistryBackupProtection(t, filepath.Join(filepath.Dir(second), "IN.BIN"))
}

func TestPendingRegistryTransportErrorIsNotAbsence(t *testing.T) { //nolint:paralleltest // registryCache changes process cache-directory environment
	registryCache(t)
	d, _, targets := registryFixture(t)

	// A broken USB transfer whose text happens to contain "not found".
	broken := fmt.Errorf("%w: MTP transfer: libusb: not found [code -5]", device.ErrDisconnected)
	sess := watchsession.New(&registryReadDevice{Device: d, path: PathAppsIn, fail: broken})

	if changes, err := (PendingRegistryAction{}).Run(t.Context(), sess); !errors.Is(err, device.ErrDisconnected) {
		t.Fatalf("pending changes = %v, error = %v; want the transport error", changes, err)
	}

	if _, err := (VisibilityAction{}).Run(t.Context(), sess, targets[0].Name, !targets[0].Hidden); !errors.Is(err, device.ErrDisconnected) || d.writes != 0 {
		t.Fatalf("visibility change over a broken read: err=%v writes=%d", err, d.writes)
	}
}
