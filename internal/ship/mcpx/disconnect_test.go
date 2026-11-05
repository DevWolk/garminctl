package mcpx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
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
			manager := watchsession.NewManager(func(context.Context) (device.Device, error) {
				opens++
				return device.NewDirFS(root)
			})

			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
				defer cancel()

				if err := manager.Close(ctx); err != nil {
					t.Error(err)
				}
			})

			handler := WithWatch(manager, nil, func(context.Context, *watchsession.Lease, struct{}) (struct{}, error) { return struct{}{}, tt.err })
			if _, _, err := handler(t.Context(), nil, struct{}{}); !errors.Is(err, tt.err) {
				t.Fatalf("MCP changed the tool error: %v", err)
			}

			lease, err := manager.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			if err := lease.Release(); err != nil {
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
