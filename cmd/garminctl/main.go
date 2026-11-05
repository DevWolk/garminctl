// Command garminctl manages Garmin watches over USB and syncs with Garmin Connect.
package main

import (
	"fmt"
	"os"

	"github.com/DevWolk/garminctl/internal/app/cli"
	"github.com/DevWolk/garminctl/internal/ship/clix"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	err := cli.Run(version, os.Args[1:], os.Stdout, os.Stderr)

	code := clix.ExitCode(err)
	if code != 0 {
		fmt.Fprintln(os.Stderr, "error:", err)
	}

	os.Exit(code)
}
