package updatetransport

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

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
	} // #nosec G703 -- dedicated local composition path, not a request or manifest path.

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

	if err := os.Rename(pending, filepath.Join(c.directory, manifest.ID)); err != nil {
		return nil, err
	} // #nosec G703 -- generated pending child and validated hex identity in one private directory.

	entry := manifest.CachedPackage

	return &entry, nil
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
func (c *PackageCache) List(ctx context.Context) ([]CachedPackage, error) {
	if err := c.check(ctx); err != nil {
		return nil, err
	}

	if err := hostfile.Protect(c.directory, true); errors.Is(err, os.ErrNotExist) {
		return []CachedPackage{}, nil
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

	result := make([]CachedPackage, 0)

	for _, entry := range entries {
		if !validPackageID(entry.Name()) {
			continue
		}

		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, errors.New("invalid package catalog entry type")
		}

		manifest, err := c.readManifest(ctx, entry.Name())
		if err != nil {
			return nil, err
		}

		result = append(result, manifest.CachedPackage)
	}

	slices.SortFunc(result, func(a, b CachedPackage) int { return strings.Compare(a.ID, b.ID) })

	return result, nil
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
	if err := ctx.Err(); err != nil {
		return err
	}

	if c == nil || c.directory == "" || c.stager == nil {
		return errors.New("package cache is not configured")
	}

	return nil
}
