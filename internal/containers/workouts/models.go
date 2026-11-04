package workouts

import "github.com/DevWolk/garminctl/internal/containers/connect"

// PulledFile is the Connect-owned result of queued delivery.
type PulledFile = connect.PulledFile

// CloudWorkout is a workout stored in Garmin Connect.
type CloudWorkout struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Sport    string `json:"sport"`
	Duration int    `json:"estimatedSec"`
	Updated  string `json:"updated,omitempty"`
}
