package updatetransport

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var installerTestKey = sync.OnceValues(func() (*rsa.PrivateKey, error) { return rsa.GenerateKey(rand.Reader, 3072) })

const installLostAcknowledgement = "lost acknowledgement"

type fixtureInstallerOwner struct {
	wait   func(context.Context) error
	closed *int
}

func (o fixtureInstallerOwner) Wait(ctx context.Context) error { return o.wait(ctx) }
func (o fixtureInstallerOwner) Close() error                   { *o.closed++; return nil }

func installWorkerFixture(t *testing.T) (InstallWorkerConfig, installRequest) {
	t.Helper()

	key, err := installerTestKey()
	if err != nil {
		t.Fatal(err)
	}

	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	op := strings.Repeat("a", 64)
	if err := writeOperationMarker(dir, op); err != nil {
		t.Fatal(err)
	}

	target := MatchRequest{ReleaseTag: "v7.8.9", OS: runtime.GOOS, Arch: runtime.GOARCH, Installation: InstallDEB}
	if runtime.GOOS == platformWindows {
		target.Installation = InstallSetup
	}

	name, err := packageFilename(target)
	if err != nil {
		t.Skipf("native installer unsupported: %v", err)
	}

	data := []byte("inert installer fixture")
	digest := sha256.Sum256(data)

	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if err := root.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := root.WriteFile(name+".sig", signature, 0o600); err != nil {
		t.Fatal(err)
	}

	identity, err := keyID(der)
	if err != nil {
		t.Fatal(err)
	}

	release := ResolvedRelease{ReleaseID: 1, Release: Release{Tag: target.ReleaseTag}, Package: AssetRef{ID: 2, Name: name, Size: int64(len(data)), URL: "https://api.github.com/repos/DevWolk/garminctl/releases/assets/2"}, Signature: AssetRef{ID: 3, Name: name + ".sig", Size: packageSignatureSize, URL: "https://api.github.com/repos/DevWolk/garminctl/releases/assets/3"}}

	options := InstallOptions{Desktop: filepath.Join(dir, "garmin-desktop"), CLI: filepath.Join(dir, "garminctl"), Args: []string{"-root", filepath.Join(dir, "dump")}}
	if runtime.GOOS == platformWindows {
		options.Desktop += ".exe"
		options.CLI += ".exe"
		options.Root = dir
	}

	request := installRequest{Protocol: installProtocol, Operation: op, BuildVersion: "v1.0.0", KeyID: identity, OwnerPID: 42, Target: target, Release: release, Digest: hex.EncodeToString(digest[:]), Install: options}

	return InstallWorkerConfig{Directory: dir, BuildVersion: request.BuildVersion, PublicKey: der}, request
}

func installFixtureInput(t *testing.T, request installRequest, decision installDecision) io.ReadCloser {
	t.Helper()

	var data bytes.Buffer
	if err := writeInstallFrame(&data, request); err != nil {
		t.Fatal(err)
	}

	if err := writeInstallFrame(&data, decision); err != nil {
		t.Fatal(err)
	}

	return io.NopCloser(bytes.NewReader(data.Bytes()))
}

func TestInstallerWorkerRequiresCommitAndOwnerExitBeforeExecution(t *testing.T) {
	t.Parallel()

	for _, commit := range []bool{false, true} {
		name := "abort"
		if commit {
			name = "commit"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()
			config, request := installWorkerFixture(t)
			events := []string{}
			closed := 0
			host := installHost{parentPID: 42, openOwner: func(pid int) (installerOwner, error) {
				if pid != 42 {
					t.Fatal("wrong executable owner")
				}

				return fixtureInstallerOwner{closed: &closed, wait: func(context.Context) error { events = append(events, "owner exit"); return nil }}, nil
			}, run: func(context.Context, installRequest, string) error { events = append(events, "install"); return nil }, restart: func(context.Context, InstallOptions, string) error { events = append(events, "restart"); return nil }}

			var output bytes.Buffer

			err := runInstallWorker(t.Context(), config, installFixtureInput(t, request, installDecision{Protocol: installProtocol, Operation: request.Operation, Commit: commit}), &output, host)
			if err != nil {
				t.Fatal(err)
			}

			want := []string{}
			if commit {
				want = []string{"owner exit", "install", "restart"}
			}

			if !slices.Equal(events, want) || closed != 1 {
				t.Fatalf("sequence=%v close=%d", events, closed)
			}

			if !bytes.Contains(output.Bytes(), []byte(`"kind":"ready"`)) {
				t.Fatal("no verified Ready")
			}

			if bytes.Contains(output.Bytes(), []byte(`"kind":"accepted"`)) != commit {
				t.Fatal("accepted without exact commit")
			}
		})
	}
}

func TestInstallerWorkerRejectsUntrustedInputBeforeExecution(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"protocol", "operation", "key", "build", "wrong parent", "portable", "bad signature", "bad size", "escaping desktop"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			config, request := installWorkerFixture(t)

			switch name {
			case "protocol":
				request.Protocol++
			case "operation":
				request.Operation = strings.Repeat("b", 64)
			case "key":
				request.KeyID = strings.Repeat("b", 64)
			case "build":
				request.BuildVersion = "other"
			case "wrong parent":
				request.OwnerPID++
			case "portable":
				request.Target.Installation = InstallPortable
			case "bad signature":
				root, err := os.OpenRoot(config.Directory)
				if err != nil {
					t.Fatal(err)
				}
				defer root.Close()

				if err := root.WriteFile(request.Release.Signature.Name, make([]byte, 384), 0o600); err != nil {
					t.Fatal(err)
				}
			case "bad size":
				request.Release.Package.Size++
			case "escaping desktop":
				request.Install.Desktop = "../desktop"
			}

			closed := 0
			host := installHost{parentPID: 42, openOwner: func(int) (installerOwner, error) {
				return fixtureInstallerOwner{closed: &closed, wait: func(context.Context) error { return nil }}, nil
			}, run: func(context.Context, installRequest, string) error { t.Fatal("invalid payload executed"); return nil }}

			var output bytes.Buffer

			err := runInstallWorker(t.Context(), config, installFixtureInput(t, request, installDecision{Protocol: 1, Operation: request.Operation, Commit: true}), &output, host)
			if err == nil || output.Len() != 0 {
				t.Fatalf("untrusted worker=%v output=%q", err, output.String())
			}
		})
	}
}

func TestInstallerWorkerRechecksBytesAfterOwnerExit(t *testing.T) {
	t.Parallel()
	config, request := installWorkerFixture(t)
	closed, calls := 0, 0
	host := installHost{parentPID: 42, openOwner: func(int) (installerOwner, error) {
		return fixtureInstallerOwner{closed: &closed, wait: func(context.Context) error {
			root, err := os.OpenRoot(config.Directory)
			if err != nil {
				return err
			}
			defer root.Close()

			return root.WriteFile(request.Release.Package.Name, []byte("changed after Ready"), 0o600)
		}}, nil
	}, run: func(context.Context, installRequest, string) error { calls++; return nil }}

	var output bytes.Buffer

	err := runInstallWorker(t.Context(), config, installFixtureInput(t, request, installDecision{Protocol: 1, Operation: request.Operation, Commit: true}), &output, host)
	if err == nil || calls != 0 || closed != 1 {
		t.Fatalf("tampered handoff=%v calls=%d closed=%d", err, calls, closed)
	}

	var record map[string]any

	root, err := os.OpenRoot(config.Directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	data, err := root.ReadFile("install-result.json")
	if err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}

	if record["status"] != "failed" {
		t.Fatalf("result=%s", data)
	}
}

type lostInstallAcknowledgement struct{ writes int }

func (w *lostInstallAcknowledgement) Write(data []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, io.ErrClosedPipe
	}

	return len(data), nil
}

func TestInstallerWorkerDoesNotRepeatOrRestartOnUncertainOutcome(t *testing.T) {
	t.Parallel()

	for _, name := range []string{installLostAcknowledgement, "owner wait failed", "installer failed", "installer still running"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			config, request := installWorkerFixture(t)
			closed, runs, restarts := 0, 0, 0
			failure := errors.New("fixture failure")
			host := installHost{parentPID: 42, openOwner: func(int) (installerOwner, error) {
				return fixtureInstallerOwner{closed: &closed, wait: func(context.Context) error {
					if name == "owner wait failed" {
						return failure
					}

					return nil
				}}, nil
			}, run: func(context.Context, installRequest, string) error {
				runs++

				if name == "installer failed" {
					return failure
				}

				if name == "installer still running" {
					return errInstallerOutcomeUnknown
				}

				return nil
			}, restart: func(context.Context, InstallOptions, string) error { restarts++; return nil }}

			err := runInstallWorker(t.Context(), config, installFixtureInput(t, request, installDecision{Protocol: installProtocol, Operation: request.Operation, Commit: true}), &lostInstallAcknowledgement{}, host)

			wantRuns := 1
			if name == "owner wait failed" {
				wantRuns = 0
			}

			wantRestarts := 0
			if name == installLostAcknowledgement {
				wantRestarts = 1
			}

			if runs != wantRuns || restarts != wantRestarts || closed != 1 || (err == nil) != (name == installLostAcknowledgement) {
				t.Fatalf("outcome=%v runs=%d restarts=%d close=%d", err, runs, restarts, closed)
			}
		})
	}
}

func TestInstallerWorkerCancellationClosesPendingDecision(t *testing.T) {
	t.Parallel()
	config, request := installWorkerFixture(t)

	input, writer := io.Pipe()
	defer func() { _ = writer.Close() }()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	closed := 0
	host := installHost{parentPID: 42, openOwner: func(int) (installerOwner, error) {
		return fixtureInstallerOwner{closed: &closed, wait: func(context.Context) error { return errors.New("owner waited before commit") }}, nil
	}}
	done := make(chan error, 1)
	ready := make(chan struct{})

	go func() { done <- runInstallWorker(ctx, config, input, installerReadyWriter{ready}, host) }()

	if err := writeInstallFrame(writer, request); err != nil {
		t.Fatal(err)
	}

	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("worker exited before Ready: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) || closed != 1 {
		t.Fatalf("pending cancellation=%v owner close=%d", err, closed)
	}
}

type installerReadyWriter struct{ ready chan struct{} }

func (w installerReadyWriter) Write(data []byte) (int, error) {
	close(w.ready)
	return len(data), nil
}
