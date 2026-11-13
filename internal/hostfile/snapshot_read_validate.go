package hostfile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const snapshotManifestLimit = 4 << 20

func validSnapshotID(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == id
}

func validSnapshotArea(name string) bool {
	return name != "." && fs.ValidPath(name) && !strings.ContainsAny(name, "/\\\x00")
}

func validateSnapshotManifest(id string, manifest SnapshotManifest) error {
	if err := validateSnapshotHeader(id, manifest); err != nil {
		return err
	}

	missing, err := validateMissingSnapshotSources(manifest.MissingSources)
	if err != nil {
		return err
	}

	return validateRecordedSnapshotFiles(manifest.Files, missing)
}

func validateSnapshotHeader(id string, manifest SnapshotManifest) error {
	if manifest.Version != 1 || manifest.ID != id || !validSnapshotID(id) || manifest.CreatedAt.IsZero() || !json.Valid(manifest.Metadata) || len(manifest.Metadata) > snapshotMetadataLimit || len(manifest.Files) > snapshotEntryLimit || len(manifest.MissingSources) > snapshotEntryLimit {
		return errors.New("unsupported or invalid snapshot manifest")
	}

	return nil
}

func validateMissingSnapshotSources(areas []string) (map[string]bool, error) {
	missing := make(map[string]bool)
	for _, area := range areas {
		if !validSnapshotArea(area) || missing[area] {
			return nil, errors.New("invalid missing snapshot source")
		}

		missing[area] = true
	}

	return missing, nil
}

func validateRecordedSnapshotFiles(files []SnapshotFileInfo, missing map[string]bool) error {
	seen := make(map[string]bool, len(files))

	var total int64

	for _, file := range files {
		if err := validateSnapshotFileRecord(file, missing); err != nil {
			return err
		}

		key := strings.ToLower(file.Area + "/" + file.Path)
		if seen[key] {
			return errors.New("duplicate snapshot file record")
		}

		seen[key] = true

		total += file.Bytes
		if total > snapshotTotalLimit {
			return errors.New("snapshot exceeds its total size limit")
		}
	}

	return nil
}

func validateSnapshotFileRecord(file SnapshotFileInfo, missing map[string]bool) error {
	if !validSnapshotArea(file.Area) || missing[file.Area] || !fs.ValidPath(file.Path) || file.Path == "." || strings.ContainsAny(file.Path, "\\\x00") || !filepath.IsLocal(filepath.FromSlash(file.Path)) || !validSnapshotID(file.Digest) || file.Bytes < 0 || file.Bytes > snapshotFileLimit {
		return errors.New("invalid snapshot file record")
	}

	return nil
}

func verifySnapshotLayout(ctx context.Context, root *os.Root, manifest SnapshotManifest, directory string) error {
	files := map[string]bool{"manifest.json": true}
	directories := make(map[string]bool)

	for _, file := range manifest.Files {
		name := file.Area + "/" + file.Path

		files[name] = true
		for current := path.Dir(name); current != "."; current = path.Dir(current) {
			directories[current] = true
		}
	}

	entries := 0

	err := fs.WalkDir(root.FS(), ".", func(name string, _ fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		if walkErr != nil {
			return walkErr
		}

		if name == "." {
			return nil
		}

		entries++
		if entries > 2*snapshotEntryLimit+2 {
			return errors.New("snapshot layout exceeds its entry limit")
		}

		info, err := root.Lstat(filepath.FromSlash(name))
		if err != nil {
			return err
		}

		if info.IsDir() && directories[name] {
			return Protect(filepath.Join(directory, filepath.FromSlash(name)), true)
		}

		if !info.Mode().IsRegular() || !files[name] {
			return errors.New("snapshot contains an unexpected file or directory")
		}

		delete(files, name)

		return Protect(filepath.Join(directory, filepath.FromSlash(name)), false)
	})
	if err != nil {
		return err
	}

	if len(files) != 0 {
		return errors.New("snapshot is missing a declared file")
	}

	return nil
}

func verifyRetainedSnapshotFile(ctx context.Context, root *os.Root, file SnapshotFileInfo) error {
	name := filepath.Join(file.Area, filepath.FromSlash(file.Path))

	before, err := root.Lstat(name)
	if err != nil {
		return err
	}

	if !before.Mode().IsRegular() || before.Size() != file.Bytes {
		return errors.New("snapshot file type or size differs from its manifest")
	}

	if err := verifySnapshotCopy(ctx, root, name, file); err != nil {
		return err
	}

	after, err := root.Lstat(name)
	if err != nil {
		return err
	}

	if !sameSnapshotFile(before, after) {
		return errors.New("retained snapshot changed during verification")
	}

	return nil
}
