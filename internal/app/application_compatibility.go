package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/containers/connect"
	"github.com/DevWolk/garminctl/internal/ship/gc"
	"github.com/DevWolk/garminctl/internal/ship/hostfile"
)

type applicationDataCompatibility struct {
	backend *ApplicationBackend
	address string
	owned   bool
}

func (s applicationDataCompatibility) Verify(ctx context.Context, target, safety application.RecoverySnapshot, stopped bool) error {
	if !application.KeepsCurrentData(target, safety) {
		return errors.New("data format compatibility is unknown; choose explicit backup restoration")
	}

	if stopped {
		if err := s.backend.joinedFor(s.address, s.owned)(ctx); err != nil {
			return err
		}
	} else {
		bound, cancel, err := s.backend.snapshotContextFor(s.address, s.owned)(ctx)
		if err != nil {
			return err
		}
		defer cancel()

		ctx = bound
	}

	config, directory, ok := applicationDataRoot()
	if !ok {
		return errors.New("current data directory is unavailable")
	}

	reader := hostfile.NewSnapshotReader(directory)
	store := applicationDataRestorations{reader: reader}

	verified, err := store.verifiedSnapshot(ctx, safety)
	if err != nil {
		return err
	}

	inspector := hostfile.NewSnapshotRestorer(directory, applicationSnapshotSources(config), applicationSnapshotPath)

	return inspector.InspectCurrent(ctx, verified, inspectApplicationData)
}

func inspectApplicationData(file hostfile.SnapshotFileInfo, reader io.Reader) error {
	var err error
	if file.Area == application.StateAreaConfig {
		err = gc.ValidateSavedState(reader, file.Path == "garmin-connect.json.mfa")
	} else if application.StatePath(file.Area, file.Path) == application.StateFile && filepath.Ext(file.Path) == ".json" {
		err = connect.ValidateSavedLedger(reader)
	}
	// Watch recovery files are opaque retained bytes, never decoded or rewritten
	// by startup. Their exact hashes are checked by InspectCurrent.
	if err != nil {
		return fmt.Errorf("current data cannot be read under the retained format contract: %s/%s: %w", file.Area, file.Path, err)
	}

	return nil
}
