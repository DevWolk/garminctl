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
	case app.ApplicationInstallCommand:
		worker = runInstallerWorker
	default:
		return cli.Run(version, args, stdout, stderr)
	}

	if len(args) != 1 {
		return clix.UsageErrorf("private application command takes no arguments")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return worker(ctx, version, updatePublicKey, input, stdout)
}

func runInstallerWorker(ctx context.Context, version, key string, input io.Reader, output io.Writer) error {
	return installerPipeScope(func() error { return app.RunApplicationInstall(ctx, version, key, input, output) })
}

func installerPipeScope(run func() error) error {
	// A lost acknowledgement on fd 1 must return EPIPE instead of terminating a
	// committed executor before it can wait for its parent and finish its work.
	broken := make(chan os.Signal, 1)

	signal.Notify(broken, syscall.SIGPIPE)
	defer signal.Stop(broken)

	return run()
}
