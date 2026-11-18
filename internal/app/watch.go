package app

import (
	"context"

	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/containers/watch"
	"github.com/DevWolk/garminctl/internal/ship/fit"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// WatchAppCount supplies the registry owner's count for the same leased Session.
func WatchAppCount(ctx context.Context, sess *watchsession.Session) (int, error) {
	entries, err := (apps.ReadRegistryAction{}).Run(ctx, sess)
	return len(entries), err
}

// WatchSummary supplies the shell's connection-state view without a feature import.
func WatchSummary(ctx context.Context, sess *watchsession.Session) (any, error) {
	return watch.NewSummaryAction(fit.MustProfile(), WatchAppCount).Run(ctx, sess)
}

// WatchModel names the watch for notifications from the same summary.
func WatchModel(ctx context.Context, sess *watchsession.Session) (string, error) {
	summary, err := watch.NewSummaryAction(fit.MustProfile(), WatchAppCount).Run(ctx, sess)
	if err != nil {
		return "", err
	}

	return summary.Model, nil
}

// refreshWith drops the leased Session's cached files and the app schemas
// cached for that connection.
func refreshWith(cacheFor func(*watchsession.Session) *apps.SchemaCache) func(*watchsession.Session) {
	return func(sess *watchsession.Session) { cacheFor(sess).Reset(sess.Refresh()) }
}
