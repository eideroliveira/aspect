#!/usr/bin/env python3
"""Keep only mined cases where the fix repairs a defect the introducing PR added.

Blame finds the PR that last touched the lines a fix changed, which is not
always the PR that caused the bug (a rename, a later feature, a deliberate
behaviour change). A judge reads both diffs and decides; it also writes the
one-paragraph defect statement the grader scores against.

Usage: curate.py cases/raw.jsonl > cases/gosite.jsonl
"""
import argparse
import json
import sys

from judge import ask, git_diff

SCHEMA = {
    "type": "object", "additionalProperties": False,
    "required": ["valid", "defect", "location", "category", "detectable_from_diff", "why"],
    "properties": {
        "valid": {"type": "boolean"},
        "defect": {"type": "string"},
        "location": {"type": "string"},
        "category": {"type": "string", "enum": ["correctness", "security", "performance",
                                                 "design", "test-gap", "data", "spec-drift", "docs"]},
        "detectable_from_diff": {"type": "string", "enum": ["yes", "with-context", "no"]},
        "why": {"type": "string"},
    },
}

PROMPT = """You are curating an evaluation set for code-review agents.

PR A was merged first. PR B was merged later and its description claims to fix
something. Decide whether PR B repairs a DEFECT THAT PR A INTRODUCED: code in
PR A's diff that was wrong when merged (a bug, a security hole, a performance
problem, a missed case), as opposed to a change of requirements, a new feature,
a refactor, copy or styling changes, or a defect that was introduced by some
other change.

Answer:
- valid: true only if PR A's diff contains the defect PR B repairs.
- defect: if valid, one paragraph stating the defect as a reviewer of PR A
  should have written it (what is wrong, where, and what goes wrong at run time).
  Do not mention PR B. If not valid, a short reason.
- location: file (and function or line if you can tell) in PR A's head.
- category: the harm.
- detectable_from_diff: could a careful reviewer of PR A have caught it from
  the diff alone ("yes"), only by reading surrounding code ("with-context"),
  or only with knowledge outside the repository ("no")?
- why: two sentences of reasoning.

## PR A: {a_subject}
```diff
{a_diff}
```

## PR B: {b_subject}
{b_body}

```diff
{b_diff}
```
"""


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("raw")
    ap.add_argument("--repo", required=True)
    ap.add_argument("--model", default=None)
    a = ap.parse_args()
    for line in open(a.raw):
        c = json.loads(line)
        rv, tr = c["review"], c["truth"]
        prompt = PROMPT.format(
            a_subject=rv["subject"], a_diff=git_diff(a.repo, rv["base"], rv["head"]),
            b_subject=tr["subject"], b_body=tr["body"][:3000],
            b_diff=git_diff(a.repo, tr["base"], tr["head"], max_chars=12000, exclude_tests=True))
        try:
            v, cost = ask(prompt, SCHEMA, model=a.model)
        except Exception as e:  # keep going; a failed judgement drops the case
            print(f"skip {c['id']}: {e}", file=sys.stderr)
            continue
        c["truth"].update(v)
        print(f"{'KEEP' if v['valid'] else 'drop'} {c['id']} ({v['detectable_from_diff']}, ${cost}): {v['defect'][:90]}",
              file=sys.stderr, flush=True)
        if v["valid"]:
            print(json.dumps(c), flush=True)


if __name__ == "__main__":
    main()
