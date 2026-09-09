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

var (
	identRe  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	entityRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
)

// SupportedLanguages lists the languages the pipeline can build and test.
var SupportedLanguages = []string{"go", "swift"}

var (
	dbEngines      = []string{"postgres", "mysql", "sqlite"}
	migrationModes = []string{"auto", "files"}
	relationKinds  = []string{"has_one", "has_many", "belongs_to", "many_to_many"}
	interfaceKinds = []string{"http", "web", "cli", "grpc"}
)

type collector struct {
	issues Issues
}

func (c *collector) add(sev Severity, path, format string, args ...any) {
	c.issues = append(c.issues, Issue{Severity: sev, Path: path, Message: fmt.Sprintf(format, args...)})
}

func oneOf(v string, set []string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

// Validate checks the semantic rules of a spec: identifiers, references,
// dependency cycles, goal coverage, and ownership of entities and surfaces.
// It returns every issue found rather than stopping at the first one, so a
// user can fix a spec in one pass.
func Validate(s *Spec) Issues {
	c := &collector{}
	goals := validateSystem(c, s)
	validateStack(c, s)
	entities := validateDatabase(c, s)
	surfaces := validateInterfaces(c, s)
	validateModules(c, s, goals, entities, surfaces)
	return c.issues
}

func validateSystem(c *collector, s *Spec) map[string]bool {
	if s.Aspect != Version {
		c.add(Error, "aspect", "unsupported spec version %d (this build understands %d)", s.Aspect, Version)
	}
	if s.System.Name == "" {
		c.add(Error, "system.name", "is required")
	} else if !identRe.MatchString(s.System.Name) {
		c.add(Error, "system.name", "%q must match %s", s.System.Name, identRe)
	}
	if strings.TrimSpace(s.System.Intent) == "" {
		c.add(Error, "system.intent", "is required: agents cannot judge a system without a stated intent")
	}
	if !oneOf(s.System.Language, SupportedLanguages) {
		c.add(Error, "system.language", "%q is not supported (one of %s)", s.System.Language, strings.Join(SupportedLanguages, ", "))
	}
	if s.System.ModulePath == "" {
		c.add(Error, "system.module_path", "is required (the module or package path of the generated code)")
	}
	if len(s.System.Goals) == 0 {
		c.add(Error, "system.goals", "at least one goal is required: without goals there is nothing to validate against")
	}
	goals := map[string]bool{}
	for i, g := range s.System.Goals {
		p := fmt.Sprintf("system.goals[%d]", i)
		if g.ID == "" {
			c.add(Error, p+".id", "is required")
		} else if goals[g.ID] {
			c.add(Error, p+".id", "duplicate goal id %q", g.ID)
		}
		goals[g.ID] = true
		if strings.TrimSpace(g.Statement) == "" {
			c.add(Error, p+".statement", "is required")
		}
		switch g.Verify {
		case VerifyTest, VerifyInvariant, VerifyReview:
		default:
			c.add(Error, p+".verify", "%q is not one of test, invariant, review", g.Verify)
		}
	}
	return goals
}

func validateStack(c *collector, s *Spec) {
	langs := make([]string, 0, len(s.System.Stack))
	for l := range s.System.Stack {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	for _, lang := range langs {
		ls := s.System.Stack[lang]
		p := "system.stack." + lang
		if lang != s.System.Language {
			c.add(Warning, p, "configured for %q but system.language is %q; it will be ignored", lang, s.System.Language)
		}
		names := map[string]bool{}
		for i, f := range ls.Frameworks {
			fp := fmt.Sprintf("%s.frameworks[%d]", p, i)
			if f.Name == "" {
				c.add(Error, fp+".name", "is required")
			} else if names[f.Name] {
				c.add(Error, fp+".name", "duplicate framework %q", f.Name)
			}
			names[f.Name] = true
			if f.Module == "" {
				c.add(Error, fp+".module", "is required (the import path agents must use for %q)", f.Name)
			}
		}
		if ls.ORM != nil && ls.ORM.Module == "" {
			c.add(Error, p+".orm.module", "is required")
		}
		for i, m := range ls.AllowedModules {
			if strings.TrimSpace(m) == "" {
				c.add(Error, fmt.Sprintf("%s.allowed_modules[%d]", p, i), "is empty")
			}
		}
	}
}

func validateDatabase(c *collector, s *Spec) map[string]bool {
	entities := map[string]bool{}
	db := s.System.Database
	if db == nil {
		return entities
	}
	const p = "system.database"
	if !oneOf(db.Engine, dbEngines) {
		c.add(Error, p+".engine", "%q is not one of %s", db.Engine, strings.Join(dbEngines, ", "))
	}
	if !oneOf(db.Migrations, migrationModes) {
		c.add(Error, p+".migrations", "%q is not one of %s", db.Migrations, strings.Join(migrationModes, ", "))
	}
	if !oneOf(db.Test.Engine, dbEngines) {
		c.add(Error, p+".test.engine", "%q is not one of %s", db.Test.Engine, strings.Join(dbEngines, ", "))
	}
	if db.Test.Engine != "sqlite" && db.Test.DSNEnv == "" {
		c.add(Warning, p+".test", "tests run against %s but no dsn_env is set; generated tests will need a running database", db.Test.Engine)
	}
	if len(db.Entities) == 0 {
		c.add(Error, p+".entities", "a database with no entities declares nothing to persist")
	}
	for i, e := range db.Entities {
		ep := fmt.Sprintf("%s.entities[%d]", p, i)
		if e.Name == "" {
			c.add(Error, ep+".name", "is required")
		} else if !entityRe.MatchString(e.Name) {
			c.add(Error, ep+".name", "%q must match %s", e.Name, entityRe)
		} else if entities[e.Name] {
			c.add(Error, ep+".name", "duplicate entity %q", e.Name)
		}
		entities[e.Name] = true
		if strings.TrimSpace(e.Intent) == "" {
			c.add(Warning, ep+".intent", "entity %q has no intent; the Validator cannot judge whether it models the right thing", e.Name)
		}
		if len(e.Fields) == 0 {
			c.add(Error, ep+".fields", "entity %q has no fields", e.Name)
		}
		fields := map[string]bool{}
		primaries := 0
		for j, f := range e.Fields {
			fp := fmt.Sprintf("%s.fields[%d]", ep, j)
			if f.Name == "" {
				c.add(Error, fp+".name", "is required")
			} else if fields[f.Name] {
				c.add(Error, fp+".name", "duplicate field %q in entity %q", f.Name, e.Name)
			}
			fields[f.Name] = true
			if f.Type == "" {
				c.add(Error, fp+".type", "is required")
			}
			switch f.Key {
			case "":
			case "primary":
				primaries++
			default:
				c.add(Error, fp+".key", "%q is not \"primary\"", f.Key)
			}
		}
		if primaries == 0 {
			c.add(Warning, ep, "entity %q has no primary key field; agents will add a surrogate id", e.Name)
		}
	}
	// Relations are checked after all entity names are known.
	for i, e := range db.Entities {
		for j, r := range e.Relations {
			rp := fmt.Sprintf("%s.entities[%d].relations[%d]", p, i, j)
			if !oneOf(r.Kind, relationKinds) {
				c.add(Error, rp+".kind", "%q is not one of %s", r.Kind, strings.Join(relationKinds, ", "))
			}
			if !entities[r.Entity] {
				c.add(Error, rp+".entity", "unknown entity %q", r.Entity)
			}
		}
	}
	return entities
}

func validateInterfaces(c *collector, s *Spec) map[string]bool {
	surfaces := map[string]bool{}
	ls := s.LanguageStack()
	names := map[string]bool{}
	for i, iface := range s.System.Interfaces {
		p := fmt.Sprintf("system.interfaces[%d]", i)
		if iface.Name == "" {
			c.add(Error, p+".name", "is required")
		} else if !identRe.MatchString(iface.Name) {
			c.add(Error, p+".name", "%q must match %s", iface.Name, identRe)
		} else if names[iface.Name] {
			c.add(Error, p+".name", "duplicate interface %q", iface.Name)
		}
		names[iface.Name] = true
		if !oneOf(iface.Kind, interfaceKinds) {
			c.add(Error, p+".kind", "%q is not one of %s", iface.Kind, strings.Join(interfaceKinds, ", "))
		}
		if strings.TrimSpace(iface.Intent) == "" {
			c.add(Error, p+".intent", "is required")
		}
		if iface.Framework != "" {
			found := false
			if ls != nil {
				for _, f := range ls.Frameworks {
					if f.Name == iface.Framework {
						found = true
					}
				}
			}
			if !found {
				c.add(Error, p+".framework", "%q is not declared in system.stack.%s.frameworks", iface.Framework, s.System.Language)
			}
		}
		if len(iface.Surfaces) == 0 {
			c.add(Error, p+".surfaces", "interface %q exposes nothing", iface.Name)
		}
		seen := map[string]bool{}
		for j, sf := range iface.Surfaces {
			sp := fmt.Sprintf("%s.surfaces[%d]", p, j)
			if sf.Name == "" {
				c.add(Error, sp+".name", "is required")
			} else if !identRe.MatchString(sf.Name) {
				c.add(Error, sp+".name", "%q must match %s", sf.Name, identRe)
			} else if seen[sf.Name] {
				c.add(Error, sp+".name", "duplicate surface %q in interface %q", sf.Name, iface.Name)
			}
			seen[sf.Name] = true
			surfaces[iface.Name+"."+sf.Name] = true
			switch iface.Kind {
			case "http":
				if sf.Route == "" || sf.Method == "" {
					c.add(Error, sp, "http surfaces need route and method")
				}
			case "web", "grpc":
				if sf.Route == "" {
					c.add(Error, sp+".route", "is required for %s surfaces", iface.Kind)
				}
			}
			if sf.Entity != "" && s.Entity(sf.Entity) == nil {
				c.add(Error, sp+".entity", "unknown entity %q", sf.Entity)
			}
			if len(sf.Operations) > 0 && sf.Entity == "" {
				c.add(Warning, sp+".operations", "operations are listed but no entity is named")
			}
		}
	}
	return surfaces
}

func validateModules(c *collector, s *Spec, goals, entities, surfaces map[string]bool) {
	if len(s.Modules) == 0 {
		c.add(Error, "modules", "at least one module is required")
	}
	mods := map[string]bool{}
	covered := map[string]bool{}
	entityOwner := map[string][]string{}
	surfaceOwner := map[string][]string{}
	for i, m := range s.Modules {
		p := fmt.Sprintf("modules[%d]", i)
		if m.Name == "" {
			c.add(Error, p+".name", "is required")
		} else if !identRe.MatchString(m.Name) {
			c.add(Error, p+".name", "%q must match %s", m.Name, identRe)
		} else if mods[m.Name] {
			c.add(Error, p+".name", "duplicate module name %q", m.Name)
		}
		mods[m.Name] = true
		if strings.TrimSpace(m.Intent) == "" {
			c.add(Error, p+".intent", "is required")
		}
		if len(m.Goals) == 0 {
			c.add(Warning, p+".goals", "module %q owns no goal; the Validator will only check its intent", m.Name)
		}
		for j, g := range m.Goals {
			if !goals[g] {
				c.add(Error, fmt.Sprintf("%s.goals[%d]", p, j), "unknown goal %q", g)
			}
			covered[g] = true
		}
		if len(m.Scenarios) == 0 && len(m.Invariants) == 0 {
			c.add(Warning, p, "module %q has no scenarios or invariants; tests will be inferred from the interface only", m.Name)
		}
		seenScenario := map[string]bool{}
		for j, sc := range m.Scenarios {
			sp := fmt.Sprintf("%s.scenarios[%d]", p, j)
			if sc.ID == "" {
				c.add(Error, sp+".id", "is required")
			} else if seenScenario[sc.ID] {
				c.add(Error, sp+".id", "duplicate scenario id %q in module %q", sc.ID, m.Name)
			}
			seenScenario[sc.ID] = true
			if sc.When == "" || sc.Then == "" {
				c.add(Error, sp, "scenario needs at least `when` and `then`")
			}
			for k, g := range sc.Goals {
				if !goals[g] {
					c.add(Error, fmt.Sprintf("%s.goals[%d]", sp, k), "unknown goal %q", g)
				}
				covered[g] = true
			}
		}
		for j, op := range m.Interface {
			op_ := fmt.Sprintf("%s.interface[%d]", p, j)
			if op.Name == "" {
				c.add(Error, op_+".name", "is required")
			}
			if op.Signature == "" {
				c.add(Error, op_+".signature", "is required so dependents can be generated against a stable contract")
			}
		}
		for j, e := range m.Entities {
			if !entities[e] {
				c.add(Error, fmt.Sprintf("%s.entities[%d]", p, j), "unknown entity %q", e)
				continue
			}
			entityOwner[e] = append(entityOwner[e], m.Name)
		}
		refs, errs := s.ResolveSurfaces(m.Surfaces)
		for _, err := range errs {
			c.add(Error, p+".surfaces", "%v", err)
		}
		for _, r := range refs {
			surfaceOwner[r.ID()] = append(surfaceOwner[r.ID()], m.Name)
		}
	}

	// Dependency references and cycles.
	for i, m := range s.Modules {
		for j, d := range m.DependsOn {
			p := fmt.Sprintf("modules[%d].depends_on[%d]", i, j)
			if d == m.Name {
				c.add(Error, p, "module %q depends on itself", m.Name)
			} else if !mods[d] {
				c.add(Error, p, "unknown module %q", d)
			}
		}
	}
	if cycle := findCycle(s); cycle != nil {
		c.add(Error, "modules", "dependency cycle: %s", strings.Join(cycle, " -> "))
	}

	// Goal coverage: a goal nobody owns can never be validated.
	for _, id := range sortedKeys(goals) {
		if id != "" && !covered[id] {
			c.add(Error, "system.goals", "goal %q is not owned by any module or scenario", id)
		}
	}
	// Entity ownership: exactly one module writes each entity.
	for _, e := range sortedKeys(entities) {
		switch owners := entityOwner[e]; len(owners) {
		case 0:
			c.add(Error, "system.database.entities", "entity %q is not owned by any module", e)
		case 1:
		default:
			c.add(Error, "modules", "entity %q is owned by several modules (%s); pick one owner", e, strings.Join(owners, ", "))
		}
	}
	// Surface ownership: every surface is implemented somewhere, once.
	for _, sf := range sortedKeys(surfaces) {
		switch owners := surfaceOwner[sf]; len(owners) {
		case 0:
			c.add(Error, "system.interfaces", "surface %q is not implemented by any module", sf)
		case 1:
		default:
			c.add(Error, "modules", "surface %q is implemented by several modules (%s)", sf, strings.Join(owners, ", "))
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
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
