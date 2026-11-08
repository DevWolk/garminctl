// Package updatetransport reads release metadata, matches package assets and
// adapts their bounded streams to the pinned headless Wails updater. Release
// policy, trust configuration and installation belong to its callers.
package updatetransport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	releasesEndpoint = "https://api.github.com/repos/DevWolk/garminctl/releases"
	pageSize         = 100
	maxPages         = 10
	maxPageBytes     = 2 << 20
	requestTimeout   = 10 * time.Second
	checkTimeout     = 30 * time.Second
)

// Release retains GitHub display metadata, including draft and prerelease flags.
// It is not evidence of a downloaded or authenticated package.
type Release struct {
	Tag         string    `json:"tag_name"`
	Name        string    `json:"name"`
	Notes       string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
}

// StatusError reports a failed metadata response without copying its body.
type StatusError struct{ StatusCode int }

func (e *StatusError) Error() string {
	return fmt.Sprintf("GitHub releases: HTTP %d (%s)", e.StatusCode, http.StatusText(e.StatusCode))
}

// GitHub reads public release and asset metadata from the compiled repository.
type GitHub struct{ client *http.Client }

// NewGitHub borrows the client's transport, with bounded time and no redirects.
// A nil client uses the standard HTTP transport. Credentials are not configured.
func NewGitHub(client *http.Client) *GitHub {
	bounded := http.Client{}
	if client != nil {
		bounded = *client
	}

	if bounded.Timeout <= 0 || bounded.Timeout > requestTimeout {
		bounded.Timeout = requestTimeout
	}

	bounded.Jar = nil
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	return &GitHub{client: &bounded}
}

// Releases fetches all bounded pages or returns an error without partial data.
func (g *GitHub) Releases(ctx context.Context) ([]Release, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if g.client == nil {
		return nil, errors.New("GitHub release client is not configured")
	}

	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	releases := make([]Release, 0)

	for page := 1; page <= maxPages; page++ {
		items, next, err := g.page(ctx, page)
		if err != nil {
			return nil, fmt.Errorf("GitHub release page %d: %w", page, err)
		}

		releases = append(releases, items...)

		if err := ctx.Err(); err != nil {
			return nil, err
		}

		if !next {
			return releases, nil
		}
	}

	return nil, errors.New("GitHub release history exceeds the page limit")
}

func (g *GitHub) page(ctx context.Context, page int) ([]Release, bool, error) {
	endpoint := releasesEndpoint + "?per_page=" + strconv.Itoa(pageSize) + "&page=" + strconv.Itoa(page)

	data, headers, err := g.getMetadata(ctx, endpoint)
	if err != nil {
		return nil, false, err
	}

	items, err := readReleases(data)
	if err != nil {
		return nil, false, err
	}

	next, err := hasNextPage(headers.Values("Link"))
	if err != nil {
		return nil, false, err
	}

	return items, next, nil
}

func (g *GitHub) getMetadata(ctx context.Context, endpoint string) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, nil, err
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	req.Header.Set("User-Agent", "garminctl")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, &StatusError{StatusCode: resp.StatusCode}
	}

	data, err := readMetadata(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	return data, resp.Header, nil
}

func readMetadata(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxPageBytes+1))
	if err != nil {
		return nil, err
	}

	if len(data) > maxPageBytes {
		return nil, errors.New("GitHub release page exceeds the byte limit")
	}

	return data, nil
}

func readReleases(data []byte) ([]Release, error) {
	var items []Release
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("decode GitHub releases: %w", err)
	}

	if items == nil || len(items) > pageSize {
		return nil, errors.New("GitHub release page must be an array of at most 100 records")
	}

	return items, nil
}

func hasNextPage(headers []string) (bool, error) {
	next := false

	for _, header := range headers {
		for link := range strings.SplitSeq(header, ",") {
			_, parameters, ok := strings.Cut(strings.TrimSpace(link), ">")
			if !ok || !strings.HasPrefix(strings.TrimSpace(link), "<") {
				return false, errors.New("invalid GitHub pagination header")
			}

			_, values, err := mime.ParseMediaType("link" + parameters)
			if err != nil {
				return false, fmt.Errorf("invalid GitHub pagination parameters: %w", err)
			}

			// Only rel is consumed. URLs never leave the compiled repository;
			// GitHub's numbered pagination advances the locally generated page.
			next = next || slices.Contains(strings.Fields(values["rel"]), "next")
		}
	}

	return next, nil
}
