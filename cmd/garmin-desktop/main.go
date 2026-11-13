//go:build desktop

// garmin-desktop starts the local server and opens its native application window.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/desktop"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/devicehost"
	"github.com/wailsapp/wails/v3/pkg/application"
)

var version = "dev"

// updatePublicKey is compiled into both binaries; requests cannot set it.
var updatePublicKey string

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:8787", "local server address")
	root := flag.String("root", "", "watch directory for offline testing")

	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	open := func(ctx context.Context) (device.Device, error) {
		return devicehost.Open(ctx, *root, "")
	}

	backend := new(app.ApplicationBackend)
	serve := func(ctx context.Context, listener net.Listener) error {
		return backend.ServeListener(ctx, listener, open, *root == "", version)
	}

	return desktop.Run(func() (*desktop.Backend, error) { return desktop.StartBackend(ctx, *addr, serve) }, func(scope desktop.Scope) []application.Service {
		return []application.Service{app.NewApplicationNativeService(version, updatePublicKey, scope, backend, os.Args[1:]...)}
	})
}
