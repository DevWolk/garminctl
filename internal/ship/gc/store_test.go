package gc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const oldTestEmail = "old@example.invalid"

func testSession(email string) Session {
	return Session{Email: email, OAuth1: &OAuth1Token{Token: "test-token", Domain: "garmin.cn"}}
}

func TestSessionStoreRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config", "session.json")
	c := New(path)

	if err := c.StoreError(); err != nil {
		t.Fatal(err)
	}

	if c.LoggedIn() {
		t.Fatal("new store is already signed in")
	}

	if err := c.saveSession(testSession("test@example.invalid")); err != nil {
		t.Fatal(err)
	}

	fresh := New(path)
	if err := fresh.StoreError(); err != nil {
		t.Fatal(err)
	}

	if !fresh.LoggedIn() || fresh.Email() != c.Email() || fresh.Domain != "garmin.cn" || fresh.sess.SavedAt.IsZero() {
		t.Fatal("saved account was not restored")
	}

	if err := c.Logout(); err != nil {
		t.Fatal(err)
	}

	if !fresh.LoggedIn() || New(path).LoggedIn() {
		t.Fatal("existing clients must keep their account snapshot; fresh clients must observe logout")
	}

	assertNoStoreTemps(t, path)
}

func TestSessionStoreReportsErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path func(*testing.T) string
	}{
		{name: "unavailable config", path: func(*testing.T) string { return "" }},
		{name: "malformed session", path: func(t *testing.T) string {
			t.Helper()

			path := filepath.Join(t.TempDir(), "session.json")
			if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}

			return path
		}},
		{name: "file used as directory", path: func(t *testing.T) string {
			t.Helper()

			path := filepath.Join(t.TempDir(), "not-a-directory")
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}

			return filepath.Join(path, "session.json")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := New(tt.path(t))
			if c.StoreError() == nil || c.LoggedIn() {
				t.Fatal("invalid store was silently treated as signed out")
			}

			if _, err := c.Login(t.Context(), "test@example.invalid", "unused"); err == nil {
				t.Fatal("Login ignored the store error")
			}

			if err := c.ResumeMFA(t.Context(), "123456"); err == nil {
				t.Fatal("ResumeMFA ignored the store error")
			}

			if _, err := c.bearer(context.Background()); err == nil || errors.Is(err, ErrNotLoggedIn) {
				t.Fatal("bearer hid the store error")
			}
		})
	}
}

func TestLogoutRemovesMalformedSession(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := New(path)

	if err := c.Logout(); err != nil {
		t.Fatal(err)
	}

	if c.StoreError() != nil || c.LoggedIn() {
		t.Fatal("logout did not clear the failed account snapshot")
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("logout left the account file: %v", err)
	}
}

func TestDefaultStoreUnavailable(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("APPDATA", "")

	path := DefaultStorePath()
	if path != "" || New(path).StoreError() == nil {
		t.Fatal("unavailable user config silently fell back to a working-directory token file")
	}
}

func TestSessionReplacementFailureKeepsAccount(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.json")
	c := New(path)

	if err := c.saveSession(testSession(oldTestEmail)); err != nil {
		t.Fatal(err)
	}

	// A directory at the destination makes replacement fail on both platforms.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := c.saveSession(testSession("new@example.invalid")); err == nil {
		t.Fatal("replacement unexpectedly succeeded")
	}

	if c.Email() != oldTestEmail {
		t.Fatal("failed save published new credentials in memory")
	}

	assertNoStoreTemps(t, path)
}

func assertNoStoreTemps(t *testing.T, path string) {
	t.Helper()

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".garminctl-") {
			t.Fatal("session temporary file was not removed")
		}
	}
}

func assertSessionFile(t *testing.T, path, email string) {
	t.Helper()

	b, err := os.ReadFile(path) //nolint:gosec // test-owned session under t.TempDir
	if err != nil {
		t.Fatal(err)
	}

	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}

	if s.Email != email {
		t.Fatal("previous session was replaced after a failed save")
	}
}
