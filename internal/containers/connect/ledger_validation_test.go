package connect

import (
	"strings"
	"testing"
)

func TestValidateSavedLedgerMatchesReaderWithoutUploading(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		data  string
		valid bool
	}{
		{`{"GARMIN/Activity/a.fit":"42|modified"}`, true}, {`{}`, true}, {`null`, false}, {`{"file":42}`, false}, {`{} {}`, false},
	} {
		if err := ValidateSavedLedger(strings.NewReader(tc.data)); (err == nil) != tc.valid {
			t.Fatalf("valid=%v error=%v", tc.valid, err)
		}
	}
}
