package updatetransport_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

const selectedVersion = "1.2.0"

type providerFixture struct {
	request updatetransport.MatchRequest
	record  updatetransport.ResolvedRelease
	data    []byte
	sig     []byte
	key     []byte
}

func signedFixture(t *testing.T) providerFixture {
	t.Helper()

	data := []byte("synthetic package bytes; never executable")

	signature, err := stageTestSign(data)
	if err != nil {
		t.Fatal(err)
	}

	pair := resolvePair()
	pair[0].Size = int64(len(data))

	return providerFixture{
		request: setupRequest(), data: data, sig: signature, key: stageTestPublicKey(),
		record: updatetransport.ResolvedRelease{
			ReleaseID: 41, Package: pair[0], Signature: pair[1],
			Release: updatetransport.Release{
				Tag: tag120, Name: selectedName, Notes: "plain notes",
				PublishedAt: time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC),
			},
		},
	}
}

func checkRequest() updater.CheckRequest {
	return updater.CheckRequest{CurrentVersion: "1.0.0", Platform: windowsOS, Arch: amd64Arch}
}

func assetResponse(data []byte) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)),
	}
}

func assetClient(t *testing.T, fixture providerFixture, calls *[]string) *http.Client {
	t.Helper()

	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		*calls = append(*calls, r.URL.String())
		if r.Method != http.MethodGet || r.Header.Get("Accept") != "application/octet-stream" ||
			r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" ||
			r.Header.Get("User-Agent") != "garminctl" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("unexpected byte request: %s %s headers=%v", r.Method, r.URL, r.Header)
		}

		switch r.URL.String() {
		case fixture.record.Signature.URL:
			return assetResponse(fixture.sig), nil
		case fixture.record.Package.URL:
			return assetResponse(fixture.data), nil
		default:
			t.Errorf("unexpected byte endpoint: %s", r.URL)
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
		}
	})}
}

func newFixtureProvider(t *testing.T, fixture providerFixture) (*updatetransport.Provider, *[]string) {
	t.Helper()

	calls := []string{}

	provider, err := updatetransport.NewProvider(assetClient(t, fixture, &calls), fixture.request, fixture.record, fixture.key)
	if err != nil {
		t.Fatal(err)
	}

	return provider, &calls
}

func TestProviderCopiesSelectedReleaseAndRawSiblingSignature(t *testing.T) {
	t.Parallel()

	fixture := signedFixture(t)
	provider, calls := newFixtureProvider(t, fixture)
	// Later caller edits cannot change the provider's owned selection.
	fixture.record.Package.URL = "https://untrusted.invalid/package"
	fixture.record.Release.Name = callerEdit

	got, err := provider.Check(t.Context(), checkRequest())
	if err != nil {
		t.Fatal(err)
	}

	want := &updater.Release{
		Version: selectedVersion, Name: selectedName, Notes: "plain notes",
		PublishedAt:  fixture.record.Release.PublishedAt,
		Artifact:     updater.Artifact{Filename: setup120, Size: int64(len(fixture.data)), Platform: windowsOS, Arch: amd64Arch},
		Verification: &updater.Verification{DigestAlgo: "sha256"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected Wails metadata: %+v verification=%+v", got, got.Verification)
	}

	got.Verification.Digest = []byte("caller edit")
	got.Name = callerEdit

	again, err := provider.Check(t.Context(), checkRequest())
	if err != nil || again == nil || again.Name != selectedName || len(again.Verification.Digest) != 0 || len(*calls) != 2 {
		t.Fatalf("second check=%+v error=%v calls=%v", again, err, *calls)
	}
}

func TestProviderDownloadsOnlySelectedAssetAndCountsProgress(t *testing.T) {
	t.Parallel()

	fixture := signedFixture(t)
	provider, calls := newFixtureProvider(t, fixture)

	release, err := provider.Check(t.Context(), checkRequest())
	if err != nil {
		t.Fatal(err)
	}

	// Download must not use caller-controlled URL metadata.
	release.Metadata = map[string]any{"github.asset.url": "https://untrusted.invalid/package"}

	var (
		output   bytes.Buffer
		progress [][2]int64
	)

	err = provider.Download(t.Context(), release, &output, func(written, total int64) {
		progress = append(progress, [2]int64{written, total})
	})

	wantProgress := [][2]int64{{int64(len(fixture.data)), int64(len(fixture.data))}}
	if err != nil || !bytes.Equal(output.Bytes(), fixture.data) || !reflect.DeepEqual(progress, wantProgress) ||
		!reflect.DeepEqual(*calls, []string{fixture.record.Signature.URL, fixture.record.Package.URL}) {
		t.Fatalf("download error=%v bytes=%q progress=%v calls=%v", err, output.Bytes(), progress, *calls)
	}
}

func TestProviderRejectsActualSizeMismatch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		delta int64
	}{{name: "short", delta: 1}, {name: "overlong", delta: -1}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fixture := signedFixture(t)
			fixture.record.Package.Size += tc.delta
			provider, _ := newFixtureProvider(t, fixture)

			release, err := provider.Check(t.Context(), checkRequest())
			if err != nil {
				t.Fatal(err)
			}

			var output bytes.Buffer

			err = provider.Download(t.Context(), release, &output, nil)
			if err == nil || int64(output.Len()) > fixture.record.Package.Size {
				t.Fatalf("size mismatch accepted: error=%v written=%d expected=%d", err, output.Len(), fixture.record.Package.Size)
			}
		})
	}
}

func TestProviderCancellationBeforeByteRequests(t *testing.T) {
	t.Parallel()

	fixture := signedFixture(t)
	provider, calls := newFixtureProvider(t, fixture)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	got, err := provider.Check(ctx, checkRequest())
	if got != nil || err == nil || len(*calls) != 0 {
		t.Fatalf("canceled check=%+v error=%v calls=%v", got, err, *calls)
	}
}
