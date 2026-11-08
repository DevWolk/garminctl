package updatetransport_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

type failedPackageWriter struct{ err error }

func (w failedPackageWriter) Write(data []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}

	return len(data) - 1, nil
}

func TestProviderPreservesDestinationWriteErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		writer failedPackageWriter
		want   error
	}{
		{name: "write error", writer: failedPackageWriter{err: io.ErrClosedPipe}, want: io.ErrClosedPipe},
		{name: "short write", want: io.ErrShortWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			provider, _ := newFixtureProvider(t, signedFixture(t))

			release, err := provider.Check(t.Context(), checkRequest())
			if err != nil {
				t.Fatal(err)
			}

			if err := provider.Download(t.Context(), release, tc.writer, nil); !errors.Is(err, tc.want) {
				t.Fatalf("write error=%v want=%v", err, tc.want)
			}
		})
	}
}

func TestProviderBoundsPackageReadWithoutTrustingContentLength(t *testing.T) {
	t.Parallel()

	fixture := signedFixture(t)
	fixture.record.Package.Size = 4
	body := &observedAssetBody{reader: strings.NewReader(strings.Repeat("a", 1<<20))}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == fixture.record.Signature.URL {
			return assetResponse(fixture.sig), nil
		}

		// A reported length matching metadata must not hide extra stream bytes.
		return &http.Response{StatusCode: http.StatusOK, Body: body, ContentLength: fixture.record.Package.Size}, nil
	})}

	provider, err := updatetransport.NewProvider(client, fixture.request, fixture.record)
	if err != nil {
		t.Fatal(err)
	}

	release, err := provider.Check(t.Context(), checkRequest())
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer

	err = provider.Download(t.Context(), release, &output, nil)
	if err == nil || body.read != 5 || body.closed != 1 || output.Len() > 4 {
		t.Fatalf("error=%v read=%d closed=%d written=%d", err, body.read, body.closed, output.Len())
	}
}
