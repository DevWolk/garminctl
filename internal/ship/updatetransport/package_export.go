package updatetransport

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/DevWolk/garminctl/internal/hostfile"
)

// ExportedPackage retains an authenticated copy independently of the catalog.
type ExportedPackage struct {
	Entry                          CachedPackage
	Directory, Path, SignaturePath string
}

// Export publishes a manual-recovery copy under a trusted composition directory.
// It never executes a package or modifies the retained catalog.
func (c *PackageCache) Export(ctx context.Context, id, directory string, progress func(StageProgress)) (result *ExportedPackage, err error) {
	if err := c.check(ctx); err != nil {
		return nil, err
	}

	root, err := c.exportDirectory(directory)
	if err != nil {
		return nil, err
	}

	manifest, err := c.readManifest(ctx, id)
	if err != nil {
		return nil, err
	}

	verified, err := c.Open(ctx, id, progress)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, verified.Close()) }()

	if verified.Digest() != manifest.Digest || verified.Release() != manifest.Release {
		return nil, errors.New("recovery catalog changed during verification")
	}

	pending, final, err := preparePackageExport(root)
	if err != nil {
		return nil, err
	}

	defer func() { err = errors.Join(err, os.RemoveAll(pending)) }()

	if err := copyPackagePair(ctx, verified.directory, pending, manifest.Release); err != nil {
		return nil, err
	}
	// Verify the published pair independently, including its copied signature.
	if err := c.verifyPending(ctx, pending, manifest); err != nil {
		return nil, err
	}

	if err := verified.Close(); err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := os.Rename(pending, final); err != nil {
		return nil, err
	}

	return &ExportedPackage{Entry: manifest.CachedPackage, Directory: final, Path: filepath.Join(final, manifest.Release.Package.Name), SignaturePath: filepath.Join(final, manifest.Release.Signature.Name)}, nil
}

func (c *PackageCache) exportDirectory(directory string) (string, error) {
	if directory == "" {
		return "", errors.New("recovery export requires a dedicated directory")
	}

	value, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}

	if packagePathsOverlap(value, c.directory) || packagePathsOverlap(value, c.stager.cacheDir) {
		return "", errors.New("recovery exports must be separate from retained and temporary packages")
	}

	return value, nil
}

func preparePackageExport(directory string) (string, string, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", "", err
	}

	if err := hostfile.Protect(directory, true); err != nil {
		return "", "", err
	}

	id, err := newOperationID()
	if err != nil {
		return "", "", err
	}

	pending, err := os.MkdirTemp(directory, ".pending-")
	if err != nil {
		return "", "", err
	}

	if err := hostfile.Protect(pending, true); err != nil {
		return "", "", errors.Join(err, os.RemoveAll(pending))
	}

	return pending, filepath.Join(directory, "recovery-"+id), nil
}
