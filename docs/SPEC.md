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
      kind: web                # http | web | cli | grpc | app (native screens)
      role: provider           # provider (default) | consumer: this system is a client of it
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

  source:                      # written by `aspect import`; provenance only
    language: go
    repository: git@github.com:org/repo.git
    commit: 2a630a7d
    imported_at: 2026-09-09T20:00:00Z
    frameworks: [chi, gorm, qor5]

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

## Tiers and topology

A system can be split into **tiers**: separate deployables in their own
languages that talk only through interfaces. `system.topology` names the
shape:

| Topology | Shape | Typical use |
|---|---|---|
| `monolith` | one tier owns the database and serves every interface | a web app connecting directly to its database |
| `api_backend` | a backend tier owns the database and serves an API; a frontend tier (web or mobile app) consumes it | a Go service plus an iOS app, or a web frontend over a backend service |
| `cloud_service` | one generated app tier; the database and server interfaces are provided by a hosted service (`provider: external`) | a mobile app over Supabase, Firebase or an existing hosted API |

When `tiers` is present, `language`, `module_path`, `stack` and `modules`
move under each tier, and `system.database.tier` names the owner of the
system database (or `external`). A tier may also carry its own `database`
for local persistence, such as an app cache. Interfaces name their
`provider` tier (or `external`, with a `service` name). Modules in the
providing tier implement surfaces under `surfaces`; modules in other tiers
list the surfaces they call under `consumes`. Module dependencies
(`depends_on`) stay inside a tier.

```yaml
system:
  name: shop
  topology: api_backend
  database: {engine: postgres, tier: backend, entities: [...]}
  interfaces:
    - {name: api, kind: http, provider: backend, surfaces: [...]}
    - {name: app, kind: app, provider: mobile, surfaces: [...]}
    - {name: push, kind: http, provider: external, service: Firebase Cloud Messaging, surfaces: [...]}
tiers:
  - name: backend
    language: go
    module_path: example.com/shop
    modules:
      - {name: catalog, entities: [Product]}
      - {name: api, depends_on: [catalog], surfaces: [api]}
  - name: mobile
    language: swift
    module_path: com.example.shop
    depends_on: [backend]
    database: {engine: sqlite, entities: [{name: CachedProduct, ...}]}
    modules:
      - {name: api_client, consumes: [api, push.send]}
      - {name: catalog_feature, depends_on: [api_client], entities: [CachedProduct], surfaces: [app]}
```

Each tier is generated into its own workspace (`out/<system>/<tier>/`) in
tier dependency order. A single-tier spec is an implicit tier and keeps the
`out/<system>/` layout. See
[examples/shop_two_tier/aspect.yaml](../examples/shop_two_tier/aspect.yaml).

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
- Every entity of a generated database is owned by exactly one module in the database's tier; entities of an external database are owned by nobody.
- Every provided surface is implemented by exactly one module in the providing tier; external surfaces are consumed, never implemented.
- `consumes` names surfaces of other tiers or external services, never of the module's own tier; `depends_on` never crosses tiers.
- Tier names are unique, tier dependencies are acyclic, every tier has modules; `system.database.tier` and `interfaces[].provider` are required in a multi-tier spec.
- A declared `api_backend` topology has at least two tiers and a cross-tier consumer; a declared `cloud_service` has an external provider.

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

## Language-neutral notation

A spec can be built for any supported language, so the parts that look like
code are notation. Interface signatures may be written in the neutral form
`Reserve(sku: string, qty: int, order: string) -> available: int | error`
or in a language's syntax; the Coder translates them idiomatically. Entity
field types use neutral names (`string`, `int`, `int64`, `float`, `bool`,
`time`, `decimal`, `uuid`, or another entity's name). `aspect import`
writes specs in this notation so they can be retargeted.

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
