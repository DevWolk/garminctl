package app

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/gc"
	"github.com/DevWolk/garminctl/internal/ship/paths"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// signInTransport answers Garmin sign-in with a second-factor challenge and
// accepts any code, so gc writes its pending and account files itself.
type signInTransport struct{}

func (signInTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()

	switch req.URL.Path {
	case "/mobile/sso/en/sign-in", "/portal/sso/embed":
		w.WriteHeader(http.StatusOK)
	case "/mobile/api/login":
		http.SetCookie(w, &http.Cookie{Name: "GARMIN-SSO", Value: "synthetic", Path: "/mobile", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		_, _ = io.WriteString(w, `{"responseStatus":{"type":"MFA_REQUIRED"},"customerMfaInfo":{"mfaLastMethodUsed":"email"}}`)
	case "/mobile/api/mfa/verifyCode":
		_, _ = io.WriteString(w, `{"responseStatus":{"type":"SUCCESSFUL"},"serviceTicketId":"synthetic-ticket"}`)
	case "/oauth_consumer.json":
		_, _ = io.WriteString(w, `{"consumer_key":"synthetic-key","consumer_secret":"synthetic-secret"}`)
	case "/oauth-service/oauth/preauthorized":
		_, _ = io.WriteString(w, "oauth_token=synthetic&oauth_token_secret=synthetic")
	case "/oauth-service/oauth/exchange/user/2.0":
		_, _ = io.WriteString(w, `{"access_token":"synthetic","token_type":"Bearer","expires_in":3600}`)
	default:
		return nil, errors.New("unexpected sign-in request: " + req.URL.String())
	}

	resp := w.Result()
	resp.Request = req

	return resp, nil
}

// TestApplicationBackupSelectsStateWrittenByItsOwners lets gc, connect and apps
// write their files and checks that a data backup selects each one, so a
// renamed file in any owner fails here rather than drop out of backups.
func TestApplicationBackupSelectsStateWrittenByItsOwners(t *testing.T) {
	testdump.AppEnvironment(t)

	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("APPDATA", config)

	configRoot, _, ok := applicationDataRoot()
	if !ok {
		t.Fatal("application data directory is unavailable")
	}

	if err := os.MkdirAll(configRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	account := gc.New(gc.DefaultStorePath())
	account.HTTP.Transport = signInTransport{}

	assertBackupSelectsNewState(t, application.StateAreaConfig, configRoot, func() error {
		_, err := account.Login(t.Context(), "owner@example.invalid", "synthetic")
		return err
	})
	assertBackupSelectsNewState(t, application.StateAreaConfig, configRoot, func() error { return account.ResumeMFA(t.Context(), "123456") })

	root := testdump.Root(t)
	runtime := NewRuntime(t.Context(), func(context.Context) (device.Device, error) { return device.NewDirFS(root) })
	t.Cleanup(func() { _ = runtime.Close(context.WithoutCancel(t.Context())) })

	assertBackupSelectsNewState(t, application.StateAreaCache, paths.Cache(), func() error {
		lease, err := runtime.Manager().Acquire(t.Context())
		if err != nil {
			return err
		}

		_, err = ConnectFor(runtime, account)(lease.Session()).Upload(lease.Context(), nil)

		return errors.Join(err, lease.Release())
	})

	dev, _ := testdump.Apps(t)
	sess := watchsession.New(dev)

	// The second change also copies the pending IN.BIN the first one queued.
	for range 2 {
		assertBackupSelectsNewState(t, application.StateAreaCache, paths.Cache(), func() error {
			_, err := (apps.VisibilityAction{}).Run(t.Context(), sess, testdump.AppRef, true)
			return err
		})
	}

	assertBackupSelectsNewState(t, application.StateAreaCache, paths.Cache(), func() error {
		_, err := apps.NewApplySettingsAction(new(apps.SchemaCache)).Run(t.Context(), sess, testdump.AppRef, map[string]any{"Mode": "2"})
		return err
	})
}

// assertBackupSelectsNewState runs one owner step and requires every file it
// created under root to be selected, together with its recovery directory.
func assertBackupSelectsNewState(t *testing.T, area, root string, step func() error) {
	t.Helper()

	before := stateFiles(t, root)

	if err := step(); err != nil {
		t.Fatal(err)
	}

	created := 0

	for name := range stateFiles(t, root) {
		if before[name] {
			continue
		}

		created++

		if got := application.StatePath(area, name); got != application.StateFile {
			t.Errorf("%s/%s is not backed up: kind %d", area, name, got)
		}

		if dir := path.Dir(name); dir != "." && application.StatePath(area, dir) != application.StateDirectory {
			t.Errorf("%s/%s is not a backed-up recovery directory", area, dir)
		}
	}

	if created == 0 {
		t.Fatalf("the step wrote no file under %s", area)
	}
}

func stateFiles(t *testing.T, root string) map[string]bool {
	t.Helper()

	files := map[string]bool{}

	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}

		relative, err := filepath.Rel(root, name)
		files[filepath.ToSlash(relative)] = true

		return err
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}

	return files
}
