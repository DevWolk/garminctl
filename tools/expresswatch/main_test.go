package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseNotes_ReadsVersionAndChanges(t *testing.T) {
	t.Parallel()

	page, err := os.ReadFile(filepath.Join("testdata", "release-notes.html"))
	if err != nil {
		t.Fatal(err)
	}

	version, notes := parseNotes(string(page))
	if version != "7.29.1" {
		t.Fatalf("version = %q, want 7.29.1", version)
	}

	if len(notes) != 1 || !strings.Contains(notes[0], "CIRQA") {
		t.Fatalf("notes = %q, want the one CIRQA line", notes)
	}
}

func TestNewestThread_PicksTheHighestID(t *testing.T) {
	t.Parallel()

	listing, err := os.ReadFile(filepath.Join("testdata", "listing.html"))
	if err != nil {
		t.Fatal(err)
	}

	got := newestThread(string(listing))
	want := "https://forums.garmin.com/apps-software/mac-windows-software/f/garmin-express-windows/440292/garmin-express-release-notes"

	if got != want {
		t.Fatalf("newestThread = %q, want %q", got, want)
	}

	if newestThread("<p>nothing here</p>") != "" {
		t.Fatal("a listing without the thread must give an empty result")
	}
}

func TestSummary_MentionsVersionAndNotes(t *testing.T) {
	t.Parallel()

	found := release{Version: "7.30.0", Notes: []string{"Fixed syncing."}, Thread: "https://forums.garmin.com/t/1"}
	s := summary(release{Version: "7.29.1"}, found)

	for _, want := range []string{"**7.30.0**", "7.29.1", "- Fixed syncing.", found.Thread, installerURL} {
		if !strings.Contains(s, want) {
			t.Fatalf("summary lacks %q:\n%s", want, s)
		}
	}
}
