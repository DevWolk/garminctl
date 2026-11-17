package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/gc"
)

func TestApplicationRestoreRequiresRealOwnedBackendJoinAndKeepsSafetyCopy(t *testing.T) { //nolint:paralleltest // Production runtime and account paths are isolated with process environment.
	f, root, old, safety := applicationRestoreFixture(t)

	action, err := f.backend.control.restoreDataAction(f.backend.address, true)
	if err != nil {
		t.Fatal(err)
	}

	// The signed cached-CLI copies are borrowed; no installer is invoked.
	packages := applicationPackageStore{key: f.pipeline.key}
	current := openRestorePackage(t, packages, f.local.target, f.update.CacheID)
	target := openRestorePackage(t, packages, f.local.target, f.current.CacheID)

	plan, err := action.Prepare(t.Context(), current, target, old.ID, safety.ID)
	if err != nil {
		t.Fatal(err)
	}

	consent := application.RestoreConsent{Confirmed: true, EditsResolved: true, ReviewID: plan.View().ReviewID}
	if value, err := action.Run(t.Context(), plan, consent); value != nil || err == nil {
		t.Fatalf("live backend permitted restore=%+v %v", value, err)
	}

	name := filepath.Base(gc.DefaultStorePath())
	if data, err := root.ReadFile(name); err != nil || string(data) != `{"opaque":"new state"}` {
		t.Fatalf("live data changed=%q %v", data, err)
	}

	if err := f.backend.control.stopFor(f.backend.address, true)(t.Context()); err != nil {
		t.Fatal(err)
	}

	plan, err = action.Prepare(t.Context(), current, target, old.ID, safety.ID)
	if err != nil {
		t.Fatal(err)
	}

	result, err := action.Run(t.Context(), plan, consent)
	assertApplicationRestoredFiles(t, root, name, result, err)

	reader, err := NewApplicationRecoverySnapshots()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := reader.Verify(t.Context(), safety.ID); err != nil {
		t.Fatalf("newer-state safety copy lost: %v", err)
	}
}

func TestApplicationRestoreReportsUntouchedDataWhenLiveStateChangedAfterReview(t *testing.T) { //nolint:paralleltest // Production runtime and account paths are isolated with process environment.
	f, root, old, safety := applicationRestoreFixture(t)

	action, err := f.backend.control.restoreDataAction(f.backend.address, true)
	if err != nil {
		t.Fatal(err)
	}

	packages := applicationPackageStore{key: f.pipeline.key}
	current := openRestorePackage(t, packages, f.local.target, f.update.CacheID)
	target := openRestorePackage(t, packages, f.local.target, f.current.CacheID)

	if err := f.backend.control.stopFor(f.backend.address, true)(t.Context()); err != nil {
		t.Fatal(err)
	}

	plan, err := action.Prepare(t.Context(), current, target, old.ID, safety.ID)
	if err != nil {
		t.Fatal(err)
	}

	name := filepath.Base(gc.DefaultStorePath())
	if err := root.WriteFile(name, []byte(`{"opaque":"changed after review"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := action.Run(t.Context(), plan, application.RestoreConsent{Confirmed: true, EditsResolved: true, ReviewID: plan.View().ReviewID})
	if err == nil || result == nil || result.Attempted || result.Completed || result.Replaced != 0 || result.Removed != 0 {
		t.Fatalf("refused restore=%+v %v", result, err)
	}

	if data, err := root.ReadFile(name); err != nil || string(data) != `{"opaque":"changed after review"}` {
		t.Fatalf("refused restore changed live data=%q %v", data, err)
	}
}

func applicationRestoreFixture(t *testing.T) (*preflightNativeFixture, *os.Root, *application.DataSnapshot, *application.DataSnapshot) {
	t.Helper()
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("APPDATA", config)
	f := applicationPreflightFixture(t)

	prepared, err := f.reader.Preflight(t.Context(), f.request())
	if err != nil {
		t.Fatal(err)
	}

	closeRestoreResource(t, prepared)

	account := gc.DefaultStorePath()

	root, err := os.OpenRoot(filepath.Dir(account))
	if err != nil {
		t.Fatal(err)
	}

	closeRestoreResource(t, root)

	if err := root.WriteFile(filepath.Base(account), []byte(`{"opaque":"old state"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	info := prepared.Info()

	old, err := (applicationDataSnapshots{}).Create(t.Context(), info)
	if err != nil {
		t.Fatal(err)
	}

	if err := root.WriteFile(filepath.Base(account), []byte(`{"opaque":"new state"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := root.WriteFile(filepath.Base(account)+".mfa", []byte(`{"opaque":"new mfa"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	safety, err := (applicationDataSnapshots{}).Create(t.Context(), application.PreflightInfo{Current: info.Update, Update: info.Current})
	if err != nil {
		t.Fatal(err)
	}

	return f, root, old, safety
}

func closeRestoreResource(t *testing.T, resource io.Closer) {
	t.Helper()
	t.Cleanup(func() {
		if err := resource.Close(); err != nil {
			t.Error(err)
		}
	})
}

func openRestorePackage(t *testing.T, store applicationPackageStore, target application.DownloadTarget, id string) application.VerifiedPackage {
	t.Helper()

	value, err := store.Open(t.Context(), target, id)
	if err != nil {
		t.Fatal(err)
	}

	closeRestoreResource(t, value)

	return value
}

func assertApplicationRestoredFiles(t *testing.T, root *os.Root, name string, result *application.DataRestoreResult, err error) {
	t.Helper()

	if err != nil || result == nil || !result.Completed || result.Replaced != 1 || result.Removed != 1 {
		t.Fatalf("restore=%+v %v", result, err)
	}

	if data, err := root.ReadFile(name); err != nil || string(data) != `{"opaque":"old state"}` {
		t.Fatalf("old data not restored=%q %v", data, err)
	}

	if _, err := root.Stat(name + ".mfa"); !os.IsNotExist(err) {
		t.Fatalf("new MFA not removed: %v", err)
	}
}

func TestApplicationRestoreJoinedGuardRefusesReusedAndStaleCapabilities(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	close(done)

	for _, tc := range []struct {
		name    string
		backend *ApplicationBackend
		owned   bool
	}{
		{name: "nil owner", owned: true},
		{name: "reused", backend: new(ApplicationBackend)},
		{name: "never started", backend: new(ApplicationBackend), owned: true},
		{name: "wrong address", backend: &ApplicationBackend{started: true, address: "http://127.0.0.1:2", done: done}, owned: true},
		{name: "failed join", backend: &ApplicationBackend{started: true, address: "http://127.0.0.1:1", done: done, runErr: errors.New("join failure")}, owned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := tc.backend.joinedFor("http://127.0.0.1:1", tc.owned)(context.Background()); err == nil {
				t.Fatal("unowned/stale backend accepted")
			}
		})
	}
}

func TestApplicationRestoreSerializesDifferentActionsOnSameOwner(t *testing.T) {
	t.Parallel()

	backend := new(ApplicationBackend)
	backend.dataRestoreMu.Lock()
	defer backend.dataRestoreMu.Unlock()

	store := applicationDataRestorations{backend: backend}
	if value, err := store.Restore(t.Context(), application.RecoverySnapshot{}, application.RecoverySnapshot{}); value != nil || !errors.Is(err, application.ErrPreparationBusy) {
		t.Fatalf("second action bypassed owner exclusion=%+v %v", value, err)
	}
}
