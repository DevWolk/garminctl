package store

import (
	"context"
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

	id, err := ciqstore.DeviceTypeID(ctx, part)
	if err != nil {
		// A failed lookup is asked again next time; only answers are cached.
		return ""
	}

	deviceTypeMu.Lock()
	deviceTypeCache[part] = id
	deviceTypeMu.Unlock()

	return id
}

// installedMap returns installed store apps by apps.IDKey.
func installedMap(ctx context.Context, read InstalledApps) map[string]apps.App {
	m := map[string]apps.App{}
	if read == nil {
		return m
	}

	entries, err := read(ctx)
	if err != nil {
		return m
	}

	for _, a := range entries {
		if a.FileName != "" {
			m[apps.IDKey(a.ID)] = a
		}
	}

	return m
}

func decorateStore(ctx context.Context, read InstalledApps, records []ciqstore.StoreApp) []App {
	inst := installedMap(ctx, read)

	out := make([]App, 0, len(records))
	for _, a := range records {
		v := App{StoreApp: a}
		if ia, ok := inst[apps.IDKey(a.ID)]; ok {
			v.Installed, v.InstalledVersion = true, ia.Version
			v.UpdateAvailable = ia.UpdateAvailable(a.InternalVersion)
		}

		out = append(out, v)
	}

	return out
}
