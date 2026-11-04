package health

import (
	"context"
	"time"

	"github.com/DevWolk/garminctl/internal/deviceinfo"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Account is the health protocol supplied by the existing Connect client.
type Account interface {
	GetJSON(context.Context, string, any) error
	SendJSON(context.Context, string, string, any, any) error
}

// AccountAccess borrows account/identity/metadata and its existing cache policy.
// DisplayName retains its caller-owned profile cache and error behavior.
type AccountAccess struct {
	Client      Account
	DisplayName func(context.Context) (string, error)
	DeviceXML   func(context.Context) (*deviceinfo.DeviceXML, error)
	CacheDir    string
}
type accountReader struct {
	gc          Account
	displayName func(context.Context) (string, error)
	deviceXML   func(context.Context) (*deviceinfo.DeviceXML, error)
	cacheDir    string
}

func newAccountReader(access AccountAccess) *accountReader {
	return &accountReader{gc: access.Client, displayName: access.DisplayName, deviceXML: access.DeviceXML, cacheDir: access.CacheDir}
}

// ReadActivityAction decodes one activity using the caller's FIT profile.
type ReadActivityAction struct{ Profile *fit.Profile }

// Run retains path validation, track resolution and lap extraction.
func (a ReadActivityAction) Run(ctx context.Context, sess *watchsession.Session, name string, points int) (*ActivityDetail, error) {
	return newActivityReader(a.Profile, sess).Activity(ctx, name, points)
}

// ListActivitiesAction preserves the unmanaged compatibility decode path.
// Managed adapters use the shared ActivityCoordinator rather than an outer lease.
type ListActivitiesAction struct{ Profile *fit.Profile }

// Run retains same-cache joining for direct Session callers.
func (a ListActivitiesAction) Run(ctx context.Context, sess *watchsession.Session, wait bool) (*ActivityList, error) {
	return newActivityReader(a.Profile, sess).Activities(ctx, wait)
}

// DailyAction reads short daily summaries.
type DailyAction struct{ Access AccountAccess }

// Run preserves defaults, bounds, concurrency and per-day errors.
func (a DailyAction) Run(ctx context.Context, days int) ([]DailyHealth, error) {
	return newAccountReader(a.Access).daily(ctx, days)
}

// HistoryAction reads/cache-merges Connect range endpoints.
type HistoryAction struct{ Access AccountAccess }

// Run retains windows, final-day policy and partial range behavior.
func (a HistoryAction) Run(ctx context.Context, days int) ([]DailyHealth, error) {
	return newAccountReader(a.Access).history(ctx, days)
}

// DayAction reads intraday series and sleep.
type DayAction struct{ Access AccountAccess }

// Run retains date validation and endpoint-specific warnings.
func (a DayAction) Run(ctx context.Context, date string) (*DayDetail, error) {
	return newAccountReader(a.Access).day(ctx, date)
}

// ExtrasAction reads optional fitness metrics, records, badges and weight.
type ExtrasAction struct{ Access AccountAccess }

// Run retains optional failures in the result's errors list.
func (a ExtrasAction) Run(ctx context.Context) (*Extras, error) {
	return newAccountReader(a.Access).extras(ctx)
}

// LogWeightAction records one manual weigh-in.
type LogWeightAction struct{ Access AccountAccess }

// Run preserves validation and timestamp/unit payloads without retries.
func (a LogWeightAction) Run(ctx context.Context, kg float64, when time.Time) error {
	return newAccountReader(a.Access).logWeight(ctx, kg, when)
}
