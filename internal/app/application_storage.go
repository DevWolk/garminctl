package app

import (
	"path/filepath"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/gc"
	"github.com/DevWolk/garminctl/internal/ship/hostfile"
	"github.com/DevWolk/garminctl/internal/ship/paths"
)

// The application feature keeps its data backups beside the account store and
// its retained packages in the cache directory.
const (
	applicationBackups  = "application-backups"
	applicationPackages = "application-packages"
)

// applicationDataRoot returns the config directory that holds the account store
// and the backup root beside it. ok is false when the directory is unavailable.
func applicationDataRoot() (config, backups string, ok bool) {
	account := gc.DefaultStorePath()
	if account == "" {
		return "", "", false
	}

	config = filepath.Dir(account)

	return config, filepath.Join(config, applicationBackups), true
}

// applicationSnapshotSources lists the areas a data backup copies.
func applicationSnapshotSources(config string) []hostfile.SnapshotSource {
	return []hostfile.SnapshotSource{
		{Name: application.StateAreaConfig, Directory: config},
		{Name: application.StateAreaCache, Directory: paths.Cache()},
	}
}

// applicationPackageDirectory retains verified package/signature pairs.
func applicationPackageDirectory() string { return filepath.Join(paths.Cache(), applicationPackages) }
