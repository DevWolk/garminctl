package apps

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/paths"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/DevWolk/garminctl/internal/testdump"
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

	if err := writeRegistrySnapshot(dir, []byte("new")); err == nil {
		t.Fatal("replaced a directory with a registry file")
	}

	assertRegistryBytes(t, marker, []byte("previous"))
}
