package mcpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/garmin"
)

func TestWithWatchDropsOnlyDisconnectedSessions(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		err  error
		drop bool
	}{
		{name: "disconnected", err: device.ErrDisconnected, drop: true},
		{name: "busy", err: device.ErrBusy},
		{name: "cancelled before I/O", err: context.Canceled},
		{name: "domain error mentioning bridge", err: errors.New("invalid bridge label")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			opens := 0
			lazy := garmin.NewLazy(func(context.Context) (device.Device, error) {
				opens++
				return device.NewDirFS(root)
			})
			t.Cleanup(lazy.Drop)

			s := &server{svc: lazy}

			handler := withWatch(s, func(context.Context, *garmin.Service, struct{}) (struct{}, error) { return struct{}{}, tt.err })
			if _, _, err := handler(t.Context(), nil, struct{}{}); !errors.Is(err, tt.err) {
				t.Fatalf("MCP changed the tool error: %v", err)
			}

			if _, err := lazy.Get(t.Context()); err != nil {
				t.Fatal(err)
			}

			wantOpens := 1
			if tt.drop {
				wantOpens = 2
			}

			if opens != wantOpens {
				t.Fatalf("connection opens=%d, want=%d", opens, wantOpens)
			}
		})
	}
}
