package apps

import "testing"

func TestUpdateAvailableComparesStoreVersion(t *testing.T) {
	t.Parallel()

	installed := App{ID: "5a000000-0000-4000-8000-0000000000d4", Version: 23}

	for _, tt := range []struct {
		latest int
		want   bool
	}{{latest: 24, want: true}, {latest: 23}, {latest: 22}} {
		if got := installed.UpdateAvailable(tt.latest); got != tt.want {
			t.Errorf("UpdateAvailable(%d)=%t, want %t", tt.latest, got, tt.want)
		}
	}

	if IDKey("5A000000-0000-4000-8000-0000000000D4") != IDKey(installed.ID) || IDKey("other") == IDKey(installed.ID) {
		t.Fatal("ids must match without regard to case and only themselves")
	}
}
