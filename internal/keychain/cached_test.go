package keychain

import (
	"testing"
	"time"
)

func TestCachedStoreGetCachesValue(t *testing.T) {
	inner := NewMemoryStore()
	inner.Set("key1", "value1")

	store := NewCachedStore(inner, 5*time.Minute)

	// First get — should hit inner
	val, err := store.Get("key1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if val != "value1" {
		t.Errorf("expected value1, got %q", val)
	}

	// Change inner directly — cached store should return stale value
	inner.Set("key1", "changed")

	val, err = store.Get("key1")
	if err != nil {
		t.Fatalf("Get cached: %v", err)
	}
	if val != "value1" {
		t.Errorf("expected cached value1, got %q", val)
	}
}

func TestCachedStoreGetExpiresTTL(t *testing.T) {
	inner := NewMemoryStore()
	inner.Set("key1", "value1")

	store := NewCachedStore(inner, 10*time.Millisecond)

	store.Get("key1")

	// Update inner and wait for TTL
	inner.Set("key1", "value2")
	time.Sleep(20 * time.Millisecond)

	val, err := store.Get("key1")
	if err != nil {
		t.Fatalf("Get after TTL: %v", err)
	}
	if val != "value2" {
		t.Errorf("expected value2 after TTL, got %q", val)
	}
}

func TestCachedStoreGetMissPassesThrough(t *testing.T) {
	inner := NewMemoryStore()
	store := NewCachedStore(inner, 5*time.Minute)

	_, err := store.Get("missing")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestCachedStoreSetInvalidatesCache(t *testing.T) {
	inner := NewMemoryStore()
	inner.Set("key1", "old")

	store := NewCachedStore(inner, 5*time.Minute)

	// Warm the cache
	store.Get("key1")

	// Set via cached store — should invalidate and pass through
	store.Set("key1", "new")

	val, err := store.Get("key1")
	if err != nil {
		t.Fatalf("Get after Set: %v", err)
	}
	if val != "new" {
		t.Errorf("expected new, got %q", val)
	}
}

func TestCachedStoreDeleteInvalidatesCache(t *testing.T) {
	inner := NewMemoryStore()
	inner.Set("key1", "value1")

	store := NewCachedStore(inner, 5*time.Minute)

	// Warm the cache
	store.Get("key1")

	store.Delete("key1")

	_, err := store.Get("key1")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestCachedStoreInvalidate(t *testing.T) {
	inner := NewMemoryStore()
	inner.Set("key1", "v1")

	store := NewCachedStore(inner, 5*time.Minute)

	// Warm cache
	store.Get("key1")

	// Change inner, then invalidate
	inner.Set("key1", "v2")
	store.Invalidate("key1")

	val, err := store.Get("key1")
	if err != nil {
		t.Fatalf("Get after Invalidate: %v", err)
	}
	if val != "v2" {
		t.Errorf("expected v2, got %q", val)
	}
}

func TestCachedStoreInvalidateAll(t *testing.T) {
	inner := NewMemoryStore()
	inner.Set("a", "1")
	inner.Set("b", "2")

	store := NewCachedStore(inner, 5*time.Minute)

	// Warm cache
	store.Get("a")
	store.Get("b")

	// Change inner, then invalidate all
	inner.Set("a", "10")
	inner.Set("b", "20")
	store.InvalidateAll()

	val, _ := store.Get("a")
	if val != "10" {
		t.Errorf("expected 10, got %q", val)
	}
	val, _ = store.Get("b")
	if val != "20" {
		t.Errorf("expected 20, got %q", val)
	}
}

func TestCachedStoreListPassesThrough(t *testing.T) {
	inner := NewMemoryStore()
	inner.Set("a", "1")
	inner.Set("b", "2")

	store := NewCachedStore(inner, 5*time.Minute)

	keys, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}
}

func TestCachedStoreWarm(t *testing.T) {
	inner := NewMemoryStore()
	inner.Set("a", "1")
	inner.Set("b", "2")

	store := NewCachedStore(inner, 5*time.Minute)

	n, err := store.Warm()
	if err != nil {
		t.Fatalf("Warm: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 warmed, got %d", n)
	}

	// Change inner — should still get cached values
	inner.Set("a", "changed")
	val, _ := store.Get("a")
	if val != "1" {
		t.Errorf("expected cached 1 after warm, got %q", val)
	}
}
