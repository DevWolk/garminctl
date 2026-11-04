package app

import (
	"github.com/DevWolk/garminctl/internal/containers/workouts"
	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// WorkoutDelivery binds the temporary queue provider to the existing owners.
// Each result is used only while the supplied Session's lease remains held.
func WorkoutDelivery(watch *garmin.Lazy, account *gc.Client) func(*watchsession.Session) workouts.Delivery {
	return func(sess *watchsession.Session) workouts.Delivery { return watch.WorkoutDeliveryFor(sess, account) }
}
