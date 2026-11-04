package garmin

import (
	"context"

	"github.com/DevWolk/garminctl/internal/containers/connect"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

// ErrSyncBusy preserves the shared coordinator error identity.
var ErrSyncBusy = connect.ErrSyncBusy

// SyncOptions preserves compatible request/epoch matching.
type SyncOptions = connect.SyncOptions

// SyncRunner preserves runtime-owned runner signatures.
type SyncRunner = connect.SyncRunner

// SyncSnapshot preserves progress snapshot access.
type SyncSnapshot = connect.SyncSnapshot

// SyncJob preserves stable waiter handles.
type SyncJob = connect.SyncJob

// SyncCoordinator preserves one process's Connect-owned coordinator.
type SyncCoordinator = connect.SyncCoordinator

// NewSyncCoordinator delegates construction over the same admission gate.
func NewSyncCoordinator(parent context.Context, gate *lifecycle.Gate) *SyncCoordinator {
	return connect.NewSyncCoordinator(parent, gate)
}
