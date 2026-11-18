package gc

import (
	"errors"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
)

type savedCookie struct {
	Origin string      `json:"origin"`
	Cookie http.Cookie `json:"cookie"`
}

// recordingJar retains SetCookies history, including deletion events. Cookies
// returned by CookieJar.Cookies omit the attributes needed to restore a jar.
type recordingJar struct {
	mu     sync.Mutex
	jar    *cookiejar.Jar
	domain string
	now    func() time.Time
	events []savedCookie
	err    error
}

func newRecordingJar(domain string, now func() time.Time) *recordingJar {
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	return &recordingJar{jar: jar, domain: domain, now: now}
}

func (j *recordingJar) Cookies(u *url.URL) []*http.Cookie { return j.jar.Cookies(u) }

func (j *recordingJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.jar.SetCookies(u, cookies)

	if len(cookies) == 0 {
		return
	}

	if !cookieOriginAllowed(u, j.domain) {
		j.err = errors.New("cannot persist cookies from an unsupported sign-in origin")
		return
	}

	origin := (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()

	for _, c := range cookies {
		copyCookie := *c // #nosec G124 -- replay exact server attributes; this client does not issue cookies
		if copyCookie.MaxAge > 0 {
			if int64(copyCookie.MaxAge) > math.MaxInt64/int64(time.Second) {
				j.err = errors.New("sign-in cookie lifetime is too large to persist")
				return
			}

			copyCookie.Expires = j.now().Add(time.Duration(copyCookie.MaxAge) * time.Second)
			copyCookie.MaxAge = 0
		}

		j.events = append(j.events, savedCookie{Origin: origin, Cookie: copyCookie})
	}
}

func (j *recordingJar) snapshot() ([]savedCookie, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	return append([]savedCookie(nil), j.events...), j.err
}

func restoreCookies(domain string, events []savedCookie, now func() time.Time) (*recordingJar, error) {
	j := newRecordingJar(domain, now)

	for _, event := range events {
		u, err := url.Parse(event.Origin)
		if err != nil || !cookieOriginAllowed(u, domain) {
			return nil, errors.New("invalid saved sign-in cookie origin; start cloud login again")
		}

		c := event.Cookie // #nosec G124 -- restore server attributes through the standard validating jar
		if c.MaxAge > 0 {
			return nil, errors.New("invalid relative cookie expiry; start cloud login again")
		}

		if !c.Expires.IsZero() && !now().Before(c.Expires) {
			// An expired replacement must delete its predecessor, not revive it.
			c.MaxAge = -1
		}

		j.jar.SetCookies(u, []*http.Cookie{&c})
	}

	j.events = append([]savedCookie(nil), events...)

	return j, nil
}

func cookieOriginAllowed(u *url.URL, domain string) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}

	host := strings.ToLower(u.Hostname())

	return host == domain || strings.HasSuffix(host, "."+domain)
}
