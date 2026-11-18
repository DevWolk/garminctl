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
	// From is the build that handed the installation over; older records lack it.
	From string `json:"from,omitempty"`
}

// Incomplete reports an installation the user still has to look at.
func (o InstallationOutcome) Incomplete() bool { return o.Status != InstallationInstalled }

// Resolved reports that the record no longer describes the installed
// programs: desktop and CLI agree, and their version changed since the build
// that handed the installation over, for example because the helper replaced
// both or the user ran Setup by hand. A failed rollback leaves the handing-over
// build running and stays reported. A record without its source build counts
// as resolved once the programs reach its target or a later version.
func (o InstallationOutcome) Resolved(desktop, cli string) bool {
	running := semanticVersion(desktop)
	if running == "" || running != semanticVersion(cli) {
		return false
	}

	if from := semanticVersion(o.From); from != "" {
		return running != from
	}

	target := semanticVersion(o.Tag)

	return target != "" && semver.Compare(running, target) >= 0
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
