# Architecture

## Why separate agents

One model asked to "write this module and its tests" grades its own homework:
the tests it writes mirror the code it wrote, and passing them proves little.
Aspect splits the work by *what each agent is allowed to know and do*:

- The **Coder** sees the spec and its dependencies' source. It cannot write
  tests.
- The **Tester** sees the spec and the implementation, but is instructed to
  derive tests from scenarios and invariants and to let the spec win when the
  two disagree. It cannot change implementation files.
- The **Validator** sees everything, including the real test run and the
  Tester's coverage claims, and is told it did not write any of it. Its output
  is a verdict per goal with evidence that must name files and identifiers.

The pipeline enforces these boundaries mechanically, not just by prompt: each
agent's file list is filtered to its module directory and file kind before
anything reaches disk (`agents.keepModuleFiles`), and the workspace refuses
paths that escape the root or touch `go.mod`.

## Data flow

```
spec.Load ─▶ spec.Validate ─▶ plan.Build ─▶ pipeline.Run
                                              │
                        for each module in dependency order:
                                              │
        ┌──── Coder.Generate(spec, deps) ──────┤
        │            │                        │
        │     ws.WriteFiles                   │
        │            │                        │
        │     Tester.Generate(spec, code) ────┤
        │            │                        │
        │     ws.WriteFiles                   │
        │            │                        │
        │  ┌──▶ ws.Test ── fail ──▶ Coder.Generate(feedback) ─┐
        │  │         │                                        │
        │  └─────────┼────────────────────────────────────────┘  (≤ max-repairs)
        │          pass
        │            │
        │     Validator.Judge(spec, code, tests, result, coverage)
        │            │
        └──── ModuleReport
                                              │
                                        Report.Write ─▶ report.json, REPORT.md
```

Modules are generated in topological order so each Coder call sees the real
source of its dependencies. Ties are broken alphabetically to keep runs
comparable.

## Verification methods

A goal declares how it can be checked, and the Validator applies a different
standard to each:

| `verify` | Achieved when |
|---|---|
| `test` | a test genuinely exercising the goal exists and the real run passed |
| `invariant` | a property-style test exists **and** the Validator's reading of the code finds no path (errors, concurrency, overflow) that breaks it |
| `review` | the Validator can cite code that supports the claim; no test can prove "the API is idiomatic" |

A failed test run caps every `test` and `invariant` goal below `achieved`,
whatever the model would otherwise say; the pipeline states the real outcome
in the Validator's prompt and the report shows the last run.

## Silence never means success

Two rules make the report conservative:

1. `reconcileGoals` guarantees one verdict per accountable goal. A goal the
   Validator forgot becomes `unverifiable` with zero confidence.
2. `summarise` rolls a goal up across modules as its *weakest* verdict, and a
   module that errored out marks every goal it owns `unverifiable`.

## Model usage

- One provider package, `internal/llm`, behind a two-method interface. Agents
  and the pipeline are tested with fakes; the Anthropic client has no tests
  because it would need the network.
- Every call is single-turn and stateless. The system prompt carries a cache
  breakpoint, so the role prompt is read from cache across the run; the
  volatile spec and code come after it.
- Responses are constrained with structured outputs (a JSON schema per agent),
  streamed, and accumulated. `max_tokens` is high because agents emit whole
  files.
- Server-side refusal fallbacks are on by default so a classifier refusal on
  one call is re-served by a fallback model instead of aborting the run.
- Effort defaults to `high`; `xhigh` is worth trying for large modules.

## Language profiles

`internal/lang` is the only place that knows a language's layout, manifest,
toolchain commands and idioms. A profile supplies:

- `CodeDir` / `TestDir` / `IsTestFile`, used to filter agent proposals and to
  collect dependency sources;
- `Init` / `Sync`, which write the manifest (`go.mod` once; `Package.swift`
  regenerated from the spec after every module so SwiftPM sees exactly the
  targets on disk);
- `Steps`, the build-and-test commands the workspace runs;
- `CheckImports`, the mechanical allowlist (Go parses import declarations;
  Swift relies on the manifest, which only declares packages from the stack);
- `CoderRules` / `TesterRules`, appended to the agents' system prompts.

The agents and the pipeline never branch on the language name.

## Stack, database and interfaces in prompts

The system-level context every agent sees carries the stack for the target
language, the database in outline (engine, test strategy, entity names and
intents) and the interfaces in outline. The module section then carries the
full definitions of the entities the module owns and the surfaces it
implements, plus the guidance of the frameworks those surfaces use. Keeping
detail with the owner keeps prompts small and makes drift visible: a module
that touches an entity it does not own is contradicting what it was shown.

## Trust boundaries

Generated code is executed (`go test -race`) on the machine running Aspect.
Treat `aspect run` like running any code you have not read: use a throwaway
directory or container for specs you do not control. The workspace bounds
each run with a timeout but does not sandbox it.
