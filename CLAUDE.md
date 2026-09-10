# Aspect

Agents that turn a formal specification into code, tests, and a verdict on
whether the result achieves the spec's stated goals. Go module
`github.com/eideroliveira/aspect`.

## Layout

- `internal/spec` — spec format (YAML), loader (includes via `file`/`dir` on the node tree, briefs, dependencies), semantic validator. Pure Go, no LLM. Single-tier specs are an implicit tier (`EffectiveTiers`); never branch on the spec's shape elsewhere.
- `internal/plan` — deterministic build order from the module dependency graph.
- `internal/lang` — language profiles (go, swift): layout, manifest, toolchain steps, import guard, prompt rules. Agents and pipeline never branch on language.
- `internal/llm` — the only package that calls the Anthropic API. Agents use the `llm.Client` interface.
- `internal/agents` — Coder, Tester, Validator (build) and Describer, Synthesizer (import). Pure functions of their input plus a client; never touch disk.
- `internal/analyze` — deterministic Go inventory (parser only, no type checking).
- `internal/importer` — import orchestration, fragment cache, deterministic assembly, YAML writer.
- `internal/drift` — re-validate existing code against a spec: presence, orphans, tests, verdicts vs baseline.
- `internal/workspace` — writes proposed files (path-guarded) and runs the profile's toolchain steps.
- `internal/pipeline` — orchestration and the report.
- `cmd/aspect` — CLI: `validate`, `expand`, `plan`, `run`, `drift`, `inventory`, `import`.
- `examples/` — reference specs; CI validates them.
- `docs/` — architecture and spec format.

## Rules

- Write all engineering text in English.
- Agents propose, the workspace applies. Do not add filesystem or exec calls to `internal/agents`.
- The Validator must never share a prompt or output with the Coder or Tester; separation is the point.
- Every agent output is structured JSON with a schema in the agent's file; keep schemas `additionalProperties: false`.
- Tests must run without an API key. Use the fake clients in `*_test.go`; never call the network in tests.
- Stage files explicitly (`git add <path>`), never `git add .` or `-A`.
- Never squash-merge PRs.
- Every yaml tag on spec structs has a matching json tag; the Synthesizer's output is decoded with encoding/json and snake_case keys would otherwise be dropped silently.

## Verify

```
gofmt -l . && go vet ./... && go test -race ./...
go run ./cmd/aspect validate examples/inventory/aspect.yaml
go run ./cmd/aspect plan examples/inventory/aspect.yaml
```
