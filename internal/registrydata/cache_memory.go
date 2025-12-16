package registrydata

import (
	"encoding/json"
	"sync"
	"time"
)

type memoryCache struct {
	mu      sync.RWMutex
	entries map[string]memEntry
	now     func() time.Time
}

type memEntry struct {
	b         []byte
	expiresAt time.Time
}

func newMemoryCache() *memoryCache {
	return &memoryCache{
		entries: make(map[string]memEntry),
		now:     time.Now,
	}
}

func (c *memoryCache) Get(key string, dst any) (bool, error) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return false, nil
	}
	if !e.expiresAt.IsZero() && c.now().After(e.expiresAt) {
		c.mu.Lock()
		// re-check under write lock
		if e2, ok2 := c.entries[key]; ok2 {
			if !e2.expiresAt.IsZero() && c.now().After(e2.expiresAt) {
				delete(c.entries, key)
			}
		}
		c.mu.Unlock()
		return false, nil
	}
	if err := json.Unmarshal(e.b, dst); err != nil {
		// treat as miss
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		return false, nil
	}
	return true, nil
}

func (c *memoryCache) Set(key string, value any, ttl time.Duration) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = c.now().Add(ttl)
	}
	c.mu.Lock()
	c.entries[key] = memEntry{b: b, expiresAt: expiresAt}
	c.mu.Unlock()
	return nil
}
