# ADR-050: Per-Task Run History (an additive execution-record layer)

**Status:** Proposed (grilled — 4 adversarial review passes, 2026-07-19; pivoted after)
**Date:** 2026-07-19
**Deciders:** architect (+ backend-lead, frontend-lead, qa-lead for implementation/review)
**Amends:** ADR-049 (RRULE recurrence model) — occurrences gain per-run state; nothing in
ADR-049's scheduling or expansion model changes.

## Context

ADR-049 shipped recurring tasks as calendar events: a recurring series is **one** `Task`
row carrying an RRULE, and the calendar renders each occurrence by projecting fire times
from the rule (`GET /api/v1/tasks/occurrences` returns `occurrences_ms[]` — pure
timestamps). Every occurrence chip is painted with the parent's single `task.status`
(`src/lib/calendar/eventMapping.ts` — `STATUS_STYLE[task.status]`).

Two gaps, one root cause:

1. **Per-occurrence state is impossible.** When occurrence #1 fires and completes, the
   single series row flips to `done` with that one result, so **every** projected chip —
   past *and* future — shows Done with the same result. Operator UAT, 2026-07-19: *"the
   status and result must be per instance."*
2. **Normal tasks have no run history.** A `once` task that fails can be re-run, but the
   re-run **overwrites** the prior attempt's status/result/session — no record it failed,
   no way to open the failed run's chat. Operator: *"save all runs linked to a task —
   that would also enhance normal tasks; if I have a one-time task and want to re-run it
   after a failure."*

Root cause: **execution state lives on the Task, which holds exactly one execution's
worth of it.**

## Pivot recorded (why this ADR looks different from its first draft)

The first draft demoted `Task.status` to a **series lifecycle** (`active/paused/exhausted`)
and deleted ADR-049's recurring machinery. Four parallel adversarial reviews (model
coherence, `Task.status` blast-radius, calendar join + retention, executor/scheduler seam)
showed that demotion **severs a load-bearing invariant** — `Task.status` reaching a
terminal value is how ~13 code paths detect "this task's run just finished":
channel notify-back (`notifySourceChannel`), blocked_by dependency satisfaction
(`recomputeBlockedStateLocked`, `ExecuteTask` dispatch guard), parent follow-up
(`notifyParentIfAllSiblingsDone`), rollup liveness (`computeRollup`), the agent-completion
detector (`finishTaskRun`'s `IsTerminal` check), the exactly-once dispatch CAS
(`ClaimForRun`), and the overlap guard (`SpawnReset`'s `ErrAlreadyRunning`). Demoting the
field would break all of them for recurring tasks, plus force a closed-enum wire-contract
change across 3 schemas + 12 frontend files.

**Decision after grilling: do not touch `Task.status` semantics at all.** Runs become a
**purely additive** record layer. The Task-level status/result/session_id fields keep their
exact current behaviour (cycle next→in_progress→done per fire, driven by the *unchanged*
SpawnReset/ClaimForRun/finishTaskRun path); ADR-049's recurring machinery **stays**. The
only behavioural change is that the **calendar and task-detail read per-execution runs**
instead of the single Task mirror. This is the minimal correct design.

## Decision

**Record every task execution as an append-only `TaskRun`, additively — without changing
how `Task.status`/`result`/`session_id` behave.** A Task accretes 0..N runs; the calendar
and task-detail surfaces read runs for per-execution truth; everything else is unchanged.

### RD1 — `TaskRun`: an additive, append-only execution record

```
run_id        ULID
task_id       owning task
occurrence_ms int64?   — the scheduled RRULE instant this run realizes (the calendar join
                         key); null for an ad-hoc / once / manual run
status        in_progress | done | failed        (see RD10 — no canceled in v1)
result        string
session_id    the chat session this run produced
kind          scheduled | manual
started_at    RFC3339
ended_at      RFC3339?
```

A run is **event-sourced**: an *open* record is appended when a dispatch begins and a
*close* record (same `run_id`, terminal status + result + ended_at) is appended when it
finishes. Readers fold by `run_id`, last record wins. No in-place mutation, no whole-file
rewrite (a MINUTELY series over a 90-day window is ~130k rows — rewriting twice per fire is
untenable; this was HIGH-#7).

### RD2 — `Task.status` / `result` / `session_id` are UNCHANGED

They keep their current semantics, current writers, and current cycle. No new enum values
(`active/paused/exhausted` are **rejected** — blast-radius). The series row still goes
next→in_progress→done per fire; every terminal-status hook (channel notify, blocked_by,
follow-up, rollup, completion detection) fires exactly as today. The Task fields remain the
mirror of the **most recent** execution — which is exactly what they are now. Runs are an
*additional* record, not a replacement.

Consequence: the *original* per-occurrence bug is fixed **not** by changing `Task.status`
but by the **calendar reading runs** (RD6). `task.status` flipping to `done` after
occurrence #1 no longer paints the other occurrences, because the chips no longer read it.

### RD3 — the series is one Task; each fire *additionally* records a run keyed by `occurrence_ms`

ADR-049 unchanged: one Task per series, occurrences projected from the RRULE. The scheduler
fire path is unchanged **except** it now also opens/closes a run stamped with the fire's
`occurrence_ms`. That instant is already available at the fire site (`job.Schedule.AtMS`,
set by `triggerToCronSchedule`'s RRULE branch) but is not threaded down today; the dispatch
signature widens end-to-end to carry `occurrenceMs *int64` (HIGH-#H2). ADR-049's
"series survives a per-run terminal status" machinery is **kept** — it drives the mirror
cycle and the re-arm.

### RD4 — storage: `tasks/<id>/runs/<YYYY-MM-DD>.jsonl`, append-only, day-partitioned

Day-partitioned exactly like sessions (`sessions/<id>/<YYYY-MM-DD>.jsonl`) so the **existing
whole-file-mtime retention sweep applies unmodified** (BLOCK on retention otherwise —
the single-file design broke both the sweep and the floor-of-one, calendar-grill H5).
Reuses `fileutil.WriteFileAtomic` + advisory-flock + the sharded-mutex pool. A run's day
file is assigned by its **`started_at`** (an open + its later close may land in different
day files; folding is by `run_id` across the read window, so this is fine). No SQLite, no
new package, no new infra (Constraints #1, #2).

### RD5 — the executor opens a run at dispatch, closes it in the completion handler

One seam, reusing the existing completion plumbing:

- **Open** at the moment a dispatch is claimed — right where `ClaimForRun` /
  `SpawnReset` already gate exactly-once dispatch (the run-open is idempotent per
  `(task_id, occurrence_ms)`; see RD7). Mint the run's `session_id`; stamp `occurrence_ms`
  for a scheduled recurring fire, else null.
- **Close** in `finishTaskRun` / `completeTaskWithResult` — the single functions that
  already observe completion for **both** the marker path *and* the `update_task`-tool path.
  Because the close is driven from there, **the `update_task` / `update_task_in_workspace`
  agent tools need no change** (BLOCK-#3/#5 dissolve): the agent still writes `Task.status`
  as its completion signal, and the executor, detecting completion exactly as today, closes
  the open run with that outcome. A run row is therefore produced for the majority
  completion path, not only for the two dispatch entry points.

The mirror refreshes at **both** open (status→in_progress, new session_id) and close
(status→done/failed, result) — this is already what the current path does; it is stated
explicitly so the launch-idempotency guard keeps reading a consistent mirror (HIGH-#4).

### RD6 — the calendar joins occurrences to runs by `occurrence_ms`

`GET /api/v1/tasks/occurrences` stays the time-grid authority (still projects from the
RRULE). It is enriched with an **additive** per-occurrence run overlay, scoped strictly to
the individual `occurrences_ms[]` instants (never bucket members — so it inherits the
existing ≤500/task instant cap with no new cap; HIGH-#H4):

```
occurrence_runs: [{ occurrence_ms, status, run_id, session_id, has_result }]   (in-range, matched by occurrence_ms)
```

Per-chip display rule (the four states, resolving calendar-grill B1/M6 + blast #8):

| Occurrence | Rendering |
|---|---|
| **has a run** (individual instant) | that **run's** status badge/color + icon; Open-in-Chat if `session_id`; result fetched per-run (RD8) |
| **aggregated day bucket** (>3/day) | never one status — new additive `DayBucket.run_counts {scheduled,in_progress,done,failed}` (computed by scanning the day's runs, **not** by enumerating RRULE members); client shows a **worst-wins** glyph (failed > in_progress > done > scheduled) + tooltip breakdown |
| **no run, `occurrence_ms >= now`** | **"Scheduled"** (today's Clock chip) |
| **no run, `occurrence_ms < now`** | **"No record"** — a distinct muted glyph (NOT Clock, NOT "Scheduled"); tooltip: "run history unavailable — retention expired or the schedule changed since". Never rendered as a future fire. |

The "No record" state is required because a pruned-past run (RD9) and a schedule-edit that
re-anchored the rule (BLOCK-B2 below) both leave the same server signal — absence — which
must not read as "will run".

### RD7 — Run-now is per-occurrence (recurring) and per-attempt (normal), via an idempotent run-open

A new store primitive `OpenRun(taskID, occurrenceMs *int64) (*TaskRun, created bool, err)`
runs under the per-task `StripedLock` and **atomically creates-or-returns** the open run for
`(task_id, occurrence_ms)` — the missing per-occurrence idempotency guard (BLOCK-B2/#5). It
prevents a scheduler fire and a user Run-now from double-running the same occurrence.

- **Recurring occurrence Run-now:** `OpenRun(task, clicked_occurrence_ms)` then dispatch;
  the chip flips scheduled→in_progress→done/failed.
- **Normal / once task re-run after failure:** `OpenRun(task, nil)` opens a **new** run;
  the prior failed run is preserved, its chat still openable. (Note: `once`-task Run-now UI
  is genuinely new — normal tasks have no such affordance today; HIGH-#10.)

### RD8 — run-history surfaces (independent of occurrence projection)

- `GET /api/v1/tasks/{id}/runs` → `TaskRun[]` (retention-bounded, full result strings).
  This is the authoritative history list — **independent of whether the current trigger can
  still project a run's `occurrence_ms`** (resolves BLOCK-B2: editing a series' schedule
  re-anchors the rule, orphaning prior occurrences from projection; the history list still
  shows every run).
- `TaskDetailPanel` (Board/List) grows a **Runs** section (each run: status, ended_at,
  result, Open-in-Chat). Recurring tasks stay excluded from Board/List (ADR-049 D3); their
  runs are reached via the calendar occurrence's edit slide-over **and** the always-present
  history list in that slide-over.
- The calendar edit slide-over threads the clicked `occurrenceMs` (today it does not — the
  instant lives only in the FullCalendar event id; HIGH-#M7). `TaskRunStatusField` /
  `TaskResultField` / `OpenInChatButton` are re-pointed from `task.*` to the **resolved
  run** for that occurrence (the overlay carries `run_id`; `result` text is fetched per-run
  since the overlay only carries `has_result`). A **future** occurrence with no run shows
  only "Run now" — no status badge, no result. A **bucket** click opens a mini-list of that
  day's runs, not a single-occurrence view.

### RD9 — retention: day-partitioned sweep + keep-newest-day floor

Reuse the session-retention sweep (whole-file-by-mtime over day-partitioned files, default
90 days) — it applies to `runs/<date>.jsonl` unmodified. One addition: **always retain a
task's single newest day file** so a long-idle task never loses its last run (the floor-of-
one, which naive whole-file deletion would violate). Pruning piggybacks the existing
session-retention cadence — no new scheduler.

### RD10 — cancel and pause are out of scope; a stuck-run reaper closes abandoned runs

`canceled` and `paused` are **not** in this ADR: task cancellation does not exist anywhere
in the codebase today (`finishTaskRun` maps every error to `failed`; the per-run cancel func
is never invoked), and pause is a separate pause/resume feature. Runs are `in_progress →
{done, failed}`. A run whose gateway died mid-execution is closed to `failed` by a
**stuck-run reaper** folded onto the retention sweep (close `in_progress` runs with no
matching live execution older than N) — this also closes a currently-permanent gap where
`reconcileStuckTasks` only runs at boot (MED-#M2). Cross-references ADR-045.

### RD11 — migration: none (purely additive)

Existing tasks have zero runs and keep working via the unchanged mirror. The first fire or
Run-now after upgrade creates run #1. Existing recurring series transparently begin
recording per-occurrence runs; their pre-upgrade past occurrences have no run and render
**"No record"** (RD6), never "Scheduled". No data migration.

## Options Considered

| Option | Verdict | Why |
|--------|---------|-----|
| **A. Keep stateless projections** | Rejected | The reported defect: one run's outcome paints every occurrence. |
| **B. Materialize each occurrence as its own `once` Task** (series = generator; instances = real Tasks linked by `series_id`) | Rejected | Reuses Task machinery but **bloats the task list** with one row per fired occurrence and does **nothing** for normal-task re-run history. Solves half with more rows. |
| **C-demote. Runs + demote `Task.status` to a series lifecycle** (the first draft) | Rejected (grill) | Severs the `terminal-status ⇒ run-finished` invariant → breaks ~13 hooks for recurring tasks, forces a closed-enum wire-contract change across 3 schemas + 12 FE files, introduces a DAG dead-lock and a rollback-corruption path. |
| **C-additive. Runs as an additive layer; `Task.status` unchanged** *(chosen)* | **Accepted** | Fixes both cases (per-occurrence recurring state via the calendar reading runs; re-run history for every task) with **zero** change to status semantics and zero broken hooks. Cost: a new persisted record type + contract schema + calendar overlay + run-history UI + a per-occurrence run-open primitive. |
| **D. Audit-log-only run trail** | Rejected | The audit log is append-only forensic data with its own HMAC-chain/retention semantics; joining it to occurrences per request is the wrong tool. |

## Consequences

### Unchanged (explicitly)
`Task.status`/`result`/`session_id` semantics and every reader/writer of them; `ClaimForRun`,
`SpawnReset`, `finishTaskRun`, `completeTaskWithResult`, `validateTransition`; the
`update_task` / `update_task_in_workspace` tools; ADR-049's recurring-lifecycle machinery
(`IsRepeating`/`SeriesRetired`, the re-arm, the done→in_progress carve-out); channel
notify-back, blocked_by, follow-up, rollup; **heartbeats** (a separate `CronService`
instance — confirmed orthogonal; the spec must state "no changes to
`heartbeat_schedule.go`"). The Run-now fresh-run reset stays until superseded by RD7's
`OpenRun` in the same change.

### New / touched
`TaskRun` schema (contract-first) + `GET /tasks/{id}/runs`; the occurrence-overlay +
`DayBucket.run_counts` additive schema fields; `runs/<date>.jsonl` store (open/close/list/
fold/prune) + `OpenRun` primitive; `occurrenceMs` threaded through the dispatch signature
and the calendar event props/click handler; run-open at claim + run-close in the completion
handler; `TaskDetailPanel` Runs section; calendar slide-over run re-pointing + per-run
result fetch; the keep-newest-day retention tweak + stuck-run reaper.

### Realtime
A recurring occurrence's `queued→in_progress→done` transition does not move `Task.status`
between distinct values the calendar reads, so the existing `TaskStatusChangedFrame` won't
drive chip updates. Add an **additive** run-status WS frame `{task_id, run_id, occurrence_ms,
status}`, or (fallback) document occurrence status as refetch-on-`TaskStatusChanged`-driven.
Decided in the spec (HIGH-#6).

### DST join precision (accepted)
`NextOccurrenceAfter` (scheduler) and `ExpandRRULE` (endpoint) can, inside a spring-forward
gap for a sub-hourly rule, name different valid minutes for the same nominal occurrence, so
an exact `occurrence_ms` join can miss for that one transition (self-healing next fire).
**Accepted** as a documented single-transition edge; no tolerance-window join in v1
(HIGH-#H3).

### Rollback
Purely additive: a pre-runs binary ignores `runs/` and reads the unchanged
`Task.status`/`result` mirror as before (recurring occurrences revert to single-status
display; no data lost). Graceful both ways.

### Single binary / no new deps
File-based JSONL only; no new runtime dependency, no CGo, no SQLite.

## Grill findings → resolution (traceability)

- **Enum break / 12-file churn / DAG dead-lock / rollback corruption / task_update
  corruption** (model-coherence #1–#5, blast #1–#4, #8–#13, AMBIG-A/B) → **dissolved** by
  RD2 (no status change; runs additive; executor closes the run so `update_task` is
  untouched).
- **SpawnReset/ClaimForRun incompatibility, per-occurrence double-run** (executor B1/B2/B3,
  blast #5/#6) → RD5 keeps them unchanged; RD7 adds the idempotent `OpenRun`.
- **`canceled` has no producer** (executor B4) → RD10 scopes cancel out; runs are
  done/failed; stuck-run reaper closes abandoned runs.
- **Storage rewrite cost, retention sweep mismatch, floor-of-one** (calendar H5, model #7)
  → RD4 event-sourced day-partitioned + RD9 keep-newest-day.
- **Day-bucket status, pruned-vs-scheduled, occurrence-scoped click/result** (calendar
  B1/M6/M7, blast #8/#17) → RD6 four-state rule + `run_counts` + RD8 threading.
- **Series schedule-edit orphans history** (calendar B2) → RD8's projection-independent
  `GET /tasks/{id}/runs`.
- **No realtime for per-run status** (model #6) → run-status WS frame (Consequences).
- **`occurrence_ms` not threaded; heartbeats orthogonal; SeriesRetired forward-compatible**
  (executor H2/SOUND) → RD3 widens the signature; heartbeats untouched.

## References

- Amends ADR-049 (RRULE recurrence model). Related: ADR-045 (orphaned-run timeout).
- Defect origin: operator UAT, 2026-07-19 ("status/result must be per instance"; "save all
  runs linked to a task").
- Grill: 4 adversarial passes, 2026-07-19 (model coherence, status blast-radius, calendar
  join + retention, executor/scheduler seam).
- Spec (to be written): `docs/internal/specs/task-run-history-spec.md`.
- Reused infra: session day-partitioned JSONL + retention sweep, `fileutil.WriteFileAtomic`,
  `StripedLock`/sharded-mutex pool.
