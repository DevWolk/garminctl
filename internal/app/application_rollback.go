package app

import (
	"errors"

	"github.com/DevWolk/garminctl/internal/containers/application"
)

// rollbackAction binds internal rollback to the existing owned reader and backend.
// It is not registered on the native Service or any HTTP/MCP/CLI endpoint.
func (b *ApplicationBackend) rollbackAction(reader *applicationNativeReader, address string, owned bool) (*application.RollbackAction, error) {
	if reader == nil || reader.preflight == nil || reader.snapshot == nil || reader.handoff == nil {
		return nil, errors.New("owned application rollback is not configured")
	}

	restore, err := b.restoreDataAction(address, owned)
	if err != nil {
		return nil, err
	}

	return application.NewRollbackAction(reader.preflight, reader.snapshot, restore, reader.handoff), nil
}
