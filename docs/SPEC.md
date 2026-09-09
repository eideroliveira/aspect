# The Aspect specification language

An Aspect spec is a YAML document that states what a system is for, what it
must achieve, and how the pieces are cut, precisely enough that agents can
build it, test it, and judge whether the result honours the intent. This is
the reference for format version 1.

Contents

1. [Concepts](#1-concepts)
2. [Document structure](#2-document-structure)
3. [Notation](#3-notation)
4. [The `system` section](#4-the-system-section)
5. [Goals](#5-goals)
6. [Stack](#6-stack)
7. [Database](#7-database)
8. [Interfaces](#8-interfaces)
9. [Tiers and topology](#9-tiers-and-topology)
10. [Modules](#10-modules)
11. [Source (provenance)](#11-source-provenance)
12. [Validation rules](#12-validation-rules)
13. [How agents read the spec](#13-how-agents-read-the-spec)
14. [Language profiles](#14-language-profiles)
15. [Writing good specs](#15-writing-good-specs)
16. [Complete skeleton](#16-complete-skeleton)

---

## 1. Concepts

**Intent** is the reason something exists, written the way its owner would
explain it. The system has one, every tier has one, every module has one.
Intent is not used to generate code; it is used to judge it. The Validator
agent compares what was built against the intent to detect code that works
but solves a narrower or different problem.

**Goals** are the outcomes the finished system must achieve. Each goal
declares how it can be verified, and every goal must be owned by at least
one module or scenario: an unowned goal could never be judged, so the
validator refuses it. The final report is a verdict per goal.

**Modules** are units of implementation with their own intent. A module
declares the operations it exposes, the invariants that must always hold,
Given/When/Then scenarios that become tests, the entities it owns and the
interface surfaces it implements or consumes.

**Tiers** are separate deployables in their own languages (a backend
service, a mobile app). They talk only through interfaces. A single-tier
spec omits the `tiers` key entirely.

**Everything that looks like code is notation.** Signatures and field types
are language-neutral so the same spec can be built in Go or Swift; the
language profile decides layout and toolchain.

---

## 2. Document structure

```yaml
aspect: 1            # format version, required
system:              # name, intent, goals, and the shared model
  ...
modules:             # single-tier spec: the modules live here
  - ...
# or
tiers:               # multi-tier spec: language, module_path, stack and
  - ...              # modules live under each tier
```

`modules` and `tiers` are exclusive. A spec with `tiers` declares the
system's shape with `system.topology` and puts `language`, `module_path`
and `stack` on each tier; a spec without `tiers` puts them under `system`.

Unknown keys anywhere are errors, so typos are caught at load time.

---

## 3. Notation

### Identifiers

Names of the system, tiers, modules, interfaces and surfaces match
`[a-z][a-z0-9_]*` (snake_case). Entity names match `[A-Za-z][A-Za-z0-9_]*`
(typically PascalCase). Goal ids are free strings, conventionally `G1`,
`G2`; scenario ids are unique per module, conventionally `S1`, `S2`.

### References

| Reference | Form | Example |
|---|---|---|
| goal | goal id | `goals: [G1, G3]` |
| module | module name | `depends_on: [catalog]` |
| tier | tier name, or `external` | `provider: backend`, `tier: external` |
| entity | entity name | `entities: [Product]` |
| surface | `interface.surface`, or `interface` for all of its surfaces | `surfaces: [api.reserve, app]` |

### Signatures

Operation signatures are notation the Coder translates idiomatically to the
target language. Two forms are accepted:

```
Reserve(sku: string, qty: int, order: string) -> available: int | error   # neutral form
func (s *Stock) Reserve(sku string, qty int, order string) error          # a language's syntax
```

Prefer the neutral form in specs that may be retargeted; `aspect import`
writes it.

### Field types

Entity field types use neutral names: `string`, `int`, `int64`, `float`,
`bool`, `time`, `decimal`, `uuid`, `bytes`, or the name of another entity.
The Coder maps them to the language and the database engine.

### Contract clauses

`pre`, `post`, `invariants` and `constraints` are free text, but the more
they read as checkable relations between observable values, the better the
tests. `old(x)` in a post-condition means the value of `x` before the call.

---

## 4. The `system` section

| Key | Type | Required | Meaning |
|---|---|---|---|
| `name` | identifier | yes | System name; also the output directory |
| `intent` | text | yes | Why the system exists, in the owner's words |
| `topology` | `monolith` \| `api_backend` \| `cloud_service` | no | Shape of the solution; derived when omitted (see §9) |
| `language` | `go` \| `swift` | single-tier only | Target language (default `go`) |
| `module_path` | string | single-tier only | Go module path, or a bundle-style id for Swift |
| `goals` | list of [Goal](#5-goals) | yes, at least one | Outcomes to achieve |
| `constraints` | list of text | no | Rules every module obeys |
| `stack` | [Stack](#6-stack) | single-tier only | Language-specific configuration |
| `database` | [Database](#7-database) | no | The system's persistent model |
| `interfaces` | list of [Interface](#8-interfaces) | no | How the system is exposed |
| `source` | [Source](#11-source-provenance) | no | Provenance of an imported spec |

```yaml
system:
  name: inventory
  intent: >
    Keep an accurate count of physical stock per SKU so that staff never
    promise goods that are not on the shelf.
  language: go
  module_path: example.com/inventory
  goals: [...]
  constraints:
    - Standard library only.
```

---

## 5. Goals

| Key | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | yes | Unique id, referenced by modules and scenarios |
| `statement` | text | yes | The outcome, as a product owner would state it |
| `verify` | `test` \| `invariant` \| `review` | no (default `test`) | How the Validator judges it |

The verification method sets the standard of evidence:

| `verify` | Achieved when |
|---|---|
| `test` | a test genuinely exercising the goal exists and the real test run passed |
| `invariant` | a property-style test exists **and** the Validator's reading of the code finds no path (errors, concurrency, overflow) that breaks it |
| `review` | the Validator can cite code that supports the claim; for qualities no test can execute, such as "the API is idiomatic" |

A failed test run caps every `test` and `invariant` goal below `achieved`,
whatever the model would otherwise say. Goals a module never reached a
verdict on are reported as `unverifiable`, never silently omitted.

```yaml
goals:
  - id: G1
    statement: Stock levels never go negative, whatever sequence of operations is applied.
    verify: invariant
  - id: G2
    statement: A reservation holds goods for an order so they cannot be sold twice.
    verify: test
  - id: G3
    statement: The public API is small, idiomatic Go with no global mutable state.
    verify: review
```

---

## 6. Stack

The stack is keyed by language; only the entry matching the tier's language
is used. It tells agents which libraries they may use and how.

```yaml
stack:
  go:
    version: "1.26"
    frameworks:
      - name: qor5
        module: github.com/qor5/admin/v3
        version: v3.0.0
        purpose: Admin UI with CRUD presets over GORM models.
        guidance: |
          Build the admin with presets.New(). Register each entity with
          b.Model(&Entity{}) ...
    orm:
      name: gorm
      module: gorm.io/gorm
    allowed_modules:
      - gorm.io/driver/
      - github.com/go-chi/chi/v5
    guidance: |
      One *gorm.DB is opened in main and injected; no package-level
      database variables.
```

| Key | Type | Meaning |
|---|---|---|
| `version` | string | Toolchain version; Go writes it into `go.mod` |
| `frameworks[]` | list of Framework | Libraries the system is built on; interfaces reference them by `name` |
| `orm` | Framework | The ORM, when the database is accessed through one |
| `allowed_modules` | list of import prefixes | Extra imports permitted beyond the standard library, the tier's own module, and the frameworks' modules |
| `guidance` | text | Conventions for every module in this language |

Framework fields: `name` (required, unique), `module` (required: import
path in Go, package URL in Swift), `version`, `purpose`, `guidance`. A
framework's guidance is shown to the agents working on any surface that
names the framework.

**The allowlist is enforced mechanically.** For Go, generated files are
parsed and any import outside the standard library, the tier's own module
path and the stack is fed back to the Coder as a failed build. Swift relies
on the generated `Package.swift`, which only declares stack packages.

---

## 7. Database

```yaml
database:
  engine: postgres          # postgres | mysql | sqlite
  tier: backend             # multi-tier: owning tier, or external
  migrations: auto          # auto (ORM migrates at startup) | files
  test:
    engine: sqlite          # what generated tests run against; sqlite = in-memory
    dsn_env: APP_TEST_DSN   # when set at test time, tests use this DSN instead
  entities:
    - name: Product
      intent: A sellable item identified by its SKU.
      fields:
        - {name: ID, type: int64, key: primary}
        - {name: SKU, type: string, required: true, unique: true}
        - {name: OnHand, type: int, default: "0", intent: units physically on the shelf}
      relations:
        - {kind: has_many, entity: Movement, via: ProductID}
      constraints:
        - OnHand >= 0 at all times.
```

| Key | Type | Required | Meaning |
|---|---|---|---|
| `engine` | `postgres` \| `mysql` \| `sqlite` | yes | Production engine |
| `tier` | tier name or `external` | multi-tier: yes | Who owns the database. `external` means a hosted service owns it and the entities only document the remote model |
| `migrations` | `auto` \| `files` | no (default `auto`) | Schema management |
| `test.engine` | engine | no (default `sqlite`) | Engine the generated tests use |
| `test.dsn_env` | string | no | Environment variable carrying a DSN that overrides the test engine |
| `entities[]` | list of Entity | yes, at least one | Persistent types |

Entity: `name` (required, unique across every database in the spec),
`intent`, `fields` (at least one), `relations`, `constraints`.

Field: `name`, `type` (required, neutral names), `key: primary`, `required`,
`unique`, `default`, `intent`. An entity without a primary key gets a
surrogate id from the Coder, with a warning.

Relation: `kind` (`has_one`, `has_many`, `belongs_to`, `many_to_many`),
`entity` (must exist in the same database), `via` (the field or join table
carrying it), `intent`.

**Ownership.** Every entity of a generated database is owned by exactly one
module in the database's tier, listed under that module's `entities`. The
owner defines the persistent type, its schema or migrations, and every
write path; other modules read through the owner's interface. Entities of
an external database are owned by nobody.

**Tier-local databases.** A tier may carry its own `database` (an app's
offline cache). Its entities follow the same rules within that tier. Entity
names stay unique across the system database and every tier database.

---

## 8. Interfaces

An interface is one way the system is exposed to people or programs. Its
surfaces are the individual endpoints, pages, commands, RPCs or screens.

```yaml
interfaces:
  - name: api
    kind: http
    intent: What the shop calls.
    provider: backend                 # multi-tier: serving tier, or external
    auth: bearer token from SHOP_API_TOKEN
    surfaces:
      - name: reserve
        method: POST
        route: /api/reservations
        entity: Reservation
        request: '{"sku": string, "qty": int, "order": string}'
        response: '{"available": int}'
        errors: ["404 unknown SKU", "409 when fewer than qty are available"]
  - name: admin
    kind: web
    intent: Back office for warehouse staff.
    provider: backend
    framework: qor5
    surfaces:
      - name: products
        route: /admin/products
        entity: Product
        operations: [list, create, update, delete]
  - name: push
    kind: http
    intent: Notifications.
    provider: external
    service: Firebase Cloud Messaging
    surfaces:
      - {name: send, method: POST, route: /v1/send}
```

| Key | Type | Required | Meaning |
|---|---|---|---|
| `name` | identifier | yes | Unique interface name |
| `kind` | `http` \| `web` \| `cli` \| `grpc` \| `app` | yes | `http`: endpoints called by programs; `web`: server-rendered pages; `cli`: commands; `grpc`: RPC services; `app`: native screens |
| `intent` | text | yes | Who it serves and why |
| `provider` | tier name or `external` | multi-tier: yes | The tier that serves it; `external` for a service outside the spec. Empty in a single-tier spec means the tier itself |
| `service` | string | with `external` | Name of the hosted provider (Supabase, Firebase, an existing API) |
| `framework` | framework name | no | Must be declared in the providing tier's stack |
| `auth` | text | no | Who may use it; surfaces may override |
| `surfaces[]` | list of Surface | yes, at least one | The individual surfaces |

Surface fields:

| Key | Meaning | Required for |
|---|---|---|
| `name` | identifier, unique within the interface | all |
| `intent` | what the surface is for | |
| `route` | path (`http`, `web`), command (`cli`), RPC name (`grpc`), navigation path (`app`) | `http`, `web`, `grpc`, `app` |
| `method` | HTTP method | `http` |
| `entity` | entity the surface operates on, when it is a CRUD surface | |
| `operations` | `list`, `create`, `read`, `update`, `delete` or a custom verb, for entity-bound surfaces | |
| `request`, `response` | shapes, free text | |
| `errors` | error cases with their status or meaning | |
| `auth` | override of the interface's auth | |

**Implementing and consuming.** Modules in the providing tier implement
surfaces by listing them under `surfaces`. Modules in other tiers, and any
module for an external interface, list what they call under `consumes`.
Every provided surface is implemented by exactly one module; external
surfaces are never implemented, only consumed.

---

## 9. Tiers and topology

`system.topology` names the shape of the solution:

| Topology | Shape | Typical use |
|---|---|---|
| `monolith` | one tier owns the database and serves every interface | a web app connecting directly to its database |
| `api_backend` | a backend tier owns the database and serves an API; a frontend tier (web or mobile app) consumes it | a Go service plus an iOS app, or a web frontend over a backend service |
| `cloud_service` | one generated app tier; the database and server interfaces are provided by a hosted service | a mobile app over Supabase, Firebase or an existing hosted API |

When omitted, the topology is derived: several tiers mean `api_backend`,
an external provider or database means `cloud_service`, otherwise
`monolith`. When declared, it is checked against the spec's shape.

### Single-tier specs

`monolith` and `cloud_service` are single-tier: `language`, `module_path`,
`stack` and `modules` sit at the top. A `cloud_service` spec differs only in
that its server-side interfaces and its database carry `provider: external`
and `tier: external`. See
[examples/monolith_minimal](../examples/monolith_minimal/aspect.yaml) and
[examples/cloud_service_minimal](../examples/cloud_service_minimal/aspect.yaml).

### Multi-tier specs

```yaml
system:
  name: shop
  topology: api_backend
  database: {engine: postgres, tier: backend, entities: [...]}
  interfaces:
    - {name: api, kind: http, provider: backend, surfaces: [...]}
    - {name: app, kind: app, provider: mobile, surfaces: [...]}
tiers:
  - name: backend
    intent: Own the data and serve the API.
    language: go
    module_path: example.com/shop
    stack: {go: {...}}
    modules:
      - {name: catalog, entities: [Product]}
      - {name: api, depends_on: [catalog], surfaces: [api]}
  - name: mobile
    intent: The members' iOS app.
    language: swift
    module_path: com.example.shop
    depends_on: [backend]
    database: {engine: sqlite, entities: [{name: CachedProduct, ...}]}
    modules:
      - {name: api_client, consumes: [api]}
      - {name: catalog_feature, depends_on: [api_client], entities: [CachedProduct], surfaces: [app]}
```

| Tier key | Type | Required | Meaning |
|---|---|---|---|
| `name` | identifier | yes | Unique; `external` is reserved |
| `intent` | text | yes | What this deployable is for |
| `language` | `go` \| `swift` | no (default `go`) | Target language of this tier |
| `module_path` | string | yes | Module or package path for this tier |
| `depends_on` | list of tier names | no | Build order: a consumer after its provider |
| `stack` | Stack | no | This tier's language configuration |
| `database` | Database | no | A tier-local database (cache) |
| `modules` | list of Module | yes, at least one | The tier's modules |

Rules that hold across tiers: module names are unique across the whole
spec; `depends_on` between modules never crosses a tier (cross-tier calls go
through `consumes`); tier dependencies are acyclic. Each tier is generated
into its own workspace, `out/<system>/<tier>/`, in dependency order. A
single-tier spec keeps `out/<system>/`.

Full example: [examples/shop_two_tier](../examples/shop_two_tier/aspect.yaml).

---

## 10. Modules

```yaml
modules:
  - name: stock
    intent: >
      Answer "how many of X can we promise right now?" and enforce that no
      movement can take a SKU below zero. Delegates the audit trail to ledger.
    goals: [G1, G3]
    depends_on: [ledger]
    entities: [Product]
    surfaces: [api.reserve, admin]
    consumes: [push.send]
    interface:
      - name: Reserve
        signature: "Reserve(sku: string, qty: int, order: string) -> error"
        intent: Hold goods for an order. Fails if fewer than qty are available.
        pre: ["qty > 0"]
        post: ["Available(sku) == old(Available(sku)) - qty"]
    invariants:
      - Available(sku) == on-hand(sku) - reserved(sku) at all times.
    scenarios:
      - id: S2
        given: Receive("A", 5)
        when: Reserve("A", 6, "order-1")
        then: an error is returned and Available("A") == 5
        goals: [G1]
    constraints:
      - Safe for concurrent use from multiple goroutines.
```

| Key | Type | Required | Meaning |
|---|---|---|---|
| `name` | identifier | yes | Unique across the spec; also the code directory |
| `intent` | text | yes | What the module is for, not how it works |
| `goals` | goal ids | no | Goals this module is accountable for |
| `depends_on` | module names | no | Same-tier modules whose code this one imports; acyclic |
| `entities` | entity names | no | Entities this module owns (defines and writes) |
| `surfaces` | surface refs | no | Surfaces this module implements; only for its own tier's interfaces |
| `consumes` | surface refs | no | Surfaces this module calls in other tiers or external services |
| `interface` | list of Operation | no | Exported operations other modules call |
| `invariants` | list of text | no | Properties that hold in every state |
| `scenarios` | list of Scenario | no | Given/When/Then examples |
| `constraints` | list of text | no | Module-specific rules |

Operation: `name` and `signature` (both required), `intent`, `pre`, `post`.
Signatures are the contract dependents are generated against; keep them
stable.

Scenario: `id` (unique within the module), `given`, `when` and `then`
(required), `goals`. Every scenario becomes at least one test whose name
carries the scenario id, and the Validator checks that the test does what
the scenario says. Scenarios may pin goals directly, which is how a goal
can be owned by a scenario rather than a whole module.

A module with neither scenarios nor invariants is accepted with a warning:
its tests are inferred from the interface alone, which is weak evidence.

---

## 11. Source (provenance)

Written by `aspect import`; informational.

```yaml
source:
  language: go
  repository: git@github.com:org/repo.git
  commit: 2a630a7d
  imported_at: 2026-09-09T20:00:00Z
  frameworks: [chi, gorm, qor5]
```

`frameworks` keeps what the source used even when the target language's
stack cannot carry them, so a retargeted spec remembers where its shape
came from.

---

## 12. Validation rules

`aspect validate` reports every issue in one pass. Errors block `run`;
warnings do not.

### Errors

Structure
- `aspect` is `1`; `system.name` and `system.intent` are set; at least one goal.
- `modules` and `tiers` are exclusive. Without tiers, `system.language` is supported and `system.module_path` is set, and there is at least one module.
- Every stack framework has `name` (unique) and `module`; the ORM has `module`; `allowed_modules` entries are non-empty.

Identifiers and references
- Names match their pattern (§3); goal ids, entity names (across all databases), interface names, tier names, and module names (across all tiers) are unique; scenario ids are unique per module; surface names are unique per interface.
- Every goal, module, tier, entity and surface reference resolves.

Goals
- Every goal is owned by at least one module or scenario.

Database
- `engine`, `migrations`, `test.engine` and relation kinds use the listed values; every entity has fields; field `key` is empty or `primary`; relation targets exist in the same database.
- Multi-tier: `system.database.tier` is a tier name or `external`. Single-tier: it may only be empty or `external`.
- Every entity of a generated database is owned by exactly one module, in the database's tier. Entities of an external database are owned by nobody.

Interfaces
- `kind` is one of the listed kinds; `http` surfaces have `route` and `method`; `web`, `grpc` and `app` surfaces have `route`; every interface has at least one surface; surface entities exist; `framework` is declared in the providing tier's stack.
- Multi-tier: `provider` is a tier name or `external`. Single-tier: it may only be empty or `external`.
- Every provided surface is implemented by exactly one module of the providing tier. External surfaces are never implemented.
- `consumes` never names a surface of the module's own tier.

Tiers
- Every tier has `name`, `intent`, a supported `language`, `module_path` and modules; `external` is not a tier name; tier dependencies resolve and are acyclic.
- Module `depends_on` resolves, is not the module itself, stays inside the tier, and is acyclic.

Topology
- A declared `api_backend` has at least two tiers and at least one module consuming a surface provided by another tier.
- A declared `cloud_service` has an external interface or an external database.

### Warnings

- A module owns no goal (the Validator will only judge its intent).
- A module has neither scenarios nor invariants.
- A stack entry for a language other than the tier's.
- An entity without an intent or without a primary key.
- A non-sqlite test engine without `dsn_env`.
- An external interface without a `service` name; an external surface nobody consumes.
- `system.language`, `module_path` or `stack` set alongside `tiers`; a `tier` field on a tier-local database; a single tier written under `tiers`.
- `operations` listed on a surface with no `entity`.
- A declared `monolith` with several tiers or with external providers.

---

## 13. How agents read the spec

| Section | Coder | Tester | Validator |
|---|---|---|---|
| system intent, goals, constraints | context | context | the standard to judge against |
| tiers (outline) | knows which tier it is in and who provides what | same | same |
| stack (own tier) | which libraries to use and how; enforced by the import guard | same | checks frameworks are used as their guidance says |
| database (outline) + owned entities (full) | defines types, schema, write paths | tests persistence against the test database | checks fields, keys, relations, and that no write path exists elsewhere |
| interfaces (outline) + implemented surfaces (full) | wires routes, pages, commands, screens exactly as specified | exercises them as a client would | checks routes, methods, shapes, errors, auth |
| consumed surfaces (full) | implements a client against the contract, base address from configuration | stubs the provider | checks the client honours every listed error |
| module interface, pre/post | implements the operations | derives tests from the clauses | cites the code that makes them true |
| invariants | must hold on every path | property-style tests | reads paths tests cannot reach |
| scenarios | happy and error paths to support | one test per scenario, named after its id | confirms each named test does what the scenario says |

The Validator is a different agent from the Coder and Tester, sees the real
test run, and must cite files and identifiers as evidence. Goals it forgets
become `unverifiable`; a goal owned by several modules rolls up to its
weakest verdict.

---

## 14. Language profiles

The spec is language-agnostic; a profile decides the layout and toolchain
of each tier.

| Language | Layout | Manifest | Build and test |
|---|---|---|---|
| `go` | `<module>/` as package `<module>`; tests `*_test.go` beside the code | `go.mod` written once (version from the stack) | `go mod tidy`, `go vet`, `go test -race` |
| `swift` | `Sources/<Module>/` and `Tests/<Module>Tests/` (PascalCase of the module name) | `Package.swift` regenerated after every module; platforms iOS 17 and macOS 14 so tests run on the host; stack frameworks become package dependencies | `swift build`, `swift test --filter <Module>Tests` |

---

## 15. Writing good specs

- **Intent is for judgement, not generation.** Write it the way you would
  explain the module to a new colleague. Vague intent yields vague verdicts.
- **Scenarios are the tests.** Concrete values beat prose: `Reserve("A", 6)`
  after `Receive("A", 5)` is testable; "reserving too much fails" is not.
- **Invariants catch what scenarios miss.** State them as relations between
  observable values so a property test can check them after every step.
- **Use `review` sparingly.** It is for qualities no test can execute.
- **Signatures are the contract between modules.** Dependents are generated
  against them; keep them stable and neutral.
- **Give every entity and surface one owner.** The validator insists, and
  it is what makes drift detectable: a module touching an entity it does
  not own contradicts what it was shown.
- **Split tiers where the deployment splits.** A mobile app cannot open the
  database; make the API a provided interface and let the app consume it.
- **Put framework knowledge in `guidance`.** It is shown to every agent that
  touches a surface using the framework; it is the cheapest way to get
  idiomatic code.

---

## 16. Complete skeleton

Every key of the format in one place. Optional keys are marked `#opt`.

```yaml
aspect: 1
system:
  name: identifier
  intent: text
  topology: monolith | api_backend | cloud_service      #opt
  language: go | swift                                  # single-tier
  module_path: string                                   # single-tier
  goals:
    - {id: string, statement: text, verify: test | invariant | review}
  constraints: [text]                                   #opt
  stack:                                                #opt, single-tier
    <language>:
      version: string                                   #opt
      frameworks:                                       #opt
        - {name: string, module: string, version: string, purpose: text, guidance: text}
      orm: {name: string, module: string}               #opt
      allowed_modules: [import-prefix]                  #opt
      guidance: text                                    #opt
  database:                                             #opt
    engine: postgres | mysql | sqlite
    tier: tier-name | external                          # multi-tier
    migrations: auto | files                            #opt
    test: {engine: engine, dsn_env: string}             #opt
    entities:
      - name: Entity
        intent: text                                    #opt
        fields:
          - {name: string, type: neutral-type, key: primary, required: bool, unique: bool, default: string, intent: text}
        relations:                                      #opt
          - {kind: has_one | has_many | belongs_to | many_to_many, entity: Entity, via: string, intent: text}
        constraints: [text]                             #opt
  interfaces:                                           #opt
    - name: identifier
      kind: http | web | cli | grpc | app
      intent: text
      provider: tier-name | external                    # multi-tier, or external
      service: string                                   # with external
      framework: framework-name                         #opt
      auth: text                                        #opt
      surfaces:
        - name: identifier
          intent: text                                  #opt
          route: string
          method: GET | POST | ...                      # http
          entity: Entity                                #opt
          operations: [list, create, read, update, delete, ...]   #opt
          request: text                                 #opt
          response: text                                #opt
          errors: [text]                                #opt
          auth: text                                    #opt
  source:                                               #opt, written by import
    {language: string, repository: string, commit: string, imported_at: string, frameworks: [string]}

modules:                                                # single-tier
  - &module
    name: identifier
    intent: text
    goals: [goal-id]                                    #opt
    depends_on: [module-name]                           #opt, same tier
    entities: [Entity]                                  #opt
    surfaces: [interface.surface | interface]           #opt, own tier
    consumes: [interface.surface | interface]           #opt, other tiers / external
    interface:                                          #opt
      - {name: string, signature: string, intent: text, pre: [text], post: [text]}
    invariants: [text]                                  #opt
    scenarios:                                          #opt
      - {id: string, given: text, when: text, then: text, goals: [goal-id]}
    constraints: [text]                                 #opt

tiers:                                                  # multi-tier, instead of modules
  - name: identifier
    intent: text
    language: go | swift
    module_path: string
    depends_on: [tier-name]                             #opt
    stack: {<language>: {...}}                          #opt
    database: {...}                                     #opt, tier-local
    modules: [*module]
```
