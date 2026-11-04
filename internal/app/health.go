package app

import (
	"github.com/DevWolk/garminctl/internal/containers/health"
	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// HealthAccess supplies per-operation identity/metadata policy until INV-18.
func HealthAccess(watch *garmin.Lazy, account *gc.Client) func(*watchsession.Session) health.AccountAccess {
	return func(sess *watchsession.Session) health.AccountAccess { return watch.HealthAccessFor(sess, account) }
}
