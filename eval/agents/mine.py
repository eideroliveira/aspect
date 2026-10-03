#!/usr/bin/env python3
"""Mine evaluation cases from a repository's history.

A case is a pull request that introduced a bug, paired with the later pull
request that fixed it. The fix is found by its description; the introducing
PR is found by blaming the lines the fix removed or changed, at the commit
just before the fix merged. The agent under test reviews the introducing PR
as if it were open today; the fix PR is the ground truth for what it should
have caught.

Usage: mine.py REPO [--since 2026-06-01] [--max-files 40] [--max-lines 1500] > cases.jsonl
"""
import argparse
import collections
import json
import re
import subprocess
import sys

FIX_RE = re.compile(r"\b(fix(es|ed)?|bug|regress(ion|ed)?|broke(n)?|crash(es|ed)?|wrong|never|stops?|no longer)\b", re.I)
TEST_RE = re.compile(r"(_test\.go$|/testdata/|\.test\.|/tests?/|_spec\.)")
PR_RE = re.compile(r"\(#(\d+)\)|pull request #(\d+)")
CITE_RE = re.compile(r"\b(introduced|regress(?:ed|ion)?|broke|caused|since)\b[^.\n]{0,40}?#(\d+)", re.I)


def git(repo, *args, check=True):
    r = subprocess.run(["git", "-C", repo, *args], capture_output=True, text=True)
    if check and r.returncode != 0:
        raise RuntimeError(f"git {' '.join(args)}: {r.stderr.strip()}")
    return r.stdout


def pr_number(subject):
    m = PR_RE.search(subject)
    return int(m.group(1) or m.group(2)) if m else None


def merges(repo, since, branch):
    out = git(repo, "log", "--first-parent", branch, "--merges", f"--since={since}",
              "--format=%H%x1f%P%x1f%s%x1f%b%x1e")
    for rec in out.split("\x1e"):
        rec = rec.strip("\n")
        if not rec:
            continue
        sha, parents, subject, body = rec.split("\x1f")
        p = parents.split()
        if len(p) != 2:
            continue
        yield {"merge": sha, "base": p[0], "head": p[1], "subject": subject, "body": body.strip()}


def removed_ranges(repo, base, head):
    """Lines of `base` that the PR deleted or rewrote, per non-test file."""
    diff = git(repo, "diff", "-U0", "--no-color", f"{base}...{head}")
    ranges, path = collections.defaultdict(list), None
    for line in diff.splitlines():
        if line.startswith("--- "):
            path = None if line == "--- /dev/null" else line[6:]
        elif line.startswith("@@") and path and not TEST_RE.search(path):
            m = re.match(r"@@ -(\d+)(?:,(\d+))? ", line)
            start, count = int(m.group(1)), int(m.group(2) or 1)
            if count:
                ranges[path].append((start, start + count - 1))
    return ranges


def introducing_merge(repo, commit, branch, cache):
    """The first-parent merge on `branch` that brought `commit` in."""
    if commit in cache:
        return cache[commit]
    path = git(repo, "rev-list", "--ancestry-path", "--first-parent", "--merges",
               f"{commit}..{branch}", check=False).split()
    cache[commit] = path[-1] if path else None
    return cache[commit]


def diffstat(repo, base, head):
    files, lines = 0, 0
    for row in git(repo, "diff", "--numstat", f"{base}...{head}").splitlines():
        a, d, _ = row.split("\t", 2)
        files += 1
        lines += (int(a) if a != "-" else 0) + (int(d) if d != "-" else 0)
    return files, lines


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("repo")
    ap.add_argument("--since", default="2026-06-01")
    ap.add_argument("--branch", default="main")
    ap.add_argument("--max-files", type=int, default=40)
    ap.add_argument("--max-lines", type=int, default=1500)
    ap.add_argument("--min-share", type=float, default=0.5,
                    help="share of blamed lines the introducing PR must own")
    a = ap.parse_args()

    cache, seen = {}, set()
    by_pr = {pr_number(m["subject"]): m["merge"] for m in merges(a.repo, "2020-01-01", a.branch)}
    for fx in merges(a.repo, a.since, a.branch):
        # A fix that names the PR it repairs is the strongest signal; blame is the fallback.
        cited = [int(m.group(2)) for m in CITE_RE.finditer(fx["body"])]
        cited = [by_pr[n] for n in cited if n in by_pr and by_pr[n] != fx["merge"]]
        if cited:
            emit(a, fx, cited[0], 1.0, "cited", seen)
            continue
        if not FIX_RE.search(fx["subject"] + "\n" + fx["body"][:600]):
            continue
        votes = collections.Counter()
        for path, spans in removed_ranges(a.repo, fx["base"], fx["head"]).items():
            for lo, hi in spans:
                blame = git(a.repo, "blame", "--porcelain", "-L", f"{lo},{hi}", fx["base"], "--", path, check=False)
                for line in blame.splitlines():
                    m = re.match(r"^([0-9a-f]{40}) \d+ \d+", line)
                    if m:
                        im = introducing_merge(a.repo, m.group(1), a.branch, cache)
                        if im:
                            votes[im] += 1
        total = sum(votes.values())
        if not total:
            continue
        intro, n = votes.most_common(1)[0]
        if n / total >= a.min_share:
            emit(a, fx, intro, n / total, "blame", seen)


def emit(a, fx, intro, share, source, seen):
    if intro in seen:
        return
    p = git(a.repo, "rev-list", "--parents", "-n1", intro).split()
    if len(p) != 3:
        return
    files, lines = diffstat(a.repo, p[1], p[2])
    if files > a.max_files or lines > a.max_lines:
        return
    seen.add(intro)
    subject = git(a.repo, "log", "-1", "--format=%s", intro).strip()
    case = {
        "id": f"pr{pr_number(subject) or intro[:9]}-fixed-by-pr{pr_number(fx['subject']) or fx['merge'][:9]}",
        "kind": "introduced-bug",
        "review": {"base": p[1], "head": p[2], "merge": intro, "subject": subject,
                   "files": files, "lines": lines},
        "truth": {"merge": fx["merge"], "base": fx["base"], "head": fx["head"],
                  "subject": fx["subject"], "body": fx["body"][:4000],
                  "source": source, "blame_share": round(share, 2)},
    }
    print(json.dumps(case), flush=True)
    print(f"case {case['id']}: {subject[:70]}", file=sys.stderr)


if __name__ == "__main__":
    main()
