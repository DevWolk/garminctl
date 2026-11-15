package app

import (
	"context"
	"os"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

// ApplicationStartupNotice consumes the private restart ticket before any children start.
// The returned callback writes only after the real native frontend reports Ready.
func ApplicationStartupNotice(version string) (func(context.Context, string, bool) error, error) {
	encoded := os.Getenv(updatetransport.StartupEnvironment)
	if err := os.Unsetenv(updatetransport.StartupEnvironment); err != nil {
		return nil, err
	}

	return updatetransport.NewStartupNotice(applicationInstallCache(), encoded, version)
}
