package notify

import "testing"

func TestPSQuoteDoublesEverySingleQuote(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"plain":                    "'plain'",
		"it's":                     "'it''s'",
		"a\u2018b\u2019c":          "'a\u2018\u2018b\u2019\u2019c'",
		"\u201a'); calc; ('\u201b": "'\u201a\u201a''); calc; (''\u201b\u201b'",
	} {
		if got := psQuote(in); got != want {
			t.Errorf("psQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
