package updatetransport_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

func TestGitHubResolveRejectsChangedTagOrInvalidReleaseRecord(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, body string
	}{
		{name: "different tag", body: `{"id":41,"tag_name":"v9.0.0"}`},
		{name: "normalized alias is not the selected tag", body: `{"id":41,"tag_name":"1.2.0"}`},
		{name: "missing id", body: `{"tag_name":"v1.2.0"}`},
		{name: "negative id", body: `{"id":-41,"tag_name":"v1.2.0"}`},
		{name: "id overflow", body: `{"id":9223372036854775808,"tag_name":"v1.2.0"}`},
		{name: "null object", body: nullJSON},
		{name: "array", body: "[]"},
		{name: "trailing value", body: `{"id":41,"tag_name":"v1.2.0"} {}`},
		{name: "overlong record", body: `{"id":41,"tag_name":"v1.2.0"}` + strings.Repeat(" ", 2<<20)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			source := updatetransport.NewGitHub(resolveClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++

				if r.URL.Path == tagPath {
					_, _ = fmt.Fprint(w, tc.body)
				} else {
					writeResolveJSON(t, w, resolvePair())
				}
			}))

			got, err := source.Resolve(t.Context(), setupRequest())
			if got != nil || err == nil || calls != 1 {
				t.Fatalf("result=%+v error=%v calls=%d", got, err, calls)
			}
		})
	}
}

func TestGitHubResolveRejectsInvalidAssetIdentity(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		id   int64
		url  string
	}{
		{name: "zero id", url: releaseEndpoint + "/assets/0"},
		{name: "negative id", id: -1, url: releaseEndpoint + "/assets/-1"},
		{name: "missing url", id: 101},
		{name: "http", id: 101, url: "http://api.github.com/repos/DevWolk/garminctl/releases/assets/101"},
		{name: "foreign host", id: 101, url: "https://untrusted.invalid/repos/DevWolk/garminctl/releases/assets/101"},
		{name: "foreign repository", id: 101, url: "https://api.github.com/repos/other/repo/releases/assets/101"},
		{name: "id mismatch", id: 101, url: releaseEndpoint + "/assets/999"},
		{name: "query", id: 101, url: releaseEndpoint + "/assets/101?raw=1"},
		{name: "fragment", id: 101, url: releaseEndpoint + "/assets/101#asset"},
		{name: "userinfo", id: 101, url: "https://user@api.github.com/repos/DevWolk/garminctl/releases/assets/101"},
		{name: "relative path", id: 101, url: "/repos/DevWolk/garminctl/releases/assets/101"},
		{name: "encoded id", id: 101, url: releaseEndpoint + "/assets/%31%30%31"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pair := resolvePair()
			pair[0].ID, pair[0].URL = tc.id, tc.url
			source := updatetransport.NewGitHub(resolveClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tagPath {
					writeResolveJSON(t, w, tagRecord())
				} else {
					writeResolveJSON(t, w, pair)
				}
			}))

			got, err := source.Resolve(t.Context(), setupRequest())
			if got != nil || err == nil {
				t.Fatalf("result=%+v error=%v", got, err)
			}
		})
	}
}

func TestGitHubResolveUsesMatcherAndRejectsDuplicateIDs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		assets []updatetransport.AssetRef
	}{
		{name: "empty"},
		{name: "wrong package and signature", assets: []updatetransport.AssetRef{apiAsset(101, zip120, 2), apiAsset(708, zip120+".sig", 64)}},
		{name: "bad package size", assets: []updatetransport.AssetRef{apiAsset(101, setup120, 0), apiAsset(708, setup120+".sig", 64)}},
		{name: "bad signature size", assets: []updatetransport.AssetRef{apiAsset(101, setup120, 1), apiAsset(708, setup120+".sig", 63)}},
		{name: "same id used twice", assets: []updatetransport.AssetRef{apiAsset(101, setup120, 1), apiAsset(101, setup120+".sig", 64)}},
		{name: "duplicate name with distinct ids", assets: []updatetransport.AssetRef{apiAsset(101, setup120, 1), apiAsset(102, setup120, 2), apiAsset(708, setup120+".sig", 64)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			source := updatetransport.NewGitHub(resolveClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tagPath {
					writeResolveJSON(t, w, tagRecord())
				} else {
					assets := tc.assets
					if assets == nil {
						assets = []updatetransport.AssetRef{}
					}

					writeResolveJSON(t, w, assets)
				}
			}))

			got, err := source.Resolve(t.Context(), setupRequest())
			if got != nil || err == nil {
				t.Fatalf("result=%+v error=%v", got, err)
			}
		})
	}
}
