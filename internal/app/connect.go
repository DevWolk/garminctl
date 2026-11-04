package app

import (
	"context"

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
	return connect.Config{Invalidated: func(sess *watchsession.Session, generation uint64) { watch.AppSchemaCacheFor(sess).Reset(generation) }}
}

func connectRunner(watch *garmin.Lazy, account *gc.Client) func(watchsession.ConnectionEpoch, bool, bool) connect.SyncRunner {
	return func(epoch watchsession.ConnectionEpoch, up, down bool) connect.SyncRunner {
		cloud := JobConnect(watch, account, epoch)

		return func(ctx context.Context, progress connect.Progress) (*connect.SyncResult, error) {
			return (connect.SyncAction{Cloud: cloud}).Run(ctx, up, down, progress)
		}
	}
}
