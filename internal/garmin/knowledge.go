package garmin

import (
	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/containers/settings"
)

// AppHints preserves the app-owned hint model during facade migration.
type AppHints = apps.AppHints

// AppHintsFor returns an independent snapshot from the apps owner.
func AppHintsFor(appID string) *AppHints { return apps.AppHintsFor(appID) }

// Note preserves the public settings knowledge model.
type Note = settings.Note

// Knowledge preserves the public settings knowledge model.
type Knowledge = settings.Knowledge

// DefaultKnowledge returns an independent settings knowledge snapshot.
func DefaultKnowledge() *Knowledge { return settings.DefaultKnowledge() }
