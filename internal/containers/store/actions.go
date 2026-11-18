package store

import (
	"context"

	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/ship/ciqstore"
)

// InstalledApps reads registry entries within the caller's existing watch lease.
// Nil and failed readers leave catalog records undecorated.
type InstalledApps func(context.Context) ([]apps.App, error)

// BrowseAction reads a catalog page and annotates installed versions.
type BrowseAction struct{ installed InstalledApps }

// NewBrowseAction accepts the registry capability for one operation scope.
func NewBrowseAction(installed InstalledApps) *BrowseAction {
	return &BrowseAction{installed: installed}
}

// Run searches or lists the store; part is empty without a selected watch.
func (a *BrowseAction) Run(ctx context.Context, part string, q ciqstore.StoreQuery) (*View, error) {
	q.PartNumber = part
	q.DeviceTypeID = storeDeviceType(ctx, part)

	page, err := ciqstore.Browse(ctx, q)
	if err != nil {
		return nil, err
	}

	return &View{Total: page.Total, Apps: decorateStore(ctx, a.installed, page.Apps), Types: ciqstore.StoreTypeList(), Sorts: ciqstore.StoreSortList()}, nil
}

// DetailAction reads one catalog record with optional reviews and install state.
type DetailAction struct{ installed InstalledApps }

// NewDetailAction accepts the registry capability for one operation scope.
func NewDetailAction(installed InstalledApps) *DetailAction {
	return &DetailAction{installed: installed}
}

// Run retains an app's details when its separate review request fails.
func (a *DetailAction) Run(ctx context.Context, part, id string) (*Detail, error) {
	d, err := ciqstore.Detail(ctx, id, part, storeDeviceType(ctx, part))
	if err != nil {
		return nil, err
	}

	rv, _ := ciqstore.Reviews(ctx, id, 8)
	v := decorateStore(ctx, a.installed, []ciqstore.StoreApp{*d})[0]

	return &Detail{App: v, Reviews: rv}, nil
}
