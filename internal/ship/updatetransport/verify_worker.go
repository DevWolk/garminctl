package updatetransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
)

const verifyInput = "input"

// RunVerifyWorker uses the same Wails flow with a local-only asset transport.
// The compiled key and marked operation rules are identical to staging. Any
// supplied network client is ignored; neither IPC nor retained bytes choose one.
func RunVerifyWorker(ctx context.Context, config WorkerConfig, input io.Reader, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if _, err := keyID(config.PublicKey); err != nil {
		return err
	}

	request, err := readStageRequest(input)
	if err != nil {
		return err
	}

	root, err := os.OpenRoot(config.Directory)
	if err != nil {
		return err
	}
	defer root.Close()

	info, err := root.Lstat(verifyInput)
	if err != nil {
		return err
	}

	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("offline verification requires a real input directory")
	}

	source, err := root.OpenRoot(verifyInput)
	if err != nil {
		return err
	}
	defer source.Close()

	config.HTTPClient = &http.Client{Transport: &localPackageAssets{root: source, release: request.Release}}

	data, err := json.Marshal(request)
	if err != nil {
		return err
	}

	return RunStageWorker(ctx, config, bytes.NewReader(data), output)
}

type localPackageAssets struct {
	root    *os.Root
	release ResolvedRelease
}

func (p *localPackageAssets) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		return nil, err
	}

	if request.Method != http.MethodGet {
		return nil, errors.New("offline verifier only reads selected assets")
	}

	var (
		name     string
		expected int64
	)

	switch request.URL.String() {
	case p.release.Package.URL:
		name = "package"
		expected = p.release.Package.Size
	case p.release.Signature.URL:
		name = "signature"
		expected = 64
	default:
		return nil, errors.New("offline verifier has no asset for this URL")
	}

	file, err := openPackageFile(p.root, name, expected)
	if err != nil {
		return nil, err
	}

	return &http.Response{StatusCode: http.StatusOK, Body: file, ContentLength: expected, Header: http.Header{"Content-Type": []string{"application/octet-stream"}}}, nil
}
