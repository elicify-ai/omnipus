# Dispatch and watch — keep workers observable and resumable

Last reviewed: 2026-10-06

Mechanics for the planning loop's "Dispatch and watch" step. Launcher selection stays
in the personal rules, outside this repo; set `OMNIPUS_WORKER_CLI` from those rules
explicitly rather than relying on the controller's default.

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
worker for steering and resuming. A watcher observes; it does not replace squad-lead's
foreground wait in its goal loop.

## Resume the same session

If unfinished work stops or disconnects, confirm the old process has exited, then run
the controller with the same role and worktree, a follow-up brief and a fresh output
base; the **fifth argument is the original session id**:

```bash
OMNIPUS_WORKER_CLI="$WORKER_LAUNCHER" python3 -I /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/recovery-20261002/logs/steerable_worker_current_turn.py "$ROLE" "$WORKTREE" "$FOLLOW_UP" "$NEW_OUTPUT_BASE" "$SESSION_ID"
```

Arm a watcher on the new stream immediately. Resume the worker in its own session with
its context intact, rather than replacing it with a fresh worker.
