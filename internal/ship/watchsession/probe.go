package watchsession

import (
	"context"
	"errors"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/device"
)

// ProbeObservation is a fresh transport observation or an explicit skipped
// check. Skipped never turns an old connection flag into new evidence.
type ProbeObservation uint8

// Probe outcomes distinguish an answer, no usable session and no observation.
const (
	ProbeSkipped ProbeObservation = iota
	ProbeConnected
	ProbeDisconnected
)

// ProbeIfIdle never waits for an operation lease or opens a new connection.
// quiet suppresses checks soon after an operation group finished. Callers retain
// their previous observation when the outcome is ProbeSkipped, including errors
// that do not prove transport disconnection.
func (l *Manager) ProbeIfIdle(ctx context.Context, quiet time.Duration) (ProbeObservation, error) {
	if err := ctx.Err(); err != nil {
		return ProbeSkipped, err
	}

	l.mu.Lock()
	session, epoch, closed, lastUse := l.session, l.epoch, l.closed, l.lastUse
	l.mu.Unlock()

	if closed || session == nil {
		return ProbeDisconnected, nil
	}

	if l.gate.Snapshot().Operations > 0 {
		return ProbeSkipped, nil
	}

	if quiet > 0 && time.Since(lastUse) < quiet {
		return ProbeSkipped, nil
	}

	release, err := l.gate.Enter(ctx)
	if err != nil {
		return ProbeSkipped, err
	}

	select {
	case l.slot <- struct{}{}:
	default:
		release()
		return ProbeSkipped, nil
	}

	lease, err := l.acquireReserved(ctx, epoch, release)
	if err != nil {
		if errors.Is(err, ErrSessionChanged) && l.Current() == nil {
			return ProbeDisconnected, err
		}

		return ProbeSkipped, err
	}

	return l.observeProbe(lease)
}

func (l *Manager) observeProbe(lease *Lease) (ProbeObservation, error) {
	_, err := lease.Session().Stat(lease.Context(), "GARMIN")

	observation := ProbeConnected
	if err != nil {
		observation = ProbeSkipped
		if errors.Is(err, device.ErrDisconnected) {
			observation = ProbeDisconnected
			err = errors.Join(err, l.DropAt(lease.Epoch()))
		}
	}

	err = errors.Join(err, lease.Release())
	// A concurrent Drop or replacement can retire the probed epoch after Stat.
	// Never report its successful answer as fresh evidence for another session.
	l.mu.Lock()
	current, epoch, closed := l.session, l.epoch, l.closed
	l.mu.Unlock()

	if current != nil && !closed && epoch != lease.Epoch() {
		return ProbeSkipped, err
	}

	if current == nil || closed {
		return ProbeDisconnected, err
	}

	return observation, err
}
