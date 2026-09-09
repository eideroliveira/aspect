# Aspect specification format, version 1

A spec is one YAML file. Unknown fields are errors, so typos are caught early.

```yaml
aspect: 1                      # format version; required

system:
  name: inventory              # [a-z][a-z0-9_]*; also the output directory
  intent: >                    # why the system exists, in the owner's words
    Keep an accurate count of physical stock per SKU ...
  language: go                 # default go; the only value supported today
  module_path: example.com/inventory   # Go module path of the generated code
  goals:                       # at least one
    - id: G1
      statement: Stock levels never go negative.
      verify: invariant        # test (default) | invariant | review
  constraints:                 # optional, apply to every module
    - Standard library only.

modules:                       # at least one; order in the file is irrelevant
  - name: stock
    intent: >                  # what this module is for, not how it works
      Answer "how many of X can we promise right now?"
    goals: [G1]                # goals this module is accountable for
    depends_on: [ledger]       # other modules; must be acyclic
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

Warnings:

- A module owns no goal (the Validator will only judge its intent).
- A module has neither scenarios nor invariants (tests are inferred from the
  interface alone).

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
