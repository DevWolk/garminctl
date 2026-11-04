package garmin

import (
	"context"
	"errors"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/health"
)

// ActivitySummary preserves the health-owned model.
type ActivitySummary = health.ActivitySummary

// ActivityList preserves the health-owned model.
type ActivityList = health.ActivityList

// TrackPoint preserves the health-owned model.
type TrackPoint = health.TrackPoint

// LapSummary preserves the health-owned model.
type LapSummary = health.LapSummary

// ActivityDetail preserves the health-owned model.
type ActivityDetail = health.ActivityDetail

// DailyHealth preserves the health-owned model.
type DailyHealth = health.DailyHealth

// Series preserves the health-owned model.
type Series = health.Series

// DayDetail preserves the health-owned model.
type DayDetail = health.DayDetail

// SleepSummary preserves the health-owned model.
type SleepSummary = health.SleepSummary

// HealthExtras preserves the health-owned model.
type HealthExtras = health.Extras

// HRVStatus preserves the health-owned model.
type HRVStatus = health.HRVStatus

// PersonalRecord preserves the health-owned model.
type PersonalRecord = health.PersonalRecord

// Badge preserves the health-owned model.
type Badge = health.Badge

// WeightEntry preserves the health-owned model.
type WeightEntry = health.WeightEntry

// Activities delegates the direct compatibility path over this Session.
func (s *Service) Activities(ctx context.Context, wait bool) (*ActivityList, error) {
	return (health.ListActivitiesAction{Profile: s.profile}).Run(ctx, s.session, wait)
}

// Activity delegates decoding over this Session.
func (s *Service) Activity(ctx context.Context, name string, points int) (*ActivityDetail, error) {
	return (health.ReadActivityAction{Profile: s.profile}).Run(ctx, s.session, name, points)
}

// HealthAccess temporarily supplies caller-scoped identity and metadata policy.
func (c *Cloud) HealthAccess() health.AccountAccess {
	return health.AccountAccess{Client: c.gc, DisplayName: c.displayName, DeviceXML: c.deviceXML, CacheDir: c.cacheDir}
}

func (c *Cloud) displayName(ctx context.Context) (string, error) {
	st := c.identityStatus(ctx)
	if !st.LoggedIn {
		return "", errors.New("not signed in to Garmin Connect")
	}

	if st.Profile == nil || st.Profile.DisplayName == "" {
		return "", errors.New("the Garmin Connect profile has no display name")
	}

	return st.Profile.DisplayName, nil
}

// Daily delegates to the health owner with the existing policy.
func (c *Cloud) Daily(ctx context.Context, days int) ([]DailyHealth, error) {
	return (health.DailyAction{Access: c.HealthAccess()}).Run(ctx, days)
}

// History delegates to the health owner with the existing policy.
func (c *Cloud) History(ctx context.Context, days int) ([]DailyHealth, error) {
	return (health.HistoryAction{Access: c.HealthAccess()}).Run(ctx, days)
}

// Day delegates to the health owner with the existing policy.
func (c *Cloud) Day(ctx context.Context, date string) (*DayDetail, error) {
	return (health.DayAction{Access: c.HealthAccess()}).Run(ctx, date)
}

// Extras delegates to the health owner with the existing policy.
func (c *Cloud) Extras(ctx context.Context) (*HealthExtras, error) {
	return (health.ExtrasAction{Access: c.HealthAccess()}).Run(ctx)
}

// LogWeight delegates to the health owner with the existing policy.
func (c *Cloud) LogWeight(ctx context.Context, kg float64, when time.Time) error {
	return (health.LogWeightAction{Access: c.HealthAccess()}).Run(ctx, kg, when)
}
