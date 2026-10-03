#!/usr/bin/env python3
"""Score agent runs against each case's known defect.

For every run without a grade, a judge reads the defect statement (written by
curate.py from the later fix) and the agent's findings, and decides whether a
finding catches it. It also sorts the other findings into plausible issues and
noise, which is how false-positive load is tracked.

Usage: grade.py [--runs results/runs.jsonl] [--cases cases/gosite.jsonl]
Writes results/grades.jsonl (one line per run, keyed by case, agent, started).
"""
import argparse
import json
import pathlib

from judge import ask

HERE = pathlib.Path(__file__).resolve().parent

SCHEMA = {
    "type": "object", "additionalProperties": False,
    "required": ["verdict", "matched", "blocking_match", "others", "notes"],
    "properties": {
        "verdict": {"type": "string", "enum": ["caught", "partial", "missed"]},
        "matched": {"type": "array", "items": {"type": "string"}},
        "blocking_match": {"type": "boolean"},
        "others": {"type": "array", "items": {
            "type": "object", "additionalProperties": False,
            "required": ["id", "assessment"],
            "properties": {
                "id": {"type": "string"},
                "assessment": {"type": "string", "enum": ["plausible", "unsupported", "trivial"]},
            }}},
        "notes": {"type": "string"},
    },
}

PROMPT = """You grade a code-review agent. The agent reviewed a pull request that,
as we now know, contained this defect (it was fixed later):

DEFECT: {defect}
LOCATION: {location}

The agent's findings follow as JSON. Decide:
- verdict: "caught" if a finding identifies this defect (same mechanism, same
  place or an equivalent one), "partial" if a finding points at the right code
  with a different or vaguer concern that would have led a reader to the bug,
  "missed" otherwise.
- matched: the ids of the findings that catch it (empty when missed).
- blocking_match: true if a matching finding has severity critical or high and
  confidence >= 0.5.
- others: every finding NOT in matched, assessed as "plausible" (a real issue
  a maintainer would want to know about, judging by the evidence given),
  "unsupported" (claim not backed by its evidence, or likely wrong), or
  "trivial" (true but not worth a reviewer's time).
- notes: one or two sentences on what the agent got right or wrong.

Findings:
```json
{findings}
```
"""


def key(r):
    return f"{r['case']}|{r['agent']}|{r['started']}"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--runs", default=str(HERE / "results" / "runs.jsonl"))
    ap.add_argument("--cases", default=str(HERE / "cases" / "gosite.jsonl"))
    ap.add_argument("--grades", default=str(HERE / "results" / "grades.jsonl"))
    ap.add_argument("--model", default=None)
    a = ap.parse_args()

    cases = {c["id"]: c for c in map(json.loads, open(a.cases))}
    done = set()
    if pathlib.Path(a.grades).exists():
        done = {g["key"] for g in map(json.loads, open(a.grades))}
    for r in map(json.loads, open(a.runs)):
        k = key(r)
        if k in done or r["case"] not in cases:
            continue
        tr = cases[r["case"]]["truth"]
        g = {"key": k, "case": r["case"], "agent": r["agent"], "cost_usd": r.get("cost_usd"),
             "seconds": r.get("seconds"), "report_source": r.get("report_source")}
        findings = (r.get("report") or {}).get("findings")
        if findings is None:
            g.update(verdict="no-report", matched=[], blocking_match=False, others=[],
                     notes="no parseable aspect-review/v1 report")
        elif not findings:
            g.update(verdict="missed", matched=[], blocking_match=False, others=[], notes="empty report")
        else:
            # Agents may not follow aspect-review/v1 yet; grade whatever shape they wrote.
            slim = [{k2: v2 for k2, v2 in f.items() if k2 not in ("recommendation", "fix", "reproduction")}
                    for f in findings]
            v, cost = ask(PROMPT.format(defect=tr["defect"], location=tr["location"],
                                        findings=json.dumps(slim, indent=1)), SCHEMA, model=a.model)
            g.update(v)
            g["severities"] = {str(f.get("id")): f.get("severity") for f in findings}
        with open(a.grades, "a") as f:
            f.write(json.dumps(g) + "\n")
        print(f"{r['case']} {r['agent']}: {g['verdict']}", flush=True)


if __name__ == "__main__":
    main()
