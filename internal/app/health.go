package app

import (
	"github.com/DevWolk/garminctl/internal/app/process"
	"github.com/DevWolk/garminctl/internal/containers/health"
	"github.com/DevWolk/garminctl/internal/ship/gc"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// HealthAccess supplies caller-scoped Connect identity and watch metadata.
func HealthAccess(runtime *process.Runtime, account *gc.Client) func(*watchsession.Session) health.AccountAccess {
	cloudFor := ConnectFor(runtime, account)

	return func(sess *watchsession.Session) health.AccountAccess {
		cloud := cloudFor(sess)
		return health.AccountAccess{Client: cloud.Client(), DisplayName: cloud.DisplayName, DeviceXML: cloud.DeviceXML, CacheDir: cloud.CacheDirectory()}
	}
}
