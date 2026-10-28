// Package app assembles process owners and transport shells. Feature behavior
// belongs to the feature packages; shells receive their dependencies from here.
package app

import (
	"context"

	"github.com/DevWolk/garminctl/internal/garmin"
)

// NewRuntime creates one process's watch and job owners. Runtime temporarily
// remains a legacy type until watchsession and connect have been extracted.
func NewRuntime(ctx context.Context, open garmin.Opener) *garmin.Runtime {
	return garmin.NewRuntime(ctx, open)
}
