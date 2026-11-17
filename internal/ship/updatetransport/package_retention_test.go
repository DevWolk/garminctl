package updatetransport_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

func TestPackageCatalogNamesAndRemovesUnusableEntries(t *testing.T) {
	t.Parallel()

	cache, entry, directory := retainedPackageForTest(t)
	stray := strings.Repeat("ab", 32)

	if err := os.Mkdir(filepath.Join(directory, stray), 0o700); err != nil {
		t.Fatal(err)
	}

	rows, skipped, err := cache.Catalog(t.Context())
	if err != nil || len(rows) != 1 || rows[0].ID != entry.ID || len(skipped) != 1 || skipped[0].ID != stray || skipped[0].Reason == "" {
		t.Fatalf("rows=%+v skipped=%+v error=%v", rows, skipped, err)
	}

	rows, skipped, err = rotatedKeyCache(t, directory).Catalog(t.Context())
	if err != nil || len(rows) != 0 || len(skipped) != 2 {
		t.Fatalf("another key: rows=%+v skipped=%+v error=%v", rows, skipped, err)
	}
}

func TestPackageCacheRemoveDeletesEntriesOfAnotherKeyByIdentity(t *testing.T) {
	t.Parallel()

	_, entry, directory := retainedPackageForTest(t)
	stray := strings.Repeat("ab", 32)

	if err := os.Mkdir(filepath.Join(directory, stray), 0o700); err != nil {
		t.Fatal(err)
	}

	foreign := rotatedKeyCache(t, directory)

	for _, id := range []string{stray, entry.ID} {
		if err := foreign.Remove(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}

	if err := foreign.Remove(t.Context(), entry.ID); !errors.Is(err, updatetransport.ErrPackageNotRetained) {
		t.Fatalf("second removal error=%v", err)
	}

	if err := foreign.Remove(t.Context(), "../"+entry.ID); err == nil {
		t.Fatal("removal accepted a path")
	}

	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 0 {
		t.Fatalf("left after removal: %v error=%v", entries, err)
	}
}

func TestPackageCatalogWithoutVerifierListsAndRemovesButNeverOpens(t *testing.T) {
	t.Parallel()

	_, entry, directory := retainedPackageForTest(t)

	catalog, err := updatetransport.NewPackageCatalog(directory)
	if err != nil {
		t.Fatal(err)
	}

	assertCachedEntry(t, catalog, entry.ID)

	if verified, err := catalog.Open(t.Context(), entry.ID, nil); verified != nil || err == nil {
		t.Fatalf("opened without a verifier: %+v %v", verified, err)
	}

	if exported, err := catalog.Export(t.Context(), entry.ID, filepath.Join(t.TempDir(), "recovery"), nil); exported != nil || err == nil {
		t.Fatalf("exported without a verifier: %+v %v", exported, err)
	}

	if err := catalog.Prune(t.Context(), 1); err == nil {
		t.Fatal("pruned without a verifier")
	}

	if err := catalog.Remove(t.Context(), entry.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := updatetransport.NewPackageCatalog(""); err == nil {
		t.Fatal("accepted an empty directory")
	}
}

// savePackage stages and saves one synthetic release, then dates the entry.
func savePackage(t *testing.T, cache *updatetransport.PackageCache, stager *updatetransport.Stager, directory, tag string, age time.Duration) string {
	t.Helper()

	request, release := stageSelection(tag)

	staged, err := stager.Stage(t.Context(), request, release, nil)
	if err != nil {
		t.Fatal(err)
	}

	entry, err := cache.Put(t.Context(), request, staged)
	if err := errors.Join(err, staged.Close()); err != nil {
		t.Fatal(err)
	}

	when := time.Now().Add(-age)
	if err := os.Chtimes(filepath.Join(directory, entry.ID), when, when); err != nil {
		t.Fatal(err)
	}

	return entry.ID
}

func TestPackageCachePruneKeepsRecentPackagesAndTheRunningBuild(t *testing.T) {
	t.Parallel()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	stages := filepath.Join(t.TempDir(), "stage")

	stager, err := updatetransport.NewStager(updatetransport.StagerConfig{CLIPath: executable, CacheDir: stages, BuildVersion: stageTestVersion, PublicKey: stageTestPublicKey()})
	if err != nil {
		t.Fatal(err)
	}

	directory := filepath.Join(t.TempDir(), "packages")

	cache, err := updatetransport.NewPackageCache(directory, stager)
	if err != nil {
		t.Fatal(err)
	}

	oldest := savePackage(t, cache, stager, directory, "v1.0.0", 4*time.Hour)
	older := savePackage(t, cache, stager, directory, "v1.1.0", 3*time.Hour)
	recent := savePackage(t, cache, stager, directory, "v1.2.0", 2*time.Hour)
	latest := savePackage(t, cache, stager, directory, "v1.3.0", time.Hour)
	unreadable := strings.Repeat("ab", 32)

	for name, age := range map[string]time.Duration{
		filepath.Join(directory, unreadable):            72 * time.Hour,
		filepath.Join(directory, ".pending-abandoned"):  72 * time.Hour,
		filepath.Join(directory, ".pending-active"):     time.Minute,
		filepath.Join(stages, "stage-abandoned"):        72 * time.Hour,
		filepath.Join(stages, "stage-active"):           time.Minute,
		filepath.Join(stages, "unrelated-old-neighbor"): 72 * time.Hour,
	} {
		if err := os.MkdirAll(name, 0o700); err != nil {
			t.Fatal(err)
		}

		when := time.Now().Add(-age)
		if err := os.Chtimes(name, when, when); err != nil {
			t.Fatal(err)
		}
	}

	// The installed build is v1.0.0; its version carries no "v" as in portable builds.
	installed, err := updatetransport.NewStager(updatetransport.StagerConfig{CLIPath: executable, CacheDir: stages, BuildVersion: "1.0.0", PublicKey: stageTestPublicKey()})
	if err != nil {
		t.Fatal(err)
	}

	owner, err := updatetransport.NewPackageCache(directory, installed)
	if err != nil {
		t.Fatal(err)
	}

	if err := owner.Prune(t.Context(), 0); err == nil {
		t.Fatal("accepted a policy that keeps nothing")
	}

	if err := owner.Prune(t.Context(), 2); err != nil {
		t.Fatal(err)
	}

	assertDirectoryNames(t, directory, oldest, recent, latest, unreadable, ".pending-active")
	assertDirectoryNames(t, stages, "stage-active", "unrelated-old-neighbor")

	if _, err := os.Stat(filepath.Join(directory, older)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("package beyond the retention count stayed: %v", err)
	}
}

func assertDirectoryNames(t *testing.T, directory string, want ...string) {
	t.Helper()

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}

	got := make(map[string]bool, len(entries))
	for _, entry := range entries {
		got[entry.Name()] = true
	}

	for _, name := range want {
		if !got[name] {
			t.Errorf("%s is missing from %s", name, directory)
		}

		delete(got, name)
	}

	if len(got) != 0 {
		t.Errorf("unexpected entries in %s: %v", directory, got)
	}
}
