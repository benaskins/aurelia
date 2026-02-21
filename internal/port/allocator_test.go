package port

import (
	"testing"
)

func TestAllocateInRange(t *testing.T) {
	a := NewAllocator(20000, 20100)
	port, err := a.Allocate("test-svc")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if port < 20000 || port > 20100 {
		t.Errorf("port %d outside range 20000-20100", port)
	}
}

func TestAllocateIdempotent(t *testing.T) {
	a := NewAllocator(20000, 20100)
	p1, err := a.Allocate("svc")
	if err != nil {
		t.Fatalf("first Allocate: %v", err)
	}
	p2, err := a.Allocate("svc")
	if err != nil {
		t.Fatalf("second Allocate: %v", err)
	}
	if p1 != p2 {
		t.Errorf("idempotent allocate returned different ports: %d vs %d", p1, p2)
	}
}

func TestAllocateDifferentServices(t *testing.T) {
	a := NewAllocator(20000, 20100)
	p1, err := a.Allocate("svc-a")
	if err != nil {
		t.Fatalf("Allocate svc-a: %v", err)
	}
	p2, err := a.Allocate("svc-b")
	if err != nil {
		t.Fatalf("Allocate svc-b: %v", err)
	}
	if p1 == p2 {
		t.Errorf("two services got same port: %d", p1)
	}
}

func TestReserve(t *testing.T) {
	a := NewAllocator(20000, 20100)
	if err := a.Reserve("svc", 20050); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if got := a.Port("svc"); got != 20050 {
		t.Errorf("expected port 20050, got %d", got)
	}
}

func TestReserveConflict(t *testing.T) {
	a := NewAllocator(20000, 20100)
	if err := a.Reserve("svc-a", 20050); err != nil {
		t.Fatalf("Reserve svc-a: %v", err)
	}
	if err := a.Reserve("svc-b", 20050); err == nil {
		t.Error("expected error reserving port already taken by another service")
	}
}

func TestReserveSameService(t *testing.T) {
	a := NewAllocator(20000, 20100)
	if err := a.Reserve("svc", 20050); err != nil {
		t.Fatalf("first Reserve: %v", err)
	}
	if err := a.Reserve("svc", 20050); err != nil {
		t.Errorf("reserving same port for same service should succeed: %v", err)
	}
}

func TestReleaseAndReuse(t *testing.T) {
	a := NewAllocator(20000, 20000) // single port range
	p1, err := a.Allocate("svc-a")
	if err != nil {
		t.Fatalf("Allocate svc-a: %v", err)
	}

	a.Release("svc-a")

	p2, err := a.Allocate("svc-b")
	if err != nil {
		t.Fatalf("Allocate svc-b after release: %v", err)
	}
	if p1 != p2 {
		t.Errorf("expected reuse of port %d, got %d", p1, p2)
	}
}

func TestPortLookup(t *testing.T) {
	a := NewAllocator(20000, 20100)
	if got := a.Port("nonexistent"); got != 0 {
		t.Errorf("expected 0 for unknown service, got %d", got)
	}

	a.Allocate("svc")
	if got := a.Port("svc"); got == 0 {
		t.Error("expected non-zero port after allocation")
	}
}

func TestAllocateTemporary(t *testing.T) {
	a := NewAllocator(20000, 20100)
	p, err := a.AllocateTemporary("chat", "deploy")
	if err != nil {
		t.Fatalf("AllocateTemporary: %v", err)
	}
	if p < 20000 || p > 20100 {
		t.Errorf("port %d outside range", p)
	}
	// Should be accessible via the compound key
	if got := a.Port("chat__deploy"); got != p {
		t.Errorf("expected port %d via compound key, got %d", p, got)
	}
}

func TestAllocateTemporaryIdempotent(t *testing.T) {
	a := NewAllocator(20000, 20100)
	p1, _ := a.AllocateTemporary("chat", "deploy")
	p2, _ := a.AllocateTemporary("chat", "deploy")
	if p1 != p2 {
		t.Errorf("expected idempotent allocation, got %d and %d", p1, p2)
	}
}

func TestReleaseTemporary(t *testing.T) {
	a := NewAllocator(20000, 20000) // single port
	p1, err := a.AllocateTemporary("chat", "deploy")
	if err != nil {
		t.Fatalf("AllocateTemporary: %v", err)
	}
	a.ReleaseTemporary("chat", "deploy")

	// Port should be available again
	p2, err := a.Allocate("other")
	if err != nil {
		t.Fatalf("Allocate after release: %v", err)
	}
	if p1 != p2 {
		t.Errorf("expected reuse of port %d, got %d", p1, p2)
	}
}

func TestReassign(t *testing.T) {
	a := NewAllocator(20000, 20100)
	p, _ := a.AllocateTemporary("chat", "deploy")

	if err := a.Reassign("chat__deploy", "chat"); err != nil {
		t.Fatalf("Reassign: %v", err)
	}
	// Old key should be gone
	if got := a.Port("chat__deploy"); got != 0 {
		t.Errorf("expected 0 for old key, got %d", got)
	}
	// New key should have the port
	if got := a.Port("chat"); got != p {
		t.Errorf("expected port %d for new key, got %d", p, got)
	}
}

func TestReassignFromMissing(t *testing.T) {
	a := NewAllocator(20000, 20100)
	if err := a.Reassign("nonexistent", "chat"); err == nil {
		t.Error("expected error when reassigning from missing key")
	}
}

func TestReassignToExisting(t *testing.T) {
	a := NewAllocator(20000, 20100)
	a.AllocateTemporary("chat", "deploy")
	a.Allocate("chat")

	if err := a.Reassign("chat__deploy", "chat"); err == nil {
		t.Error("expected error when reassigning to existing key")
	}
}

func TestTemporaryDoesNotConflictWithPrimary(t *testing.T) {
	a := NewAllocator(20000, 20100)
	p1, _ := a.Allocate("chat")
	p2, _ := a.AllocateTemporary("chat", "deploy")
	if p1 == p2 {
		t.Errorf("temporary and primary got same port: %d", p1)
	}
}

func TestRangeExhaustion(t *testing.T) {
	a := NewAllocator(20000, 20000) // single port
	_, err := a.Allocate("svc-a")
	if err != nil {
		t.Fatalf("first Allocate: %v", err)
	}
	_, err = a.Allocate("svc-b")
	if err == nil {
		t.Error("expected error when range is exhausted")
	}
}
