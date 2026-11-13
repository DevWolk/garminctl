package app

import (
	"context"
	"errors"
	"net/http"
	"os"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

// ApplicationReadConfig contains only trusted local composition settings.
type ApplicationReadConfig struct {
	Inventory        ApplicationInventoryConfig
	ReleaseClient    *http.Client
	HistoryDirectory string
	RestartArgs      []string
}

// ApplicationReads assembles metadata Actions without a watch runtime.
type ApplicationReads struct {
	inventory *application.ReadInventoryAction
	saved     *application.ReadReleaseHistoryAction
	check     *application.CheckReleaseHistoryAction
	version   string
}

// NewApplicationReads performs no IO; sources are invoked only by its methods.
func NewApplicationReads(config ApplicationReadConfig) (*ApplicationReads, error) {
	inventory, err := NewApplicationInventory(config.Inventory)
	if err != nil {
		return nil, err
	}

	store, err := applicationHistoryStoreFor(config.HistoryDirectory)
	if err != nil {
		return nil, err
	}

	feed := applicationReleaseFeed{source: updatetransport.NewGitHub(config.ReleaseClient)}

	return &ApplicationReads{inventory: inventory, saved: application.NewReadReleaseHistoryAction(store), check: application.NewCheckReleaseHistoryAction(feed, store), version: config.Inventory.DesktopVersion}, nil
}

// Info reads local installation and independent backend metadata.
func (r *ApplicationReads) Info(ctx context.Context) (*application.InventoryResult, error) {
	return r.inventory.Run(ctx)
}

// History selects saved metadata, contacting the feed only for explicit refresh.
// No saved snapshot is a normal first-use result; corrupt/unreadable state is not.
func (r *ApplicationReads) History(ctx context.Context, prerelease, refresh bool) (*application.ReleaseHistoryResult, error) {
	request := application.CheckRequest{CurrentVersion: r.version, IncludePrerelease: prerelease}
	if refresh {
		return r.check.Run(ctx, request)
	}

	result, err := r.saved.Run(ctx, request)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	return result, err
}
