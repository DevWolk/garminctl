package store

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/ciqstore"
)

type storeTransport func(*http.Request) (*http.Response, error)

func (f storeTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// No other test of this package uses the store's HTTP client it replaces.
func TestDeviceTypeLookupFailureIsNotCached(t *testing.T) {
	t.Parallel()

	const part = "006-B9999-00" // used by no other test, so the process cache starts empty

	previous := ciqstore.HTTPClient

	t.Cleanup(func() { ciqstore.HTTPClient = previous })

	calls := 0
	ciqstore.HTTPClient = &http.Client{Transport: storeTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("store unavailable")
		}

		body := `[{"id":"42","partNumber":"` + part + `"}]`

		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}

	if id := storeDeviceType(t.Context(), part); id != "" {
		t.Fatalf("failed lookup = %q, want empty", id)
	}

	if id := storeDeviceType(t.Context(), part); id != "42" || calls != 2 {
		t.Fatalf("lookup after failure = %q after %d call(s), want 42 after 2", id, calls)
	}

	if id := storeDeviceType(t.Context(), part); id != "42" || calls != 2 {
		t.Fatalf("cached lookup = %q after %d call(s), want 42 without a request", id, calls)
	}
}
