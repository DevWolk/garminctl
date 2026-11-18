package app

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/connect"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func TestAutoSyncNotesDescribeFinishedJob(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		result *connect.SyncResult
		err    error
		want   []string
	}{
		{
			name: "work done", result: &connect.SyncResult{Upload: &connect.UploadResult{Uploaded: 2}, Pulled: []connect.PulledFile{{Path: "GARMIN/NewFiles/A.FIT"}}, Errors: []string{"one"}},
			want: []string{"2 file(s) uploaded to Garmin Connect", "1 item(s) from Connect written to the watch", "1 sync error(s)"},
		},
		{
			name: "failed transfers", result: &connect.SyncResult{Upload: &connect.UploadResult{Failed: 1}, Pulled: []connect.PulledFile{{Error: "watch full"}, {Error: "watch full"}}},
			want: []string{"1 upload(s) failed", "2 item(s) from Connect not written: watch full"},
		},
		{name: "nothing new", result: &connect.SyncResult{Upload: &connect.UploadResult{}}},
		{name: "failed", err: errors.New("synthetic failure"), want: []string{"sync failed: synthetic failure"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			coordinator := connect.NewSyncCoordinator(t.Context(), new(lifecycle.Gate))
			t.Cleanup(func() { _ = coordinator.Close(context.WithoutCancel(t.Context())) })

			var got []connect.SyncOptions

			runner := func(epoch watchsession.ConnectionEpoch, up, down bool) connect.SyncRunner {
				got = append(got, connect.SyncOptions{Upload: up, Download: down, Epoch: epoch})

				return func(context.Context, connect.Progress) (*connect.SyncResult, error) { return tt.result, tt.err }
			}

			notes, ok := autoSyncNotes(coordinator, runner)(t.Context(), 7)
			if !ok || !slices.Equal(notes, tt.want) || len(got) != 1 || got[0] != (connect.SyncOptions{Upload: true, Download: true, Epoch: 7}) {
				t.Fatalf("notes=%q ok=%t runner calls=%+v", notes, ok, got)
			}
		})
	}
}

func TestAutoSyncNotesReportStartFailureAndCancellation(t *testing.T) {
	t.Parallel()

	gate := new(lifecycle.Gate)
	gate.Stop()

	stopped := connect.NewSyncCoordinator(t.Context(), gate)
	started := make(chan struct{})
	runner := func(watchsession.ConnectionEpoch, bool, bool) connect.SyncRunner {
		return func(ctx context.Context, _ connect.Progress) (*connect.SyncResult, error) {
			close(started)
			<-ctx.Done()

			return nil, ctx.Err()
		}
	}

	if notes, ok := autoSyncNotes(stopped, runner)(t.Context(), 1); !ok || len(notes) != 1 || notes[0][:len("sync failed: ")] != "sync failed: " {
		t.Fatalf("start failure notes=%q ok=%t", notes, ok)
	}

	coordinator := connect.NewSyncCoordinator(t.Context(), new(lifecycle.Gate))
	t.Cleanup(func() { _ = coordinator.Close(context.WithoutCancel(t.Context())) })

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		if notes, ok := autoSyncNotes(coordinator, runner)(ctx, 1); ok || notes != nil {
			t.Errorf("cancelled wait notes=%q ok=%t", notes, ok)
		}
	}()

	<-started
	cancel()
	<-done
}
