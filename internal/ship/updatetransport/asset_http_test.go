package updatetransport_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

type observedAssetBody struct {
	reader io.Reader
	read   int
	closed int
}

func (b *observedAssetBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n

	return n, err
}

func (b *observedAssetBody) Close() error { b.closed++; return nil }

func TestProviderSignatureReadIsBoundedAndBodyClosed(t *testing.T) {
	t.Parallel()

	body := &observedAssetBody{reader: strings.NewReader(strings.Repeat("a", 1<<20))}
	fixture := signedFixture(t)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	})}

	provider, err := updatetransport.NewProvider(client, fixture.request, fixture.record, fixture.key)
	if err != nil {
		t.Fatal(err)
	}

	got, err := provider.Check(t.Context(), checkRequest())
	if got != nil || err == nil || body.read != rawSignatureSize+1 || body.closed != 1 {
		t.Fatalf("result=%+v error=%v read=%d closed=%d", got, err, body.read, body.closed)
	}
}

func TestProviderAssetReadErrorsPreserveCauseAndCloseBody(t *testing.T) {
	t.Parallel()

	for _, phase := range []string{"signature", "package"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()

			fixture := signedFixture(t)
			body := &failingBody{err: io.ErrUnexpectedEOF}
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if phase == "package" && r.URL.String() == fixture.record.Signature.URL {
					return assetResponse(fixture.sig), nil
				}

				return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
			})}

			provider, err := updatetransport.NewProvider(client, fixture.request, fixture.record, fixture.key)
			if err != nil {
				t.Fatal(err)
			}

			release, err := provider.Check(t.Context(), checkRequest())
			if phase == "package" {
				if err != nil {
					t.Fatal(err)
				}

				err = provider.Download(t.Context(), release, io.Discard, nil)
			}

			if !errors.Is(err, io.ErrUnexpectedEOF) || body.closed.Load() != 1 {
				t.Fatalf("error=%v closed=%d", err, body.closed.Load())
			}
		})
	}
}

func TestProviderFollowsHTTPSAssetRedirectWithoutCookiesOrCredentials(t *testing.T) {
	t.Parallel()

	fixture := signedFixture(t)

	const destination = "https://release-assets.githubusercontent.com/selected.sig"

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}

	api, err := url.Parse(fixture.record.Signature.URL)
	if err != nil {
		t.Fatal(err)
	}

	jar.SetCookies(api, []*http.Cookie{{Name: "session", Value: "synthetic-secret", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}})

	var calls []string

	redirects := 0
	client := &http.Client{
		Timeout: time.Hour, Jar: jar,
		CheckRedirect: func(r *http.Request, _ []*http.Request) error {
			redirects++

			r.Header.Set("Authorization", "caller credential") //nolint:gosec // G119: fake transport checks removal of deliberately injected caller credentials.
			r.Header.Set("Cookie", "caller cookie")            //nolint:gosec // G119: fake transport checks removal of deliberately injected caller cookies.

			return nil
		},
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls = append(calls, r.URL.String())
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Accept") != "application/octet-stream" {
				t.Errorf("redirect request leaked credentials or lost accept: %v", r.Header)
			}

			if r.URL.String() == fixture.record.Signature.URL {
				return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {destination}}, Body: http.NoBody}, nil
			}

			if r.URL.String() != destination {
				t.Errorf("unexpected redirect destination: %s", r.URL)
			}

			return assetResponse(fixture.sig), nil
		}),
	}

	provider, err := updatetransport.NewProvider(client, fixture.request, fixture.record, fixture.key)
	if err != nil {
		t.Fatal(err)
	}

	got, err := provider.Check(t.Context(), checkRequest())
	if err != nil || got == nil || len(calls) != 2 || redirects != 1 || client.Timeout != time.Hour || client.Jar != jar {
		t.Fatalf("result=%+v error=%v calls=%v redirects=%d", got, err, calls, redirects)
	}
}

func TestProviderRejectsUnsafeOrExcessiveRedirects(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, destination string
		policy            func(*http.Request, []*http.Request) error
		calls             int
	}{
		{name: "http downgrade", destination: "http://cdn.invalid/asset", calls: 1},
		{name: "url credentials", destination: "https://user@cdn.invalid/asset", calls: 1},
		{name: "redirect limit with caller policy", destination: "https://cdn.invalid/asset", policy: func(*http.Request, []*http.Request) error { return nil }, calls: 10},
		{name: "stricter caller policy", destination: "https://cdn.invalid/asset", policy: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fixture := signedFixture(t)
			calls := 0
			client := &http.Client{CheckRedirect: tc.policy, Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {tc.destination}}, Body: http.NoBody}, nil
			})}

			provider, err := updatetransport.NewProvider(client, fixture.request, fixture.record, fixture.key)
			if err != nil {
				t.Fatal(err)
			}

			got, err := provider.Check(t.Context(), checkRequest())
			if got != nil || err == nil || calls != tc.calls {
				t.Fatalf("result=%+v error=%v calls=%d want=%d", got, err, calls, tc.calls)
			}
		})
	}
}

func TestProviderTimeBounds(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		download bool
		client   time.Duration
		caller   time.Duration
		want     time.Duration
	}{
		{name: "signature check cap", client: time.Hour, want: 30 * time.Second},
		{name: "download cap", download: true, client: time.Hour, want: 15 * time.Minute},
		{name: "shorter client", download: true, client: 200 * time.Millisecond, want: 200 * time.Millisecond},
		{name: "shorter caller", download: true, caller: 100 * time.Millisecond, want: 100 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				fixture := signedFixture(t)
				client := &http.Client{Timeout: tc.client, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if tc.download && r.URL.String() == fixture.record.Signature.URL {
						return assetResponse(fixture.sig), nil
					}

					<-r.Context().Done()

					return nil, r.Context().Err()
				})}

				provider, err := updatetransport.NewProvider(client, fixture.request, fixture.record, fixture.key)
				if err != nil {
					t.Fatal(err)
				}

				ctx := t.Context()

				if tc.caller > 0 {
					var cancel context.CancelFunc

					ctx, cancel = context.WithTimeout(ctx, tc.caller)
					defer cancel()
				}

				start := time.Now()

				release, err := provider.Check(ctx, checkRequest())
				if tc.download {
					if err != nil {
						t.Fatal(err)
					}

					err = provider.Download(ctx, release, io.Discard, nil)
				}

				if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != tc.want {
					t.Fatalf("error=%v elapsed=%s want=%s", err, time.Since(start), tc.want)
				}
			})
		})
	}
}

func TestProviderCancellationAfterProgressPreservesContextAndClosesBody(t *testing.T) {
	t.Parallel()

	fixture := signedFixture(t)
	body := &observedAssetBody{reader: bytes.NewReader(fixture.data)}

	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), metadataContextKey{}, "selected bytes"))
	defer cancel()

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Context().Value(metadataContextKey{}) != "selected bytes" {
			t.Error("lost caller context")
		}

		if r.URL.String() == fixture.record.Signature.URL {
			return assetResponse(fixture.sig), nil
		}

		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	})}

	provider, err := updatetransport.NewProvider(client, fixture.request, fixture.record, fixture.key)
	if err != nil {
		t.Fatal(err)
	}

	release, err := provider.Check(ctx, checkRequest())
	if err != nil {
		t.Fatal(err)
	}

	err = provider.Download(ctx, release, io.Discard, func(int64, int64) { cancel() })
	if !errors.Is(err, context.Canceled) || body.closed != 1 {
		t.Fatalf("download error=%v closed=%d", err, body.closed)
	}
}
