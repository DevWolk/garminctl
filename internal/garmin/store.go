package garmin

import (
	"context"
	"strings"
	"sync"

	"github.com/DevWolk/garminctl/internal/ciqstore"
)

// StoreView is a store page merged with what the watch has installed.
type StoreView struct {
	Total int                      `json:"total"`
	Apps  []StoreViewApp           `json:"apps"`
	Types []ciqstore.StoreTypeInfo `json:"types"`
	Sorts []ciqstore.StoreTypeInfo `json:"sorts"`
}

// StoreViewApp is a store app plus its state on the watch.
type StoreViewApp struct {
	ciqstore.StoreApp
	InstalledVersion int  `json:"installedVersion,omitempty"`
	Installed        bool `json:"installed"`
	UpdateAvailable  bool `json:"updateAvailable"`
}

var (
	deviceTypeMu    sync.Mutex
	deviceTypeCache = map[string]string{}
)

func storeDeviceType(ctx context.Context, part string) string {
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
func (s *Service) installedMap(ctx context.Context) map[string]App {
	m := map[string]App{}
	if s == nil {
		return m
	}

	apps, err := s.Apps(ctx)
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

func (s *Service) decorateStore(ctx context.Context, apps []ciqstore.StoreApp) []StoreViewApp {
	inst := s.installedMap(ctx)

	out := make([]StoreViewApp, 0, len(apps))
	for _, a := range apps {
		v := StoreViewApp{StoreApp: a}
		if ia, ok := inst[strings.ToLower(a.ID)]; ok {
			v.Installed, v.InstalledVersion = true, ia.Version
			v.UpdateAvailable = a.InternalVersion > ia.Version
		}

		out = append(out, v)
	}

	return out
}

// BrowseStore searches or lists the Connect IQ store for this watch. s may
// be nil when no watch is connected; part is the firmware part number.
func (s *Service) BrowseStore(ctx context.Context, part string, q ciqstore.StoreQuery) (*StoreView, error) {
	q.PartNumber = part
	q.DeviceTypeID = storeDeviceType(ctx, part)

	page, err := ciqstore.Browse(ctx, q)
	if err != nil {
		return nil, err
	}

	return &StoreView{Total: page.Total, Apps: s.decorateStore(ctx, page.Apps), Types: ciqstore.StoreTypeList(), Sorts: ciqstore.StoreSortList()}, nil
}

// StoreDetail is one app with reviews.
type StoreDetail struct {
	StoreViewApp
	Reviews []ciqstore.StoreReview `json:"reviews"`
}

// StoreApp fetches one store app for this watch.
func (s *Service) StoreApp(ctx context.Context, part, id string) (*StoreDetail, error) {
	d, err := ciqstore.Detail(ctx, id, part, storeDeviceType(ctx, part))
	if err != nil {
		return nil, err
	}

	rv, _ := ciqstore.Reviews(ctx, id, 8)
	v := s.decorateStore(ctx, []ciqstore.StoreApp{*d})[0]

	return &StoreDetail{StoreViewApp: v, Reviews: rv}, nil
}
