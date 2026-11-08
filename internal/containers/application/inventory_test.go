package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
)

const (
	inventoryTestPackage   = "garminctl"
	inventoryTestRPM       = "rpm"
	inventoryTestUnknown   = "unknown"
	inventoryTestUnmanaged = "none"
)

type localInventoryFixture struct {
	value  application.LocalInventory
	err    error
	cancel context.CancelFunc
}

func TestInventoryDoesNotSubstituteBackendForMissingDesktopVersion(t *testing.T) {
	t.Parallel()

	observation := inventoryFixture()
	observation.CLI.Ownership.Error = "read-only query failed"

	result, err := application.NewReadInventoryAction("", localInventoryFixture{value: observation}, backendInventoryFixture{value: application.BackendInfo{Version: version100, Owned: true}}).Run(t.Context())
	if err != nil || result == nil || result.Desktop.Version != "" || result.VersionAgreement != inventoryTestUnknown || result.Installation.Kind != inventoryTestUnknown || !strings.Contains(result.Installation.Reason, "read-only query failed") {
		t.Fatalf("missing desktop=%+v error=%v", result, err)
	}
}

func TestInventoryRecognizesEqualBuildLabelsWithoutInferringDataCompatibility(t *testing.T) {
	t.Parallel()

	result, err := application.NewReadInventoryAction("1.0.0", localInventoryFixture{value: inventoryFixture()}, backendInventoryFixture{value: application.BackendInfo{Version: version100, Owned: true}}).Run(t.Context())
	if err != nil || result == nil || result.VersionAgreement != "same" {
		t.Fatalf("equal labels=%+v error=%v", result, err)
	}
}

func (f localInventoryFixture) Inspect(context.Context) (application.LocalInventory, error) {
	if f.cancel != nil {
		f.cancel()
	}

	return f.value, f.err
}

type backendInventoryFixture struct {
	value application.BackendInfo
	err   error
}

func (f backendInventoryFixture) Inspect(context.Context) (application.BackendInfo, error) {
	return f.value, f.err
}

func inventoryFixture() application.LocalInventory {
	return application.LocalInventory{
		OS: "linux", Arch: "amd64",
		Desktop: application.BinaryObservation{Path: "/usr/bin/garmin-desktop", Directory: "/usr/bin", Name: "garmin-desktop", Version: "untrusted-probe-label", Ownership: application.OwnershipEvidence{Manager: "dpkg", Package: inventoryTestPackage}},
		CLI:     application.BinaryObservation{Path: "/usr/bin/garminctl", Directory: "/usr/bin", Name: inventoryTestPackage, Version: version100, Ownership: application.OwnershipEvidence{Manager: "dpkg", Package: inventoryTestPackage}},
	}
}

func TestInventoryKeepsDesktopCLIAndBackendSeparate(t *testing.T) {
	t.Parallel()

	result, err := application.NewReadInventoryAction(historyStableTag, localInventoryFixture{value: inventoryFixture()}, backendInventoryFixture{value: application.BackendInfo{Version: historyBetaTag, Owned: false}}).Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if result.Desktop.Version != historyStableTag || result.CLI.Version != version100 || result.Backend.Version != historyBetaTag || result.Backend.Owned {
		t.Fatalf("version roles=%+v", result)
	}

	if result.Installation.Kind != "deb" || result.Installation.Package != inventoryTestPackage || result.VersionAgreement != "different" {
		t.Fatalf("inventory=%+v", result)
	}
}

func TestInventoryRequiresConfirmedMatchingOwnership(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, kind string
		edit       func(*application.LocalInventory)
	}{
		{"deb", "deb", func(*application.LocalInventory) {}},
		{inventoryTestRPM, inventoryTestRPM, func(i *application.LocalInventory) {
			i.Desktop.Ownership.Manager = inventoryTestRPM
			i.CLI.Ownership.Manager = inventoryTestRPM
		}},
		{"mixed packages", inventoryTestUnknown, func(i *application.LocalInventory) { i.CLI.Ownership.Package = "other" }},
		{"mixed managers", inventoryTestUnknown, func(i *application.LocalInventory) { i.CLI.Ownership.Manager = inventoryTestRPM }},
		{"probe failed", inventoryTestUnknown, func(i *application.LocalInventory) { i.CLI.Ownership.Error = "query failed" }},
		{"system path unmanaged", inventoryTestUnknown, func(i *application.LocalInventory) {
			i.Desktop.Ownership.Manager = inventoryTestUnmanaged
			i.CLI.Ownership.Manager = inventoryTestUnmanaged
		}},
		{"portable", "portable", func(i *application.LocalInventory) {
			i.Desktop.Ownership.Manager = inventoryTestUnmanaged
			i.CLI.Ownership.Manager = inventoryTestUnmanaged
			i.Desktop.UserDirectory = true
			i.CLI.UserDirectory = true
		}},
		{"setup", "setup", func(i *application.LocalInventory) {
			i.OS = "windows"
			i.Desktop.Name = "garmin-desktop.exe"
			i.CLI.Name = "garminctl.exe"
			i.Desktop.Ownership = application.OwnershipEvidence{Manager: "registry", Package: inventoryTestPackage, Root: "C:\\Apps\\garminctl"}
			i.CLI.Ownership = i.Desktop.Ownership
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			observation := inventoryFixture()
			tc.edit(&observation)

			result, err := application.NewReadInventoryAction(version100, localInventoryFixture{value: observation}, backendInventoryFixture{value: application.BackendInfo{Version: version100, Owned: true}}).Run(t.Context())
			if err != nil || result == nil || result.Installation.Kind != tc.kind {
				t.Fatalf("inventory=%+v error=%v", result, err)
			}
		})
	}
}

func TestInventoryPreservesPartialErrorsAndCallerCancellation(t *testing.T) {
	t.Parallel()

	observation := inventoryFixture()
	observation.CLI.Error = "CLI unavailable"
	observation.CLI.Version = ""

	result, err := application.NewReadInventoryAction(version100, localInventoryFixture{value: observation}, backendInventoryFixture{value: application.BackendInfo{Owned: true}, err: errors.New("backend unavailable")}).Run(t.Context())
	if err != nil || result == nil || result.CLI.Error == "" || result.Backend.Error == "" || result.VersionAgreement != inventoryTestUnknown {
		t.Fatalf("partial inventory=%+v error=%v", result, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	result, err = application.NewReadInventoryAction(version100, localInventoryFixture{value: observation, cancel: cancel}, backendInventoryFixture{}).Run(ctx)
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled inventory=%+v error=%v", result, err)
	}
}
