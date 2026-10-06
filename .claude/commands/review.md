---
description: Run the Aspect review gates locally via Qwen, automatically gating PR merges with zero Anthropic API calls.
argument-hint: "[base-ref, default origin/main]"
---

Run the Aspect review gates on this branch using the local Qwen 2.5 Coder multi-agent review engine.

Follow these steps:

1. **Base Reference:** Determine the base branch: `$ARGUMENTS` (defaults to `origin/main` if omitted).

2. **Run Local Review Gates:**
   - **In Claude Desktop (MCP):** Call the tool:
     `review_with_aspect_gates(repo_path=".", base_ref="<base-ref>", publish_to_pr=True)`
     This executes all applicable review agents locally on Qwen 2.5 Coder (`adversarial-reviewer`, `security-reviewer`, `red-team`, `quality-reviewer`, `consistency-reviewer`, `readability-reviewer`, `design-reviewer`, `architect`, `spec-keeper`) without Anthropic API billing.
   - **In Claude Code / Terminal:** Run via Bash:
     `aspect-review gates --repo . --base <base-ref>`
     (or `.claude/review/run.sh --publish <base-ref>`)

3. **PR Merge Gating & Decision:**
   - If an open pull request exists for this branch, the review result is automatically published to GitHub as the sticky comment, and the GitHub commit status checks (`plan` and `gate`) are posted directly on the PR head.
   - **If Outcome is BLOCK:**
     State clearly:
     "🛑 **PR Merge Blocked:** Review gates found blocking issues."
     List all blocking findings with their exact file and line locations, explain the defect/vulnerability, and provide the actionable fix.
     **Do NOT proceed with merging the PR or branch while gates are blocking.**
   - **If Outcome is PASS:**
     State clearly:
     "✅ **PR Merge Cleared:** All blocking review gates passed. The branch is approved for merge."
