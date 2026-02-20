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
