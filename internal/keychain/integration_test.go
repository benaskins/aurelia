//go:build integration

package keychain

import (
	"testing"
)

// Integration tests use real macOS Keychain.
// Run with: go test -tags integration ./internal/keychain/
//
// Requires an unlocked login Keychain and an interactive session
// (first run may prompt for Keychain access approval).

func integrationStore() *SystemStore {
	return &SystemStore{service: "com.aurelia.test"}
}

func cleanupIntegration(t *testing.T, s *SystemStore, keys ...string) {
	t.Helper()
	for _, k := range keys {
		s.Delete(k)
	}
}

func TestKeychainSetAndGet(t *testing.T) {
	s := integrationStore()
	key := "test/integration-set-get"
	defer cleanupIntegration(t, s, key)

	if err := s.Set(key, "hello-keychain"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	val, err := s.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if val != "hello-keychain" {
		t.Errorf("expected 'hello-keychain', got %q", val)
	}
}

func TestKeychainOverwrite(t *testing.T) {
	s := integrationStore()
	key := "test/integration-overwrite"
	defer cleanupIntegration(t, s, key)

	s.Set(key, "first")
	s.Set(key, "second")

	val, err := s.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if val != "second" {
		t.Errorf("expected 'second', got %q", val)
	}
}

func TestKeychainDelete(t *testing.T) {
	s := integrationStore()
	key := "test/integration-delete"

	s.Set(key, "to-delete")
	s.Delete(key)

	_, err := s.Get(key)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestKeychainList(t *testing.T) {
	s := integrationStore()
	keys := []string{"test/integration-list-a", "test/integration-list-b"}
	defer cleanupIntegration(t, s, keys...)

	for _, k := range keys {
		s.Set(k, "val")
	}

	listed, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	found := make(map[string]bool)
	for _, k := range listed {
		found[k] = true
	}
	for _, k := range keys {
		if !found[k] {
			t.Errorf("expected %q in list, not found", k)
		}
	}
}
