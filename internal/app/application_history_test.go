package app_test

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/containers/application"
)

func TestApplicationHistorySurvivesNewCompositionOffline(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "history")
	fresh := saveApplicationHistory(t, directory)
	offline := &http.Client{Transport: releaseRoundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline fixture") })}

	restarted, err := app.NewApplicationReleaseHistory(offline, directory)
	if err != nil {
		t.Fatal(err)
	}

	request := application.CheckRequest{CurrentVersion: "v1.1.0", IncludePrerelease: true}

	cached, err := restarted.Run(t.Context(), request)
	if err != nil || cached == nil || !cached.FromCache || cached.RefreshError == "" || !cached.CheckedAt.Equal(fresh.CheckedAt) || cached.Available.Tag != "v2.0.0-beta.1" || len(cached.History) != 2 {
		t.Fatalf("cached=%+v error=%v", cached, err)
	}

	if cached.History[1].Notes != "<notes>" {
		t.Fatal("lost release notes")
	}

	if err := os.WriteFile(filepath.Join(directory, "history.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	if result, err := restarted.Run(t.Context(), request); result != nil || err == nil {
		t.Fatalf("corrupt offline=%+v %v", result, err)
	}
}

func saveApplicationHistory(t *testing.T, directory string) *application.ReleaseHistoryResult {
	t.Helper()

	calls := 0
	online := &http.Client{Transport: releaseRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++

		if r.URL.Host != "api.github.com" || r.URL.Path != "/repos/DevWolk/garminctl/releases" {
			t.Errorf("unexpected URL %s", r.URL)
		}

		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[{"tag_name":"v1.3.0","body":"<notes>"},{"tag_name":"v2.0.0-beta.1","prerelease":true},{"tag_name":"v9.0.0","draft":true}]`))}, nil
	})}

	check, err := app.NewApplicationReleaseHistory(online, directory)
	if err != nil {
		t.Fatal(err)
	}

	if calls != 0 {
		t.Fatal("composition fetched metadata")
	}

	request := application.CheckRequest{CurrentVersion: "v1.1.0"}

	fresh, err := check.Run(t.Context(), request)
	if err != nil || fresh == nil || fresh.FromCache || fresh.Available.Tag != "v1.3.0" || fresh.CacheError != "" {
		t.Fatalf("fresh=%+v error=%v", fresh, err)
	}

	return fresh
}
