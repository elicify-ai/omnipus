# Chat loops

Use `/loop` when you want the agent in this chat to return to an instruction on a timetable. Choose a fixed interval, or let the agent choose when to check in again. A loop is scheduled work; a goal adds a definition of done and independent judging.

## Start, check or stop a loop

Send the command yourself in the web app or a connected chat platform, using the chat you want to schedule. The loop scheduler must be available.

| Command | What it does |
|---|---|
| `/loop every 5m check the build and report anything new` | Run the instruction every five minutes. |
| `/loop check the build and choose when to check again` | Start a self-paced loop: the agent chooses the delay after each run. |
| `/loop` | Show this chat's loop, its cadence and its run count. |
| `/loop stop` | Remove this chat's loop schedule and clear its loop state. |

In the web app, the loop footer disappears when the chat receives the loop's stopped status; you do not need to reload the page.

Intervals must be positive durations, for example `30s`, `5m`, `1h30m` or `1d`. Starting another loop in the same chat replaces its existing loop.

## Self-paced check-ins

A self-paced loop schedules its first run after about one second. After each run, Omnipus reads the agent's reply for a delay marker:

```text
LOOP_NEXT: 10m — check again once the build has had time to finish
```

The delay is a positive whole number followed by `s` (seconds), `m` (minutes), `h` (hours) or `d` (days). The explanation after the dash is optional. You do not need to type this marker yourself: the loop asks the agent to include it in its reply.

If the reply has no valid marker, Omnipus uses a **15-minute** delay instead of dropping the loop.

## Run limit

A loop stops after **100 runs by default**. An operator can configure a different positive run limit; a running loop keeps the limit it started with. Use bare `/loop` to see its current count and saved limit.

A run counts after it executes, even if that run returns an error. The limit is not a promise of 100 successful answers. Use `/loop stop` to stop the schedule sooner.

A failed run is recorded and the loop continues on its next step. It is not retried on a separate backoff timer. A loop whose owner is no longer a registered agent, or whose owner is a worker, does not start a turn. The gateway log says the owner is unavailable. Workers run only when another agent delegates to them, never on a timer.

## After a restart

Chat loops share the one timer with heartbeats and scheduled tasks (see [calendar](calendar.md)). Only a real gateway restart recomputes them. Saving settings does not stop the timer, drop a step, or apply a new retry gap.

Saved "next run" times and "still running" flags from the previous process are hints. Startup works them out again before anything is allowed to fire.

| What was saved | What startup does |
|---|---|
| A self-paced loop whose next check is still in the future | That one step is kept. |
| A self-paced loop whose next check is already overdue, or whose step is missing | One new step, scheduled for startup plus the delay the loop last saved. If no positive delay was saved, that delay is about one second (the same delay a new self-paced loop uses for its first run). Missed checks are not replayed. After that step runs, the agent chooses the next delay as usual, so the chain continues. |
| An interval loop that still has its repeating job | That job is kept. Startup sets its next time to one interval after the gateway starts, so a check that was due while Omnipus was down is not replayed. |
| An interval loop whose job is missing | One fresh repeating job at the saved interval. Its first run is one interval after startup. |
| A loop that has already reached its run limit | Stopped. Nothing is armed. |
| No existing step can be kept, and the saved state (owner, mode, interval) cannot create one | Stopped. Keeping an existing step is separate from checking whether its owner can still run it. |
| Loop jobs whose chat no longer has loop state | Removed. They do not run. |

An active loop from an older install, whose job lived in a separate file, continues the same way: that file is not read, and startup rebuilds one next step from the chat's saved loop state.

## Replacing or stopping a loop

Starting another loop in the same chat, or `/loop stop`, is meant to leave only the selected next step. **Pending a production fix:** in the current build, a turn that finishes at the same moment can leave a competing step, and if saving the stopped state fails, the command can still say "Loop stopped." while the saved loop stays active and may restart at the next boot. Do not rely on Stop or replacement being instant and permanent until that fix is verified.

## If recovery cannot be saved

During startup recovery, the gateway must record which step is next, or record that a finished loop stopped. If that write fails, the gateway does not finish starting. The error names the chat (`error recovering /loop schedules`). No heartbeat, loop or scheduled task fires in that process. The next start tries again from what is still saved on disk. During startup recovery, nothing is marked stopped unless that stopped state was actually saved.
