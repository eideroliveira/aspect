#!/usr/bin/env python3
"""Run review agents against mined cases and keep their reports.

For each case the agent reviews the introducing PR (review.base..review.head)
inside a sandbox repository that holds only history reachable from the PR
head, so the later fix cannot be seen with git. The session loads no user
settings, hooks or MCP servers, so no memory of the fix leaks in either.

Usage:
  run.py --cases cases/gosite.jsonl --agent adversarial-reviewer [--case ID ...]
         [--repo ~/PL/website/gosite] [--agents-src .claude] [--model opus]
"""
import argparse
import datetime
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent.parent  # the aspect checkout
UPLOAD_PACK = "git -c uploadpack.allowAnySHA1InWant=true upload-pack"
HISTORY_DEPTH = 300


def sh(*cmd, cwd=None, check=True, **kw):
    r = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, **kw)
    if check and r.returncode != 0:
        raise RuntimeError(f"{' '.join(map(str, cmd))}: {r.stderr.strip()[-2000:]}")
    return r


def fill_submodules(repo, box, head):
    """Check out each submodule at the commit the PR head pins, from the local clone.

    Without them an agent cannot read forked dependencies or build the project,
    and reports "could not review the submodule" instead of reviewing it.
    """
    tree = sh("git", "ls-tree", "-r", head, cwd=box).stdout
    for row in tree.splitlines():
        meta, path = row.split("\t", 1)
        mode, _, sha = meta.split()
        src = repo / path
        if mode != "160000" or not (src / ".git").exists():
            continue
        dst = box / path
        dst.mkdir(parents=True, exist_ok=True)
        sh("git", "init", "-q", cwd=dst)
        r = sh("git", "fetch", "-q", "--depth=50", f"--upload-pack={UPLOAD_PACK}", str(src), sha,
               cwd=dst, check=False)
        if r.returncode == 0:
            sh("git", "checkout", "-q", "--detach", sha, cwd=dst)
        else:
            print(f"  submodule {path} @ {sha[:9]} not in local clone", file=sys.stderr)


def sandbox(repo, case, workdir, agents_src):
    """A fresh repository at the PR head with no refs or objects past it."""
    rv = case["review"]
    box = workdir / case["id"]
    if box.exists():
        shutil.rmtree(box)
    box.mkdir(parents=True)
    sh("git", "init", "-q", cwd=box)
    sh("git", "fetch", "-q", f"--depth={HISTORY_DEPTH}", f"--upload-pack={UPLOAD_PACK}",
       str(repo), rv["head"], rv["base"], cwd=box)
    sh("git", "checkout", "-q", "--detach", rv["head"], cwd=box)
    sh("git", "tag", "review-base", rv["base"], cwd=box)
    fill_submodules(repo, box, rv["head"])
    # The project's own agents, hooks and settings are not under test.
    claude = box / ".claude"
    for p in ("agents", "hooks", "settings.json", "settings.local.json", "commands", "skills", "worktrees"):
        t = claude / p
        if t.is_dir():
            shutil.rmtree(t)
        elif t.exists():
            t.unlink()
    # Hide those removals from `git status`, so agents see a clean tree.
    gone = [l for l in sh("git", "ls-files", "--deleted", cwd=box).stdout.splitlines() if l]
    if gone:
        sh("git", "update-index", "--skip-worktree", *gone, cwd=box)
    for p in ("agents", "review", "commands"):
        s = agents_src / p
        if s.is_dir():
            shutil.copytree(s, claude / p, dirs_exist_ok=True)
    # Keep the sandbox's own status clean so agents see only the PR.
    with open(box / ".git" / "info" / "exclude", "a") as f:
        f.write("\n.claude/agents/\n.claude/review/\n.claude/commands/\n.review/\n")
    return box


def spec_for(box, wanted):
    """What the orchestrator would pass: the spec path if it exists at head, else none."""
    if wanted != "auto":
        return wanted
    for p in ("aspect.yaml", "_aspect/aspect.yaml"):
        if (box / p).exists():
            return p
    return "none"


def change_block(case, agent, mode, spec):
    rv = case["review"]
    return "\n".join([
        f"base: {rv['base']}",
        f"head: {rv['head']}",
        f"spec: {spec}",
        f"mode: {mode}",
        f"out:  .review/eval/{agent}.json",
    ])


def last_json_block(text):
    blocks = re.findall(r"```json\s*\n(.*?)\n```", text or "", re.S)
    for b in reversed(blocks):
        try:
            return json.loads(b)
        except json.JSONDecodeError:
            continue
    return None


def agent_version(src, agent):
    """Hash of the agent file and shared protocol, so tuning rounds can be compared."""
    import hashlib
    h = hashlib.sha256()
    for f in [src / "agents" / f"{agent}.md", *sorted((src / "review").glob("*"))]:
        if f.is_file():
            h.update(f.read_bytes())
    return h.hexdigest()[:12]


def run_agent(box, case, agent, a):
    prompt = (f"Review this change.\n\n{change_block(case, agent, a.mode, spec_for(box, a.spec))}\n\n"
              f"PR title: {case['review']['subject']}\n")
    cmd = ["claude", "-p", prompt, "--agent", agent,
           "--output-format", "json",
           "--setting-sources", "project",
           "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}',
           "--no-session-persistence",
           "--permission-mode", "dontAsk",
           "--allowedTools", *a.allowed_tools.split(","),
           "--max-budget-usd", str(a.budget)]
    if a.model:
        cmd += ["--model", a.model]
    started = datetime.datetime.now(datetime.timezone.utc)
    env = {k: v for k, v in os.environ.items() if not k.startswith("OMEGA")}
    r = sh(*cmd, cwd=box, check=False, env=env, timeout=a.timeout)
    secs = (datetime.datetime.now(datetime.timezone.utc) - started).total_seconds()
    try:
        meta = json.loads(r.stdout)
    except json.JSONDecodeError:
        meta = {"result": r.stdout, "is_error": True}
    report_path = box / ".review" / "eval" / f"{agent}.json"
    report, source = None, None
    if report_path.exists():
        try:
            report, source = json.loads(report_path.read_text()), "file"
        except json.JSONDecodeError:
            pass
    if report is None:
        report = last_json_block(meta.get("result"))
        source = "message" if report else None
    return {
        "case": case["id"], "agent": agent,
        "agent_version": agent_version(pathlib.Path(a.agents_src), agent), "mode": a.mode, "model": a.model or "default",
        "started": started.isoformat(), "seconds": round(secs),
        "exit": r.returncode, "stderr": r.stderr[-2000:],
        "cost_usd": meta.get("total_cost_usd"), "turns": meta.get("num_turns"),
        "is_error": meta.get("is_error"),
        "report_source": source, "report": report,
        "final_message": (meta.get("result") or "")[-6000:],
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--cases", required=True)
    ap.add_argument("--agent", action="append", required=True)
    ap.add_argument("--case", action="append", help="case ids (default: all)")
    ap.add_argument("--repo", default=os.path.expanduser("~/PL/website/gosite"))
    ap.add_argument("--agents-src", default=str(ROOT / ".claude"))
    ap.add_argument("--workdir", default=os.environ.get("ASPECT_EVAL_WORK", "/tmp/aspect-eval"))
    ap.add_argument("--results", default=str(HERE / "results" / "runs.jsonl"))
    ap.add_argument("--mode", default="advisory")
    ap.add_argument("--spec", default="auto",
                    help="spec line of the change block; auto passes 'none' when the head has no spec")
    ap.add_argument("--model", default=None)
    ap.add_argument("--budget", type=float, default=5.0)
    ap.add_argument("--timeout", type=int, default=1800)
    ap.add_argument("--resume", action="store_true",
                    help="skip case/agent pairs already in results at the same agent version")
    ap.add_argument("--allowed-tools", default="Read,Grep,Glob,Bash,Write(.review/**)")
    a = ap.parse_args()

    cases = [json.loads(l) for l in open(a.cases) if l.strip()]
    if a.case:
        cases = [c for c in cases if c["id"] in a.case]
    out = pathlib.Path(a.results)
    out.parent.mkdir(parents=True, exist_ok=True)
    done = set()
    if a.resume and out.exists():
        done = {(r["case"], r["agent"], r.get("agent_version")) for r in map(json.loads, open(out))}
    for c in cases:
        todo = [g for g in a.agent
                if (c["id"], g, agent_version(pathlib.Path(a.agents_src), g)) not in done]
        if not todo:
            continue
        box = sandbox(pathlib.Path(a.repo), c, pathlib.Path(a.workdir), pathlib.Path(a.agents_src))
        for agent in todo:
            print(f"run {c['id']} {agent} ...", file=sys.stderr, flush=True)
            rec = run_agent(box, c, agent, a)
            with open(out, "a") as f:
                f.write(json.dumps(rec) + "\n")
            n = len((rec["report"] or {}).get("findings", []))
            print(f"  {rec['seconds']}s ${rec['cost_usd']} report={rec['report_source']} findings={n}",
                  file=sys.stderr, flush=True)


if __name__ == "__main__":
    main()
