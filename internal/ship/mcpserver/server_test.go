package mcpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type shellRuntime struct {
	lifetime func() context.Context
	close    func(context.Context) error
}

func (r shellRuntime) Context() context.Context        { return r.lifetime() }
func (r shellRuntime) Close(ctx context.Context) error { return r.close(ctx) }

type failedTransport struct {
	failure error
	cancel  context.CancelFunc
}

func (t failedTransport) Connect(context.Context) (mcp.Connection, error) {
	t.cancel()
	return nil, t.failure
}

func TestServeRuntimeJoinsTransportAndShutdownErrors(t *testing.T) {
	t.Parallel()

	connectFailure := errors.New("synthetic MCP connect failure")

	closeFailure := errors.New("synthetic backend close failure")
	for _, test := range []struct {
		name       string
		closeError error
	}{
		{name: "transport failure"},
		{name: "transport and shutdown failure", closeError: closeFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			parent, cancel := context.WithCancel(t.Context())
			defer cancel()

			closes := 0
			runtime := shellRuntime{
				lifetime: func() context.Context { return parent },
				close: func(ctx context.Context) error {
					closes++

					if ctx.Err() != nil {
						t.Errorf("shutdown inherited canceled runtime context: %v", ctx.Err())
					}

					if _, ok := ctx.Deadline(); !ok {
						t.Error("runtime close has no deadline")
					}

					return test.closeError
				},
			}

			err := ServeRuntime(runtime, failedTransport{failure: connectFailure, cancel: cancel}, "test")
			if !errors.Is(err, connectFailure) {
				t.Fatalf("lost transport error: %v", err)
			}

			if test.closeError != nil && !errors.Is(err, test.closeError) {
				t.Fatalf("lost shutdown error: %v", err)
			}

			if closes != 1 {
				t.Fatalf("runtime closes=%d want=1", closes)
			}
		})
	}
}
