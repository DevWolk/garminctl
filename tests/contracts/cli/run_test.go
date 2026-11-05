package cli

import (
	"io"

	appcli "github.com/DevWolk/garminctl/internal/app/cli"
	"github.com/DevWolk/garminctl/internal/ship/clix"
)

const (
	rootFlag           = "-root"
	musicAddSubcommand = "add"
)

// run is the garminctl command line of a development build.
func run(args []string, stdout, stderr io.Writer) error {
	return appcli.Run("dev", args, stdout, stderr)
}

func exitCode(err error) int { return clix.ExitCode(err) }
