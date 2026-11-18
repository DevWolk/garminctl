package connect

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/gc"
)

func TestLogoutDeletesSessionCopiesAndReportsEveryFailure(t *testing.T) {
	t.Parallel()

	store := filepath.Join(t.TempDir(), "garminctl", "garmin-connect.json")
	if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(store, []byte(`{"email":"reader@example.invalid"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	forgotten := 0
	failure := errors.New("fixture backup is read-only")

	err := LogoutAction{Client: gc.New(store), ForgetCopies: func(context.Context) error {
		forgotten++

		if _, err := os.Stat(store); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("copies were deleted before the session itself: %v", err)
		}

		return failure
	}}.Run(t.Context())
	if !errors.Is(err, failure) || forgotten != 1 {
		t.Fatalf("error=%v calls=%d", err, forgotten)
	}

	if err := (LogoutAction{Client: gc.New(store)}).Run(t.Context()); err != nil {
		t.Fatalf("an entry point without copies: %v", err)
	}
}
