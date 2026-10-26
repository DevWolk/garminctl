package gc

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMFAAcceptedCodeSaveFailureKeepsPreviousSession(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.json")
	if err := New(path).saveSession(testSession(oldTestEmail)); err != nil {
		t.Fatal(err)
	}

	sso := &fakeSSO{}
	server := httptest.NewServer(sso)
	t.Cleanup(server.Close)

	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)

	c := New(path)
	c.HTTP.Transport = fakeSSOTransport{target: cookieURL(t, server.URL), base: transport}
	startTestLogin(t, c)

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMFAWriteLimitHelper$") //nolint:gosec // execute this test binary with a fixed helper selector

	cmd.Env = append(os.Environ(), "GARMINCTL_TEST_MFA_LIMIT_STORE="+path, "GARMINCTL_TEST_MFA_LIMIT_SSO="+server.URL)

	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("MFA write-limit helper: %v\n%s", err, output)
	}

	if sso.verifyCalls.Load() != 1 {
		t.Fatal("save-failure test did not submit the code")
	}

	assertSessionFile(t, path, oldTestEmail)
	assertNoStoreTemps(t, path)

	if _, err := os.Stat(path + ".mfa"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("consumed challenge survived token-save failure")
	}
}

func TestMFAWriteLimitHelper(t *testing.T) { //nolint:paralleltest // isolated helper changes process-wide resource limits
	path := os.Getenv("GARMINCTL_TEST_MFA_LIMIT_STORE")
	if path == "" {
		t.Skip("only run as the MFA write-limit subprocess")
	}

	transport := &http.Transport{}
	defer transport.CloseIdleConnections()

	c := New(path)
	c.HTTP.Transport = fakeSSOTransport{target: cookieURL(t, os.Getenv("GARMINCTL_TEST_MFA_LIMIT_SSO")), base: transport}
	limitSessionWrite(t)

	if err := c.ResumeMFA(t.Context(), "654321"); err == nil || !strings.Contains(err.Error(), "start cloud login again") {
		t.Fatalf("save failure after accepted code = %v", err)
	}

	if c.Email() != oldTestEmail {
		t.Fatal("failed save replaced the loaded account")
	}
}
