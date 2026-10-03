---
description: Run the review gates (spec keeper, adversarial reviewer, security red team, and any other applicable agent) on the current branch, the same way CI does.
argument-hint: "[base-ref, default origin/main]"
---

Run the review gates on this branch. Follow these steps exactly; the
decision is made by `aspect gate`, not by you.

**aspect** below means `aspect` from `PATH`, or `go run ./cmd/aspect` inside
the Aspect repository. If neither works, stop and tell the user to install
it with `go install github.com/eideroliveira/aspect/cmd/aspect@latest`.

1. **Plan.** Pick a run id, `local-<UTC timestamp>`, and run:

   ```
   aspect gate plan -base ${ARGUMENTS:-origin/main} -o .review/<run-id>/plan.json
   ```

   If the plan lists no agents, say so and stop. Otherwise show the user
   the agents, their modes and reasons in a short table.

2. **Run the agents in parallel.** In one message, delegate to every planned
   agent with the Task tool (`subagent_type` is the agent's name). Each
   prompt is exactly the change block, with values from `plan.json`:

   ```
   base: <plan.base>
   head: <plan.head>
   spec: <plan.spec>
   mode: <the agent's mode>
   out:  .review/<run-id>/<agent>.json
   files: <the agent's files, comma-separated>
   ```

   Add nothing else: no summary of the change, no opinion, no PR
   description. Judges must form their own view (docs/design.md §2.4).

3. **Collect.** For each agent, check that `.review/<run-id>/<agent>.json`
   exists. If it does not but the agent printed its report as the last
   fenced `json` block, save that block **verbatim** with
   `aspect gate extract -o .review/<run-id>/<agent>.json` (pipe the agent's
   final message to it). Never write, repair or edit a report yourself: a
   missing or malformed report must fail the gate.

4. **Handoffs, once.** Read the `handoffs` of every report. For a target that
   is a judge and was not in the plan, run it once in `advisory` mode with
   the same change block plus `handoff: .review/<run-id>/<sender>.json`, and
   `out: .review/<run-id>/handoffs/<target>.json`. Do not run authoring
   agents (spec-keeper in author mode, test-data-generator, docs-writer,
   videocast-script-writer) here: list their handoffs for the user, who
   decides whether to run them. An agent run because of a handoff never
   hands back to its sender.

5. **Decide.** Run:

   ```
   aspect gate check .review/<run-id>
   aspect gate render .review/<run-id>
   ```

   Show the rendered Markdown. Then, in two or three sentences, say whether
   the branch would pass in CI and what to fix first. If `check` blocked
   because of spec drift, offer to run `spec-keeper` in `author` mode to
   apply its recommended edits.

Do not commit, push or change any file outside `.review/` during this
command.
