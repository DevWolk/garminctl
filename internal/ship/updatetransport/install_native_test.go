package updatetransport

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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
		{"DEB", InstallDEB, "/usr/bin/apt-get", []string{"--yes", "--no-remove", installTestCommand, payload}},
		{"RPM", InstallRPM, "/usr/bin/dnf", []string{"--assumeyes", installTestCommand, payload}},
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

	want := []string{"--yes", "--no-remove", "--allow-downgrades", installTestCommand, filepath.Join(directory, request.Release.Package.Name)}
	if err != nil || got.program != "/usr/bin/apt-get" || !slices.Equal(got.args, want) {
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

func TestInstallOptionsBindSetupPathsToObservedRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	other := t.TempDir()
	valid := InstallOptions{Root: root, Desktop: filepath.Join(root, "garmin-desktop"), CLI: filepath.Join(root, "garminctl")}

	for _, tc := range []struct {
		name   string
		change func(*InstallOptions)
		valid  bool
	}{
		{"observed root", func(*InstallOptions) {}, true},
		{"desktop outside the root", func(o *InstallOptions) { o.Desktop = filepath.Join(other, "garmin-desktop") }, false},
		{"CLI outside the root", func(o *InstallOptions) { o.CLI = filepath.Join(other, "garminctl") }, false},
		{"root is another directory", func(o *InstallOptions) { o.Root = other }, false},
		{"relative root", func(o *InstallOptions) { o.Root = installTestRelative }, false},
		{"missing root", func(o *InstallOptions) { o.Root = "" }, false},
		{"quote", func(o *InstallOptions) { quotedInstallRoot(o, root+`"x`) }, false},
		{"carriage return", func(o *InstallOptions) { quotedInstallRoot(o, root+"\rx") }, false},
		{"line feed", func(o *InstallOptions) { quotedInstallRoot(o, root+"\nx") }, false},
		{"NUL", func(o *InstallOptions) { quotedInstallRoot(o, root+"\x00x") }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			options := valid
			tc.change(&options)

			err := validateInstallOptions(MatchRequest{Installation: InstallSetup}, options)
			if (err == nil) != tc.valid {
				t.Fatalf("setup options %+v: %v", options, err)
			}
		})
	}
}

// quotedInstallRoot keeps both executables inside the altered root, so only its spelling can be refused.
func quotedInstallRoot(options *InstallOptions, root string) {
	options.Root = root
	options.Desktop = filepath.Join(root, "garmin-desktop")
	options.CLI = filepath.Join(root, "garminctl")
}

func TestRestartRefusesExecutablesThatAreNotRegularFiles(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"CLI", "desktop"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			options := InstallOptions{CLI: filepath.Join(root, "garminctl"), Desktop: filepath.Join(root, "garmin-desktop")}

			regular, directory := options.Desktop, options.CLI
			if name == "desktop" {
				regular, directory = options.CLI, options.Desktop
			}

			if err := os.WriteFile(regular, []byte("fixture"), 0o600); err != nil {
				t.Fatal(err)
			}

			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}

			err := restartInstalledDesktop(t.Context(), options, "v1.0.0", root, func(context.Context, string, string) error { return nil })
			if err == nil || err.Error() != "installed executable is not a regular file" {
				t.Fatalf("restart of a directory=%v", err)
			}
		})
	}
}

// The helper never gives a package manager the path of a file its own user can
// still replace: DEB/RPM go through the installed CLI's privileged step.
func TestHandoffInvocationSendsLinuxPackagesThroughThePrivilegedStep(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()

	for _, kind := range []InstallKind{InstallDEB, InstallRPM} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			request := pendingInstallRequest()
			request.Digest = "digest"
			request.Target = MatchRequest{ReleaseTag: installTestTag, OS: runtime.GOOS, Arch: runtime.GOARCH, Installation: kind}
			request.Release = ResolvedRelease{Package: AssetRef{Name: "signed package", Size: 23}}
			request.Install = InstallOptions{CLI: "/usr/bin/garminctl", AllowDowngrade: kind == InstallDEB}

			got, err := handoffInvocation(request, directory)
			if err != nil || got.program != "/usr/bin/pkexec" || !slices.Equal(got.args, []string{"/usr/bin/garminctl", PackageCommand}) {
				t.Fatalf("invocation=%+v %v", got, err)
			}

			var sent packageRequest
			if err := decodeStageJSON(got.input, &sent); err != nil {
				t.Fatal(err)
			}

			want := packageRequest{Protocol: packageProtocol, BuildVersion: request.BuildVersion, KeyID: request.KeyID, Target: request.Target, Directory: directory, Size: 23, Digest: request.Digest, AllowDowngrade: kind == InstallDEB}
			if sent != want {
				t.Fatalf("privileged request=%+v, want %+v", sent, want)
			}
		})
	}

	setup := installRequest{Target: MatchRequest{Installation: InstallSetup}, Release: ResolvedRelease{Package: AssetRef{Name: "setup.exe"}}, Install: InstallOptions{Root: directory}}

	got, err := handoffInvocation(setup, directory)
	if err != nil || got.program != filepath.Join(directory, "setup.exe") || got.input != nil || got.setupRoot != directory {
		t.Fatalf("Setup invocation=%+v %v", got, err)
	}
}

func TestInstallOptionsRequireAbsoluteRestartDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for _, tc := range []struct {
		name, dir string
		valid     bool
	}{
		{"not recorded", "", true},
		{"absolute", root, true},
		{"not absolute", installTestRelative, false},
		{"NUL", root + "\x00", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			options := InstallOptions{Desktop: filepath.Join(root, "garmin-desktop"), CLI: filepath.Join(root, "garminctl"), Dir: tc.dir}
			if err := validateInstallOptions(MatchRequest{Installation: InstallDEB}, options); (err == nil) != tc.valid {
				t.Fatalf("restart directory %q: %v", tc.dir, err)
			}
		})
	}
}
