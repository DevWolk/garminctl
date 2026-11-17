package updatetransport

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type installInputBuffer struct{ bytes.Buffer }

func (*installInputBuffer) Close() error { return nil }

func TestPendingInstallCommitIsConsumedEvenWhenAcknowledgementIsLost(t *testing.T) {
	t.Parallel()

	for _, acknowledge := range []bool{false, true} {
		name := installLostAcknowledgement
		if acknowledge {
			name = "accepted"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			request := pendingInstallRequest()

			var reply bytes.Buffer
			if acknowledge {
				if err := writeInstallFrame(&reply, installEvent{Protocol: request.Protocol, Operation: request.Operation, BuildVersion: request.BuildVersion, KeyID: request.KeyID, Kind: "accepted"}); err != nil {
					t.Fatal(err)
				}
			}

			input := &installInputBuffer{}
			pending := &PendingInstall{input: input, output: io.NopCloser(&reply), reader: bufio.NewReader(&reply), request: request}

			err := pending.Commit(t.Context())
			if (err == nil) != acknowledge {
				t.Fatalf("commit=%v", err)
			}

			first := input.String()
			if !pending.attempted || first == "" {
				t.Fatal("commit attempt not retained")
			}

			if err := pending.Commit(context.Background()); err == nil || input.String() != first {
				t.Fatal("commit sent more than once")
			}
		})
	}
}

const foreignInstallValue = "of another owner"

func pendingInstallRequest() installRequest {
	return installRequest{Protocol: installProtocol, Operation: "owned operation", BuildVersion: "fixture", KeyID: "owned key"}
}

func TestPendingInstallRefusesReplyFromAnotherOwner(t *testing.T) {
	t.Parallel()

	request := pendingInstallRequest()

	for _, tc := range []struct {
		name   string
		change func(*installEvent)
	}{
		{"foreign operation", func(e *installEvent) { e.Operation = foreignInstallValue }},
		{"foreign build", func(e *installEvent) { e.BuildVersion = foreignInstallValue }},
		{"foreign key", func(e *installEvent) { e.KeyID = foreignInstallValue }},
		{"foreign protocol", func(e *installEvent) { e.Protocol++ }},
		{"refusal without a reason", func(e *installEvent) { e.Kind = installRefused }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			event := installEvent{Protocol: request.Protocol, Operation: request.Operation, BuildVersion: request.BuildVersion, KeyID: request.KeyID, Kind: "accepted"}
			tc.change(&event)

			var reply bytes.Buffer
			if err := writeInstallFrame(&reply, event); err != nil {
				t.Fatal(err)
			}

			pending := &PendingInstall{input: &installInputBuffer{}, output: io.NopCloser(&reply), reader: bufio.NewReader(&reply), request: request}
			if err := pending.Commit(t.Context()); err == nil {
				t.Fatal("reply of another owner accepted")
			}
		})
	}
}

func TestInstallerPrepareRefusesPortableTarget(t *testing.T) {
	t.Parallel()

	target := MatchRequest{ReleaseTag: installTestTag, OS: runtime.GOOS, Arch: "amd64", Installation: InstallPortable}

	name, err := packageFilename(target)
	if err != nil {
		t.Skipf("no portable package for this system: %v", err)
	}

	cache := t.TempDir()
	payload := &StagedPackage{directory: t.TempDir(), release: ResolvedRelease{ReleaseID: 1, Release: Release{Tag: target.ReleaseTag}, Package: AssetRef{ID: 2, Name: name, Size: 1, URL: "https://api.github.com/repos/DevWolk/garminctl/releases/assets/2"}, Signature: AssetRef{ID: 3, Name: name + ".sig", Size: packageSignatureSize, URL: "https://api.github.com/repos/DevWolk/garminctl/releases/assets/3"}}}
	installer := &Installer{stager: &Stager{cliPath: filepath.Join(cache, "garminctl"), cacheDir: cache, version: "fixture", key: "owned key"}}
	options := InstallOptions{Desktop: filepath.Join(cache, "garmin-desktop"), CLI: filepath.Join(cache, "garminctl")}

	pending, err := installer.Prepare(t.Context(), target, payload, options)
	if pending != nil || err == nil || !strings.Contains(err.Error(), "portable") {
		t.Fatalf("portable preparation=%v %v", pending, err)
	}

	if entries, err := os.ReadDir(cache); err != nil || len(entries) != 0 {
		t.Fatalf("portable preparation touched the cache: %v %v", entries, err)
	}
}

func TestPendingInstallReturnsTheHelperRefusalAsItsError(t *testing.T) {
	t.Parallel()

	request := pendingInstallRequest()
	refusal := installEvent{Protocol: request.Protocol, Operation: request.Operation, BuildVersion: request.BuildVersion, KeyID: request.KeyID, Kind: installRefused, Message: "garminctl.exe is running; close it, then install again"}

	for _, own := range []bool{true, false} {
		event := refusal
		if !own {
			event.Operation = foreignInstallValue
		}

		var reply bytes.Buffer
		if err := writeInstallFrame(&reply, event); err != nil {
			t.Fatal(err)
		}

		pending := &PendingInstall{output: io.NopCloser(&reply), reader: bufio.NewReader(&reply), request: request}

		err := pending.readEvent(t.Context(), installReady, time.Second)
		if err == nil || (err.Error() == refusal.Message) != own {
			t.Fatalf("own=%t refusal=%v", own, err)
		}
	}
}
