package updatetransport_test

import (
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

func TestMatchAssetsRejectsUnsupportedRequestsEvenWithMatchingNames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, tag, platform, arch, filename string
		kind                                updatetransport.InstallKind
	}{
		{name: "empty tag", platform: windowsOS, arch: amd64Arch, kind: updatetransport.InstallSetup, filename: "garminctl__windows_amd64_setup.exe"},
		{name: "short tag", tag: "v1.2", platform: windowsOS, arch: amd64Arch, kind: updatetransport.InstallSetup, filename: "garminctl_v1.2_windows_amd64_setup.exe"},
		{name: "tag path", tag: "../1.2.0", platform: windowsOS, arch: amd64Arch, kind: updatetransport.InstallSetup, filename: "garminctl_../1.2.0_windows_amd64_setup.exe"},
		{name: "tag whitespace", tag: "v1.2.0 ", platform: windowsOS, arch: amd64Arch, kind: updatetransport.InstallSetup, filename: "garminctl_v1.2.0 _windows_amd64_setup.exe"},
		{name: "build metadata outside publisher contract", tag: "v1.2.0+build.1", platform: windowsOS, arch: amd64Arch, kind: updatetransport.InstallSetup, filename: "garminctl_v1.2.0+build.1_windows_amd64_setup.exe"},
		{name: "empty mode", tag: tag120, platform: windowsOS, arch: amd64Arch, filename: setup120},
		{name: "unknown mode", tag: tag120, platform: windowsOS, arch: amd64Arch, kind: "unknown", filename: setup120},
		{name: "setup on linux", tag: tag120, platform: linuxOS, arch: amd64Arch, kind: updatetransport.InstallSetup, filename: "garminctl_v1.2.0_linux_amd64_setup.exe"},
		{name: "setup arm64", tag: tag120, platform: windowsOS, arch: arm64Arch, kind: updatetransport.InstallSetup, filename: "garminctl_v1.2.0_windows_arm64_setup.exe"},
		{name: "deb on windows", tag: tag120, platform: windowsOS, arch: amd64Arch, kind: updatetransport.InstallDEB, filename: "garminctl_1.2.0_windows_amd64.deb"},
		{name: "deb arm64", tag: tag120, platform: linuxOS, arch: arm64Arch, kind: updatetransport.InstallDEB, filename: "garminctl_1.2.0_linux_arm64.deb"},
		{name: "rpm on windows", tag: tag120, platform: windowsOS, arch: amd64Arch, kind: updatetransport.InstallRPM, filename: "garminctl_1.2.0_windows_amd64.rpm"},
		{name: "rpm arm64", tag: tag120, platform: linuxOS, arch: arm64Arch, kind: updatetransport.InstallRPM, filename: "garminctl_1.2.0_linux_arm64.rpm"},
		{name: "darwin portable", tag: tag120, platform: "darwin", arch: amd64Arch, kind: updatetransport.InstallPortable, filename: "garminctl_1.2.0_darwin_amd64.tar.gz"},
		{name: "386 portable", tag: tag120, platform: linuxOS, arch: "386", kind: updatetransport.InstallPortable, filename: "garminctl_1.2.0_linux_386.tar.gz"},
		{name: "upper case os", tag: tag120, platform: "Windows", arch: amd64Arch, kind: updatetransport.InstallPortable, filename: "garminctl_1.2.0_Windows_amd64.zip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			input := []updatetransport.Asset{{Name: tc.filename, Size: 1}, {Name: tc.filename + ".sig", Size: 64}}

			match, err := updatetransport.MatchAssets(updatetransport.MatchRequest{
				ReleaseTag: tc.tag, OS: tc.platform, Arch: tc.arch, Installation: tc.kind,
			}, input)
			if err == nil || match != nil {
				t.Fatalf("match=%+v error=%v", match, err)
			}
		})
	}
}

func TestMatchAssetsRequiresNormalizedLinuxFilename(t *testing.T) {
	t.Parallel()

	input := []updatetransport.Asset{
		{Name: "garminctl_1.2.0-rc.1_linux_amd64.deb", Size: 1},
		{Name: "garminctl_1.2.0-rc.1_linux_amd64.deb.sig", Size: 64},
	}

	match, err := updatetransport.MatchAssets(updatetransport.MatchRequest{
		ReleaseTag: "v1.2.0-rc.1", OS: linuxOS, Arch: amd64Arch, Installation: updatetransport.InstallDEB,
	}, input)
	if err == nil || match != nil {
		t.Fatalf("unpublished filename accepted: match=%+v error=%v", match, err)
	}
}
