package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

// composedInstallHelper stands in for the OS helper process only; everything
// above it is the installation pipeline the native window receives.
type composedInstallHelper struct {
	backend           *applicationBackendFixture
	prepared, commits int
	config            updatetransport.StagerConfig
	target            updatetransport.MatchRequest
	options           updatetransport.InstallOptions
	digest            string
}

func (h *composedInstallHelper) prepare(_ context.Context, config updatetransport.StagerConfig, target updatetransport.MatchRequest, payload *updatetransport.StagedPackage, options updatetransport.InstallOptions) (application.PendingInstallation, error) {
	h.prepared++
	h.config, h.target, h.options = config, target, options

	if payload == nil {
		return nil, errors.New("helper received no verified package")
	}

	h.digest = payload.Digest()

	return applicationFixturePendingInstaller{commit: h.commit, close: func() error { return nil }}, nil
}

func (h *composedInstallHelper) commit(context.Context) error {
	h.commits++

	select {
	case <-h.backend.done:
	default:
		return errors.New("backend not joined before commit")
	}

	if h.backend.runtime.Manager().Gate().Snapshot().State != lifecycle.Closed {
		return errors.New("backend still admits work at commit")
	}

	return nil
}

func TestApplicationComposedInstallJoinsBackendAndHandsRestartArgumentsToHelper(t *testing.T) { //nolint:paralleltest // Production backend and cached CLI fixture isolate process account/cache paths.
	f := applicationPreflightFixture(t)

	endpoint, err := url.Parse(f.backend.address)
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Transport: downloadTestHTTP(func(*http.Request) (*http.Response, error) {
		f.external.Add(1)
		return nil, errors.New("installation must not fetch release metadata")
	})}
	restart := []string{"-root", "fixture-dump"}
	helper := &composedInstallHelper{backend: f.backend}

	reader, err := f.backend.control.nativeReaderWith(ApplicationReadConfig{
		Inventory:     ApplicationInventoryConfig{DesktopVersion: downloadTestBuild, BackendURL: endpoint, BackendOwned: true, LocalSource: f.local},
		ReleaseClient: client, HistoryDirectory: filepath.Join(t.TempDir(), "history"), RestartArgs: restart,
	}, f.pipeline.key, helper.prepare)
	if err != nil {
		t.Fatal(err)
	}

	result, err := reader.Install(t.Context(), f.request(), application.InstallConsent{Confirmed: true, EditsResolved: true}, nil)
	if err != nil || result == nil || result.State != application.HandoffAccepted || helper.prepared != 1 || helper.commits != 1 {
		t.Fatalf("result=%+v %v prepared=%d commits=%d", result, err, helper.prepared, helper.commits)
	}

	if !slices.Equal(helper.options.Args, restart) || helper.options.Dir == "" || helper.options.AllowDowngrade {
		t.Fatalf("helper options=%+v", helper.options)
	}

	assertComposedInstallTarget(t, f, helper)

	if f.external.Load() != 0 {
		t.Fatal("installation contacted the release feed")
	}
}

func assertComposedInstallTarget(t *testing.T, f *preflightNativeFixture, helper *composedInstallHelper) {
	t.Helper()

	local := f.local.target

	if helper.target.ReleaseTag != f.update.Tag || string(helper.target.Installation) != local.Installation || helper.digest != f.update.Digest {
		t.Fatalf("helper target=%+v digest=%s, want %s %s", helper.target, helper.digest, f.update.Tag, f.update.Digest)
	}

	config := helper.config
	if config.CacheDir != applicationInstallCache() || config.CLIPath != local.CLIPath || helper.options.CLI != local.CLIPath {
		t.Fatalf("helper composition=%+v options=%+v", config, helper.options)
	}

	if config.BuildVersion != local.CLIVersion || len(config.PublicKey) == 0 {
		t.Fatalf("helper build=%q key bytes=%d", config.BuildVersion, len(config.PublicKey))
	}
}
