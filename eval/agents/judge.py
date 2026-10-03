"""A tool-less, memory-less Claude call that returns schema-checked JSON."""
import json
import os
import subprocess
import tempfile


def ask(prompt, schema, model=None, budget=1.0, timeout=600):
    env = {k: v for k, v in os.environ.items() if not k.startswith("OMEGA")}
    with tempfile.TemporaryDirectory() as d:  # an empty cwd: no CLAUDE.md, no repo
        cmd = ["claude", "-p", "--output-format", "json",
               "--json-schema", json.dumps(schema),
               "--setting-sources", "project",
               "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}',
               "--no-session-persistence", "--tools", "",
               "--max-budget-usd", str(budget)]
        if model:
            cmd += ["--model", model]
        r = subprocess.run(cmd, input=prompt, cwd=d, env=env, capture_output=True,
                           text=True, timeout=timeout)
    meta = json.loads(r.stdout)
    out = meta.get("structured_output")
    if out is None:
        out = json.loads(meta["result"])
    return out, meta.get("total_cost_usd")


def git_diff(repo, base, head, max_chars=24000, exclude_tests=False):
    args = ["git", "-C", repo, "diff", "--no-color", "-U3", f"{base}...{head}", "--"]
    if exclude_tests:
        args += [".", ":(exclude)**/*_test.go", ":(exclude)**/testdata/**"]
    d = subprocess.run(args, capture_output=True, text=True).stdout
    if len(d) > max_chars:
        d = d[:max_chars] + f"\n... [diff truncated, {len(d) - max_chars} more chars]"
    return d
