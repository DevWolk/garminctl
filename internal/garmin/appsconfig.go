package garmin

import "github.com/DevWolk/garminctl/internal/containers/apps"

// App preserves the installed-app model while callers migrate.
type App = apps.App

// ParseAppsConfig delegates registry decoding to its domain owner.
func ParseAppsConfig(b []byte) ([]App, error) { return apps.ParseAppsConfig(b) }
