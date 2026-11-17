//go:build desktop

package desktop

import (
	"context"
	"net/url"
	"testing"
)

func TestReadyReportsStartupOnceThroughExistingScope(t *testing.T) {
	t.Parallel()

	address := &url.URL{Scheme: "http", Host: "127.0.0.1:8787"}
	calls := 0
	a := &App{host: new(fakeWindowHost), backend: &Backend{URL: address, cancel: func() {}}}
	a.onReady = func(ctx context.Context, scope Scope) {
		calls++

		got, owned, err := scope.Backend()
		if ctx.Err() != nil || err != nil || got != address.String() || !owned {
			t.Errorf("startup scope=%s owned=%v error=%v context=%v", got, owned, err, ctx.Err())
		}
	}
	a.startup(t.Context())
	a.Ready()
	a.Ready()
	a.operations.close()
	a.Ready()

	if calls != 1 {
		t.Fatalf("startup notices=%d, want one", calls)
	}
}

func TestReadyDoesNotNotifyAfterWindowLifetimeEnds(t *testing.T) {
	t.Parallel()

	a := &App{host: new(fakeWindowHost), onReady: func(context.Context, Scope) { t.Error("startup notification escaped the window lifetime") }}
	a.startup(t.Context())
	a.operations.close()
	a.Ready()
}
