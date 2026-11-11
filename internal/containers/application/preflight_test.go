package application_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

const (
	preflightTestLinux    = "linux"
	preflightArchAMD64    = "amd64"
	preflightTestWindows  = "windows"
	preflightKindPortable = "portable"
)

type preflightInventory struct {
	value *application.InventoryResult
	calls int
	err   error
}

func (f *preflightInventory) Info(context.Context) (*application.InventoryResult, error) {
	f.calls++
	return f.value, f.err
}

type preflightVerified struct {
	record application.PackageRecord
	closes int
	err    error
}

func (p *preflightVerified) Record() application.PackageRecord { return p.record }
func (p *preflightVerified) Close() error                      { p.closes++; return p.err }

type preflightPackages struct {
	rows                       []application.PackageRecord
	opened                     []string
	copies                     []*preflightVerified
	listCalls                  int
	listErr, openErr, closeErr error
	onOpen                     func(string)
	changeRecord               bool
}

func (f *preflightPackages) List(context.Context, application.DownloadTarget) ([]application.PackageRecord, error) {
	f.listCalls++
	return slices.Clone(f.rows), f.listErr
}

func (f *preflightPackages) Open(ctx context.Context, _ application.DownloadTarget, id string) (application.VerifiedPackage, error) {
	f.opened = append(f.opened, id)
	openErr := f.openErr

	index := slices.IndexFunc(f.rows, func(row application.PackageRecord) bool { return row.ID == id })
	if index < 0 {
		return nil, errors.New("missing fixture package")
	}

	value := &preflightVerified{record: f.rows[index], err: f.closeErr}
	if f.changeRecord {
		value.record.Digest = strings.Repeat("f", 64)
	}

	f.copies = append(f.copies, value)
	if f.onOpen != nil {
		f.onOpen(id)
	}

	return value, errors.Join(openErr, ctx.Err())
}

func preflightFixture() (*preflightInventory, *preflightPackages, application.PreflightRequest) {
	info := downloadLocal()
	info.Desktop.Path = "/trusted/garmin-desktop"
	info.Backend = application.BackendInfo{Owned: true, Version: version100}
	info.Installation.Package = "garminctl"
	current := application.PackageRecord{ID: strings.Repeat("a", 64), Tag: version100, Digest: strings.Repeat("c", 64), OS: preflightTestLinux, Arch: preflightArchAMD64, Installation: downloadTestDEB, Bytes: 10}
	update := current
	update.ID, update.Tag, update.Digest = strings.Repeat("b", 64), downloadTestStable, strings.Repeat("d", 64)

	return &preflightInventory{value: info}, &preflightPackages{rows: []application.PackageRecord{current, update}}, application.PreflightRequest{CacheID: update.ID, Tag: update.Tag}
}

func assertPreflightReleased(t *testing.T, gate *lifecycle.Gate, packages *preflightPackages) {
	t.Helper()

	if state := gate.Snapshot(); state.State != lifecycle.Running {
		t.Fatalf("maintenance leaked: %+v", state)
	}

	for _, artifact := range packages.copies {
		if artifact.closes != 1 {
			t.Fatalf("verified copy closed %d times", artifact.closes)
		}
	}
}

func TestPreflightOwnsVerifiedPairUntilClose(t *testing.T) {
	t.Parallel()

	inventory, packages, request := preflightFixture()
	gate := new(lifecycle.Gate)

	prepared, err := application.NewPreflightAction(inventory, packages, gate.TryMaintenance).Run(t.Context(), request)
	if err != nil || prepared == nil {
		t.Fatalf("preflight=%v %v", prepared, err)
	}

	defer func() {
		if err := prepared.Close(); err != nil {
			t.Error(err)
		}
	}()

	if inventory.calls != 2 || len(packages.opened) != 2 || gate.Snapshot().State != lifecycle.Maintenance {
		t.Fatalf("reads=%d opens=%v gate=%+v", inventory.calls, packages.opened, gate.Snapshot())
	}

	got := prepared.Info()
	if got.Current != packages.rows[0] || got.Update != packages.rows[1] || got.Inventory != *inventory.value {
		t.Fatalf("snapshot=%+v", got)
	}

	got.Inventory.CLI.Path = "caller mutation"
	if prepared.Info().Inventory.CLI.Path != inventory.value.CLI.Path {
		t.Fatal("caller mutated preflight inventory")
	}

	for _, artifact := range packages.copies {
		if artifact.closes != 0 {
			t.Fatal("verified copy released before its owner")
		}
	}

	var joined sync.WaitGroup
	for range 2 {
		joined.Go(func() {
			if err := prepared.Close(); err != nil {
				t.Error(err)
			}
		})
	}

	joined.Wait()
	assertPreflightReleased(t, gate, packages)
}

func TestPreflightRejectsUnconfirmedInventoryBeforePackageIO(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		change func(*application.InventoryResult)
	}{
		{name: "reused", change: func(i *application.InventoryResult) { i.Backend.Owned = false }},
		{name: "backend error", change: func(i *application.InventoryResult) { i.Backend.Error = "unreachable" }},
		{name: "backend version", change: func(i *application.InventoryResult) { i.Backend.Version = "v9.9.9" }},
		{name: "CLI version", change: func(i *application.InventoryResult) { i.CLI.Version = "v9.9.9" }},
		{name: "desktop path", change: func(i *application.InventoryResult) { i.Desktop.Path = "" }},
		{name: "CLI path", change: func(i *application.InventoryResult) { i.CLI.Path = "" }},
		{name: "unknown installation", change: func(i *application.InventoryResult) { i.Installation.Kind = inventoryTestUnknown }},
		{name: "package ownership", change: func(i *application.InventoryResult) { i.Installation.Package = "other" }},
		{name: "platform", change: func(i *application.InventoryResult) { i.OS = "other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			inventory, packages, request := preflightFixture()
			test.change(inventory.value)

			gate := new(lifecycle.Gate)
			if result, err := application.NewPreflightAction(inventory, packages, gate.TryMaintenance).Run(t.Context(), request); result != nil || err == nil || packages.listCalls != 0 {
				t.Fatalf("result=%v error=%v lists=%d", result, err, packages.listCalls)
			}

			assertPreflightReleased(t, gate, packages)
		})
	}
}

func TestPreflightRejectsUnsafeCatalogBeforeOpen(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		change func(*preflightPackages, *application.PreflightRequest)
		want   error
	}{
		{name: "missing recovery", change: func(p *preflightPackages, _ *application.PreflightRequest) { p.rows = p.rows[1:] }, want: application.ErrRecoveryPackageUnavailable},
		{name: "ambiguous recovery", change: func(p *preflightPackages, _ *application.PreflightRequest) {
			v := p.rows[0]
			v.ID = strings.Repeat("e", 64)
			p.rows = append(p.rows, v)
		}},
		{name: "unselected id", change: func(_ *preflightPackages, r *application.PreflightRequest) { r.CacheID = strings.Repeat("e", 64) }},
		{name: "different tag", change: func(_ *preflightPackages, r *application.PreflightRequest) { r.Tag = "v5.0.0" }},
		{name: "draft", change: func(p *preflightPackages, _ *application.PreflightRequest) { p.rows[1].Draft = true }},
		{name: "prerelease flag", change: func(p *preflightPackages, _ *application.PreflightRequest) { p.rows[1].Prerelease = true }},
		{name: "prerelease suffix", change: func(p *preflightPackages, r *application.PreflightRequest) {
			p.rows[1].Tag = downloadTestBeta
			r.Tag = downloadTestBeta
		}},
		{name: "same version", change: func(p *preflightPackages, r *application.PreflightRequest) {
			p.rows[1].Tag = version100
			r.Tag = version100
		}},
		{name: "downgrade", change: func(p *preflightPackages, r *application.PreflightRequest) {
			p.rows[1].Tag = "v0.9.0"
			r.Tag = "v0.9.0"
		}},
		{name: "OS", change: func(p *preflightPackages, _ *application.PreflightRequest) { p.rows[1].OS = preflightTestWindows }},
		{name: "architecture", change: func(p *preflightPackages, _ *application.PreflightRequest) { p.rows[1].Arch = "arm64" }},
		{name: "installation", change: func(p *preflightPackages, _ *application.PreflightRequest) { p.rows[1].Installation = "rpm" }},
		{name: "digest", change: func(p *preflightPackages, _ *application.PreflightRequest) { p.rows[1].Digest = "" }},
		{name: "size", change: func(p *preflightPackages, _ *application.PreflightRequest) { p.rows[1].Bytes = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			inventory, packages, request := preflightFixture()
			test.change(packages, &request)

			gate := new(lifecycle.Gate)

			result, err := application.NewPreflightAction(inventory, packages, gate.TryMaintenance).Run(t.Context(), request)
			if result != nil || err == nil || len(packages.opened) != 0 {
				t.Fatalf("result=%v error=%v opens=%v", result, err, packages.opened)
			}

			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}

			assertPreflightReleased(t, gate, packages)
		})
	}
}

func TestPreflightFailuresClosePartialPairAndReturnMaintenance(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"list", "first verify", "second verify", "cancel verify", "changed receipt", "changed inventory", "cleanup"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			inventory, packages, request := preflightFixture()
			gate := new(lifecycle.Gate)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			failure := errors.New("fixture failure")

			switch name {
			case "list":
				packages.listErr = failure
			case "first verify":
				packages.openErr = failure
			case "second verify":
				packages.onOpen = func(id string) {
					if id == packages.rows[0].ID {
						packages.openErr = failure
					}
				}
			case "cancel verify":
				packages.onOpen = func(string) { cancel() }
			case "changed receipt":
				packages.changeRecord = true
			case "changed inventory":
				packages.onOpen = func(string) { inventory.value.CLI.Path = "/changed/garminctl" }
			case "cleanup":
				packages.closeErr = failure
				packages.openErr = failure
			}

			result, err := application.NewPreflightAction(inventory, packages, gate.TryMaintenance).Run(ctx, request)
			if result != nil || err == nil {
				t.Fatalf("result=%v error=%v", result, err)
			}

			assertPreflightFailureCause(t, name, err, failure)

			assertPreflightReleased(t, gate, packages)
		})
	}
}

func TestPreflightBusyAndCancelledRequestsDoNoPackageIO(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"busy", "cancelled", "bad id", "bad tag"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			inventory, packages, request := preflightFixture()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			gate := new(lifecycle.Gate)

			var (
				release func()
				err     error
			)

			switch name {
			case "busy":
				release, err = gate.EnterJob(ctx)
			case "cancelled":
				cancel()
			case "bad id":
				request.CacheID = "../package"
			case "bad tag":
				request.Tag = "../release"
			}

			if err != nil {
				t.Fatal(err)
			}

			if release != nil {
				defer release()
			}

			result, err := application.NewPreflightAction(inventory, packages, gate.TryMaintenance).Run(ctx, request)
			if result != nil || err == nil || inventory.calls != 0 || packages.listCalls != 0 {
				t.Fatalf("result=%v error=%v inventory=%d lists=%d", result, err, inventory.calls, packages.listCalls)
			}

			assertPreflightRejectionCause(t, name, err)
		})
	}
}

func assertPreflightFailureCause(t *testing.T, name string, err, failure error) {
	t.Helper()

	if name == "cancel verify" && !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}

	if name == "cleanup" && !errors.Is(err, failure) {
		t.Fatalf("cleanup error lost: %v", err)
	}
}

func assertPreflightRejectionCause(t *testing.T, name string, err error) {
	t.Helper()

	if name == "busy" && !errors.Is(err, lifecycle.ErrBusy) {
		t.Fatalf("busy lost: %v", err)
	}

	if name == "cancelled" && !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestPreflightCurrentPrereleaseIsAllowedForRecovery(t *testing.T) {
	t.Parallel()

	inventory, packages, request := preflightFixture()
	inventory.value.Desktop.Version = "v1.0.0-beta.1"
	inventory.value.CLI.Version = inventory.value.Desktop.Version
	inventory.value.Backend.Version = inventory.value.Desktop.Version
	packages.rows[0].Tag = inventory.value.Desktop.Version
	packages.rows[0].Prerelease = true
	gate := new(lifecycle.Gate)

	result, err := application.NewPreflightAction(inventory, packages, gate.TryMaintenance).Run(t.Context(), request)
	if err != nil || result == nil {
		t.Fatalf("recovery=%v %v", result, err)
	}

	if err := result.Close(); err != nil {
		t.Fatal(err)
	}

	assertPreflightReleased(t, gate, packages)
}

func TestPreflightSupportedTargetsAndExplicitPrerelease(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ name, platform, arch, kind string }{
		{name: "DEB", platform: preflightTestLinux, arch: preflightArchAMD64, kind: downloadTestDEB},
		{name: "RPM", platform: preflightTestLinux, arch: preflightArchAMD64, kind: "rpm"},
		{name: "Setup", platform: preflightTestWindows, arch: preflightArchAMD64, kind: "setup"},
		{name: "Linux portable", platform: preflightTestLinux, arch: "arm64", kind: preflightKindPortable},
		{name: "Windows portable", platform: preflightTestWindows, arch: "arm64", kind: preflightKindPortable},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			inventory, packages, request := preflightFixture()
			inventory.value.OS, inventory.value.Arch = test.platform, test.arch

			inventory.value.Installation.Kind, inventory.value.Installation.Root = test.kind, "observed-root"
			for i := range packages.rows {
				packages.rows[i].OS, packages.rows[i].Arch, packages.rows[i].Installation = test.platform, test.arch, test.kind
			}

			request.Tag, request.IncludePrerelease = downloadTestBeta, true
			packages.rows[1].Tag = downloadTestBeta
			gate := new(lifecycle.Gate)

			result, err := application.NewPreflightAction(inventory, packages, gate.TryMaintenance).Run(t.Context(), request)
			if err != nil || result == nil {
				t.Fatalf("target=%v %v", result, err)
			}

			if err := result.Close(); err != nil {
				t.Fatal(err)
			}

			assertPreflightReleased(t, gate, packages)
		})
	}
}

func TestPreflightCloseErrorsRemainVisibleAndReleaseMaintenance(t *testing.T) {
	t.Parallel()

	inventory, packages, request := preflightFixture()
	failure := errors.New("verified copy cleanup failed")
	packages.closeErr = failure
	gate := new(lifecycle.Gate)

	result, err := application.NewPreflightAction(inventory, packages, gate.TryMaintenance).Run(t.Context(), request)
	if err != nil || result == nil {
		t.Fatalf("preflight=%v %v", result, err)
	}

	for range 2 {
		if err := result.Close(); !errors.Is(err, failure) {
			t.Fatalf("cleanup error=%v", err)
		}
	}

	assertPreflightReleased(t, gate, packages)
}
