package updatetransport

import (
	"path/filepath"
	"slices"
	"testing"
)

const installTestCommand = "install"

func TestNativeInstallerCommandUsesOwnedPackageAndFixedTools(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	root := filepath.Join(directory, "installation with spaces")
	payload := filepath.Join(directory, "signed package")

	for _, tc := range []struct {
		name string
		kind InstallKind
		tool string
		args []string
	}{
		{"Setup", InstallSetup, payload, []string{"/S", "/D=" + root}},
		{"DEB", InstallDEB, "/usr/bin/pkexec", []string{"/usr/bin/apt-get", "--yes", "--no-remove", installTestCommand, payload}},
		{"RPM", InstallRPM, "/usr/bin/pkexec", []string{"/usr/bin/dnf", "--assumeyes", installTestCommand, payload}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			request := installRequest{Target: MatchRequest{Installation: tc.kind}, Release: ResolvedRelease{Package: AssetRef{Name: "signed package"}}, Install: InstallOptions{Root: root}}

			got, err := nativeInstallerCommand(request, directory)
			if err != nil || got.program != tc.tool || !slices.Equal(got.args, tc.args) {
				t.Fatalf("invocation=%+v %v", got, err)
			}
		})
	}

	if _, err := nativeInstallerCommand(installRequest{Target: MatchRequest{Installation: InstallPortable}}, directory); err == nil {
		t.Fatal("portable file treated as native installer")
	}
}

func TestNativeInstallerCommandExplicitDEBRollback(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	request := installRequest{
		Target:  MatchRequest{Installation: InstallDEB},
		Release: ResolvedRelease{Package: AssetRef{Name: "signed older package.deb"}},
		Install: InstallOptions{AllowDowngrade: true},
	}
	got, err := nativeInstallerCommand(request, directory)

	want := []string{"/usr/bin/apt-get", "--yes", "--no-remove", "--allow-downgrades", installTestCommand, filepath.Join(directory, request.Release.Package.Name)}
	if err != nil || got.program != "/usr/bin/pkexec" || !slices.Equal(got.args, want) {
		t.Fatalf("rollback invocation=%+v %v", got, err)
	}
}

func TestInstallOptionsAcceptDowngradePermissionOnlyForDEB(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	for _, kind := range []InstallKind{InstallDEB, InstallRPM, InstallSetup, InstallPortable} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			options := InstallOptions{Root: directory, Desktop: filepath.Join(directory, "garmin-desktop"), CLI: filepath.Join(directory, "garminctl"), AllowDowngrade: true}

			err := validateInstallOptions(MatchRequest{OS: "linux", Installation: kind}, options)
			if (err == nil) != (kind == InstallDEB) {
				t.Fatalf("downgrade permission for %s: %v", kind, err)
			}
		})
	}
}
