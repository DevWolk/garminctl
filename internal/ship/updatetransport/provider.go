package updatetransport

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

// Provider authenticates one selected package before Wails can complete staging.
// Application admits the release; composition supplies the compiled public key.
// Provider neither installs a package nor chooses a release or trust root.
type Provider struct {
	client    *http.Client
	request   MatchRequest
	record    ResolvedRelease
	key       *rsa.PublicKey
	mu        sync.Mutex
	signature []byte
}

var _ updater.Provider = (*Provider)(nil)

// NewProvider copies the selected metadata and borrows a bounded transport.
// It revalidates references so mutable DTOs cannot introduce arbitrary URLs.
func NewProvider(client *http.Client, request MatchRequest, record ResolvedRelease, publicKey []byte) (*Provider, error) {
	key, err := parsePublicKey(publicKey)
	if err != nil {
		return nil, err
	}

	if err := validateResolved(request, record); err != nil {
		return nil, err
	}

	return &Provider{client: assetHTTPClient(client), request: request, record: record, key: key}, nil
}

// Name identifies this selected-release source to the Wails updater.
func (p *Provider) Name() string { return "garminctl-selected-release" }

// Check fetches the exact sibling signature, without a second release lookup.
// Its result is metadata only; Download authenticates bytes against the pin.
func (p *Provider) Check(ctx context.Context, request updater.CheckRequest) (*updater.Release, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if p == nil || p.client == nil || p.key == nil {
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

	p.mu.Lock()
	p.signature = bytes.Clone(signature)
	p.mu.Unlock()

	return &updater.Release{
		Version: strings.TrimPrefix(p.record.Release.Tag, "v"), Name: p.record.Release.Name,
		Notes: p.record.Release.Notes, PublishedAt: p.record.Release.PublishedAt,
		Artifact:     p.artifact(),
		Verification: &updater.Verification{DigestAlgo: "sha256"},
	}, nil
}

// Download streams only the owned package URL, enforcing its declared size.
// Success requires the exact size and a valid RSA signature; it does not install.
func (p *Provider) Download(ctx context.Context, release *updater.Release, dst io.Writer, progress func(int64, int64)) error {
	_, err := p.download(ctx, release, dst, progress)
	return err
}

func (p *Provider) download(ctx context.Context, release *updater.Release, dst io.Writer, progress func(int64, int64)) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	signature, err := p.downloadSignature(release, dst)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	body, err := p.openAsset(ctx, p.record.Package)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	digest := sha256.New()
	if err := copyPackage(ctx, io.MultiWriter(dst, digest), body, p.record.Package.Size, progress); err != nil {
		return nil, err
	}

	if err := verifyPackageSignature(ctx, p.key, digest.Sum(nil), signature); err != nil {
		return nil, err
	}

	return signature, nil
}

func (p *Provider) downloadSignature(release *updater.Release, dst io.Writer) ([]byte, error) {
	if p == nil || p.client == nil || p.key == nil {
		return nil, errors.New("release provider is not configured")
	}

	if err := p.validateDownload(release); err != nil {
		return nil, err
	}

	if dst == nil {
		return nil, errors.New("package destination is required")
	}

	p.mu.Lock()
	signature := bytes.Clone(p.signature)
	p.mu.Unlock()

	if len(signature) != packageSignatureSize {
		return nil, errors.New("download requires a checked package signature")
	}

	return signature, nil
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
	if v == nil || v.DigestAlgo != "sha256" || v.SignatureAlgo != "" || len(v.Signature) != 0 || len(v.Digest) != 0 {
		return errors.New("download requires the selected RSA provider contract")
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
