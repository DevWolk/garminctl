package hostinfo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestMain runs a fixture child in Docker/native CI, never a desktop or installer.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "version" && os.Getenv("GARMIN_HOSTINFO_FIXTURE") != "" {
		switch os.Getenv("GARMIN_HOSTINFO_FIXTURE") {
		case "overflow":
			fmt.Print(strings.Repeat("x", 9<<10))
		case "wait":
			time.Sleep(10 * time.Second)
		case "fail":
			os.Exit(1)
		default:
			fmt.Print("garminctl fixture-build\n")
		}

		os.Exit(0)
	}

	os.Exit(m.Run())
}

func TestVersionChildIsBoundedAndJoined(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("GARMIN_HOSTINFO_FIXTURE", "version")

	text, err := runProbe(t.Context(), probeCLI, path)
	if err != nil || text != "garminctl fixture-build\n" {
		t.Fatalf("text=%q error=%v", text, err)
	}

	t.Setenv("GARMIN_HOSTINFO_FIXTURE", "overflow")

	if _, err := runProbe(t.Context(), probeCLI, path); !errors.Is(err, io.ErrShortBuffer) {
		t.Fatalf("overflow error=%v", err)
	}

	t.Setenv("GARMIN_HOSTINFO_FIXTURE", "wait")
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	t.Cleanup(cancel)

	started := time.Now()

	if _, err := runProbe(ctx, probeCLI, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel error=%v", err)
	}

	if time.Since(started) > 3*time.Second {
		t.Fatal("version child did not join within its bound")
	}
}

func TestOnlyExactNoOwnerDiagnosticsAreUnmanaged(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("GARMIN_HOSTINFO_FIXTURE", "fail")

	err = exec.CommandContext(t.Context(), path, "version").Run() //nolint:gosec // bounded fixture child uses only this test executable, never a request-controlled program.
	if err == nil {
		t.Fatal("fixture child unexpectedly succeeded")
	}

	for _, tc := range []struct {
		kind             probeCommand
		out, diagnostics string
		unowned          bool
	}{
		{probeDPKG, "", "dpkg-query: no path found matching pattern /fixture/garminctl\n", true},
		{probeRPM, "file /fixture/garminctl is not owned by any package\n", "", true},
		{probeDPKG, "", "dpkg-query: database unavailable", false},
		{probeRPM, "", "error: file /fixture/garminctl: No such file or directory", false},
		{probeCLI, "file /fixture/garminctl is not owned by any package", "", false},
	} {
		if got := unownedQuery(tc.kind, "/fixture/garminctl", tc.out, tc.diagnostics, err); got != tc.unowned {
			t.Errorf("diagnostics=%+v unmanaged=%t", tc, got)
		}
	}
}
