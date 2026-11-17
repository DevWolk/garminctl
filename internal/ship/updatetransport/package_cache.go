package updatetransport

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/DevWolk/garminctl/internal/hostfile"
)

// CachedPackage is unverified catalog metadata, never installation authority.
type CachedPackage struct {
	ID      string          `json:"id"`
	Target  MatchRequest    `json:"target"`
	Release ResolvedRelease `json:"release"`
	Digest  string          `json:"digest"`
}

// PackageCache retains packages separately from temporary verified operations.
type PackageCache struct {
	directory string
	stager    *Stager
}

// UnusablePackage names a catalog entry this build cannot read. The entry stays
// on disk until Remove deletes it or a verified copy of the same package replaces it.
type UnusablePackage struct{ ID, Reason string }

// NewPackageCatalog reads and removes retained metadata without a verifier, for
// builds that carry no compiled key. Put, Open and Export stay unavailable.
func NewPackageCatalog(directory string) (*PackageCache, error) {
	if directory == "" {
		return nil, errors.New("package catalog requires a dedicated directory")
	}

	path, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}

	return &PackageCache{directory: path}, nil
}

// NewPackageCache binds local storage to the current compiled CLI verifier.
func NewPackageCache(directory string, stager *Stager) (*PackageCache, error) {
	if directory == "" || stager == nil || stager.key == "" {
		return nil, errors.New("package cache requires a dedicated directory and compiled CLI verifier")
	}

	path, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}

	if packagePathsOverlap(path, stager.cacheDir) {
		return nil, errors.New("package retention and temporary staging directories must be disjoint")
	}

	return &PackageCache{directory: path, stager: stager}, nil
}

func packagePathsOverlap(a, b string) bool {
	first, err := filepath.Rel(a, b)
	if err == nil && filepath.IsLocal(first) {
		return true
	}

	second, err := filepath.Rel(b, a)

	return err == nil && filepath.IsLocal(second)
}

// Put copies an owned staged package; the caller still closes the input.
func (c *PackageCache) Put(ctx context.Context, target MatchRequest, staged *StagedPackage) (result *CachedPackage, err error) {
	if err := c.check(ctx); err != nil {
		return nil, err
	}

	if staged == nil {
		return nil, errors.New("verified staging result is required")
	}

	release := staged.Release()
	if err := validateResolved(target, release); err != nil {
		return nil, err
	}

	if err := verifyStagedFiles(ctx, staged.directory, release, staged.digest); err != nil {
		return nil, err
	}

	manifest, err := newPackageManifest(target, release, staged.digest, c.stager.key)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(c.directory, 0o700); err != nil {
		return nil, err
	}

	if err := hostfile.Protect(c.directory, true); err != nil {
		return nil, err
	}

	pending, err := os.MkdirTemp(c.directory, ".pending-")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(pending)) }()

	if err := hostfile.Protect(pending, true); err != nil {
		return nil, err
	}

	if err := copyPackagePair(ctx, staged.directory, pending, release); err != nil {
		return nil, err
	}

	if err := writePackageManifest(pending, manifest); err != nil {
		return nil, err
	}

	if err := c.verifyPending(ctx, pending, manifest); err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := publishPackageEntry(c.directory, pending, manifest.ID); err != nil {
		return nil, err
	}

	entry := manifest.CachedPackage

	return &entry, nil
}

// publishPackageEntry replaces an entry of the same identity. The pending copy
// has just been verified, so whatever it replaces is either a damaged copy of
// the same bytes or the same bytes saved under earlier release metadata.
func publishPackageEntry(directory, pending, id string) (err error) {
	final := filepath.Join(directory, id)

	if _, err := os.Lstat(final); errors.Is(err, os.ErrNotExist) {
		return os.Rename(pending, final)
	} else if err != nil {
		return err
	}

	retired, err := os.MkdirTemp(directory, ".pending-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(retired)) }()

	previous := filepath.Join(retired, id)
	if err := os.Rename(final, previous); err != nil {
		return err
	}

	if err := os.Rename(pending, final); err != nil {
		return errors.Join(err, os.Rename(previous, final))
	}

	return nil
}

func (c *PackageCache) verifyPending(ctx context.Context, directory string, manifest packageManifest) error {
	verified, err := c.stager.verifyRetained(ctx, manifest.Target, manifest.Release, directory, nil)
	if err != nil {
		return err
	}

	matched := verified.Digest() == manifest.Digest

	err = verified.Close()
	if !matched {
		return errors.Join(err, errors.New("retained package differs from its staging digest"))
	}

	return err
}

// List returns bounded metadata without granting verified-package authority.
// Entries this build cannot read are left out; Catalog names them.
func (c *PackageCache) List(ctx context.Context) ([]CachedPackage, error) {
	result, _, err := c.Catalog(ctx)

	return result, err
}

// Catalog returns the same metadata as List and the entries it had to skip, so
// one unreadable directory never hides the packages beside it.
func (c *PackageCache) Catalog(ctx context.Context) ([]CachedPackage, []UnusablePackage, error) {
	if err := c.checkCatalog(ctx); err != nil {
		return nil, nil, err
	}

	names, err := c.entryNames()
	if err != nil {
		return nil, nil, err
	}

	result, skipped := make([]CachedPackage, 0), make([]UnusablePackage, 0)

	for _, name := range names {
		manifest, err := c.readManifest(ctx, name)
		if canceled := ctx.Err(); canceled != nil {
			return nil, nil, canceled
		}

		if err != nil {
			skipped = append(skipped, UnusablePackage{ID: name, Reason: err.Error()})

			continue
		}

		result = append(result, manifest.CachedPackage)
	}

	return result, skipped, nil
}

// entryNames lists catalog identities in order; working directories and
// foreign names are not entries.
func (c *PackageCache) entryNames() ([]string, error) {
	if err := hostfile.Protect(c.directory, true); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	root, err := os.OpenRoot(c.directory)
	if err != nil {
		return nil, err
	}

	defer root.Close()

	file, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	entries, err := file.ReadDir(1001)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	if len(entries) > 1000 {
		return nil, errors.New("package catalog exceeds the directory-entry limit")
	}

	names := make([]string, 0, len(entries))

	for _, entry := range entries {
		if validPackageID(entry.Name()) {
			names = append(names, entry.Name())
		}
	}

	slices.Sort(names)

	return names, nil
}

// Open re-verifies offline and returns an independently owned temporary copy.
func (c *PackageCache) Open(ctx context.Context, id string, progress func(StageProgress)) (*StagedPackage, error) {
	if err := c.check(ctx); err != nil {
		return nil, err
	}

	manifest, err := c.readManifest(ctx, id)
	if err != nil {
		return nil, err
	}

	verified, err := c.stager.verifyRetained(ctx, manifest.Target, manifest.Release, filepath.Join(c.directory, id), progress)
	if err != nil {
		return nil, err
	}

	if verified.Digest() != manifest.Digest {
		return nil, errors.Join(verified.Close(), errors.New("verified package digest does not match its catalog entry"))
	}

	return verified, nil
}

func (c *PackageCache) check(ctx context.Context) error {
	if err := c.checkCatalog(ctx); err != nil {
		return err
	}

	if c.stager == nil {
		return errors.New("package cache has no compiled CLI verifier")
	}

	return nil
}

func (c *PackageCache) checkCatalog(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if c == nil || c.directory == "" {
		return errors.New("package cache is not configured")
	}

	return nil
}
