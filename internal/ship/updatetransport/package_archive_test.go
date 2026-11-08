package updatetransport_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

func TestPackageCacheReverifiesOriginalPortableArchive(t *testing.T) {
	t.Parallel()

	stager, _ := stagerForTest(t)
	request, release := stageSelection(tag120)
	request.Installation = updatetransport.InstallPortable

	extension := ".tar.gz"
	if runtime.GOOS == windowsOS {
		extension = ".zip"
	}

	name := fmt.Sprintf("garminctl_%s_%s_%s%s", strings.TrimPrefix(request.ReleaseTag, "v"), runtime.GOOS, runtime.GOARCH, extension)

	want, err := stagePayload(request)
	if err != nil {
		t.Fatal(err)
	}

	release.Package.Name = name
	release.Package.Size = int64(len(want))
	release.Signature.Name = name + ".sig"

	staged, err := stager.Stage(t.Context(), request, release, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := staged.Close(); err != nil {
			t.Error(err)
		}
	})

	cache, err := updatetransport.NewPackageCache(t.TempDir(), stager)
	if err != nil {
		t.Fatal(err)
	}

	entry, err := cache.Put(t.Context(), request, staged)
	if err != nil {
		t.Fatal(err)
	}

	verified, err := cache.Open(t.Context(), entry.ID, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := verified.Close(); err != nil {
			t.Error(err)
		}
	})

	root, err := os.OpenRoot(filepath.Dir(verified.Path()))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	data, err := root.ReadFile(filepath.Base(verified.Path()))
	if err != nil || !bytes.Equal(data, want) {
		t.Fatalf("signed archive lost after re-verification: %d bytes error=%v", len(data), err)
	}
}
