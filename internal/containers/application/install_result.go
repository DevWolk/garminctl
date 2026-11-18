package application

import (
	"fmt"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// Outcomes the installer helper records after an accepted handoff.
const (
	InstallationInstalled = "installed"
	InstallationFailed    = "failed"
	InstallationUnknown   = "unknown"
)

// InstallationOutcome is the helper's record of the last committed
// installation. Directory holds its logs. It is local metadata, not proof of
// the currently installed version.
type InstallationOutcome struct {
	Tag       string    `json:"tag"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	Finished  time.Time `json:"finished"`
	Directory string    `json:"directory"`
}

// Incomplete reports an installation the user still has to look at.
func (o InstallationOutcome) Incomplete() bool { return o.Status != InstallationInstalled }

// Superseded reports that the running version already is the target of the
// record or a later one, for example after the user ran Setup by hand. An
// unreadable version supersedes nothing.
func (o InstallationOutcome) Superseded(running string) bool {
	target, current := semanticVersion(o.Tag), semanticVersion(running)

	return target != "" && current != "" && semver.Compare(current, target) >= 0
}

// Summary states the outcome in one sentence for the Application tab and the CLI.
func (o InstallationOutcome) Summary() string {
	when := o.Finished.Local().Format("2006-01-02 15:04")

	switch o.Status {
	case InstallationInstalled:
		return fmt.Sprintf("The last update installed %s on %s.", o.Tag, when)
	case InstallationUnknown:
		return fmt.Sprintf("The last update to %s was still installing when its monitoring ended on %s; check the installed version before trying again. Logs: %s", o.Tag, when, o.Directory)
	default:
		// The helper joins several causes with line breaks.
		reason := strings.Join(strings.FieldsFunc(o.Error, func(r rune) bool { return r == '\n' || r == '\r' }), "; ")
		if reason == "" {
			reason = "no reason was recorded"
		}

		// A step after the installer can fail, so the programs may already be replaced.
		return fmt.Sprintf("The last update to %s on %s did not complete: %s. The programs may already be replaced; check the installed versions before trying again. Logs: %s", o.Tag, when, reason, o.Directory)
	}
}
