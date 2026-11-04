package music

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var badChars = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]+`)

func safeName(s, fallback string) string {
	s = strings.TrimSpace(badChars.ReplaceAllString(s, " "))

	s = strings.Trim(s, ". ")
	if s == "" {
		return fallback
	}

	if r := []rune(s); len(r) > 60 {
		s = string(r[:60])
	}

	return s
}

func musicPath(p string) error {
	p = strings.ReplaceAll(p, "\\", "/")
	for _, f := range MusicFolders {
		if strings.HasPrefix(p, f+"/") {
			return nil
		}
	}

	return fmt.Errorf("%s is not inside Music, Podcasts or Audiobooks", p)
}

// titleCase upper-cases the first letter of every word and lower-cases the
// rest; underscores count as word breaks ("trail_running" -> "Trail Running").
func titleCase(s string) string {
	var b strings.Builder

	startWord := true

	for _, r := range strings.ReplaceAll(s, "_", " ") {
		switch {
		case unicode.IsSpace(r):
			startWord = true

			b.WriteRune(r)
		case startWord:
			startWord = false

			b.WriteRune(unicode.ToUpper(r))
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}

	return b.String()
}
