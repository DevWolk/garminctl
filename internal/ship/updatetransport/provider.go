package updatetransport

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

// Provider adapts one selected release to Wails' download/verification flow.
// Application must admit the release; the staging caller must pin PublicKey.
// Provider neither installs a package nor chooses a release or trust root.
type Provider struct {
	client  *http.Client
	request MatchRequest
	record  ResolvedRelease
}

var _ updater.Provider = (*Provider)(nil)

// NewProvider copies the selected metadata and borrows a bounded transport.
// It revalidates references so mutable DTOs cannot introduce arbitrary URLs.
func NewProvider(client *http.Client, request MatchRequest, record ResolvedRelease) (*Provider, error) {
	if err := validateResolved(request, record); err != nil {
		return nil, err
	}

	return &Provider{client: assetHTTPClient(client), request: request, record: record}, nil
}

// Name identifies this selected-release source to the Wails updater.
func (p *Provider) Name() string { return "garminctl-selected-release" }

// Check fetches the exact sibling signature, without a second release lookup.
// Its result is unverified until Wails authenticates the downloaded bytes.
func (p *Provider) Check(ctx context.Context, request updater.CheckRequest) (*updater.Release, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if p == nil || p.client == nil {
		return nil, errors.New("release provider is not configured")
	}

	if request.Platform != p.request.OS || request.Arch != p.request.Arch {
		return nil, errors.New("updater platform does not match the selected package")
	}

	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	signature, err := p.readSignature(ctx)
	if err != nil {
		return nil, err
	}

	return &updater.Release{
		Version: strings.TrimPrefix(p.record.Release.Tag, "v"), Name: p.record.Release.Name,
		Notes: p.record.Release.Notes, PublishedAt: p.record.Release.PublishedAt,
		Artifact: p.artifact(),
		Verification: &updater.Verification{
			DigestAlgo: "sha256", SignatureAlgo: "ed25519", Signature: signature,
		},
	}, nil
}

// Download streams only the owned package URL, enforcing its declared size.
// Success means an exact-size stream, not an authenticated or installed file.
func (p *Provider) Download(ctx context.Context, release *updater.Release, dst io.Writer, progress func(int64, int64)) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if p == nil || p.client == nil {
		return errors.New("release provider is not configured")
	}

	if err := p.validateDownload(release); err != nil {
		return err
	}

	if dst == nil {
		return errors.New("package destination is required")
	}

	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	body, err := p.openAsset(ctx, p.record.Package)
	if err != nil {
		return err
	}
	defer body.Close()

	return copyPackage(ctx, dst, body, p.record.Package.Size, progress)
}

func (p *Provider) artifact() updater.Artifact {
	return updater.Artifact{
		Filename: p.record.Package.Name, Size: p.record.Package.Size,
		Platform: p.request.OS, Arch: p.request.Arch,
	}
}

func (p *Provider) validateDownload(release *updater.Release) error {
	if release == nil || release.Version != strings.TrimPrefix(p.record.Release.Tag, "v") || release.Artifact != p.artifact() {
		return errors.New("download does not match the selected release")
	}

	v := release.Verification
	if v == nil || v.DigestAlgo != "sha256" || v.SignatureAlgo != "ed25519" || len(v.Signature) != ed25519.SignatureSize || len(v.Digest) != 0 {
		return errors.New("download requires the selected raw signature contract")
	}

	return nil
}

func validateResolved(request MatchRequest, record ResolvedRelease) error {
	if record.ReleaseID <= 0 || record.Release.Tag != request.ReleaseTag {
		return errors.New("selected release identity does not match the package request")
	}

	for _, ref := range []AssetRef{record.Package, record.Signature} {
		if err := validateAssetRef(ref); err != nil {
			return err
		}
	}

	if record.Package.ID == record.Signature.ID {
		return errors.New("package and signature must have distinct asset identities")
	}

	match, err := MatchAssets(request, []Asset{
		{Name: record.Package.Name, Size: record.Package.Size},
		{Name: record.Signature.Name, Size: record.Signature.Size},
	})
	if err != nil {
		return err
	}

	if match.PackageIndex != 0 || match.SignatureIndex != 1 {
		return errors.New("package and signature references are reversed")
	}

	return nil
}
