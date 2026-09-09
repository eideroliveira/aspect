// Package plan turns a validated spec into an ordered build plan.
//
// The planner is deliberately deterministic: module order comes from the
// dependency graph, not from a model. Determinism here means two runs of the
// same spec produce the same plan, which makes reports comparable across runs.
package plan

import (
	"fmt"
	"sort"

	"github.com/eideroliveira/aspect/internal/spec"
)

// Step is the work the pipeline performs for one module.
type Step struct {
	Module    string
	DependsOn []string
	// Goals the Validator must reach a verdict on for this module: the module's
	// own goals plus those named by its scenarios.
	Goals []string
	// Scenarios the Tester must cover.
	Scenarios int
	// Invariants the Tester must probe and the Validator must judge.
	Invariants int
}

// Plan is the ordered list of steps.
type Plan struct {
	System string
	Steps  []Step
}

// Order returns module names in build order.
func (p *Plan) Order() []string {
	out := make([]string, len(p.Steps))
	for i, s := range p.Steps {
		out[i] = s.Module
	}
	return out
}

// Build computes a topological order of modules (Kahn's algorithm) with ties
// broken alphabetically. It assumes the spec passed Validate; on a cycle it
// returns an error rather than a partial plan.
func Build(s *spec.Spec) (*Plan, error) {
	indeg := map[string]int{}
	dependents := map[string][]string{}
	for _, m := range s.Modules {
		if _, ok := indeg[m.Name]; !ok {
			indeg[m.Name] = 0
		}
		for _, d := range m.DependsOn {
			indeg[m.Name]++
			dependents[d] = append(dependents[d], m.Name)
		}
	}

	var ready []string
	for name, n := range indeg {
		if n == 0 {
			ready = append(ready, name)
		}
	}
	sort.Strings(ready)

	p := &Plan{System: s.System.Name}
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		m := s.Module(name)
		p.Steps = append(p.Steps, stepFor(m))
		next := append([]string{}, dependents[name]...)
		sort.Strings(next)
		for _, d := range next {
			indeg[d]--
			if indeg[d] == 0 {
				ready = append(ready, d)
				sort.Strings(ready)
			}
		}
	}
	if len(p.Steps) != len(s.Modules) {
		return nil, fmt.Errorf("plan: dependency cycle among modules (planned %d of %d)", len(p.Steps), len(s.Modules))
	}
	return p, nil
}

func stepFor(m *spec.Module) Step {
	seen := map[string]bool{}
	var goals []string
	addGoal := func(g string) {
		if !seen[g] {
			seen[g] = true
			goals = append(goals, g)
		}
	}
	for _, g := range m.Goals {
		addGoal(g)
	}
	for _, sc := range m.Scenarios {
		for _, g := range sc.Goals {
			addGoal(g)
		}
	}
	sort.Strings(goals)
	return Step{
		Module:     m.Name,
		DependsOn:  append([]string{}, m.DependsOn...),
		Goals:      goals,
		Scenarios:  len(m.Scenarios),
		Invariants: len(m.Invariants),
	}
}
