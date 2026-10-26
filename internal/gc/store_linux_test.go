package gc

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The subprocess limit forces a genuine partial file write without changing
// process-wide resource limits in the parent test runner.
func TestStorePartialWritePreservesPreviousSession(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.json")
	if err := New(path).saveSession(testSession("old@example.invalid")); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestStoreWriteLimitHelper$") //nolint:gosec // execute this test binary with a fixed helper selector

	cmd.Env = append(os.Environ(), "GARMINCTL_TEST_WRITE_LIMIT="+path)

	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("write-limit helper: %v\n%s", err, output)
	}

	assertSessionFile(t, path, "old@example.invalid")
	assertNoStoreTemps(t, path)
}

func TestStoreWriteLimitHelper(t *testing.T) { //nolint:paralleltest // isolated helper changes process-wide resource limits
	path := os.Getenv("GARMINCTL_TEST_WRITE_LIMIT")
	if path == "" {
		t.Skip("only run in the write-limit subprocess")
	}

	signal.Ignore(syscall.SIGXFSZ)

	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
		t.Fatal(err)
	}

	limit.Cur = 128
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
		t.Fatal(err)
	}

	c := New(path)
	if err := c.saveSession(testSession(strings.Repeat("x", 1024))); err == nil {
		t.Fatal("save ignored the partial-write error")
	}

	if c.Email() != "old@example.invalid" {
		t.Fatal("failed partial write replaced the in-memory account")
	}
}
