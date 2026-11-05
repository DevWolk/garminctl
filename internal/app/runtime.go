// Package app assembles process owners and transport shells. Feature behavior
// belongs to the feature packages; shells receive their dependencies from here.
package app

import (
	"context"

	"github.com/DevWolk/garminctl/internal/app/process"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// NewRuntime creates one process's watch and job owners.
func NewRuntime(ctx context.Context, open watchsession.Opener) *process.Runtime {
	return process.New(ctx, open)
}
