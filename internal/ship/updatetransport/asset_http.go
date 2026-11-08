package updatetransport

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"net/http"
	"time"
)

const downloadTimeout = 15 * time.Minute

func assetHTTPClient(client *http.Client) *http.Client {
	bounded := http.Client{}
	if client != nil {
		bounded = *client
	}

	if bounded.Timeout <= 0 || bounded.Timeout > downloadTimeout {
		bounded.Timeout = downloadTimeout
	}

	bounded.Jar = nil
	previous := bounded.CheckRedirect
	bounded.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("release asset exceeds the redirect limit")
		}

		if previous != nil {
			if err := previous(r, via); err != nil {
				return err
			}
		}

		if r.URL.Scheme != "https" || r.URL.Host == "" || r.URL.User != nil || r.URL.Opaque != "" {
			return errors.New("release asset redirects require HTTPS without URL credentials")
		}

		r.Header.Del("Authorization")
		r.Header.Del("Cookie")

		return nil
	}

	return &bounded
}

func (p *Provider) openAsset(ctx context.Context, asset AssetRef) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	req.Header.Set("User-Agent", "garminctl")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()

		return nil, &StatusError{StatusCode: resp.StatusCode}
	}

	return resp.Body, nil
}

func (p *Provider) readSignature(ctx context.Context) ([]byte, error) {
	body, err := p.openAsset(ctx, p.record.Signature)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	signature, err := io.ReadAll(io.LimitReader(body, ed25519.SignatureSize+1))
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if len(signature) != ed25519.SignatureSize {
		return nil, errors.New("release signature must contain exactly 64 raw bytes")
	}

	return signature, nil
}
