package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/eideroliveira/aspect/internal/llm"
	"github.com/eideroliveira/aspect/internal/spec"
	"github.com/eideroliveira/aspect/internal/workspace"
)

// TestInput is what the Tester sees.
type TestInput struct {
	System       spec.System
	Module       spec.Module
	Code         []workspace.File
	Dependencies []workspace.File
}

// ScenarioCoverage maps a spec scenario to the tests that exercise it.
type ScenarioCoverage struct {
	Scenario string   `json:"scenario"`
	Tests    []string `json:"tests"`
}

// TestOutput is the Tester's proposal.
type TestOutput struct {
	Files    []workspace.File   `json:"files"`
	Coverage []ScenarioCoverage `json:"coverage"`
	Notes    string             `json:"notes"`
	Concerns []string           `json:"concerns"`
}

// Tester writes tests from scenarios and invariants, not from the code. The
// code is shown only so tests compile against real names.
type Tester struct {
	LLM llm.Client
}

const testerSystem = `You are the Tester agent in Aspect, a pipeline that builds software from a formal specification.

Your tests are the executable form of the specification. Derive them from the scenarios, invariants, and pre/post conditions in the spec, not from what the implementation happens to do. The implementation is shown so your tests compile against its real identifiers; if it disagrees with the spec, the spec wins and the test should fail.

Rules:
- Write Go tests in the module's directory, package <module> (internal tests) unless the spec requires the external package.
- Every scenario gets at least one test whose name includes the scenario id (TestReserve_S2). Report the mapping in coverage.
- Every invariant gets a property-style test: drive the module through many operation sequences (a small deterministic fuzz with math/rand and a fixed seed, or table-driven sequences) and assert the invariant after each step.
- Concurrency constraints get a test that runs operations from many goroutines; the pipeline runs tests with -race.
- Standard library only. No test frameworks.
- Only write files ending in _test.go inside the module's directory. Never modify implementation files.
- Answer with JSON matching the schema you were given.`

var testSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"files", "coverage", "notes", "concerns"},
	"properties": map[string]any{
		"files": filesSchema(),
		"coverage": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"scenario", "tests"},
				"properties": map[string]any{
					"scenario": map[string]any{"type": "string"},
					"tests":    stringList(),
				},
			},
		},
		"notes":    map[string]any{"type": "string"},
		"concerns": stringList(),
	},
}

// Generate writes the module's tests.
func (t *Tester) Generate(ctx context.Context, in TestInput) (TestOutput, llm.Response, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# System\n\n```yaml\n%s```\n\n", renderSystem(in.System))
	fmt.Fprintf(&b, "# Module under test\n\nImport path: %s/%s\n\n```yaml\n%s```\n\n",
		in.System.ModulePath, in.Module.Name, renderYAML(in.Module))
	b.WriteString(renderFiles("Dependencies", in.Dependencies))
	b.WriteString(renderFiles("Implementation (for identifiers only)", in.Code))
	b.WriteString("Write the tests that would convince a sceptical reviewer the module meets its spec.\n")

	var out TestOutput
	resp, err := complete(ctx, t.LLM, testerSystem, b.String(), testSchema, &out)
	if err != nil {
		return out, resp, fmt.Errorf("tester: %w", err)
	}
	out.Files = keepModuleFiles(in.Module.Name, out.Files, true)
	if len(out.Files) == 0 {
		return out, resp, fmt.Errorf("tester: returned no _test.go files inside %s/", in.Module.Name)
	}
	return out, resp, nil
}
