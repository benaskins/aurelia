package daemon

import (
	"testing"
	"time"

	"github.com/benaskins/aurelia/internal/spec"
)

func makeSpec(name string, after, requires []string) *spec.ServiceSpec {
	s := &spec.ServiceSpec{
		Service: spec.Service{Name: name, Type: "native", Command: "sleep 30"},
	}
	if len(after) > 0 || len(requires) > 0 {
		s.Dependencies = &spec.Dependencies{
			After:    after,
			Requires: requires,
		}
	}
	return s
}

func TestStartOrderNoDeps(t *testing.T) {
	g := newDepGraph([]*spec.ServiceSpec{
		makeSpec("a", nil, nil),
		makeSpec("b", nil, nil),
		makeSpec("c", nil, nil),
	})

	order, err := g.startOrder()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("expected 3, got %d", len(order))
	}
}

func TestStartOrderLinearChain(t *testing.T) {
	// c depends on b depends on a
	g := newDepGraph([]*spec.ServiceSpec{
		makeSpec("a", nil, nil),
		makeSpec("b", []string{"a"}, nil),
		makeSpec("c", []string{"b"}, []string{"b"}),
	})

	order, err := g.startOrder()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idx := make(map[string]int)
	for i, name := range order {
		idx[name] = i
	}

	if idx["a"] >= idx["b"] {
		t.Errorf("expected a before b, got a=%d b=%d", idx["a"], idx["b"])
	}
	if idx["b"] >= idx["c"] {
		t.Errorf("expected b before c, got b=%d c=%d", idx["b"], idx["c"])
	}
}

func TestStartOrderDiamond(t *testing.T) {
	// d depends on b and c, both depend on a
	g := newDepGraph([]*spec.ServiceSpec{
		makeSpec("a", nil, nil),
		makeSpec("b", []string{"a"}, nil),
		makeSpec("c", []string{"a"}, nil),
		makeSpec("d", []string{"b", "c"}, nil),
	})

	order, err := g.startOrder()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idx := make(map[string]int)
	for i, name := range order {
		idx[name] = i
	}

	if idx["a"] >= idx["b"] || idx["a"] >= idx["c"] {
		t.Errorf("a must come before b and c: %v", order)
	}
	if idx["b"] >= idx["d"] || idx["c"] >= idx["d"] {
		t.Errorf("b and c must come before d: %v", order)
	}
}

func TestStartOrderCycleDetected(t *testing.T) {
	g := newDepGraph([]*spec.ServiceSpec{
		makeSpec("a", []string{"b"}, nil),
		makeSpec("b", []string{"a"}, nil),
	})

	_, err := g.startOrder()
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
}

func TestStopOrderReverseOfStart(t *testing.T) {
	g := newDepGraph([]*spec.ServiceSpec{
		makeSpec("a", nil, nil),
		makeSpec("b", []string{"a"}, nil),
		makeSpec("c", []string{"b"}, nil),
	})

	start, err := g.startOrder()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stop, err := g.stopOrder()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i := range start {
		if start[i] != stop[len(stop)-1-i] {
			t.Errorf("stop order should reverse start: start=%v stop=%v", start, stop)
			break
		}
	}
}

func TestCascadeStopTargets(t *testing.T) {
	// c requires b requires a
	g := newDepGraph([]*spec.ServiceSpec{
		makeSpec("a", nil, nil),
		makeSpec("b", []string{"a"}, []string{"a"}),
		makeSpec("c", []string{"b"}, []string{"b"}),
	})

	targets := g.cascadeStopTargets("a")
	if len(targets) != 2 {
		t.Fatalf("expected 2 cascade targets, got %d: %v", len(targets), targets)
	}

	has := make(map[string]bool)
	for _, t := range targets {
		has[t] = true
	}
	if !has["b"] || !has["c"] {
		t.Errorf("expected b and c in cascade targets, got %v", targets)
	}
}

func TestCascadeStopNoRequires(t *testing.T) {
	// b is "after" a but doesn't "require" a — no cascade
	g := newDepGraph([]*spec.ServiceSpec{
		makeSpec("a", nil, nil),
		makeSpec("b", []string{"a"}, nil),
	})

	targets := g.cascadeStopTargets("a")
	if len(targets) != 0 {
		t.Errorf("expected no cascade targets, got %v", targets)
	}
}

func TestExternalServiceInDependencyOrder(t *testing.T) {
	// External service participates in dep ordering like any other
	ext := &spec.ServiceSpec{
		Service: spec.Service{Name: "ollama", Type: "external"},
		Health: &spec.HealthCheck{
			Type:     "http",
			Path:     "/",
			Port:     11434,
			Interval: spec.Duration{Duration: 15 * time.Second},
			Timeout:  spec.Duration{Duration: 3 * time.Second},
		},
	}
	app := makeSpec("chat", []string{"ollama"}, nil)

	g := newDepGraph([]*spec.ServiceSpec{ext, app})

	order, err := g.startOrder()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idx := make(map[string]int)
	for i, name := range order {
		idx[name] = i
	}

	if idx["ollama"] >= idx["chat"] {
		t.Errorf("expected ollama before chat, got ollama=%d chat=%d", idx["ollama"], idx["chat"])
	}
}

func TestStartOrderSkipsUnknownDeps(t *testing.T) {
	// b depends on "external" which isn't in the graph — should be skipped
	g := newDepGraph([]*spec.ServiceSpec{
		makeSpec("a", nil, nil),
		makeSpec("b", []string{"external"}, nil),
	})

	order, err := g.startOrder()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 2 {
		t.Fatalf("expected 2 services, got %d", len(order))
	}
}
