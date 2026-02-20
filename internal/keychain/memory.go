package keychain

import (
	"fmt"
	"sort"
	"sync"
)

// MemoryStore is an in-memory implementation of Store for testing.
type MemoryStore struct {
	mu      sync.RWMutex
	secrets map[string]string
}

// NewMemoryStore creates a new in-memory secret store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{secrets: make(map[string]string)}
}

func (s *MemoryStore) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secrets[key] = value
	return nil
}

func (s *MemoryStore) Get(key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.secrets[key]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return val, nil
}

func (s *MemoryStore) List() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.secrets))
	for k := range s.secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

func (s *MemoryStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.secrets, key)
	return nil
}

func (s *MemoryStore) GetMultiple(keys []string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if val, ok := s.secrets[key]; ok {
			result[key] = val
		}
	}
	return result, nil
}
