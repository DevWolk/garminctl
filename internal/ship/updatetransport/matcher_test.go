package updatetransport_test

import (
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

const (
	windowsOS        = "windows"
	linuxOS          = "linux"
	amd64Arch        = "amd64"
	arm64Arch        = "arm64"
	tag120           = "v1.2.0"
	setup120         = "garminctl_v1.2.0_windows_amd64_setup.exe"
	zip120           = "garminctl_1.2.0_windows_amd64.zip"
	maxPackageSize   = 512 << 20
	rawSignatureSize = 384
)

func TestMatchAssetsRSAContractRejectsLegacySignatures(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		size  int64
		valid bool
	}{
		{name: "RSA-3072", size: rawSignatureSize, valid: true},
		{name: "legacy Ed25519", size: 64},
		{name: "short RSA", size: rawSignatureSize - 1},
		{name: "long RSA", size: rawSignatureSize + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assets := []updatetransport.Asset{{Name: setup120, Size: 2}, {Name: setup120 + ".sig", Size: tc.size}}

			got, err := updatetransport.MatchAssets(setupRequest(), assets)
			if (err == nil) != tc.valid || (got != nil) != tc.valid {
				t.Fatalf("signature size %d: match=%+v error=%v", tc.size, got, err)
			}
		})
	}
}

func TestMatchAssetsPackagingMatrix(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, tag, platform, arch, filename string
		kind                                updatetransport.InstallKind
	}{
		{name: "windows setup keeps tag", tag: tag120, platform: windowsOS, arch: amd64Arch, kind: updatetransport.InstallSetup, filename: setup120},
		{name: "windows setup unprefixed tag", tag: "1.2.0", platform: windowsOS, arch: amd64Arch, kind: updatetransport.InstallSetup, filename: "garminctl_1.2.0_windows_amd64_setup.exe"},
		{name: "deb", tag: tag120, platform: linuxOS, arch: amd64Arch, kind: updatetransport.InstallDEB, filename: "garminctl_1.2.0_linux_amd64.deb"},
		{name: "rpm", tag: tag120, platform: linuxOS, arch: amd64Arch, kind: updatetransport.InstallRPM, filename: "garminctl_1.2.0_linux_amd64.rpm"},
		{name: "deb prerelease normalization", tag: "v1.2.0-rc.1", platform: linuxOS, arch: amd64Arch, kind: updatetransport.InstallDEB, filename: "garminctl_1.2.0.rc.1_linux_amd64.deb"},
		{name: "rpm prerelease normalization", tag: "v1.2.0-beta-2", platform: linuxOS, arch: amd64Arch, kind: updatetransport.InstallRPM, filename: "garminctl_1.2.0.beta.2_linux_amd64.rpm"},
		{name: "windows portable amd64", tag: tag120, platform: windowsOS, arch: amd64Arch, kind: updatetransport.InstallPortable, filename: zip120},
		{name: "windows portable arm64", tag: tag120, platform: windowsOS, arch: arm64Arch, kind: updatetransport.InstallPortable, filename: "garminctl_1.2.0_windows_arm64.zip"},
		{name: "linux portable amd64", tag: tag120, platform: linuxOS, arch: amd64Arch, kind: updatetransport.InstallPortable, filename: "garminctl_1.2.0_linux_amd64.tar.gz"},
		{name: "linux portable arm64", tag: tag120, platform: linuxOS, arch: arm64Arch, kind: updatetransport.InstallPortable, filename: "garminctl_1.2.0_linux_arm64.tar.gz"},
		{name: "portable prerelease keeps hyphen", tag: "v1.2.0-rc.1", platform: linuxOS, arch: amd64Arch, kind: updatetransport.InstallPortable, filename: "garminctl_1.2.0-rc.1_linux_amd64.tar.gz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assets := []updatetransport.Asset{
				{Name: "checksums.txt", Size: 200},
				{Name: tc.filename + ".sig", Size: rawSignatureSize},
				{Name: tc.filename, Size: 1234},
				{Name: "windows-signing-certificate.pem", Size: 400},
			}
			before := slices.Clone(assets)

			match, err := updatetransport.MatchAssets(updatetransport.MatchRequest{
				ReleaseTag: tc.tag, OS: tc.platform, Arch: tc.arch, Installation: tc.kind,
			}, assets)
			if err != nil {
				t.Fatal(err)
			}

			if match == nil || match.PackageIndex != 2 || match.SignatureIndex != 1 || !reflect.DeepEqual(assets, before) {
				t.Fatalf("match=%+v changed assets=%t", match, !reflect.DeepEqual(assets, before))
			}
		})
	}
}

func setupRequest() updatetransport.MatchRequest {
	return updatetransport.MatchRequest{ReleaseTag: tag120, OS: windowsOS, Arch: amd64Arch, Installation: updatetransport.InstallSetup}
}

func assetPair(name string) []updatetransport.Asset {
	return []updatetransport.Asset{{Name: name, Size: 2}, {Name: name + ".sig", Size: rawSignatureSize}}
}

func TestMatchAssetsDoesNotDependOnOrderOrChoosePortableForSetup(t *testing.T) {
	t.Parallel()

	assets := []updatetransport.Asset{
		{Name: zip120, Size: 1},
		{Name: zip120 + ".sig", Size: rawSignatureSize},
		{Name: setup120, Size: 3},
		{Name: setup120 + ".sig", Size: rawSignatureSize},
	}

	for _, reverse := range []struct {
		name string
		flip bool
	}{{name: "portable first"}, {name: "setup first", flip: true}} {
		t.Run(reverse.name, func(t *testing.T) {
			t.Parallel()

			input := slices.Clone(assets)
			if reverse.flip {
				slices.Reverse(input)
			}

			match, err := updatetransport.MatchAssets(setupRequest(), input)
			if err != nil {
				t.Fatal(err)
			}

			if match == nil || input[match.PackageIndex].Name != setup120 || input[match.SignatureIndex].Name != setup120+".sig" {
				t.Fatalf("match=%+v", match)
			}
		})
	}
}

func TestMatchAssetsRejectsMissingAmbiguousOrWrongNames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		assets []updatetransport.Asset
	}{
		{name: "empty"},
		{name: "only portable", assets: []updatetransport.Asset{{Name: zip120, Size: 2}, {Name: zip120 + ".sig", Size: rawSignatureSize}}},
		{name: "missing signature", assets: []updatetransport.Asset{{Name: setup120, Size: 2}}},
		{name: "wrong signature", assets: []updatetransport.Asset{{Name: setup120, Size: 2}, {Name: zip120 + ".sig", Size: rawSignatureSize}}},
		{name: "duplicate package", assets: []updatetransport.Asset{{Name: setup120, Size: 2}, {Name: setup120, Size: 3}, {Name: setup120 + ".sig", Size: rawSignatureSize}}},
		{name: "duplicate signature", assets: []updatetransport.Asset{{Name: setup120, Size: 2}, {Name: setup120 + ".sig", Size: rawSignatureSize}, {Name: setup120 + ".sig", Size: rawSignatureSize}}},
		{name: "wrong version", assets: assetPair("garminctl_v1.3.0_windows_amd64_setup.exe")},
		{name: "unprefixed setup for prefixed tag", assets: assetPair("garminctl_1.2.0_windows_amd64_setup.exe")},
		{name: "path suffix", assets: assetPair("../" + setup120)},
		{name: "filename suffix", assets: assetPair(setup120 + ".old")},
		{name: "uppercase filename", assets: assetPair("GARMINCTL_v1.2.0_windows_amd64_setup.exe")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			match, err := updatetransport.MatchAssets(setupRequest(), tc.assets)
			if match != nil || err == nil {
				t.Fatalf("match=%+v error=%v", match, err)
			}
		})
	}
}

func TestMatchAssetsSizeBounds(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name               string
		payload, signature int64
		valid              bool
	}{
		{name: "one byte", payload: 1, signature: rawSignatureSize, valid: true},
		{name: "maximum", payload: maxPackageSize, signature: rawSignatureSize, valid: true},
		{name: "empty package", signature: rawSignatureSize},
		{name: "negative package", payload: -1, signature: rawSignatureSize},
		{name: "overlong package", payload: maxPackageSize + 1, signature: rawSignatureSize},
		{name: "overflow sized package", payload: math.MaxInt64, signature: rawSignatureSize},
		{name: "unknown signature", payload: 1},
		{name: "negative signature", payload: 1, signature: -1},
		{name: "short signature", payload: 1, signature: 63},
		{name: "overlong signature", payload: 1, signature: 65},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			input := []updatetransport.Asset{{Name: setup120, Size: tc.payload}, {Name: setup120 + ".sig", Size: tc.signature}}

			match, err := updatetransport.MatchAssets(setupRequest(), input)
			if (err == nil) != tc.valid || (match != nil) != tc.valid {
				t.Fatalf("match=%+v error=%v valid=%t", match, err, tc.valid)
			}
		})
	}
}
