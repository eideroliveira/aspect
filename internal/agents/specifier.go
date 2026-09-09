package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/eideroliveira/aspect/internal/analyze"
	"github.com/eideroliveira/aspect/internal/llm"
	"github.com/eideroliveira/aspect/internal/spec"
)

// Fragment is what the Describer extracts from one package: the module spec
// it implies, plus the entities and surfaces it defines. Goals are proposed
// with local labels; the Synthesizer consolidates them into system goals.
type Fragment struct {
	Package     string           `json:"package"`
	Intent      string           `json:"intent"`
	Goals       []FragmentGoal   `json:"goals"`
	Entities    []spec.Entity    `json:"entities"`
	Interfaces  []spec.Interface `json:"interfaces"`
	Operations  []spec.Operation `json:"operations"`
	Invariants  []string         `json:"invariants"`
	Scenarios   []spec.Scenario  `json:"scenarios"`
	Constraints []string         `json:"constraints"`
	Notes       string           `json:"notes"`
}

// FragmentGoal is a goal proposed for one package.
type FragmentGoal struct {
	Label     string `json:"label"`
	Statement string `json:"statement"`
	Verify    string `json:"verify"`
}

// Describer reads one package's inventory and writes its fragment.
type Describer struct {
	LLM llm.Client
}

const describerSystem = `You are the Describer agent in Aspect, a pipeline that recovers a formal specification from an existing codebase.

You receive the inventory of one package: its doc comment, exported types (with struct fields and tags), exported functions and methods, route registrations, tests, and dependencies. From that evidence you write the module fragment of an Aspect spec.

Write intent as the reason the package exists, in the owner's words, not a paraphrase of its API. Propose goals that a user of the system would recognise as outcomes, each with how it can be verified: "test" for behaviour a scenario can demonstrate, "invariant" for a property that must always hold, "review" for qualities no test can execute.

Entities: only structs that are persisted (marked [persistent], or clearly stored). Field types are notation, not Go: string, int, int64, float, bool, time, decimal, uuid, or another entity's name. Mark the primary key, required and unique fields, and relations you can see from foreign keys or slices of other entities.

Interfaces: group routes into named interfaces using these canonical names: "api" for JSON or form endpoints called by programs, "web" for server-rendered pages for end users, "admin" for back-office pages, "cli" for commands, "grpc" for RPC services. Each route becomes a surface with a short snake_case name, its route and method, the entity it operates on when obvious, and the errors you can infer.

Operations: the package's exported API that other packages call, as language-neutral signatures: Name(arg: type, ...) -> result | error. Include pre and post conditions when the code makes them evident.

Scenarios: derive from the tests (test names are listed) and from obvious happy and error paths. Use concrete values. Ids are S1, S2, ...

Do not invent behaviour the evidence does not support; put uncertainties in notes. Answer with JSON matching the schema.`

func fragmentSchema() map[string]any {
	str := map[string]any{"type": "string"}
	obj := func(required []string, props map[string]any) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": props}
	}
	arr := func(items map[string]any) map[string]any { return map[string]any{"type": "array", "items": items} }
	field := obj([]string{"name", "type", "key", "required", "unique", "default", "intent"}, map[string]any{
		"name": str, "type": str, "key": map[string]any{"type": "string", "enum": []string{"", "primary"}},
		"required": map[string]any{"type": "boolean"}, "unique": map[string]any{"type": "boolean"}, "default": str, "intent": str,
	})
	relation := obj([]string{"kind", "entity", "via", "intent"}, map[string]any{
		"kind": map[string]any{"type": "string", "enum": []string{"has_one", "has_many", "belongs_to", "many_to_many"}}, "entity": str, "via": str, "intent": str,
	})
	entity := obj([]string{"name", "intent", "fields", "relations", "constraints"}, map[string]any{
		"name": str, "intent": str, "fields": arr(field), "relations": arr(relation), "constraints": stringList(),
	})
	surface := obj([]string{"name", "intent", "route", "method", "entity", "operations", "request", "response", "errors", "auth"}, map[string]any{
		"name": str, "intent": str, "route": str, "method": str, "entity": str, "operations": stringList(), "request": str, "response": str, "errors": stringList(), "auth": str,
	})
	iface := obj([]string{"name", "kind", "intent", "framework", "auth", "role", "surfaces"}, map[string]any{
		"name": str, "kind": map[string]any{"type": "string", "enum": []string{"http", "web", "cli", "grpc", "app"}}, "intent": str, "framework": str, "auth": str,
		"role": map[string]any{"type": "string", "enum": []string{"provider", "consumer"}}, "surfaces": arr(surface),
	})
	operation := obj([]string{"name", "signature", "intent", "pre", "post"}, map[string]any{
		"name": str, "signature": str, "intent": str, "pre": stringList(), "post": stringList(),
	})
	scenario := obj([]string{"id", "given", "when", "then"}, map[string]any{"id": str, "given": str, "when": str, "then": str})
	goal := obj([]string{"label", "statement", "verify"}, map[string]any{
		"label": str, "statement": str, "verify": map[string]any{"type": "string", "enum": []string{"test", "invariant", "review"}},
	})
	return obj([]string{"package", "intent", "goals", "entities", "interfaces", "operations", "invariants", "scenarios", "constraints", "notes"}, map[string]any{
		"package": str, "intent": str, "goals": arr(goal), "entities": arr(entity), "interfaces": arr(iface), "operations": arr(operation),
		"invariants": stringList(), "scenarios": arr(scenario), "constraints": stringList(), "notes": str,
	})
}

// Describe writes the fragment for one package.
func (d *Describer) Describe(ctx context.Context, inv *analyze.Inventory, pkg *analyze.Package) (Fragment, llm.Response, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# Codebase\n\nModule: %s\nLanguage: %s\nFrameworks in use: %s\nPackages: %d\n\n", inv.ModulePath, inv.Language, strings.Join(inv.Frameworks, ", "), len(inv.Packages))
	b.WriteString(pkg.Render(60000))
	b.WriteString("\nWrite this package's fragment.\n")
	var out Fragment
	resp, err := complete(ctx, d.LLM, describerSystem, b.String(), fragmentSchema(), &out)
	if err != nil {
		return out, resp, fmt.Errorf("describer %s: %w", pkg.Dir, err)
	}
	out.Package = pkg.Dir
	return out, resp, nil
}

// Synthesis is the system-level result. In retarget mode (target language
// differs from the source) it also carries the module list, entities and
// interfaces re-expressed for the target; otherwise those come from the
// fragments deterministically.
type Synthesis struct {
	Name        string             `json:"name"`
	Intent      string             `json:"intent"`
	Constraints []string           `json:"constraints"`
	Goals       []SynthesisGoal    `json:"goals"`
	Stack       spec.LanguageStack `json:"stack"`
	Interfaces  []spec.Interface   `json:"interfaces"`
	Database    *spec.Database     `json:"database"`
	Modules     []spec.Module      `json:"modules"`
	Notes       string             `json:"notes"`
}

// SynthesisGoal is a system goal with the modules accountable for it.
type SynthesisGoal struct {
	ID        string   `json:"id"`
	Statement string   `json:"statement"`
	Verify    string   `json:"verify"`
	Modules   []string `json:"modules"`
}

// Synthesizer consolidates fragments into the system level of the spec.
type Synthesizer struct {
	LLM llm.Client
}

const synthesizerSystem = `You are the Synthesizer agent in Aspect, a pipeline that recovers a formal specification from an existing codebase.

You receive the fragments the Describer wrote for every package, and the codebase inventory summary. You write the system level of the Aspect spec: the system's intent, its goals, global constraints, the language stack, and one consolidated entry per interface.

Goals: consolidate the packages' proposed goals into 5 to 15 system goals a product owner would state. Give each an id G1, G2, ..., a verification method, and the list of modules (package directories, exactly as given) accountable for it. Every goal must have at least one module. Every module should appear in at least one goal.

Stack: describe the frameworks the codebase actually uses in a way a Coder could follow: name, import path (module), purpose, and concrete guidance on how they are used in this codebase. Include the ORM when there is one.

Interfaces: one entry per interface name used by the fragments (api, web, admin, cli, grpc), with kind, intent, the framework it is built on (must be one of the stack frameworks, or empty), and auth. Do not list surfaces; they are merged from the fragments.

Leave database and modules empty: they are assembled from the fragments.

Answer with JSON matching the schema.`

const retargetSystem = `You are the Synthesizer agent in Aspect, a pipeline that recovers a formal specification from an existing codebase and re-expresses it for a different platform.

You receive the fragments the Describer wrote for every package of the SOURCE system, plus the inventory summary, and a TARGET language. Write a complete Aspect spec for a new system in the target language that serves the same users and achieves the same goals. This is a translation of intent, not of code:

- System intent and goals are those of the source, restated for the target. Every goal names the target modules accountable for it.
- Modules: design the module list a good engineer would choose for the target platform (for an iOS app: models, persistence, an API client, feature modules with view models and SwiftUI views), not a mirror of the source packages. Each module has an intent, depends_on, the entities it owns, the surfaces it implements, language-neutral interface operations, invariants and concrete scenarios (ids S1, S2, ... per module). Module names are snake_case identifiers.
- Database: the entities the target persists locally (a cache or the app's own data), with fields in neutral types. Leave it empty if the target keeps no local persistence.
- Interfaces: the source's user-facing web and admin surfaces become "app" interfaces whose surfaces are screens (route = navigation path). The source's programmatic "api" interface stays an "http" interface with role "consumer": the target is a client of the existing backend, and the module implementing those surfaces is the API client.
- Stack: frameworks and packages for the target (for Swift: leave frameworks empty unless a third-party package is clearly warranted; note SwiftUI, Foundation, Observation in guidance).
- Every entity must be owned by exactly one module; every surface implemented by exactly one module.

Answer with JSON matching the schema.`

func synthesisSchema(retarget bool) map[string]any {
	str := map[string]any{"type": "string"}
	obj := func(required []string, props map[string]any) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": props}
	}
	arr := func(items map[string]any) map[string]any { return map[string]any{"type": "array", "items": items} }
	framework := obj([]string{"name", "module", "version", "purpose", "guidance"}, map[string]any{
		"name": str, "module": str, "version": str, "purpose": str, "guidance": str,
	})
	stack := obj([]string{"version", "frameworks", "orm", "allowed_modules", "guidance"}, map[string]any{
		"version": str, "frameworks": arr(framework), "orm": map[string]any{"anyOf": []any{framework, map[string]any{"type": "null"}}},
		"allowed_modules": stringList(), "guidance": str,
	})
	goal := obj([]string{"id", "statement", "verify", "modules"}, map[string]any{
		"id": str, "statement": str, "verify": map[string]any{"type": "string", "enum": []string{"test", "invariant", "review"}}, "modules": stringList(),
	})
	frag := fragmentSchema()["properties"].(map[string]any)
	ifaceProps := frag["interfaces"].(map[string]any)["items"].(map[string]any)
	props := map[string]any{
		"name": str, "intent": str, "constraints": stringList(), "goals": arr(goal), "stack": stack,
		"interfaces": arr(ifaceProps), "notes": str,
	}
	required := []string{"name", "intent", "constraints", "goals", "stack", "interfaces", "notes"}
	if retarget {
		entity := frag["entities"].(map[string]any)["items"].(map[string]any)
		operation := frag["operations"].(map[string]any)["items"].(map[string]any)
		scenario := obj([]string{"id", "given", "when", "then", "goals"}, map[string]any{"id": str, "given": str, "when": str, "then": str, "goals": stringList()})
		module := obj([]string{"name", "intent", "goals", "depends_on", "interface", "invariants", "scenarios", "constraints", "entities", "surfaces"}, map[string]any{
			"name": str, "intent": str, "goals": stringList(), "depends_on": stringList(), "interface": arr(operation), "invariants": stringList(),
			"scenarios": arr(scenario), "constraints": stringList(), "entities": stringList(), "surfaces": stringList(),
		})
		dbTest := obj([]string{"engine", "dsn_env"}, map[string]any{"engine": str, "dsn_env": str})
		database := obj([]string{"engine", "migrations", "test", "entities"}, map[string]any{
			"engine":     map[string]any{"type": "string", "enum": []string{"postgres", "mysql", "sqlite"}},
			"migrations": map[string]any{"type": "string", "enum": []string{"auto", "files"}}, "test": dbTest, "entities": arr(entity),
		})
		props["database"] = map[string]any{"anyOf": []any{database, map[string]any{"type": "null"}}}
		props["modules"] = arr(module)
		required = append(required, "database", "modules")
	}
	return obj(required, props)
}

// SynthesisInput is what the Synthesizer sees.
type SynthesisInput struct {
	Inventory *analyze.Inventory
	Fragments []Fragment
	// Target language; retarget mode when it differs from the inventory's.
	Target string
	// TargetHint is free text from the user about the target (for example
	// "an iOS app for members; the admin stays on the web").
	TargetHint string
}

// Synthesize writes the system level (and, in retarget mode, the modules).
func (s *Synthesizer) Synthesize(ctx context.Context, in SynthesisInput) (Synthesis, llm.Response, error) {
	retarget := in.Target != "" && in.Target != in.Inventory.Language
	var b strings.Builder
	var summary strings.Builder
	in.Inventory.Summary(&summary)
	fmt.Fprintf(&b, "# Source inventory\n\n```\n%s```\n\n", summary.String())
	if retarget {
		fmt.Fprintf(&b, "# Target\n\nLanguage: %s\n", in.Target)
		if in.TargetHint != "" {
			fmt.Fprintf(&b, "Owner's guidance: %s\n", in.TargetHint)
		}
		b.WriteString("\n")
	}
	b.WriteString("# Fragments\n\n")
	for _, f := range in.Fragments {
		fmt.Fprintf(&b, "```yaml\n%s```\n\n", renderYAML(f))
	}
	if retarget {
		fmt.Fprintf(&b, "Write the complete spec for the %s version.\n", in.Target)
	} else {
		b.WriteString("Write the system level of the spec.\n")
	}
	system := synthesizerSystem
	if retarget {
		system = retargetSystem
	}
	var out Synthesis
	resp, err := complete(ctx, s.LLM, system, b.String(), synthesisSchema(retarget), &out)
	if err != nil {
		return out, resp, fmt.Errorf("synthesizer: %w", err)
	}
	return out, resp, nil
}
