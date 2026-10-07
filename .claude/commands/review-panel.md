---
description: Open the live auto-refreshing Aspect Code Review panel in your web browser.
argument-hint: "[--port 9922]"
---

Open the live auto-refreshing Aspect Code Review dashboard panel.

1. **In Claude Desktop (MCP):**
   Call the tool:
   `open_review_panel(port=9922)`

2. **In Claude Code / Terminal:**
   Run:
   `aspect-review panel`

3. **Dashboard Details:**
   - **URL:** `http://localhost:9922`
   - **Refresh Rate:** 2 seconds
   - **Displays:** Active review runs, live per-agent execution status, elapsed times, blocker/warning counts, and recent history.
