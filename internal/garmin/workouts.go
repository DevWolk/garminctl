package garmin

import (
	"context"

	"github.com/DevWolk/garminctl/internal/containers/workouts"
	"github.com/DevWolk/garminctl/internal/deviceinfo"
)

// WorkoutSpec preserves the workout-owned specification.
type WorkoutSpec = workouts.WorkoutSpec

// StepSpec preserves one workout step or repeat group.
type StepSpec = workouts.StepSpec

// CloudWorkout preserves the workout-owned Connect record.
type CloudWorkout = workouts.CloudWorkout

// WriteWorkoutToWatch delegates local staging over the existing Session.
func (s *Service) WriteWorkoutToWatch(ctx context.Context, spec *WorkoutSpec) (string, error) {
	return (workouts.WriteLocalAction{}).Run(ctx, s.session, spec)
}

// Workouts delegates account listing.
func (c *Cloud) Workouts(ctx context.Context, limit int) ([]CloudWorkout, error) {
	return (workouts.ListAction{Client: c.Client()}).Run(ctx, limit)
}

// CreateWorkout delegates validation and Connect creation.
func (c *Cloud) CreateWorkout(ctx context.Context, spec *WorkoutSpec) (*CloudWorkout, error) {
	return (workouts.CreateAction{Client: c.Client()}).Run(ctx, spec)
}

// SendWorkout delegates queue submission and filtered delivery.
func (c *Cloud) SendWorkout(ctx context.Context, id int64, name string) ([]PulledFile, error) {
	return (workouts.SendAction{Client: c.Client(), Queue: c.WorkoutDelivery()}).Run(ctx, id, name)
}

// DeleteWorkout delegates account deletion.
func (c *Cloud) DeleteWorkout(ctx context.Context, id int64) error {
	return (workouts.DeleteAction{Client: c.Client()}).Run(ctx, id)
}

// WorkoutDelivery is the temporary Connect queue bridge until INV-18.
// It preserves both already-leased and job-bound Cloud watch access.
func (c *Cloud) WorkoutDelivery() workouts.Delivery { return workoutDelivery{cloud: c} }

type workoutDelivery struct{ cloud *Cloud }

func (d workoutDelivery) DeviceXML(ctx context.Context) (*deviceinfo.DeviceXML, error) {
	return d.cloud.deviceXML(ctx)
}

func (d workoutDelivery) PullQueued(ctx context.Context) ([]PulledFile, error) {
	return d.cloud.pullQueued(ctx, "workouts")
}
