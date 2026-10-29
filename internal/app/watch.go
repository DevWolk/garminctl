package app

import (
	"context"

	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// WatchAppCount adapts the legacy registry owner to watch's narrow count port.
// The facade borrows the supplied Session without opening or owning a device.
func WatchAppCount(ctx context.Context, sess *watchsession.Session) (int, error) {
	apps, err := garmin.NewFromSession(sess).Apps(ctx)
	return len(apps), err
}
