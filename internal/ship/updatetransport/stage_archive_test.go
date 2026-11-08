package updatetransport_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

func stagePayload(target updatetransport.MatchRequest) ([]byte, error) {
	if target.Installation != updatetransport.InstallPortable {
		return stageData, nil
	}

	if target.OS == windowsOS {
		return stageZIP()
	}

	return stageTarGzip()
}

func stageZIP() ([]byte, error) {
	var output bytes.Buffer

	archive := zip.NewWriter(&output)

	file, err := archive.Create("garminctl.exe")
	if err != nil {
		return nil, err
	}

	if _, err := file.Write(stageData); err != nil {
		return nil, err
	}

	if err := archive.Close(); err != nil {
		return nil, err
	}

	return output.Bytes(), nil
}

func stageTarGzip() ([]byte, error) {
	var output bytes.Buffer

	compressed := gzip.NewWriter(&output)

	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{Name: "garminctl", Mode: 0o700, Size: int64(len(stageData))}); err != nil {
		return nil, err
	}

	if _, err := archive.Write(stageData); err != nil {
		return nil, err
	}

	if err := archive.Close(); err != nil {
		return nil, err
	}

	if err := compressed.Close(); err != nil {
		return nil, err
	}

	return output.Bytes(), nil
}

func TestStagerRetainsOriginalSignedPortableArchive(t *testing.T) {
	t.Parallel()
	stager, _ := stagerForTest(t)
	request, release := stageSelection(tag120)
	request.Installation = updatetransport.InstallPortable

	name := "garminctl_1.2.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	if runtime.GOOS == windowsOS {
		name = "garminctl_1.2.0_windows_" + runtime.GOARCH + ".zip"
	}

	want, err := stagePayload(request)
	if err != nil {
		t.Fatal(err)
	}

	release.Package.Name = name
	release.Package.Size = int64(len(want))
	release.Signature.Name = name + ".sig"

	result, err := stager.Stage(t.Context(), request, release, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := result.Close(); err != nil {
			t.Error(err)
		}
	})

	root, err := os.OpenRoot(filepath.Dir(result.Path()))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	data, err := root.ReadFile(filepath.Base(result.Path()))

	digest := sha256.Sum256(want)
	if err != nil || !bytes.Equal(data, want) || result.Digest() != hex.EncodeToString(digest[:]) {
		t.Fatalf("original archive lost: bytes=%d error=%v", len(data), err)
	}

	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("temporary Wails extraction remains: %s", entry.Name())
		}
	}
}
