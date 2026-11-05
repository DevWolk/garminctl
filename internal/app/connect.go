package app

import (
	"context"
	"fmt"

	"github.com/DevWolk/garminctl/internal/containers/connect"
	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// ConnectFor borrows the caller's leased Session and canonical schema cache.
func ConnectFor(watch *garmin.Lazy, account *gc.Client) func(*watchsession.Session) *connect.Cloud {
	return func(sess *watchsession.Session) *connect.Cloud {
		return connect.NewCloud(sess, account, connectConfig(watch))
	}
}

// JobConnect binds bounded groups to the existing watch epoch, never a new owner.
func JobConnect(watch *garmin.Lazy, account *gc.Client, epoch watchsession.ConnectionEpoch) *connect.Cloud {
	return connect.NewJobCloud(watch.Manager(), epoch, account, connectConfig(watch))
}

func connectConfig(watch *garmin.Lazy) connect.Config {
	return connect.Config{
		Invalidated: func(sess *watchsession.Session, generation uint64) { watch.AppSchemaCacheFor(sess).Reset(generation) },
		Claims:      watch.MessageClaims(),
	}
}

func connectRunner(watch *garmin.Lazy, account *gc.Client) func(watchsession.ConnectionEpoch, bool, bool) connect.SyncRunner {
	return func(epoch watchsession.ConnectionEpoch, up, down bool) connect.SyncRunner {
		cloud := JobConnect(watch, account, epoch)

		return func(ctx context.Context, progress connect.Progress) (*connect.SyncResult, error) {
			return (connect.SyncAction{Cloud: cloud}).Run(ctx, up, down, progress)
		}
	}
}

// autoSyncNotes runs or joins the plug-in sync of epoch and describes its outcome.
// ok is false when ctx ended before the sync finished.
func autoSyncNotes(coordinator *connect.SyncCoordinator, runner func(watchsession.ConnectionEpoch, bool, bool) connect.SyncRunner) func(context.Context, watchsession.ConnectionEpoch) ([]string, bool) {
	return func(ctx context.Context, epoch watchsession.ConnectionEpoch) ([]string, bool) {
		job, _, err := coordinator.JoinOrStart(ctx, connect.SyncOptions{Upload: true, Download: true, Epoch: epoch}, runner(epoch, true, true))
		if err != nil {
			return []string{"sync failed: " + err.Error()}, true
		}

		select {
		case <-job.Done():
			return syncNotes(job), true
		case <-ctx.Done():
			return nil, false
		}
	}
}

// syncNotes describes the outcome of a finished sync job in a few phrases.
func syncNotes(job *connect.SyncJob) []string {
	state := job.Snapshot()
	res, errText := state.Result, state.Error

	if errText != "" {
		return []string{"sync failed: " + errText}
	}

	if res == nil {
		return nil
	}

	var parts []string

	if res.Upload != nil && res.Upload.Uploaded > 0 {
		parts = append(parts, fmt.Sprintf("%d file(s) uploaded to Garmin Connect", res.Upload.Uploaded))
	}

	if len(res.Pulled) > 0 {
		parts = append(parts, fmt.Sprintf("%d item(s) from Connect written to the watch", len(res.Pulled)))
	}

	if len(res.Errors) > 0 {
		parts = append(parts, fmt.Sprintf("%d sync error(s)", len(res.Errors)))
	}

	return parts
}
