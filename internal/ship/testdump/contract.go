package testdump

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"
)

// CheckDigest compares exact fixture output with its captured contract digest.
func CheckDigest(tb testing.TB, data []byte, want string) {
	tb.Helper()

	got := fmt.Sprintf("%x", sha256.Sum256(data))
	if got != want {
		tb.Fatalf("contract SHA256=%s bytes=%d; want %s", got, len(data), want)
	}
}

// NormalizePendingPath checks a real UUID filename and normalizes only that
// generated path in an apply response, including MCP text/structured copies.
func NormalizePendingPath(tb testing.TB, data []byte) []byte {
	tb.Helper()

	pattern := regexp.MustCompile(`GARMIN/NewFiles/[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\.FIT`)

	paths := pattern.FindAll(data, -1)
	if len(paths) == 0 {
		tb.Fatal("apply response has no UUID NewFiles path")
	}

	for _, path := range paths {
		if !bytes.Equal(path, paths[0]) {
			tb.Fatal("apply response contains different queued paths")
		}
	}

	return bytes.ReplaceAll(data, paths[0], []byte("GARMIN/NewFiles/<uuid>.FIT"))
}

// CheckJSON compares deterministic JSON encoding of a fixture result.
func CheckJSON(tb testing.TB, value any, want string) {
	tb.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		tb.Fatal(err)
	}

	CheckDigest(tb, data, want)
}
