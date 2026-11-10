//go:build desktop

package desktop

import (
	"context"
	"errors"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestNativeReadScopeCancelsAndJoinsBeforeOwnedBackend(t *testing.T) {
	t.Parallel()

	var stops atomic.Int32

	done := make(chan error, 1)
	a := &App{backend: &Backend{cancel: func() { stops.Add(1); done <- nil }, done: done}}

	scope := serviceScope{app: a}
	if _, _, err := scope.Acquire(); err == nil {
		t.Fatal("admitted before startup")
	}

	a.startup(t.Context())

	ctx, release, err := scope.Acquire()
	if err != nil {
		t.Fatal(err)
	}

	closed := make(chan error, 1)
	go func() { closed <- a.shutdown() }()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("read not canceled")
	}

	if stops.Load() != 0 {
		t.Fatal("backend closed before read joined")
	}

	select {
	case err := <-closed:
		t.Fatalf("shutdown bypassed join: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	if _, _, err := scope.Acquire(); !errors.Is(err, context.Canceled) {
		t.Fatalf("admitted during shutdown: %v", err)
	}

	release()
	release()

	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join")
	}

	if stops.Load() != 1 {
		t.Fatalf("stops=%d", stops.Load())
	}
}

func TestNativeServicesRegisterBeforeBackendWithoutStartingWork(t *testing.T) {
	t.Parallel()

	a := new(App)
	called := false

	services := nativeServices(a, []ServiceRegistrar{func(scope Scope) []application.Service {
		called = true

		if _, _, err := scope.Acquire(); err == nil {
			t.Fatal("service usable during registration")
		}

		return nil
	}})
	if !called || len(services) != 1 || services[0].Instance() != a {
		t.Fatal("lost existing native bridge")
	}

	endpoint, _ := url.Parse("http://127.0.0.1:8787")
	a.backend = &Backend{URL: endpoint}
	a.startup(t.Context())
	scope := serviceScope{app: a}

	_, release, err := scope.Acquire()
	if err != nil {
		t.Fatal(err)
	}

	address, owned, err := scope.Backend()

	release()

	if err != nil || owned || address != endpoint.String() {
		t.Fatalf("backend=%q owned=%v err=%v", address, owned, err)
	}

	if err := a.shutdown(); err != nil {
		t.Fatal(err)
	}
}
