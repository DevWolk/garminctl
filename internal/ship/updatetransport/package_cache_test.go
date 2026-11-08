package updatetransport_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

func TestPackageCacheSurvivesReopenAndOwnsIndependentVerifiedCopy(t *testing.T) {
	t.Parallel()
	stager, _ := stagerForTest(t)
	request, release := stageSelection(tag120)

	staged, err := stager.Stage(t.Context(), request, release, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := staged.Close(); err != nil {
			t.Error(err)
		}
	})
	directory := filepath.Join(t.TempDir(), "packages")

	cache, err := updatetransport.NewPackageCache(directory, stager)
	if err != nil {
		t.Fatal(err)
	}

	entry, err := cache.Put(t.Context(), request, staged)
	if err != nil {
		t.Fatal(err)
	}

	if err := staged.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := updatetransport.NewPackageCache(directory, stager)
	if err != nil {
		t.Fatal(err)
	}

	assertCachedEntry(t, reopened, entry.ID)

	verified, err := reopened.Open(t.Context(), entry.ID, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := verified.Close(); err != nil {
			t.Error(err)
		}
	})

	if verified.Digest() != entry.Digest {
		t.Fatalf("digest=%s want=%s", verified.Digest(), entry.Digest)
	}

	if err := verified.Close(); err != nil {
		t.Fatal(err)
	}

	assertCachedEntry(t, reopened, entry.ID)
}

func TestPackageCacheRejectsTamperedBytesAndSignature(t *testing.T) {
	t.Parallel()

	for _, signature := range []bool{false, true} {
		t.Run(map[bool]string{false: "payload", true: "signature"}[signature], func(t *testing.T) {
			t.Parallel()
			cache, entry, directory := retainedPackageForTest(t)

			name := entry.Release.Package.Name
			if signature {
				name = entry.Release.Signature.Name
			}

			root, err := os.OpenRoot(filepath.Join(directory, entry.ID))
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()

			data, err := root.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}

			data[0] ^= 0xff
			if err := root.WriteFile(name, data, 0o600); err != nil {
				t.Fatal(err)
			}

			if result, err := cache.Open(t.Context(), entry.ID, nil); result != nil || err == nil {
				t.Fatalf("accepted tamper: %+v %v", result, err)
			}
		})
	}
}

func TestPackageCacheCancellationDoesNotPublishVerifiedResult(t *testing.T) {
	t.Parallel()
	cache, entry, _ := retainedPackageForTest(t)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	result, err := cache.Open(ctx, entry.ID, func(updatetransport.StageProgress) { cancel() })
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled result=%+v error=%v", result, err)
	}

	if entries, err := cache.List(t.Context()); err != nil || len(entries) != 1 {
		t.Fatalf("cancel removed retained entry: %+v %v", entries, err)
	}
}

func retainedPackageForTest(t *testing.T) (*updatetransport.PackageCache, *updatetransport.CachedPackage, string) {
	t.Helper()
	stager, _ := stagerForTest(t)
	request, release := stageSelection(tag120)

	staged, err := stager.Stage(t.Context(), request, release, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := staged.Close(); err != nil {
			t.Error(err)
		}
	})
	directory := t.TempDir()

	cache, err := updatetransport.NewPackageCache(directory, stager)
	if err != nil {
		t.Fatal(err)
	}

	entry, err := cache.Put(t.Context(), request, staged)
	if err != nil {
		t.Fatal(err)
	}

	return cache, entry, directory
}

func TestPackageCacheFailedPutKeepsPreviousAndCleansPending(t *testing.T) {
	t.Parallel()

	cache, _, directory := retainedPackageForTest(t)
	stager, _ := stagerForTest(t)
	request, release := stageSelection(tag120 + "-next")

	staged, err := stager.Stage(t.Context(), request, release, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := staged.Close(); err != nil {
			t.Error(err)
		}
	})

	root, err := os.OpenRoot(filepath.Dir(staged.SignaturePath()))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if err := root.WriteFile(filepath.Base(staged.SignaturePath()), make([]byte, 64), 0o600); err != nil {
		t.Fatal(err)
	}

	if result, err := cache.Put(t.Context(), request, staged); result != nil || err == nil {
		t.Fatalf("accepted bad signature: %+v %v", result, err)
	}

	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("failed put left pending files: %v error=%v", entries, err)
	}

	if entries, err := cache.List(t.Context()); err != nil || len(entries) != 1 {
		t.Fatalf("previous entry lost: %+v %v", entries, err)
	}
}

func TestPackageCacheRejectsUnconfiguredOrOverlappingStorage(t *testing.T) {
	t.Parallel()

	stager, directory := stagerForTest(t)
	for _, path := range []string{"", directory, filepath.Join(directory, "nested"), filepath.Dir(directory)} {
		if _, err := updatetransport.NewPackageCache(path, stager); err == nil {
			t.Fatalf("accepted overlapping/empty path %q", path)
		}
	}

	if _, err := updatetransport.NewPackageCache(t.TempDir(), nil); err == nil {
		t.Fatal("accepted no verifier")
	}
}

func assertCachedEntry(t *testing.T, cache *updatetransport.PackageCache, id string) {
	t.Helper()

	items, err := cache.List(t.Context())
	if err != nil || len(items) != 1 || items[0].ID != id {
		t.Fatalf("catalog=%+v error=%v", items, err)
	}
}
