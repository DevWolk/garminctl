package apps

import (
	"context"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func TestSessionSchemasKeepNewestConnection(t *testing.T) {
	t.Parallel()

	var schemas SessionSchemas

	first, second := watchsession.New(nil), watchsession.New(nil)

	cache := schemas.For(first, 1)
	if schemas.For(first, 1) != cache || schemas.For(first, 0) != cache {
		t.Fatal("one connection got two caches")
	}

	replacement := schemas.For(second, 2)
	if replacement == cache || schemas.For(second, 2) != replacement {
		t.Fatal("a newer connection did not get its own retained cache")
	}

	// A delayed caller still holding the retired connection gets a private
	// cache and cannot evict the newer one.
	if late := schemas.For(first, 1); late == replacement || late == cache || schemas.For(second, 2) != replacement {
		t.Fatal("a retired connection evicted or shared the current cache")
	}

	if schemas.source != second {
		t.Fatal("a retired connection is still retained")
	}
}

func TestSessionSchemasDoNotRetainUnknownConnections(t *testing.T) {
	t.Parallel()

	var schemas SessionSchemas

	session := watchsession.New(nil)

	first := schemas.For(session, 0)
	if second := schemas.For(session, 0); first == second {
		t.Fatal("a connection that is not current was retained")
	}

	if schemas.For(nil, 1) == nil || schemas.source != nil {
		t.Fatal("a missing Session was retained")
	}
}

func TestSessionSchemasCloseDropsCache(t *testing.T) {
	t.Parallel()

	var schemas SessionSchemas

	session := watchsession.New(nil)
	cache := schemas.For(session, 1)
	schemas.Close()

	newer := schemas.For(session, 2)
	if schemas.For(session, 1) == cache || schemas.For(session, 2) == newer || schemas.source != nil || schemas.cache != nil {
		t.Fatal("a closed owner still retains a cache")
	}
}

type managedSchemaDevice struct{ device.Device }

func (managedSchemaDevice) Close() error { return nil }

// managedSchemasFor looks a cache up the way connection owners do: with the
// Manager's epoch when session is its current connection, and zero otherwise.
func managedSchemasFor(manager *watchsession.Manager, schemas *SessionSchemas, session *watchsession.Session) *SchemaCache {
	current, epoch := manager.Snapshot()
	if current != session {
		epoch = 0
	}

	return schemas.For(session, epoch)
}

func acquireManaged(t *testing.T, manager *watchsession.Manager) *watchsession.Lease {
	t.Helper()

	lease, err := manager.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	return lease
}

func releaseManaged(t *testing.T, lease *watchsession.Lease) {
	t.Helper()

	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestSessionSchemasFollowManagerReconnect(t *testing.T) {
	t.Parallel()

	var schemas SessionSchemas

	manager := watchsession.NewManager(func(context.Context) (device.Device, error) { return managedSchemaDevice{}, nil })

	first := acquireManaged(t, manager)
	firstSession, firstEpoch := first.Session(), first.Epoch()

	cache := managedSchemasFor(manager, &schemas, firstSession)
	if managedSchemasFor(manager, &schemas, firstSession) != cache {
		t.Fatal("one Manager connection got two caches")
	}

	releaseManaged(t, first)

	if err := manager.DropAt(firstEpoch); err != nil {
		t.Fatal(err)
	}

	next := acquireManaged(t, manager)
	nextSession, nextEpoch := next.Session(), next.Epoch()

	fresh := managedSchemasFor(manager, &schemas, nextSession)
	if nextSession == firstSession || fresh == cache || managedSchemasFor(manager, &schemas, nextSession) != fresh {
		t.Fatal("the replacement connection reused the dropped connection's cache")
	}

	// A lookup delayed since the dropped connection passes epoch zero; it gets
	// a private cache and cannot displace the current one.
	if late := managedSchemasFor(manager, &schemas, firstSession); late == fresh || late == cache || managedSchemasFor(manager, &schemas, nextSession) != fresh {
		t.Fatal("a late retired-Session lookup displaced or shared the current cache")
	}

	releaseManaged(t, next)

	if err := manager.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	schemas.Close()

	_ = schemas.For(firstSession, firstEpoch)
	_ = schemas.For(nextSession, nextEpoch)

	if schemas.source != nil || schemas.cache != nil {
		t.Fatal("a closed owner retained or republished a cache")
	}
}
