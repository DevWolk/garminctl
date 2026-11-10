package updatetransport_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

func TestNewProviderRevalidatesSelectedMetadataWithoutHTTP(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		edit func(*providerFixture)
	}{
		{name: "release id", edit: func(f *providerFixture) { f.record.ReleaseID = 0 }},
		{name: "changed tag", edit: func(f *providerFixture) { f.record.Release.Tag = "v9.0.0" }},
		{name: "unsupported request", edit: func(f *providerFixture) { f.request.Arch = arm64Arch }},
		{name: "foreign package url", edit: func(f *providerFixture) { f.record.Package.URL = "https://other.invalid/package" }},
		{name: "foreign signature url", edit: func(f *providerFixture) { f.record.Signature.URL = "https://other.invalid/signature" }},
		{name: "duplicate id", edit: func(f *providerFixture) {
			f.record.Signature.ID = f.record.Package.ID
			f.record.Signature.URL = f.record.Package.URL
		}},
		{name: "reversed refs", edit: func(f *providerFixture) { f.record.Package, f.record.Signature = f.record.Signature, f.record.Package }},
		{name: "package name", edit: func(f *providerFixture) { f.record.Package.Name = zip120 }},
		{name: "signature name", edit: func(f *providerFixture) { f.record.Signature.Name = setup120 + ".other.sig" }},
		{name: "package size", edit: func(f *providerFixture) { f.record.Package.Size = 0 }},
		{name: "signature size", edit: func(f *providerFixture) { f.record.Signature.Size = 63 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fixture := signedFixture(t)
			tc.edit(&fixture)

			calls := []string{}

			got, err := updatetransport.NewProvider(assetClient(t, fixture, &calls), fixture.request, fixture.record, fixture.key)
			if got != nil || err == nil || len(calls) != 0 {
				t.Fatalf("provider=%+v error=%v calls=%v", got, err, calls)
			}
		})
	}
}

func TestProviderRejectsMismatchedPlatformBeforeHTTP(t *testing.T) {
	t.Parallel()

	for _, request := range []updater.CheckRequest{
		{}, {Platform: linuxOS, Arch: amd64Arch}, {Platform: windowsOS, Arch: arm64Arch},
	} {
		t.Run(request.Platform+"/"+request.Arch, func(t *testing.T) {
			t.Parallel()

			provider, calls := newFixtureProvider(t, signedFixture(t))
			if got, err := provider.Check(t.Context(), request); got != nil || err == nil || len(*calls) != 0 {
				t.Fatalf("result=%+v error=%v calls=%v", got, err, *calls)
			}
		})
	}
}

func TestProviderRejectsChangedDownloadContractBeforeHTTP(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		edit func(*updater.Release)
	}{
		{name: "version", edit: func(r *updater.Release) { r.Version = "9.0.0" }},
		{name: "filename", edit: func(r *updater.Release) { r.Artifact.Filename = zip120 }},
		{name: "size", edit: func(r *updater.Release) { r.Artifact.Size++ }},
		{name: "platform", edit: func(r *updater.Release) { r.Artifact.Platform = linuxOS }},
		{name: "architecture", edit: func(r *updater.Release) { r.Artifact.Arch = arm64Arch }},
		{name: "no verification", edit: func(r *updater.Release) { r.Verification = nil }},
		{name: "digest algorithm", edit: func(r *updater.Release) { r.Verification.DigestAlgo = "sha512" }},
		{name: "signature algorithm", edit: func(r *updater.Release) { r.Verification.SignatureAlgo = "ed25519" }},
		{name: "caller signature", edit: func(r *updater.Release) { r.Verification.Signature = []byte("caller signature") }},
		{name: "caller digest", edit: func(r *updater.Release) { r.Verification.Digest = []byte("caller digest") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			provider, calls := newFixtureProvider(t, signedFixture(t))

			release, err := provider.Check(t.Context(), checkRequest())
			if err != nil {
				t.Fatal(err)
			}

			tc.edit(release)

			if err := provider.Download(t.Context(), release, io.Discard, nil); err == nil || len(*calls) != 1 {
				t.Fatalf("download error=%v calls=%v", err, *calls)
			}
		})
	}
}

func TestProviderRejectsMissingOrOverlongSignatureBodies(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, 64, rawSignatureSize - 1, rawSignatureSize + 1, 1 << 20} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			t.Parallel()

			fixture := signedFixture(t)
			fixture.sig = bytes.Repeat([]byte{1}, size)

			provider, calls := newFixtureProvider(t, fixture)
			if got, err := provider.Check(t.Context(), checkRequest()); got != nil || err == nil || len(*calls) != 1 {
				t.Fatalf("result=%+v error=%v calls=%v", got, err, *calls)
			}
		})
	}
}

func TestProviderPreservesSignatureReadAndHTTPFailures(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusPartialContent, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			fixture := signedFixture(t)
			body := &failingBody{err: io.ErrUnexpectedEOF}
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Body: body}, nil
			})}

			provider, err := updatetransport.NewProvider(client, fixture.request, fixture.record, fixture.key)
			if err != nil {
				t.Fatal(err)
			}

			got, err := provider.Check(t.Context(), checkRequest())

			var failure *updatetransport.StatusError
			if got != nil || !errors.As(err, &failure) || failure.StatusCode != status || body.closed.Load() != 1 {
				t.Fatalf("result=%+v error=%v closed=%d", got, err, body.closed.Load())
			}
		})
	}
}

func TestProviderZeroAndNilDownload(t *testing.T) {
	t.Parallel()

	zero := &updatetransport.Provider{}
	if got, err := zero.Check(t.Context(), checkRequest()); got != nil || err == nil {
		t.Fatalf("zero check=%+v error=%v", got, err)
	}

	if err := zero.Download(t.Context(), nil, io.Discard, nil); err == nil {
		t.Fatal("zero provider accepted a download")
	}

	provider, calls := newFixtureProvider(t, signedFixture(t))
	if err := provider.Download(t.Context(), nil, io.Discard, nil); err == nil || len(*calls) != 0 {
		t.Fatalf("nil download error=%v calls=%v", err, *calls)
	}
}

func TestProviderRejectsUnconfiguredKeyBeforeHTTP(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		key  []byte
	}{
		{name: "missing"},
		{name: "legacy raw key", key: bytes.Repeat([]byte{3}, 32)},
		{name: "malformed SPKI", key: []byte("untrusted key")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := signedFixture(t)
			calls := []string{}

			provider, err := updatetransport.NewProvider(assetClient(t, fixture, &calls), fixture.request, fixture.record, tc.key)
			if err == nil || provider != nil || len(calls) != 0 {
				t.Fatalf("provider=%+v error=%v calls=%v", provider, err, calls)
			}
		})
	}
}

func TestProviderOwnsKeyAndRejectsFeedKeySubstitution(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"caller key mutation", "feed key substitution"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := signedFixture(t)

			key := bytes.Clone(fixture.key)
			if name == "feed key substitution" {
				key = otherStagePublicKey(t)
			}

			calls := []string{}

			provider, err := updatetransport.NewProvider(assetClient(t, fixture, &calls), fixture.request, fixture.record, key)
			if err != nil {
				t.Fatal(err)
			}

			key[0] ^= 1

			release, err := provider.Check(t.Context(), checkRequest())
			if err != nil {
				t.Fatal(err)
			}

			release.Metadata = map[string]any{"publicKey": bytes.Clone(fixture.key), "signatureAlgo": "none"}

			err = provider.Download(t.Context(), release, io.Discard, nil)
			if (err == nil) != (name == "caller key mutation") {
				t.Fatalf("download error=%v", err)
			}
		})
	}
}
