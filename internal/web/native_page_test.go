package web

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"testing"
)

func TestNativeIndexRenderingContract(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t)

	index, err := fs.ReadFile(uiFS, "ui/index.html")
	if err != nil {
		t.Fatal(err)
	}

	const (
		header = "X-Garminctl-Page-Nonce"
		nonce  = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	)

	for _, path := range []string{"/", "/index.html", "/settings", "/apps/example", "/application"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}

			request.Header.Set(header, nonce)
			request.Header.Set("If-None-Match", "*")
			request.Header.Set("If-Modified-Since", "Wed, 31 Dec 2098 23:59:59 GMT")

			response, err := ts.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}

			defer func() { _ = response.Body.Close() }()

			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}

			if response.StatusCode != http.StatusOK || response.Header.Get(header) != nonce || response.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("native HTML must carry the fresh rendering acknowledgement: status=%d headers=%v", response.StatusCode, response.Header)
			}

			// Native rendering may add nonces only at the authored script tags;
			// every other byte of the embedded browser page stays unchanged.
			plain := bytes.ReplaceAll(body, []byte(` nonce="`+nonce+`"`), nil)
			if !bytes.Equal(plain, index) || bytes.Count(body, []byte(` nonce="`+nonce+`"`)) != bytes.Count(index, []byte("<script ")) {
				t.Fatal("native rendering changed the page or missed a trusted script")
			}
		})
	}
}

func TestBrowserIndexRemainsByteIdentical(t *testing.T) {
	t.Parallel()

	ts, _ := newTestServer(t)

	index, err := fs.ReadFile(uiFS, "ui/index.html")
	if err != nil {
		t.Fatal(err)
	}

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := ts.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(body, index) || response.Header.Get("X-Garminctl-Page-Nonce") != "" {
		t.Fatal("ordinary browser rendering must remain byte-identical")
	}
}

func TestNativeIndexRejectsMalformedNonce(t *testing.T) {
	t.Parallel()

	ts, _ := newTestServer(t)
	for _, nonce := range []string{"short", strings.Repeat("A", 31), strings.Repeat("-", 32), `\"><script>alert(1)</script>`} {
		t.Run(nonce, func(t *testing.T) {
			t.Parallel()

			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/", nil)
			if err != nil {
				t.Fatal(err)
			}

			request.Header.Set("X-Garminctl-Page-Nonce", nonce)

			response, err := ts.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}

			defer func() { _ = response.Body.Close() }()

			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("malformed nonce accepted: status=%d", response.StatusCode)
			}
		})
	}
}
