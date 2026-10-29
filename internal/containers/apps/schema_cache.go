package apps

import (
	"sync"

	"github.com/DevWolk/garminctl/internal/ciqstore"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// The requested version is retained even when Load falls back to a newer schema.
type appSchemaKey struct {
	appID      string
	version    int
	partNumber string
	unit       uint64
}

// SchemaCache remains feature-owned. Its generation only advances: a late
// publisher that sampled Session before a mutation cannot replace newer entries.
// No device or Session call is made while mu is held.
// The zero value is usable. Its owner supplies one cache per Session epoch and
// shares it across Actions; it must not reuse this value for a replacement Session.
type SchemaCache struct {
	mu         sync.Mutex
	generation uint64
	entries    map[appSchemaKey]*ciqstore.Schema
}

func (c *SchemaCache) advanceLocked(generation uint64) {
	if generation > c.generation || c.entries == nil {
		c.generation = generation
		c.entries = map[appSchemaKey]*ciqstore.Schema{}
	}
}

// Reset advances in-memory invalidation after the caller invalidates Session.
func (c *SchemaCache) Reset(generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// A lookup may already have advanced to this generation and published a
	// fresh entry after Session invalidated. Do not erase that entry.
	c.advanceLocked(generation)
}

func (c *SchemaCache) load(key appSchemaKey, generation uint64) *ciqstore.Schema {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.advanceLocked(generation)

	if generation != c.generation {
		return nil
	}

	return c.entries[key]
}

func (c *SchemaCache) store(key appSchemaKey, generation uint64, sc *ciqstore.Schema) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.advanceLocked(generation)

	if generation == c.generation {
		c.entries[key] = sc
	}
}

func (c *SchemaCache) cached(sess *watchsession.Session, key appSchemaKey, generation uint64) *ciqstore.Schema {
	current, valid := sess.CacheGeneration()
	if !valid || current != generation {
		return nil
	}

	return c.load(key, generation)
}

func (c *SchemaCache) remember(sess *watchsession.Session, key appSchemaKey, generation uint64, sc *ciqstore.Schema) {
	current, valid := sess.CacheGeneration()
	if valid && current == generation {
		c.store(key, generation, sc)
	}
}
