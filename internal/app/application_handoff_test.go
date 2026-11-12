package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

type applicationFixtureInstaller struct {
	prepare func(context.Context, application.InstallationInput) (application.PendingInstallation, error)
}

func (f applicationFixtureInstaller) Prepare(ctx context.Context, input application.InstallationInput) (application.PendingInstallation, error) {
	return f.prepare(ctx, input)
}

type applicationFixturePendingInstaller struct {
	commit func(context.Context) error
	close  func() error
}

func (f applicationFixturePendingInstaller) Commit(ctx context.Context) error { return f.commit(ctx) }
func (f applicationFixturePendingInstaller) Close() error                     { return f.close() }

func TestApplicationHandoffJoinsRealOwnedBackendBeforeFakeCommit(t *testing.T) { //nolint:paralleltest // Production backend and cached CLI fixture isolate process account/cache paths.
	f := applicationPreflightFixture(t)

	prepared, err := f.reader.Preflight(t.Context(), f.request())
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := f.reader.BackupData(t.Context(), prepared)
	if err != nil {
		t.Fatal(err)
	}

	commits, closes := 0, 0

	var payload string

	installer := applicationFixtureInstaller{prepare: func(_ context.Context, input application.InstallationInput) (application.PendingInstallation, error) {
		if input.Info != prepared.Info() || input.Snapshot.ID != snapshot.ID {
			t.Fatal("owned preparation changed")
		}

		if _, err := os.Stat(filepath.Join(input.Snapshot.Directory, "manifest.json")); err != nil {
			t.Fatal(err)
		}

		payload = copyApplicationHandoffFixture(t, input)

		return applicationFixturePendingInstaller{commit: func(context.Context) error {
			commits++

			select {
			case <-f.backend.done:
			default:
				t.Fatal("backend not joined before commit")
			}

			if f.backend.runtime.Manager().Gate().Snapshot().State != lifecycle.Closed {
				t.Fatal("handoff reopened runtime admission")
			}

			return nil
		}, close: func() error { closes++; return nil }}, nil
	}}
	stop := func(ctx context.Context) error {
		f.backend.cancel()

		select {
		case <-f.backend.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	result, err := application.NewInstallHandoffAction(f.reader.ApplicationReads, installer, stop).Run(t.Context(), prepared, application.InstallConsent{Confirmed: true, EditsResolved: true})
	assertApplicationHandoffAccepted(t, f, prepared, snapshot, payload, result, err, commits, closes)
}

func copyApplicationHandoffFixture(t *testing.T, input application.InstallationInput) string {
	t.Helper()
	owned := t.TempDir()

	for _, pkg := range []application.VerifiedPackage{input.Current, input.Update} {
		value, ok := pkg.(*verifiedApplicationPackage)
		if !ok || value.staged == nil {
			t.Fatal("preflight did not lend actual cached CLI verified copies")
		}

		path := value.staged.Path()

		data := readApplicationState(t, filepath.Dir(path), filepath.Base(path))
		if int64(len(data)) != pkg.Record().Bytes {
			t.Fatal("verified package no longer has its exact size")
		}

		writeApplicationState(t, owned, pkg.Record().ID+".pkg", data)

		path = value.staged.SignaturePath()
		writeApplicationState(t, owned, pkg.Record().ID+".sig", readApplicationState(t, filepath.Dir(path), filepath.Base(path)))
	}

	return owned
}

func TestApplicationHandoffPrepareFailureKeepsOwnedBackendRunning(t *testing.T) { //nolint:paralleltest // Production fixture isolates process account/cache paths.
	f := applicationPreflightFixture(t)

	prepared, err := f.reader.Preflight(t.Context(), f.request())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.reader.BackupData(t.Context(), prepared); err != nil {
		t.Fatal(err)
	}

	failure := errors.New("fixture installer unavailable")
	installer := applicationFixtureInstaller{prepare: func(context.Context, application.InstallationInput) (application.PendingInstallation, error) {
		return nil, failure
	}}
	stop := func(context.Context) error { t.Fatal("preparation failure attempted backend stop"); return nil }

	result, err := application.NewInstallHandoffAction(f.reader.ApplicationReads, installer, stop).Run(t.Context(), prepared, application.InstallConsent{Confirmed: true, EditsResolved: true})
	if !errors.Is(err, failure) || result == nil || result.State != application.HandoffUncommitted {
		t.Fatalf("handoff=%+v %v", result, err)
	}

	if f.backend.runtime.Manager().Gate().Snapshot().State != lifecycle.Running {
		t.Fatal("preparation failure stranded maintenance")
	}

	f.assertClean(t)
}

func assertApplicationHandoffAccepted(t *testing.T, f *preflightNativeFixture, prepared *application.PreparedUpdate, snapshot *application.DataSnapshot, payload string, result *application.InstallHandoffResult, err error, commits, closes int) {
	t.Helper()

	if err != nil || result == nil || result.State != application.HandoffAccepted || commits != 1 || closes != 1 {
		t.Fatalf("handoff=%+v %v commits=%d closes=%d", result, err, commits, closes)
	}

	assertDownloadOperationsClean(t, f.pipeline)

	if got := readApplicationState(t, payload, prepared.Info().Update.ID+".pkg"); !bytes.Equal(got, downloadTestBytes) {
		t.Fatal("handoff lost its independently owned fixture package")
	}

	if _, err := os.Stat(filepath.Join(snapshot.Directory, "manifest.json")); err != nil {
		t.Fatal("snapshot removed after handoff")
	}

	if f.external.Load() != 0 {
		t.Fatal("handoff fetched release metadata")
	}
}
