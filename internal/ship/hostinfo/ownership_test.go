package hostinfo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestReaderRequiresUnambiguousOwnership(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		owner   func(probeCommand, string) (string, error)
		manager string
		invalid bool
	}{
		{"unmanaged", func(probeCommand, string) (string, error) { return "", errNoPackageOwner }, testUnmanaged, false},
		{"rpm", func(kind probeCommand, _ string) (string, error) {
			if kind == probeDPKG {
				return "", errNoPackageOwner
			}

			return "garminctl\n", nil
		}, "rpm", false},
		{"two managers", func(kind probeCommand, path string) (string, error) {
			if kind == probeDPKG {
				return "garminctl: " + path + "\n", nil
			}

			return "garminctl\n", nil
		}, "", true},
		{"ambiguous dpkg", func(probeCommand, string) (string, error) { return "garminctl,other: /elsewhere\n", nil }, "", true},
		{"database failure", func(probeCommand, string) (string, error) { return "", errors.New("cannot open package database") }, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := fixtureReader(t)
			reader.lookup = func(name string) (string, error) { return name, nil }
			previous := reader.run
			reader.run = func(ctx context.Context, kind probeCommand, path string) (string, error) {
				if kind == probeCLI {
					return previous(ctx, kind, path)
				}

				return tc.owner(kind, path)
			}

			result, err := reader.Inspect(t.Context())
			if err != nil || result.CLI.Ownership.Manager != tc.manager || (result.CLI.Ownership.Error != "") != tc.invalid {
				t.Fatalf("ownership=%+v error=%v", result.CLI.Ownership, err)
			}
		})
	}
}

func TestReaderWindowsRegistryEvidenceIsLocationScoped(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, manager string
		invalid       bool
	}{
		{"registered", "registry", false}, {"elsewhere", testUnmanaged, false}, {"missing", testUnmanaged, false}, {"denied", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := fixtureReader(t)
			reader.platform = "windows"
			directory := filepath.Dir(reader.config.CLIPath)
			reader.registered = func(key string) (string, error) {
				if key != reader.config.RegistryKey {
					t.Fatalf("unexpected registry key %q", key)
				}

				switch tc.name {
				case "missing":
					return "", os.ErrNotExist
				case "denied":
					return "", os.ErrPermission
				case "elsewhere":
					return t.TempDir(), nil
				default:
					return directory, nil
				}
			}

			result, err := reader.Inspect(t.Context())
			if err != nil || result.CLI.Ownership.Manager != tc.manager || (result.CLI.Ownership.Error != "") != tc.invalid {
				t.Fatalf("registry=%+v error=%v", result.CLI.Ownership, err)
			}
		})
	}
}

func TestReaderDiscoversSiblingAndPreservesUnavailableDesktop(t *testing.T) {
	t.Parallel()
	reader := fixtureReader(t)
	reader.config.CLIPath = ""
	reader.lookup = func(string) (string, error) { return "", exec.ErrNotFound }

	result, err := reader.Inspect(t.Context())
	if err != nil || result.CLI.Version == "" || result.CLI.Ownership.Error == "" {
		t.Fatalf("sibling=%+v error=%v", result, err)
	}

	reader.config.DesktopPath = ""
	reader.executable = func() (string, error) { return "", errors.New("current executable inaccessible") }

	result, err = reader.Inspect(t.Context())
	if err != nil || result.Desktop.Error != "current executable inaccessible" || result.CLI.Error == "" {
		t.Fatalf("unavailable=%+v error=%v", result, err)
	}
}

func TestCLIOutputRequiresSingleVersionLine(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"", "garminctl ", "different v1", "garminctl v1\nextra", "garminctl v1 dev"} {
		if version, err := cliVersion(text); err == nil {
			t.Errorf("accepted %q as %q", text, version)
		}
	}

	if version, err := cliVersion("garminctl dev\r\n"); err != nil || version != "dev" {
		t.Fatalf("version=%q error=%v", version, err)
	}
}
