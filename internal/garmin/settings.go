package garmin

import (
	"context"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/settings"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Paths on the watch.
const (
	PathSettings    = watchsession.PathSettings
	PathDeviceXML   = watchsession.PathDeviceXML
	PathDeviceFIT   = watchsession.PathDeviceFIT
	PathAppsConfig  = watchsession.PathAppsConfig
	PathAppSettings = watchsession.PathAppSettings
	PathNewFiles    = watchsession.PathNewFiles
	PathSports      = watchsession.PathSports
)

const (
	// FileTypeSettingsChange is the pending settings file type.
	FileTypeSettingsChange = settings.FileTypeSettingsChange
	// MesgSettingsChange is the pending settings change message number.
	MesgSettingsChange = settings.MesgSettingsChange
)

type (
	// SettingsView preserves the public settings view model.
	SettingsView = settings.View
	// Change preserves one requested settings edit.
	Change = settings.Change
	// ResolvedChange preserves numeric edit resolution and previous values.
	ResolvedChange = settings.ResolvedChange
	// ApplyResult preserves the queued-file response.
	ApplyResult = settings.ApplyResult
	// SettingsRow preserves the flattened settings JSON model.
	SettingsRow = settings.Row
	// SchemaField preserves one documented settings field.
	SchemaField = settings.SchemaField
)

// Resolve maps an edit through the current profile and prior view.
func (s *Service) Resolve(change Change, view *SettingsView) (ResolvedChange, error) {
	return settings.Resolve(s.profile, change, view)
}

// BuildChangeFile preserves the pending FIT encoding contract.
func BuildChangeFile(changes []ResolvedChange, unitID uint64, swVersion uint16, now time.Time) ([]byte, error) {
	return settings.BuildChangeFile(changes, unitID, swVersion, now)
}

// ApplyChanges queues one settings-change file.
func (s *Service) ApplyChanges(ctx context.Context, changes []Change) (*ApplyResult, error) {
	return settings.NewApplyAction(s.profile).Run(ctx, s.session, changes)
}

// ParseChangeSpec parses the CLI settings edit syntax.
func ParseChangeSpec(spec string) (Change, error) { return settings.ParseChangeSpec(spec) }

// Schema lists documented fields through the current profile.
func (s *Service) Schema(message string) ([]SchemaField, error) {
	return settings.NewSchemaAction(s.profile).Run(message)
}
