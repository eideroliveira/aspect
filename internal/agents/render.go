// Package agents holds the roles in the Aspect pipeline. Each agent is a pure
// function of its inputs and an llm.Client: it renders a prompt, requests a
// structured answer, and returns typed data. Agents never write files or run
// commands; the pipeline does that, so every side effect is traceable.
package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/eideroliveira/aspect/internal/llm"
	"github.com/eideroliveira/aspect/internal/spec"
	"github.com/eideroliveira/aspect/internal/workspace"
)

// renderYAML gives the model the spec fragment exactly as the author wrote it.
// YAML is more compact than JSON and matches the file the user is editing, so
// the model's comments about the spec line up with what the user sees.
func renderYAML(v any) string {
	b, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<render error: %v>", err)
	}
	return string(b)
}

func renderFiles(title string, files []workspace.File) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", title)
	for _, f := range files {
		fmt.Fprintf(&b, "### %s\n```go\n%s\n```\n\n", f.Path, strings.TrimRight(f.Content, "\n"))
	}
	return b.String()
}

func renderSystem(s spec.System) string {
	// Goals and constraints are the system-level context every module needs;
	// module details are rendered separately so prompts stay focused.
	view := struct {
		Name        string      `yaml:"name"`
		Intent      string      `yaml:"intent"`
		ModulePath  string      `yaml:"module_path"`
		Goals       []spec.Goal `yaml:"goals"`
		Constraints []string    `yaml:"constraints,omitempty"`
	}{s.Name, s.Intent, s.ModulePath, s.Goals, s.Constraints}
	return renderYAML(view)
}

// complete requests a JSON answer matching schema and decodes it into out.
func complete(ctx context.Context, c llm.Client, system, prompt string, schema map[string]any, out any) (llm.Response, error) {
	resp, err := c.Complete(ctx, llm.Request{System: system, Prompt: prompt, Schema: schema})
	if err != nil {
		return resp, err
	}
	text := stripFence(resp.Text)
	if err := json.Unmarshal([]byte(text), out); err != nil {
		return resp, fmt.Errorf("agent returned invalid JSON: %w\n--- response ---\n%s", err, truncate(text, 2000))
	}
	return resp, nil
}

// stripFence tolerates a model that wraps JSON in a markdown code fence even
// though structured outputs should make that impossible.
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…(truncated)"
}

// filesSchema is the JSON schema fragment for a list of files.
func filesSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"path", "content"},
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "Path relative to the module root, e.g. stock/stock.go"},
				"content": map[string]any{"type": "string", "description": "Complete file content"},
			},
		},
	}
}

func stringList() map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
}
