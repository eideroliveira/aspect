package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Write stores report.json and REPORT.md next to the generated module.
func (rep *Report) Write(outDir string) error {
	dir := filepath.Join(outDir, rep.System)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), b, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "REPORT.md"), []byte(rep.Markdown()), 0o644)
}

// Markdown renders the report for humans.
func (rep *Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Aspect report: %s\n\n", rep.System)
	fmt.Fprintf(&b, "Topology: %s  \nLanguage: %s  \nModel: `%s`  \nStarted: %s  \nDuration: %s  \nLLM calls: %d (in %d / out %d / cached %d tokens)\n\n",
		rep.Topology, rep.Language, rep.Model, rep.Started.Format("2006-01-02 15:04:05"), rep.Finished.Sub(rep.Started).Round(1e9),
		rep.Usage.Calls, rep.Usage.InputTokens, rep.Usage.OutputTokens, rep.Usage.CacheReadTokens)

	b.WriteString("## Goals\n\n| Goal | Verify | Status | System pass | By module |\n|---|---|---|---|---|\n")
	for _, g := range rep.Goals {
		var parts []string
		for m, st := range g.ByModule {
			parts = append(parts, fmt.Sprintf("%s: %s", m, st))
		}
		sort.Strings(parts)
		fmt.Fprintf(&b, "| **%s** %s | %s | %s | %s | %s |\n", g.ID, g.Statement, g.Verify, badge(string(g.Status)), badge(string(g.System)), strings.Join(parts, "<br>"))
	}

	b.WriteString("\n## System\n\n")
	if rep.SystemVerdictError != "" {
		fmt.Fprintf(&b, "**The system pass failed:** %s\n", rep.SystemVerdictError)
	} else {
		fmt.Fprintf(&b, "Intent: **%s** — %s\n", rep.SystemVerdict.IntentStatus, oneLine(rep.SystemVerdict.IntentRationale))
		if len(rep.SystemVerdict.Goals) > 0 {
			b.WriteString("\n| Goal | System verdict | Confidence | Evidence |\n|---|---|---|---|\n")
			for _, g := range rep.SystemVerdict.Goals {
				ev := g.Evidence
				if len(g.Gaps) > 0 {
					ev += " Gaps: " + strings.Join(g.Gaps, "; ")
				}
				fmt.Fprintf(&b, "| %s | %s | %.0f%% | %s |\n", g.ID, badge(string(g.Status)), g.Confidence*100, oneLine(ev))
			}
		}
		if len(rep.SystemVerdict.Integration) > 0 {
			b.WriteString("\n| Surface | Provider | Consumer | Status | Note |\n|---|---|---|---|---|\n")
			for _, f := range rep.SystemVerdict.Integration {
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", f.Surface, f.Provider, f.Consumer, badge(string(f.Status)), oneLine(f.Note))
			}
		}
		if len(rep.SystemVerdict.Recommendations) > 0 {
			b.WriteString("\nRecommendations:\n\n")
			for _, c := range rep.SystemVerdict.Recommendations {
				fmt.Fprintf(&b, "- %s\n", oneLine(c))
			}
		}
	}

	if len(rep.Tiers) > 1 {
		b.WriteString("\n## Tiers\n\n| Tier | Language | Output |\n|---|---|---|\n")
		for _, t := range rep.Tiers {
			fmt.Fprintf(&b, "| %s | %s | `%s` |\n", t.Name, t.Language, t.Dir)
		}
	}

	for _, m := range rep.Modules {
		if m.Tier != "" {
			fmt.Fprintf(&b, "\n## Module `%s` (tier %s)\n\n", m.Module, m.Tier)
		} else {
			fmt.Fprintf(&b, "\n## Module `%s`\n\n", m.Module)
		}
		if m.Error != "" {
			fmt.Fprintf(&b, "**Failed:** %s\n\n", m.Error)
		}
		tests := "passed"
		if !m.TestsOK {
			tests = "FAILED"
		}
		fmt.Fprintf(&b, "Tests %s after %d run(s). Intent: **%s** — %s\n\n", tests, m.Iterations, m.Verdict.IntentStatus, m.Verdict.IntentRationale)
		if len(m.Verdict.Goals) > 0 {
			b.WriteString("| Goal | Status | Confidence | Evidence |\n|---|---|---|---|\n")
			for _, g := range m.Verdict.Goals {
				ev := g.Evidence
				if len(g.Gaps) > 0 {
					ev += " Gaps: " + strings.Join(g.Gaps, "; ")
				}
				fmt.Fprintf(&b, "| %s | %s | %.0f%% | %s |\n", g.ID, badge(string(g.Status)), g.Confidence*100, oneLine(ev))
			}
			b.WriteString("\n")
		}
		if len(m.Verdict.Scenarios) > 0 {
			b.WriteString("| Scenario | Covered | Test | Note |\n|---|---|---|---|\n")
			for _, s := range m.Verdict.Scenarios {
				fmt.Fprintf(&b, "| %s | %v | `%s` | %s |\n", s.Scenario, s.Covered, s.Test, oneLine(s.Note))
			}
			b.WriteString("\n")
		}
		if len(m.Concerns) > 0 {
			b.WriteString("Concerns raised by agents:\n\n")
			for _, c := range m.Concerns {
				fmt.Fprintf(&b, "- %s\n", oneLine(c))
			}
			b.WriteString("\n")
		}
		if len(m.Verdict.Recommendations) > 0 {
			b.WriteString("Recommendations:\n\n")
			for _, c := range m.Verdict.Recommendations {
				fmt.Fprintf(&b, "- %s\n", oneLine(c))
			}
			b.WriteString("\n")
		}
		if m.CoderNotes != "" {
			fmt.Fprintf(&b, "<details><summary>Coder notes</summary>\n\n%s\n\n</details>\n\n", m.CoderNotes)
		}
		if m.TestOutput != "" {
			fmt.Fprintf(&b, "<details><summary>Last test run</summary>\n\n```\n%s\n```\n\n</details>\n", m.TestOutput)
		}
	}
	return b.String()
}

func badge(status string) string {
	switch status {
	case "achieved", "aligned":
		return "✅ " + status
	case "partial", "drifted":
		return "🟡 " + status
	case "not_achieved", "violated":
		return "❌ " + status
	default:
		return "⚪ " + status
	}
}

func oneLine(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(s), "\n", " "), "|", "\\|")
}
