package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"io"
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
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := validateApplicationStageLocation(cache, tc.path); (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
		})
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
