package updatetransport_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

const (
	tagPath    = "/repos/DevWolk/garminctl/releases/tags/v1.2.0"
	assetsPath = "/repos/DevWolk/garminctl/releases/41/assets"
)

func resolveClient(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()

	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.github.com" {
			t.Errorf("unexpected request (no asset bytes allowed): %s %s", r.Method, r.URL)
		}

		switch r.URL.Path {
		case tagPath:
			if r.URL.RawQuery != "" {
				t.Errorf("tag lookup has unexpected query: %s", r.URL)
			}
		case assetsPath:
			if r.URL.Query().Get("per_page") != "100" ||
				r.URL.Query().Get("page") == "" || len(r.URL.Query()) != 2 {
				t.Errorf("unexpected asset pagination: %s", r.URL)
			}
		default:
			t.Errorf("unexpected metadata path: %s", r.URL)
		}

		checkMetadataHeaders(t, r)

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)

		return recorder.Result(), nil
	})}
}

func writeResolveJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()

	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}

func apiAsset(id int64, name string, size int64) updatetransport.AssetRef {
	return updatetransport.AssetRef{ID: id, Name: name, Size: size, URL: releaseEndpoint + "/assets/" + strconv.FormatInt(id, 10)}
}

func resolvePair() []updatetransport.AssetRef {
	return []updatetransport.AssetRef{apiAsset(101, setup120, 1234), apiAsset(708, setup120+".sig", rawSignatureSize)}
}

func tagRecord() map[string]any {
	return map[string]any{
		"id": 41, "tag_name": tag120, "name": selectedName, "body": "<b>plain notes</b>",
		"published_at": "2026-11-08T10:00:00Z",
		"prerelease":   true, "draft": true,
		"assets_url": "https://untrusted.invalid/assets",
		"assets":     []any{map[string]any{"name": setup120, "size": 0}},
	}
}

func TestGitHubResolvePinsTagReleaseIDAndAssetSnapshot(t *testing.T) {
	t.Parallel()

	calls := []string{}
	pair := resolvePair()
	ctx := context.WithValue(t.Context(), metadataContextKey{}, "selected release")
	client := resolveClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(metadataContextKey{}) != "selected release" {
			t.Error("lost caller context")
		}

		calls = append(calls, r.URL.RequestURI())
		switch {
		case r.URL.Path == tagPath:
			writeResolveJSON(t, w, tagRecord())
		case r.URL.Query().Get("page") == "1":
			w.Header().Set("Link", `<https://untrusted.invalid/other-release>; rel="next"`)
			writeResolveJSON(t, w, pair[:1])
		default:
			writeResolveJSON(t, w, pair[1:])
		}
	})
	source := updatetransport.NewGitHub(client)

	got, err := source.Resolve(ctx, setupRequest())
	if err != nil {
		t.Fatal(err)
	}

	want := &updatetransport.ResolvedRelease{
		ReleaseID: 41,
		Release: updatetransport.Release{
			Tag: tag120, Name: selectedName, Notes: "<b>plain notes</b>", Draft: true, Prerelease: true,
			PublishedAt: time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC),
		},
		Package: pair[0], Signature: pair[1],
	}

	uris := []string{tagPath, assetsPath + "?per_page=100&page=1", assetsPath + "?per_page=100&page=2"}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(calls, uris) {
		t.Fatalf("resolved=%+v calls=%v, want %+v calls=%v", got, calls, want, uris)
	}

	got.Package.URL = callerEdit
	got.Release.Notes = callerEdit

	again, err := source.Resolve(ctx, setupRequest())
	if err != nil || !reflect.DeepEqual(again, want) || len(calls) != 6 {
		t.Fatalf("fresh resolve=%+v calls=%d error=%v", again, len(calls), err)
	}
}

func TestGitHubResolveRejectsInvalidRequestBeforeHTTP(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		request updatetransport.MatchRequest
	}{
		{name: "zero request"},
		{name: "tag path", request: updatetransport.MatchRequest{ReleaseTag: "../evil", OS: windowsOS, Arch: amd64Arch, Installation: updatetransport.InstallSetup}},
		{name: "unsupported target", request: updatetransport.MatchRequest{ReleaseTag: tag120, OS: linuxOS, Arch: arm64Arch, Installation: updatetransport.InstallDEB}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			source := updatetransport.NewGitHub(resolveClient(t, func(http.ResponseWriter, *http.Request) { calls++ }))

			got, err := source.Resolve(t.Context(), tc.request)
			if got != nil || err == nil || calls != 0 {
				t.Fatalf("result=%+v error=%v calls=%d", got, err, calls)
			}
		})
	}
}

func TestGitHubResolveStatusErrorsAndNoPartialSuccess(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, failurePath string
		status            int
	}{
		{name: "tag deleted", failurePath: tagPath, status: http.StatusNotFound},
		{name: "tag forbidden", failurePath: tagPath, status: http.StatusForbidden},
		{name: "asset rate limit", failurePath: assetsPath, status: http.StatusTooManyRequests},
		{name: "asset server error", failurePath: assetsPath, status: http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			source := updatetransport.NewGitHub(resolveClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tc.failurePath {
					w.WriteHeader(tc.status)
					_, _ = fmt.Fprint(w, "untrusted diagnostic")
				} else {
					writeResolveJSON(t, w, tagRecord())
				}
			}))
			got, err := source.Resolve(t.Context(), setupRequest())

			var status *updatetransport.StatusError
			if got != nil || !errors.As(err, &status) || status.StatusCode != tc.status {
				t.Fatalf("result=%+v error=%v", got, err)
			}
		})
	}
}

func TestGitHubResolveCancellationAndZeroClient(t *testing.T) {
	t.Parallel()

	for _, when := range []string{"before request", "after tag", "after asset page"} {
		t.Run(when, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			if when == "before request" {
				cancel()
			}

			calls := 0
			source := updatetransport.NewGitHub(resolveClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++

				if r.URL.Path == tagPath {
					writeResolveJSON(t, w, tagRecord())

					if when == "after tag" {
						cancel()
					}
				} else {
					writeResolveJSON(t, w, resolvePair())
					cancel()
				}
			}))

			got, err := source.Resolve(ctx, setupRequest())

			wantCalls := map[string]int{"before request": 0, "after tag": 1, "after asset page": 2}[when]
			if got != nil || !errors.Is(err, context.Canceled) || calls != wantCalls {
				t.Fatalf("result=%+v error=%v calls=%d, want %d", got, err, calls, wantCalls)
			}
		})
	}

	if got, err := (&updatetransport.GitHub{}).Resolve(t.Context(), setupRequest()); got != nil || err == nil {
		t.Fatalf("zero client=%+v error=%v", got, err)
	}
}
