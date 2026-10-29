package cli

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/clix"
)

func TestDevicesUsesInjectedDiscoveryWithoutSession(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		devices []device.Info
		json    bool
		want    string
	}{
		{name: "empty", want: "no devices found\n"},
		{name: "text", devices: []device.Info{{ID: "id", Name: "name", Manufacturer: "Garmin"}}, want: "name\tGarmin\tid\n"},
		{name: "JSON", devices: []device.Info{}, json: true, want: "[]\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			calls := 0

			var out bytes.Buffer

			command := &Commands{Out: clix.NewPrinter(&out), JSON: test.json, Discover: func(ctx context.Context) ([]device.Info, error) {
				calls++

				if ctx != t.Context() {
					t.Error("discovery context changed")
				}

				return test.devices, nil
			}}
			if err := command.Devices(t.Context(), nil); err != nil || calls != 1 || out.String() != test.want {
				t.Fatalf("discovery error=%v calls=%d output=%q", err, calls, &out)
			}
		})
	}
}

func TestDevicesRetainsDiscoveryError(t *testing.T) {
	t.Parallel()

	failure := errors.New("synthetic discovery failure")

	var out bytes.Buffer

	command := &Commands{Out: clix.NewPrinter(&out), Discover: func(context.Context) ([]device.Info, error) { return nil, failure }}
	if err := command.Devices(t.Context(), nil); !errors.Is(err, failure) || out.Len() != 0 {
		t.Fatalf("discovery error=%v output=%q", err, &out)
	}
}
