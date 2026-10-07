---
description: Inspect detailed code review findings (title, severity, file, line, code evidence, and recommendations).
argument-hint: "[run-id, default latest] [--severity critical|high|medium|low|info] [--agent <name>]"
---

Inspect detailed code review findings to compare what reviewer personas found (including comparing local Qwen 2.5 Coder against previous Anthropic review runs).

Follow these steps based on arguments and environment:

1. **In Claude Desktop (MCP):**
   - Call the tool:
     `get_review_findings(run_id="<run-id>", severity="<severity>", agent="<agent>")`
   - If `$ARGUMENTS` provides a run ID (or omit for newest), pass it to `run_id`.
   - Claude will render the findings grouped by severity with exact code snippets, locations, and actionable fixes.

2. **In Claude Code / Terminal:**
   - Run:
     `aspect-review findings <run-id>`
   - To filter by severity:
     `aspect-review findings <run-id> --severity high`
   - To filter by reviewer:
     `aspect-review findings <run-id> --agent adversarial-reviewer`

3. **In the Web Panel:**
   - To browse findings interactively with search and filters, run `/review-panel` or open `http://localhost:9922` and click the **Findings Explorer** tab.
