---
description: Compare review findings side-by-side between two review runs (e.g. Anthropic Claude vs local Qwen 2.5 Coder).
argument-hint: "<run-a> <run-b>"
---

Compare review findings side-by-side between two review runs to evaluate local Qwen 2.5 Coder vs previous Anthropic runs.

Follow these steps:

1. **Run IDs:**
   Extract `<run-a>` and `<run-b>` from `$ARGUMENTS`.
   If omitted, list available runs first using `list_review_runs()`.

2. **In Claude Desktop (MCP):**
   - Call the tool:
     `compare_review_runs(run_a="<run-a>", run_b="<run-b>")`
   - Displays comparative metrics table (Engine, Total Findings, Blockers, Warnings), reviewer persona breakdown, and common files.

3. **In Claude Code / Terminal:**
   - Run:
     `aspect-review compare <run-a> <run-b>`

4. **In the Web Panel:**
   - Run `/review-panel` or open `http://localhost:9922`, click the **Anthropic vs Qwen Comparison** tab, select the two runs, and view side-by-side finding comparison cards.
