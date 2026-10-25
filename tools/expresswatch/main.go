// Command expresswatch monitors Garmin Express for Windows releases.
// It updates release metadata for .github/workflows/express-watch.yml.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	installerURL = "https://download.garmin.com/omt/express/GarminExpress.exe"
	forumURL     = "https://forums.garmin.com/apps-software/mac-windows-software/f/garmin-express-windows"
	userAgent    = "Mozilla/5.0 (compatible; garminctl-express-watch; +https://github.com/DevWolk/garminctl)"
	maxPage      = 4 << 20
)

// release is what the state file records about the current Garmin Express.
type release struct {
	Version   string    `json:"version"`
	Thread    string    `json:"thread,omitempty"`
	Notes     []string  `json:"notes,omitempty"`
	Installer installer `json:"installer"`
	Seen      string    `json:"seen"` // the day this release was first noticed
}

type installer struct {
	ETag     string `json:"etag"`
	Modified string `json:"modified"`
	Bytes    int64  `json:"bytes"`
}

var (
	// reThread finds release-notes threads in the forum listing; Garmin
	// starts a new thread per release, so the highest id is the newest.
	reThread = regexp.MustCompile(`/f/garmin-express-windows/(\d+)/garmin-express-release-notes`)
	// reVersion is the opening sentence of a release-notes post.
	reVersion = regexp.MustCompile(`Garmin Express v(\d+(?:\.\d+)+) is available for PC`)
	reTag     = regexp.MustCompile(`<[^>]+>`)
	reUnicode = regexp.MustCompile(`\\u([0-9a-fA-F]{4})`)
)

func main() {
	state := flag.String("state", "docs/express-release.json", "state file with the last release seen")
	notes := flag.String("notes", "", "write a Markdown summary of a new release to this file")
	output := flag.String("output", "", "append changed=/version= lines to this file ($GITHUB_OUTPUT)")

	flag.Parse()

	if err := run(context.Background(), *state, *notes, *output); err != nil {
		fmt.Fprintln(os.Stderr, "expresswatch:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, statePath, notesPath, outputPath string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	current := loadState(statePath)

	found, err := probe(ctx, &http.Client{Timeout: time.Minute})
	if err != nil {
		return err
	}

	changed := found.Version != current.Version || found.Installer != current.Installer
	fmt.Printf("current %s (%s), found %s (%s): changed=%v\n", current.Version, current.Installer.Modified, found.Version, found.Installer.Modified, changed)

	if changed {
		found.Seen = time.Now().UTC().Format(time.DateOnly)
		if err := saveState(statePath, found); err != nil {
			return err
		}

		if notesPath != "" {
			if err := os.WriteFile(notesPath, []byte(summary(current, found)), 0o600); err != nil {
				return err
			}
		}
	}

	return appendOutput(outputPath, changed, found.Version)
}

func loadState(path string) release {
	var r release

	b, err := os.ReadFile(path) //nolint:gosec // the state file named on the command line
	if err == nil {
		_ = json.Unmarshal(b, &r)
	}

	return r
}

func saveState(path string, r release) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, append(b, '\n'), 0o600)
}

func appendOutput(path string, changed bool, version string) error {
	if path == "" {
		return nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600) //nolint:gosec // $GITHUB_OUTPUT, named on the command line
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(f, "changed=%v\nversion=%s\n", changed, version)

	return errors.Join(err, f.Close())
}

// probe asks Garmin's download server about the installer and reads the
// newest release-notes thread. The installer is the reliable signal; the
// thread supplies the version and the notes when it can be read.
func probe(ctx context.Context, client *http.Client) (release, error) {
	var r release

	inst, err := headInstaller(ctx, client)
	if err != nil {
		return r, err
	}

	r.Installer = inst

	listing, err := get(ctx, client, forumURL)
	if err != nil {
		return r, fmt.Errorf("forum listing: %w", err)
	}

	thread := newestThread(listing)
	if thread == "" {
		return r, errors.New("no release-notes thread in the forum listing; the forum layout may have changed")
	}

	page, err := get(ctx, client, thread)
	if err != nil {
		return r, fmt.Errorf("release notes: %w", err)
	}

	r.Thread = thread
	r.Version, r.Notes = parseNotes(page)

	if r.Version == "" {
		return r, fmt.Errorf("no version in %s; the post format may have changed", thread)
	}

	return r, nil
}

func headInstaller(ctx context.Context, client *http.Client) (installer, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, installerURL, nil)
	if err != nil {
		return installer{}, err
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return installer{}, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return installer{}, fmt.Errorf("installer: HTTP %d", resp.StatusCode)
	}

	return installer{ETag: resp.Header.Get("ETag"), Modified: resp.Header.Get("Last-Modified"), Bytes: resp.ContentLength}, nil
}

func get(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, maxPage))
	if err != nil {
		return "", err
	}

	return string(b), nil
}

// newestThread returns the release-notes thread with the highest id in the
// listing, or "".
func newestThread(listing string) string {
	var (
		best   string
		bestID int64
	)

	for _, m := range reThread.FindAllStringSubmatch(listing, -1) {
		id, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || id <= bestID {
			continue
		}

		best, bestID = m[0], id
	}

	if best == "" {
		return ""
	}

	return "https://forums.garmin.com/apps-software/mac-windows-software" + best
}

// parseNotes reads the version and the change notes out of a release-notes
// page. The post appears more than once in the page (rendered and as
// escaped JSON), so every occurrence is tried and the fullest wins.
func parseNotes(page string) (string, []string) {
	text := postText(page)

	var (
		version string
		notes   []string
	)

	for _, m := range reVersion.FindAllStringSubmatchIndex(text, -1) {
		n := notesAfter(text[m[1]:])
		if version == "" || len(n) > len(notes) {
			version, notes = text[m[2]:m[3]], n
		}
	}

	return version, notes
}

// notesAfter collects the change lines that follow the opening sentence,
// up to the history of earlier versions.
func notesAfter(rest string) []string {
	if i := strings.Index(rest, "following changes:"); i >= 0 {
		rest = rest[i+len("following changes:"):]
	}

	if i := strings.Index(rest, "Previous Versions"); i >= 0 {
		rest = rest[:i]
	}

	var notes []string

	for line := range strings.SplitSeq(rest, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			notes = append(notes, line)
		}
	}

	return notes
}

// postText flattens the page to text: JSON escapes become characters, tags
// become spaces, entities are decoded.
func postText(page string) string {
	s := strings.ReplaceAll(page, `\n`, "\n")
	s = reUnicode.ReplaceAllStringFunc(s, func(m string) string {
		r, err := strconv.ParseUint(m[2:], 16, 16) // four hex digits: fits a rune
		if err != nil {
			return m
		}

		return string(rune(r))
	})
	s = reTag.ReplaceAllString(s, " ")

	return html.UnescapeString(s)
}

// summary is the pull request body for a new release.
func summary(old, found release) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Garmin released Garmin Express **%s** for Windows", found.Version)

	if old.Version != "" {
		fmt.Fprintf(&b, " (garminctl last looked at %s)", old.Version)
	}

	b.WriteString(".\n\n")

	if len(found.Notes) > 0 {
		b.WriteString("Changes Garmin lists:\n\n")

		for _, n := range found.Notes {
			fmt.Fprintf(&b, "- %s\n", n)
		}

		b.WriteString("\n")
	}

	if found.Thread != "" {
		fmt.Fprintf(&b, "Release notes: %s\n", found.Thread)
	}

	fmt.Fprintf(&b, "Installer: %s, %d bytes, modified %s\n\n", installerURL, found.Installer.Bytes, found.Installer.Modified)
	b.WriteString("Review changes affecting garminctl's sync, device settings, Connect IQ apps, software updates or music.\n")

	return b.String()
}
