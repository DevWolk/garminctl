package garmin

import (
	"context"

	"github.com/DevWolk/garminctl/internal/containers/health"
	"github.com/DevWolk/garminctl/internal/fit"
)

// ActivityCoordinator preserves the runtime's health-owned job owner.
type ActivityCoordinator = health.ActivityCoordinator

// WatchAcquireError preserves initial watch failure classification.
type WatchAcquireError = health.WatchAcquireError

// NewActivityCoordinator delegates over the same Manager and FIT profile.
func NewActivityCoordinator(parent context.Context, watch *Lazy) *ActivityCoordinator {
	return health.NewActivityCoordinator(parent, watch.Manager(), fit.MustProfile())
}
