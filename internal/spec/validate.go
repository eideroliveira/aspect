package spec

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Severity of a validation issue.
type Severity string

const (
	// Error issues make the spec unusable; the pipeline refuses to run.
	Error Severity = "error"
	// Warning issues are reported but do not block the pipeline.
	Warning Severity = "warning"
)

// Issue is one validation finding, addressed by a dotted path into the spec.
type Issue struct {
	Severity Severity
	Path     string
	Message  string
}

func (i Issue) String() string {
	return fmt.Sprintf("%s: %s: %s", i.Severity, i.Path, i.Message)
}

// Issues is a list of findings with helpers for the CLI and the pipeline.
type Issues []Issue

// HasErrors reports whether any issue is blocking.
func (is Issues) HasErrors() bool {
	for _, i := range is {
		if i.Severity == Error {
			return true
		}
	}
	return false
}

var identRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Validate checks the semantic rules of a spec: identifiers, references,
// dependency cycles and goal coverage. It returns every issue found rather than
// stopping at the first one, so a user can fix a spec in one pass.
func Validate(s *Spec) Issues {
	var out Issues
	add := func(sev Severity, path, format string, args ...any) {
		out = append(out, Issue{Severity: sev, Path: path, Message: fmt.Sprintf(format, args...)})
	}

	if s.Aspect != Version {
		add(Error, "aspect", "unsupported spec version %d (this build understands %d)", s.Aspect, Version)
	}
	if s.System.Name == "" {
		add(Error, "system.name", "is required")
	} else if !identRe.MatchString(s.System.Name) {
		add(Error, "system.name", "%q must match %s", s.System.Name, identRe)
	}
	if strings.TrimSpace(s.System.Intent) == "" {
		add(Error, "system.intent", "is required: agents cannot judge a system without a stated intent")
	}
	if s.System.Language != "go" {
		add(Error, "system.language", "%q is not supported yet (only \"go\")", s.System.Language)
	}
	if s.System.ModulePath == "" {
		add(Error, "system.module_path", "is required (the Go module path of the generated code)")
	}
	if len(s.System.Goals) == 0 {
		add(Error, "system.goals", "at least one goal is required: without goals there is nothing to validate against")
	}

	goals := map[string]bool{}
	for i, g := range s.System.Goals {
		p := fmt.Sprintf("system.goals[%d]", i)
		if g.ID == "" {
			add(Error, p+".id", "is required")
		} else if goals[g.ID] {
			add(Error, p+".id", "duplicate goal id %q", g.ID)
		}
		goals[g.ID] = true
		if strings.TrimSpace(g.Statement) == "" {
			add(Error, p+".statement", "is required")
		}
		switch g.Verify {
		case VerifyTest, VerifyInvariant, VerifyReview:
		default:
			add(Error, p+".verify", "%q is not one of test, invariant, review", g.Verify)
		}
	}

	if len(s.Modules) == 0 {
		add(Error, "modules", "at least one module is required")
	}
	mods := map[string]bool{}
	covered := map[string]bool{}
	for i, m := range s.Modules {
		p := fmt.Sprintf("modules[%d]", i)
		if m.Name == "" {
			add(Error, p+".name", "is required")
		} else if !identRe.MatchString(m.Name) {
			add(Error, p+".name", "%q must match %s", m.Name, identRe)
		} else if mods[m.Name] {
			add(Error, p+".name", "duplicate module name %q", m.Name)
		}
		mods[m.Name] = true
		if strings.TrimSpace(m.Intent) == "" {
			add(Error, p+".intent", "is required")
		}
		if len(m.Goals) == 0 {
			add(Warning, p+".goals", "module %q owns no goal; the Validator will only check its intent", m.Name)
		}
		for j, g := range m.Goals {
			if !goals[g] {
				add(Error, fmt.Sprintf("%s.goals[%d]", p, j), "unknown goal %q", g)
			}
			covered[g] = true
		}
		if len(m.Scenarios) == 0 && len(m.Invariants) == 0 {
			add(Warning, p, "module %q has no scenarios or invariants; tests will be inferred from the interface only", m.Name)
		}
		seenScenario := map[string]bool{}
		for j, sc := range m.Scenarios {
			sp := fmt.Sprintf("%s.scenarios[%d]", p, j)
			if sc.ID == "" {
				add(Error, sp+".id", "is required")
			} else if seenScenario[sc.ID] {
				add(Error, sp+".id", "duplicate scenario id %q in module %q", sc.ID, m.Name)
			}
			seenScenario[sc.ID] = true
			if sc.When == "" || sc.Then == "" {
				add(Error, sp, "scenario needs at least `when` and `then`")
			}
			for k, g := range sc.Goals {
				if !goals[g] {
					add(Error, fmt.Sprintf("%s.goals[%d]", sp, k), "unknown goal %q", g)
				}
				covered[g] = true
			}
		}
		for j, op := range m.Interface {
			op_ := fmt.Sprintf("%s.interface[%d]", p, j)
			if op.Name == "" {
				add(Error, op_+".name", "is required")
			}
			if op.Signature == "" {
				add(Error, op_+".signature", "is required so dependents can be generated against a stable contract")
			}
		}
	}

	// Dependency references and cycles.
	for i, m := range s.Modules {
		for j, d := range m.DependsOn {
			p := fmt.Sprintf("modules[%d].depends_on[%d]", i, j)
			if d == m.Name {
				add(Error, p, "module %q depends on itself", m.Name)
			} else if !mods[d] {
				add(Error, p, "unknown module %q", d)
			}
		}
	}
	if cycle := findCycle(s); cycle != nil {
		add(Error, "modules", "dependency cycle: %s", strings.Join(cycle, " -> "))
	}

	// Goal coverage: a goal nobody owns can never be validated.
	var uncovered []string
	for id := range goals {
		if id != "" && !covered[id] {
			uncovered = append(uncovered, id)
		}
	}
	sort.Strings(uncovered)
	for _, id := range uncovered {
		add(Error, "system.goals", "goal %q is not owned by any module or scenario", id)
	}

	return out
}

// findCycle returns one dependency cycle as a path (closed, first == last), or nil.
func findCycle(s *Spec) []string {
	const (
		white = iota
		gray
		black
	)
	color := map[string]int{}
	var stack []string
	var cycle []string

	var visit func(name string) bool
	visit = func(name string) bool {
		color[name] = gray
		stack = append(stack, name)
		m := s.Module(name)
		if m != nil {
			for _, d := range m.DependsOn {
				switch color[d] {
				case gray:
					// Close the loop from the first occurrence of d on the stack.
					for i, n := range stack {
						if n == d {
							cycle = append(append([]string{}, stack[i:]...), d)
							return true
						}
					}
				case white:
					if s.Module(d) != nil && visit(d) {
						return true
					}
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[name] = black
		return false
	}
	names := make([]string, 0, len(s.Modules))
	for _, m := range s.Modules {
		names = append(names, m.Name)
	}
	sort.Strings(names)
	for _, n := range names {
		if color[n] == white && visit(n) {
			return cycle
		}
	}
	return nil
}
