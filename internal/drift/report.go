package drift

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Write stores drift.json and DRIFT.md under dir.
func (r *Report) Write(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "drift.json"), b, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "DRIFT.md"), []byte(r.Markdown()), 0o644)
}

// Markdown renders the drift report for humans.
func (r *Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Drift report: %s\n\nChecked: %s  \nBaseline: %s  \n\n", r.System, r.Checked.Format("2006-01-02 15:04:05"), orNone(r.Baseline))
	if !r.Drifted() {
		b.WriteString("**No drift.** Every module is present, tests pass, and no goal regressed.\n\n")
	} else {
		b.WriteString("## Attention\n\n")
		if m := r.Missing(); len(m) > 0 {
			fmt.Fprintf(&b, "- Modules in the spec with no code: %s\n", strings.Join(m, ", "))
		}
		if len(r.Orphans) > 0 {
			fmt.Fprintf(&b, "- Code directories no module claims: %s\n", strings.Join(r.Orphans, ", "))
		}
		if f := r.Failing(); len(f) > 0 {
			fmt.Fprintf(&b, "- Modules whose tests fail: %s\n", strings.Join(f, ", "))
		}
		if g := r.Regressions(); len(g) > 0 {
			fmt.Fprintf(&b, "- Goals that regressed since the baseline: %s\n", strings.Join(g, ", "))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Goals\n\n| Goal | Verify | Now | Before | Change | System pass | By module |\n|---|---|---|---|---|---|---|\n")
	for _, g := range r.Goals {
		var parts []string
		for m, st := range g.ByModule {
			parts = append(parts, fmt.Sprintf("%s: %s", m, st))
		}
		sort.Strings(parts)
		fmt.Fprintf(&b, "| **%s** %s | %s | %s | %s | %s | %s | %s |\n", g.ID, g.Statement, g.Verify, badge(string(g.Status)), orNone(string(g.Previous)), g.Change, badge(string(g.System)), strings.Join(parts, "<br>"))
	}

	if r.SystemVerdictError == "" && r.SystemVerdict.IntentStatus != "" {
		fmt.Fprintf(&b, "\nSystem intent: **%s** — %s\n", r.SystemVerdict.IntentStatus, oneLine(r.SystemVerdict.IntentRationale))
	}

	for _, m := range r.Modules {
		name := m.Module
		if m.Tier != "" {
			name += " (tier " + m.Tier + ")"
		}
		fmt.Fprintf(&b, "\n## Module `%s`\n\n", name)
		switch {
		case !m.Present:
			b.WriteString("**Missing:** the spec names this module but no code exists for it.\n")
			continue
		case m.Error != "":
			fmt.Fprintf(&b, "**Error:** %s\n", m.Error)
		}
		tests := "passed"
		if !m.TestsOK {
			tests = "FAILED"
		}
		fmt.Fprintf(&b, "%d file(s); tests %s. Intent: **%s** — %s\n", len(m.Files), tests, orNone(string(m.Verdict.IntentStatus)), oneLine(m.Verdict.IntentRationale))
		if len(m.Verdict.Goals) > 0 {
			b.WriteString("\n| Goal | Status | Confidence | Evidence |\n|---|---|---|---|\n")
			for _, g := range m.Verdict.Goals {
				ev := g.Evidence
				if len(g.Gaps) > 0 {
					ev += " Gaps: " + strings.Join(g.Gaps, "; ")
				}
				fmt.Fprintf(&b, "| %s | %s | %.0f%% | %s |\n", g.ID, badge(string(g.Status)), g.Confidence*100, oneLine(ev))
			}
		}
		if !m.TestsOK && m.TestOutput != "" {
			fmt.Fprintf(&b, "\n<details><summary>Test run</summary>\n\n```\n%s\n```\n\n</details>\n", m.TestOutput)
		}
	}
	return b.String()
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func badge(status string) string {
	switch status {
	case "achieved", "aligned":
		return "✅ " + status
	case "partial", "drifted":
		return "🟡 " + status
	case "not_achieved", "violated":
		return "❌ " + status
	case "":
		return "none"
	default:
		return "⚪ " + status
	}
}

func oneLine(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(s), "\n", " "), "|", "\\|")
}
