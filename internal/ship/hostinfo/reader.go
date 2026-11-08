// Package hostinfo observes local executable and OS package ownership metadata.
// It starts only a bounded CLI version probe and read-only package queries.
package hostinfo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Config supplies trusted local binary paths and package/registry identities.
type Config struct{ DesktopPath, CLIPath, PackageName, RegistryKey string }

// Ownership is raw OS evidence, without updater admission policy.
type Ownership struct{ Manager, Package, Root, Error string }

// Binary records a regular executable location and its reported CLI label.
type Binary struct {
	Path, Directory, Name, Version, Error string
	UserDirectory                         bool
	Ownership                             Ownership
}

// Snapshot records this host's platform and two separate executable roles.
type Snapshot struct {
	OS, Arch     string
	Desktop, CLI Binary
}

// Reader borrows local OS facilities and owns no background work.
type Reader struct {
	config         Config
	platform, arch string
	run            func(context.Context, probeCommand, string) (string, error)
	lookup         func(string) (string, error)
	executable     func() (string, error)
	home           func() (string, error)
	registered     func(string) (string, error)
}

// New validates no files and performs no commands until Inspect.
func New(config Config) *Reader {
	return &Reader{config: config, platform: runtime.GOOS, arch: runtime.GOARCH, run: runProbe, lookup: exec.LookPath, executable: os.Executable, home: os.UserHomeDir, registered: registeredDirectory}
}

// Inspect observes paths and versions without running a desktop or installer.
func (r *Reader) Inspect(ctx context.Context) (Snapshot, error) {
	result := Snapshot{OS: r.platform, Arch: r.arch}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	home, _ := r.home()
	desktop := r.config.DesktopPath

	var desktopErr error
	if desktop == "" {
		desktop, desktopErr = r.executable()
	}

	result.Desktop = r.observe(desktop, home)
	if desktopErr != nil {
		result.Desktop.Error = desktopErr.Error()
	}

	cli, err := r.cliPath(result.Desktop.Directory)
	if err != nil {
		result.CLI.Error = err.Error()
	} else {
		result.CLI = r.observe(cli, home)
	}

	if result.CLI.Path != "" {
		text, err := r.run(ctx, probeCLI, result.CLI.Path)
		if err == nil {
			result.CLI.Version, err = cliVersion(text)
		}

		if err != nil {
			result.CLI.Error = err.Error()
		}
	}

	result.Desktop.Ownership = r.ownership(ctx, result.Desktop)
	result.CLI.Ownership = r.ownership(ctx, result.CLI)

	return result, ctx.Err()
}

type probeCommand int

const (
	probeCLI probeCommand = iota
	probeDPKG
	probeRPM
)

func (r *Reader) observe(path, home string) Binary {
	if path == "" {
		return Binary{Error: "binary path is unavailable"}
	}

	binary, err := regularBinary(path, home)
	if err != nil {
		binary.Error = err.Error()
	}

	return binary
}

func (r *Reader) cliPath(directory string) (string, error) {
	if r.config.CLIPath != "" {
		return r.config.CLIPath, nil
	}

	name := "garminctl"
	if r.platform == "windows" {
		name += ".exe"
	}

	if directory != "" {
		path := filepath.Join(directory, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}

	return r.lookup(name)
}

func (r *Reader) ownership(ctx context.Context, binary Binary) Ownership {
	if binary.Path == "" {
		return Ownership{Error: "binary location is unavailable"}
	}

	switch r.platform {
	case "windows":
		return r.windowsOwnership(binary)
	case "linux":
		return r.linuxOwnership(ctx, binary.Path)
	}

	return Ownership{Error: "package ownership is unsupported on this platform"}
}

func (r *Reader) windowsOwnership(binary Binary) Ownership {
	directory, err := r.registered(r.config.RegistryKey)
	if errors.Is(err, os.ErrNotExist) {
		return Ownership{Manager: "none"}
	}

	if err != nil {
		return Ownership{Error: err.Error()}
	}

	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return Ownership{Error: err.Error()}
	}

	if strings.EqualFold(binary.Directory, canonical) {
		return Ownership{Manager: "registry", Package: r.config.PackageName, Root: canonical}
	}

	return Ownership{Manager: "none"}
}

func (r *Reader) linuxOwnership(ctx context.Context, path string) Ownership {
	if strings.ContainsAny(path, "*?[") {
		return Ownership{Error: "binary path contains package-query pattern characters"}
	}

	var owners []Ownership

	checked := 0

	for _, tool := range []struct {
		name string
		kind probeCommand
	}{{"dpkg-query", probeDPKG}, {"rpm", probeRPM}} {
		if _, err := r.lookup(tool.name); err != nil {
			continue
		}

		checked++

		text, err := r.run(ctx, tool.kind, path)
		if errors.Is(err, errNoPackageOwner) {
			continue
		}

		if err != nil {
			return Ownership{Error: err.Error()}
		}

		owner, err := packageOwner(tool.kind, path, text)
		if err != nil {
			return Ownership{Error: err.Error()}
		}

		owners = append(owners, owner)
	}

	if len(owners) == 1 {
		return owners[0]
	}

	if len(owners) > 1 {
		return Ownership{Error: "multiple package managers claim the same binary"}
	}

	if checked > 0 {
		return Ownership{Manager: "none"}
	}

	return Ownership{Error: "no supported package ownership query is available"}
}

func packageOwner(kind probeCommand, path, text string) (Ownership, error) {
	line := strings.TrimSpace(text)
	if kind == probeDPKG {
		owner, actual, ok := strings.Cut(line, ": ")
		if !ok || actual != path || strings.ContainsAny(owner, ", \n\t") {
			return Ownership{}, errors.New("ambiguous dpkg file ownership")
		}

		owner, _, _ = strings.Cut(owner, ":")

		return Ownership{Manager: "dpkg", Package: owner}, nil
	}

	if kind == probeRPM && line != "" && !strings.ContainsAny(line, " \t\r\n") {
		return Ownership{Manager: "rpm", Package: line}, nil
	}

	return Ownership{}, errors.New("ambiguous package file ownership")
}

func regularBinary(path, home string) (Binary, error) {
	canonical, err := filepath.Abs(path)
	if err != nil {
		return Binary{}, err
	}

	canonical, err = filepath.EvalSymlinks(canonical)
	if err != nil {
		return Binary{}, err
	}

	info, err := os.Stat(canonical)
	if err != nil {
		return Binary{}, err
	}

	if !info.Mode().IsRegular() {
		return Binary{}, errors.New("binary path is not a regular file")
	}

	directory := filepath.Dir(canonical)
	relative, err := filepath.Rel(home, canonical)

	return Binary{Path: canonical, Directory: directory, Name: filepath.Base(canonical), UserDirectory: home != "" && err == nil && filepath.IsLocal(relative)}, nil
}

func cliVersion(text string) (string, error) {
	line := strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r")

	version, ok := strings.CutPrefix(line, "garminctl ")
	if !ok || version == "" || strings.ContainsAny(version, " \t\r\n") {
		return "", errors.New("CLI version output does not match its contract")
	}

	return version, nil
}
