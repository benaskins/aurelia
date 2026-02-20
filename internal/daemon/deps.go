package daemon

import (
	"fmt"
	"slices"

	"github.com/benaskins/aurelia/internal/spec"
)

// depGraph builds a dependency-ordered startup sequence and reverse-ordered shutdown.
type depGraph struct {
	specs map[string]*spec.ServiceSpec
	// after[A] = [B, C] means A must start after B and C
	after map[string][]string
	// requires[A] = [B] means A hard-depends on B (cascade stop)
	requires map[string][]string
	// dependents[B] = [A] means A depends on B (reverse of requires)
	dependents map[string][]string
}

func newDepGraph(specs []*spec.ServiceSpec) *depGraph {
	g := &depGraph{
		specs:      make(map[string]*spec.ServiceSpec),
		after:      make(map[string][]string),
		requires:   make(map[string][]string),
		dependents: make(map[string][]string),
	}

	for _, s := range specs {
		name := s.Service.Name
		g.specs[name] = s
		if s.Dependencies != nil {
			g.after[name] = s.Dependencies.After
			g.requires[name] = s.Dependencies.Requires
			for _, dep := range s.Dependencies.Requires {
				g.dependents[dep] = append(g.dependents[dep], name)
			}
		}
	}

	return g
}

// startOrder returns services in dependency order (dependencies first).
// Returns an error if there's a cycle.
func (g *depGraph) startOrder() ([]string, error) {
	visited := make(map[string]bool)
	inStack := make(map[string]bool) // cycle detection
	var order []string

	var visit func(name string) error
	visit = func(name string) error {
		if inStack[name] {
			return fmt.Errorf("dependency cycle detected at %q", name)
		}
		if visited[name] {
			return nil
		}

		inStack[name] = true

		// Visit all dependencies first
		for _, dep := range g.after[name] {
			if _, exists := g.specs[dep]; !exists {
				continue // skip unknown deps (may not be loaded)
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		for _, dep := range g.requires[name] {
			if _, exists := g.specs[dep]; !exists {
				continue
			}
			if err := visit(dep); err != nil {
				return err
			}
		}

		inStack[name] = false
		visited[name] = true
		order = append(order, name)
		return nil
	}

	for name := range g.specs {
		if err := visit(name); err != nil {
			return nil, err
		}
	}

	return order, nil
}

// stopOrder returns services in reverse dependency order (dependents first).
func (g *depGraph) stopOrder() ([]string, error) {
	order, err := g.startOrder()
	if err != nil {
		return nil, err
	}
	slices.Reverse(order)
	return order, nil
}

// cascadeStopTargets returns all services that should be stopped when
// the given service stops (hard dependents via requires).
func (g *depGraph) cascadeStopTargets(name string) []string {
	var targets []string
	visited := make(map[string]bool)

	var collect func(n string)
	collect = func(n string) {
		for _, dep := range g.dependents[n] {
			if !visited[dep] {
				visited[dep] = true
				targets = append(targets, dep)
				collect(dep) // transitive dependents
			}
		}
	}

	collect(name)
	return targets
}
