package application

import (
	"context"
	"errors"
	"slices"
	"strings"
)

const inventoryPackage = "garminctl"

// OwnershipEvidence describes OS observations, not permission to replace files.
type OwnershipEvidence struct{ Manager, Package, Root, Error string }

// BinaryObservation is supplied by a caller-owned native probe.
type BinaryObservation struct {
	Path, Directory, Name, Version, Error string
	UserDirectory                         bool
	Ownership                             OwnershipEvidence
}

// LocalInventory keeps native observations separate from backend metadata.
type LocalInventory struct {
	OS, Arch     string
	Desktop, CLI BinaryObservation
}

// LocalInventorySource observes the local binaries without starting a desktop.
type LocalInventorySource interface {
	Inspect(context.Context) (LocalInventory, error)
}

// BackendInfo is separately reported even when this desktop owns the server.
type BackendInfo struct {
	Version string `json:"version,omitempty"`
	Owned   bool   `json:"owned"`
	Error   string `json:"error,omitempty"`
}

// BackendInventorySource reads backend metadata without opening a watch.
type BackendInventorySource interface {
	Inspect(context.Context) (BackendInfo, error)
}

// BinaryVersion preserves each role's path and raw reported build label.
type BinaryVersion struct {
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// InstallationInfo is conservative detection evidence, never install admission.
type InstallationInfo struct {
	Kind    string `json:"kind"`
	Root    string `json:"root,omitempty"`
	Package string `json:"package,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// InventoryResult distinguishes all three versions and confirmed installation.
type InventoryResult struct {
	OS               string           `json:"os"`
	Arch             string           `json:"arch"`
	Desktop          BinaryVersion    `json:"desktop"`
	CLI              BinaryVersion    `json:"cli"`
	Backend          BackendInfo      `json:"backend"`
	Installation     InstallationInfo `json:"installation"`
	VersionAgreement string           `json:"versionAgreement"`
}

// ReadInventoryAction combines native evidence and backend metadata read-only.
type ReadInventoryAction struct {
	desktopVersion string
	local          LocalInventorySource
	backend        BackendInventorySource
}

// NewReadInventoryAction borrows sources and the actual compiled desktop label.
func NewReadInventoryAction(desktopVersion string, local LocalInventorySource, backend BackendInventorySource) *ReadInventoryAction {
	return &ReadInventoryAction{desktopVersion: desktopVersion, local: local, backend: backend}
}

// Run returns metadata without granting update or rollback permission.
func (a *ReadInventoryAction) Run(ctx context.Context) (*InventoryResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if a.local == nil {
		return nil, errors.New("application inventory source is not configured")
	}

	local, err := a.local.Inspect(ctx)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}

	if err != nil {
		local.Desktop.Error = err.Error()
		local.CLI.Error = err.Error()
	}

	backend := BackendInfo{Error: "backend information is unavailable"}
	if a.backend != nil {
		backend, err = a.backend.Inspect(ctx)
		if err != nil {
			backend.Error = err.Error()
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := &InventoryResult{
		OS: local.OS, Arch: local.Arch,
		Desktop: BinaryVersion{Path: local.Desktop.Path, Version: a.desktopVersion, Error: local.Desktop.Error},
		CLI:     BinaryVersion{Path: local.CLI.Path, Version: local.CLI.Version, Error: local.CLI.Error}, Backend: backend,
		Installation: classifyInstallation(local),
	}
	result.VersionAgreement = versionAgreement(result)

	return result, nil
}

func versionAgreement(result *InventoryResult) string {
	if result.Desktop.Error != "" || result.CLI.Error != "" || result.Backend.Error != "" {
		return "unknown"
	}

	versions := []string{result.Desktop.Version, result.CLI.Version, result.Backend.Version}
	if slices.Contains(versions, "") {
		return "unknown"
	}

	if strings.TrimPrefix(versions[0], "v") != strings.TrimPrefix(versions[1], "v") || strings.TrimPrefix(versions[0], "v") != strings.TrimPrefix(versions[2], "v") {
		return "different"
	}

	return "same"
}

func classifyInstallation(local LocalInventory) InstallationInfo {
	desktop, cli := local.Desktop, local.CLI

	unknown := InstallationInfo{Kind: "unknown", Reason: "installation ownership is not confirmed for both binaries"}
	if reason := inventoryOwnershipErrors(desktop.Ownership, cli.Ownership); reason != "" {
		unknown.Reason = reason
	}

	if !inventoryBinaryAvailable(desktop) || !inventoryBinaryAvailable(cli) {
		return unknown
	}

	if !expectedInventoryNames(local) {
		return unknown
	}

	d, c := desktop.Ownership, cli.Ownership
	if local.OS == "windows" && matchingSetupOwnership(d, c) {
		return InstallationInfo{Kind: "setup", Root: d.Root, Package: inventoryPackage}
	}

	if local.OS == "linux" && d.Manager == c.Manager && d.Package == inventoryPackage && c.Package == inventoryPackage {
		switch d.Manager {
		case "dpkg":
			return InstallationInfo{Kind: "deb", Package: inventoryPackage}
		case "rpm":
			return InstallationInfo{Kind: "rpm", Package: inventoryPackage}
		}
	}

	if matchingPortableLocation(local) {
		return InstallationInfo{Kind: "portable", Root: desktop.Directory}
	}

	return unknown
}

func inventoryOwnershipErrors(desktop, cli OwnershipEvidence) string {
	var messages []string
	if desktop.Error != "" {
		messages = append(messages, "desktop ownership: "+desktop.Error)
	}

	if cli.Error != "" {
		messages = append(messages, "CLI ownership: "+cli.Error)
	}

	return strings.Join(messages, "; ")
}

func expectedInventoryNames(local LocalInventory) bool {
	if local.OS == "windows" {
		return strings.EqualFold(local.Desktop.Name, "garmin-desktop.exe") && strings.EqualFold(local.CLI.Name, "garminctl.exe")
	}

	return local.OS == "linux" && local.Desktop.Name == "garmin-desktop" && local.CLI.Name == inventoryPackage
}

func sameInventoryDirectory(platform, a, b string) bool {
	if platform == "windows" {
		return strings.EqualFold(a, b)
	}

	return a == b
}

func inventoryBinaryAvailable(binary BinaryObservation) bool {
	return binary.Path != "" && binary.Error == "" && binary.Ownership.Error == ""
}

func matchingSetupOwnership(d, c OwnershipEvidence) bool {
	return d.Manager == "registry" && c.Manager == "registry" && d.Package == inventoryPackage && c.Package == inventoryPackage && d.Root != "" && strings.EqualFold(d.Root, c.Root)
}

func matchingPortableLocation(local LocalInventory) bool {
	desktop, cli := local.Desktop, local.CLI
	return desktop.Ownership.Manager == "none" && cli.Ownership.Manager == "none" && desktop.UserDirectory && cli.UserDirectory && desktop.Directory != "" && sameInventoryDirectory(local.OS, desktop.Directory, cli.Directory)
}
