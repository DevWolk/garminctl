package gc

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

const testCookieName = "session"

func cookieURL(t *testing.T, raw string) *url.URL {
	t.Helper()

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}

	return u
}

func cookieValues(jar http.CookieJar, u *url.URL) map[string]string {
	values := map[string]string{}
	for _, cookie := range jar.Cookies(u) {
		values[cookie.Name] = cookie.Value
	}

	return values
}

func TestCookieReplayKeepsScopeAndAttributes(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clock := func() time.Time { return now }
	jar := newRecordingJar(DefaultDomain, clock)
	origin := cookieURL(t, "https://sso.garmin.com/mobile/api/login?private=query")
	jar.SetCookies(origin, []*http.Cookie{
		{Name: "host", Value: "h", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode},
		{Name: "domain", Value: "d", Domain: ".garmin.com", Path: "/mobile", MaxAge: 60}, // #nosec G124 -- intentional non-Secure cookie tests HTTP scope preservation
		{Name: "long", Value: "l", Path: "/", MaxAge: 7200, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode},
	})

	events, err := jar.snapshot()
	if err != nil {
		t.Fatal(err)
	}

	if events[0].Origin != "https://sso.garmin.com/mobile/api/login" || !events[0].Cookie.HttpOnly ||
		events[0].Cookie.SameSite != http.SameSiteLaxMode || events[1].Cookie.MaxAge != 0 ||
		!events[1].Cookie.Expires.Equal(now.Add(time.Minute)) || !events[2].Cookie.Expires.Equal(now.Add(2*time.Hour)) {
		t.Fatal("cookie attributes or absolute expiry were not preserved")
	}

	restored, err := restoreCookies(DefaultDomain, events, clock)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		url  string
		host bool
		dom  bool
	}{
		{name: "same host and default path", url: "https://sso.garmin.com/mobile/api/verify", host: true, dom: true},
		{name: "other subdomain", url: "https://other.garmin.com/mobile/api/verify", dom: true},
		{name: "outside default path", url: "https://sso.garmin.com/mobile/other", dom: true},
		{name: "outside both paths", url: "https://sso.garmin.com/other"},
		{name: "insecure request", url: "http://sso.garmin.com/mobile/api/verify", dom: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			values := cookieValues(restored, cookieURL(t, tt.url))
			if (values["host"] == "h") != tt.host || (values["domain"] == "d") != tt.dom {
				t.Fatalf("cookie scopes = %v", values)
			}
		})
	}
}

func TestCookieReplayExpiredReplacementDeletesOldValue(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clock := func() time.Time { return now }
	jar := newRecordingJar(DefaultDomain, clock)
	u := cookieURL(t, "https://sso.garmin.com/mobile/login")
	jar.SetCookies(u, []*http.Cookie{{Name: testCookieName, Value: "old", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}})
	jar.SetCookies(u, []*http.Cookie{{Name: testCookieName, Value: "new", Path: "/", MaxAge: 1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}})

	events, err := jar.snapshot()
	if err != nil {
		t.Fatal(err)
	}

	restored, err := restoreCookies(DefaultDomain, events, func() time.Time { return now.Add(2 * time.Second) })
	if err != nil {
		t.Fatal(err)
	}

	if cookies := restored.Cookies(u); len(cookies) != 0 {
		t.Fatal("expired replacement revived an older cookie")
	}

	jar.SetCookies(u, []*http.Cookie{{Name: testCookieName, Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}})

	events, err = jar.snapshot()
	if err != nil {
		t.Fatal(err)
	}

	restored, err = restoreCookies(DefaultDomain, events, clock)
	if err != nil || len(restored.Cookies(u)) != 0 {
		t.Fatalf("explicit deletion was lost: %v", err)
	}
}

func TestCookieReplayRejectsUnsupportedOrigins(t *testing.T) {
	t.Parallel()

	for _, origin := range []string{"http://sso.garmin.com/", "https://garmin.com.evil.invalid/", "https://user@sso.garmin.com/", "https://sso.garmin.com:123/"} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()

			events := []savedCookie{{Origin: origin, Cookie: http.Cookie{Name: testCookieName, Value: "test", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}}}
			if _, err := restoreCookies(DefaultDomain, events, time.Now); err == nil {
				t.Fatal("unsupported cookie origin accepted")
			}
		})
	}
}
