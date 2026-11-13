package updatetransport

import (
	"path/filepath"
	"slices"
	"testing"
)

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
		{"DEB", InstallDEB, "/usr/bin/pkexec", []string{"/usr/bin/apt-get", "--yes", "--no-remove", "install", payload}},
		{"RPM", InstallRPM, "/usr/bin/pkexec", []string{"/usr/bin/dnf", "--assumeyes", "install", payload}},
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
