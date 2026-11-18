package apps

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// InstallPRGAction stages a binary using the current Session's feature cache.
type InstallPRGAction struct{ cache *SchemaCache }

// NewInstallPRGAction borrows the same cache as settings/schema Actions.
func NewInstallPRGAction(cache *SchemaCache) *InstallPRGAction {
	return &InstallPRGAction{cache: cache}
}

// RemoveAction removes an app's files without editing the registry image.
type RemoveAction struct{ cache *SchemaCache }

// NewRemoveAction borrows the current Session's shared feature cache.
func NewRemoveAction(cache *SchemaCache) *RemoveAction { return &RemoveAction{cache: cache} }

// WriteFileAction delivers a named app binary or store queue message.
type WriteFileAction struct{}

// Run preserves visible-folder then optional hidden-filesystem delivery.
func (WriteFileAction) Run(ctx context.Context, sess *watchsession.Session, name string, data []byte) (string, error) {
	return writeAppFile(ctx, sess, name, data)
}

// FilesAction lists visible or hidden app files.
type FilesAction struct{}

// Run writes a Connect IQ binary to the app folder.
// The watch registers it after the USB session ends.
func (a *InstallPRGAction) Run(ctx context.Context, sess *watchsession.Session, data []byte) (string, error) {
	if len(data) < 16 || data[0] != 0 || data[1] != 0 || data[2] != 0 {
		return "", errors.New("this does not look like a Connect IQ .PRG file")
	}

	var b [4]byte

	_, _ = rand.Read(b[:])
	name := strings.ToUpper(hex.EncodeToString(b[:])) + ".PRG"

	p, err := writeAppFile(ctx, sess, name, data)
	if err != nil {
		return "", err
	}

	a.cache.Reset(sess.Invalidate(PathAppsConfig))

	return p, nil
}

// AppFolder stores Connect IQ binaries and may be hidden from ordinary MTP
// listings; see WriteFileAction.
const AppFolder = "GARMIN/APPS/MEDIA"

// WriteAppFile puts a Connect IQ binary (or STM store message) into the app
// folder and returns the watch path. When the folder is hidden from the
// ordinary listing it goes through the device's raw PTP path; the watch
// registers the app when the USB session ends.
func writeAppFile(ctx context.Context, sess *watchsession.Session, name string, data []byte) (string, error) {
	target := AppFolder + "/" + name
	if _, err := sess.Stat(ctx, AppFolder); err == nil {
		if err := sess.WriteFile(ctx, target, data); err != nil {
			return "", fmt.Errorf("write %s: %w", target, err)
		}

		return target, nil
	}

	if err := sess.WriteHidden(ctx, AppFolder, name, data); errors.Is(err, watchsession.ErrCapabilityUnavailable) {
		return "", errors.New("this watch does not expose its app folder (GARMIN/APPS/MEDIA) over USB, so apps can only be installed through Garmin Connect on the phone")
	} else if err != nil {
		return "", err
	}

	return target, nil
}

// Run deletes a Connect IQ app's binary, settings and data files. The
// watch drops the app from its registry when the USB session ends. ref is the
// app's file id, name or store id; built-in apps and the Connect IQ Store app
// are refused.
func (a *RemoveAction) Run(ctx context.Context, sess *watchsession.Session, ref string) ([]string, error) {
	apps, err := readRegistry(ctx, sess)
	if err != nil {
		return nil, err
	}

	ref = strings.TrimSuffix(strings.TrimSuffix(ref, ".PRG"), ".prg")

	app := installedApp(apps, ref)
	if app == nil {
		return nil, fmt.Errorf("%q is not an installed Connect IQ app", ref)
	}

	if app.Native {
		return nil, fmt.Errorf("%s is built into the watch and cannot be removed", app.Name)
	}

	if strings.EqualFold(app.FileName, "Toystore") {
		return nil, errors.New("the Connect IQ Store app cannot be removed")
	}

	var removed []string

	binary, err := removeAppBinary(ctx, sess, app.FileName+".PRG")
	if err != nil {
		return removed, err
	}

	if binary != "" {
		removed = append(removed, binary)
	}

	removed, err = removeAppSidecars(ctx, sess, app.FileName, removed)
	if err != nil {
		return removed, err
	}

	a.cache.Reset(sess.Invalidate(PathAppsConfig))

	return removed, nil
}

// installedApp finds a store app by file id, name or store id.
func installedApp(apps []App, ref string) *App {
	for i := range apps {
		a := &apps[i]
		if a.FileName != "" && matchesApp(*a, ref) {
			return a
		}
	}

	return nil
}

// removeAppBinary deletes the .PRG from the visible or hidden app folder and
// returns the deleted path, or "" when it was not there.
func removeAppBinary(ctx context.Context, sess *watchsession.Session, prg string) (string, error) {
	target := AppFolder + "/" + prg

	if _, err := sess.Stat(ctx, target); err == nil {
		if err := sess.DeleteFile(ctx, target); err != nil {
			return "", err
		}

		return target, nil
	}

	switch err := sess.DeleteHidden(ctx, AppFolder, prg); {
	case errors.Is(err, watchsession.ErrCapabilityUnavailable):
		return "", errors.New("this watch does not expose its app folder over USB from here")
	case err == nil:
		return target, nil
	case errors.Is(err, device.ErrNotFound):
		return "", nil
	default:
		return "", err
	}
}

// removeAppSidecars deletes the settings and data files of an app.
func removeAppSidecars(ctx context.Context, sess *watchsession.Session, fileName string, removed []string) ([]string, error) {
	for _, p := range []string{"GARMIN/Apps/SETTINGS/" + fileName + ".SET", "GARMIN/Apps/DATA/" + fileName + ".DAT", "GARMIN/Apps/DATA/" + fileName + ".IDX", "GARMIN/Apps/DATA/" + fileName + ".IMT"} {
		if _, err := sess.Stat(ctx, p); err != nil {
			continue
		}

		if err := sess.DeleteFile(ctx, p); err != nil {
			return removed, err
		}

		removed = append(removed, p)
	}

	return removed, nil
}

// Run lists the app folder, hidden or not.
func (FilesAction) Run(ctx context.Context, sess *watchsession.Session) ([]device.Entry, error) {
	if entries, err := sess.List(ctx, AppFolder); err == nil {
		return entries, nil
	}

	entries, err := sess.ListHidden(ctx, AppFolder)
	if errors.Is(err, watchsession.ErrCapabilityUnavailable) {
		return nil, device.ErrNotFound
	}

	return entries, err
}
