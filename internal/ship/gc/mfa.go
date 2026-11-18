package gc

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"
)

const pendingTTL = 10 * time.Minute

func connectBase(domain string) (string, error) {
	switch domain {
	case DefaultDomain:
		return "https://connectapi.garmin.com", nil
	case "garmin.cn":
		return "https://connectapi.garmin.cn", nil
	default:
		return "", errors.New("unsupported Garmin account domain")
	}
}

func (c *Client) prepareLogin(ctx context.Context) error {
	if err := c.StoreError(); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if c.Domain != DefaultDomain && c.Domain != "garmin.cn" {
		return errors.New("unsupported Garmin sign-in domain")
	}

	return c.clearPending()
}

type pendingMFAState struct {
	Version   int           `json:"version"`
	ID        string        `json:"id"`
	Domain    string        `json:"domain"`
	Email     string        `json:"email"`
	Method    string        `json:"method"`
	Params    url.Values    `json:"params"`
	Cookies   []savedCookie `json:"cookies"`
	CreatedAt time.Time     `json:"createdAt"`
	ExpiresAt time.Time     `json:"expiresAt"`
}

type pendingLogin struct {
	http  *http.Client
	jar   *recordingJar
	state pendingMFAState
}

func (c *Client) ssoClient(jar *recordingJar) *http.Client {
	hc := *c.HTTP
	hc.Jar = jar

	return &hc
}

func (c *Client) savePending(p *pendingLogin) error {
	state := p.state

	var err error

	state.Cookies, err = p.jar.snapshot()
	if err != nil {
		return err
	}

	b, err := json.Marshal(state)
	if err != nil {
		return err
	}

	return writeStore(c.store+".mfa", b)
}

func (c *Client) startPending(hc *http.Client, jar *recordingJar, params url.Values, email, method string) error {
	now := c.now()

	p := &pendingLogin{http: hc, jar: jar, state: pendingMFAState{
		Version: 1, ID: rand.Text(), Domain: c.Domain, Email: email, Method: method,
		Params: params, CreatedAt: now, ExpiresAt: now.Add(pendingTTL),
	}}
	if err := c.savePending(p); err != nil {
		return fmt.Errorf("save verification session; start cloud login again: %w", err)
	}

	c.pending = p

	return nil
}

func (c *Client) loadPending() (*pendingLogin, error) {
	p := c.pending
	if p == nil {
		var err error

		p, err = c.readPending()
		if err != nil {
			return nil, err
		}
	}

	if !c.now().Before(p.state.ExpiresAt) {
		return nil, errors.Join(errors.New("verification session expired; start cloud login again"), c.clearPending())
	}

	c.pending = p

	return p, nil
}

func (c *Client) readPending() (*pendingLogin, error) {
	b, err := readStore(c.store + ".mfa")
	if err != nil {
		return nil, fmt.Errorf("read verification session; start cloud login again: %w", err)
	}

	if b == nil {
		return nil, errors.New("no sign-in is waiting for a code; start cloud login again")
	}

	var state pendingMFAState
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, errors.New("invalid verification session; start cloud login again")
	}

	if err := state.validate(c.now()); err != nil {
		return nil, err
	}

	jar, err := restoreCookies(state.Domain, state.Cookies, c.now)
	if err != nil {
		return nil, err
	}

	return &pendingLogin{http: c.ssoClient(jar), jar: jar, state: state}, nil
}

func (s *pendingMFAState) validate(now time.Time) error {
	if s.Version != 1 || s.ID == "" || (s.Domain != DefaultDomain && s.Domain != "garmin.cn") ||
		s.Email == "" || s.Method == "" || s.Params.Get("clientId") != clientID ||
		s.Params.Get("service") != "https://mobile.integration."+s.Domain+"/gcm/android" ||
		s.CreatedAt.IsZero() || s.CreatedAt.After(now) || !s.ExpiresAt.After(s.CreatedAt) ||
		s.ExpiresAt.Sub(s.CreatedAt) > pendingTTL {
		return errors.New("invalid verification session; start cloud login again")
	}

	return nil
}

// Account mutations hold authMu through completion so an older in-process
// request cannot remove or commit over a newer challenge. Separate processes
// support one authentication writer per store.
func (c *Client) clearPending() error {
	if err := os.Remove(c.store + ".mfa"); err != nil && !errors.Is(err, os.ErrNotExist) { // #nosec G703 -- remove only the pending file beside the local caller-selected account store
		return err
	}

	c.pending = nil

	return nil
}
