package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/testdump"
)

func TestRunMusicBatchAndBackupPreserveFiles(t *testing.T) {
	t.Parallel()
	root, input, dest := testdump.Root(t), t.TempDir(), t.TempDir()
	payload := []byte("synthetic audio payload")

	first, second := filepath.Join(input, "first.mp3"), filepath.Join(input, "second.mp3")
	for _, file := range []string{first, second} {
		if err := os.WriteFile(file, payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var out, diagnostics bytes.Buffer
	if err := run([]string{rootFlag, root, "music", musicAddSubcommand, first, second}, &out, &diagnostics); err != nil {
		t.Fatal(err)
	}

	if err := run([]string{rootFlag, root, "backup", "-skip", "", dest}, &out, &diagnostics); err != nil {
		t.Fatal(err)
	}

	backup, err := os.OpenRoot(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()

	for _, file := range []string{"Music/first.mp3", "Music/second.mp3"} {
		data, err := backup.ReadFile(filepath.FromSlash(file))
		if err != nil || !bytes.Equal(data, payload) {
			t.Fatalf("backup changed %s: %q, %v", file, data, err)
		}
	}

	if err := run([]string{rootFlag, root, "music", "rm", "Music/first.mp3", "Music/second.mp3"}, &out, &diagnostics); err != nil {
		t.Fatal(err)
	}

	watch, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()

	for _, file := range []string{"Music/first.mp3", "Music/second.mp3"} {
		if _, err := watch.Stat(filepath.FromSlash(file)); !os.IsNotExist(err) {
			t.Fatalf("batch did not remove %s: %v", file, err)
		}
	}

	if diagnostics.Len() != 0 {
		t.Fatalf("unexpected diagnostics: %s", &diagnostics)
	}
}
