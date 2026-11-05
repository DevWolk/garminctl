package app

import (
	"context"

	"github.com/DevWolk/garminctl/internal/app/process"
	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/containers/updates"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// WatchUpdateConfig supplies the default watch endpoint and the updates client.
func WatchUpdateConfig() updates.Config {
	return updates.Config{URL: updates.DefaultURL, Client: updates.HTTPClient}
}

// SoftwareUpdateNames names the offered watch updates as "product version".
func SoftwareUpdateNames(ctx context.Context, sess *watchsession.Session) ([]string, error) {
	offered, err := updates.NewCheckSoftwareAction(WatchUpdateConfig()).Run(ctx, sess)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(offered))
	for _, u := range offered {
		names = append(names, u.ProductName+" "+u.Version)
	}

	return names, nil
}

// UpdatesRegistry supplies app entries for the current operation scope.
func UpdatesRegistry(sess *watchsession.Session) updates.InstalledApps {
	return func(ctx context.Context) ([]apps.App, error) { return (apps.ReadRegistryAction{}).Run(ctx, sess) }
}

func updateInvalidator(runtime *process.Runtime) func(*watchsession.Session) func(uint64) {
	return func(sess *watchsession.Session) func(uint64) { return runtime.AppSchemaCacheFor(sess).Reset }
}
