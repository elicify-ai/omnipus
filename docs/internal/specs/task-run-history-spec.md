# Spec: Per-Task Run History (additive execution-record layer)

**Status:** Draft Rev 1 · **Date:** 2026-07-19 · **Implements:** ADR-050 (grilled)
**Branch:** `feat/calendar-scheduler-ui` (folded into the calendar recurrence work)
**Amended:** 2026-07-20 — RD10 reaper retired, RD7/RD8 future Run-now retired; see
§3.4/§3.5/§4.3/§6/§8 and BDD scenario 4.

Derived from ADR-050 after 4 adversarial grill passes. The governing decision: **runs are a
purely additive record layer; `Task.status`/`result`/`session_id` semantics are unchanged;
only the calendar and task-detail surfaces read per-execution runs.** This spec pins the
wire contracts, the exact code seams, the display rules, retention, and the test matrix.

---

## 1. Scope

**In:** a `TaskRun` record per execution (append-only, day-partitioned); the calendar reads
per-occurrence runs; task-detail gains a run-history list; Run-now is per-occurrence
(recurring) and per-attempt (normal, re-run after failure); age-based retention with a
floor; a stuck-run reaper REMOVED (2026-07-20 — see §3.5; a liveness-aware reaper is
deferred, §8).

**Out (explicit):** task **cancellation** (no producer exists today — runs are
`in_progress → {done, failed}`; a `canceled` value is not introduced); series **pause/
resume**; any new `Task.status` enum value (`active`/`paused`/`exhausted` are **rejected**);
any change to `Task.status`/`result`/`session_id` behaviour, to the `update_task`
tools, to ADR-049's recurring machinery, or to **heartbeats** (a separate `CronService` —
`pkg/gateway/heartbeat_schedule.go` is **not touched**).

---

## 2. Data model

### 2.1 `TaskRun` (new wire type — contract-first)

`contracts/components/schemas/TaskRun.yaml`:

| field | type | notes |
|---|---|---|
| `run_id` | string (ULID) | stable id across the open+close records |
| `task_id` | string | owner |
| `occurrence_ms` | integer(int64), nullable | the scheduled RRULE instant this run realizes; **null** for ad-hoc/once/manual runs. The calendar join key. |
| `status` | enum `in_progress` \| `done` \| `failed` | **no `canceled`/`queued` in v1** |
| `result` | string | terminal-run output; empty until close |
| `session_id` | string | the chat session this run produced |
| `kind` | enum `scheduled` \| `manual` | how it started |
| `started_at` | string (date-time) | open time (also the day-partition key) |
| `ended_at` | string (date-time), nullable | close time; null while in_progress |

`additionalProperties: false`. Generated Go/TS + Zod via `make gen-contracts`.

### 2.2 On-disk shape

`~/.omnipus/tasks/<task_id>/runs/<YYYY-MM-DD>.jsonl` — **append-only, event-sourced**:

- **open record**: full `TaskRun` with `status:in_progress`, `ended_at:null`, appended when
  a dispatch is claimed.
- **close record**: same `run_id`, `status:done|failed`, `result`, `ended_at`, appended at
  completion.

Readers **fold by `run_id`, last record wins**. Day file chosen by `started_at`'s
`YYYY-MM-DD` (UTC). Reuse `fileutil.WriteFileAtomic` (append via read-modify-write is
acceptable here — files are day-bounded) + advisory flock + the `StripedLock` per-task pool.
**No whole-file rewrite per fire, no in-place line mutation.** (ADR-050 RD4; resolves the
130k-row rewrite cost, model-grill #7.)

### 2.3 The mirror is unchanged

`Task.status`/`result`/`session_id` keep their exact current writers and cycle
(next→in_progress→done per fire). This spec adds run records **alongside** those writes; it
does not alter them. No new `Task` field, no enum change, no `TaskRollupStatus` change.

---

## 3. Backend seams (exact functions)

### 3.1 New store API (`pkg/task/run_store.go`, new file)

```go
// OpenRun atomically creates-or-returns the open (ended_at==nil) run for
// (taskID, occurrenceMs) under the per-task StripedLock. Idempotent: a second
// caller for the same key returns the existing open run with created=false.
// occurrenceMs==nil => an ad-hoc/manual run keyed only by "an open run exists".
func (s *Store) OpenRun(taskID string, occurrenceMs *int64, kind RunKind, sessionID string) (run *TaskRun, created bool, err error)

// CloseRun appends the terminal record for run_id (status done|failed + result + ended_at).
func (s *Store) CloseRun(taskID, runID string, status Status, result string) error

// ListRuns folds all runs for a task within the retention window (newest first).
func (s *Store) ListRuns(taskID string) ([]TaskRun, error)

// RunsInRange folds runs for a task whose occurrence_ms ∈ [fromMs, toMs) (calendar overlay).
func (s *Store) RunsInRange(taskID string, fromMs, toMs int64) ([]TaskRun, error)

// PruneRuns removes day files older than cutoff, ALWAYS retaining the newest day file
// (floor-of-one) and any day file holding an in_progress run, regardless of age.
// No reaper: an in_progress run is closed only by its own execution or boot
// reconciliation (§3.5) — never by PruneRuns.
func (s *Store) PruneRuns(taskID string, cutoff time.Time) error
```

`OpenRun` is the missing per-occurrence idempotency guard (ADR-050 RD7; executor-grill
B2/B3, blast #5). It runs under the same lock domain as `ClaimForRun`.

### 3.2 Run-open — at the dispatch claim

The dispatch signature widens end-to-end to carry the occurrence instant (available at the
fire site but not threaded today — executor-grill H2):

- `pkg/agent/task_trigger.go`: `dispatch func(ctx, taskID string, occurrenceMs *int64) error`;
  `RunScheduled` passes `job.Schedule.AtMS` (set by `triggerToCronSchedule`'s RRULE branch).
- `pkg/agent/task_executor.go`: `SpawnTriggeredRun`, `ExecuteTask` gain `occurrenceMs *int64`.

Immediately after the existing `ClaimForRun` (scheduled/`next` path) **or** `SpawnReset`
(recurring re-fire) succeeds — i.e. once exactly-once dispatch is guaranteed — call
`OpenRun(taskID, occurrenceMs, kindFromPath, newSessionID)`. **Do not change** `ClaimForRun`
/ `SpawnReset` themselves; the run-open is an additional call inside the same claimed
critical path. The run's `session_id` is the one the dispatch already mints.

### 3.3 Run-close — in the completion handler

In `finishTaskRun` / `completeTaskWithResult` (`pkg/agent/task_executor.go`) — the single
functions that already observe completion for **both** the marker path and the `update_task`
-tool path — after the existing mirror write, call `CloseRun(taskID, runID, status, result)`
with the same outcome. Because the close is driven here, **`update_task` /
`update_task_in_workspace` need no change** (blast #8/#9 dissolve). Thread the active
`run_id` through the same execution context that already carries the task/session.

### 3.4 Run-now (`pkg/gateway/rest_tasks.go`)

- **Recurring occurrence Run-now** (new): a request carrying `{occurrence_ms}` →
  `OpenRun(task, &occurrence_ms, manual, …)` then dispatch. Idempotent vs a concurrent
  scheduler fire for the same instant.
- **Normal/once re-run after failure**: `OpenRun(task, nil, manual, …)` opens a fresh run;
  the prior failed run is preserved. The ADR-049 fresh-run reset is superseded by this in
  the same change (do not keep both paths).

**Amended 2026-07-20 (operator decision, D1).** Run-now is rejected for a future occurrence:
`handleTaskRunNow` (`pkg/gateway/rest_task_runs.go`) returns 400 when `occurrence_ms` is
present and `occurrence_ms > time.Now().UnixMilli()` ("cannot Run-now a future occurrence;
it will run at its scheduled time"). API-level gate, not just UI. Task-level Run-now (no
`occurrence_ms`) and a past/current occurrence (`occurrence_ms <= now`) are unaffected.

### 3.5 Retention (`pkg/session/retention_sweep.go` neighbour)

Runs join the existing retention sweep: for each task, `PruneRuns(cutoff)` (signature
dropped `staleAfter` — no reaper). Day-partitioned mtime deletion applies unmodified
**except** (a) the newest day file per task is always retained (floor-of-one, ADR-050 RD9),
and (b) a day file holding any `in_progress` run is never deleted regardless of age (a run
stays `in_progress` until its own execution closes it; operator decision 2026-07-20).

**No stuck-run reaper.** An `in_progress` run is closed only by (a) its own execution
completing, or (b) boot reconciliation on a crashed gateway (`reconcileStuckTasks` →
`reconcileStuckTaskRuns`, `pkg/gateway/rest_tasks.go`) best-effort closing a reset task's
open runs to `failed`, bounding the orphaned-run gap to "next restart". No new goroutine. A
liveness-aware reaper is a deferred future feature (§8).

### 3.6 New REST endpoint

`GET /api/v1/tasks/{id}/runs` → `TaskRun[]` (retention-bounded, newest first, full result
strings). Authoritative history **independent of occurrence projection** — a series whose
schedule was edited (re-anchored rule) still lists every past run (resolves calendar-grill
B2). `withAuth`, `taskReadLimiter`.

### 3.7 Occurrence overlay (`pkg/gateway/task_occurrences.go`)

`buildOneOccurrenceSet` enriches each set with an additive overlay, scoped **strictly to the
individual `occurrences_ms[]` instants** (never bucket members — inherits the ≤500/task cap,
no new cap; HIGH-#H4):

```
occurrence_runs: [{ occurrence_ms, status, run_id, session_id, has_result }]
```

sourced via `RunsInRange(taskID, setFromMs, setToMs)` matched on `occurrence_ms`. For
aggregated `DayBucket`s add `run_counts: {scheduled, in_progress, done, failed}` computed by
scanning that day's runs (not by enumerating RRULE members). Both are additive schema fields
on `TaskOccurrenceSet` / `DayBucket` (`additionalProperties:false` — regenerate).

### 3.8 Realtime (additive WS frame)

New AsyncAPI frame `TaskRunStatusFrame {task_id, run_id, occurrence_ms, status}` emitted at
run open and close, so the calendar chip updates live (the existing `TaskStatusChangedFrame`
won't fire per-occurrence since `Task.status` transitions are not what the chips read —
model-grill #6). SPA edge validates via generated Zod; drop+count on failure.

---

## 4. Frontend

### 4.1 Occurrence → run resolution (thread `occurrenceMs`)

Today the clicked instant lives only in the FullCalendar event id (`task:<id>:occurrence:<ms>`)
and is dropped by the click handler (HIGH-#M7). Add `occurrenceMs` to the
`task-occurrence` / `task-occurrence-agg` extendedProps union
(`src/components/calendar/types.ts`), capture it in `CalendarScreen.handleEventClick`, and
thread `selectedOccurrenceMs` into `CalendarEventSlideOver`.

### 4.2 Chip display rule (four states — ADR-050 RD6)

`eventMapping.ts` stops using `STATUS_STYLE[task.status]` for occurrence chips and instead:

| case | render |
|---|---|
| individual instant **with** a run | badge/color from **`run.status`**; Open-in-Chat if `session_id` |
| aggregated bucket | **worst-wins** glyph over `run_counts` (failed > in_progress > done > scheduled) + tooltip breakdown |
| no run, `occurrence_ms >= now` | **"Scheduled"** (Clock) |
| no run, `occurrence_ms < now` | **"No record"** — distinct muted glyph, tooltip explains retention/schedule-change; **not** a future fire |

### 4.3 Slide-over run sections (re-point from `task.*` to the run)

`TaskRunStatusField` / `TaskResultField` / `OpenInChatButton`, when rendered for a calendar
occurrence, read the **resolved run** (from the overlay's `run_id`; fetch full `result` per
run since the overlay carries only `has_result`). States:

- occurrence **with** a run → its status badge, result, Open-in-Chat; **Run now** re-opens a
  run for that occurrence.
- **Amended 2026-07-20.** A **future** occurrence (`occurrence_ms > now`), no run → shows
  only its "Scheduled" status text — **no badge, no Run-now button**. Rationale: firing
  early doesn't cancel the natural scheduled fire (the scheduler is RRULE/`Task.status`-
  driven, unaware of `TaskRun`s), so it would double-execute. Run-now for an occurrence is
  offered only when `occurrence_ms <= now`. `TaskRunStatusField` takes an injected `now`
  prop for deterministic tests. Enforced server-side too (§3.4).
- **bucket** click → a mini-list of that day's runs (not a single-occurrence view).
- Always show the **run-history list** (`GET /tasks/{id}/runs`) so history survives a
  schedule edit (calendar-grill B2).

### 4.4 Task detail run history (Board/List)

`TaskDetailPanel` gains a **Runs** section (each run: status, ended_at, result,
Open-in-Chat). Enables the normal-task re-run-with-history flow. Recurring tasks stay
excluded from Board/List (ADR-049 D3).

---

## 5. BDD scenarios (acceptance)

1. **Per-occurrence status.** Given a weekly series whose Jul-20 occurrence ran `done` and
   Jul-27 ran `failed`, When I view the month, Then Jul-20 shows Done (green), Jul-27 shows
   Failed (red), Aug-3 shows Scheduled — three distinct states, not all Done.
2. **Per-occurrence result.** Given the above, When I open the Jul-27 occurrence, Then I see
   the Jul-27 run's failure result and its Open-in-Chat opens the Jul-27 session — not
   Jul-20's.
3. **Re-run a failed once-task.** Given a `once` task that failed, When I Run-now, Then a new
   run starts, the prior failed run remains in the Runs list, and both chats are openable.
4. **Run-now is unavailable for a future occurrence.** Given a future scheduled occurrence
   with no run, When I open its slide-over, Then it shows only its "Scheduled" status — no
   badge, no result, no Run-now button — and a direct `POST /tasks/{id}/runs {occurrence_ms}`
   for that future instant is rejected 400. (Amended 2026-07-20, D1 — supersedes the original
   "Run-now a future occurrence materializes a run ahead of schedule", which is retired: it
   would double-execute.)
5. **Double-fire safety.** Given a scheduler fire and a Run-now for the same occurrence race,
   Then exactly one run is opened (OpenRun idempotency), not two.
6. **Schedule edit preserves history.** Given a daily series with 10 days of runs, When I
   change its time, Then the 10 runs remain in the Runs list (even though the re-anchored
   rule no longer projects their occurrence_ms).
7. **Pruned-past ≠ scheduled.** Given a past occurrence whose run was pruned, When I view it,
   Then it shows "No record", visually distinct from a future "Scheduled".
8. **Retention floor.** Given a task idle beyond the retention window, Then its single most
   recent run is still retained and listed.
9. **Bucket worst-wins.** Given a day with 12 done + 2 failed + 26 scheduled occurrences,
   Then the aggregated chip shows the failed glyph + a "12 done · 2 failed · 26 scheduled"
   tooltip.
10. **Mirror + hooks unchanged.** Given a recurring reminder delegated from chat, When an
    occurrence completes, Then the source channel still receives the result (the terminal-
    status hook still fires — proving `Task.status` behaviour is intact).

## 6. Test matrix (TDD — deterministic, no LLM)

- `pkg/task`: `OpenRun` idempotency (same key → created=false); `CloseRun` fold; `ListRuns`/
  `RunsInRange` fold-last-wins across day files; day-partition assignment; `PruneRuns` age +
  floor-of-one + open-run-day-file skip (no reaper — 2026-07-20); concurrency (parallel
  OpenRun same key → one run).
- `pkg/agent`: dispatch threads occurrence_ms; run opened at claim, closed at completion for
  both marker and `update_task` paths; mirror still cycles (regression: terminal-status hooks
  still fire); scheduled vs manual `kind`.
- `pkg/gateway`: `GET /tasks/{id}/runs`; occurrence overlay join by occurrence_ms; bucket
  `run_counts`; overlay respects the 500 cap; Run-now-occurrence opens a run; contract_test
  (TaskRun JSON schema-valid).
- Frontend (vitest): four-state chip rule (with-run / scheduled / no-record / bucket);
  occurrenceMs threading; slide-over run re-pointing incl. future-occurrence "Scheduled, no
  Run-now" (2026-07-20 — supersedes "Run now only"); Runs list; worst-wins glyph.
- Contract: `make verify-contracts` after `TaskRun` + overlay + WS frame regen.

## 7. Traceability

Each RD (ADR-050) → §: RD1→§2, RD2→§2.3, RD3→§3.2, RD4→§2.2, RD5→§3.2/3.3, RD6→§3.7/4.2,
RD7→§3.1/3.4, RD8→§3.6/4.3/4.4, RD9→§3.5, RD10→§3.5/§1-out, RD11→migration-none. Grill
findings → resolution table in ADR-050 §"Grill findings → resolution".

## 8. Out-of-scope / accepted limitations

- Cancel, pause/resume: separate features.
- DST spring-forward sub-hourly join: exact `occurrence_ms` match may miss for one
  transition, self-heals next fire — accepted, no tolerance window (HIGH-#H3).
- `blocked_by` a recurring task: behaviour **unchanged** from today (the mirror still cycles,
  so today's flaky transient-unblock is neither fixed nor worsened here).
- **(2026-07-20)** The stuck-run reaper is removed; a liveness-aware reaper is a deferred
  future feature.
- **(2026-07-20)** `RunsInRange`'s "future Run-now lands in an old day file" edge case is now
  moot since Run-now is rejected for any future `occurrence_ms`.
