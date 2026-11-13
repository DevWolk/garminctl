package native

import (
	"context"
	"errors"

	"github.com/DevWolk/garminctl/internal/containers/application"
)

type retainedUpdates interface {
	RetainedUpdates(context.Context, bool) ([]application.PackageRecord, error)
}

// SavedPackage is display metadata, not proof of authenticated retained bytes.
type SavedPackage struct {
	Tag        string `json:"tag"`
	CacheID    string `json:"cacheId"`
	Prerelease bool   `json:"prerelease"`
}

// SavedPackages uses local metadata only, so a retained update can be chosen offline.
func (s *Service) SavedPackages(prerelease bool) ([]SavedPackage, error) {
	result, err := invokeRead(s, func(ctx context.Context, r Reader) (*[]SavedPackage, error) {
		source, ok := r.(retainedUpdates)
		if !ok {
			return nil, errors.New("saved application packages are unavailable")
		}

		rows, err := source.RetainedUpdates(ctx, prerelease)
		if err != nil {
			return nil, err
		}

		values := make([]SavedPackage, 0, len(rows))
		for _, row := range rows {
			values = append(values, SavedPackage{Tag: row.Tag, CacheID: row.ID, Prerelease: row.Prerelease})
		}

		return &values, nil
	})
	if err != nil {
		return nil, err
	}

	return *result, nil
}
