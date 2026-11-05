package web

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func TestAutoActionsNotifySyncAndUpdates(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		signedIn bool
		sync     []string
		ok       bool
		updates  []string
		want     string
	}{
		{
			name: "sync and update", signedIn: true, sync: []string{"2 file(s) uploaded to Garmin Connect", "1 sync error(s)"}, ok: true, updates: []string{"Fixture 1.00"},
			want: "Fixture watch: 2 file(s) uploaded to Garmin Connect; 1 sync error(s); update available: Fixture 1.00",
		},
		{name: "signed out", updates: []string{"Fixture 1.00", "Maps 2.00"}, want: "Fixture watch: update available: Fixture 1.00, Maps 2.00"},
		{name: "nothing new", signedIn: true, ok: true, want: "Fixture watch: synced, nothing new"},
		{name: "cancelled sync", signedIn: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			manager := watchsession.NewManager(func(context.Context) (device.Device, error) { return device.NewDirFS(root) })
			t.Cleanup(manager.Drop)

			lease, err := manager.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			epoch := lease.Epoch()
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}

			var notified []string

			s := &Server{manager: manager, cloud: signedOutClient(t), notify: func(_ context.Context, _, text string) { notified = append(notified, text) }}
			if tt.signedIn {
				s.cloud, _ = signedInSyncClient(t)
			}

			s.features.AutoSync = func(_ context.Context, got watchsession.ConnectionEpoch) ([]string, bool) {
				if got != epoch {
					t.Errorf("sync epoch=%d, want %d", got, epoch)
				}

				return tt.sync, tt.ok
			}
			s.features.SoftwareUpdates = func(context.Context, *watchsession.Session) ([]string, error) { return tt.updates, nil }

			s.autoActions(t.Context(), epoch, "Fixture watch")

			if tt.want == "" && len(notified) != 0 || tt.want != "" && (len(notified) != 1 || notified[0] != tt.want) {
				t.Fatalf("notifications=%q, want %q", notified, tt.want)
			}
		})
	}
}

func TestUpdateNotesDropOnlyDisconnectedWatch(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		err  error
		drop bool
	}{
		{name: "disconnected", err: device.ErrDisconnected, drop: true},
		{name: "other failure", err: errors.New("synthetic")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			manager := watchsession.NewManager(func(context.Context) (device.Device, error) { return device.NewDirFS(root) })
			t.Cleanup(manager.Drop)

			lease, err := manager.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			epoch := lease.Epoch()
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}

			s := &Server{manager: manager}
			s.features.SoftwareUpdates = func(context.Context, *watchsession.Session) ([]string, error) { return []string{"ignored"}, tt.err }

			if notes := s.updateNotes(t.Context(), epoch); notes != nil {
				t.Fatalf("failed update check produced notes %q", notes)
			}

			if dropped := manager.CurrentEpoch() != epoch; dropped != tt.drop {
				t.Fatalf("dropped=%t, want %t", dropped, tt.drop)
			}
		})
	}
}

// signedOutClient has no saved Connect session.
func signedOutClient(t *testing.T) *gc.Client {
	t.Helper()
	return gc.New(filepath.Join(t.TempDir(), "account.json"))
}
