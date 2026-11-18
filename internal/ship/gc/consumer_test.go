package gc

import (
	"errors"
	"net/http"
	"path/filepath"
	"testing"
)

func TestBuiltConsumerNeedsKeyAndSecret(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ key, secret string }{{"", ""}, {"key", ""}, {"", "secret"}} {
		if got := builtConsumer(tc.key, tc.secret); got != nil {
			t.Errorf("builtConsumer(%q, %q) = %+v, want nil", tc.key, tc.secret, got)
		}
	}

	got := builtConsumer("key", "secret")
	if got == nil || got.Key != "key" || got.Secret != "secret" {
		t.Fatalf("builtConsumer = %+v", got)
	}
}

func TestLoadConsumerUsesBuildValueWithoutDownload(t *testing.T) {
	t.Parallel()

	client := New(filepath.Join(t.TempDir(), "account.json"))
	client.consumer = builtConsumer("build-key", "build-secret")
	client.HTTP.Transport = profileTransport(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("unexpected request: " + req.URL.String())
	})

	got, err := client.loadConsumer(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if got.Key != "build-key" || got.Secret != "build-secret" {
		t.Fatalf("loadConsumer = %+v", got)
	}
}
