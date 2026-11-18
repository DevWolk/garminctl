package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/hostfile"
)

func TestApplicationCompatibleRollbackPreservesCurrentEditsAndMFAWithoutRestoration(t *testing.T) { //nolint:paralleltest // Owns isolated account/cache paths and a real fixture backend.
	f := newApplicationRollbackFixture(t)
	if err := os.Remove(f.account + ".mfa"); err != nil {
		t.Fatal(err)
	}

	request := f.request
	request.KeepCurrentData = true
	request.SnapshotID = compatibilitySnapshot(t, f).ID
	accountData := []byte(`{"email":"newer@example.invalid","oauth1":{"oauth_token":"newer-fake-token"}}`)
	writeApplicationState(t, filepath.Dir(f.account), filepath.Base(f.account), accountData)

	mfaData := []byte(`{"version":1,"id":"fake-pending","domain":"garmin.com","email":"newer@example.invalid","method":"email","params":{"clientId":["GCM_ANDROID_DARK"],"service":["https://mobile.integration.garmin.com/gcm/android"]},"cookies":[],"createdAt":"2026-11-01T10:00:00Z","expiresAt":"2026-11-01T10:10:00Z"}`)
	writeApplicationState(t, filepath.Dir(f.account), filepath.Base(f.account)+".mfa", mfaData)
	compatibleInstaller(t, f)

	action, err := f.f.backend.control.rollbackAction(f.f.reader, f.f.backend.address, true)
	if err != nil {
		t.Fatal(err)
	}

	prepared, err := action.Prepare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}

	closeRestoreResource(t, prepared)

	view := prepared.View()
	if !view.Data.KeepCurrentData || len(view.Data.Changes) != 0 {
		t.Fatalf("review=%+v", view.Data)
	}

	result, err := action.Run(t.Context(), prepared, application.RollbackConsent{Installation: application.InstallConsent{Confirmed: true, EditsResolved: true}, Data: application.RestoreConsent{Confirmed: true, EditsResolved: true, ReviewID: view.Data.ReviewID}})
	if err != nil || result == nil || result.Data != nil || result.Handoff.State != application.HandoffAccepted || f.commits != 1 {
		t.Fatalf("result=%+v commits=%d error=%v", result, f.commits, err)
	}

	assertCompatibleRollbackData(t, f, request, view, accountData, mfaData)
}

// compatibilitySnapshot creates format provenance from the simulated producing
// build. Package bytes still pass the ordinary cached-CLI signature verification.
func compatibilitySnapshot(t *testing.T, f *applicationRollbackFixture) *application.DataSnapshot {
	t.Helper()

	packages := applicationPackageStore{key: f.f.pipeline.key}
	current := openRestorePackage(t, packages, f.f.local.target, f.f.current.CacheID)
	old := openRestorePackage(t, packages, f.f.local.target, f.request.Package.CacheID)
	info := application.PreflightInfo{Current: old.Record(), Update: current.Record(), Inventory: application.InventoryResult{CLI: application.BinaryVersion{Version: old.Record().Tag}}}

	value, err := (applicationDataSnapshots{build: old.Record().Tag}).Create(t.Context(), info)
	if err != nil {
		t.Fatal(err)
	}

	return value
}

func compatibleInstaller(t *testing.T, f *applicationRollbackFixture) {
	t.Helper()

	installer := applicationFixtureInstaller{prepare: func(_ context.Context, input application.InstallationInput) (application.PendingInstallation, error) {
		copyApplicationHandoffFixture(t, input)

		return applicationFixturePendingInstaller{commit: func(context.Context) error {
			f.commits++
			if err := f.f.backend.control.joinedFor(f.f.backend.address, true)(t.Context()); err != nil {
				t.Fatal(err)
			}

			if got := readApplicationState(t, filepath.Dir(f.account), filepath.Base(f.account)); string(got) != `{"email":"newer@example.invalid","oauth1":{"oauth_token":"newer-fake-token"}}` {
				t.Fatal("compatible rollback changed live account")
			}

			return nil
		}, close: func() error { return nil }}, nil
	}}
	f.f.reader.handoff = application.NewInstallHandoffAction(f.f.reader.ApplicationReads, installer, f.f.backend.control.stopFor(f.f.backend.address, true))
}

func assertCompatibleRollbackData(t *testing.T, f *applicationRollbackFixture, request application.RollbackRequest, view application.RollbackView, accountData, mfaData []byte) {
	t.Helper()

	for name, want := range map[string][]byte{filepath.Base(f.account): accountData, filepath.Base(f.account) + ".mfa": mfaData} {
		if got := readApplicationState(t, filepath.Dir(f.account), name); string(got) != string(want) {
			t.Fatalf("current %s changed", name)
		}
	}

	snapshots, err := NewApplicationRecoverySnapshots()
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{request.SnapshotID, view.Data.SafetySnapshotID} {
		if _, err := snapshots.Verify(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSnapshotFormatProvenanceRequiresProducingBuildIdentity(t *testing.T) { //nolint:paralleltest // Redirects local account/cache paths for protected snapshot fixtures.
	f := newRecoverySnapshotFixture(t)

	legacy, err := f.action.Verify(t.Context(), f.snapshot.ID)
	if err != nil || legacy.Formats != nil {
		t.Fatalf("legacy=%+v error=%v", legacy, err)
	}

	info := application.PreflightInfo{Current: f.current, Update: f.update, Inventory: application.InventoryResult{CLI: application.BinaryVersion{Version: f.current.Tag}}}
	for _, build := range []string{f.current.Tag, "other-producing-build"} {
		value, err := (applicationDataSnapshots{build: build}).Create(t.Context(), info)
		if err != nil {
			t.Fatal(err)
		}

		verified, err := f.action.Verify(t.Context(), value.ID)
		if err != nil {
			t.Fatal(err)
		}

		if (verified.Formats != nil) != (build == f.current.Tag) {
			t.Fatalf("incorrect producer provenance for %s", build)
		}
	}
}

func TestApplicationDataInspectionNamesTheFileAndTheReaderReason(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, area, path, data, reason string
	}{
		{name: "account", area: applicationStateConfig, path: "garmin-connect.json", data: `{"laterField":1}`, reason: `unknown field "laterField"`},
		{name: "pending verification", area: applicationStateConfig, path: "garmin-connect.json.mfa", data: `{"version":1}`, reason: "no creation time"},
		{name: "upload ledger", area: "cache", path: "uploaded-123.json", data: `null`, reason: "must be a JSON object"},
		{name: "readable account", area: applicationStateConfig, path: "garmin-connect.json", data: `{"email":"reader@example.invalid"}`},
		{name: "opaque watch recovery copy", area: "cache", path: "OUT.BIN-20261101-100000-abc/OUT.BIN", data: "not JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := inspectApplicationData(hostfile.SnapshotFileInfo{Area: tc.area, Path: tc.path}, strings.NewReader(tc.data))
			if tc.reason == "" {
				if err != nil {
					t.Fatal(err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tc.area+"/"+tc.path) || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("refusal=%v, want the file and %q", err, tc.reason)
			}
		})
	}
}
