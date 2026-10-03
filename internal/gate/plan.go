package gate

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// PlanSchema identifies a plan document.
const PlanSchema = "aspect-gate-plan/v1"

// Change is one file a diff touched, with its line counts. Binary files
// count zero lines.
type Change struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
}

// Plan is the list of agents that apply to a change. It is written next to
// the reports so Check knows which reports must exist.
type Plan struct {
	Schema       string    `json:"schema"`
	Spec         string    `json:"spec"`
	Base         string    `json:"base"`
	Head         string    `json:"head"`
	ChangedFiles int       `json:"changed_files"`
	ChangedLines int       `json:"changed_lines"`
	Agents       []Planned `json:"agents"`
}

// Planned is one agent the orchestrator must run.
type Planned struct {
	Agent  string `json:"agent"`
	Mode   string `json:"mode"`
	Reason string `json:"reason"`
	// Files are the changed paths that made the gate apply, so the agent can
	// start from them.
	Files []string `json:"files"`
}

// BuildPlan decides which gates apply to the changes, in the order
// gates.yaml lists them. A gate applies when at least one changed file
// matches its paths and its When condition, if any, holds.
func BuildPlan(c *Config, base, head string, changes []Change) *Plan {
	p := &Plan{Schema: PlanSchema, Spec: c.Spec, Base: base, Head: head, ChangedFiles: len(changes), Agents: []Planned{}}
	for _, ch := range changes {
		p.ChangedLines += ch.Added + ch.Deleted
	}
	for _, g := range c.Gates {
		var files []string
		lines := 0
		for _, ch := range changes {
			if matchAny(g.Paths, ch.Path) {
				files = append(files, ch.Path)
				lines += ch.Added + ch.Deleted
			}
		}
		if len(files) == 0 {
			continue
		}
		reason := fmt.Sprintf("%d changed file(s) match its paths", len(files))
		if w := g.When; w != nil {
			var touched []string
			for _, f := range files {
				if matchAny(w.OrTouches, f) {
					touched = append(touched, f)
				}
			}
			switch {
			case len(touched) > 0:
				reason = "touches " + strings.Join(touched, ", ")
			case w.MinChangedLines > 0 && lines >= w.MinChangedLines:
				reason = fmt.Sprintf("%d changed lines in matching files (threshold %d)", lines, w.MinChangedLines)
			default:
				continue
			}
		}
		p.Agents = append(p.Agents, Planned{Agent: g.Agent, Mode: g.Mode, Reason: reason, Files: files})
	}
	return p
}

// Agent reports whether the plan includes the named agent.
func (p *Plan) Agent(name string) (Planned, bool) {
	i := slices.IndexFunc(p.Agents, func(a Planned) bool { return a.Agent == name })
	if i < 0 {
		return Planned{}, false
	}
	return p.Agents[i], true
}

func matchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if Match(p, name) {
			return true
		}
	}
	return false
}

// Match reports whether a slash-separated path matches a glob pattern.
// Segments follow path.Match; a segment that is exactly "**" matches zero or
// more whole segments, so "**/*.go" matches "main.go" and "a/b/c.go", and
// ".github/**" matches everything under .github.
func Match(pattern, name string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			if len(rest) == 0 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if matchSegments(rest, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], name[0]); !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// checkPattern rejects patterns path.Match cannot parse, so a typo in
// gates.yaml fails loudly instead of silently never matching.
func checkPattern(p string) error {
	if p == "" {
		return fmt.Errorf("empty path pattern")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "**" {
			continue
		}
		if _, err := path.Match(seg, ""); err != nil {
			return fmt.Errorf("bad path pattern %q: %w", p, err)
		}
	}
	return nil
}
