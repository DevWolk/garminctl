package app

import (
	"context"

	"github.com/DevWolk/garminctl/internal/containers/connect"
	"github.com/DevWolk/garminctl/internal/containers/workouts"
	"github.com/DevWolk/garminctl/internal/deviceinfo"
	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// WorkoutDelivery binds the Connect queue provider to the existing owners.
// Each result is used only while the supplied Session's lease remains held.
func WorkoutDelivery(watch *garmin.Lazy, account *gc.Client) func(*watchsession.Session) workouts.Delivery {
	cloudFor := ConnectFor(watch, account)
	return func(sess *watchsession.Session) workouts.Delivery { return workoutQueue{cloud: cloudFor(sess)} }
}

// workoutQueue narrows Connect delivery to the workouts consumer contract.
type workoutQueue struct{ cloud *connect.Cloud }

func (q workoutQueue) DeviceXML(ctx context.Context) (*deviceinfo.DeviceXML, error) {
	return q.cloud.DeviceXML(ctx)
}

func (q workoutQueue) PullQueued(ctx context.Context) ([]connect.PulledFile, error) {
	return q.cloud.PullQueued(ctx, "workouts")
}
