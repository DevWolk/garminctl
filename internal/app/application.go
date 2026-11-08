package app

import (
	"context"
	"net/http"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

// NewApplicationReleaseCheck composes a read-only feed without a watch runtime.
// The entry point owns the client; no request is made until Run is called.
func NewApplicationReleaseCheck(client *http.Client) *application.CheckReleasesAction {
	return application.NewCheckReleasesAction(applicationReleaseFeed{source: updatetransport.NewGitHub(client)})
}

type applicationReleaseFeed struct{ source *updatetransport.GitHub }

func (f applicationReleaseFeed) Releases(ctx context.Context) ([]application.Release, error) {
	items, err := f.source.Releases(ctx)
	if err != nil {
		return nil, err
	}

	return applicationReleases(items), nil
}

func applicationReleases(items []updatetransport.Release) []application.Release {
	releases := make([]application.Release, len(items))
	for i, item := range items {
		releases[i] = application.Release{
			Tag: item.Tag, Name: item.Name, Notes: item.Notes, PublishedAt: item.PublishedAt,
			Prerelease: item.Prerelease, Draft: item.Draft,
		}
	}

	return releases
}
