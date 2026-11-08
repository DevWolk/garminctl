package updatetransport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

// AssetRef identifies an API asset, without downloading or authenticating it.
type AssetRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"url"`
}

// ResolvedRelease copies the selected release and same-release asset metadata.
// It is not permission to install; Application must recheck release policy.
type ResolvedRelease struct {
	ReleaseID int64    `json:"releaseId"`
	Release   Release  `json:"release"`
	Package   AssetRef `json:"package"`
	Signature AssetRef `json:"signature"`
}

// Resolve reads the requested tag and that release ID's asset pages, then
// matches one package/signature pair without making any byte-download request.
func (g *GitHub) Resolve(ctx context.Context, request MatchRequest) (*ResolvedRelease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if _, err := packageFilename(request); err != nil {
		return nil, err
	}

	if g.client == nil {
		return nil, errors.New("GitHub release client is not configured")
	}

	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	record, err := g.releaseRecord(ctx, request.ReleaseTag)
	if err != nil {
		return nil, err
	}

	assets, err := g.releaseAssets(ctx, record.ID)
	if err != nil {
		return nil, err
	}

	candidates := make([]Asset, len(assets))
	for i, asset := range assets {
		candidates[i] = Asset{Name: asset.Name, Size: asset.Size}
	}

	match, err := MatchAssets(request, candidates)
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return &ResolvedRelease{
		ReleaseID: record.ID, Release: record.Release,
		Package: assets[match.PackageIndex], Signature: assets[match.SignatureIndex],
	}, nil
}

type releaseRecord struct {
	Release
	ID int64 `json:"id"`
}

func (g *GitHub) releaseRecord(ctx context.Context, tag string) (*releaseRecord, error) {
	data, _, err := g.getMetadata(ctx, releasesEndpoint+"/tags/"+url.PathEscape(tag))
	if err != nil {
		return nil, fmt.Errorf("read requested GitHub release: %w", err)
	}

	var record *releaseRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("decode requested GitHub release: %w", err)
	}

	if record == nil || record.ID <= 0 || record.Tag != tag {
		return nil, errors.New("GitHub response does not identify the requested release tag")
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return record, nil
}

func (g *GitHub) releaseAssets(ctx context.Context, releaseID int64) ([]AssetRef, error) {
	base := releasesEndpoint + "/" + strconv.FormatInt(releaseID, 10) + "/assets"
	assets := make([]AssetRef, 0)
	seen := make(map[int64]bool)

	for page := 1; page <= maxPages; page++ {
		endpoint := base + "?per_page=" + strconv.Itoa(pageSize) + "&page=" + strconv.Itoa(page)

		data, headers, err := g.getMetadata(ctx, endpoint)
		if err != nil {
			return nil, fmt.Errorf("read GitHub asset page %d: %w", page, err)
		}

		var items []AssetRef
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, fmt.Errorf("decode GitHub asset page: %w", err)
		}

		if items == nil || len(items) > pageSize {
			return nil, errors.New("GitHub asset page must be an array of at most 100 records")
		}

		for _, asset := range items {
			if err := validateAssetRef(asset); err != nil {
				return nil, err
			}

			if seen[asset.ID] {
				return nil, errors.New("duplicate GitHub asset identity")
			}

			seen[asset.ID] = true
		}

		assets = append(assets, items...)

		if err := ctx.Err(); err != nil {
			return nil, err
		}

		next, err := hasNextPage(headers.Values("Link"))
		if err != nil {
			return nil, err
		}

		if !next {
			return assets, nil
		}
	}

	return nil, errors.New("GitHub release assets exceed the page limit")
}

func validateAssetRef(asset AssetRef) error {
	expected := releasesEndpoint + "/assets/" + strconv.FormatInt(asset.ID, 10)
	if asset.ID <= 0 || asset.URL != expected {
		return errors.New("GitHub asset has an invalid identity or repository URL")
	}

	return nil
}
