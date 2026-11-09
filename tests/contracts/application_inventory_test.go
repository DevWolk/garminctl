package contracts

import (
	"context"
	"errors"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/device"
)

type inventoryContractSource struct{}

func (inventoryContractSource) Inspect(context.Context) (application.LocalInventory, error) {
	return application.LocalInventory{OS: "linux", Arch: "amd64", Desktop: application.BinaryObservation{Path: "/fixture/garmin-desktop"}, CLI: application.BinaryObservation{Path: "/fixture/garminctl", Version: "v1.2.0"}}, nil
}

func TestApplicationInventoryUsesProductionServerMetadataWithoutOpeningWatch(t *testing.T) { //nolint:paralleltest // production composition reads the isolated process account paths.
	isolateContractAccount(t)

	var opens atomic.Int32

	server := serveContractRoot(t, func(context.Context) (device.Device, error) {
		opens.Add(1)
		return nil, errors.New("inventory must not open a watch")
	})

	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	action, err := app.NewApplicationInventory(app.ApplicationInventoryConfig{DesktopVersion: "v1.3.0", BackendURL: endpoint, BackendOwned: false, HTTPClient: server.Client(), LocalSource: inventoryContractSource{}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := action.Run(t.Context())
	if err != nil || result == nil {
		t.Fatalf("inventory=%+v error=%v", result, err)
	}

	if result.Backend.Error != "" || result.Backend.Version != contractVersion || result.Backend.Owned || result.Desktop.Version != "v1.3.0" || result.CLI.Version != "v1.2.0" || opens.Load() != 0 {
		t.Fatalf("inventory=%+v opens=%d", result, opens.Load())
	}
}
