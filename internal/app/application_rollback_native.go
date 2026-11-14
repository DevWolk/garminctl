package app

import (
	"context"
	"errors"

	"github.com/DevWolk/garminctl/internal/containers/application"
	applicationnative "github.com/DevWolk/garminctl/internal/containers/application/ui/native"
)

type applicationRollbackPreparation struct {
	action   *application.RollbackAction
	prepared *application.PreparedRollback
}

func (p *applicationRollbackPreparation) View() application.RollbackView {
	return p.prepared.View()
}

func (p *applicationRollbackPreparation) Close() error {
	return p.prepared.Close()
}

func (p *applicationRollbackPreparation) Run(ctx context.Context, consent application.RollbackConsent) (*application.RollbackResult, error) {
	return p.action.Run(ctx, p.prepared, consent)
}

func (r *applicationNativeReader) rollbackSource() (*application.RollbackAction, error) {
	if r.rollbackFor == nil {
		return nil, errors.New("owned rollback is unavailable in this build")
	}

	return r.rollbackFor()
}

// RetainedRollbacks lists local pairs without acquiring maintenance or a watch.
func (r *applicationNativeReader) RetainedRollbacks(ctx context.Context, prerelease bool) ([]application.RollbackOption, error) {
	action, err := r.rollbackSource()
	if err != nil {
		return nil, err
	}

	return action.Options(ctx, prerelease)
}

// PrepareRollback returns a native-owned capability, never paths or an installer command.
func (r *applicationNativeReader) PrepareRollback(ctx context.Context, request application.RollbackRequest) (applicationnative.RollbackPreparation, error) {
	action, err := r.rollbackSource()
	if err != nil {
		return nil, err
	}

	prepared, err := action.Prepare(ctx, request)
	if err != nil {
		return nil, err
	}

	return &applicationRollbackPreparation{action: action, prepared: prepared}, nil
}
