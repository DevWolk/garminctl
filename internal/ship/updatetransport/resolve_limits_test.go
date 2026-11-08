package updatetransport_test

import (
	"context"
	"encoding/json"
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

func assetJSON(t *testing.T, assets []updatetransport.AssetRef) string {
	t.Helper()

	body, err := json.Marshal(assets)
	if err != nil {
		t.Fatal(err)
	}

	return string(body)
}

func TestGitHubResolveMustFinishAllAssetPages(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{name: "later rate limit", status: http.StatusTooManyRequests},
		{name: "later malformed JSON", body: "["},
		{name: "later duplicate id", body: assetJSON(t, []updatetransport.AssetRef{apiAsset(101, "extra.txt", 1)})},
		{name: "later duplicate package", body: assetJSON(t, []updatetransport.AssetRef{apiAsset(999, setup120, 2)})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			source := updatetransport.NewGitHub(resolveClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++

				switch {
				case r.URL.Path == tagPath:
					writeResolveJSON(t, w, tagRecord())
				case r.URL.Query().Get("page") == "1":
					w.Header().Set("Link", nextPage)
					writeResolveJSON(t, w, resolvePair())
				case tc.status != 0:
					w.WriteHeader(tc.status)
				default:
					_, _ = fmt.Fprint(w, tc.body)
				}
			}))

			got, err := source.Resolve(t.Context(), setupRequest())
			if got != nil || err == nil || calls != 3 {
				t.Fatalf("partial result=%+v error=%v calls=%d", got, err, calls)
			}
		})
	}
}

func TestGitHubResolveAssetPageLimit(t *testing.T) {
	t.Parallel()

	calls := 0
	source := updatetransport.NewGitHub(resolveClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++

		if r.URL.Path == tagPath {
			writeResolveJSON(t, w, tagRecord())
		} else {
			w.Header().Set("Link", nextPage)
			writeResolveJSON(t, w, []updatetransport.AssetRef{})
		}
	}))

	got, err := source.Resolve(t.Context(), setupRequest())
	if got != nil || err == nil || calls != 11 {
		t.Fatalf("result=%+v error=%v calls=%d", got, err, calls)
	}
}

func TestGitHubResolveAssetJSONAndByteBounds(t *testing.T) {
	t.Parallel()

	many := resolvePair()
	for i := range 99 {
		many = append(many, apiAsset(int64(1000+i), fmt.Sprintf("extra-%d", i), 1))
	}

	valid := assetJSON(t, resolvePair())

	for _, tc := range []struct {
		name, body string
	}{
		{name: "null", body: nullJSON},
		{name: "object", body: "{}"},
		{name: "trailing value", body: valid + " []"},
		{name: "oversized page", body: assetJSON(t, many)},
		{name: "overlong body", body: valid + strings.Repeat(" ", 2<<20)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			source := updatetransport.NewGitHub(resolveClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tagPath {
					writeResolveJSON(t, w, tagRecord())
				} else {
					_, _ = fmt.Fprint(w, tc.body)
				}
			}))
			if got, err := source.Resolve(t.Context(), setupRequest()); got != nil || err == nil {
				t.Fatalf("result=%+v error=%v", got, err)
			}
		})
	}
}

func TestGitHubResolvePreservesReadErrorAndClosesBody(t *testing.T) {
	t.Parallel()

	body := &failingBody{err: io.ErrUnexpectedEOF}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
	})}

	got, err := updatetransport.NewGitHub(client).Resolve(t.Context(), setupRequest())
	if got != nil || !errors.Is(err, io.ErrUnexpectedEOF) || body.closed.Load() != 1 {
		t.Fatalf("result=%+v error=%v closed=%d", got, err, body.closed.Load())
	}
}

func TestGitHubResolveTimeBounds(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		paged bool
		want  time.Duration
		calls int
	}{
		{name: "request cap", want: 10 * time.Second, calls: 1},
		{name: "whole resolve", paged: true, want: 30 * time.Second, calls: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				calls := 0
				client := &http.Client{Timeout: time.Hour, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++

					if tc.paged {
						select {
						case <-time.After(9 * time.Second):
							body := `{"id":41,"tag_name":"v1.2.0"}`
							headers := make(http.Header)

							if r.URL.Path == assetsPath {
								body = "[]"

								headers.Set("Link", nextPage)
							}

							return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: headers}, nil
						case <-r.Context().Done():
							return nil, r.Context().Err()
						}
					}

					<-r.Context().Done()

					return nil, r.Context().Err()
				})}
				start := time.Now()

				got, err := updatetransport.NewGitHub(client).Resolve(t.Context(), setupRequest())
				if got != nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != tc.want || calls != tc.calls {
					t.Fatalf("result=%+v error=%v elapsed=%s calls=%d", got, err, time.Since(start), calls)
				}
			})
		})
	}
}
