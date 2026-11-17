package updatetransport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPackageCachePruneKeepsOnlyTheLatestSaveOfARepublishedRelease(t *testing.T) {
	t.Parallel()

	first, owner := catalogManifest(t)
	directory := t.TempDir()

	cache, err := NewPackageCache(directory, owner)
	if err != nil {
		t.Fatal(err)
	}

	// The release was published again with other bytes: same tag and
	// installation kind, another digest and therefore another identity.
	second, err := newPackageManifest(first.Target, first.Release, strings.Repeat("c", 64), owner.key)
	if err != nil {
		t.Fatal(err)
	}

	for manifest, age := range map[*packageManifest]time.Duration{&first: 2 * time.Hour, &second: time.Hour} {
		entry := filepath.Join(directory, manifest.ID)
		if err := os.Mkdir(entry, 0o700); err != nil {
			t.Fatal(err)
		}

		if err := writePackageManifest(entry, *manifest); err != nil {
			t.Fatal(err)
		}

		when := time.Now().Add(-age)
		if err := os.Chtimes(entry, when, when); err != nil {
			t.Fatal(err)
		}
	}

	if rows, err := cache.List(t.Context()); err != nil || len(rows) != 2 {
		t.Fatalf("fixture catalog=%+v error=%v", rows, err)
	}

	if err := cache.Prune(t.Context(), 5); err != nil {
		t.Fatal(err)
	}

	rows, err := cache.List(t.Context())
	if err != nil || len(rows) != 1 || rows[0].ID != second.ID {
		t.Fatalf("catalog=%+v error=%v, want only %s", rows, err, second.ID)
	}
}

// installerOperation leaves an operation directory last changed age ago, with
// the result record of a finished installation when finished is set.
func installerOperation(t *testing.T, cache, name string, age time.Duration, finished bool) {
	t.Helper()

	operation := filepath.Join(cache, name)
	if err := os.Mkdir(operation, 0o700); err != nil {
		t.Fatal(err)
	}

	when := time.Now().Add(-age)

	if finished {
		result := filepath.Join(operation, installResultName)
		if err := os.WriteFile(result, []byte(`{"operation":"fixture","tag":"v1.2.0","status":"installed"}`), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := os.Chtimes(result, when, when); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Chtimes(operation, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestInstallerPreparationClearsAbandonedOperations(t *testing.T) {
	t.Parallel()

	cache := t.TempDir()

	// Two installations finished long ago; the later result is still the one
	// a status request reports. A third operation is running.
	installerOperation(t, cache, "stage-finished-last-month", 30*24*time.Hour, true)
	installerOperation(t, cache, "stage-finished-last-week", 7*24*time.Hour, true)
	installerOperation(t, cache, "stage-running-now", time.Minute, false)

	// Preparation stops at the missing CLI, after the directory was cleared.
	installer := &Installer{stager: &Stager{cacheDir: cache, cliPath: filepath.Join(t.TempDir(), "missing-cli")}}
	if pending, err := installer.prepare(t.Context(), MatchRequest{}, &StagedPackage{}, InstallOptions{}); pending != nil || err == nil {
		t.Fatalf("pending=%+v error=%v", pending, err)
	}

	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 2 || entries[0].Name() != "stage-finished-last-week" || entries[1].Name() != "stage-running-now" {
		t.Fatalf("operations=%v error=%v", entries, err)
	}

	if latest, err := LastInstallResult(cache); err != nil || latest == nil || filepath.Base(latest.Directory) != "stage-finished-last-week" {
		t.Fatalf("latest result=%+v error=%v", latest, err)
	}
}
