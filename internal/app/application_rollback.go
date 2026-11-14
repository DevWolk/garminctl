package app

import (
	"errors"

	"github.com/DevWolk/garminctl/internal/containers/application"
)

// rollbackAction binds internal rollback to the existing owned reader and backend.
// Native receives only its opaque preparation; no HTTP/MCP/CLI endpoint exposes it.
func (b *ApplicationBackend) rollbackAction(reader *applicationNativeReader, address string, owned bool) (*application.RollbackAction, error) {
	if reader == nil || reader.preflight == nil || reader.snapshot == nil || reader.handoff == nil {
		return nil, errors.New("owned application rollback is not configured")
	}

	restore, err := b.restoreDataAction(address, owned)
	if err != nil {
		return nil, err
	}

	return application.NewCompatibleRollbackAction(reader.preflight, reader.snapshot, restore, reader.handoff, applicationDataCompatibility{backend: b, address: address, owned: owned}), nil
}
