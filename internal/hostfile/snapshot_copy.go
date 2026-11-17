package hostfile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type snapshotReader struct {
	ctx    context.Context //nolint:containedctx // Reader borrows the synchronous copy call's context and never escapes it.
	reader io.Reader
}

func (r snapshotReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}

	return r.reader.Read(p)
}

func copySnapshotFile(ctx context.Context, destination, source *os.Root, input snapshotInput, absolute string) (SnapshotFileInfo, error) {
	receipt := SnapshotFileInfo{Area: input.area, Path: input.path, Bytes: input.info.Size()}

	file, err := source.Open(filepath.FromSlash(input.path))
	if err != nil {
		return receipt, err
	}

	defer func() { _ = file.Close() }()

	if err := checkSnapshotSource(source, file, input); err != nil {
		return receipt, err
	}

	target := filepath.Join(input.area, filepath.FromSlash(input.path))
	if err := destination.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return receipt, err
	}
	// Protect every newly created level, including on Windows where modes alone are insufficient.
	if err := protectSnapshotDirectories(absolute, filepath.Dir(target)); err != nil {
		return receipt, err
	}

	out, err := destination.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return receipt, err
	}

	defer func() { _ = out.Close() }()

	if err := Protect(filepath.Join(absolute, target), false); err != nil {
		return receipt, err
	}

	hash := sha256.New()

	n, err := io.Copy(io.MultiWriter(out, hash), snapshotReader{ctx: ctx, reader: io.LimitReader(file, input.info.Size()+1)})
	if err != nil {
		return receipt, err
	}

	if n != input.info.Size() {
		return receipt, errors.New("snapshot source changed size during copy")
	}

	if err := checkSnapshotSource(source, file, input); err != nil {
		return receipt, err
	}

	if err := out.Sync(); err != nil {
		return receipt, err
	}

	if err := out.Close(); err != nil {
		return receipt, err
	}

	receipt.Digest = hex.EncodeToString(hash.Sum(nil))
	if err := verifySnapshotCopy(ctx, destination, target, receipt); err != nil {
		return receipt, err
	}

	return receipt, nil
}

func checkSnapshotSource(source *os.Root, file *os.File, input snapshotInput) error {
	opened, err := file.Stat()
	if err != nil {
		return err
	}

	path, err := source.Lstat(filepath.FromSlash(input.path))
	if err != nil {
		return err
	}

	if !sameSnapshotFile(input.info, opened) || !sameSnapshotFile(input.info, path) {
		return errors.New("snapshot source changed during copy")
	}

	return nil
}

func protectSnapshotDirectories(absolute, relative string) error {
	for relative != "." {
		if err := Protect(filepath.Join(absolute, relative), true); err != nil {
			return err
		}

		relative = filepath.Dir(relative)
	}

	return nil
}

func verifySnapshotCopy(ctx context.Context, root *os.Root, path string, expected SnapshotFileInfo) error {
	file, err := root.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()

	n, err := io.Copy(hash, snapshotReader{ctx: ctx, reader: io.LimitReader(file, expected.Bytes+1)})
	if err != nil {
		return err
	}

	if n != expected.Bytes || hex.EncodeToString(hash.Sum(nil)) != expected.Digest {
		return errors.New("snapshot readback does not match copied bytes")
	}

	return nil
}

func writeSnapshotManifest(ctx context.Context, root *os.Root, manifest SnapshotManifest, absolute string) error {
	return writeSnapshotManifestAs(ctx, root, "manifest.json", manifest, absolute)
}

func writeSnapshotManifestAs(ctx context.Context, root *os.Root, name string, manifest SnapshotManifest, absolute string) error {
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}

	if len(data) > snapshotManifestLimit {
		return errors.New("snapshot manifest exceeds the byte limit")
	}

	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}

	defer func() { _ = file.Close() }()

	if err := Protect(filepath.Join(absolute, name), false); err != nil {
		return err
	}

	if _, err := file.Write(data); err != nil {
		return err
	}

	if err := file.Sync(); err != nil {
		return err
	}

	if err := file.Close(); err != nil {
		return err
	}

	digest := sha256.Sum256(data)

	return verifySnapshotCopy(ctx, root, name, SnapshotFileInfo{Bytes: int64(len(data)), Digest: hex.EncodeToString(digest[:])})
}
