package updatetransport_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

type verifierHost struct {
	t      *testing.T
	events []string
}

// Wails stages beside the test executable; serialize directory-set assertions.
var verifierStagingMu sync.Mutex

func (h *verifierHost) Emit(name string, _ ...any) bool {
	h.events = append(h.events, name)

	return true
}

func (h *verifierHost) OnEvent(string, func(any)) func() { return func() {} }

func (h *verifierHost) OpenWindow(updater.WindowOptions) updater.WindowHandle {
	h.t.Error("verification test must not open a native window")

	return nil
}

func (h *verifierHost) Quit() { h.t.Error("verification test must not quit or restart the host") }

func stagingDirectories(t *testing.T) []string {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	dirs, err := filepath.Glob(filepath.Join(filepath.Dir(executable), "wails-update-*"))
	if err != nil {
		t.Fatal(err)
	}

	return dirs
}

func TestProviderMandatoryRSAVerificationAndWailsFailureCleanup(t *testing.T) {
	t.Parallel()

	otherKey := otherStagePublicKey(t)
	for _, tc := range []struct {
		name   string
		edit   func(*providerFixture)
		cancel bool
		ready  bool
	}{
		{name: "signed setup", ready: true},
		{name: "signed deb", ready: true, edit: func(f *providerFixture) {
			linuxFixture(f, updatetransport.InstallDEB, "garminctl_1.2.0_linux_amd64.deb")
		}},
		{name: "signed rpm", ready: true, edit: func(f *providerFixture) {
			linuxFixture(f, updatetransport.InstallRPM, "garminctl_1.2.0_linux_amd64.rpm")
		}},
		{name: "tampered same length", edit: func(f *providerFixture) { f.data[0] ^= 0xff }},
		{name: "wrong signature", edit: func(f *providerFixture) { f.sig[0] ^= 0xff }},
		{name: "signed short body", edit: func(f *providerFixture) { f.record.Package.Size++ }},
		{name: "signed overlong body", edit: func(f *providerFixture) { f.record.Package.Size-- }},
		{name: "different trust root", edit: func(f *providerFixture) {
			f.key = otherKey
		}},
		{name: "canceled package response", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fixture := signedFixture(t)
			if tc.edit != nil {
				tc.edit(&fixture)
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			calls := []string{}
			client := assetClient(t, fixture, &calls)
			transport := client.Transport
			client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				response, err := transport.RoundTrip(r)
				if tc.cancel && r.URL.String() == fixture.record.Package.URL {
					cancel()
				}

				return response, err
			})

			provider, err := updatetransport.NewProvider(client, fixture.request, fixture.record, fixture.key)
			if err != nil {
				t.Fatal(err)
			}

			host := &verifierHost{t: t}
			flow := updater.New(host)

			if err := flow.Init(updater.Config{
				CurrentVersion: "1.0.0", Platform: fixture.request.OS, Arch: fixture.request.Arch,
				Providers: []updater.Provider{provider}, Window: updater.WindowNone,
			}); err != nil {
				t.Fatal(err)
			}

			if _, err := flow.Check(ctx); err != nil {
				t.Fatal(err)
			}

			verifierStagingMu.Lock()
			defer verifierStagingMu.Unlock()

			before := stagingDirectories(t)

			err = flow.DownloadAndInstall(ctx)
			if tc.ready {
				assertVerifiedStage(t, flow, fixture.data, err)
			} else if err == nil || flow.DownloadedPath() != "" || flow.State() == updater.StateReady || slices.Contains(host.events, updater.EventUpdateReady) {
				t.Fatalf("failed verification published ready: error=%v path=%s state=%s events=%v", err, flow.DownloadedPath(), flow.State(), host.events)
			}

			if after := stagingDirectories(t); !reflect.DeepEqual(before, after) {
				t.Fatalf("staging leaked: before=%v after=%v", before, after)
			}
		})
	}
}

func linuxFixture(f *providerFixture, kind updatetransport.InstallKind, name string) {
	f.request.OS, f.request.Installation = linuxOS, kind
	f.record.Package.Name = name
	f.record.Signature.Name = name + ".sig"
}

func assertVerifiedStage(t *testing.T, flow *updater.Updater, want []byte, stageErr error) {
	t.Helper()

	path := flow.DownloadedPath()
	if stageErr != nil || path == "" || flow.State() != updater.StateReady {
		t.Fatalf("signed bytes rejected: error=%v path=%s state=%s", stageErr, path, flow.State())
	}
	defer func() {
		if err := os.RemoveAll(filepath.Dir(path)); err != nil {
			t.Error(err)
		}
	}()

	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()

	data, err := root.ReadFile(filepath.Base(path))
	if err != nil || !bytes.Equal(data, want) {
		t.Fatalf("staged bytes=%q error=%v", data, err)
	}
}
