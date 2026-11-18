// Package mcp adapts health Actions and shared activity jobs to typed tools.
package mcp

import (
	"context"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/health"
	"github.com/DevWolk/garminctl/internal/ship/fit"
	"github.com/DevWolk/garminctl/internal/ship/mcpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultActivities = 30
	defaultPoints     = 200
)

// Tools borrows one process's owners and caller-scoped account policy.
type Tools struct {
	Manager     *watchsession.Manager
	Profile     *fit.Profile
	Activities  *health.ActivityCoordinator
	AccessFor   func(*watchsession.Session) health.AccountAccess
	BindContext func(context.Context) (context.Context, context.CancelFunc)
}
type (
	empty  struct{}
	actsIn struct {
		Limit int `json:"limit,omitempty" jsonschema:"how many of the newest activities to return (default 30)"`
	}
)

type actIn struct {
	File   string `json:"file" jsonschema:"activity file name from activities_list"`
	Points int    `json:"points,omitempty" jsonschema:"max track points to include (default 200; a negative value keeps laps only)"`
}

type daysIn struct {
	Days int `json:"days,omitempty" jsonschema:"days including today; default 14 for health_daily, 30 for health_history"`
}

type weightIn struct {
	Kg float64 `json:"kg" jsonschema:"body weight in kilograms"`
}

type dayIn struct {
	Date string `json:"date" jsonschema:"YYYY-MM-DD"`
}

type daysResult struct {
	Days []health.DailyHealth `json:"days"`
}

type weightResult struct {
	OK bool    `json:"ok"`
	Kg float64 `json:"kg"`
}

// RegisterTools preserves seven health tools and exact schemas/annotations.
func (t *Tools) RegisterTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "activities_list",
		Description: "List recorded activities with time, sport, distance, calories and heart rate. Call again while pending > 0. Use file with activity_detail.",
		Annotations: mcpx.ReadOnly("List activities", false),
	}, t.activitiesTool())

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "activity_detail",
		Description: "One activity with laps and a downsampled track (time, lat/lon, heart rate, speed, altitude).",
		Annotations: mcpx.ReadOnly("Activity detail", false),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in actIn) (*health.ActivityDetail, error) {
		n := in.Points
		if n == 0 {
			n = defaultPoints
		}

		d, err := (health.ReadActivityAction{Profile: t.Profile}).Run(ctx, lease.Session(), in.File, n)
		if err != nil {
			return nil, err
		}

		if in.Points < 0 {
			d.Points = nil
		}

		return d, nil
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "health_daily",
		Description: "Read daily health from Garmin Connect, including steps, heart rate, stress, sleep and calories. Makes one request per day; use health_history for longer ranges. Sign-in required.",
		Annotations: mcpx.ReadOnly("Daily health", true),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in daysIn) (daysResult, error) {
		days, err := (health.DailyAction{Access: t.AccessFor(lease.Session())}).Run(ctx, in.Days)
		return daysResult{Days: days}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "health_history",
		Description: "Read up to a year of daily steps, distance, resting heart rate, stress, body battery and sleep from Garmin Connect. Excludes calories and intensity minutes. Sign-in required.",
		Annotations: mcpx.ReadOnly("Health history", true),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in daysIn) (daysResult, error) {
		days, err := (health.HistoryAction{Access: t.AccessFor(lease.Session())}).Run(ctx, in.Days)
		return daysResult{Days: days}, err
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "health_extras",
		Description: "Read VO2 max, fitness age, HRV, records, badges and weight history from Garmin Connect. Sign-in required.",
		Annotations: mcpx.ReadOnly("Fitness metrics", true),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, _ empty) (*health.Extras, error) {
		return (health.ExtrasAction{Access: t.AccessFor(lease.Session())}).Run(ctx)
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "health_log_weight",
		Description: "Record a manual weigh-in (kg, now) in Garmin Connect. Each call adds one entry.",
		Annotations: mcpx.Writes("Log weight", false, true),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in weightIn) (weightResult, error) {
		if err := (health.LogWeightAction{Access: t.AccessFor(lease.Session())}).Run(ctx, in.Kg, time.Time{}); err != nil {
			return weightResult{}, err
		}

		return weightResult{OK: true, Kg: in.Kg}, nil
	}))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "health_day",
		Description: "Read one day's heart rate, stress and body battery series ([Unix ms, value]), sleep stages and sleep score from Garmin Connect. Sign-in required.",
		Annotations: mcpx.ReadOnly("Intraday health", true),
	}, mcpx.WithWatch(t.Manager, t.BindContext, func(ctx context.Context, lease *watchsession.Lease, in dayIn) (*health.DayDetail, error) {
		return (health.DayAction{Access: t.AccessFor(lease.Session())}).Run(ctx, in.Date)
	}))
}

func (t *Tools) activitiesTool() mcp.ToolHandlerFor[actsIn, *health.ActivityList] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in actsIn) (*mcp.CallToolResult, *health.ActivityList, error) {
		if t.BindContext != nil {
			bound, cancel := t.BindContext(ctx)
			defer cancel()

			ctx = bound
		}

		list, err := t.Activities.Activities(ctx, false)
		if err != nil {
			return nil, nil, err
		}

		n := in.Limit
		if n <= 0 {
			n = defaultActivities
		}

		if len(list.Activities) > n {
			list.Activities = list.Activities[:n]
		}

		return nil, list, nil
	}
}
