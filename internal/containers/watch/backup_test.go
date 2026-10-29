package watch_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/watch"
	"github.com/DevWolk/garminctl/internal/device"
)

const backupMissingPath = "missing"

type backupFiles struct {
	entries []device.Entry
	err     error
}

func (s backupFiles) List(context.Context, string) ([]device.Entry, error) { return s.entries, nil }
func (s backupFiles) ReadFile(_ context.Context, path string) ([]byte, error) {
	if path == backupMissingPath {
		return nil, s.err
	}

	return []byte{1, 2, 3}, nil
}

func TestBackupRetainsReadFailurePolicy(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		err      error
		terminal bool
	}{
		{name: "missing", err: device.ErrNotFound},
		{name: "ordinary read failure", err: errors.New("synthetic read failure")},
		{name: "cancel", err: context.Canceled, terminal: true},
		{name: "deadline", err: context.DeadlineExceeded, terminal: true},
		{name: "disconnect", err: device.ErrDisconnected, terminal: true},
		{name: "disconnect with deadline", err: errors.Join(device.ErrDisconnected, context.DeadlineExceeded), terminal: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source := backupFiles{entries: []device.Entry{{Name: backupMissingPath}, {Name: "good"}}, err: test.err}
			skipped, copied := 0, 0
			observer := watch.BackupObserver{
				Skipped: func(path string, err error) {
					skipped++

					if path != backupMissingPath || !errors.Is(err, test.err) {
						t.Errorf("skip path=%s error=%v", path, err)
					}
				},
				Copied: func(path string, size int) {
					copied++

					if path != "good" || size != 3 {
						t.Errorf("copied path=%s size=%d", path, size)
					}
				},
			}

			result, err := watch.NewBackupAction(source, observer).Run(t.Context(), watch.BackupRequest{Destination: t.TempDir()})
			if test.terminal {
				if !errors.Is(err, test.err) || result != nil || copied != 0 || skipped != 0 {
					t.Fatalf("terminal backup result=%+v error=%v copied=%d skipped=%d", result, err, copied, skipped)
				}

				return
			}

			if err != nil || copied != 1 || skipped != 1 {
				t.Fatalf("partial backup result=%+v error=%v copied=%d skipped=%d", result, err, copied, skipped)
			}

			requireBackupResult(t, result, watch.BackupResult{Files: 1, Bytes: 3})
		})
	}
}

func TestBackupCannotEscapeDestinationThroughReportedNames(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	source := backupFiles{entries: []device.Entry{{Name: "../escape.bin"}}}

	result, err := watch.NewBackupAction(source, watch.BackupObserver{}).Run(t.Context(), watch.BackupRequest{Destination: filepath.Join(parent, "dest")})
	if err == nil || result != nil {
		t.Fatalf("escaping name accepted: result=%+v error=%v", result, err)
	}

	if _, err := os.Stat(filepath.Join(parent, "escape.bin")); !os.IsNotExist(err) {
		t.Fatalf("backup escaped destination: %v", err)
	}
}

func requireBackupResult(t *testing.T, got *watch.BackupResult, want watch.BackupResult) {
	t.Helper()

	if got == nil || *got != want {
		t.Fatalf("backup result=%+v want %+v", got, want)
	}
}
