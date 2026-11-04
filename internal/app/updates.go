package app

import (
	"context"

	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/containers/updates"
	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// WatchUpdateConfig supplies the default watch endpoint and compatibility client.
// The mutable client injection point is removed with the legacy facade.
func WatchUpdateConfig() updates.Config {
	return updates.Config{URL: updates.DefaultURL, Client: garmin.HTTPClient}
}

// UpdatesRegistry supplies app entries for the current operation scope.
func UpdatesRegistry(sess *watchsession.Session) updates.InstalledApps {
	return func(ctx context.Context) ([]apps.App, error) { return (apps.ReadRegistryAction{}).Run(ctx, sess) }
}

func updateInvalidator(runtime *garmin.Runtime) func(*watchsession.Session) func(uint64) {
	return func(sess *watchsession.Session) func(uint64) { return runtime.Watch.AppSchemaCacheFor(sess).Reset }
}
