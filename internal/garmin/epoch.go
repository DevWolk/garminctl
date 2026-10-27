package garmin

import (
	"context"
	"errors"
	"sync"

	"github.com/DevWolk/garminctl/internal/device"
)

// BindEpoch cancels a job when its original connection is retired. It performs
// no device I/O and does not hold an operation lease between files/network calls.
// The caller owns job admission and must call release to join the binding worker.
func (l *Lazy) BindEpoch(ctx context.Context, epoch ConnectionEpoch) (bound context.Context, release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	l.mu.Lock()
	if l.closed || l.svc == nil || l.epoch != epoch {
		l.mu.Unlock()
		return nil, nil, errors.Join(ErrSessionChanged, device.ErrDisconnected)
	}

	epochDone := l.epochDone
	l.mu.Unlock()

	bound, cancel := context.WithCancelCause(ctx)

	joined := make(chan struct{})
	go func() {
		defer close(joined)

		select {
		case <-epochDone:
			cancel(errors.Join(ErrSessionChanged, device.ErrDisconnected))
		case <-bound.Done():
		}
	}()

	var once sync.Once

	release = func() { once.Do(func() { cancel(nil); <-joined }) }

	return bound, release, nil
}
