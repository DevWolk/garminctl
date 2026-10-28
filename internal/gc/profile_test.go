package gc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type profileTransport func(*http.Request) (*http.Response, error)

func (f profileTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func profileTestClient(t *testing.T, transport profileTransport) *Client {
	t.Helper()
	client := New(filepath.Join(t.TempDir(), "account.json"))
	client.sess = Session{
		OAuth1: &OAuth1Token{Token: "synthetic", Domain: DefaultDomain},
		OAuth2: &OAuth2Token{TokenType: "Bearer", AccessToken: "synthetic-access", ExpiresAt: time.Now().Add(time.Hour).Unix()},
	}
	client.HTTP.Transport = transport

	return client
}

func profileResponse(body string, status int) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestSocialProfileIsFreshAndAuthenticated(t *testing.T) {
	t.Parallel()

	requests := 0
	client := profileTestClient(t, func(req *http.Request) (*http.Response, error) {
		requests++

		if req.Method != http.MethodGet || req.URL.String() != "https://connectapi.garmin.com/userprofile-service/socialProfile" || req.Header.Get("Authorization") != "Bearer synthetic-access" {
			t.Errorf("unexpected profile request method=%s url=%s", req.Method, req.URL)
		}

		return profileResponse(fmt.Sprintf(`{"displayName":"opaque-id","userName":"user%d"}`, requests), http.StatusOK), nil
	})

	first, err := client.SocialProfile(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	first.Name = "locally modified"

	second, err := client.SocialProfile(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if requests != 2 || second.Name != "user2" || first == second {
		t.Fatalf("client cached a profile: requests=%d second=%+v", requests, second)
	}
}

func TestSocialProfileLabelAndJSONContract(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ name, body, want string }{
		{name: "full name", body: `{"displayName":"opaque-id","fullName":"Full","userName":"User"}`, want: "Full"},
		{name: "user name", body: `{"displayName":"opaque-id","userName":"User"}`, want: "User"},
		{name: "plain display name", body: `{"displayName":"Plain"}`, want: "Plain"},
		{name: "opaque display name", body: `{"displayName":"opaque-id"}`},
		{name: "empty", body: `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := profileTestClient(t, func(*http.Request) (*http.Response, error) {
				return profileResponse(test.body, http.StatusOK), nil
			})

			profile, err := client.SocialProfile(t.Context())
			if err != nil || profile.Name != test.want {
				t.Fatalf("profile=%+v error=%v", profile, err)
			}
		})
	}

	data, err := json.Marshal(CloudProfile{})
	if err != nil || string(data) != `{"displayName":"","fullName":"","userName":"","name":""}` {
		t.Fatalf("empty profile JSON=%s error=%v", data, err)
	}
}

func TestSocialProfilePreservesErrors(t *testing.T) {
	t.Parallel()

	client := profileTestClient(t, func(*http.Request) (*http.Response, error) {
		return profileResponse("unavailable", http.StatusServiceUnavailable), nil
	})
	if profile, err := client.SocialProfile(t.Context()); profile != nil {
		t.Fatalf("failed profile=%+v", profile)
	} else if he, ok := errors.AsType[*HTTPError](err); !ok || he.Status != http.StatusServiceUnavailable {
		t.Fatalf("lost HTTP error: %v", err)
	}

	client.HTTP.Transport = profileTransport(func(*http.Request) (*http.Response, error) {
		return profileResponse("{", http.StatusOK), nil
	})
	if _, err := client.SocialProfile(t.Context()); err == nil {
		t.Fatal("invalid JSON accepted")
	}

	client.HTTP.Transport = profileTransport(func(*http.Request) (*http.Response, error) { return nil, context.Canceled })
	if _, err := client.SocialProfile(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}

	client.sess = Session{}
	if _, err := client.SocialProfile(t.Context()); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("lost missing-session error: %v", err)
	}
}
