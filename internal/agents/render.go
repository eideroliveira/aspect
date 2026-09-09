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

	"github.com/eideroliveira/aspect/internal/lang"
	"github.com/eideroliveira/aspect/internal/llm"
	"github.com/eideroliveira/aspect/internal/spec"
	"github.com/eideroliveira/aspect/internal/workspace"
)

// Task is the part of the input every agent shares.
type Task struct {
	Spec   *spec.Spec
	Module *spec.Module
	Lang   *lang.Profile
}

func (t Task) system() spec.System { return t.Spec.System }

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
		fmt.Fprintf(&b, "### %s\n```\n%s\n```\n\n", f.Path, strings.TrimRight(f.Content, "\n"))
	}
	return b.String()
}

// renderContext is the system-level context every agent needs: intent,
// goals, constraints, the language stack, the data model in outline, and the
// interfaces in outline. Module-level detail is rendered by renderModule.
func renderContext(t Task) string {
	s := t.system()
	var b strings.Builder
	view := struct {
		Name        string      `yaml:"name"`
		Intent      string      `yaml:"intent"`
		Language    string      `yaml:"language"`
		ModulePath  string      `yaml:"module_path"`
		Goals       []spec.Goal `yaml:"goals"`
		Constraints []string    `yaml:"constraints,omitempty"`
	}{s.Name, s.Intent, s.Language, s.ModulePath, s.Goals, s.Constraints}
	fmt.Fprintf(&b, "# System\n\n```yaml\n%s```\n\n", renderYAML(view))

	if ls := t.Spec.LanguageStack(); ls != nil {
		fmt.Fprintf(&b, "# Stack (%s)\n\n```yaml\n%s```\n\n", t.Lang.DisplayName, renderYAML(ls))
	}
	if db := s.Database; db != nil {
		type outline struct {
			Name   string `yaml:"name"`
			Intent string `yaml:"intent,omitempty"`
		}
		var ents []outline
		for _, e := range db.Entities {
			ents = append(ents, outline{e.Name, e.Intent})
		}
		view := struct {
			Engine     string      `yaml:"engine"`
			Migrations string      `yaml:"migrations"`
			Test       spec.DBTest `yaml:"test"`
			Entities   []outline   `yaml:"entities"`
		}{db.Engine, db.Migrations, db.Test, ents}
		fmt.Fprintf(&b, "# Database (outline; owned entities are detailed below)\n\n```yaml\n%s```\n\n", renderYAML(view))
	}
	if len(s.Interfaces) > 0 {
		type outline struct {
			Name      string   `yaml:"name"`
			Kind      string   `yaml:"kind"`
			Intent    string   `yaml:"intent"`
			Framework string   `yaml:"framework,omitempty"`
			Surfaces  []string `yaml:"surfaces"`
		}
		var ifs []outline
		for _, i := range s.Interfaces {
			o := outline{i.Name, i.Kind, i.Intent, i.Framework, nil}
			for _, sf := range i.Surfaces {
				o.Surfaces = append(o.Surfaces, sf.Name)
			}
			ifs = append(ifs, o)
		}
		fmt.Fprintf(&b, "# Interfaces (outline; surfaces this module implements are detailed below)\n\n```yaml\n%s```\n\n", renderYAML(ifs))
	}
	return b.String()
}

// renderModule renders the module spec plus the full definitions of the
// entities it owns, the surfaces it implements, and the frameworks those
// surfaces use.
func renderModule(t Task, heading string) string {
	m := t.Module
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\nModule: %s\nImport path: %s/%s\nCode directory: %s\nTest directory: %s\n\n```yaml\n%s```\n\n",
		heading, m.Name, t.system().ModulePath, m.Name, t.Lang.CodeDir(m.Name), t.Lang.TestDir(m.Name), renderYAML(m))

	if len(m.Entities) > 0 {
		var ents []spec.Entity
		for _, name := range m.Entities {
			if e := t.Spec.Entity(name); e != nil {
				ents = append(ents, *e)
			}
		}
		fmt.Fprintf(&b, "## Entities owned by this module\n\nThis module defines these persistent types and is the only writer of them.\n\n```yaml\n%s```\n\n", renderYAML(ents))
	}
	refs, _ := t.Spec.ResolveSurfaces(m.Surfaces)
	if len(refs) > 0 {
		type surfaceView struct {
			Interface string       `yaml:"interface"`
			Kind      string       `yaml:"kind"`
			Framework string       `yaml:"framework,omitempty"`
			Auth      string       `yaml:"interface_auth,omitempty"`
			Surface   spec.Surface `yaml:"surface"`
		}
		var views []surfaceView
		frameworks := map[string]bool{}
		for _, r := range refs {
			views = append(views, surfaceView{r.Interface.Name, r.Interface.Kind, r.Interface.Framework, r.Interface.Auth, *r.Surface})
			if r.Interface.Framework != "" {
				frameworks[r.Interface.Framework] = true
			}
		}
		fmt.Fprintf(&b, "## Surfaces this module implements\n\n```yaml\n%s```\n\n", renderYAML(views))
		if ls := t.Spec.LanguageStack(); ls != nil {
			for _, f := range ls.Frameworks {
				if frameworks[f.Name] && f.Guidance != "" {
					fmt.Fprintf(&b, "### How to use %s here\n\n%s\n\n", f.Name, strings.TrimSpace(f.Guidance))
				}
			}
		}
	}
	return b.String()
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
				"path":    map[string]any{"type": "string", "description": "Path relative to the project root, inside the module's directory"},
				"content": map[string]any{"type": "string", "description": "Complete file content"},
			},
		},
	}
}

func stringList() map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
}
