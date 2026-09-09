// Package spec defines the Aspect specification format: a formal, machine-checkable
// description of a system, its modules, their intents, and the goals that decide
// whether the generated implementation is acceptable.
//
// The spec is the single source of truth for every agent in the pipeline. Agents
// never invent requirements; they read them from here.
package spec

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Version is the only spec format version this build understands.
const Version = 1

// VerifyMethod tells the pipeline how a goal is checked.
type VerifyMethod string

const (
	// VerifyTest means the goal is demonstrated by scenarios that become tests.
	VerifyTest VerifyMethod = "test"
	// VerifyInvariant means the goal is a property that must hold in every state;
	// it becomes property-style tests plus a Validator review.
	VerifyInvariant VerifyMethod = "invariant"
	// VerifyReview means the goal cannot be executed and is judged by the
	// Validator agent reading the code (for example, "the API is idiomatic").
	VerifyReview VerifyMethod = "review"
)

// Spec is the root document.
type Spec struct {
	Aspect  int      `yaml:"aspect"`
	System  System   `yaml:"system"`
	Modules []Module `yaml:"modules"`
}

// System describes the whole program the agents must produce.
type System struct {
	Name        string   `yaml:"name"`
	Intent      string   `yaml:"intent,omitempty"`
	Language    string   `yaml:"language,omitempty"`
	ModulePath  string   `yaml:"module_path,omitempty"`
	Goals       []Goal   `yaml:"goals,omitempty"`
	Constraints []string `yaml:"constraints,omitempty"`
	// Stack holds language-specific configuration keyed by language name
	// ("go", "python", ...). Only the entry for System.Language is used.
	Stack Stack `yaml:"stack,omitempty"`
	// Database, when present, declares the persistent data model.
	Database *Database `yaml:"database,omitempty"`
	// Interfaces declare how the system is exposed: HTTP APIs, web UIs,
	// command lines, gRPC services, native app screens.
	Interfaces []Interface `yaml:"interfaces,omitempty"`
	// Source records where an imported spec came from. Absent for specs
	// written by hand.
	Source *Source `yaml:"source,omitempty"`
}

// Source is the provenance of a spec produced by `aspect import`.
type Source struct {
	Language   string `yaml:"language,omitempty"`
	Repository string `yaml:"repository,omitempty"`
	Commit     string `yaml:"commit,omitempty"`
	ImportedAt string `yaml:"imported_at,omitempty"`
	// Frameworks detected in the source, kept for reference when the target
	// language differs and the stack section cannot carry them.
	Frameworks []string `yaml:"frameworks,omitempty"`
}

// Goal is an outcome the finished system must achieve. Every goal is owned by at
// least one module, and the Validator issues a verdict per goal.
type Goal struct {
	ID        string       `yaml:"id"`
	Statement string       `yaml:"statement"`
	Verify    VerifyMethod `yaml:"verify"`
}

// Stack maps a language name to its configuration.
type Stack map[string]LanguageStack

// LanguageStack is the language-specific configuration: which frameworks and
// libraries the generated code may use and how. The shape is the same for
// every language; "module" means an import path in Go, a package in Python.
type LanguageStack struct {
	// Version of the language toolchain, e.g. "1.26".
	Version string `yaml:"version,omitempty"`
	// Frameworks the system is built on, e.g. qor5 for a Go admin UI.
	Frameworks []Framework `yaml:"frameworks,omitempty"`
	// ORM, when the database is accessed through one.
	ORM *Framework `yaml:"orm,omitempty"`
	// AllowedModules are import-path prefixes generated code may import in
	// addition to the standard library, the system's own module, and the
	// modules of Frameworks and ORM. The pipeline enforces this list.
	AllowedModules []string `yaml:"allowed_modules,omitempty"`
	// Guidance is free-text advice for the Coder and Tester: conventions,
	// project layout, idioms to follow or avoid.
	Guidance string `yaml:"guidance,omitempty"`
}

// Framework is a library the generated code builds on.
type Framework struct {
	Name    string `yaml:"name"`
	Module  string `yaml:"module,omitempty"`
	Version string `yaml:"version,omitempty"`
	Purpose string `yaml:"purpose,omitempty"`
	// Guidance tells agents how this framework is meant to be used here.
	Guidance string `yaml:"guidance,omitempty"`
}

// Database declares persistence.
type Database struct {
	// Engine is postgres, mysql or sqlite.
	Engine string `yaml:"engine"`
	// Migrations is "auto" (the ORM migrates the schema at startup) or
	// "files" (versioned migration files are generated).
	Migrations string `yaml:"migrations,omitempty"`
	// Test says which database the generated tests run against.
	Test DBTest `yaml:"test,omitempty"`
	// Entities are the persistent types.
	Entities []Entity `yaml:"entities,omitempty"`
}

// DBTest configures the database used by generated tests.
type DBTest struct {
	// Engine used by tests; sqlite means an in-memory database needing no
	// external service. Defaults to sqlite unless Database.Engine is sqlite.
	Engine string `yaml:"engine"`
	// DSNEnv names an environment variable; when it is set at test time the
	// tests connect to that DSN instead of the test engine default.
	DSNEnv string `yaml:"dsn_env,omitempty"`
}

// Entity is one persistent type (a table, a collection).
type Entity struct {
	Name        string     `yaml:"name"`
	Intent      string     `yaml:"intent,omitempty"`
	Fields      []Field    `yaml:"fields"`
	Relations   []Relation `yaml:"relations,omitempty"`
	Constraints []string   `yaml:"constraints,omitempty"`
}

// Field is one attribute of an entity.
type Field struct {
	Name string `yaml:"name"`
	// Type is expressed in the target language's terms (Go: string, int64,
	// time.Time, decimal). Agents map it to the database column type.
	Type string `yaml:"type"`
	// Key is "primary" for the primary key, empty otherwise.
	Key      string `yaml:"key,omitempty"`
	Required bool   `yaml:"required,omitempty"`
	Unique   bool   `yaml:"unique,omitempty"`
	Default  string `yaml:"default,omitempty"`
	Intent   string `yaml:"intent,omitempty"`
}

// Relation links two entities.
type Relation struct {
	// Kind is has_one, has_many, belongs_to or many_to_many.
	Kind   string `yaml:"kind"`
	Entity string `yaml:"entity,omitempty"`
	// Via names the field or join table carrying the relation.
	Via    string `yaml:"via,omitempty"`
	Intent string `yaml:"intent,omitempty"`
}

// Interface is one way the system is exposed to users or other systems.
type Interface struct {
	Name string `yaml:"name"`
	// Kind is http, web, cli, grpc or app (native screens).
	Kind   string `yaml:"kind"`
	Intent string `yaml:"intent,omitempty"`
	// Role is "provider" (default: this system serves the interface) or
	// "consumer" (this system is a client of an interface served elsewhere,
	// as an iOS app is of its backend API). Consumer surfaces are
	// implemented as clients.
	Role string `yaml:"role,omitempty"`
	// Framework names an entry in Stack[language].Frameworks used to build
	// this interface (for example "qor5" for a web admin).
	Framework string `yaml:"framework,omitempty"`
	// Auth describes who may use this interface; surfaces can override it.
	Auth     string    `yaml:"auth,omitempty"`
	Surfaces []Surface `yaml:"surfaces,omitempty"`
}

// Surface is one endpoint, page, command or RPC of an interface.
type Surface struct {
	Name   string `yaml:"name"`
	Intent string `yaml:"intent,omitempty"`
	// Route is the path (http, web), command name (cli), RPC name (grpc) or
	// navigation path (app).
	Route string `yaml:"route"`
	// Method is the HTTP method for http surfaces.
	Method string `yaml:"method,omitempty"`
	// Entity the surface operates on, when it is a CRUD surface.
	Entity string `yaml:"entity,omitempty"`
	// Operations for entity-bound surfaces: list, create, read, update, delete
	// or a custom verb.
	Operations []string `yaml:"operations,omitempty"`
	Request    string   `yaml:"request,omitempty"`
	Response   string   `yaml:"response,omitempty"`
	Errors     []string `yaml:"errors,omitempty"`
	Auth       string   `yaml:"auth,omitempty"`
}

// Module is a unit of implementation with its own intent. Modules are generated
// in dependency order, each one seeing the interfaces of its dependencies.
type Module struct {
	Name        string      `yaml:"name"`
	Intent      string      `yaml:"intent,omitempty"`
	Goals       []string    `yaml:"goals,omitempty"`
	DependsOn   []string    `yaml:"depends_on,omitempty"`
	Interface   []Operation `yaml:"interface,omitempty"`
	Invariants  []string    `yaml:"invariants,omitempty"`
	Scenarios   []Scenario  `yaml:"scenarios,omitempty"`
	Constraints []string    `yaml:"constraints,omitempty"`
	// Entities this module owns: it defines the persistent type and is the
	// only module that writes it.
	Entities []string `yaml:"entities,omitempty"`
	// Surfaces this module implements, as "interface.surface" or "interface"
	// for every surface of that interface.
	Surfaces []string `yaml:"surfaces,omitempty"`
}

// Operation is one exported function or method the module must expose, with
// optional design-by-contract clauses.
type Operation struct {
	Name      string   `yaml:"name"`
	Signature string   `yaml:"signature"`
	Intent    string   `yaml:"intent,omitempty"`
	Pre       []string `yaml:"pre,omitempty"`
	Post      []string `yaml:"post,omitempty"`
}

// Scenario is a Given/When/Then example. The Tester agent turns each scenario
// into at least one test, and the Validator checks that mapping exists.
type Scenario struct {
	ID    string   `yaml:"id"`
	Given string   `yaml:"given,omitempty"`
	When  string   `yaml:"when"`
	Then  string   `yaml:"then"`
	Goals []string `yaml:"goals,omitempty"`
}

// Load reads and parses a spec file. It does not validate semantics; call
// Validate for that.
func Load(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse decodes YAML into a Spec and applies defaults.
func Parse(data []byte) (*Spec, error) {
	var s Spec
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	s.applyDefaults()
	return &s, nil
}

func (s *Spec) applyDefaults() {
	if s.System.Language == "" {
		s.System.Language = "go"
	}
	for i := range s.System.Goals {
		if s.System.Goals[i].Verify == "" {
			s.System.Goals[i].Verify = VerifyTest
		}
	}
	if db := s.System.Database; db != nil {
		if db.Migrations == "" {
			db.Migrations = "auto"
		}
		if db.Test.Engine == "" {
			db.Test.Engine = "sqlite"
		}
	}
}

// Module returns the module with the given name, or nil.
func (s *Spec) Module(name string) *Module {
	for i := range s.Modules {
		if s.Modules[i].Name == name {
			return &s.Modules[i]
		}
	}
	return nil
}

// Goal returns the system goal with the given id, or nil.
func (s *Spec) Goal(id string) *Goal {
	for i := range s.System.Goals {
		if s.System.Goals[i].ID == id {
			return &s.System.Goals[i]
		}
	}
	return nil
}

// LanguageStack returns the stack configuration for System.Language, or nil.
func (s *Spec) LanguageStack() *LanguageStack {
	if ls, ok := s.System.Stack[s.System.Language]; ok {
		return &ls
	}
	return nil
}

// Entity returns the database entity with the given name, or nil.
func (s *Spec) Entity(name string) *Entity {
	if s.System.Database == nil {
		return nil
	}
	for i := range s.System.Database.Entities {
		if s.System.Database.Entities[i].Name == name {
			return &s.System.Database.Entities[i]
		}
	}
	return nil
}

// Interface returns the interface with the given name, or nil.
func (s *Spec) Interface(name string) *Interface {
	for i := range s.System.Interfaces {
		if s.System.Interfaces[i].Name == name {
			return &s.System.Interfaces[i]
		}
	}
	return nil
}

// SurfaceRef is a resolved "interface.surface" reference.
type SurfaceRef struct {
	Interface *Interface
	Surface   *Surface
}

// ID is the canonical "interface.surface" form.
func (r SurfaceRef) ID() string { return r.Interface.Name + "." + r.Surface.Name }

// ResolveSurfaces expands a module's Surfaces list. A bare interface name
// expands to all of its surfaces. Unknown references are returned as errors
// so the validator can report them by position.
func (s *Spec) ResolveSurfaces(refs []string) ([]SurfaceRef, []error) {
	var out []SurfaceRef
	var errs []error
	for _, ref := range refs {
		ifaceName, surfName, hasSurface := strings.Cut(ref, ".")
		iface := s.Interface(ifaceName)
		if iface == nil {
			errs = append(errs, fmt.Errorf("unknown interface %q in %q", ifaceName, ref))
			continue
		}
		if !hasSurface {
			for i := range iface.Surfaces {
				out = append(out, SurfaceRef{Interface: iface, Surface: &iface.Surfaces[i]})
			}
			continue
		}
		found := false
		for i := range iface.Surfaces {
			if iface.Surfaces[i].Name == surfName {
				out = append(out, SurfaceRef{Interface: iface, Surface: &iface.Surfaces[i]})
				found = true
				break
			}
		}
		if !found {
			errs = append(errs, fmt.Errorf("unknown surface %q in interface %q", surfName, ifaceName))
		}
	}
	return out, errs
}

// AllowedImports lists the import-path prefixes generated code may use beyond
// the standard library: the system's own module, every framework and ORM
// module, and the explicit allowlist.
func (s *Spec) AllowedImports() []string {
	out := []string{s.System.ModulePath}
	if ls := s.LanguageStack(); ls != nil {
		for _, f := range ls.Frameworks {
			if f.Module != "" {
				out = append(out, f.Module)
			}
		}
		if ls.ORM != nil && ls.ORM.Module != "" {
			out = append(out, ls.ORM.Module)
		}
		out = append(out, ls.AllowedModules...)
	}
	return out
}
