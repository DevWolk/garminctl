package main

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
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/testdump"
)

type updatesReadDevice struct {
	device.Device
	appErr     error
	onAppsRead func()
}

func (d updatesReadDevice) Read(ctx context.Context, path string) ([]byte, error) {
	if path == garmin.PathAppsConfig {
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

				svc := garmin.New(readDevice)

				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if tt.softwareFail {
						w.WriteHeader(http.StatusBadGateway)
						return
					}

					_, _ = io.WriteString(w, `{"updates":[{"partNumber":"006-test","productName":"synthetic"}]}`)
				}))
				defer server.Close()

				svc.UpdatesURL = server.URL

				var output bytes.Buffer

				cli := &cli{asJSON: asJSON, out: newPrinter(&output)}
				err = cli.updates(ctx, svc, nil)

				wantExit := 0
				if tt.softwareFail || tt.appsFail {
					wantExit = 1
				}

				if tt.cancelOnApps {
					wantExit = 130
				}

				if exitCode(err) != wantExit {
					t.Fatalf("exit=%d, want %d; error=%v", exitCode(err), wantExit, err)
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
		Software []garmin.SoftwareUpdate `json:"software"`
		Apps     []garmin.AppUpdate      `json:"apps"`
		Errors   map[string]string       `json:"errors"`
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

	app := garmin.AppUpdate{App: garmin.App{ID: "synthetic-app"}, Error: "synthetic store unavailable"}

	var output bytes.Buffer

	cli := &cli{asJSON: true, out: newPrinter(&output)}
	if err := cli.reportUpdates(nil, nil, []garmin.AppUpdate{app}, nil); exitCode(err) != 1 {
		t.Fatalf("per-app error ignored: %v", err)
	}

	if !strings.Contains(output.String(), app.Error) || !strings.Contains(output.String(), `"errors"`) {
		t.Fatalf("per-app failure was not retained: %s", &output)
	}

	backendErr := errors.New("synthetic update error")
	cli.out = newPrinter(updateFailedWriter{})

	err := cli.reportUpdates(nil, backendErr, nil, nil)
	if !errors.Is(err, backendErr) || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("output failure hid check failure: %v", err)
	}
}

type updateFailedWriter struct{}

func (updateFailedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
