package app

import (
	"bytes"
	"context"
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

			if err := RunApplicationStage(context.Background(), "dev", encoded, bytes.NewBufferString(`{"publicKey":"ignored"}`), io.Discard); err == nil {
				t.Fatal("request chose its own key")
			}
		})
	}
}
