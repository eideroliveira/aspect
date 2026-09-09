package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/eideroliveira/aspect/internal/llm"
	"github.com/eideroliveira/aspect/internal/workspace"
)

// CodeInput is everything the Coder may know about one module.
type CodeInput struct {
	Task
	// Dependencies is the source of modules this one depends on, so the Coder
	// codes against real signatures instead of guessing.
	Dependencies []workspace.File
	// Existing is the module's current source, set on repair rounds.
	Existing []workspace.File
	// Tests is the module's current tests, set on repair rounds. The Coder may
	// read them but not change them.
	Tests []workspace.File
	// Feedback is the failing build/test output on repair rounds.
	Feedback string
}

// CodeOutput is the Coder's proposal.
type CodeOutput struct {
	Files []workspace.File `json:"files"`
	// Notes explain design decisions in one or two paragraphs.
	Notes string `json:"notes"`
	// Concerns list places where the Coder believes the spec or a test is
	// wrong or contradictory. They are surfaced in the report; the Coder does
	// not silently "fix" the spec.
	Concerns []string `json:"concerns"`
}

// Coder writes the implementation of one module.
type Coder struct {
	LLM llm.Client
}

const coderSystem = `You are the Coder agent in Aspect, a pipeline that builds software from a formal specification.

You implement exactly one module at a time. The specification is the source of truth: implement the stated interface with the stated intent, honour every invariant and constraint, and make each pre/post condition true. Where the spec is silent, choose the simplest design that keeps the module's intent obvious to a reader.

When the module owns database entities, it defines their persistent types, their schema or migrations, and every write path; other modules read through this module's interface. When the module implements interface surfaces (HTTP endpoints, web pages, commands), it wires them exactly as specified, using the framework the spec names and following the framework guidance.

Rules:
- Produce complete, compilable files. Never elide code with comments like "rest unchanged".
- Only write implementation files inside the module's code directory. Never write tests; the Tester agent owns those.
- Use only the libraries the stack section allows. The pipeline checks imports mechanically.
- On a repair round you receive failing build or test output. Fix the implementation. If you are convinced a test contradicts the spec, keep the implementation faithful to the spec and say so in concerns.
- Answer with JSON matching the schema you were given.`

var codeSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"files", "notes", "concerns"},
	"properties": map[string]any{
		"files":    filesSchema(),
		"notes":    map[string]any{"type": "string"},
		"concerns": stringList(),
	},
}

// Generate produces or repairs the module's implementation.
func (c *Coder) Generate(ctx context.Context, in CodeInput) (CodeOutput, llm.Response, error) {
	var b strings.Builder
	b.WriteString(renderContext(in.Task))
	b.WriteString(renderModule(in.Task, "Module to implement"))
	b.WriteString(renderFiles("Dependencies (already implemented, import them)", in.Dependencies))
	if in.Feedback != "" {
		b.WriteString(renderFiles("Current implementation", in.Existing))
		b.WriteString(renderFiles("Current tests (read-only)", in.Tests))
		fmt.Fprintf(&b, "## Failing output\n\n```\n%s\n```\n\n", truncate(in.Feedback, 12000))
		b.WriteString("Repair the implementation so the build and tests pass while staying faithful to the spec. Return every file of the module, not just the ones you changed.\n")
	} else {
		b.WriteString("Implement the module. Return every file it needs.\n")
	}

	var out CodeOutput
	system := coderSystem + "\n\n" + in.Lang.CoderRules
	resp, err := complete(ctx, c.LLM, system, b.String(), codeSchema, &out)
	if err != nil {
		return out, resp, fmt.Errorf("coder: %w", err)
	}
	out.Files = in.Lang.KeepModuleFiles(in.Module.Name, out.Files, false)
	if len(out.Files) == 0 {
		return out, resp, fmt.Errorf("coder: returned no implementation files inside %s", in.Lang.CodeDir(in.Module.Name))
	}
	return out, resp, nil
}
