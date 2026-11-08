package updatetransport_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

func workerRequest(t *testing.T, directory string) map[string]any {
	t.Helper()

	operation := strings.Repeat("a", 64)

	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if err := root.WriteFile(".stage-operation", []byte(operation), 0o600); err != nil {
		t.Fatal(err)
	}

	target, release := stageSelection(tag120)
	keyHash := sha256.Sum256(stageTestPublicKey())

	return map[string]any{
		stageProtocolField: 1, stageOperationField: operation, "buildVersion": stageTestVersion,
		"keyId": hex.EncodeToString(keyHash[:]), "target": target, "release": release,
	}
}

func TestStageWorkerRejectsUntrustedProtocolBeforeHTTP(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		edit   func(map[string]any)
		suffix string
	}{
		{name: "protocol", edit: func(r map[string]any) { r[stageProtocolField] = 2 }},
		{name: "operation", edit: func(r map[string]any) { r[stageOperationField] = strings.Repeat("b", 64) }},
		{name: "build", edit: func(r map[string]any) { r["buildVersion"] = "other" }},
		{name: "key fingerprint", edit: func(r map[string]any) { r["keyId"] = strings.Repeat("b", 64) }},
		{name: "JSON key substitution", edit: func(r map[string]any) { r["publicKey"] = "untrusted" }},
		{name: "URL override", edit: func(r map[string]any) { r["baseURL"] = "https://untrusted.invalid" }},
		{name: "trailing JSON", suffix: " {}"},
		{name: "request bound", suffix: strings.Repeat(" ", 64<<10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()

			request := workerRequest(t, directory)
			if tc.edit != nil {
				tc.edit(request)
			}

			data, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}

			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, io.ErrUnexpectedEOF })}

			var output bytes.Buffer

			err = updatetransport.RunStageWorker(t.Context(), updatetransport.WorkerConfig{
				Directory: directory, BuildVersion: stageTestVersion,
				PublicKey: stageTestPublicKey(), HTTPClient: client,
			}, strings.NewReader(string(data)+tc.suffix), &output)
			if err == nil || calls != 0 || output.Len() != 0 {
				t.Fatalf("error=%v calls=%d output=%q", err, calls, output.String())
			}
		})
	}
}

func TestStageWorkerRejectsMissingCompiledKeyBeforeReading(t *testing.T) {
	t.Parallel()

	input := strings.NewReader("untrusted input")

	err := updatetransport.RunStageWorker(t.Context(), updatetransport.WorkerConfig{Directory: t.TempDir()}, input, io.Discard)
	if err == nil || input.Len() != len("untrusted input") {
		t.Fatalf("error=%v remaining=%d", err, input.Len())
	}
}

func TestStagerCancelAtFullProgressDoesNotPublishResult(t *testing.T) {
	t.Parallel()
	stager, cache := stagerForTest(t)
	request, release := stageSelection(tag120)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	result, err := stager.Stage(ctx, request, release, func(p updatetransport.StageProgress) {
		if p.Written == p.Total {
			cancel()
		}
	})
	if result != nil || err == nil || ctx.Err() != context.Canceled {
		t.Fatalf("result=%+v error=%v context=%v", result, err, ctx.Err())
	}

	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled result retained: %v error=%v", entries, err)
	}
}
