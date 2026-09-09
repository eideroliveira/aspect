# Aspect

**Aspect** is a system of agents that builds software from a formal
specification and then checks whether what it built achieves the goals the
specification states.

Most code-generation tools stop at "it compiles and the tests pass". Aspect
treats that as the midpoint. The specification declares the *intent* of the
system and of every module, plus the *goals* the finished system must reach.
Separate agents write the code, write the tests from the spec (not from the
code), run them for real, and finally judge, goal by goal and with cited
evidence, whether the implementation honours the intent.

```
spec.yaml ──▶ Planner ──▶ ┌─────────┐   ┌─────────┐   ┌───────────┐
                          │  Coder  │──▶│ Tester  │──▶│ go test   │──┐
                          └─────────┘   └─────────┘   └───────────┘  │ fail: repair
                               ▲                                     │
                               └─────────────────────────────────────┘
                                                        pass │
                                                             ▼
                                                      ┌───────────┐
                                                      │ Validator │──▶ REPORT.md
                                                      └───────────┘
```

## What "formal specification" means here

A spec is a YAML document with three layers:

| Layer | Declares | Consumed by |
|---|---|---|
| **System** | name, intent, goals (each with a verification method), global constraints | every agent |
| **Module** | intent, owned goals, dependencies, interface (signatures with pre/post conditions), invariants, Given/When/Then scenarios | Coder, Tester, Validator |
| **Goal** | one outcome with `verify: test`, `invariant` or `review` | Validator's verdict |

Goals are the contract. Every goal must be owned by at least one module or
scenario, or validation refuses the spec: an unowned goal could never be
judged. See [docs/SPEC.md](docs/SPEC.md) for the full format and
[examples/inventory/aspect.yaml](examples/inventory/aspect.yaml) for a
complete spec.

## The agents

| Agent | Input | Output | Cannot |
|---|---|---|---|
| **Planner** | validated spec | deterministic build order (topological sort by dependency) | call a model; it is plain Go |
| **Coder** | system + module spec, source of dependencies | complete files for the module, notes, concerns | write tests, write outside its module |
| **Tester** | module spec, implementation (for identifiers only) | `_test.go` files, scenario→test mapping | modify the implementation |
| **Validator** | spec, code, tests, the *real* test result, coverage claims | verdict per goal with evidence and confidence, intent alignment | be the agent that wrote the code |

The pipeline (not the agents) writes files and runs tools, so every side
effect passes through one path-guarded boundary. When tests fail, the Coder
receives the failing output and repairs the implementation; the tests stay
fixed because they are the spec's executable form. If the Coder believes a
test contradicts the spec, it says so in `concerns` and the report surfaces
it rather than silently bending the code to the test.

## Install and run

```sh
go install github.com/eideroliveira/aspect/cmd/aspect@latest

aspect validate examples/inventory/aspect.yaml   # every issue in one pass
aspect plan     examples/inventory/aspect.yaml   # build order and per-step work
aspect run      examples/inventory/aspect.yaml -out ./out
```

`run` needs credentials: `ANTHROPIC_API_KEY` or an `ant auth login` profile.
It writes the generated Go module to `out/<system>/`, plus `report.json` and a
human-readable `REPORT.md` with the goal table.

Flags: `-model` (default `claude-opus-5`, or `$ASPECT_MODEL`), `-effort`
(`low`…`max`), `-max-repairs` (default 3), `-fallbacks=false` to disable
server-side refusal fallbacks.

## Status

Early. What exists today:

- Spec format v1, loader, and a validator that reports every problem at once
  (identifiers, references, dependency cycles, goal coverage).
- Deterministic planner.
- Coder, Tester and Validator agents on the Anthropic API with structured
  JSON outputs, prompt caching, streaming, and refusal fallbacks.
- Workspace runner: `go mod tidy`, `go vet`, `go test -race` with a timeout.
- Repair loop and per-module, per-goal report.
- Tests run offline against fake clients, including an end-to-end pipeline
  test that exercises a real repair round through the Go toolchain.

Planned next:

- Cross-module validation pass once all modules exist (system-level goals).
- Spec-drift detection: re-run the Validator on an existing codebase against
  an updated spec.
- More target languages; the spec is language-agnostic, the workspace is not.
- Parallel module generation for independent subgraphs of the plan.

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the design rationale.

## Development

```sh
gofmt -l . && go vet ./... && go test -race ./...
```

No test touches the network. Contributions follow the conventions in
[CLAUDE.md](CLAUDE.md).

## License

MIT. See [LICENSE](LICENSE).
