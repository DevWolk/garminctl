package cli

import "io"

// run is Run for a development build.
func run(args []string, stdout, stderr io.Writer) error {
	return Run("dev", args, stdout, stderr)
}
