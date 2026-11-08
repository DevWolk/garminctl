package updatetransport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

func stageTestFault(input []byte) (bool, error) {
	var request struct {
		Operation    string                          `json:"operation"`
		BuildVersion string                          `json:"buildVersion"`
		KeyID        string                          `json:"keyId"`
		Target       updatetransport.MatchRequest    `json:"target"`
		Release      updatetransport.ResolvedRelease `json:"release"`
	}
	if err := json.Unmarshal(input, &request); err != nil {
		return true, err
	}

	event := map[string]any{stageProtocolField: 1, stageOperationField: request.Operation, "kind": "progress", "written": 1, "total": len(stageData)}
	switch strings.TrimPrefix(request.Target.ReleaseTag, "v1.2.0-") {
	case "missing-complete":
		return true, json.NewEncoder(os.Stdout).Encode(event)
	case "truncated":
		_, err := io.WriteString(os.Stdout, `{stageProtocolField:1`)
		return true, err
	case "bad-count":
		event["written"] = len(stageData) + 1
	case "oversized-frame":
		if _, err := io.WriteString(os.Stdout, strings.Repeat("x", 8192)); err != nil {
			return true, err
		}

		for {
			time.Sleep(time.Hour)
		}
	case "forged-digest":
		executable, err := os.Executable()
		if err != nil {
			return true, err
		}

		root, err := os.OpenRoot(filepath.Dir(executable))
		if err != nil {
			return true, err
		}
		defer root.Close()

		if err := root.WriteFile(request.Release.Package.Name, stageData, 0o600); err != nil {
			return true, err
		}

		if err := root.WriteFile(request.Release.Signature.Name, bytes.Repeat([]byte{1}, 64), 0o600); err != nil {
			return true, err
		}

		event["kind"] = "complete"
		event["written"] = len(stageData)
		event["buildVersion"] = request.BuildVersion
		event["keyId"] = request.KeyID
		event["digest"] = strings.Repeat("0", 64)

		return true, json.NewEncoder(os.Stdout).Encode(event)
	default:
		return false, nil
	}

	if err := json.NewEncoder(os.Stdout).Encode(event); err != nil {
		return true, err
	}

	for {
		time.Sleep(time.Hour)
	}
}

func TestStagerRejectsChildFailuresWithoutRetainingOperation(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"missing-complete", "truncated", "bad-count", "oversized-frame", "forged-digest", "bad-signature"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			stager, cache := stagerForTest(t)
			request, release := stageSelection("v1.2.0-" + mode)

			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()

			result, err := stager.Stage(ctx, request, release, nil)
			if result != nil || err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("result=%+v error=%v", result, err)
			}

			entries, err := os.ReadDir(cache)
			if err != nil || len(entries) != 0 {
				t.Fatalf("operation leaked: %v error=%v", entries, err)
			}
		})
	}
}

func TestStagerRefusesMismatchedCompiledIdentity(t *testing.T) {
	t.Parallel()

	for _, mismatch := range []string{"version", "key"} {
		t.Run(mismatch, func(t *testing.T) {
			t.Parallel()

			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}

			config := updatetransport.StagerConfig{CLIPath: executable, CacheDir: t.TempDir(), BuildVersion: stageTestVersion, PublicKey: stageTestPublicKey()}
			if mismatch == "version" {
				config.BuildVersion = "different-build"
			} else {
				config.PublicKey = bytes.Repeat([]byte{2}, 32)
			}

			stager, err := updatetransport.NewStager(config)
			if err != nil {
				t.Fatal(err)
			}

			request, release := stageSelection(tag120)

			result, err := stager.Stage(t.Context(), request, release, nil)
			if result != nil || err == nil {
				t.Fatalf("mismatched child accepted: %+v %v", result, err)
			}
		})
	}
}
