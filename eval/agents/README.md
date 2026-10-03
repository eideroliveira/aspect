# Evaluating the review agents on a real repository

Does a review agent catch bugs that actually shipped? This harness answers
that from a repository's own history, without hand-written test cases.

1. **Mine** (`mine.py`). For every merged PR whose description reads like a
   fix, blame the lines it removed or rewrote at the commit before it merged.
   The PR that owns most of those lines is the candidate *introducing* PR.
2. **Curate** (`curate.py`). A judge reads both diffs and keeps the pair only
   if the fix repairs a defect the introducing PR added (not a requirement
   change or a later regression). It writes the defect statement a reviewer
   of the introducing PR should have written.
3. **Run** (`run.py`). Each agent reviews the introducing PR as if it were
   open: a fresh repository fetched only up to the PR head (the fix is not
   in its history), the agents under test copied into `.claude/`, the
   project's own agents, hooks and settings removed, and a headless Claude
   session with no user settings or MCP servers, so no memory of the fix
   leaks in. The prompt is the change block of `docs/design.md` §2.4.
4. **Grade** (`grade.py`). A judge decides whether a finding catches the
   known defect (caught, partial, missed) and sorts the other findings into
   plausible issues and noise.
5. **Report** (`report.py`). Recall, blocking recall, noise, cost and time per
   agent.

```sh
python3 mine.py ~/src/target --since 2026-09-01 > cases/raw.jsonl
python3 curate.py cases/raw.jsonl --repo ~/src/target > cases/target.jsonl
python3 run.py --cases cases/target.jsonl --repo ~/src/target \
    --agent adversarial-reviewer --agent security-red-team
python3 grade.py --cases cases/target.jsonl
python3 report.py
```

`cases/` and `results/` are gitignored: they quote the evaluated
repository, which may be private.

Limits worth knowing when reading the numbers:

- Only bugs that were later found and fixed are counted, so recall is
  measured against a floor; the "plausible extras" column is where agents
  find things nobody fixed yet, and it needs a human to confirm.
- Blame picks the PR that last touched the fixed lines. Curation drops most
  misattributions, but the judge can be wrong in both directions.
- Each run is a full agent session; a case set of 20 across four agents is
  80 sessions.
