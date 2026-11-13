package updatetransport_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

func TestPackageExportIsIndependentAuthenticatedAndPersistent(t *testing.T) {
	t.Parallel()
	cache, entry, catalog := retainedPackageForTest(t)
	directory := filepath.Join(t.TempDir(), "recovery")

	result, err := cache.Export(t.Context(), entry.ID, directory, nil)
	if err != nil || result == nil {
		t.Fatalf("export=%+v error=%v", result, err)
	}

	if result.Entry != *entry || filepath.Dir(result.Directory) != directory || filepath.Dir(result.Path) != result.Directory || filepath.Dir(result.SignaturePath) != result.Directory {
		t.Fatalf("invalid export receipt=%+v", result)
	}

	assertRecoveryCopies(t, filepath.Join(catalog, entry.ID), result.Directory, entry.Release.Package.Name, entry.Release.Signature.Name)

	if err := os.RemoveAll(filepath.Join(catalog, entry.ID)); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(result.Path); err != nil {
		t.Fatalf("export still depends on retained catalog: %v", err)
	}

	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(result.Directory) {
		t.Fatalf("export publication=%v error=%v", entries, err)
	}
}

func TestPackageExportRejectsTamperWithoutPublishingOrRemovingCatalog(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		signature bool
	}{
		{name: "payload"}, {name: "signature", signature: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cache, entry, catalog := retainedPackageForTest(t)

			name := entry.Release.Package.Name
			if tc.signature {
				name = entry.Release.Signature.Name
			}

			root, err := os.OpenRoot(filepath.Join(catalog, entry.ID))
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

			directory := filepath.Join(t.TempDir(), "recovery")
			if result, err := cache.Export(t.Context(), entry.ID, directory, nil); result != nil || err == nil {
				t.Fatalf("accepted tampered export=%+v error=%v", result, err)
			}

			assertNoPackageExport(t, directory)
			assertCachedEntry(t, cache, entry.ID)
		})
	}
}

func TestPackageExportRefusesCancellationOverlapAndInvalidIdentity(t *testing.T) {
	t.Parallel()
	cache, entry, catalog := retainedPackageForTest(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	directory := filepath.Join(t.TempDir(), "recovery")
	if result, err := cache.Export(ctx, entry.ID, directory, nil); result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled export=%+v error=%v", result, err)
	}

	for _, destination := range []string{"", catalog, filepath.Join(catalog, "nested"), filepath.Dir(catalog)} {
		if result, err := cache.Export(t.Context(), entry.ID, destination, nil); result != nil || err == nil {
			t.Fatalf("accepted overlapping export %q: %+v %v", destination, result, err)
		}
	}

	if result, err := cache.Export(t.Context(), "../outside", directory, nil); result != nil || err == nil {
		t.Fatalf("accepted invalid id=%+v error=%v", result, err)
	}

	assertNoPackageExport(t, directory)
	assertCachedEntry(t, cache, entry.ID)
}

func TestPackageExportCancellationDuringWorkerJoinsAndPreservesSource(t *testing.T) {
	t.Parallel()
	cache, entry, _ := retainedPackageForTest(t)
	directory := filepath.Join(t.TempDir(), "recovery")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	called := false

	value, err := cache.Export(ctx, entry.ID, directory, func(updatetransport.StageProgress) { called = true; cancel() })
	if !called || value != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("worker cancellation=%t export=%+v error=%v", called, value, err)
	}

	assertNoPackageExport(t, directory)
	assertCachedEntry(t, cache, entry.ID)
}

func assertRecoveryCopies(t *testing.T, source, destination string, names ...string) {
	t.Helper()

	first, err := os.OpenRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	second, err := os.OpenRoot(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	for _, name := range names {
		original, err := first.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}

		copied, err := second.ReadFile(name)
		if err != nil || !bytes.Equal(original, copied) {
			t.Fatalf("copy %s differs: %v", name, err)
		}
	}
}

func assertNoPackageExport(t *testing.T, directory string) {
	t.Helper()

	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return
	}

	if err != nil || len(entries) != 0 {
		t.Fatalf("failed export left entries=%v error=%v", entries, err)
	}
}
