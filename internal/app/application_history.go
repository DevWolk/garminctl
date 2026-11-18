package app

import (
	"context"
	"net/http"
	"path/filepath"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/paths"
	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

// NewApplicationReleaseHistory composes an explicit, persistent history check.
// An empty directory selects the app's private history cache. A nonempty path
// is trusted local composition, not an HTTP/CLI option. Construction performs no IO.
func NewApplicationReleaseHistory(client *http.Client, directory string) (*application.CheckReleaseHistoryAction, error) {
	store, err := applicationHistoryStoreFor(directory)
	if err != nil {
		return nil, err
	}

	return application.NewCheckReleaseHistoryAction(applicationReleaseFeed{source: updatetransport.NewGitHub(client)}, store), nil
}

func applicationHistoryStoreFor(directory string) (applicationHistoryStore, error) {
	if directory == "" {
		directory = filepath.Join(paths.Cache(), "application-history")
	}

	cache, err := updatetransport.NewHistoryCache(directory)

	return applicationHistoryStore{cache: cache}, err
}

type applicationHistoryStore struct{ cache *updatetransport.HistoryCache }

func (s applicationHistoryStore) Load(ctx context.Context) (*application.ReleaseSnapshot, error) {
	snapshot, err := s.cache.Load(ctx)
	if err != nil {
		return nil, err
	}

	return &application.ReleaseSnapshot{CheckedAt: snapshot.CheckedAt, Releases: applicationReleases(snapshot.Releases)}, nil
}

func (s applicationHistoryStore) Save(ctx context.Context, snapshot application.ReleaseSnapshot) error {
	releases := make([]updatetransport.Release, len(snapshot.Releases))
	for i, item := range snapshot.Releases {
		releases[i] = updatetransport.Release{Tag: item.Tag, Name: item.Name, Notes: item.Notes, PublishedAt: item.PublishedAt, Prerelease: item.Prerelease, Draft: item.Draft}
	}

	return s.cache.Save(ctx, updatetransport.HistorySnapshot{CheckedAt: snapshot.CheckedAt, Releases: releases})
}
