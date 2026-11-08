package updatetransport

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const maxPackageBytes = 512 << 20

// Match the publishing workflow's admitted spelling, without imposing channel
// or semantic-version precedence policy on a selected tag.
var publisherVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

// InstallKind is the locally determined installation type, not a user URL.
type InstallKind string

// Installation kinds correspond to the artifacts built by the package pipeline.
const (
	InstallSetup    InstallKind = "setup"
	InstallDEB      InstallKind = "deb"
	InstallRPM      InstallKind = "rpm"
	InstallPortable InstallKind = "portable"
)

// Asset is the metadata required for matching within one selected release.
type Asset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// MatchRequest names an already selected release tag and the installation.
// Semantic version and channel policy belong to Application, not the matcher.
type MatchRequest struct {
	ReleaseTag   string
	OS           string
	Arch         string
	Installation InstallKind
}

// AssetMatch contains indexes into the supplied, unchanged asset snapshot.
// It is metadata only; size claims and signature bytes remain unverified.
type AssetMatch struct {
	PackageIndex   int
	SignatureIndex int
}

// MatchAssets requires one exact package and its exact raw .sig sibling.
// It performs no I/O and returns nil on any selection or metadata error.
func MatchAssets(request MatchRequest, assets []Asset) (*AssetMatch, error) {
	filename, err := packageFilename(request)
	if err != nil {
		return nil, err
	}

	payload, err := uniqueAsset(assets, filename)
	if err != nil {
		return nil, err
	}

	signature, err := uniqueAsset(assets, filename+".sig")
	if err != nil {
		return nil, err
	}

	if assets[payload].Size <= 0 || assets[payload].Size > maxPackageBytes {
		return nil, errors.New("release package size must be positive and at most 512 MiB")
	}

	if assets[signature].Size != ed25519.SignatureSize {
		return nil, errors.New("release signature size must be exactly 64 bytes")
	}

	return &AssetMatch{PackageIndex: payload, SignatureIndex: signature}, nil
}

func packageFilename(request MatchRequest) (string, error) {
	if !publisherVersion.MatchString(request.ReleaseTag) {
		return "", errors.New("release tag is outside the publishing filename contract")
	}

	if request.OS != "windows" && request.OS != "linux" {
		return "", errors.New("unsupported package operating system")
	}

	if request.Arch != "amd64" && request.Arch != "arm64" {
		return "", errors.New("unsupported package architecture")
	}

	version := strings.TrimPrefix(request.ReleaseTag, "v")
	switch request.Installation {
	case InstallSetup:
		if request.OS != "windows" || request.Arch != "amd64" {
			return "", errors.New("setup requires windows amd64")
		}

		return "garminctl_" + request.ReleaseTag + "_windows_amd64_setup.exe", nil
	case InstallDEB, InstallRPM:
		if request.OS != "linux" || request.Arch != "amd64" {
			return "", errors.New("native linux package requires amd64")
		}

		return "garminctl_" + strings.ReplaceAll(version, "-", ".") + "_linux_amd64." + string(request.Installation), nil
	case InstallPortable:
		extension := ".tar.gz"
		if request.OS == "windows" {
			extension = ".zip"
		}

		return fmt.Sprintf("garminctl_%s_%s_%s%s", version, request.OS, request.Arch, extension), nil
	default:
		return "", errors.New("unsupported installation kind")
	}
}

func uniqueAsset(assets []Asset, name string) (int, error) {
	found := -1

	for i, asset := range assets {
		if asset.Name != name {
			continue
		}

		if found >= 0 {
			return -1, fmt.Errorf("ambiguous release asset %q", name)
		}

		found = i
	}

	if found < 0 {
		return -1, fmt.Errorf("release asset %q not found", name)
	}

	return found, nil
}
