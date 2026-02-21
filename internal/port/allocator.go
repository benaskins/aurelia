package port

import (
	"fmt"
	"math/rand"
	"net"
	"sync"
)

// Allocator manages dynamic port allocation for services.
type Allocator struct {
	mu        sync.Mutex
	minPort   int
	maxPort   int
	allocated map[string]int // service name → port
	usedPorts map[int]string // port → service name
}

// NewAllocator creates a port allocator for the given range [min, max].
func NewAllocator(minPort, maxPort int) *Allocator {
	return &Allocator{
		minPort:   minPort,
		maxPort:   maxPort,
		allocated: make(map[string]int),
		usedPorts: make(map[int]string),
	}
}

// Allocate picks an available port for the named service.
// Idempotent: returns the same port if already allocated.
func (a *Allocator) Allocate(serviceName string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if port, ok := a.allocated[serviceName]; ok {
		return port, nil
	}

	rangeSize := a.maxPort - a.minPort + 1
	if len(a.usedPorts) >= rangeSize {
		return 0, fmt.Errorf("port range exhausted (%d-%d)", a.minPort, a.maxPort)
	}

	// Try random ports until we find one that's available
	for attempts := 0; attempts < rangeSize*2; attempts++ {
		port := a.minPort + rand.Intn(rangeSize)
		if _, taken := a.usedPorts[port]; taken {
			continue
		}
		if !isPortAvailable(port) {
			continue
		}
		a.allocated[serviceName] = port
		a.usedPorts[port] = serviceName
		return port, nil
	}

	// Exhaustive scan as fallback
	for port := a.minPort; port <= a.maxPort; port++ {
		if _, taken := a.usedPorts[port]; taken {
			continue
		}
		if !isPortAvailable(port) {
			continue
		}
		a.allocated[serviceName] = port
		a.usedPorts[port] = serviceName
		return port, nil
	}

	return 0, fmt.Errorf("no available ports in range %d-%d", a.minPort, a.maxPort)
}

// Reserve restores a previously allocated port (e.g., from persisted state).
// Returns an error if the port is already taken by another service.
func (a *Allocator) Reserve(serviceName string, port int) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if existing, ok := a.usedPorts[port]; ok && existing != serviceName {
		return fmt.Errorf("port %d already allocated to %q", port, existing)
	}

	a.allocated[serviceName] = port
	a.usedPorts[port] = serviceName
	return nil
}

// Release frees the port allocated to a service.
func (a *Allocator) Release(serviceName string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if port, ok := a.allocated[serviceName]; ok {
		delete(a.usedPorts, port)
		delete(a.allocated, serviceName)
	}
}

// Port returns the currently allocated port for a service, or 0 if none.
func (a *Allocator) Port(serviceName string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.allocated[serviceName]
}

// AllocateTemporary allocates a port under a compound key "service__suffix".
// Used for blue-green deploys where a second instance needs a temporary port.
func (a *Allocator) AllocateTemporary(service, suffix string) (int, error) {
	key := service + "__" + suffix
	return a.Allocate(key)
}

// ReleaseTemporary frees a temporary port allocation.
func (a *Allocator) ReleaseTemporary(service, suffix string) {
	key := service + "__" + suffix
	a.Release(key)
}

// Reassign atomically moves a port allocation from one key to another.
// The fromKey must exist and the toKey must not already have a port allocated.
func (a *Allocator) Reassign(fromKey, toKey string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	port, ok := a.allocated[fromKey]
	if !ok {
		return fmt.Errorf("no allocation for key %q", fromKey)
	}

	if _, exists := a.allocated[toKey]; exists {
		return fmt.Errorf("key %q already has a port allocated", toKey)
	}

	delete(a.allocated, fromKey)
	a.allocated[toKey] = port
	a.usedPorts[port] = toKey
	return nil
}

func isPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}
