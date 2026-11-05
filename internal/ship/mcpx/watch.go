package mcpx

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// WatchLease is the lifetime contract of a watch lease.
type WatchLease interface {
	Context() context.Context
	Epoch() watchsession.ConnectionEpoch
	Release() error
}

// WatchOwner admits leases and retires a matching broken connection. Manager
// satisfies it directly.
type WatchOwner[L WatchLease] interface {
	Acquire(context.Context) (L, error)
	DropAt(watchsession.ConnectionEpoch) error
}

// WithWatch scopes a tool body to one lease and optional process-bound context.
// body must use the supplied context and must not retain or release its lease.
// A non-nil body/cleanup error is passed to the SDK, which drops structured out.
func WithWatch[In, Out any, L WatchLease](owner WatchOwner[L], bind func(context.Context) (context.Context, context.CancelFunc), body func(context.Context, L, In) (Out, error)) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		if bind != nil {
			bound, cancel := bind(ctx)
			defer cancel()

			ctx = bound
		}

		lease, err := owner.Acquire(ctx)
		if err != nil {
			var zero Out

			return nil, zero, err
		}

		out, err := body(lease.Context(), lease, in)
		if errors.Is(err, device.ErrDisconnected) {
			err = errors.Join(err, owner.DropAt(lease.Epoch()))
		}

		err = errors.Join(err, lease.Release())

		return nil, out, err
	}
}

// ReadOnly marks tools that leave watch/account data unchanged.
func ReadOnly(title string, network bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: new(network)}
}

// Writes marks tools that change watch/account data.
func Writes(title string, destructive, network bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: new(destructive), OpenWorldHint: new(network)}
}
