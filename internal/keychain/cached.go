package keychain

import (
	"sync"
	"time"
)

type cacheEntry struct {
	value     string
	expiresAt time.Time
}

// CachedStore wraps a Store with an in-memory TTL cache.
// Cache hits avoid round-trips to the inner store.
// Writes and deletes invalidate the affected key.
type CachedStore struct {
	inner Store
	ttl   time.Duration

	mu    sync.RWMutex
	cache map[string]cacheEntry
}

// NewCachedStore wraps inner with a TTL-based in-memory cache.
func NewCachedStore(inner Store, ttl time.Duration) *CachedStore {
	return &CachedStore{
		inner: inner,
		ttl:   ttl,
		cache: make(map[string]cacheEntry),
	}
}

func (c *CachedStore) Get(key string) (string, error) {
	c.mu.RLock()
	if e, ok := c.cache[key]; ok && time.Now().Before(e.expiresAt) {
		c.mu.RUnlock()
		return e.value, nil
	}
	c.mu.RUnlock()

	val, err := c.inner.Get(key)
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	c.cache[key] = cacheEntry{value: val, expiresAt: time.Now().Add(c.ttl)}
	c.mu.Unlock()

	return val, nil
}

func (c *CachedStore) Set(key, value string) error {
	err := c.inner.Set(key, value)
	if err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.cache, key)
	c.mu.Unlock()
	return nil
}

func (c *CachedStore) Delete(key string) error {
	err := c.inner.Delete(key)
	if err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.cache, key)
	c.mu.Unlock()
	return nil
}

func (c *CachedStore) List() ([]string, error) {
	return c.inner.List()
}

// Invalidate evicts a single key from the cache.
func (c *CachedStore) Invalidate(key string) {
	c.mu.Lock()
	delete(c.cache, key)
	c.mu.Unlock()
}

// InvalidateAll evicts all entries from the cache.
func (c *CachedStore) InvalidateAll() {
	c.mu.Lock()
	c.cache = make(map[string]cacheEntry)
	c.mu.Unlock()
}

// Warm pre-loads all secrets from the inner store into cache.
// Returns the number of secrets cached.
func (c *CachedStore) Warm() (int, error) {
	keys, err := c.inner.List()
	if err != nil {
		return 0, err
	}

	now := time.Now()
	expires := now.Add(c.ttl)

	c.mu.Lock()
	defer c.mu.Unlock()

	count := 0
	for _, key := range keys {
		val, err := c.inner.Get(key)
		if err != nil {
			continue
		}
		c.cache[key] = cacheEntry{value: val, expiresAt: expires}
		count++
	}
	return count, nil
}
