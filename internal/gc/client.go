// Package gc talks to Garmin Connect the way the Garmin Connect Mobile app
// does: Garmin SSO sign-in, an OAuth 1.0 token from the preauthorized
// endpoint, exchanged for an OAuth 2.0 bearer used against connectapi.
package gc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	DefaultDomain = "garmin.com"
	clientID      = "GCM_ANDROID_DARK"
	consumerURL   = "https://thegarth.s3.amazonaws.com/oauth_consumer.json"
	ssoUserAgent  = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148"
	oauthUA       = "com.garmin.android.apps.connectmobile"
	apiUserAgent  = "GCM-iOS-5.22.1.4"
)

// ErrNotLoggedIn is returned when no valid session exists.
var ErrNotLoggedIn = errors.New("not signed in to Garmin Connect")

// OAuth1Token is the long-lived token (about a year).
type OAuth1Token struct {
	Token         string `json:"oauth_token"`
	Secret        string `json:"oauth_token_secret"`
	MFAToken      string `json:"mfa_token,omitempty"`
	MFAExpiration string `json:"mfa_expiration_timestamp,omitempty"`
	Domain        string `json:"domain"`
}

// OAuth2Token is the short-lived bearer (about an hour).
type OAuth2Token struct {
	Scope            string `json:"scope"`
	JTI              string `json:"jti"`
	TokenType        string `json:"token_type"`
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int64  `json:"expires_in"`
	ExpiresAt        int64  `json:"expires_at"`
	RefreshExpiresIn int64  `json:"refresh_token_expires_in"`
	RefreshExpiresAt int64  `json:"refresh_token_expires_at"`
}

func (t *OAuth2Token) expired() bool { return t == nil || t.ExpiresAt-60 < time.Now().Unix() }

// Session is what gets persisted.
type Session struct {
	Email   string       `json:"email"`
	OAuth1  *OAuth1Token `json:"oauth1"`
	OAuth2  *OAuth2Token `json:"oauth2"`
	SavedAt time.Time    `json:"savedAt"`
}

type consumer struct {
	Key    string `json:"consumer_key"`
	Secret string `json:"consumer_secret"`
}

// Client is a Garmin Connect session. Safe for concurrent use.
type Client struct {
	Domain string
	HTTP   *http.Client

	mu       sync.Mutex
	store    string
	sess     Session
	consumer *consumer
	pending  *pendingLogin
}

type pendingLogin struct {
	http      *http.Client
	params    url.Values
	mfaMethod string
	email     string
}

// DefaultStorePath is ~/.config/garminctl/garmin-connect.json.
func DefaultStorePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "garminctl", "garmin-connect.json")
}

// New creates a client and loads a saved session from store, if any.
func New(store string) *Client {
	c := &Client{Domain: DefaultDomain, HTTP: &http.Client{Timeout: 5 * time.Minute}, store: store}
	if b, err := os.ReadFile(store); err == nil {
		var s Session
		if json.Unmarshal(b, &s) == nil && s.OAuth1 != nil {
			c.sess = s
			if s.OAuth1.Domain != "" {
				c.Domain = s.OAuth1.Domain
			}
		}
	}
	return c
}

// LoggedIn reports whether a usable session is stored.
func (c *Client) LoggedIn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess.OAuth1 != nil && c.sess.OAuth1.Token != ""
}

// Email returns the signed-in address.
func (c *Client) Email() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess.Email
}

// Logout forgets the session.
func (c *Client) Logout() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sess = Session{}
	c.pending = nil
	if err := os.Remove(c.store); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (c *Client) save() error {
	if c.store == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.store), 0o700); err != nil {
		return err
	}
	c.sess.SavedAt = time.Now()
	b, _ := json.MarshalIndent(c.sess, "", " ")
	return os.WriteFile(c.store, b, 0o600)
}

func (c *Client) sso(path string) string { return "https://sso." + c.Domain + path }
func (c *Client) api(path string) string { return "https://connectapi." + c.Domain + path }

func ssoHeaders(req *http.Request, site string) {
	req.Header.Set("User-Agent", ssoUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Site", site)
}

// LoginResult says whether a second factor is needed.
type LoginResult struct {
	NeedsMFA  bool   `json:"needsMfa"`
	MFAMethod string `json:"mfaMethod,omitempty"`
}

type ssoResponse struct {
	ResponseStatus struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"responseStatus"`
	ServiceTicketID string `json:"serviceTicketId"`
	CustomerMfaInfo struct {
		MfaLastMethodUsed string `json:"mfaLastMethodUsed"`
	} `json:"customerMfaInfo"`
}

// Login signs in with email and password. If Garmin asks for a second
// factor the result says so and ResumeMFA completes the sign-in.
func (c *Client) Login(ctx context.Context, email, password string) (*LoginResult, error) {
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar, Timeout: c.HTTP.Timeout}
	params := url.Values{
		"clientId": {clientID},
		"locale":   {"en-US"},
		"service":  {"https://mobile.integration." + c.Domain + "/gcm/android"},
	}
	signin := c.sso("/mobile/sso/en/sign-in") + "?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, signin, nil)
	ssoHeaders(req, "none")
	if resp, err := hc.Do(req); err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	} else {
		return nil, fmt.Errorf("sso: %w", err)
	}
	body, _ := json.Marshal(map[string]any{"username": email, "password": password, "rememberMe": false, "captchaToken": ""})
	var r ssoResponse
	if err := c.ssoJSON(ctx, hc, c.sso("/mobile/api/login")+"?"+params.Encode(), signin, body, &r); err != nil {
		return nil, err
	}
	switch r.ResponseStatus.Type {
	case "SUCCESSFUL":
		return &LoginResult{}, c.complete(ctx, hc, email, r.ServiceTicketID)
	case "MFA_REQUIRED":
		method := r.CustomerMfaInfo.MfaLastMethodUsed
		if method == "" {
			method = "email"
		}
		c.mu.Lock()
		c.pending = &pendingLogin{http: hc, params: params, mfaMethod: method, email: email}
		c.mu.Unlock()
		return &LoginResult{NeedsMFA: true, MFAMethod: method}, nil
	}
	msg := r.ResponseStatus.Message
	if msg == "" {
		msg = r.ResponseStatus.Type
	}
	if msg == "" {
		msg = "unexpected answer from Garmin sign-in"
	}
	return nil, fmt.Errorf("sign-in failed: %s", msg)
}

// ResumeMFA finishes a sign-in that asked for a verification code.
func (c *Client) ResumeMFA(ctx context.Context, code string) error {
	c.mu.Lock()
	p := c.pending
	c.mu.Unlock()
	if p == nil {
		return errors.New("no sign-in is waiting for a code; start again with Login")
	}
	body, _ := json.Marshal(map[string]any{"mfaMethod": p.mfaMethod, "mfaVerificationCode": strings.TrimSpace(code), "rememberMyBrowser": false, "reconsentList": []any{}, "mfaSetup": false})
	var r ssoResponse
	if err := c.ssoJSON(ctx, p.http, c.sso("/mobile/api/mfa/verifyCode")+"?"+p.params.Encode(), c.sso("/mobile/sso/en/sign-in"), body, &r); err != nil {
		return err
	}
	if r.ResponseStatus.Type != "SUCCESSFUL" || r.ServiceTicketID == "" {
		msg := r.ResponseStatus.Message
		if msg == "" {
			msg = r.ResponseStatus.Type
		}
		return fmt.Errorf("verification failed: %s", msg)
	}
	c.mu.Lock()
	c.pending = nil
	c.mu.Unlock()
	return c.complete(ctx, p.http, p.email, r.ServiceTicketID)
}

func (c *Client) ssoJSON(ctx context.Context, hc *http.Client, u, referer string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	ssoHeaders(req, "same-origin")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", referer)
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("sso: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusTooManyRequests {
		return errors.New("Garmin sign-in is rate limiting this computer; wait a few minutes and try again")
	}
	if err := json.Unmarshal(b, out); err != nil {
		if resp.StatusCode >= 400 {
			return fmt.Errorf("sso answered %s", resp.Status)
		}
		return fmt.Errorf("sso: unexpected answer (%s)", resp.Status)
	}
	return nil
}

func (c *Client) complete(ctx context.Context, hc *http.Client, email, ticket string) error {
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.sso("/portal/sso/embed"), nil); err == nil {
		ssoHeaders(req, "same-origin")
		req.Header.Set("Referer", c.sso("/mobile/sso/en/sign-in"))
		if resp, err := hc.Do(req); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}
	o1, err := c.preauthorized(ctx, ticket)
	if err != nil {
		return err
	}
	o2, err := c.exchange(ctx, o1, true)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sess = Session{Email: email, OAuth1: o1, OAuth2: o2}
	return c.save()
}

func (c *Client) loadConsumer(ctx context.Context) (*consumer, error) {
	c.mu.Lock()
	k := c.consumer
	c.mu.Unlock()
	if k != nil {
		return k, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, consumerURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth consumer: %w", err)
	}
	defer resp.Body.Close()
	var k2 consumer
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&k2); err != nil || k2.Key == "" {
		return nil, errors.New("oauth consumer: unexpected answer")
	}
	c.mu.Lock()
	c.consumer = &k2
	c.mu.Unlock()
	return &k2, nil
}

func (c *Client) preauthorized(ctx context.Context, ticket string) (*OAuth1Token, error) {
	k, err := c.loadConsumer(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{"ticket": {ticket}, "login-url": {"https://mobile.integration." + c.Domain + "/gcm/android"}, "accepts-mfa-tokens": {"true"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.api("/oauth-service/oauth/preauthorized")+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", oauthUA)
	signOAuth1(req, nil, k.Key, k.Secret, "", "")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth1: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth1 token request answered %s", resp.Status)
	}
	vals, err := url.ParseQuery(string(b))
	if err != nil || vals.Get("oauth_token") == "" {
		return nil, errors.New("oauth1: unexpected answer")
	}
	return &OAuth1Token{Token: vals.Get("oauth_token"), Secret: vals.Get("oauth_token_secret"), MFAToken: vals.Get("mfa_token"), MFAExpiration: vals.Get("mfa_expiration_timestamp"), Domain: c.Domain}, nil
}

func (c *Client) exchange(ctx context.Context, o1 *OAuth1Token, login bool) (*OAuth2Token, error) {
	k, err := c.loadConsumer(ctx)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	if login {
		form.Set("audience", "GARMIN_CONNECT_MOBILE_ANDROID_DI")
	}
	if o1.MFAToken != "" {
		form.Set("mfa_token", o1.MFAToken)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.api("/oauth-service/oauth/exchange/user/2.0"), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", oauthUA)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	signOAuth1(req, form, k.Key, k.Secret, o1.Token, o1.Secret)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth2: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth2 exchange answered %s", resp.Status)
	}
	var t OAuth2Token
	if err := json.Unmarshal(b, &t); err != nil || t.AccessToken == "" {
		return nil, errors.New("oauth2: unexpected answer")
	}
	now := time.Now().Unix()
	t.ExpiresAt = now + t.ExpiresIn
	t.RefreshExpiresAt = now + t.RefreshExpiresIn
	return &t, nil
}

// bearer returns a valid access token, refreshing it when needed.
func (c *Client) bearer(ctx context.Context) (string, error) {
	c.mu.Lock()
	o1, o2 := c.sess.OAuth1, c.sess.OAuth2
	c.mu.Unlock()
	if o1 == nil || o1.Token == "" {
		return "", ErrNotLoggedIn
	}
	if !o2.expired() {
		return o2.TokenType + " " + o2.AccessToken, nil
	}
	n2, err := c.exchange(ctx, o1, false)
	if err != nil {
		if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "403") {
			_ = c.Logout()
			return "", fmt.Errorf("%w: the saved session was rejected, sign in again", ErrNotLoggedIn)
		}
		return "", err
	}
	c.mu.Lock()
	c.sess.OAuth2 = n2
	err = c.save()
	c.mu.Unlock()
	if err != nil {
		return "", err
	}
	return n2.TokenType + " " + n2.AccessToken, nil
}

// URL resolves a connectapi path ("/x") or returns an absolute URL as is.
func (c *Client) URL(p string) string {
	if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
		return p
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return c.api(p)
}

// HTTPError is a non-2xx answer.
type HTTPError struct {
	Status int
	Body   string
	URL    string
}

func (e *HTTPError) Error() string {
	b := strings.TrimSpace(e.Body)
	if len(b) > 200 {
		b = b[:200] + "…"
	}
	if b == "" {
		return fmt.Sprintf("Garmin answered HTTP %d for %s", e.Status, e.URL)
	}
	return fmt.Sprintf("Garmin answered HTTP %d: %s", e.Status, b)
}

// Do performs an authenticated request and returns status and body.
func (c *Client) Do(ctx context.Context, method, p string, contentType string, body io.Reader) (int, []byte, error) {
	auth, err := c.bearer(ctx)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL(p), body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("User-Agent", apiUserAgent)
	req.Header.Set("Accept", "application/json, */*")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, b, nil
}

// DoOAuth1 performs a request to an absolute URL signed with the session's
// OAuth1 token, the way Garmin Express talks to services.garmin.com.
func (c *Client) DoOAuth1(ctx context.Context, method, absURL, contentType string, body []byte) (int, []byte, error) {
	c.mu.Lock()
	o1 := c.sess.OAuth1
	c.mu.Unlock()
	if o1 == nil {
		return 0, nil, errors.New("not signed in to Garmin Connect")
	}
	cons, err := c.loadConsumer(ctx)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, absURL, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", apiUserAgent)
	req.Header.Set("Accept", "application/json, */*")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	signOAuth1(req, nil, cons.Key, cons.Secret, o1.Token, o1.Secret)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return resp.StatusCode, b, err
}

// GetJSON fetches JSON.
func (c *Client) GetJSON(ctx context.Context, p string, out any) error {
	st, b, err := c.Do(ctx, http.MethodGet, p, "", nil)
	if err != nil {
		return err
	}
	if st < 200 || st > 299 {
		return &HTTPError{Status: st, Body: string(b), URL: p}
	}
	if out == nil || len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}

// SendJSON sends a JSON body with the given method and decodes the answer.
func (c *Client) SendJSON(ctx context.Context, method, p string, in any, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	st, b, err := c.Do(ctx, method, p, "application/json", body)
	if err != nil {
		return err
	}
	if st < 200 || st > 299 {
		return &HTTPError{Status: st, Body: string(b), URL: p}
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

// Download fetches bytes from a connectapi path or absolute URL.
func (c *Client) Download(ctx context.Context, p string) ([]byte, error) {
	st, b, err := c.Do(ctx, http.MethodGet, p, "", nil)
	if err != nil {
		return nil, err
	}
	if st < 200 || st > 299 {
		return nil, &HTTPError{Status: st, Body: string(b), URL: p}
	}
	return b, nil
}

// Upload posts one file as multipart form field "file".
func (c *Client) Upload(ctx context.Context, p, filename string, data []byte) (int, []byte, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		return 0, nil, err
	}
	_, _ = fw.Write(data)
	_ = w.Close()
	return c.Do(ctx, http.MethodPost, p, w.FormDataContentType(), &buf)
}
