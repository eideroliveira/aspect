# Aspect specification format, version 1

A spec is one YAML file. Unknown fields are errors, so typos are caught early.

```yaml
aspect: 1                      # format version; required

system:
  name: inventory              # [a-z][a-z0-9_]*; also the output directory
  intent: >                    # why the system exists, in the owner's words
    Keep an accurate count of physical stock per SKU ...
  language: go                 # go (default) or swift
  module_path: example.com/inventory   # Go module path, or a bundle-style id for Swift
  goals:                       # at least one
    - id: G1
      statement: Stock levels never go negative.
      verify: invariant        # test (default) | invariant | review
  constraints:                 # optional, apply to every module
    - Standard library only.

  stack:                       # optional, language-specific configuration
    go:                        # keyed by language; only system.language's entry is used
      version: "1.26"
      frameworks:
        - name: qor5           # referenced by interfaces[].framework
          module: github.com/qor5/admin/v3    # import path (Go) / package URL (Swift)
          version: v3.0.0
          purpose: Admin UI with CRUD presets.
          guidance: |          # how agents must use it here; shown to Coder, Tester, Validator
            Build the admin with presets.New() ...
      orm: {name: gorm, module: gorm.io/gorm}
      allowed_modules:         # extra import prefixes; everything else is rejected
        - gorm.io/driver/
      guidance: Conventions for all modules in this language.

  database:                    # optional persistent data model
    engine: postgres           # postgres | mysql | sqlite
    migrations: auto           # auto (ORM migrates at startup) | files
    test:
      engine: sqlite           # what generated tests run against; sqlite = in-memory
      dsn_env: APP_TEST_DSN    # when set at test time, tests use this DSN instead
    entities:
      - name: Product
        intent: A sellable item identified by its SKU.
        fields:
          - {name: ID, type: uint, key: primary}
          - {name: SKU, type: string, required: true, unique: true}
        relations:
          - {kind: has_many, entity: Movement, via: ProductID}   # has_one | has_many | belongs_to | many_to_many
        constraints:
          - OnHand >= 0 at all times.

  interfaces:                  # optional; how the system is exposed
    - name: admin
      kind: web                # http | web | cli | grpc
      intent: Back office for staff.
      framework: qor5          # must exist in stack.<language>.frameworks
      auth: staff session
      surfaces:
        - name: products
          route: /admin/products
          entity: Product
          operations: [list, create, update, delete]
    - name: api
      kind: http
      intent: What the shop calls.
      surfaces:
        - name: reserve
          method: POST         # required for http
          route: /api/reservations
          request: '{"sku": string, "qty": int}'
          response: '{"available": int}'
          errors: ["409 when fewer than qty are available"]

modules:                       # at least one; order in the file is irrelevant
  - name: stock
    intent: >                  # what this module is for, not how it works
      Answer "how many of X can we promise right now?"
    goals: [G1]                # goals this module is accountable for
    depends_on: [ledger]       # other modules; must be acyclic
    entities: [Product]        # entities this module owns (defines and writes)
    surfaces: [admin, api.reserve]   # surfaces it implements; a bare interface name means all
    interface:                 # exported operations the Coder must provide
      - name: Reserve
        signature: "func (s *Stock) Reserve(sku string, qty int, order string) error"
        intent: Hold goods for an order.
        pre:  ["qty > 0"]      # design-by-contract clauses, free text
        post: ["s.Available(sku) == old(s.Available(sku)) - qty"]
    invariants:                # properties that hold in every state
      - Available(sku) == on-hand(sku) - reserved(sku) at all times.
    scenarios:                 # Given/When/Then examples; each becomes a test
      - id: S2
        given: Receive("A", 5)
        when: Reserve("A", 6, "order-1")
        then: an error is returned and Available("A") == 5
        goals: [G1]            # a scenario can pin goals directly
    constraints: []            # module-specific constraints
```

## Validation rules

`aspect validate` reports every issue in one pass. Errors block `run`;
warnings do not.

Errors:

- `aspect` must be `1`.
- `system.name` and every `modules[].name` match `[a-z][a-z0-9_]*` and are unique.
- `system.intent`, `system.module_path`, every `intent` and every goal
  `statement` are non-empty.
- `verify` is one of `test`, `invariant`, `review`.
- Goal ids are unique; every goal reference (module or scenario) exists.
- Every goal is owned by at least one module or scenario.
- `depends_on` names existing modules, never the module itself, and the graph
  is acyclic (the cycle is printed).
- Scenario ids are unique within a module; each has `when` and `then`.
- Interface entries have `name` and `signature`.
- Every stack framework has `name` and `module`; framework names are unique.
- `database.engine`, `migrations`, `test.engine` and relation kinds use the listed values; entity and field names are unique; relation targets exist.
- Interface names and surface names are unique; http surfaces have `route` and `method`; web and grpc surfaces have `route`; `framework` is declared in the stack; surface entities exist.
- Every entity is owned by exactly one module; every surface is implemented by exactly one module.

Warnings:

- A module owns no goal (the Validator will only judge its intent).
- A module has neither scenarios nor invariants (tests are inferred from the
  interface alone).
- A stack entry for a language other than `system.language`.
- An entity without an intent or without a primary key.
- A non-sqlite test engine without `dsn_env`.

## Language profiles

The spec is language-agnostic; a profile decides the layout and toolchain:

| Language | Layout | Manifest | Build and test |
|---|---|---|---|
| `go` | `<module>/` as package `<module>`; tests `*_test.go` beside the code | `go.mod` written once | `go mod tidy`, `go vet`, `go test -race` |
| `swift` | `Sources/<Module>/` and `Tests/<Module>Tests/` (PascalCase) | `Package.swift` regenerated after every module, iOS 17 + macOS 14 platforms | `swift build`, `swift test --filter <Module>Tests` |

The import allowlist (standard library + own module + stack modules) is
enforced by parsing imports for Go. Swift relies on the manifest: a package
not declared in the stack cannot be resolved.

## Writing good specs

- **Intent is for judgement, not generation.** Write the intent the way you
  would explain the module to a new colleague; the Validator uses it to detect
  code that works but solves the wrong problem.
- **Scenarios are the tests.** Concrete values beat prose: `Reserve("A", 6)`
  after `Receive("A", 5)` is testable; "reserving too much fails" is not.
- **Invariants catch what scenarios miss.** State them as relations between
  observable values so a property test can check them after every operation.
- **Use `review` sparingly.** It is for qualities no test can execute.
- **Signatures are the contract between modules.** A dependent module is
  generated against them, so keep them stable.
