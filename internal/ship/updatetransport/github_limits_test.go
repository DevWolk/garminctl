package updatetransport_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

func TestGitHubReleasesRejectsInvalidOrExcessivePages(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, body, link string
	}{
		{name: "null array", body: nullJSON},
		{name: "object", body: "{}"},
		{name: "trailing value", body: "[] []"},
		{name: "invalid JSON", body: "["},
		{name: "invalid timestamp", body: `[{"published_at":"yesterday"}]`},
		{name: "overlong body", body: "[]" + strings.Repeat(" ", 2<<20)},
		{name: "too many records", body: "[" + strings.Repeat("{},", 100) + "{}]"},
		{name: "malformed pagination", body: "[]", link: "<broken"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0

			source := updatetransport.NewGitHub(metadataClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++

				if tc.link != "" {
					w.Header().Set("Link", tc.link)
				}

				_, _ = fmt.Fprint(w, tc.body)
			}))
			if got, err := source.Releases(t.Context()); err == nil || got != nil || calls != 1 {
				t.Fatalf("result=%+v error=%v calls=%d", got, err, calls)
			}
		})
	}
}

func TestGitHubReleasesPageLimit(t *testing.T) {
	t.Parallel()

	calls := 0
	source := updatetransport.NewGitHub(metadataClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++

		w.Header().Set("Link", nextPage)
		_, _ = fmt.Fprint(w, "[]")
	}))

	got, err := source.Releases(t.Context())
	if got != nil || err == nil || calls != 10 {
		t.Fatalf("result=%+v error=%v calls=%d, want failure at ten pages", got, err, calls)
	}
}

func TestGitHubReleasesBoundsBytesRead(t *testing.T) {
	t.Parallel()

	const total = 4 << 20

	body := &io.LimitedReader{R: strings.NewReader("[]" + strings.Repeat(" ", total)), N: total}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(body), Header: make(http.Header)}, nil
	})}

	got, err := updatetransport.NewGitHub(client).Releases(t.Context())
	if got != nil || err == nil || total-body.N != (2<<20)+1 {
		t.Fatalf("result=%+v error=%v bytes read=%d", got, err, total-body.N)
	}
}

func TestGitHubReleasesEmptyAndZeroValue(t *testing.T) {
	t.Parallel()

	source := updatetransport.NewGitHub(metadataClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "[]")
	}))

	got, err := source.Releases(t.Context())
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty result=%+v error=%v", got, err)
	}

	got, err = (&updatetransport.GitHub{}).Releases(t.Context())
	if err == nil || got != nil {
		t.Fatalf("unconfigured result=%+v error=%v", got, err)
	}
}

func TestGitHubReleasesTimeBounds(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name           string
		client, caller time.Duration
		paged          bool
		want           time.Duration
		calls          int
	}{
		{name: "request cap", client: time.Hour, want: 10 * time.Second, calls: 1},
		{name: "negative client timeout", client: -time.Second, want: 10 * time.Second, calls: 1},
		{name: "shorter client", client: 2 * time.Second, want: 2 * time.Second, calls: 1},
		{name: "caller deadline", caller: time.Second, want: time.Second, calls: 1},
		{name: "whole check", paged: true, want: 30 * time.Second, calls: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()

				if tc.caller > 0 {
					var cancel context.CancelFunc

					ctx, cancel = context.WithTimeout(ctx, tc.caller)
					defer cancel()
				}

				calls := 0
				client := &http.Client{Timeout: tc.client, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++

					if tc.paged {
						select {
						case <-time.After(9 * time.Second):
							return &http.Response{
								StatusCode: http.StatusOK,
								Body:       io.NopCloser(strings.NewReader("[]")), Header: http.Header{"Link": []string{nextPage}},
							}, nil
						case <-r.Context().Done():
							return nil, r.Context().Err()
						}
					}

					<-r.Context().Done()

					return nil, r.Context().Err()
				})}
				start := time.Now()

				got, err := updatetransport.NewGitHub(client).Releases(ctx)
				if got != nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != tc.want || calls != tc.calls {
					t.Fatalf("result=%+v error=%v elapsed=%s calls=%d", got, err, time.Since(start), calls)
				}
			})
		})
	}
}
