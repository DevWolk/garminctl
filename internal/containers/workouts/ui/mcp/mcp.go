// Package mcp adapts workout Actions to the existing typed MCP tools.
package mcp

import (
	"context"
	"encoding/json"

	"github.com/DevWolk/garminctl/internal/containers/workouts"
	"github.com/DevWolk/garminctl/internal/ship/mcpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tools receives shared owners; it creates no watch or account session.
type Tools struct {
	Manager     *watchsession.Manager
	Client      workouts.Account
	DeliveryFor func(*watchsession.Session) workouts.Delivery
	BindContext func(context.Context) (context.Context, context.CancelFunc)
}
type empty struct{}

// workoutIn mirrors workouts.WorkoutSpec with one level of repeat groups,
// because a schema for a recursive type cannot be inferred.
type workoutIn struct {
	Name        string   `json:"name" jsonschema:"workout name as it will appear on the watch"`
	Sport       string   `json:"sport" jsonschema:"running, cycling, swimming, walking, hiking, strength, cardio, hiit, yoga, pilates or other"`
	Description string   `json:"description,omitempty"`
	Steps       []stepIn `json:"steps" jsonschema:"the steps in order; a step is either one executable step or a repeat group (repeat > 0 with its own steps)"`
}

type stepIn struct {
	leafStepIn
	Repeat int          `json:"repeat,omitempty" jsonschema:"repeat count of a group; then only steps is read"`
	Steps  []leafStepIn `json:"steps,omitempty" jsonschema:"the steps of a repeat group; groups do not nest"`
}

// leafStepIn is one executable step: exactly one end condition and an
// optional target.
type leafStepIn struct {
	Type     string `json:"type,omitempty" jsonschema:"warmup, cooldown, interval, recovery, rest or other"`
	Name     string `json:"name,omitempty" jsonschema:"optional label shown on the watch"`
	Duration string `json:"duration,omitempty" jsonschema:"end after a time: '10:00', '90s', '10m', '1h'"`
	Distance string `json:"distance,omitempty" jsonschema:"end after a distance: '400m', '2km', '1mi'"`
	Calories int    `json:"calories,omitempty" jsonschema:"end after burning this many kcal"`
	Lap      bool   `json:"lap,omitempty" jsonschema:"end with the lap button"`
	Target   string `json:"target,omitempty" jsonschema:"'hr 140-160', 'hr zone 3', 'pace 4:30-4:45' (min/km), 'speed 10-12' (km/h), 'cadence 170-180' or 'power 200-250'"`
}

// spec converts matching JSON fields without duplicating the field mapping.
func (w workoutIn) spec() (workouts.WorkoutSpec, error) {
	var out workouts.WorkoutSpec

	b, err := json.Marshal(w)
	if err != nil {
		return out, err
	}

	err = json.Unmarshal(b, &out)

	return out, err
}

type wkIn struct {
	Spec  workoutIn `json:"spec" jsonschema:"the workout: name, sport and steps"`
	Local bool      `json:"local,omitempty" jsonschema:"skip Garmin Connect: write the workout FIT straight into GARMIN/NewFiles (works without sign-in; the workout will not appear in Connect)"`
}

type wkSendIn struct {
	ID int64 `json:"id" jsonschema:"workout id from workouts_list"`
}

type workoutsResult struct {
	Workouts []workouts.CloudWorkout `json:"workouts"`
}

type pulledResult struct {
	Pulled []workouts.PulledFile `json:"pulled"`
}

type workoutResult struct {
	Workout   *workouts.CloudWorkout `json:"workout,omitempty"`
	Pulled    []workouts.PulledFile  `json:"pulled,omitempty"`
	SendError string                 `json:"sendError,omitempty"`
	Path      string                 `json:"path,omitempty"`
	Note      string                 `json:"note,omitempty"`
}

// RegisterTools preserves the three workout schemas and annotations.
func (t *Tools) RegisterTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "workouts_list",
		Description: "List Garmin Connect workouts with id, name, sport and estimated duration. Use id with workout_send.",
		Annotations: mcpx.ReadOnly("List workouts", true),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, _ *watchsession.Lease, _ empty) (workoutsResult, error) {
		list, err := (workouts.ListAction{Client: t.Client}).Run(ctx, 0) // default page
		return workoutsResult{Workouts: list}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "workout_create",
		Description: "Create a workout in Garmin Connect and attempt USB delivery. With local=true, save only to the watch. Unplug after delivery to load the workout.",
		Annotations: mcpx.Writes("Create workout", false, true),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in wkIn) (workoutResult, error) {
		spec, err := in.Spec.spec()
		if err != nil {
			return workoutResult{}, err
		}

		if in.Local {
			p, err := (workouts.WriteLocalAction{}).Run(ctx, lease.Session(), &spec)
			if err != nil {
				return workoutResult{}, err
			}

			return workoutResult{Path: p, Note: "written to GARMIN/NewFiles; unplug the watch to see it"}, nil
		}

		wk, err := (workouts.CreateAction{Client: t.Client}).Run(ctx, &spec)
		if err != nil {
			return workoutResult{}, err
		}

		out := workoutResult{Workout: wk}

		pulled, err := t.send(ctx, lease.Session(), wk.ID, wk.Name)
		if err != nil {
			out.SendError = err.Error()
		} else {
			out.Pulled = pulled
		}

		return out, nil
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "workout_send",
		Description: "Queue a Garmin Connect workout and attempt USB delivery. Unplug after delivery to load it; sync later if no file is returned.",
		Annotations: mcpx.Writes("Send workout to watch", false, true),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in wkSendIn) (pulledResult, error) {
		pulled, err := t.send(ctx, lease.Session(), in.ID, "")
		return pulledResult{Pulled: pulled}, err
	}))
}

func (t *Tools) send(ctx context.Context, sess *watchsession.Session, id int64, name string) ([]workouts.PulledFile, error) {
	return (workouts.SendAction{Client: t.Client, Queue: t.DeliveryFor(sess)}).Run(ctx, id, name)
}
