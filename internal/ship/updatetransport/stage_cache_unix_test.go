//go:build !windows

package updatetransport_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

func TestStagerRejectsSymlinkCacheWithoutTouchingTarget(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()

	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(parent, "cache-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	stager, err := updatetransport.NewStager(updatetransport.StagerConfig{CLIPath: executable, CacheDir: link, BuildVersion: stageTestVersion, PublicKey: stageTestPublicKey()})
	if err != nil {
		t.Fatal(err)
	}

	request, release := stageSelection(tag120)

	result, err := stager.Stage(t.Context(), request, release, nil)
	if result != nil || err == nil {
		t.Fatalf("symlink cache accepted: result=%+v error=%v", result, err)
	}

	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("target changed: %v error=%v", entries, err)
	}

	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o750 {
		t.Fatalf("symlink target permissions changed: info=%v error=%v", info, err)
	}
}
