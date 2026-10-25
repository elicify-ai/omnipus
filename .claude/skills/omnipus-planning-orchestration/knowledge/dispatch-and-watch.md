# Dispatch and watch — keep workers observable and resumable

Last reviewed: 2026-10-23 — approved delivery-process rollout

Mechanics for the planning loop's "Dispatch and watch" step. Launcher selection stays
in the personal rules, outside this repo; set `OMNIPUS_WORKER_CLI` from those rules
explicitly rather than relying on the controller's default.

## Pre-flight before a long run or resume

Read root `CLAUDE.md` ("Feature ownership" under "Subagent Workflow", and "Running CLI
agents") and `knowledge/capacity-check.md`. Record the named feature/area owner, lane,
absolute worktree, provider quota/balance receipt and authorized routing choice.
Keep related scope with its capable owner; contained jobs and UAT lanes use the
personal-layer routing. Model names and launcher choices stay outside repo assets.

Verify controller enforcement of lane/worktree lock plus live PID ownership, the
load refusal and worker-owned process-group stop from its actual receipts. These are
requirements on separate tooling, not a claim this document implements them. If that
proof is missing, report BLOCKED on controller readiness before launch or resume;
manual checks are not a substitute for machine enforcement. Never delete a live
owner's lock or resume over a predecessor that is still alive.

## Launch with retained control

Use the existing controller with the role, absolute worktree path, brief file and a
unique, extension-less output base (absolute paths for both files):

```bash
OMNIPUS_WORKER_CLI="$WORKER_LAUNCHER" python3 -I /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/recovery-20261002/logs/steerable_worker_current_turn.py "$ROLE" "$WORKTREE" "$BRIEF" "$OUTPUT_BASE"
```

It retains streamed input/output and writes `.stream.jsonl`, `.control.jsonl` and
`.receipt.json` beside the output base. Record those paths and the worker's `session_id`
as soon as the stream emits it. Give the worker no deadline or cap; keep it running
until done, and intervene through control rather than killing it by the clock.

## Arm the watcher at dispatch, then steer

Use the Monitor tool on that exact `.stream.jsonl` with this watch pattern:
**current-turn result / error / no output for 15 minutes**. Keep the watcher armed while
the worker runs; re-arm it if it ends. Inspect recent progress on each alert, then
review the result or steer drift, hangs or overreach. Fifteen minutes of silence is a
check-and-steer signal, not a worker deadline. Check the controller's current-turn
receipt before accepting a result: a resumed stream can replay old results.

Append a newline-terminated user message to that worker's `.control.jsonl` to steer:

```json
{"type":"user","message":{"role":"user","content":"Stay within the brief; report what is blocking the next step."}}
```

For Agent-tool workers, observe their progress and use `SendMessage` to the original
worker for steering and resuming. New work goes directly to specialists; the retained
#1266 squad lead still uses its own foreground goal-loop wait.

## Resume the same session

If unfinished work stops or disconnects, confirm the old process and its children
have exited and the lane/worktree are free, then run
the controller with the same role and worktree, a follow-up brief and a fresh output
base; the **fifth argument is the original session id**:

```bash
OMNIPUS_WORKER_CLI="$WORKER_LAUNCHER" python3 -I /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/recovery-20261002/logs/steerable_worker_current_turn.py "$ROLE" "$WORKTREE" "$FOLLOW_UP" "$NEW_OUTPUT_BASE" "$SESSION_ID"
```

Arm a watcher on the new stream immediately. Resume the worker in its own session with
its context intact, rather than replacing it with a fresh worker.

On a quota/balance failure, save the provider receipt and follow the personal-layer
next-tier rule immediately. Carry the original session id when supported, plus the
latest committed SHA, ledger state, evidence and next unfinished step. If the next
provider cannot resume that session id, restore that saved state explicitly; do not
restart the task from zero or discard work. A capability routing exception needs the
founder, not a silently skipped healthy ladder tier.

On an explicit stop, use the controller's worker-owned process-group stop and verify
that the parent and children are gone before releasing ownership. Do not apply this
local-worker rule to shared Fly machines; their stop safety is documented separately
in `deploy/ci-worker/CLAUDE.md`.
