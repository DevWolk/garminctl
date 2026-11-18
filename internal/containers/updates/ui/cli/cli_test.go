package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/containers/updates"
	"github.com/DevWolk/garminctl/internal/ship/clix"
	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

type updatesReadDevice struct {
	device.Device
	appErr     error
	onAppsRead func()
}

func (d updatesReadDevice) Read(ctx context.Context, path string) ([]byte, error) {
	if path == watchsession.PathAppsConfig {
		if d.onAppsRead != nil {
			d.onAppsRead()
		}
		// root.3 -> config.2 -> list.3 -> App.name(3); native apps have no store lookup.
		return []byte("\x1a\x0c\x12\x0a\x1a\x08\x1a\x06native"), d.appErr
	}

	return d.Device.Read(ctx, path)
}

func TestUpdatesReportsPartialCheckFailures(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name                   string
		softwareFail, appsFail bool
		cancelOnApps           bool
	}{
		{name: "success"},
		{name: "software failed", softwareFail: true},
		{name: "apps failed", appsFail: true},
		{name: "both failed", softwareFail: true, appsFail: true},
		{name: "cancelled", appsFail: true, cancelOnApps: true},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", tt.name, asJSON), func(t *testing.T) {
				t.Parallel()

				dev, err := device.NewDirFS(testdump.Root(t))
				if err != nil {
					t.Fatal(err)
				}

				t.Cleanup(func() {
					if err := dev.Close(); err != nil {
						t.Error(err)
					}
				})

				var appErr error
				if tt.appsFail {
					appErr = errors.New("synthetic registry unavailable")
				}

				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()

				readDevice := updatesReadDevice{Device: dev, appErr: appErr}
				if tt.cancelOnApps {
					readDevice.onAppsRead = cancel
				}

				sess := watchsession.New(readDevice)

				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if tt.softwareFail {
						w.WriteHeader(http.StatusBadGateway)
						return
					}

					_, _ = io.WriteString(w, `{"updates":[{"partNumber":"006-test","productName":"synthetic"}]}`)
				}))
				defer server.Close()

				config := updates.Config{URL: server.URL, Client: http.DefaultClient}

				var output bytes.Buffer

				cli := &Command{Config: config, Installed: func(ctx context.Context) ([]apps.App, error) { return (apps.ReadRegistryAction{}).Run(ctx, sess) }, JSON: asJSON, Out: clix.NewPrinter(&output)}
				err = cli.Updates(ctx, sess, nil)

				wantExit := 0
				if tt.softwareFail || tt.appsFail {
					wantExit = 1
				}

				if tt.cancelOnApps {
					wantExit = 130
				}

				if clix.ExitCode(err) != wantExit {
					t.Fatalf("exit=%d, want %d; error=%v", clix.ExitCode(err), wantExit, err)
				}

				checkUpdatesOutput(t, &output, asJSON, tt.softwareFail, tt.appsFail)
			})
		}
	}
}

func checkUpdatesOutput(t *testing.T, output *bytes.Buffer, asJSON, softwareFail, appsFail bool) {
	t.Helper()

	if !asJSON {
		if !strings.Contains(output.String(), "Watch software (Garmin update service):") || !strings.Contains(output.String(), "Connect IQ apps:") {
			t.Fatalf("text sections changed: %s", output)
		}

		return
	}

	var result struct {
		Software []updates.SoftwareUpdate `json:"software"`
		Apps     []updates.AppUpdate      `json:"apps"`
		Errors   map[string]string        `json:"errors"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}

	if !softwareFail && len(result.Software) != 1 {
		t.Fatalf("partial software result lost: %s", output)
	}

	if (result.Errors["software"] != "") != softwareFail || (result.Errors["apps"] != "") != appsFail {
		t.Fatalf("named errors changed: %s", output)
	}

	if !softwareFail && !appsFail && bytes.Contains(output.Bytes(), []byte(`"errors"`)) {
		t.Fatalf("successful JSON unexpectedly changed shape: %s", output)
	}
}

func TestUpdatesPerAppFailureAndOutputFailureRemainErrors(t *testing.T) {
	t.Parallel()

	app := updates.AppUpdate{App: apps.App{ID: "synthetic-app"}, Error: "synthetic store unavailable"}

	var output bytes.Buffer

	cli := &Command{JSON: true, Out: clix.NewPrinter(&output)}
	if err := cli.reportUpdates(nil, nil, []updates.AppUpdate{app}, nil); clix.ExitCode(err) != 1 {
		t.Fatalf("per-app error ignored: %v", err)
	}

	if !strings.Contains(output.String(), app.Error) || !strings.Contains(output.String(), `"errors"`) {
		t.Fatalf("per-app failure was not retained: %s", &output)
	}

	backendErr := errors.New("synthetic update error")
	cli.Out = clix.NewPrinter(updateFailedWriter{})

	err := cli.reportUpdates(nil, backendErr, nil, nil)
	if !errors.Is(err, backendErr) || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("output failure hid check failure: %v", err)
	}
}

type updateFailedWriter struct{}

func (updateFailedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestUpdatesHelpAndIncompleteArgumentsContactNoService(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		args []string
		exit int
	}{
		{name: "help", args: []string{"-h"}},
		{name: "install without a part number", args: []string{"install"}, exit: 2},
		{name: "install with extra arguments", args: []string{"install", "006-test", "extra"}, exit: 2},
		{name: "unknown subcommand", args: []string{"check"}, exit: 2},
		{name: "unknown flag", args: []string{"-all"}, exit: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dev, err := device.NewDirFS(testdump.Root(t))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = dev.Close() })

			var requests, registryReads atomic.Int32

			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
			defer server.Close()

			var output, diagnostics bytes.Buffer

			cli := &Command{
				Config: updates.Config{URL: server.URL, Client: http.DefaultClient},
				Installed: func(context.Context) ([]apps.App, error) {
					registryReads.Add(1)
					return nil, nil
				},
				Out: clix.NewPrinter(&output), ErrOut: clix.NewPrinter(&diagnostics),
			}

			err = cli.Updates(t.Context(), watchsession.New(dev), tt.args)
			if err == nil || clix.ExitCode(err) != tt.exit {
				t.Fatalf("error=%v exit=%d, want exit %d", err, clix.ExitCode(err), tt.exit)
			}

			if requests.Load() != 0 || registryReads.Load() != 0 {
				t.Fatalf("update service requests=%d, app registry reads=%d", requests.Load(), registryReads.Load())
			}

			if output.Len() != 0 || !strings.Contains(diagnostics.String()+err.Error(), "updates [install PART]") {
				t.Fatalf("output=%q diagnostics=%q error=%v", &output, &diagnostics, err)
			}
		})
	}
}
