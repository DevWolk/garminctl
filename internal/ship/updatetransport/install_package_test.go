package updatetransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	packageTestVersion      = "7.8.9"
	packageTestOtherRelease = "another release inside the package"
)

type packageWorkerRun struct {
	invocations []installerInvocation
	installed   []byte
}

func packageWorkerFixture(t *testing.T) (PackageWorkerConfig, packageRequest, []byte) {
	t.Helper()

	config, install := installWorkerFixture(t)
	if install.Target.Installation != InstallDEB {
		t.Skip("the privileged package step installs Linux packages")
	}

	data, err := os.ReadFile(filepath.Join(config.Directory, install.Release.Package.Name))
	if err != nil {
		t.Fatal(err)
	}

	request := packageRequest{Protocol: packageProtocol, BuildVersion: install.BuildVersion, KeyID: install.KeyID, Target: install.Target, Directory: config.Directory, Size: install.Release.Package.Size, Digest: install.Digest}

	return PackageWorkerConfig{BuildVersion: config.BuildVersion, PublicKey: config.PublicKey}, request, data
}

func packageWorkerHost(t *testing.T, packaged string, run *packageWorkerRun) packageHost {
	t.Helper()

	return packageHost{query: func(_ context.Context, program string, _ ...string) (string, error) {
		switch program {
		case debMetadataTool, rpmMetadataTool:
			return packaged, nil
		case debQueryTool:
			return "7.8.8", nil
		default:
			return "", nativeQueryExitError(1)
		}
	}, run: func(invocation installerInvocation) error {
		run.invocations = append(run.invocations, invocation)

		data, err := os.ReadFile(invocation.args[len(invocation.args)-1])
		run.installed = data

		return err
	}}
}

func packageWorkerInput(t *testing.T, request packageRequest) *bytes.Reader {
	t.Helper()

	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}

	return bytes.NewReader(data)
}

func TestPackageWorkerInstallsItsOwnAuthenticatedCopy(t *testing.T) {
	t.Parallel()

	config, request, data := packageWorkerFixture(t)
	run := &packageWorkerRun{}

	if err := runPackageWorker(t.Context(), config, packageWorkerInput(t, request), packageWorkerHost(t, packageTestVersion, run)); err != nil {
		t.Fatal(err)
	}

	if len(run.invocations) != 1 || run.invocations[0].program != debInstaller {
		t.Fatalf("package manager runs=%+v", run.invocations)
	}

	args := run.invocations[0].args
	installed := args[len(args)-1]

	if !slices.Equal(args[:len(args)-1], []string{"--yes", "--no-remove", "install"}) || filepath.Base(installed) != installTestPackage {
		t.Fatalf("package manager arguments=%v", args)
	}

	if strings.HasPrefix(installed, request.Directory) || !bytes.Equal(run.installed, data) {
		t.Fatalf("package manager was given %s instead of a private copy of the verified bytes", installed)
	}

	if _, err := os.Stat(filepath.Dir(installed)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private copy left behind: %v", err)
	}
}

// The directory named by the request stays writable by its sender until the
// privileged step has copied the package, so the copy is what gets authenticated.
func TestPackageWorkerRefusesPackageReplacedAfterTheHelperVerifiedIt(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"other bytes of the same size", "another size", "symbolic link", "signature of other bytes"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			config, request, data := packageWorkerFixture(t)
			target := filepath.Join(request.Directory, installTestPackage)

			switch name {
			case "other bytes of the same size":
				replacePackageFixture(t, target, bytes.Repeat([]byte("X"), len(data)))
			case "another size":
				replacePackageFixture(t, target, []byte("replacement"))
			case "symbolic link":
				other := filepath.Join(t.TempDir(), "other.deb")
				replacePackageFixture(t, other, data)

				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}

				if err := os.Symlink(other, target); err != nil {
					t.Skipf("symbolic links are unavailable: %v", err)
				}
			case "signature of other bytes":
				replacePackageFixture(t, target+".sig", make([]byte, packageSignatureSize))
			}

			run := &packageWorkerRun{}

			err := runPackageWorker(t.Context(), config, packageWorkerInput(t, request), packageWorkerHost(t, packageTestVersion, run))
			if err == nil || len(run.invocations) != 0 {
				t.Fatalf("replaced package reached the package manager: runs=%+v err=%v", run.invocations, err)
			}
		})
	}
}

func replacePackageFixture(t *testing.T, path string, data []byte) {
	t.Helper()

	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	name := filepath.Base(path)
	if err := root.WriteFile(name+".swap", data, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := root.Rename(name+".swap", name); err != nil {
		t.Fatal(err)
	}
}

func TestPackageWorkerRefusesRequestsItWasNotBuiltFor(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		change func(*packageRequest)
	}{
		{"another protocol", func(r *packageRequest) { r.Protocol++ }},
		{"another build", func(r *packageRequest) { r.BuildVersion = "other" }},
		{"another key", func(r *packageRequest) { r.KeyID = strings.Repeat("b", 64) }},
		{"Setup", func(r *packageRequest) { r.Target.Installation = InstallSetup }},
		{"portable", func(r *packageRequest) { r.Target.Installation = InstallPortable }},
		{"another system", func(r *packageRequest) { r.Target.OS = platformWindows }},
		{"RPM downgrade", func(r *packageRequest) { r.Target.Installation = InstallRPM; r.AllowDowngrade = true }},
		{"tag outside the publishing contract", func(r *packageRequest) { r.Target.ReleaseTag = "v7.8.9/../x" }},
		{"digest", func(r *packageRequest) { r.Digest = strings.ToUpper(r.Digest) }},
		{"relative directory", func(r *packageRequest) { r.Directory = installTestRelative }},
		{"unclean directory", func(r *packageRequest) { r.Directory += "/../x" }},
		{"no size", func(r *packageRequest) { r.Size = 0 }},
		{"oversize", func(r *packageRequest) { r.Size = maxPackageBytes + 1 }},
		{packageTestOtherRelease, func(*packageRequest) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			config, request, _ := packageWorkerFixture(t)
			tc.change(&request)

			packaged := packageTestVersion
			if tc.name == packageTestOtherRelease {
				packaged = debTestOlder
			}

			run := &packageWorkerRun{}

			err := runPackageWorker(t.Context(), config, packageWorkerInput(t, request), packageWorkerHost(t, packaged, run))
			if err == nil || len(run.invocations) != 0 {
				t.Fatalf("request reached the package manager: runs=%+v err=%v", run.invocations, err)
			}
		})
	}
}

func TestPackageWorkerBoundsItsRequest(t *testing.T) {
	t.Parallel()

	config, request, _ := packageWorkerFixture(t)
	run := &packageWorkerRun{}
	host := packageWorkerHost(t, packageTestVersion, run)

	for name, input := range map[string]string{
		"oversize":         strings.Repeat(" ", maxStageRequest+1),
		"undeclared field": `{"protocol":1,"publicKey":"chosen"}`,
		"two values":       `{"protocol":1}{"protocol":1}`,
		"empty":            "",
	} {
		if err := runPackageWorker(t.Context(), config, strings.NewReader(input), host); err == nil {
			t.Fatalf("%s request accepted", name)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := runPackageWorker(ctx, config, packageWorkerInput(t, request), host); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled package step=%v", err)
	}

	if len(run.invocations) != 0 {
		t.Fatalf("package manager ran: %+v", run.invocations)
	}
}

func TestPackageWorkerKeepsExplicitDEBRollbackPermission(t *testing.T) {
	t.Parallel()

	config, request, _ := packageWorkerFixture(t)
	request.AllowDowngrade = true
	run := &packageWorkerRun{}

	if err := runPackageWorker(t.Context(), config, packageWorkerInput(t, request), packageWorkerHost(t, packageTestVersion, run)); err != nil {
		t.Fatal(err)
	}

	if len(run.invocations) != 1 || !slices.Contains(run.invocations[0].args, "--allow-downgrades") {
		t.Fatalf("rollback invocation=%+v", run.invocations)
	}
}
