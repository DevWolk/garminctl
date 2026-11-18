package updates

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/ship/ciqstore"
	"github.com/DevWolk/garminctl/internal/ship/fit"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// InstalledApps reads the apps-owned registry within the caller's watch lease.
type (
	InstalledApps func(context.Context) ([]apps.App, error)
	// CheckSoftwareAction reads Garmin's offered watch updates.
	CheckSoftwareAction struct{ config Config }
)

// NewCheckSoftwareAction supplies the feature's endpoint and client.
func NewCheckSoftwareAction(config Config) *CheckSoftwareAction {
	return &CheckSoftwareAction{config: config}
}

// Run checks firmware/data updates without writing to the watch.
func (a *CheckSoftwareAction) Run(ctx context.Context, sess *watchsession.Session) ([]SoftwareUpdate, error) {
	return checkSoftwareUpdates(ctx, sess, a.config)
}

// InstallSoftwareAction downloads, verifies and stages one offered update.
type InstallSoftwareAction struct {
	config      Config
	invalidated func(uint64)
}

// NewInstallSoftwareAction reports successful invalidation to the caller-owned
// feature cache. The optional callback must not retain the Session or do I/O.
func NewInstallSoftwareAction(config Config, invalidated func(uint64)) *InstallSoftwareAction {
	return &InstallSoftwareAction{config: config, invalidated: invalidated}
}

// Run stages an update using the existing lease and invalidation generation.
func (a *InstallSoftwareAction) Run(ctx context.Context, sess *watchsession.Session, partNumber string) (*InstallResult, error) {
	updates, err := checkSoftwareUpdates(ctx, sess, a.config)
	if err != nil {
		return nil, err
	}

	u, err := findUpdate(updates, partNumber)
	if err != nil {
		return nil, err
	}

	data, err := downloadUpdate(ctx, a.config.Client, u)
	if err != nil {
		return nil, err
	}

	dir := strings.Trim(strings.ReplaceAll(u.InstallPath, "\\", "/"), "/")
	if err := sess.EnsureDir(ctx, dir); err != nil {
		return nil, err
	}

	target := path.Join(dir, u.FileName)
	if err := sess.WriteFile(ctx, target, data); err != nil {
		return nil, fmt.Errorf("write %s: %w", target, err)
	}

	generation := sess.Invalidate(watchsession.PathDeviceXML)
	if a.invalidated != nil {
		a.invalidated(generation)
	}

	restart := u.IsRestartRequired || u.IsPrimaryFirmware

	note := "Written to the watch. It installs the update when the USB session ends: unplug the watch."
	if restart {
		note += " The watch restarts to apply it; keep it charged and do not interrupt the restart."
	}

	return &InstallResult{PartNumber: u.PartNumber, Path: target, Bytes: len(data), Restart: restart, Note: note}, nil
}

// InventoryAction reads installed components from device.fit.
type InventoryAction struct{}

// Run retains software message order and deduplicates identical part/version pairs.
func (InventoryAction) Run(ctx context.Context, sess *watchsession.Session) ([]SoftwarePart, error) {
	b, err := sess.ReadThrough(ctx, watchsession.PathDeviceFIT, true)
	if err != nil {
		return nil, err
	}

	f, err := fit.Decode(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", watchsession.PathDeviceFIT, err)
	}

	seen := map[string]bool{}

	var out []SoftwarePart

	for _, m := range f.MessagesByNum(35) { // software
		pn, _ := m.Value(5).(string)

		ver := m.Value(3)
		if pn == "" || ver == nil {
			continue
		}

		fv, _ := ver.(uint64)

		key := fmt.Sprintf("%s@%d", pn, fv)
		if seen[key] {
			continue
		}

		seen[key] = true

		out = append(out, SoftwarePart{PartNumber: pn, Version: float64(fv) / 100})
	}

	return out, nil
}

// CheckAppsAction compares the installed registry against public store metadata.
type CheckAppsAction struct{ installed InstalledApps }

// NewCheckAppsAction supplies a registry reader for this operation scope.
func NewCheckAppsAction(installed InstalledApps) *CheckAppsAction {
	return &CheckAppsAction{installed: installed}
}

// Run preserves per-app errors and case-sensitive registry deduplication.
func (a *CheckAppsAction) Run(ctx context.Context) ([]AppUpdate, error) {
	apps, err := a.installed(ctx)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}

	var out []AppUpdate

	for _, a := range apps {
		if a.FileName == "" || a.ID == "" || seen[a.ID] {
			continue
		}

		seen[a.ID] = true
		au := AppUpdate{App: a, InstalledVersion: a.Version, StoreURL: "https://apps.garmin.com/apps/" + a.ID}

		info, err := ciqstore.AppInfo(ctx, a.ID)
		if err != nil {
			au.Error = err.Error()
		} else {
			au.LatestVersion, au.LatestName = info.LatestInternalVersion, info.LatestExternalVersion
			au.UpdateAvailable = info.LatestInternalVersion > a.Version
		}

		out = append(out, au)
	}

	return out, nil
}
