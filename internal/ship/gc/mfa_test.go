package gc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSSO struct {
	verifyCalls atomic.Int32
	failOAuth   atomic.Bool
	wrongCode   atomic.Bool
}

func (s *fakeSSO) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/mobile/sso/en/sign-in", "/portal/sso/embed":
		w.WriteHeader(http.StatusOK)
	case "/mobile/api/login":
		http.SetCookie(w, &http.Cookie{Name: "GARMIN-SSO", Value: "test-session", Path: "/mobile", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 300})
		_, _ = io.WriteString(w, `{"responseStatus":{"type":"MFA_REQUIRED"},"customerMfaInfo":{"mfaLastMethodUsed":"email"}}`)
	case "/mobile/api/mfa/verifyCode":
		s.verify(w, r)
	case "/oauth_consumer.json":
		_, _ = io.WriteString(w, `{"consumer_key":"test-key","consumer_secret":"test-secret"}`)
	case "/oauth-service/oauth/preauthorized":
		if s.failOAuth.Load() {
			http.Error(w, "synthetic OAuth failure", http.StatusBadGateway)
			return
		}

		_, _ = io.WriteString(w, "oauth_token=test-oauth1&oauth_token_secret=test-secret")
	case "/oauth-service/oauth/exchange/user/2.0":
		_, _ = io.WriteString(w, `{"access_token":"test-access","token_type":"Bearer","expires_in":3600}`)
	default:
		http.NotFound(w, r)
	}
}

func (s *fakeSSO) verify(w http.ResponseWriter, r *http.Request) {
	s.verifyCalls.Add(1)

	cookie, err := r.Cookie("GARMIN-SSO")
	if err != nil || cookie.Value != "test-session" || r.URL.Query().Get("clientId") != clientID {
		http.Error(w, "missing saved cookie or parameters", http.StatusBadRequest)
		return
	}

	var in struct {
		Code string `json:"mfaVerificationCode"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Code != "654321" {
		s.wrongCode.Store(true)
		http.SetCookie(w, &http.Cookie{Name: "retry", Value: "test-retry", Path: "/mobile", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		_, _ = io.WriteString(w, `{"responseStatus":{"type":"ERROR","message":"incorrect code"}}`)

		return
	}

	if s.wrongCode.Load() {
		if _, err := r.Cookie("retry"); err != nil {
			http.Error(w, "lost retry cookie", http.StatusBadRequest)
			return
		}
	}

	_, _ = io.WriteString(w, `{"responseStatus":{"type":"SUCCESSFUL"},"serviceTicketId":"test-ticket"}`)
}

type fakeSSOTransport struct {
	target *url.URL
	base   *http.Transport
}

func (t fakeSSOTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copyReq := req.Clone(req.Context())
	copyReq.URL.Scheme = t.target.Scheme
	copyReq.URL.Host = t.target.Host

	return t.base.RoundTrip(copyReq)
}

func fakeSSOClient(t *testing.T, path string, handler http.Handler) func() *Client {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	u := cookieURL(t, server.URL)
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)

	return func() *Client {
		c := New(path)
		c.HTTP.Transport = fakeSSOTransport{target: u, base: transport}

		return c
	}
}

func TestMFACompletionSurvivesOlderTokenRefresh(t *testing.T) {
	t.Parallel()

	for _, reject := range []bool{false, true} {
		name := "successful old refresh"
		if reject {
			name = "rejected old refresh"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "session.json")
			if err := New(path).saveSession(testSession(oldTestEmail)); err != nil {
				t.Fatal(err)
			}

			entered, gate := make(chan struct{}), make(chan struct{})
			sso := &fakeSSO{}
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/oauth-service/oauth/exchange/user/2.0" && strings.Contains(r.Header.Get("Authorization"), `oauth_token="test-token"`) {
					close(entered)

					select {
					case <-gate:
					case <-r.Context().Done():
						return
					}

					if reject {
						http.Error(w, "old account rejected", http.StatusUnauthorized)
						return
					}
				}

				sso.ServeHTTP(w, r)
			})
			client := fakeSSOClient(t, path, handler)
			release := sync.OnceFunc(func() { close(gate) })
			t.Cleanup(release)

			c := client()
			done := make(chan error, 1)

			go func() {
				_, err := c.bearer(t.Context())
				done <- err
			}()

			<-entered
			startTestLogin(t, c)

			if err := c.ResumeMFA(t.Context(), "654321"); err != nil {
				t.Fatal(err)
			}

			release()

			if err := <-done; !errors.Is(err, ErrNotLoggedIn) {
				t.Fatalf("stale refresh result = %v", err)
			}

			if c.Email() != "test@example.invalid" || !New(path).LoggedIn() {
				t.Fatal("old refresh replaced or removed the newly signed-in account")
			}

			assertSessionFile(t, path, "test@example.invalid")
		})
	}
}

func startTestLogin(t *testing.T, c *Client) {
	t.Helper()

	result, err := c.Login(t.Context(), "test@example.invalid", "test-password")
	if err != nil || !result.NeedsMFA {
		t.Fatalf("start verification: %v", err)
	}
}

func TestMFAResumeFromFreshClient(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.json")
	s := &fakeSSO{}
	client := fakeSSOClient(t, path, s)
	startTestLogin(t, client())

	fresh := client()
	if err := fresh.ResumeMFA(t.Context(), "654321"); err != nil {
		t.Fatal(err)
	}

	if !fresh.LoggedIn() || fresh.sess.OAuth2.AccessToken != "test-access" || !New(path).LoggedIn() {
		t.Fatal("resumed OAuth session was not committed")
	}

	if _, err := os.Stat(path + ".mfa"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed challenge was not removed: %v", err)
	}
}

func TestMFAResumeKeepsSavedDomain(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.json")
	client := fakeSSOClient(t, path, &fakeSSO{})
	c := client()
	c.Domain = "garmin.cn"
	startTestLogin(t, c)

	fresh := client()

	if err := fresh.ResumeMFA(t.Context(), "654321"); err != nil {
		t.Fatal(err)
	}

	if fresh.URL("/test") != "https://connectapi.garmin.cn/test" ||
		!fresh.garminHost(cookieURL(t, "https://connectapi.garmin.cn/test")) ||
		fresh.garminHost(cookieURL(t, "https://connectapi.garmin.com/test")) {
		t.Fatal("resumed account lost its domain or sends tokens to the wrong region")
	}
}

func TestMFAWrongCodeRetainsUpdatedCookies(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.json")
	s := &fakeSSO{}
	client := fakeSSOClient(t, path, s)
	startTestLogin(t, client())

	if err := client().ResumeMFA(t.Context(), "wrong"); err == nil || !strings.Contains(err.Error(), "incorrect code") {
		t.Fatalf("wrong code = %v", err)
	}

	if err := client().ResumeMFA(t.Context(), "654321"); err != nil {
		t.Fatal(err)
	}
}

func TestMFAAcceptedCodeCompletionFailureKeepsPreviousSession(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.json")
	if err := New(path).saveSession(testSession(oldTestEmail)); err != nil {
		t.Fatal(err)
	}

	s := &fakeSSO{}
	s.failOAuth.Store(true)
	client := fakeSSOClient(t, path, s)
	c := client()
	startTestLogin(t, c)

	err := c.ResumeMFA(t.Context(), "654321")
	if err == nil || !strings.Contains(err.Error(), "start cloud login again") {
		t.Fatalf("OAuth failure = %v", err)
	}

	assertSessionFile(t, path, oldTestEmail)

	if c.Email() != oldTestEmail {
		t.Fatal("failed completion changed the loaded account")
	}

	if _, err := os.Stat(path + ".mfa"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("consumed challenge was retained")
	}
}

func TestMFALifetimeAndReplacement(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.json")
	s := &fakeSSO{}
	client := fakeSSOClient(t, path, s)
	c := client()
	startTestLogin(t, c)
	firstID := c.pending.state.ID
	startTestLogin(t, c)

	if c.pending.state.ID == firstID {
		t.Fatal("new login reused an old challenge")
	}

	fresh := client()
	fresh.now = func() time.Time { return time.Now().Add(pendingTTL) }

	if err := fresh.ResumeMFA(t.Context(), "654321"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired challenge = %v", err)
	}

	if s.verifyCalls.Load() != 0 {
		t.Fatal("expired challenge reached SSO")
	}

	startTestLogin(t, client())

	if err := client().Logout(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path + ".mfa"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("logout retained a challenge")
	}
}

func TestMFARejectsInvalidSavedState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(*pendingMFAState)
	}{
		{name: "unknown version", change: func(s *pendingMFAState) { s.Version = 2 }},
		{name: "wrong domain", change: func(s *pendingMFAState) { s.Domain = "evil.invalid" }},
		{name: "wrong service", change: func(s *pendingMFAState) { s.Params.Set("service", "https://evil.invalid") }},
		{name: "future creation", change: func(s *pendingMFAState) { s.CreatedAt = time.Now().Add(time.Hour) }},
		{name: "excessive lifetime", change: func(s *pendingMFAState) { s.ExpiresAt = s.CreatedAt.Add(time.Hour) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "session.json")
			s := &fakeSSO{}
			client := fakeSSOClient(t, path, s)
			c := client()
			startTestLogin(t, c)
			tt.change(&c.pending.state)

			if err := c.savePending(c.pending); err != nil {
				t.Fatal(err)
			}

			if err := client().ResumeMFA(t.Context(), "654321"); err == nil || !strings.Contains(err.Error(), "start cloud login again") {
				t.Fatalf("invalid state = %v", err)
			}

			if s.verifyCalls.Load() != 0 {
				t.Fatal("invalid state reached SSO")
			}
		})
	}
}

func TestMFACancellationAndEmptyCodeDoNotConsumeChallenge(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.json")
	s := &fakeSSO{}
	client := fakeSSOClient(t, path, s)
	startTestLogin(t, client())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := client().ResumeMFA(ctx, "654321"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled verification = %v", err)
	}

	if err := client().ResumeMFA(t.Context(), "  "); err == nil {
		t.Fatal("empty verification code accepted")
	}

	if s.verifyCalls.Load() != 0 {
		t.Fatal("cancelled or empty code reached SSO")
	}

	if err := client().ResumeMFA(t.Context(), "654321"); err != nil {
		t.Fatal(err)
	}
}
