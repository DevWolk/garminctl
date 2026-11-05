package apps

import (
	"encoding/json"
	"strings"
	"testing"
)

func appHintsJSON(t *testing.T, value *AppHints) string {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func TestAppHintsReturnsIndependentSnapshots(t *testing.T) {
	t.Parallel()

	const appID = "7c993e2b-70b3-459a-bbe0-db48a89af5a8"

	first := AppHintsFor(appID)
	if first == nil || len(first.KeyHints) == 0 || len(first.ValueHints) == 0 {
		t.Fatal("fixture lacks expected app hints")
	}

	want := appHintsJSON(t, first)
	first.Name = "modified hint name"
	clear(first.KeyHints)
	clear(first.ValueHints)

	if got := appHintsJSON(t, AppHintsFor(strings.ToUpper(appID))); got != want {
		t.Fatal("app-hint snapshot changed global hints or case-insensitive lookup")
	}

	if AppHintsFor("absent-app-hints") != nil {
		t.Fatal("unknown app acquired hints")
	}
}
