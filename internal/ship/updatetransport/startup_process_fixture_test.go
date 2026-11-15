package updatetransport_test

import (
	"context"
	"fmt"
	"os"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

const startupVersionArgument = "version"

// This test-only child has no native host, installer or device access.
func startupTestProcess() int {
	if len(os.Args) == 2 && os.Args[1] == startupVersionArgument {
		fmt.Println("garminctl " + os.Getenv("GARMINCTL_TEST_STARTUP_CLI_VERSION"))
		return 0
	}

	notify, err := updatetransport.NewStartupNotice(os.Getenv("GARMINCTL_TEST_STARTUP_CACHE"), os.Getenv(updatetransport.StartupEnvironment), os.Getenv("GARMINCTL_TEST_STARTUP_VERSION"))
	if err == nil && notify != nil {
		err = notify(context.Background(), os.Getenv("GARMINCTL_TEST_STARTUP_BACKEND"), true)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	return 0
}
