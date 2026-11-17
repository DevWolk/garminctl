package cli

import (
	"context"
	"errors"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/clix"
)

const (
	statusCommand = "status"
	statusUsage   = "application status"
)

// status prints what the installer helper recorded for the last installation
// handed over by the application. It starts nothing and verifies no package.
func (c *Command) status(ctx context.Context) error {
	if c.LastInstallation == nil {
		return errors.New("application installation status is not configured")
	}

	outcome, err := c.LastInstallation(ctx)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, struct {
			LastInstallation *application.InstallationOutcome `json:"lastInstallation"`
		}{outcome})
	}

	if outcome == nil {
		c.Out.Println("No installation started from the application has been recorded.")
	} else {
		c.Out.Println(outcome.Summary())
	}

	return c.Out.Err()
}
