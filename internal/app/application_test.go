package app_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/containers/application"
)

type releaseRoundTrip func(*http.Request) (*http.Response, error)

func (f releaseRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNewApplicationReleaseCheck(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		prerelease bool
		want       string
		history    int
	}{
		{name: "stable", want: "v1.2.0", history: 1},
		{name: "prerelease", prerelease: true, want: "v2.0.0-beta.1", history: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			client := &http.Client{Transport: releaseRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++

				if r.URL.Host != "api.github.com" || r.URL.Path != "/repos/DevWolk/garminctl/releases" {
					t.Errorf("unexpected URL %s", r.URL)
				}

				body := `[{"tag_name":"v9.0.0","draft":true},{"tag_name":"v2.0.0-beta.1","prerelease":true},{"tag_name":"v1.2.0","name":"Stable","body":"<b>text</b>","published_at":"2026-11-07T12:00:00Z"}]`

				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			check := app.NewApplicationReleaseCheck(client)

			if calls != 0 {
				t.Fatal("composition performed a request before Run")
			}

			result, err := check.Run(t.Context(), application.CheckRequest{CurrentVersion: "v1.0.0", IncludePrerelease: tc.prerelease})
			if err != nil {
				t.Fatal(err)
			}

			if calls != 1 || len(result.History) != tc.history || result.Available == nil || result.Available.Tag != tc.want {
				t.Fatalf("result=%+v calls=%d", result, calls)
			}

			stable := result.History[len(result.History)-1]
			if stable.Name != "Stable" || stable.Notes != "<b>text</b>" ||
				!stable.PublishedAt.Equal(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)) {
				t.Fatalf("lost metadata: %+v", stable)
			}
		})
	}
}

func TestNewApplicationReleaseCheckPreservesErrors(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("offline")
	client := &http.Client{Transport: releaseRoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, sentinel
	})}

	result, err := app.NewApplicationReleaseCheck(client).Run(t.Context(), application.CheckRequest{CurrentVersion: "v1.0.0"})
	if result != nil || !errors.Is(err, sentinel) {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}
