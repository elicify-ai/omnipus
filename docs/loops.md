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
