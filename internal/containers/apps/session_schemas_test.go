package apps

import (
	"testing"

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
	if schemas.For(session, 1) == cache || schemas.For(session, 2) == newer || schemas.source != nil {
		t.Fatal("a closed owner still retains a cache")
	}
}
