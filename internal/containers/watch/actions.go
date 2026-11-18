package watch

import (
	"context"
	"fmt"

	"github.com/DevWolk/garminctl/internal/ship/fit"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// AppCount supplies the installed-registry count for this same leased Session.
// Its owner defines registry parsing; Summary keeps the current optional-error policy.
type AppCount func(context.Context, *watchsession.Session) (int, error)

// SummaryAction combines required metadata with optional headline counts/storage.
type SummaryAction struct {
	profile *fit.Profile
	apps    AppCount
}

// NewSummaryAction uses the supplied profile and registry-count capability.
func NewSummaryAction(profile *fit.Profile, apps AppCount) *SummaryAction {
	return &SummaryAction{profile: profile, apps: apps}
}

// Run gathers the headline facts about the watch without owning its Session.
func (a *SummaryAction) Run(ctx context.Context, sess *watchsession.Session) (*Summary, error) {
	dx, err := sess.DeviceXML(ctx)
	if err != nil {
		return nil, err
	}

	sum := &Summary{Device: sess.Info(), Model: dx.Description, PartNumber: dx.PartNumber, UnitID: dx.UnitID, Software: dx.SoftwareVersionString(), ProfileVers: a.profile.Version}
	if count, err := a.apps(ctx, sess); err == nil {
		sum.AppCount = count
	}

	if entries, err := sess.List(ctx, watchsession.PathNewFiles); err == nil {
		sum.NewFiles = len(entries)
	}

	if st, err := sess.StorageInfo(ctx); err == nil && st.CapacityBytes > 0 {
		sum.Storage = st
	}

	return sum, nil
}

// DecodeAction names FIT records through the supplied profile.
type DecodeAction struct{ profile *fit.Profile }

// NewDecodeAction uses the supplied profile without owning a device or cache.
func NewDecodeAction(profile *fit.Profile) *DecodeAction { return &DecodeAction{profile: profile} }

// Run reads fresh FIT bytes while retaining the existing cache-population policy.
func (a *DecodeAction) Run(ctx context.Context, sess *watchsession.Session, p string) ([]fit.NamedMessage, error) {
	b, err := sess.ReadThrough(ctx, p, false)
	if err != nil {
		return nil, err
	}

	f, err := fit.Decode(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}

	return a.profile.Describe(f), nil
}
