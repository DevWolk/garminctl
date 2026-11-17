package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestApplicationStageLocation(t *testing.T) {
	t.Parallel()

	cache := filepath.Join(t.TempDir(), "application-staging")
	for _, tc := range []struct {
		name, path string
		valid      bool
	}{
		{name: "operation", path: filepath.Join(cache, "stage-123"), valid: true},
		{name: "cache itself", path: cache},
		{name: "installation", path: filepath.Dir(cache)},
		{name: "nested", path: filepath.Join(cache, "stage-123", "nested")},
		{name: "prefix sibling", path: cache + "-other"},
		{name: "empty suffix", path: filepath.Join(cache, "stage-")},
	} {
		if err := os.MkdirAll(tc.path, 0o700); err != nil {
			t.Fatal(err)
		}

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := validateApplicationStageLocation(cache, tc.path); (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
		})
	}
}

// os.Executable names the resolved operation directory, while the cache may be
// configured through a linked home or cache directory.
func TestApplicationStageLocationAcceptsCacheReachedThroughSymlink(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	operation := filepath.Join(base, "real", "application-staging", "stage-123")

	if err := os.MkdirAll(operation, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(base, "real", "elsewhere", "stage-123"), 0o700); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Join(base, "real"), link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	cache := filepath.Join(link, "application-staging")
	if err := validateApplicationStageLocation(cache, operation); err != nil {
		t.Fatalf("resolved operation refused for a linked cache: %v", err)
	}

	if err := validateApplicationStageLocation(cache, filepath.Join(link, "application-staging", "stage-123")); err != nil {
		t.Fatalf("linked operation refused for a linked cache: %v", err)
	}

	if err := validateApplicationStageLocation(cache, filepath.Join(base, "real", "elsewhere", "stage-123")); err == nil {
		t.Fatal("operation outside the cache accepted")
	}
}

func TestApplicationStageBuildKeyCannotComeFromInput(t *testing.T) {
	t.Parallel()

	for _, encoded := range []string{"", "invalid", base64.StdEncoding.EncodeToString(make([]byte, 31))} {
		t.Run(encoded, func(t *testing.T) {
			t.Parallel()

			if _, err := NewApplicationStager("trusted-cli", "dev", encoded); err == nil {
				t.Fatal("invalid compiled key accepted")
			}

			if _, err := NewApplicationPackageCache("trusted-cli", "dev", encoded); err == nil {
				t.Fatal("cache accepted missing/invalid compiled key")
			}

			if err := RunApplicationStage(context.Background(), "dev", encoded, bytes.NewBufferString(`{"publicKey":"ignored"}`), io.Discard); err == nil {
				t.Fatal("request chose its own key")
			}

			if err := RunApplicationVerify(t.Context(), "dev", encoded, bytes.NewBufferString(`{"publicKey":"ignored"}`), io.Discard); err == nil {
				t.Fatal("offline request chose its own key")
			}
		})
	}
}

func TestApplicationStageKeyAcceptsRSAAndRejectsLegacyKey(t *testing.T) {
	t.Parallel()

	private, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		t.Fatal(err)
	}

	der, err := x509.MarshalPKIXPublicKey(&private.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		data  []byte
		valid bool
	}{
		{name: "RSA SPKI", data: der, valid: true},
		{name: "missing"},
		{name: "legacy Ed25519", data: bytes.Repeat([]byte{7}, 32)},
		{name: "trailing data", data: append(bytes.Clone(der), 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := applicationStageKey(base64.StdEncoding.EncodeToString(tc.data))
			if (err == nil) != tc.valid || (tc.valid && !bytes.Equal(got, tc.data)) {
				t.Fatalf("compiled key accepted=%t error=%v", err == nil, err)
			}
		})
	}
}
