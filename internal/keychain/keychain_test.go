package keychain

import (
	"testing"
)

// Unit tests use MemoryStore — no macOS Keychain interaction needed.

func testStore() Store {
	return NewMemoryStore()
}

func TestSetAndGet(t *testing.T) {
	s := testStore()

	if err := s.Set("test/set-get", "hello-world"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	val, err := s.Get("test/set-get")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if val != "hello-world" {
		t.Errorf("expected 'hello-world', got %q", val)
	}
}

func TestGetNotFound(t *testing.T) {
	s := testStore()

	_, err := s.Get("test/nonexistent")
	if err == nil {
		t.Error("expected error for missing key")
	}
}

func TestSetOverwrites(t *testing.T) {
	s := testStore()

	s.Set("test/overwrite", "first")
	s.Set("test/overwrite", "second")

	val, err := s.Get("test/overwrite")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if val != "second" {
		t.Errorf("expected 'second', got %q", val)
	}
}

func TestDelete(t *testing.T) {
	s := testStore()

	s.Set("test/delete", "to-delete")

	if err := s.Delete("test/delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := s.Get("test/delete")
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestDeleteNonexistent(t *testing.T) {
	s := testStore()

	if err := s.Delete("test/never-existed"); err != nil {
		t.Errorf("Delete nonexistent: %v", err)
	}
}

func TestList(t *testing.T) {
	s := testStore()

	s.Set("test/list-a", "val")
	s.Set("test/list-b", "val")
	s.Set("test/list-c", "val")

	listed, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(listed) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(listed))
	}

	found := make(map[string]bool)
	for _, k := range listed {
		found[k] = true
	}
	for _, k := range []string{"test/list-a", "test/list-b", "test/list-c"} {
		if !found[k] {
			t.Errorf("expected %q in list, not found", k)
		}
	}
}

func TestGetMultiple(t *testing.T) {
	s := testStore()

	s.Set("test/multi-a", "val-a")
	s.Set("test/multi-b", "val-b")

	result, err := s.GetMultiple([]string{"test/multi-a", "test/multi-b", "test/multi-missing"})
	if err != nil {
		t.Fatalf("GetMultiple: %v", err)
	}

	if result["test/multi-a"] != "val-a" {
		t.Errorf("expected val-a, got %q", result["test/multi-a"])
	}
	if result["test/multi-b"] != "val-b" {
		t.Errorf("expected val-b, got %q", result["test/multi-b"])
	}
	if _, ok := result["test/multi-missing"]; ok {
		t.Error("expected missing key to be absent")
	}
}
