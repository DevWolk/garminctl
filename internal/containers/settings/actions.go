// Package settings reads, describes and queues watch settings changes.
package settings

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/fit"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// ReadAction reads the watch settings with independent knowledge snapshots.
type ReadAction struct{ profile *fit.Profile }

// NewReadAction uses the supplied FIT profile.
func NewReadAction(profile *fit.Profile) *ReadAction { return &ReadAction{profile: profile} }

// Run reads the cached FIT bytes and returns a described view.
func (a *ReadAction) Run(ctx context.Context, sess *watchsession.Session) (*View, error) {
	return loadSettingsTask(ctx, sess, a.profile)
}

// ApplyAction resolves edits and queues one new FIT file per invocation.
type ApplyAction struct{ profile *fit.Profile }

// NewApplyAction uses the supplied FIT profile for explicit edits.
func NewApplyAction(profile *fit.Profile) *ApplyAction { return &ApplyAction{profile: profile} }

// SchemaAction describes one message through the supplied FIT profile.
type SchemaAction struct{ profile *fit.Profile }

// NewSchemaAction uses the supplied FIT profile for field descriptions.
func NewSchemaAction(profile *fit.Profile) *SchemaAction { return &SchemaAction{profile: profile} }

// Run writes a settings-change file to GARMIN/NewFiles.
func (a *ApplyAction) Run(ctx context.Context, sess *watchsession.Session, changes []Change) (*ApplyResult, error) {
	if len(changes) == 0 {
		return nil, errors.New("no changes given")
	}

	view, err := loadSettingsTask(ctx, sess, a.profile)
	if err != nil {
		return nil, err
	}

	dx, err := sess.DeviceXML(ctx)
	if err != nil {
		return nil, err
	}

	if dx.SoftwareVersion < 0 || dx.SoftwareVersion > math.MaxUint16 {
		return nil, fmt.Errorf("software version %d in GarminDevice.xml is out of range", dx.SoftwareVersion)
	}

	var resolved []ResolvedChange

	for _, c := range changes {
		rc, err := Resolve(a.profile, c, view)
		if err != nil {
			return nil, err
		}

		resolved = append(resolved, rc)
	}

	data, err := BuildChangeFile(resolved, dx.UnitID, uint16(dx.SoftwareVersion), time.Now())
	if err != nil {
		return nil, err
	}

	name := fmt.Sprintf("%s/%s.FIT", PathNewFiles, watchsession.NewFileID())
	if err := sess.WriteFile(ctx, name, data); err != nil {
		return nil, fmt.Errorf("write %s: %w", name, err)
	}

	sess.Invalidate(PathSettings)

	note := "Disconnect the watch to apply the settings."

	var managed []string

	for _, rc := range resolved {
		if defaultKnowledge().ManagedByConnect(rc.MesgNum, rc.FieldNum) {
			managed = append(managed, rc.Mesg+"."+rc.Field)
		}
	}

	if len(managed) > 0 {
		note += " Warning: Garmin Connect may overwrite " + strings.Join(managed, ", ") + " during sync. Set the same values in Garmin Connect to keep them."
	}

	return &ApplyResult{Path: name, Bytes: len(data), Changes: resolved, Note: note}, nil
}

// Run lists the documented fields of a message.
func (a *SchemaAction) Run(mesg string) ([]SchemaField, error) {
	var mp *fit.MesgProfile

	if n, err := strconv.Atoi(mesg); err == nil {
		if n >= 0 && n <= math.MaxUint16 {
			mp = a.profile.Messages[uint16(n)]
		}
	} else if m, ok := a.profile.MessageByName(mesg); ok {
		mp = m
	}

	if mp == nil {
		return nil, fmt.Errorf("unknown message %q", mesg)
	}

	var out []SchemaField
	for _, f := range mp.Fields {
		out = append(out, SchemaField{Num: f.Num, Name: f.Name, Type: f.Type, Base: f.BaseType, Array: f.Array, Units: f.Units, Options: enumOptions(a.profile, f.Type)})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Num < out[j].Num })

	return out, nil
}
