package connect

import "testing"

func TestWrittenCountsOnlyDeliveredFiles(t *testing.T) {
	t.Parallel()

	count, failure := Written([]PulledFile{
		{Name: "settings", Error: "watch is busy"},
		{Name: "workout", Path: "GARMIN/NewFiles/w.fit"},
		{Name: "course", Error: "no place on the watch"},
	})
	if count != 1 || failure != "watch is busy" {
		t.Fatalf("Written = %d, %q", count, failure)
	}

	if count, failure := Written(nil); count != 0 || failure != "" {
		t.Fatalf("Written(nil) = %d, %q", count, failure)
	}
}
