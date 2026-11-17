package updatetransport

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// operationLifetime is how long a private working directory may go unchanged
// before it counts as the leftover of a finished or interrupted operation.
// Staging, verification and installation are each bounded to minutes.
const operationLifetime = 24 * time.Hour

// ErrPackageNotRetained reports a removal request for an identity the catalog does not hold.
var ErrPackageNotRetained = errors.New("the selected package is not retained")

// Remove deletes one catalog entry by identity, readable or not. It needs no
// verifier, so it also works on entries saved under another compiled key.
func (c *PackageCache) Remove(ctx context.Context, id string) error {
	if err := c.checkCatalog(ctx); err != nil {
		return err
	}

	if !validPackageID(id) {
		return errors.New("invalid package catalog identity")
	}

	root, err := os.OpenRoot(c.directory)
	if errors.Is(err, os.ErrNotExist) {
		return ErrPackageNotRetained
	}

	if err != nil {
		return err
	}

	defer root.Close()

	if _, err := root.Lstat(id); errors.Is(err, os.ErrNotExist) {
		return ErrPackageNotRetained
	} else if err != nil {
		return err
	}

	return root.RemoveAll(id)
}

// Prune keeps the most recently saved packages, up to keep, and always the
// package of the running build. Of several entries for one release and
// installation kind only the latest stays, so a republished release cannot
// leave two candidates. Entries this build cannot read are left for Remove.
// Working directories abandoned by interrupted operations are cleared as well.
func (c *PackageCache) Prune(ctx context.Context, keep int) error {
	if err := c.check(ctx); err != nil {
		return err
	}

	if keep < 1 {
		return errors.New("package retention keeps at least one package")
	}

	entries, _, err := c.Catalog(ctx)
	if err != nil {
		return err
	}

	saved := make(map[string]time.Time, len(entries))

	for _, entry := range entries {
		info, err := os.Lstat(filepath.Join(c.directory, entry.ID))
		if err != nil {
			return err
		}

		saved[entry.ID] = info.ModTime()
	}

	slices.SortFunc(entries, func(a, b CachedPackage) int {
		return cmp.Or(saved[b.ID].Compare(saved[a.ID]), strings.Compare(a.ID, b.ID))
	})

	var (
		failed error
		kept   int
	)

	latest := make(map[MatchRequest]bool, len(entries))

	for _, entry := range entries {
		running := strings.TrimPrefix(entry.Target.ReleaseTag, "v") == strings.TrimPrefix(c.stager.version, "v")

		if !latest[entry.Target] && (kept < keep || running) {
			latest[entry.Target] = true

			if kept < keep {
				kept++
			}

			continue
		}

		failed = errors.Join(failed, c.Remove(ctx, entry.ID))
	}

	return errors.Join(failed, removeStale(c.directory, ".pending-"), removeStale(c.stager.cacheDir, "stage-"))
}

// removeStaleInstallations clears installer operations that nothing has
// changed for operationLifetime, except the one holding the latest recorded
// result, which LastInstallResult keeps reporting until a newer one exists.
func removeStaleInstallations(cache string) error {
	latest, err := LastInstallResult(cache)
	if latest == nil {
		return errors.Join(err, removeStale(cache, "stage-"))
	}

	return removeStale(cache, "stage-", filepath.Base(latest.Directory))
}

// removeStale deletes the working directories named prefix* that nothing has
// changed for operationLifetime, sparing the named ones. It continues past a
// directory it cannot remove.
func removeStale(directory, prefix string, spare ...string) error {
	root, err := os.OpenRoot(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	defer root.Close()

	file, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	entries, err := file.ReadDir(-1)
	if err != nil {
		return err
	}

	var failed error

	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) || slices.Contains(spare, entry.Name()) {
			continue
		}

		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			failed = errors.Join(failed, err)

			continue
		}

		if time.Since(info.ModTime()) >= operationLifetime {
			failed = errors.Join(failed, root.RemoveAll(entry.Name()))
		}
	}

	return failed
}
