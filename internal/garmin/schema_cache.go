package garmin

import (
	"sync"

	"github.com/DevWolk/garminctl/internal/ciqstore"
)

// The requested version is retained even when Load falls back to a newer schema.
type appSchemaKey struct {
	appID      string
	version    int
	partNumber string
	unit       uint64
}

// appSchemaCache remains feature-owned. Its generation only advances: a late
// publisher that sampled Session before a mutation cannot replace newer entries.
// No device or Session call is made while mu is held.
type appSchemaCache struct {
	mu         sync.Mutex
	generation uint64
	entries    map[appSchemaKey]*ciqstore.Schema
}

func (c *appSchemaCache) advanceLocked(generation uint64) {
	if generation > c.generation || c.entries == nil {
		c.generation = generation
		c.entries = map[appSchemaKey]*ciqstore.Schema{}
	}
}

func (c *appSchemaCache) reset(generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// A lookup may already have advanced to this generation and published a
	// fresh entry after Session invalidated. Do not erase that entry.
	c.advanceLocked(generation)
}

func (c *appSchemaCache) load(key appSchemaKey, generation uint64) *ciqstore.Schema {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.advanceLocked(generation)

	if generation != c.generation {
		return nil
	}

	return c.entries[key]
}

func (c *appSchemaCache) store(key appSchemaKey, generation uint64, sc *ciqstore.Schema) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.advanceLocked(generation)

	if generation == c.generation {
		c.entries[key] = sc
	}
}

func (s *Service) cachedAppSchema(key appSchemaKey, generation uint64) *ciqstore.Schema {
	current, valid := s.session.CacheGeneration()
	if !valid || current != generation {
		return nil
	}

	return s.schemas.load(key, generation)
}

func (s *Service) rememberAppSchema(key appSchemaKey, generation uint64, sc *ciqstore.Schema) {
	current, valid := s.session.CacheGeneration()
	if valid && current == generation {
		s.schemas.store(key, generation, sc)
	}
}
