#!/usr/bin/env python3
"""Summarise grades per agent as Markdown."""
import collections
import json
import pathlib
import sys

HERE = pathlib.Path(__file__).resolve().parent


def main():
    path = sys.argv[1] if len(sys.argv) > 1 else HERE / "results" / "grades.jsonl"
    by = collections.defaultdict(list)
    for g in map(json.loads, open(path)):
        by[f'{g["agent"]} @{g.get("agent_version") or "?"}'].append(g)
    print("| agent | runs | caught | partial | missed | no report | would block | plausible extras | noise | avg $ | avg s |")
    print("|---|---|---|---|---|---|---|---|---|---|---|")
    for agent, gs in sorted(by.items()):
        c = collections.Counter(g["verdict"] for g in gs)
        others = [o["assessment"] for g in gs for o in g.get("others", [])]
        o = collections.Counter(others)
        cost = [g["cost_usd"] for g in gs if g.get("cost_usd")]
        secs = [g["seconds"] for g in gs if g.get("seconds")]
        print(f"| {agent} | {len(gs)} | {c['caught']} | {c['partial']} | {c['missed']} | {c['no-report']} "
              f"| {sum(g.get('blocking_match', False) for g in gs)} | {o['plausible']} "
              f"| {o['unsupported'] + o['trivial']} "
              f"| {sum(cost) / len(cost) if cost else 0:.2f} | {sum(secs) / len(secs) if secs else 0:.0f} |")
    print()
    for agent, gs in sorted(by.items()):
        print(f"### {agent}\n")
        for g in gs:
            print(f"- **{g['case']}**: {g['verdict']}. {g.get('notes', '')}")
        print()


if __name__ == "__main__":
    main()
