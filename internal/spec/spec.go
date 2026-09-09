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
	Intent      string   `yaml:"intent"`
	Language    string   `yaml:"language"`
	ModulePath  string   `yaml:"module_path"`
	Goals       []Goal   `yaml:"goals"`
	Constraints []string `yaml:"constraints"`
	// Stack holds language-specific configuration keyed by language name
	// ("go", "python", ...). Only the entry for System.Language is used.
	Stack Stack `yaml:"stack"`
	// Database, when present, declares the persistent data model.
	Database *Database `yaml:"database"`
	// Interfaces declare how the system is exposed: HTTP APIs, web UIs,
	// command lines, gRPC services.
	Interfaces []Interface `yaml:"interfaces"`
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
	Version string `yaml:"version"`
	// Frameworks the system is built on, e.g. qor5 for a Go admin UI.
	Frameworks []Framework `yaml:"frameworks"`
	// ORM, when the database is accessed through one.
	ORM *Framework `yaml:"orm"`
	// AllowedModules are import-path prefixes generated code may import in
	// addition to the standard library, the system's own module, and the
	// modules of Frameworks and ORM. The pipeline enforces this list.
	AllowedModules []string `yaml:"allowed_modules"`
	// Guidance is free-text advice for the Coder and Tester: conventions,
	// project layout, idioms to follow or avoid.
	Guidance string `yaml:"guidance"`
}

// Framework is a library the generated code builds on.
type Framework struct {
	Name    string `yaml:"name"`
	Module  string `yaml:"module"`
	Version string `yaml:"version"`
	Purpose string `yaml:"purpose"`
	// Guidance tells agents how this framework is meant to be used here.
	Guidance string `yaml:"guidance"`
}

// Database declares persistence.
type Database struct {
	// Engine is postgres, mysql or sqlite.
	Engine string `yaml:"engine"`
	// Migrations is "auto" (the ORM migrates the schema at startup) or
	// "files" (versioned migration files are generated).
	Migrations string `yaml:"migrations"`
	// Test says which database the generated tests run against.
	Test DBTest `yaml:"test"`
	// Entities are the persistent types.
	Entities []Entity `yaml:"entities"`
}

// DBTest configures the database used by generated tests.
type DBTest struct {
	// Engine used by tests; sqlite means an in-memory database needing no
	// external service. Defaults to sqlite unless Database.Engine is sqlite.
	Engine string `yaml:"engine"`
	// DSNEnv names an environment variable; when it is set at test time the
	// tests connect to that DSN instead of the test engine default.
	DSNEnv string `yaml:"dsn_env"`
}

// Entity is one persistent type (a table, a collection).
type Entity struct {
	Name        string     `yaml:"name"`
	Intent      string     `yaml:"intent"`
	Fields      []Field    `yaml:"fields"`
	Relations   []Relation `yaml:"relations"`
	Constraints []string   `yaml:"constraints"`
}

// Field is one attribute of an entity.
type Field struct {
	Name string `yaml:"name"`
	// Type is expressed in the target language's terms (Go: string, int64,
	// time.Time, decimal). Agents map it to the database column type.
	Type string `yaml:"type"`
	// Key is "primary" for the primary key, empty otherwise.
	Key      string `yaml:"key"`
	Required bool   `yaml:"required"`
	Unique   bool   `yaml:"unique"`
	Default  string `yaml:"default"`
	Intent   string `yaml:"intent"`
}

// Relation links two entities.
type Relation struct {
	// Kind is has_one, has_many, belongs_to or many_to_many.
	Kind   string `yaml:"kind"`
	Entity string `yaml:"entity"`
	// Via names the field or join table carrying the relation.
	Via    string `yaml:"via"`
	Intent string `yaml:"intent"`
}

// Interface is one way the system is exposed to users or other systems.
type Interface struct {
	Name string `yaml:"name"`
	// Kind is http, web, cli or grpc.
	Kind   string `yaml:"kind"`
	Intent string `yaml:"intent"`
	// Framework names an entry in Stack[language].Frameworks used to build
	// this interface (for example "qor5" for a web admin).
	Framework string `yaml:"framework"`
	// Auth describes who may use this interface; surfaces can override it.
	Auth     string    `yaml:"auth"`
	Surfaces []Surface `yaml:"surfaces"`
}

// Surface is one endpoint, page, command or RPC of an interface.
type Surface struct {
	Name   string `yaml:"name"`
	Intent string `yaml:"intent"`
	// Route is the path (http, web) or command name (cli) or RPC name (grpc).
	Route string `yaml:"route"`
	// Method is the HTTP method for http surfaces.
	Method string `yaml:"method"`
	// Entity the surface operates on, when it is a CRUD surface.
	Entity string `yaml:"entity"`
	// Operations for entity-bound surfaces: list, create, read, update, delete
	// or a custom verb.
	Operations []string `yaml:"operations"`
	Request    string   `yaml:"request"`
	Response   string   `yaml:"response"`
	Errors     []string `yaml:"errors"`
	Auth       string   `yaml:"auth"`
}

// Module is a unit of implementation with its own intent. Modules are generated
// in dependency order, each one seeing the interfaces of its dependencies.
type Module struct {
	Name        string      `yaml:"name"`
	Intent      string      `yaml:"intent"`
	Goals       []string    `yaml:"goals"`
	DependsOn   []string    `yaml:"depends_on"`
	Interface   []Operation `yaml:"interface"`
	Invariants  []string    `yaml:"invariants"`
	Scenarios   []Scenario  `yaml:"scenarios"`
	Constraints []string    `yaml:"constraints"`
	// Entities this module owns: it defines the persistent type and is the
	// only module that writes it.
	Entities []string `yaml:"entities"`
	// Surfaces this module implements, as "interface.surface" or "interface"
	// for every surface of that interface.
	Surfaces []string `yaml:"surfaces"`
}

// Operation is one exported function or method the module must expose, with
// optional design-by-contract clauses.
type Operation struct {
	Name      string   `yaml:"name"`
	Signature string   `yaml:"signature"`
	Intent    string   `yaml:"intent"`
	Pre       []string `yaml:"pre"`
	Post      []string `yaml:"post"`
}

// Scenario is a Given/When/Then example. The Tester agent turns each scenario
// into at least one test, and the Validator checks that mapping exists.
type Scenario struct {
	ID    string   `yaml:"id"`
	Given string   `yaml:"given"`
	When  string   `yaml:"when"`
	Then  string   `yaml:"then"`
	Goals []string `yaml:"goals"`
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
