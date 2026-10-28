package garmin

import "context"

// BindEpoch observes retirement without holding a lease between job groups.
func (l *Lazy) BindEpoch(ctx context.Context, epoch ConnectionEpoch) (context.Context, func(), error) {
	return l.manager.BindEpoch(ctx, epoch)
}

func (l *Lazy) epochCause(epoch ConnectionEpoch) error { return l.manager.EpochError(epoch) }
