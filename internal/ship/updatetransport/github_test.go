package updatetransport_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

const (
	releaseEndpoint = "https://api.github.com/repos/DevWolk/garminctl/releases"
	firstRelease    = `[{"tag_name":"v2.0.0-beta.1","name":"Preview","body":"<b>literal notes</b>","prerelease":true,"published_at":"2026-11-07T12:00:00Z"},{"tag_name":"v9.0.0","draft":true,"name":null,"body":null,"published_at":null}]`
	secondRelease   = `[{"tag_name":"v1.2.0","name":"Stable","body":"Notes"}]`
	nextPage        = `<https://api.github.com/repos/DevWolk/garminctl/releases?page=2&per_page=100>; rel="next"`
	nullJSON        = "null"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func metadataClient(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()

	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Scheme+"://"+r.URL.Host+r.URL.Path != releaseEndpoint ||
			r.URL.Query().Get("per_page") != "100" ||
			r.URL.Query().Get("page") == "" || len(r.URL.Query()) != 2 {
			t.Errorf("unexpected metadata request: %s %s", r.Method, r.URL)
		}

		checkMetadataHeaders(t, r)

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)

		return recorder.Result(), nil
	})}
}

func checkMetadataHeaders(t *testing.T, r *http.Request) {
	t.Helper()

	if r.Header.Get("Accept") != "application/vnd.github+json" ||
		r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" ||
		r.Header.Get("User-Agent") != "garminctl" ||
		r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
		t.Errorf("unexpected metadata headers: %v", r.Header)
	}
}

func TestGitHubReleasesMetadataAndPagination(t *testing.T) {
	t.Parallel()

	calls := 0
	client := metadataClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++

		switch r.URL.Query().Get("page") {
		case "1":
			// Only the relation is consumed. The request must stay on the fixed
			// repository even if a response advertises an unrelated next URL.
			w.Header().Set("Link", `<https://untrusted.invalid/asset>; rel="next", <https://api.github.com/previous>; rel="prev"`)
			_, _ = fmt.Fprint(w, firstRelease)
		case "2":
			_, _ = fmt.Fprint(w, secondRelease)
		default:
			t.Errorf("unexpected extra page: %s", r.URL)
		}
	})

	source := updatetransport.NewGitHub(client)

	got, err := source.Releases(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	want := []updatetransport.Release{
		{
			Tag: "v2.0.0-beta.1", Name: "Preview", Notes: "<b>literal notes</b>", Prerelease: true,
			PublishedAt: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC),
		},
		{Tag: "v9.0.0", Draft: true},
		{Tag: "v1.2.0", Name: "Stable", Notes: "Notes"},
	}
	if !reflect.DeepEqual(got, want) || calls != 2 {
		t.Fatalf("releases=%+v calls=%d, want %+v and two calls", got, calls, want)
	}

	got[0].Notes = "caller edit"

	again, err := source.Releases(t.Context())
	if err != nil || !reflect.DeepEqual(again, want) || calls != 4 {
		t.Fatalf("fresh check=%+v calls=%d error=%v", again, calls, err)
	}
}

func TestGitHubReleasesStatusErrors(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			calls := 0
			source := updatetransport.NewGitHub(metadataClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++

				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, "untrusted body text")
			}))

			got, err := source.Releases(t.Context())

			var statusErr *updatetransport.StatusError
			if got != nil || !errors.As(err, &statusErr) || statusErr.StatusCode != status || calls != 1 {
				t.Fatalf("result=%+v error=%v calls=%d", got, err, calls)
			}

			if strings.Contains(err.Error(), "untrusted body text") {
				t.Fatal("server body leaked into the error")
			}
		})
	}
}

func TestGitHubReleasesRejectsRedirectsWithoutMutatingClient(t *testing.T) {
	t.Parallel()

	calls := 0
	client := metadataClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++

		w.Header().Set("Location", "https://untrusted.invalid/payload")
		w.WriteHeader(http.StatusFound)
	})
	client.Timeout = time.Hour
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		t.Error("caller's redirect policy was used")

		return nil
	}

	got, err := updatetransport.NewGitHub(client).Releases(t.Context())

	var statusErr *updatetransport.StatusError
	if got != nil || !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusFound || calls != 1 {
		t.Fatalf("result=%+v error=%v calls=%d", got, err, calls)
	}

	if client.Timeout != time.Hour || client.CheckRedirect == nil {
		t.Fatal("constructor mutated the borrowed client")
	}
}

func TestGitHubReleasesRejectsPartialResults(t *testing.T) {
	t.Parallel()

	source := updatetransport.NewGitHub(metadataClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			w.Header().Set("Link", nextPage)
			_, _ = fmt.Fprint(w, firstRelease)
		} else {
			w.WriteHeader(http.StatusTooManyRequests)
		}
	}))

	got, err := source.Releases(t.Context())

	var statusErr *updatetransport.StatusError
	if got != nil || !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("partial result=%+v error=%v", got, err)
	}
}

func TestGitHubReleasesContextAndTransportErrors(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("transport failed")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, sentinel
	})}

	got, err := updatetransport.NewGitHub(client).Releases(t.Context())
	if got != nil || !errors.Is(err, sentinel) {
		t.Fatalf("result=%+v error=%v", got, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	calls := 0
	client = metadataClient(t, func(http.ResponseWriter, *http.Request) { calls++ })

	got, err = updatetransport.NewGitHub(client).Releases(ctx)
	if got != nil || !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("cancelled result=%+v error=%v calls=%d", got, err, calls)
	}
}

type metadataContextKey struct{}

func TestGitHubReleasesCancellationBetweenPages(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), metadataContextKey{}, "caller"))
	defer cancel()

	calls := 0
	source := updatetransport.NewGitHub(metadataClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++

		if r.Context().Value(metadataContextKey{}) != "caller" {
			t.Error("lost caller context")
		}

		w.Header().Set("Link", nextPage)
		_, _ = fmt.Fprint(w, firstRelease)

		cancel()
	}))

	got, err := source.Releases(ctx)
	if got != nil || !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancelled result=%+v error=%v calls=%d", got, err, calls)
	}
}

func TestGitHubReleasesDoesNotBorrowCookies(t *testing.T) {
	t.Parallel()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}

	endpoint, err := url.Parse(releaseEndpoint)
	if err != nil {
		t.Fatal(err)
	}

	jar.SetCookies(endpoint, []*http.Cookie{{
		Name: "private", Value: "not-release-auth",
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	}})

	client := metadataClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "[]")
	})
	client.Jar = jar

	if _, err := updatetransport.NewGitHub(client).Releases(t.Context()); err != nil {
		t.Fatal(err)
	}

	if client.Jar != jar || len(jar.Cookies(endpoint)) != 1 {
		t.Fatal("constructor modified the caller's cookie jar")
	}
}

type failingBody struct {
	closed atomic.Int32
	err    error
}

func (b *failingBody) Read([]byte) (int, error) { return 0, b.err }
func (b *failingBody) Close() error {
	b.closed.Add(1)

	return nil
}

func TestGitHubReleasesClosesFailedResponse(t *testing.T) {
	t.Parallel()

	sentinel := io.ErrUnexpectedEOF
	body := &failingBody{err: sentinel}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
	})}

	got, err := updatetransport.NewGitHub(client).Releases(t.Context())
	if got != nil || !errors.Is(err, sentinel) || body.closed.Load() != 1 {
		t.Fatalf("result=%+v error=%v closed=%d", got, err, body.closed.Load())
	}
}
