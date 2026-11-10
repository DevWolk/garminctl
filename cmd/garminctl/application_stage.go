package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/app/cli"
	"github.com/DevWolk/garminctl/internal/ship/clix"
)

// updatePublicKey is base64 RSA-3072 SubjectPublicKeyInfo DER set by the build. Empty builds
// refuse staging. No flag, environment value or IPC field can choose the key.
var updatePublicKey string

func runCLI(args []string, input io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return cli.Run(version, args, stdout, stderr)
	}

	var worker func(context.Context, string, string, io.Reader, io.Writer) error

	switch args[0] {
	case app.ApplicationStageCommand:
		worker = app.RunApplicationStage
	case app.ApplicationVerifyCommand:
		worker = app.RunApplicationVerify
	default:
		return cli.Run(version, args, stdout, stderr)
	}

	if len(args) != 1 {
		return clix.UsageErrorf("private staging command takes no arguments")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return worker(ctx, version, updatePublicKey, input, stdout)
}
