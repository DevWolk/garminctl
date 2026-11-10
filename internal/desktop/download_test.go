package desktop

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

func downloadURL(t *testing.T, raw string) *url.URL {
	t.Helper()

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}

	return u
}

func assertDownloadTarget(t *testing.T, target, want string) {
	t.Helper()

	got, err := os.ReadFile(target) //nolint:gosec // test-owned destination path
	if err != nil || string(got) != want {
		t.Fatalf("destination changed: %q, %v; want %q", got, err, want)
	}

	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".garminctl-download-*.part"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("temporary downloads remain: %v, %v", leftovers, err)
	}
}

func TestDownloadPublishesOnlyCompleteResponse(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusOK, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "complete download")
			}))
			t.Cleanup(server.Close)

			target := filepath.Join(t.TempDir(), "export.fit")
			if err := os.WriteFile(target, []byte("old destination"), 0o600); err != nil {
				t.Fatal(err)
			}

			d := &nativeOperations{}
			d.startup(t.Context())
			t.Cleanup(d.close)

			err := d.save(downloadURL(t, server.URL), target)
			want := "old destination"

			if status == http.StatusOK {
				if err != nil {
					t.Fatal(err)
				}

				want = "complete download"
			} else if err == nil {
				t.Fatal("non-success HTTP status accepted")
			}

			assertDownloadTarget(t, target, want)
		})
	}
}

func TestTruncatedDownloadPreservesDestination(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "short")
	}))
	t.Cleanup(server.Close)

	target := filepath.Join(t.TempDir(), "export.fit")
	if err := os.WriteFile(target, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}

	d := &nativeOperations{}
	d.startup(t.Context())
	t.Cleanup(d.close)

	if err := d.save(downloadURL(t, server.URL), target); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated response error = %v", err)
	}

	assertDownloadTarget(t, target, "previous")
}

func TestClosingWindowCancelsAndJoinsDownloads(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")

		_, _ = io.WriteString(w, "partial")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
			close(entered)

			return
		}

		close(entered)
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	target := filepath.Join(t.TempDir(), "export.fit")
	if err := os.WriteFile(target, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}

	d := &nativeOperations{}
	d.startup(t.Context())
	t.Cleanup(d.close)
	u := downloadURL(t, server.URL)

	result := make(chan error, 1)
	go func() { result <- d.save(u, target) }()

	select {
	case <-entered:
	case err := <-result:
		t.Fatalf("download stopped before the server barrier: %v", err)
	}

	d.close()

	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled download error = %v", err)
	}

	assertDownloadTarget(t, target, "previous")

	if err := d.save(u, target); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed window admitted a new download: %v", err)
	}
}

type waitingDownloadTransport struct{}

func (waitingDownloadTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func TestDownloadHasConfigurableBoundedDeadline(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		d := &nativeOperations{timeout: time.Second, client: &http.Client{Transport: waitingDownloadTransport{}}}

		d.startup(t.Context())
		defer d.close()

		start := time.Now()

		err := d.save(downloadURL(t, "http://127.0.0.1/api/file"), filepath.Join(t.TempDir(), "export.fit"))
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != time.Second {
			t.Fatalf("configured deadline = %v after %v", err, time.Since(start))
		}
	})
}

func TestDownloadReplacementFailureCleansTemporaryFile(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "new") }))
	t.Cleanup(server.Close)
	dir := t.TempDir()

	target := filepath.Join(dir, "existing-directory")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(target, "keep")
	if err := os.WriteFile(marker, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}

	d := &nativeOperations{}
	d.startup(t.Context())
	t.Cleanup(d.close)

	if err := d.save(downloadURL(t, server.URL), target); err == nil {
		t.Fatal("replaced a destination directory")
	}

	assertDownloadTarget(t, marker, "previous")

	leftovers, err := filepath.Glob(filepath.Join(dir, ".garminctl-download-*.part"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("failed replacement left temporary files: %v, %v", leftovers, err)
	}
}
