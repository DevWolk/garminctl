package app

import (
	"github.com/DevWolk/garminctl/internal/containers/health"
	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// HealthAccess supplies caller-scoped Connect identity and watch metadata.
func HealthAccess(watch *garmin.Lazy, account *gc.Client) func(*watchsession.Session) health.AccountAccess {
	cloudFor := ConnectFor(watch, account)

	return func(sess *watchsession.Session) health.AccountAccess {
		cloud := cloudFor(sess)
		return health.AccountAccess{Client: cloud.Client(), DisplayName: cloud.DisplayName, DeviceXML: cloud.DeviceXML, CacheDir: cloud.CacheDirectory()}
	}
}
