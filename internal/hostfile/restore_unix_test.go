//go:build !windows

package hostfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotRestoreCompletesWhenOnlyStagingCleanupFails(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("directory permissions do not bind the superuser")
	}

	f := restoreFileFixtureMFA(t, true)
	calls := 0
	f.restorer.replace = func(from, to string) error {
		if err := Replace(from, to); err != nil {
			return err
		}

		calls++
		if calls == 2 {
			// Every reviewed file is in place; only the emptied staging tree stays.
			staging := filepath.Dir(filepath.Dir(from))
			if err := os.Chmod(staging, 0o500); err != nil { //nolint:gosec // G302: the fixture directory must stay listable while refusing removal
				return err
			}

			t.Cleanup(func() { _ = os.Chmod(staging, 0o700) }) //nolint:gosec // G302: restores the fixture directory for TempDir cleanup
		}

		return nil
	}

	result, err := f.restorer.Restore(t.Context(), f.target, f.safety)
	if err != nil || result == nil || !result.Completed || result.Replaced != 2 || !errors.Is(result.Cleanup, os.ErrPermission) {
		t.Fatalf("restore=%+v error=%v", result, err)
	}

	for name, want := range map[string]string{"account.json": restoreOldAccount, restoreMFA: "old pending MFA"} {
		if data, err := f.live.ReadFile(name); err != nil || string(data) != want {
			t.Fatalf("%s=%q %v", name, data, err)
		}
	}
}
