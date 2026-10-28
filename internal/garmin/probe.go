package garmin

import (
	"context"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// ProbeObservation distinguishes fresh evidence from an explicitly skipped check.
type ProbeObservation = watchsession.ProbeObservation

// Probe outcomes preserve the existing watcher contract.
const (
	ProbeSkipped      = watchsession.ProbeSkipped
	ProbeConnected    = watchsession.ProbeConnected
	ProbeDisconnected = watchsession.ProbeDisconnected
)

// ProbeIfIdle observes an existing idle connection without waiting or reopening.
func (l *Lazy) ProbeIfIdle(ctx context.Context, quiet time.Duration) (ProbeObservation, error) {
	return l.manager.ProbeIfIdle(ctx, quiet)
}
