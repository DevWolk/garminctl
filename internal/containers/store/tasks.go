package store

import (
	"context"
	"strings"
	"sync"

	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/ship/ciqstore"
)

var (
	deviceTypeMu    sync.Mutex
	deviceTypeCache = map[string]string{}
)

func storeDeviceType(ctx context.Context, part string) string {
	if part == "" {
		return ""
	}

	deviceTypeMu.Lock()
	id, ok := deviceTypeCache[part]
	deviceTypeMu.Unlock()

	if ok {
		return id
	}

	id, _ = ciqstore.DeviceTypeID(ctx, part)

	deviceTypeMu.Lock()
	deviceTypeCache[part] = id
	deviceTypeMu.Unlock()

	return id
}

// installedMap returns installed store apps by id (lower-case).
func installedMap(ctx context.Context, read InstalledApps) map[string]apps.App {
	m := map[string]apps.App{}
	if read == nil {
		return m
	}

	apps, err := read(ctx)
	if err != nil {
		return m
	}

	for _, a := range apps {
		if a.FileName != "" {
			m[strings.ToLower(a.ID)] = a
		}
	}

	return m
}

func decorateStore(ctx context.Context, read InstalledApps, apps []ciqstore.StoreApp) []App {
	inst := installedMap(ctx, read)

	out := make([]App, 0, len(apps))
	for _, a := range apps {
		v := App{StoreApp: a}
		if ia, ok := inst[strings.ToLower(a.ID)]; ok {
			v.Installed, v.InstalledVersion = true, ia.Version
			v.UpdateAvailable = a.InternalVersion > ia.Version
		}

		out = append(out, v)
	}

	return out
}
