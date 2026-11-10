package updatetransport_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

const (
	stageTestVersion    = "stage-test-build"
	stageOperationField = "operation"
)

const stageProtocolField = "protocol"

var stageData = []byte("synthetic installer payload; never executable")

func TestMain(m *testing.M) {
	if err := initializeStageTestKey(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if len(os.Args) != 2 || (os.Args[1] != updatetransport.StageCommand && os.Args[1] != updatetransport.VerifyCommand) {
		os.Exit(m.Run())
	}

	worker := stageTestChild
	if os.Args[1] == updatetransport.VerifyCommand {
		worker = verifyTestChild
	}

	if err := worker(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	os.Exit(0)
}

func verifyTestChild() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}

	return updatetransport.RunVerifyWorker(context.Background(), updatetransport.WorkerConfig{
		Directory: filepath.Dir(executable), BuildVersion: stageTestVersion, PublicKey: stageTestPublicKey(),
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("offline worker attempted network")
		})},
	}, os.Stdin, os.Stdout)
}

func stageTestChild() error {
	input, err := io.ReadAll(io.LimitReader(os.Stdin, (64<<10)+1))
	if err != nil {
		return err
	}

	var request struct {
		Target    updatetransport.MatchRequest `json:"target"`
		Operation string                       `json:"operation"`
	}
	if err := json.Unmarshal(input, &request); err != nil {
		return err
	}

	if strings.HasSuffix(request.Target.ReleaseTag, "-hang") {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{stageProtocolField: 2, stageOperationField: request.Operation, "kind": "progress", "written": 1, "total": len(stageData)})

		for {
			time.Sleep(time.Hour)
		}
	}

	if strings.HasSuffix(request.Target.ReleaseTag, "-bad-frame") {
		_, _ = io.WriteString(os.Stdout, "{broken}\n")

		for {
			time.Sleep(time.Hour)
		}
	}

	if handled, err := stageTestFault(input); handled {
		return err
	}

	executable, err := os.Executable()
	if err != nil {
		return err
	}

	data, err := stagePayload(request.Target)
	if err != nil {
		return err
	}

	signature, err := stageTestSign(data)
	if err != nil {
		return err
	}

	if strings.HasSuffix(request.Target.ReleaseTag, "-bad-signature") {
		signature[0] ^= 0xff
	}

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/repos/DevWolk/garminctl/releases/assets/101":
			return assetResponse(data), nil
		case "/repos/DevWolk/garminctl/releases/assets/708":
			return assetResponse(signature), nil
		default:
			return nil, fmt.Errorf("unexpected fixture URL: %s", r.URL)
		}
	})}

	return updatetransport.RunStageWorker(context.Background(), updatetransport.WorkerConfig{
		Directory: filepath.Dir(executable), BuildVersion: stageTestVersion,
		PublicKey: stageTestPublicKey(), HTTPClient: client,
	}, bytes.NewReader(input), os.Stdout)
}

func stageSelection(tag string) (updatetransport.MatchRequest, updatetransport.ResolvedRelease) {
	version := strings.TrimPrefix(tag, "v")
	request := updatetransport.MatchRequest{ReleaseTag: tag, OS: runtime.GOOS, Arch: runtime.GOARCH}
	name := "garminctl_" + strings.ReplaceAll(version, "-", ".") + "_linux_amd64.deb"
	request.Installation = updatetransport.InstallDEB

	if runtime.GOOS == windowsOS {
		name = "garminctl_" + tag + "_windows_amd64_setup.exe"
		request.Installation = updatetransport.InstallSetup
	}

	return request, updatetransport.ResolvedRelease{
		ReleaseID: 41, Release: updatetransport.Release{Tag: tag},
		Package: apiAsset(101, name, int64(len(stageData))), Signature: apiAsset(708, name+".sig", rawSignatureSize),
	}
}

func stagerForTest(t *testing.T) (*updatetransport.Stager, string) {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	cache := filepath.Join(t.TempDir(), "private-stage-cache")

	stager, err := updatetransport.NewStager(updatetransport.StagerConfig{
		CLIPath: executable, CacheDir: cache,
		BuildVersion: stageTestVersion, PublicKey: stageTestPublicKey(),
	})
	if err != nil {
		t.Fatal(err)
	}

	return stager, cache
}

func TestStagerRunsCachedCLIAndRetainsVerifiedPackage(t *testing.T) {
	t.Parallel()
	stager, cache := stagerForTest(t)
	request, release := stageSelection(tag120)

	var progress []updatetransport.StageProgress

	result, err := stager.Stage(t.Context(), request, release, func(p updatetransport.StageProgress) { progress = append(progress, p) })
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := result.Close(); err != nil {
			t.Error(err)
		}
	})

	digest := sha256.Sum256(stageData)
	if result.Digest() != hex.EncodeToString(digest[:]) || result.Release() != release || len(progress) == 0 {
		t.Fatalf("bad receipt: digest=%s release=%+v progress=%v", result.Digest(), result.Release(), progress)
	}

	relative, err := filepath.Rel(cache, result.Path())
	if err != nil || strings.HasPrefix(relative, "..") {
		t.Fatalf("package escaped cache: %s", result.Path())
	}

	root, err := os.OpenRoot(filepath.Dir(result.Path()))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	data, err := root.ReadFile(filepath.Base(result.Path()))
	if err != nil || !bytes.Equal(data, stageData) {
		t.Fatalf("staged bytes=%q error=%v", data, err)
	}

	sig, err := root.ReadFile(filepath.Base(result.SignaturePath()))
	if err != nil || rsa.VerifyPKCS1v15(&stagePrivateKey.PublicKey, crypto.SHA256, digest[:], sig) != nil {
		t.Fatalf("bad retained signature: %v", err)
	}
}

func TestStagerJoinsCanceledChildAndRemovesOperation(t *testing.T) {
	t.Parallel()
	stager, cache := stagerForTest(t)
	request, release := stageSelection("v1.2.0-hang")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	result, err := stager.Stage(ctx, request, release, func(updatetransport.StageProgress) { cancel() })
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("result=%+v error=%v", result, err)
	}

	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 0 {
		t.Fatalf("operation leaked: %v error=%v", entries, err)
	}
}

func TestStagerRejectsBadFrameAndJoinsChild(t *testing.T) {
	t.Parallel()
	stager, cache := stagerForTest(t)
	request, release := stageSelection("v1.2.0-bad-frame")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	result, err := stager.Stage(ctx, request, release, nil)
	if result != nil || err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("result=%+v error=%v", result, err)
	}

	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 0 {
		t.Fatalf("operation leaked: %v error=%v", entries, err)
	}
}
