// Package spec defines the Aspect specification format: a formal, machine-checkable
// description of a system, its modules, their intents, and the goals that decide
// whether the generated implementation is acceptable.
//
// The spec is the single source of truth for every agent in the pipeline. Agents
// never invent requirements; they read them from here.
package spec

import (
	"fmt"
	"os"

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
}

// Goal is an outcome the finished system must achieve. Every goal is owned by at
// least one module, and the Validator issues a verdict per goal.
type Goal struct {
	ID        string       `yaml:"id"`
	Statement string       `yaml:"statement"`
	Verify    VerifyMethod `yaml:"verify"`
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
	dec := yaml.NewDecoder(bytesReader(data))
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
