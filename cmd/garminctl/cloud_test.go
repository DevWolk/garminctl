package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DevWolk/garminctl/internal/gc"
)

func TestCloudMFAAcrossProcesses(t *testing.T) {
	t.Parallel()

	sso := &cloudAuthSSO{}
	server := httptest.NewServer(sso)
	t.Cleanup(server.Close)

	config := t.TempDir()

	storeDir := config
	if runtime.GOOS == "darwin" {
		storeDir = filepath.Join(config, "Library", "Application Support")
	}

	path := filepath.Join(storeDir, "garminctl", "garmin-connect.json")

	output := runCloudAuthProcess(t, server.URL, config, "cloud", "login", "-email", "test@example.invalid")
	if !strings.Contains(output, "garminctl cloud mfa CODE") || sso.verifyCalls.Load() != 0 {
		t.Fatal("noninteractive login submitted an empty code or did not explain continuation")
	}

	if _, err := os.Stat(path + ".mfa"); err != nil {
		t.Fatalf("login did not persist the challenge: %v", err)
	}

	output = runCloudAuthProcess(t, server.URL, config, "cloud", "mfa", "654321")
	if !strings.Contains(output, "signed in as test@example.invalid") || sso.verifyCalls.Load() != 1 {
		t.Fatal("second process did not finish verification")
	}

	if c := gc.New(path); c.StoreError() != nil || !c.LoggedIn() {
		t.Fatalf("OAuth tokens not persisted by second process: %v", c.StoreError())
	}

	if _, err := os.Stat(path + ".mfa"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("completed challenge retained")
	}
}

type cloudAuthSSO struct {
	verifyCalls atomic.Int32
}

func (s *cloudAuthSSO) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/mobile/sso/en/sign-in", "/portal/sso/embed":
		w.WriteHeader(http.StatusOK)
	case "/mobile/api/login":
		http.SetCookie(w, &http.Cookie{Name: "GARMIN-SSO", Value: "test-session", Path: "/mobile", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		_, _ = io.WriteString(w, `{"responseStatus":{"type":"MFA_REQUIRED"}}`)
	case "/mobile/api/mfa/verifyCode":
		s.verifyCalls.Add(1)

		cookie, err := r.Cookie("GARMIN-SSO")

		b, readErr := io.ReadAll(r.Body)
		if err != nil || cookie.Value != "test-session" || readErr != nil || !bytes.Contains(b, []byte(`"mfaVerificationCode":"654321"`)) {
			http.Error(w, "missing code or persisted cookie", http.StatusBadRequest)
			return
		}

		_, _ = io.WriteString(w, `{"responseStatus":{"type":"SUCCESSFUL"},"serviceTicketId":"test-ticket"}`)
	case "/oauth_consumer.json":
		_, _ = io.WriteString(w, `{"consumer_key":"test-key","consumer_secret":"test-secret"}`)
	case "/oauth-service/oauth/preauthorized":
		_, _ = io.WriteString(w, "oauth_token=test-oauth1&oauth_token_secret=test-secret")
	case "/oauth-service/oauth/exchange/user/2.0":
		_, _ = io.WriteString(w, `{"access_token":"test-access","token_type":"Bearer","expires_in":3600}`)
	default:
		http.NotFound(w, r)
	}
}

func runCloudAuthProcess(t *testing.T, server, config string, args ...string) string {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	command := append([]string{"-test.run=^TestCloudAuthProcess$", "--"}, args...)
	cmd := exec.CommandContext(t.Context(), executable, command...) //nolint:gosec // run this test binary with fixed test-owned arguments

	cmd.Env = append(os.Environ(), "GARMINCTL_TEST_SSO_URL="+server, "HOME="+config,
		"XDG_CONFIG_HOME="+config, "APPDATA="+config, "GARMIN_PASSWORD=test-password")
	cmd.Stdin = strings.NewReader("")

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cloud helper: %v\n%s", err, output)
	}

	return string(output)
}

func TestCloudAuthProcess(t *testing.T) { //nolint:paralleltest // isolated subprocess changes its HTTP transport and command input
	endpoint := os.Getenv("GARMINCTL_TEST_SSO_URL")
	if endpoint == "" {
		t.Skip("only run as the fake-SSO CLI subprocess")
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}

	base := &http.Transport{}
	defer base.CloseIdleConnections()

	http.DefaultTransport = cloudAuthTransport{target: u, base: base} // only this isolated helper redirects requests to the local fake SSO

	for i, arg := range os.Args {
		if arg == "--" {
			if err := run(os.Args[i+1:], os.Stdout, os.Stderr); err != nil {
				t.Fatal(err)
			}

			return
		}
	}

	t.Fatal("missing helper arguments")
}

type cloudAuthTransport struct {
	target *url.URL
	base   *http.Transport
}

func (t cloudAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copyReq := req.Clone(req.Context())
	copyReq.URL.Scheme = t.target.Scheme
	copyReq.URL.Host = t.target.Host

	return t.base.RoundTrip(copyReq)
}
