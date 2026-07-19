# Feature Specification: Calendar Recurrence Redesign (Recurring Tasks are Calendar Events)

**Created**: 2026-07-19
**Status**: Draft (Revision 3)
**Branch**: `feat/calendar-scheduler-ui`
**Input**: Three operator-recorded work items (2026-07-19): (1) *Calendar Polish / Bug / P1* — scheduled tasks expect a raw cron expression for recurring events; replace with an Outlook-style recurring-meeting UI; (2) *Calendar Task Split / Enhancement / P1* — recurring tasks are created and managed only in the calendar, filtered out of Board and List, with a calendar-specific slide-over instead of the generic task form; (3) *Calendar Polish / Enhancement / P2* — calendar gets filters, at minimum by agent.

**Revision 2 (2026-07-19)**: addresses all findings of grill round 1 (`calendar-recurrence-redesign-spec-review.md`, verdict BLOCK): CRIT-001 (bounded-window min-gap validation), CRIT-002 (bucketed occurrences response + truncation UX), CRIT-003 (re-arm on all exit paths, no retry-backoff, recovery sweep), MAJ-001–014, MIN-001–007, OBS-001–004. No operator decision (D1–D7) was re-opened.

**Revision 6 (2026-07-19)**: operator directive (D10): **there is no Command Center surface** — verified: no such route exists (`/tasks` and `/automations` are redirect stubs into the workspace Board/Calendar); `TaskDetailPanel.tsx` is simply the generic Board/List detail panel living in the legacy-named `src/components/command-center/` folder. All "Command Center" language replaced; FR-023 rescoped to a defensive guard on the generic detail panel; the panel moves to `src/components/workspaces/` and the `command-center/` folder is deleted entirely in PR 1.

**Revision 5 (2026-07-19)**: applies two **operator directives**: (D8) **no migration / no backward compatibility for old recurring tasks** — deletes the entire reverse-mapping/conversion machinery (US-5 rewritten: legacy tasks keep firing + rendering, but editing = set a fresh rule; `server_tz` wire exposure dropped; tests 16/21 simplified); (D9) the **dead Schedules SPA components are deleted** (`SchedulesList.tsx`/`ScheduleFormSheet.tsx`/`cronUtils.ts` — verified unreachable, the last raw-cron UI anywhere); confirms: no cron entry exists in any UI surface after this feature.

**Revision 4 (2026-07-19)**: addresses all findings of grill round 3 (`calendar-recurrence-redesign-spec-review-round3.md`, verdict REVISE): in-flight-aware sweep predicate + replace-by-task re-arm (MAJ-301), endpoint task-selection predicate (MAJ-302), arithmetic derivation made MUST + consistent budget rows (MAJ-303), FR-024 scoped to already-RRULE triggers (MAJ-304), `every_ms` mappable set scoped to whole-minute multiples with a generalized fallback (MAJ-305), aged-DTSTART fast-forward + COUNT bound (MAJ-306), plus MIN-301–306 and OBS-301–303. No operator decision (D1–D7) was re-opened.

**Revision 3 (2026-07-19)**: addresses all findings of grill round 2 (`calendar-recurrence-redesign-spec-review-round2.md`, verdict REVISE): concrete `taskReadLimiter` (MAJ-201), viewer-zone bucketing via `tz` query param (MAJ-202), per-task iteration budget (MAJ-203), concrete sweep ticker + "armed" predicate (MAJ-204), spring-forward pinned to Go normalization 03:30 (MAJ-205), mutex-atomic generation guard (MAJ-206), edit-mode anchor semantics FR-024 (MAJ-207), client-side cross-zone save-payload test (MAJ-208), plus MIN-201–209 and OBS-201–204. No operator decision (D1–D7) was re-opened.

All headline decisions were made interactively with the operator on 2026-07-19 — see **Clarifications**. Companion artifact required before implementation of Slice 2: **an ADR ratifying RRULE adoption** (next after ADR-046); the ADR must include the input-bounds policy (FR-006) and the downgrade statement (Operations & Rollback).

---

## Decisions Locked (operator-confirmed)

| # | Decision |
|---|----------|
| D1 | Recurrence editor has **full power including "every 2 weeks"** → adopt **RRULE (RFC 5545)** as the recurrence model for task triggers. Cron remains valid for existing tasks (legacy, read + keep-working; no bulk migration). |
| D2 | Calendar renders **every occurrence** of a recurring task in the visible range (not just the next run). |
| D3 | Recurring tasks (`trigger.type ∈ {every, recurring}`) are **calendar-only**: hidden from workspace Board and List; created/edited only via a new calendar-specific, event-style slide-over. Board/List keep the existing generic form (with the recurring trigger options removed from it). |
| D4 | Calendar filter v1 = **agent dropdown only** ("All agents" default), client-side/instant, no contract change for filtering. |
| D5 | "Monthly on the 31st" in a short month → **clamp to the last day of that month** (never skip). Picker shows an explanatory note. |
| D6 | Fast repeats must not flood Month view → aggregate. Operative rule: a task with **more than 3 occurrences on a single day** renders as **one aggregated chip** for that day in overview ranges; 3 or fewer render individually. Week/Day views show real times. (So "every 8 hours" — 3/day — renders individually; that satisfies D6's intent.) |
| D7 | Wall-clock semantics: a 09:00 task **stays at 09:00 local across DST transitions**. Recurrence is stored with an IANA timezone and expanded server-side. Full policy in **Timezone Semantics**. |
| D8 | **No migration, no backward compatibility for old recurring tasks** (operator, 2026-07-19). Legacy `cron_expr`/`every_ms` tasks keep firing (engine untouched) and render on the calendar, but the UI never translates them: opening one offers only a fresh repeat rule that overwrites to RRULE, with no fire-time-equivalence guarantees. No cron string is ever displayed or typed anywhere in the UI; cron survives under the hood only (engine, API, heartbeats). |
| D9 | The dead Schedules SPA components (`SchedulesList.tsx`, `ScheduleFormSheet.tsx`, `cronUtils.ts`) — unreachable from any route, and the last place raw cron could be typed — are **deleted** in PR 1. The `/api/v1/schedules` backend entity and the `pkg/cron` engine stay (they execute task triggers and heartbeats). |
| D10 | **There is no Command Center** (operator, 2026-07-19; verified — no route renders one; `/tasks` and `/automations` are legacy redirects to workspace Board/Calendar). `TaskDetailPanel.tsx` is the generic Board/List task detail panel; in PR 1 it moves to `src/components/workspaces/` and the legacy `src/components/command-center/` folder is deleted entirely (it then contains only D9's dead files + tests). |

---

## Existing Codebase Context

> Gathered this session via graphify + direct reads (graphify's frontend index does not cover some newer files; direct reads were the sanctioned fallback). All cited symbols/lines verified in grill round 1.

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `contracts/components/schemas/TaskTrigger.yaml` | modifies | `{type: manual\|once\|every\|recurring, config:{at_ms,every_ms,cron_expr}}`; `config` is the documented open growth surface (`additionalProperties: true`) |
| `pkg/task/task.go` (`Trigger`, `TriggerConfig`) | modifies | Go trigger types; add `Rrule`, `DtstartMs`, `Tz` |
| `pkg/task/store.go` (`ValidateTrigger`, `validateCronExpr`) | modifies | gronx validation + `minTriggerIntervalSeconds = 60` guard; returns `ErrValidation` → 400. NOTE: the cron first-two-fires technique is sound only because cron is minute-aligned — RRULE validation uses the bounded-window scan (FR-006), not first-pair |
| `pkg/agent/task_trigger.go` (`TaskTriggerScheduler`, `triggerToCronSchedule`, `RunScheduled`, `OnTaskUpserted`) | modifies | translates trigger → `pkg/cron` job; RRULE triggers re-arm as `at` jobs per occurrence (see Scheduler section for the exit-path and race rules) |
| `pkg/cron/service.go` (`computeNextRun`, `AddJobFull`, retry backoff `service.go:812-825`, `DeleteAfterRun` at `:1074`, delete-after-fire `:802`, `Reconcile`) | calls | verified engine mechanics the Scheduler section is written against: `at`-jobs are `DeleteAfterRun` and are deleted after firing; transient dispatch errors trigger retry backoff; past `at` instants compute to nil (dropped); `Reconcile()` runs at boot only |
| `pkg/gateway/rest_tasks.go` | extends | REST seam; add the occurrences endpoint; existing 400 mapping via `isTaskValidationErr` |
| `src/components/workspaces/CreateTaskSlideOver.tsx:448-504` | modifies | generic create form; raw cron `<Input>` at 492-500 — `every`/`recurring` options removed |
| `src/components/command-center/TaskDetailPanel.tsx:690-740` | modifies + moves | the generic Board/List task detail panel (sole importer: `workspaces/TaskDetailSlideOver.tsx`); moves to `src/components/workspaces/` in PR 1 (D10). Its recurring trigger section becomes a **defensive** read-only summary + "Edit in workspace calendar" link (FR-023) — normally unreachable since Board/List exclude recurring tasks |
| `src/components/screens/CalendarScreen.tsx` | modifies | integration hub; new slide-over wiring, occurrences query keyed to the visible range, agent filter state |
| `src/lib/calendar/eventMapping.ts` | modifies | currently **skips** `every`/`recurring` triggers entirely (F-10 v1 cut) — the gap this feature closes; existing `:due`/`:fire` chip kinds (`src/components/calendar/types.ts:70-72`) unchanged |
| `src/components/calendar/CalendarToolbar.tsx` | modifies | no filters today; agent filter dropdown lands here |
| `src/components/workspaces/BoardView.tsx`, `ListView.tsx` | modifies | add recurring-exclusion predicate next to the existing `surface !== 'user'` exclusion (`BoardView.tsx:134`, `ListView.tsx:33`) |
| `src/components/workspaces/ListView.tsx:26-39` (`FilterSelect` usage) | calls | pattern to reuse for the calendar agent filter |
| `src/components/command-center/SchedulesList.tsx`, `ScheduleFormSheet.tsx`, `cronUtils.ts` | deletes | **dead code** — verified unreachable (only import each other; `AgentProfile.tsx` references them in comments only); removed in PR 1 (D9). The `/api/v1/schedules` REST entity and the `pkg/cron` engine stay — the engine executes task triggers and heartbeats |
| `pkg/audit` (existing audit pipeline) | calls | one-way conversion emits an audit entry (FR-022) |
| NEW `src/components/calendar/RecurrenceEditor.tsx` | new | Outlook/Google-style recurrence editor (shadcn primitives) |
| NEW `src/components/calendar/CalendarEventSlideOver.tsx` | new | calendar-specific create/edit Sheet |
| NEW dep `github.com/teambition/rrule-go` | new | pure Go, no CGo (Constraint #2), server-side RRULE engine |
| NEW dep `rrule` (npm, BSD-3) | new | client-side RRULE build/parse + summary text only — **never** occurrence authority |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents |
|----------------|------------|----------------|----------------|
| `TaskTrigger.yaml` (additive config keys) | MEDIUM | generated Go/TS types, `ValidateTrigger`, both task forms | any consumer decoding `trigger.config` |
| `ValidateTrigger` | MEDIUM | task Create/Update REST paths | SPA error toasts, e2e task flows |
| `TaskTriggerScheduler` | HIGH | every time-triggered task firing (incl. heartbeat surface) | scheduled sessions, run history |
| `eventMapping.ts` | MEDIUM | `CalendarScreen` events memo | FullCalendar render, drag/reschedule |
| `BoardView`/`ListView` exclusion | LOW | board/list render | drag-and-drop, rollups |
| `CreateTaskSlideOver` trigger section | LOW | board/list create flow | tests in `CreateTaskSlideOver.test.tsx` (recurring-path tests must change) |
| `TaskDetailPanel` recurring section + folder move | LOW | Board/List detail slide-over | one import path (`TaskDetailSlideOver.tsx`) |

**HIGH-risk callout**: `TaskTriggerScheduler` executes *all* time triggers, including workspace heartbeats (`surface: heartbeat`, `recurring` cron). The RRULE re-arm path must be purely additive — the existing `at`/`every`/`cron` job translation for legacy triggers must remain byte-for-byte equivalent, protected by regression tests (see TDD plan).

### Relevant Execution Flows

| Flow | Relevance |
|------|-----------|
| Task create/update → `ValidateTrigger` → 400 or persist → `OnTaskUpserted` → job registration | RRULE triggers join this flow; validation grows (bounded-window scan, input bounds) |
| `CronService` tick → `RunScheduled` → `SpawnReset` → dispatch run | RRULE `at`-jobs: the engine deletes the fired job (`DeleteAfterRun`); the ONLY thing continuing the series is the re-arm — hence the exit-path rules in the Scheduler section |
| `CronService` transient-retry backoff (`service.go:812-825`) | RRULE jobs **opt out** — `RunScheduled` returns nil to the engine even on dispatch error (after logging + re-arming); "the next occurrence is the retry" |
| Calendar: `fetchTasks` + `fetchMilestones` + occurrences query → `mapToCalendarEvents` → FullCalendar | occurrences joined by task_id; buckets and truncation markers are new event kinds |
| Calendar drag-to-reschedule (`persistReschedule`, `task-fire` case) | applies to `once` triggers only; recurring occurrence chips are **not draggable** in v1 (series-level editing only) |

---

## User Stories & Acceptance Criteria

### User Story 1 — Create a repeating task without knowing cron (Priority: P1)

An operator wants a task to run "every 2 weeks on Monday at 09:00, until end of year". Today they must type `0 9 * * MON` (which can't even express "every 2 weeks") into a raw text field with no validation feedback beyond a server 400 toast. With this story, they click a day on the calendar, get an event-style panel, pick "Weekly on Monday" from a preset dropdown or open Custom for the full editor, and read a live plain-English summary of what they built. No cron syntax anywhere.

**Why this priority**: This is the recorded P1 *bug* — the current input is "not valid or user-friendly". It blocks non-technical operators from using recurring tasks at all.

**Independent Test**: With only this story implemented (recurring tasks may still render nowhere), create a task via the new panel with "every 2 weeks on Monday, ends after 10 times" and verify via `GET /api/v1/tasks/{id}` that the stored trigger carries a valid RRULE with `INTERVAL=2;BYDAY=MO;COUNT=10` and no `cron_expr`.

**Acceptance Scenarios**:

1. **Given** the calendar in Month view, **When** the operator clicks an empty day cell, **Then** the calendar-specific event slide-over opens pre-filled with that date, showing Title, Agent, Date & time (with the rule's IANA timezone displayed read-only beside the time, e.g. "09:00 (Europe/Berlin)"), and a Repeat section defaulting to "Does not repeat".
2. **Given** the event slide-over open on a Monday, **When** the operator opens the Repeat preset dropdown, **Then** the presets are computed from that date: Does not repeat / Daily / Weekly on Monday / Monthly on the third Monday *(ordinal matching the date)* / Annually on July 20 / Every weekday / Custom…
3. **Given** Custom is selected, **When** the operator sets "every 2 weeks", toggles Mon, and chooses "After 10 occurrences", **Then** the live summary reads "Repeats every 2 weeks on Monday, 10 times" and Save persists a `recurring` trigger with an equivalent RRULE.
4. **Given** an end date earlier than the start date is selected, **When** the operator attempts it, **Then** the input is rejected at selection time (date picker disallows it) — no server round-trip.
5. **Given** a custom rule that would fire more often than once per 60 seconds, **When** the operator saves, **Then** the panel shows the server's validation message inline at the Repeat section (not a generic toast).
6. **Given** "Does not repeat" is kept and a time is set, **When** the operator saves, **Then** a task with a `once` trigger at that instant is created (an execution, not a deadline), with the same field defaults as the generic create form (`surface: user`, default status/priority, empty description) — it appears on the Board like any `once` task.

---

### User Story 2 — See repeating tasks on the calendar like recurring meetings (Priority: P1)

An operator opens the workspace calendar and expects a task that runs every Monday to appear on *every* Monday, like a recurring meeting in Outlook. Today recurring tasks are completely invisible on the calendar (a deliberate v1 scope cut, F-10), so the calendar undersells what the system will actually do.

**Why this priority**: Prerequisite for the P1 calendar-only split (US-3) — recurring tasks can't become "calendar-only" while the calendar can't show them.

**Independent Test**: Seed one task "daily at 09:00" (RRULE) and one legacy task with `cron_expr: "0 9 * * MON"`; open Month view and count chips: the daily task appears on every day, the legacy task on every Monday, at times matching the server's own expansion.

**Acceptance Scenarios**:

1. **Given** a task repeating weekly on Monday at 09:00, **When** the operator views a month containing 4 Mondays, **Then** 4 chips for that task render, each on a Monday, each showing 09:00 in Week/Day views.
2. **Given** a legacy task with `cron_expr` (created before this feature), **When** the calendar is viewed, **Then** its occurrences render identically to RRULE-based tasks (server-side expansion covers both).
3. **Given** a task repeating every 30 minutes, **When** Month view is shown, **Then** each day shows a single aggregated chip labeled with the task title and its frequency ("· every 30 min"), **But** Week and Day views show individual timed entries.
4. **Given** a recurring task whose UNTIL date has passed **or** whose COUNT is exhausted, **When** a later date range is viewed, **Then** no occurrences render beyond the end condition.
5. **Given** a recurring occurrence chip (or aggregated chip) is clicked, **When** the panel opens, **Then** it is the calendar event slide-over in edit mode showing the series (title, agent, repeat summary), not the generic task form.
6. **Given** a due chip or a `once` fire chip is clicked, **When** the panel opens, **Then** it is today's task detail panel (unchanged behavior) — the event slide-over is only for recurring series and calendar-created tasks.

---

### User Story 3 — Board and List show only real one-time work (Priority: P1)

A recurring task card never reaches "Done" — it sits in a kanban column forever, breaking the board's mental model (cards flow to completion) and adding permanent noise. With this story, Board and List show only non-recurring tasks; recurring tasks live exclusively in the calendar, and the Board's create form no longer offers recurring trigger options.

**Why this priority**: Recorded P1 enhancement; direct consequence of the operator's product decision D3.

**Independent Test**: Create one manual task, one `once` task, one recurring task. Board and List show exactly the first two; the calendar shows the `once` fire chip and the recurring occurrences.

**Acceptance Scenarios**:

1. **Given** a workspace with a recurring task, **When** the Board is viewed, **Then** the recurring task's card does not appear in any column.
2. **Given** the same workspace, **When** the List is viewed, **Then** the recurring task does not appear in the table.
3. **Given** the Board's "New task" slide-over, **When** the Trigger dropdown is opened, **Then** only "None (manual)" and "Once (at a time)" are offered — "Every (interval)" and "Recurring (cron)" are gone.
4. **Given** a `once`-trigger or `manual` task, **When** Board/List are viewed, **Then** it appears exactly as today (no behaviour change for non-recurring tasks).
5. **Given** the generic task detail panel somehow receives an `every`/`recurring` task (normally impossible — Board/List exclude them; reachable only via stale cache or a race), **Then** it shows the trigger as a read-only plain-English summary with an "Edit in workspace calendar" link — never a raw cron input, never trigger editing (defensive guard, FR-023).

---

### User Story 4 — Filter the calendar by agent (Priority: P2)

A workspace calendar mixes every agent's scheduled work. The operator wants to answer "what is Mia doing this week?" with one click. The List already has an agent filter; the calendar has none.

**Why this priority**: Recorded P2; small, independent, immediate value; no contract change.

**Independent Test**: With tasks assigned to two different agents on the same week, select one agent in the toolbar dropdown and verify only that agent's task events remain, instantly (no network request for the filter itself).

**Acceptance Scenarios**:

1. **Given** the calendar toolbar, **When** rendered, **Then** an Agent dropdown appears with "All agents" selected by default, listing the agents present in the workspace roster.
2. **Given** "Mia" is selected, **When** the grid re-renders, **Then** only task events whose task `agent_id` is Mia's remain — due chips, fire chips, and recurring occurrences alike — with no loading state (client-side filter).
3. **Given** an agent filter is active, **When** milestones exist in the range, **Then** milestone chips remain visible (milestones have no agent and are not filtered).
4. **Given** "Unassigned" is selected, **When** the grid re-renders, **Then** only tasks with no `agent_id` remain.

---

### User Story 5 — Old-format tasks keep firing and are replaced through the same UI (Priority: P1)

Existing installations may hold recurring tasks stored as cron expressions or fixed intervals (`every_ms`). **Per the operator's no-back-compat decision (D8) these are NOT migrated, NOT reverse-mapped, and carry no editing-equivalence guarantees.** They keep firing exactly as before (the engine path is untouched — zero work), they render on the calendar like any recurring task (so nothing fires invisibly), and opening one offers exactly one action: set a **fresh** repeat rule in the normal picker, which overwrites the old trigger with RRULE. No cron string is ever shown or typed anywhere.

**Why this priority**: this is the cheapest coherent stance — it avoids ghost tasks (firing but invisible) without building any translation machinery (no cron→picker mapping, no timezone-preserving conversion, no cross-zone semantics).

**Independent Test**: Seed a `cron_expr: "0 9 * * MON"` task on a pre-feature build; upgrade; its chips render on the calendar; opening one shows an "old schedule format" note with the next run time and an empty picker; saving a new rule stores RRULE and re-arms; a second cron task never opened keeps firing untouched.

**Acceptance Scenarios**:

1. **Given** a legacy cron or `every_ms` task, **When** the calendar loads, **Then** its occurrences render like any recurring task (server-expanded; `every_ms` per FR-008a).
2. **Given** its chip is opened, **Then** the calendar panel shows an "old schedule format" note with the task's next run time and a fresh (empty) repeat picker — no cron string displayed, no pre-filled translation.
3. **Given** a new rule is saved from that panel, **Then** the stored trigger becomes `type: recurring` with RRULE (anchored in the browser zone like any new rule — fire times MAY shift, accepted under D8), an audit entry records old → new, and the scheduler re-arms from the new rule.
4. **Given** a legacy task that is never opened or saved, **Then** it continues to fire exactly as before (no migration, no behaviour change server-side).

---

## Behavioral Contract

Primary flows:
- When a day/slot is clicked on the calendar, the system opens the calendar event slide-over pre-filled with that date and a "Does not repeat" default.
- When "Does not repeat" is saved with a time, the system creates a `once`-trigger task (an execution at that instant).
- When a repeat rule is built in the editor, the system always displays a live plain-English summary of the rule and the rule's timezone.
- When a task with a repeat rule is saved, the system stores it as an RRULE-based `recurring` trigger and arms its next fire.
- When the calendar viewport shows a date range, the system renders the server-expanded occurrences of each recurring task in that range — individual chips for days with ≤3 occurrences, one aggregated chip for days with >3.
- When an occurrence or aggregated chip is clicked, the calendar event slide-over opens on the series; when a due or `once` fire chip is clicked, today's task panel opens unchanged.
- When an agent is selected in the calendar filter, the system instantly hides all task events not belonging to that agent.
- When the Board or List is viewed, the system shows no tasks with `every` or `recurring` triggers.
- When a fired RRULE occurrence completes — successfully or not — the scheduler arms the next occurrence.

Error flows:
- When a repeat rule would ever fire twice within 60 seconds (checked over a bounded expansion window, not just the first pair), the server rejects it with 400 and the panel shows the message inline at the Repeat section.
- When a rule is syntactically valid but never produces an occurrence, or exceeds input-size bounds, the server rejects it with 400 within the validation time bound.
- When the occurrences query fails, the calendar still renders due/one-shot chips and shows a non-blocking "Couldn't load recurring occurrences" toast (mirrors existing degrade pattern).
- When a dispatch fails at fire time, the series does NOT die: the next occurrence is armed anyway (the next occurrence is the retry), and the failure is logged.
- When an occurrences response is truncated, the client renders a visible "more occurrences not shown" marker on the last covered day — never a silently emptier calendar.

Boundary conditions:
- When "monthly on the 31st" hits a short month, the occurrence lands on that month's last day (never skipped), and the editor summarizes and reopens it as "day 31", not as a BYMONTHDAY list.
- When a rule's local time does not exist on a DST spring-forward day, the occurrence fires at the gap-shifted normalized time (02:30 → 03:30 across a one-hour gap); when it exists twice on fall-back, the first occurrence is used; every occurrence fires exactly once, with identical normalized instants de-duplicated.
- When a rule's UNTIL/COUNT is exhausted, no further occurrences render and no further fires occur; the job is retired.
- When the gateway was down across an occurrence's instant, that occurrence is skipped (not replayed); COUNT positions are consumed by calendar time, not by actual fires.
- When a task is edited while one of its fires is in flight, exactly one armed job exists afterwards — for the new rule.

---

## Timezone Semantics (normative)

1. **New rules (editor)**: the rule's `tz` defaults to the **browser's** IANA zone (`Intl.DateTimeFormat().resolvedOptions().timeZone`), displayed **read-only beside the time field** (e.g. "09:00 (Europe/Berlin)") in both create and edit modes. v1 has no tz picker; the display makes the stored zone visible and auditable. Replacing a legacy task's rule (US-5.3) anchors the new rule the same way — like any new rule.
2. **Legacy `cron_expr`/`every_ms` (display only, D8)**: cron fires evaluate in the **server's local zone** (`gronx.NextTickAfter` semantics), so the occurrences endpoint expands `cron_expr` in the server zone — for rendering only. There is no reverse-mapping, no zone labeling of legacy times, and no conversion-equivalence guarantee (no `server_tz` wire exposure is needed). `every_ms` is timezone-free; its display projection is FR-008a.
3. **DST policy (D7 refined)**: occurrences are wall-clock in the rule's `tz`. A local time made **nonexistent** by spring-forward resolves by Go `time` normalization — **the wall-clock time shifted forward by the gap length** (02:30 → **03:30** across a one-hour gap; NOT the first instant after the gap). An **ambiguous** fall-back local time resolves to the **first** (pre-transition) occurrence. Every scheduled occurrence fires exactly once, and two rule occurrences that normalize to the **same instant** (a DST collision, e.g. an API-crafted `BYHOUR=2,3;BYMINUTE=30` on the spring-forward day) collapse to a **single fire** — the expansion layer de-duplicates identical instants, which also makes any min-gap-scan window miss of such a pair harmless. If `rrule-go`'s native behavior differs, the expansion layer normalizes to this policy — the policy is normative, the library is not; and because Go's own docs do not *guarantee* gap normalization for nonexistent times, the RRULE ADR records that the expansion layer owns the 03:30 policy even against a future stdlib behavior change (test 4 pins it).
4. All server expansion and validation use these same rules — the endpoint, the validator, and the scheduler can never disagree.

---

## Edge Cases

- Recurring task with >3 occurrences on a single day in overview ranges → single aggregated chip (D6). Exactly 3 → individual chips.
- `BYDAY` weekly rule where the anchor date's weekday is not among the toggled days → first occurrence is the first toggled weekday after the anchor (RRULE semantics); summary reflects the toggled days, not the anchor.
- "Every weekday" preset crossing a month boundary → occurrences continue seamlessly.
- Feb 29 yearly rule → fires only in leap years (RFC semantics); picker note explains this when Feb 29 is the anchor.
- Rule at 02:30 in a DST zone → spring-forward day fires once at the normalized 03:30 (gap-shifted); fall-back day fires once at the first 02:30.
- A Board-visible task `blocked_by` a calendar-only recurring task → the blocker's **name** still resolves (the store is untouched by the presentation split) and blocked-state rollups ignore trigger type; the blocker simply has no Board/List card of its own.
- `COUNT=1` or a tight UNTIL yields fewer than two occurrences → trivially satisfies the min-interval floor (no error).
- Workspace with zero agents / roster fetch failure → filter dropdown degrades to "All agents" + the existing "Team list unavailable" notice pattern.
- A recurring task assigned to an agent later removed from the workspace team → still renders; filter lists it under its stored agent id (name fallback "Unknown agent").
- Concurrent edit while a fire is in flight → the trigger-generation guard (Scheduler section) guarantees exactly one armed job for the stored rule.
- Clock skew / `from_ms ≥ to_ms` in the occurrences query → 400. Range span > 400 days → 400.
- Legacy `every_ms` task → displayed occurrences are a projection baselined on the live job's `NextRunAtMS` (FR-008a); sub-daily intervals aggregate per D6.
- A never-matching rule (e.g. `FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=31` — Feb 31) → rejected 400 at validation within bounded time (liveness bound, Validation §5).
- Downgrade to a pre-feature binary → RRULE tasks stop firing (WARN logged, job skipped), old SPA hides them, data preserved; re-upgrade restores. See Operations & Rollback.

---

## Explicit Non-Behaviors

- The system must not offer "edit this occurrence only" / per-occurrence exceptions (EXDATE) in v1 — series-level editing only, because occurrence-level exceptions multiply UI and storage complexity and were not requested.
- The system must not allow dragging an individual recurring occurrence chip to reschedule it — that is occurrence-level editing by another name; drag remains enabled only for `once` fire chips and due chips.
- The system must not bulk-migrate or rewrite stored `cron_expr`/`every_ms` triggers — legacy tasks keep working untouched; conversion happens only when the operator edits and saves (US-5.3), and each conversion is audit-logged (FR-022).
- The system must not touch the agent **Schedules** backend entity (`ScheduleTrigger.yaml`, `/api/v1/schedules`, the `pkg/cron` engine) — under-the-hood machinery stays. The **dead** Schedules SPA components (`SchedulesList.tsx`, `ScheduleFormSheet.tsx`, `cronUtils.ts` — unreachable, D9) ARE deleted in PR 1; that deletion changes no behavior.
- The system must not re-implement cron or RRULE occurrence computation in JavaScript for display purposes — the server is the single authority on fire times; the client `rrule` lib is used only for building/parsing rule strings and summary text in the editor. FR-020's upcoming-fires preview sources the occurrences endpoint, never client math.
- The system must not remove recurring tasks from the task **store** or REST list responses — the split is a presentation-layer rule on workspace Board/List; the data remains queryable (the REST API and agent tools still see it).
- The system must not change heartbeat-surface task behaviour (`surface: heartbeat`) — heartbeats stay cron-driven and hidden as today.
- The system must not enroll RRULE `at`-jobs in the cron engine's transient-retry backoff — the next occurrence is the retry (Scheduler section).
- The system must not send emoji into stored data or UI chrome (project UI rule).

---

## Integration Boundaries

### FullCalendar v6 (client library, already vendored)

- **Data in**: `EventInput[]` from `mapToCalendarEvents` — now including three new kinds in the `CalendarEventExtProps` union: `task-occurrence` (individual), `task-occurrence-agg` (aggregated day chip), `task-occurrence-more` (truncation marker).
- **Data out**: click callbacks (occurrence/aggregated chips: click only; `editable:false` per-event; truncation marker: non-interactive).
- **Contract**: existing wrapper `FullCalendarView.tsx`; no new FullCalendar plugins (`@fullcalendar/rrule` NOT adopted — server-side expansion).
- **On failure**: n/a (in-process rendering).
- **Development**: real library.

### `teambition/rrule-go` (new Go dependency)

- **Data in**: parsed RRULE + DTSTART (anchor, in the rule's IANA tz).
- **Data out**: occurrence instants (`Between`, `After`) for expansion, next-fire computation, COUNT/UNTIL handling — normalized to the Timezone Semantics DST policy by the expansion layer.
- **Contract**: pure Go, MIT, no CGo (Constraint #2 compliant). Embed `time/tzdata` in the binary so IANA zones resolve on minimal systems (single-binary constraint).
- **On failure**: parse errors → `ErrValidation` → 400 at the REST seam; never a panic path. Validation and expansion are time-bounded (FR-006) — a pathological rule cannot stall a core.
- **Development**: real library; version pinned in `go.mod`.

### `rrule` (rrule.js, new npm dependency, BSD-3)

- **Data in/out**: editor state ⇄ RRULE string; summary text (with the clamp-pattern special case rendered by our own formatter, FR-007).
- **Contract**: used exclusively inside `RecurrenceEditor`; occurrence expansion functions are not called for calendar display.
- **On failure**: unparseable legacy strings → US-5.2 read-only fallback.
- **Development**: real library.

### Omnipus REST API (contract-first, Constraint #8)

- **Data in**: `POST/PUT /api/v1/tasks` with extended `TaskTrigger.config`; new `GET /api/v1/tasks/occurrences?workspace_id&from_ms&to_ms&tz`.
- **Data out**: task objects (unchanged shape plus new optional config keys); occurrence sets (bucketed shape below).
- **Contract**: schema changes in `contracts/components/schemas/TaskTrigger.yaml` + new `TaskOccurrenceSet.yaml`, referenced from `openapi.yaml`; regenerate via `scripts/gen-contracts.sh`; generated types only, spec + generated diff in one atomic commit; `make verify-contracts` green.
- **Rate limiting**: a **new** `taskReadLimiter` (240 requests/min, matching `configLimiter`'s post-incident ceiling) wraps the occurrences endpoint only — explicitly NOT `configLimiter` (that bucket's 429s already broke the calendar once), and NOT the existing task CRUD routes (which have no limiter today and keep that behavior).
- **On failure**: 400 for validation, 200 with `truncated:true` per task for capped expansion; SPA zod-validates responses (drop + counter on mismatch, per Constraint #8).
- **Development**: real gateway (embedded-SPA e2e path).

---

## Wire & Engine Design (normative)

### TaskTrigger.config — additive keys (no enum change)

`type` stays `manual | once | every | recurring`. For `type: recurring`, `config` must carry **exactly one** of:

- `cron_expr` (string) — legacy, still accepted and validated via gronx (unchanged), or
- `rrule` (string) — RFC 5545 RRULE body (e.g. `FREQ=WEEKLY;INTERVAL=2;BYDAY=MO;COUNT=10`), **plus required siblings**:
  - `dtstart_ms` (int64) — anchor instant (first occurrence's wall-clock moment, unix ms),
  - `tz` (string) — IANA zone name in which the rule's wall-clock times are interpreted (Timezone Semantics).

Note (OBS-003, deliberate): `cron_expr` remains creatable via the raw API and agent tools (`create_task`) — "legacy" is an open class, and the read-only-fallback edit path (US-5.2) is therefore permanent, not transitional. A follow-up issue updates the `create_task` tool description to steer agents toward RRULE; out of scope here.

### Validation (`ValidateTrigger`, RRULE path)

All failures → `ErrValidation` → 400 with a human-readable message (surfaced inline at the Repeat section, FR-006).

1. Exactly one of `cron_expr` / `rrule`; `rrule` requires `dtstart_ms` + `tz`; `tz` must load (with embedded tzdata); RRULE must parse. The `rrule`/`dtstart_ms`/`tz` keys are legal **only** on `type: recurring` — `ValidateTrigger` rejects them on any other type (a legacy `every` task converts by *also* flipping `type` to `recurring`, FR-013).
2. **Input bounds**: `rrule` length ≤ 512 characters; reject `FREQ=SECONDLY`; reject any `BYSECOND` value other than the DTSTART second (the editor can produce neither — only hand-crafted API payloads can).
3. `UNTIL` (when present) must not precede `dtstart_ms`.
4. **Bounded-window minimum-gap scan — defense-in-depth** (replaces the cron first-two-fires technique, which is sound only for minute-aligned periodic cron): expand from DTSTART up to the **first 60 occurrences or 366 days, whichever is smaller**, and reject if **any consecutive pair** is < 60 s apart. Rules yielding fewer than two occurrences in the window (e.g. `COUNT=1`) trivially pass. Note: after §2's hard rejects, every surviving rule's occurrences share the DTSTART second — whole minutes apart, or 0 s on a DST collision which Timezone-§3 dedup collapses — so this scan is expected to have **no reachable rejection**; it is retained deliberately as defense-in-depth against library quirks and future rule-shape growth, not as the operative mechanism (the adversarial sub-minute inputs are rejected by §2).
5. **Liveness bound**: if the rule produces zero occurrences within 5 years of DTSTART, reject ("rule never fires") — this also bounds the scan's worst-case work on never-matching rules (e.g. Feb 31). Validation is O(window) and must complete within ~1 s for any accepted-or-rejected input.
6. **COUNT bound**: `COUNT`, when present, must be ≤ 100,000 (400 above) — this bounds every COUNT-exhaustion check and DTSTART skip-walk (endpoint "Aged DTSTART" rule and Scheduler rule 1).

**Monthly clamp (D5)**: the editor compiles "monthly on day N" (N ≥ 29) to the RFC-standard clamp form `BYMONTHDAY=28,…,N;BYSETPOS=-1` so short months land on their last day. Plain `BYMONTHDAY=N` (which *skips* short months per RFC) is never emitted by the editor. The editor **recognizes** this canonical pattern when reopening a rule and renders it as "day N" mode; the summary reads "Monthly on day N (moved to the last day in shorter months)" — never a BYMONTHDAY list (FR-007).

### Occurrence expansion endpoint

`GET /api/v1/tasks/occurrences?workspace_id={id}&from_ms={int64}&to_ms={int64}&tz={IANA}` (auth: same as task reads) → `200 [TaskOccurrenceSet]`.

The required `tz` parameter is the **viewer's** IANA zone (the SPA passes the browser zone). It is the **day-boundary authority for bucketing**: the >3/day threshold and `day_start_ms` are evaluated on days in the query `tz` for **all** trigger flavors — so aggregated chips always land on the day the viewer's grid renders, regardless of each rule's own `tz` (a Tokyo-anchored rule viewed from Berlin buckets on Berlin days). Unloadable `tz` → 400.

**Rate limiting (concrete)**: a **new** `taskReadLimiter = newAPIRateLimiter(240, 1*time.Minute)` (matching `configLimiter`'s post-incident ceiling, which calendar navigation cadence is known to fit) wraps `GET /api/v1/tasks/occurrences` only. Existing task CRUD routes remain as today — plain `withAuth`, no limiter (unchanged behavior; verified `pkg/gateway/rest.go:4695-4696`). Like every `apiRateLimiter` bucket this keys on client IP — behind a reverse proxy without `gateway.trust_xff`, all callers share one 240/min bucket (see `docs/operations/reverse-proxy.md`); worth remembering before diagnosing calendar 429s.

**Routing note**: the gateway registers `/api/v1/tasks/` as a prefix route into `HandleTasks`, which parses the trailing segment as a task ID (`pkg/gateway/rest.go:4696`) — the literal `occurrences` sub-path MUST be matched before ID parsing (or registered explicitly ahead of the prefix), else it 404s as task-not-found.

```
TaskOccurrenceSet = {
  task_id:        string,
  occurrences_ms: [int64],          // individual instants (rules below)
  day_buckets:    [DayBucket],      // aggregated days (rules below)
  truncated:      bool
}
DayBucket = {
  day_start_ms: int64,   // midnight of the day in the QUERY tz (viewer zone)
  count:        int32,
  first_ms:     int64,   // first occurrence that day — consumed by the aggregated-chip tooltip ("first at 09:00")
  interval_ms:  int64 | null   // fixed spacing when the rule is regular; null when irregular
}
```

- **Task selection**: expands recurring-capable triggers **only for tasks the scheduler would arm** — non-terminal AND not `surface: heartbeat`, the same predicate `OnTaskUpserted` applies before registering a job (`pkg/agent/task_trigger.go:158-169`). Terminal (`done`/`failed`/…) and heartbeat-surface tasks are omitted entirely — the calendar never renders occurrences that will not fire. Defense-in-depth: the client drops any occurrence set whose `task_id` is absent from its fetched task list.
- Trigger flavors: `rrule` via rrule-go (normalized per Timezone Semantics), `cron_expr` via gronx iteration in the server zone, `every_ms` per FR-008a (forward-only: occurrences before *now* are omitted — the projection cannot be extrapolated backwards; a range fully in the past returns no entry for an `every_ms` task).
- **Range semantics**: half-open `[from_ms, to_ms)`; the client passes FullCalendar's `activeStart`/`activeEnd` directly (activeEnd is exclusive) — no double-render or edge-drop across adjacent fetches.
- **Bucketing rule** (resolves the cap-vs-D6 contradiction): span is measured in milliseconds with the detail/overview boundary at **8 × 24 h** — spans ≤ 8 days (Week/Day views, including the 169-hour DST fall-back week) return raw instants for all days; spans > 8 days (overview ranges, e.g. Month) return one `DayBucket` for any query-tz day on which a task has **more than 3** occurrences, raw instants for days with ≤ 3.
- **Caps and work budget, enforced during iteration (not post-hoc)**: `occurrences_ms` ≤ 500 per task per request, plus a **total per-task iteration budget of 10,000 computed occurrences per request** counting bucket enumeration. For **provably regular** triggers (`every_ms`; fixed-interval rrule with no BY* modifiers) arithmetic derivation is a **MUST**, not an optimization: bucket counts and range positions are computed O(1) without iteration, so a valid plain `FREQ=MINUTELY` rule renders complete Month buckets with `truncated: false`. Only irregular (BY*-modified) rules consume the iteration budget; on exhausting either bound: `truncated: true`, stop, and the client renders a **truncation marker** on the last covered day ("more not shown").
- **Aged DTSTART (skip-work bound)**: reaching the queried range — or the next occurrence at re-arm time (Scheduler rule 1) — MUST NOT require walking every occurrence from DTSTART. Regular rules fast-forward arithmetically (O(1)). Irregular rules **without COUNT** begin iteration at the FREQ period containing the target instant, not at DTSTART (occurrence membership is position-independent when COUNT is absent). Rules **with COUNT** may walk from DTSTART — bounded by the validation-time COUNT cap (≤ 100,000, Validation §6); that bounded skip-walk does not consume the 10,000 in-range budget.
- **Labels (MAJ-008)**: the client derives the aggregated-chip label from `interval_ms` ("· every 30 min"); when `interval_ms` is null (irregular rule), the label is "· {count}×/day". The client performs no recurrence math.
- Tasks with zero occurrences in range are **omitted**; an empty result is `[]`, never null.
- `from_ms ≥ to_ms` → 400 (an empty half-open range is a client bug, not a valid query); span > 400 days → 400.
- The SPA keys its occurrences query on workspace + range + `tz`, so a browser-zone change mid-session cannot serve stale-zone buckets.
- Read-only; no state change.

#### FR-008a — `every_ms` display projection

The engine has **no stored anchor** for `every`-kind jobs: `computeNextRun` is `now + interval`, drift-anchored, re-baselined on restart (`pkg/cron/service.go`). Therefore the endpoint's `every_ms` expansion is defined as a **projection**: the first returned occurrence is the live job's `State.NextRunAtMS` (read internally from the trigger scheduler's cron service); subsequent occurrences are `first + k·every_ms`. The spec explicitly documents that this projection re-baselines after each fire and after restarts — the first entry always matches the armed engine state (asserted by test), later entries are best-effort.

### Scheduler (`TaskTriggerScheduler`) — RRULE execution rules

Written against the verified engine mechanics: `at`-jobs are created with `DeleteAfterRun` (`pkg/cron/service.go:1074`) and deleted after firing (`:802`); transient dispatch errors normally enter retry backoff (`:812-825`); `computeNextRun` drops past `at` instants; `Reconcile()` runs at boot only.

1. **Registration**: an RRULE trigger arms as a `kind:"at"` job at the next occurrence after `now` (per Timezone Semantics expansion). COUNT/UNTIL are evaluated statelessly from DTSTART.
2. **Re-arm on every exit path**: `RunScheduled` has five real exits (verified `pkg/agent/task_trigger.go:243-277`), and the design for each is explicit: (a) **success**, (b) **overlap-guard skip** (`ErrAlreadyRunning`), (c) **dispatch error**, and (d) **`SpawnReset` error** — all four MUST compute and register the next occurrence's job before returning (the task is readable on all of them); (e) **task-unreadable** (transient `store.Get` error) — re-arm is *impossible* (the trigger cannot be read): log ERROR and rely on the recovery sweep (rule 6) as the designated recovery. Task-deleted cleanup does not re-arm. Because the fired job is auto-deleted, the re-arm is the sole continuation of the series — any *readable-task* path that returns without re-arming is a defect.
3. **No retry backoff**: RRULE jobs opt out of the engine's transient-retry backoff — on dispatch error, log WARN, re-arm the next occurrence, and return nil to the engine ("the next occurrence is the retry", mirroring cron-kind semantics and avoiding the backoff-vs-`DeleteAfterRun` conflict).
4. **Stale re-arm guard (edit-during-fire race)**: the scheduler captures a **trigger generation** (hash of the trigger JSON) at registration. The guard is only sound if it is **atomic**: the re-arm's hash comparison, job registration, and `taskToJob` map write MUST execute as one critical section under the scheduler's mutex, and `OnTaskUpserted`'s remove → `AddJobFull` → map-write sequence MUST hold the **same** mutex across the whole sequence (today it locks only the map operations — `pkg/agent/task_trigger.go:154-212` — which leaves a check-then-register window). On hash mismatch the re-arm no-ops (a concurrent upsert already registered the new rule's job). Additionally, **every re-arm registration — exit-path and sweep alike — is replace-by-task**: remove the task's currently tracked job (if any) before `AddJobFull`, inside this same critical section, so any residual double-arm heals idempotently instead of persisting. **Lock ordering**: scheduler-mutex → engine-mutex, always; the engine MUST NOT hold `cs.mu` when invoking `RunScheduled` or any scheduler callback (current behavior — `executeJobByID` snapshots and unlocks before calling the runner, `pkg/cron/service.go:624` — regression-guarded by test 10's interleaving hook). Post-condition after any edit-during-fire: exactly one armed job, for the new rule — and the verifying test must force the interleaving deterministically (test 10's blocking hook), not hope for it.
5. **Missed occurrences**: occurrences whose instant passed while the gateway was down are skipped, not replayed; COUNT positions are consumed by calendar time (stateless computation from DTSTART), not by actual fires.
6. **Crash recovery & observability**: `Reconcile()` at boot re-arms the next future occurrence for every non-terminal RRULE task — this is the documented recovery for the crash window between fire and re-arm. In addition, a recovery sweep runs on a **dedicated ticker inside `TaskTriggerScheduler`, every 5 minutes** (no "existing maintenance cadence" exists to reuse — `Reconcile` is boot-only and `pkg/cron` has only the due-job tick loop). The sweep's predicate defines **armed** precisely: a registered job exists **and** (its `State.NextRunAtMS` is non-nil and ≥ now, **or** `State.Running` is true). The `Running` disjunct is load-bearing: `RunDueJobs` clears `NextRunAtMS` **before** dispatch (`pkg/cron/service.go:515-519`) and `State.Running` is set for the duration of the fire (`:613`) — so an **in-flight fire** (job present, `NextRunAtMS` nil, `Running` true, exit-path re-arm pending) is armed and MUST NOT be re-armed by the sweep, while the dead-entry shape the engine is known to produce (`rescheduleSkippedUnsafe` leaves an `at`-job entry with `NextRunAtMS` cleared and `Running` false) is a true orphan. Any non-terminal, non-exhausted RRULE task failing the predicate is re-armed (replace-by-task, rule 4) with a WARN — a recurring series must never be silently dead until restart. Exhaustion (COUNT/UNTIL) retires the job silently and legitimately.
7. **Legacy invariance**: the `at`/`every`/`cron` translation for non-RRULE triggers is byte-for-byte unchanged (regression-tested; guards heartbeats).

---

## Operations & Rollback

**Downgrade is a one-way door, accepted deliberately (no feature flag).** After any RRULE task exists, downgrading to a pre-feature binary means: the old `triggerToCronSchedule` fails on the missing `cron_expr`, `OnTaskUpserted` logs WARN and skips registration — the task **stops firing**; the old SPA renders nothing for it. Data is preserved verbatim; re-upgrading restores scheduling and rendering. Operators rolling back a hotfix must check the gateway log for these WARNs. This statement must be reproduced in the RRULE ADR (Slice 2).

Observability commitments: dispatch failures on RRULE fires log WARN with task id + occurrence instant; task-unreadable fire exits log ERROR; the recovery sweep logs WARN per re-armed orphan; expansion truncation is visible in the UI (marker chip), counted by a **dedicated client counter** incremented per `truncated: true` task-set (the zod-drop counter is the wrong mechanism — truncated responses are schema-valid), and debug-logged server-side per truncated expansion.

---

## BDD Scenarios

### Feature: Calendar Recurrence Redesign

#### Background

- **Given** a running gateway with a workspace "Ops" whose roster contains agents "Mia" and "Jim"
- **And** the operator is viewing the Ops workspace calendar

---

#### Scenario: Operator creates a biweekly task from a preset-plus-custom flow

**Traces to**: User Story 1, Acceptance Scenarios 1–3
**Category**: Happy Path

- **Given** the operator clicked Monday July 20 on the Month grid
- **And** the event slide-over opened pre-filled with July 20, defaulting to "Does not repeat", the time labeled with the browser's IANA zone
- **When** the operator selects "Custom…", sets interval 2 weeks, toggles Monday, chooses "After 10 occurrences", and saves
- **Then** the summary line read "Repeats every 2 weeks on Monday, 10 times" before saving
- **And** `GET /api/v1/tasks/{id}` returns `trigger.type = "recurring"` with an RRULE equivalent to `FREQ=WEEKLY;INTERVAL=2;BYDAY=MO;COUNT=10`, a `dtstart_ms` matching July 20 09:00 in the browser zone, that zone as `tz`, and no `cron_expr`

#### Scenario: Event slide-over opens with asserted defaults

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the operator clicks an empty day cell (July 20) on the Month grid
- **When** the event slide-over opens
- **Then** the Repeat control shows "Does not repeat"
- **And** the date field shows July 20
- **And** the time field is labeled with the browser's IANA zone

#### Scenario: Preset dropdown is computed from the clicked date

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the event slide-over was opened from Tuesday August 18 (the third Tuesday of August)
- **When** the operator opens the Repeat dropdown
- **Then** the options include "Weekly on Tuesday", "Monthly on the third Tuesday", "Annually on August 18", "Every weekday", and "Custom…"

#### Scenario: "Does not repeat" save creates a once-trigger task

**Traces to**: User Story 1, Acceptance Scenario 6
**Category**: Happy Path

- **Given** the event slide-over opened from a day cell with "Does not repeat" selected and a time of 14:00
- **When** the operator enters a title, picks Mia, and saves
- **Then** a task is created with `trigger.type = "once"` and `config.at_ms` at that day 14:00 local
- **And** the task carries the generic form's defaults (`surface: user`, default status and priority, empty description)
- **And** it appears on the Board and as a fire chip on the calendar

#### Scenario: End date earlier than start is unreachable

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Error Path

- **Given** the editor shows a rule starting July 20
- **When** the operator opens the "Ends on date" picker
- **Then** all dates before July 20 are disabled and cannot be selected

#### Scenario: Server rejects a sub-minute rule with an inline message

**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Error Path

- **Given** a rule whose bounded-window expansion contains two occurrences under 60 seconds apart
- **When** the operator (or an API caller) saves it
- **Then** the save fails with 400, the slide-over stays open, and the validation message appears inline under the Repeat section
- **But** no generic error toast is shown

#### Scenario Outline: Adversarial rules are rejected by the bounded validator

**Traces to**: User Story 1, Acceptance Scenario 5 (validation family)
**Category**: Error Path

- **Given** a `recurring` trigger crafted via the raw API with `<rrule_config>`
- **When** the create request is validated
- **Then** the server responds 400 with reason `<reason>` within 1 second

**Examples**:

| rrule_config | reason |
|--------------|--------|
| `FREQ=DAILY;BYHOUR=9;BYMINUTE=0;BYSECOND=0,30`, dtstart 09:00:15 | foreign `BYSECOND` rejected outright by Validation §2 (the min-gap scan sits behind it as defense-in-depth; a first-pair check alone would have passed this rule — its first pair is ≈24 h) |
| `FREQ=MINUTELY;BYSECOND=0,30` variant with 30 s gaps | foreign `BYSECOND` rejected by §2 |
| `FREQ=SECONDLY` | frequency rejected outright |
| `FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=31` | rule never fires (liveness bound) |
| 10 KB rrule string | input size bound (≤ 512 chars) |
| `rrule` + `cron_expr` both present | exactly-one rule source |
| `UNTIL` one day before dtstart | UNTIL precedes DTSTART |

#### Scenario Outline: Editor state compiles to the correct RRULE

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the editor is anchored at `<anchor>`
- **When** the operator configures `<ui_state>` and saves
- **Then** the stored rule is equivalent to `<rrule>`

**Examples**:

| anchor | ui_state | rrule |
|--------|----------|-------|
| Mon Jul 20 09:00 | preset "Daily" | `FREQ=DAILY` |
| Mon Jul 20 09:00 | preset "Every weekday" | `FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR` |
| Mon Jul 20 09:00 | weekly ×1, Mon+Thu | `FREQ=WEEKLY;BYDAY=MO,TH` |
| Fri Jul 31 08:00 | monthly on day 31 | `FREQ=MONTHLY;BYMONTHDAY=28,29,30,31;BYSETPOS=-1` |
| Tue Aug 18 10:00 | monthly on third Tuesday | `FREQ=MONTHLY;BYDAY=TU;BYSETPOS=3` |
| Mon Jul 20 09:00 (Europe/Berlin) | yearly, ends Dec 31 2027 | `FREQ=YEARLY;UNTIL=20271231T225959Z` *(Dec 31 23:59:59 Berlin → UTC, inclusive end-of-day per FR-006a)* |

#### Scenario: Saved day-31 rule reopens as day 31

**Traces to**: User Story 1, Acceptance Scenario 3 (compile + reopen round-trip)
**Category**: Alternate Path

- **Given** a rule saved from the editor as "monthly on day 31" (stored as the canonical clamp form)
- **When** its chip is opened again
- **Then** the picker shows monthly mode "day 31" (not a Custom BYMONTHDAY list)
- **And** the summary reads "Monthly on day 31 (moved to the last day in shorter months)"

#### Scenario: Weekly task renders on every Monday of the month

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a task "Weekly report" repeating weekly on Monday at 09:00 exists in Ops
- **When** the operator views a month containing four Mondays
- **Then** four "Weekly report" chips render, one on each Monday
- **And** switching to Week view shows the chip at 09:00

#### Scenario: Legacy cron task renders occurrences without migration

**Traces to**: User Story 2, Acceptance Scenario 2; User Story 5, Acceptance Scenarios 1 & 4
**Category**: Alternate Path

- **Given** a task stored before this feature with `trigger.config.cron_expr = "0 9 * * MON"` and no `rrule`
- **When** the calendar Month view loads
- **Then** its chips appear on every Monday at the instants the server's scheduler would fire (expanded in the server's zone)
- **And** the stored trigger is unchanged (still `cron_expr`)

#### Scenario: Sub-daily task aggregates to one bucketed chip per day in Month view

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Edge Case

- **Given** a task "Poll inbox" repeating every 30 minutes
- **When** Month view (a > 8-day range) is fetched and rendered
- **Then** the occurrences response contains one `DayBucket` per query-tz day for that task (`count: 48`, `interval_ms: 1800000`) and no raw instants for those days
- **And** each rendered day shows exactly one chip labeled "Poll inbox · every 30 min"
- **And** Day view (a ≤ 8-day range) shows individual timed entries

#### Scenario: Truncated expansion is visibly flagged

**Traces to**: User Story 2, Acceptance Scenario 3 (cap boundary)
**Category**: Edge Case

- **Given** a legacy task with `every_ms = 60000` (a minute-interval, valid under the existing floor)
- **When** a 7-day Week range is fetched (10,080 potential instants) and the per-task cap of 500 stops iteration
- **Then** the response for that task has `truncated: true` and instants covering only the first ~8 hours
- **And** the calendar renders a "more not shown" marker on the last covered day
- **But** no day beyond the marker renders as silently empty-and-authoritative

#### Scenario Outline: End conditions stop both rendering and firing

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Edge Case

- **Given** a task whose rule ends by `<end_condition>` and whose final occurrence is in the past
- **When** the operator views a future month
- **Then** no chips for that task render
- **And** the scheduler holds no armed job for it

**Examples**:

| end_condition |
|---------------|
| `COUNT=3`, all three fired |
| `UNTIL` = yesterday |

#### Scenario: Occurrences query failure degrades without blanking the grid

**Traces to**: User Story 2, Acceptance Scenario 1 (degrade path)
**Category**: Error Path

- **Given** the occurrences endpoint returns 500
- **When** the calendar loads
- **Then** due chips and one-shot fire chips still render
- **And** a non-blocking toast "Couldn't load recurring occurrences" appears

#### Scenario: Clicking a due chip keeps today's panel

**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Alternate Path

- **Given** a Board task with a due date renders a due chip on the calendar
- **When** the chip is clicked
- **Then** the existing task detail panel opens (status, description, dependencies intact)
- **But** the calendar event slide-over does not

#### Scenario: Dispatch failure does not kill the series

**Traces to**: User Story 2, Acceptance Scenario 4 (scheduler liveness); FR-014
**Category**: Error Path

- **Given** a nightly RRULE task whose dispatch fails at fire time (e.g. provider down)
- **When** `RunScheduled` returns
- **Then** the next night's occurrence is armed anyway (re-arm on the error path; no retry backoff)
- **And** a WARN with the task id and occurrence instant is logged
- **And** the calendar's rendered future occurrences remain truthful

#### Scenario: Title-only edit leaves the schedule untouched

**Traces to**: User Story 2, Acceptance Scenario 5 (series edit — anchor invariance, FR-024)
**Category**: Edge Case

- **Given** a biweekly `COUNT=10` series that has already consumed 4 occurrences
- **When** the operator opens it from the calendar, changes only the title, and saves
- **Then** the stored trigger is byte-identical to before (same `rrule`, `dtstart_ms`, `tz`)
- **And** the remaining occurrence count and week parity are unchanged
- **But** changing the weekday and saving re-anchors at the new rule's next occurrence and restarts COUNT, with the summary stating so before save

#### Scenario: Editing a task while it is firing leaves exactly one armed job

**Traces to**: User Story 2, Acceptance Scenario 5 (concurrency, FR-014)
**Category**: Edge Case

- **Given** an RRULE task whose fire is in flight
- **When** the operator saves a different repeat rule for it during the fire
- **Then** after the fire completes, exactly one job is armed — at the new rule's next occurrence
- **But** the stale re-arm of the old rule does not register (generation guard)

#### Scenario: Recurring tasks are absent from Board and List

**Traces to**: User Story 3, Acceptance Scenarios 1–2
**Category**: Happy Path

- **Given** Ops contains a manual task, a `once` task, and a recurring task
- **When** the operator opens the Board and then the List
- **Then** both show exactly the manual and `once` tasks
- **But** the recurring task appears in neither

#### Scenario: Board create form no longer offers recurring triggers

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the Board's "New task" slide-over is open
- **When** the Trigger dropdown is opened
- **Then** the options are exactly "None (manual)" and "Once (at a time)"

#### Scenario: Generic detail panel guards against recurring tasks (defensive)

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Edge Case

- **Given** the generic task detail panel is rendered with an `every`/`recurring` task (forced via stale cache in the test — Board/List normally exclude them)
- **When** the panel renders its trigger section
- **Then** it shows a plain-English summary of the rule and an "Edit in workspace calendar" link
- **But** no raw cron input and no editable trigger controls render

#### Scenario: Agent filter narrows all task event kinds instantly

**Traces to**: User Story 4, Acceptance Scenarios 1–3
**Category**: Happy Path

- **Given** Mia has a recurring task and a due-dated task this week, and Jim has a `once` task
- **When** the operator selects "Mia" in the toolbar Agent dropdown
- **Then** only Mia's chips remain (occurrences and due chip), with no network request issued for the filtering
- **And** milestone chips remain visible

#### Scenario: Unassigned filter shows only agentless tasks

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** one task in Ops has no `agent_id`
- **When** "Unassigned" is selected
- **Then** only that task's events remain

#### Scenario: Legacy task opens to an old-format note and a fresh picker

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** a legacy task with `cron_expr = "0 9 * * MON"` (or any `every_ms` interval)
- **When** its chip is opened from the calendar
- **Then** the panel shows an "old schedule format" note with the task's next run time and an empty repeat picker
- **But** no cron expression is displayed and no picker controls are pre-filled

#### Scenario: Replacing a legacy rule overwrites to RRULE with an audit entry

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the legacy task from above, its panel open
- **When** the operator builds "every 2 weeks on Monday" in the picker and saves
- **Then** the stored trigger is `type: recurring` carrying `rrule` (and `dtstart_ms`, `tz` = browser zone) and no `cron_expr`
- **And** an audit entry records the task id, the old trigger, and the new rule
- **And** the scheduler's next armed fire matches the new rule

#### Scenario Outline: DST transitions follow the wall-clock policy

**Traces to**: User Story 2 (D7 / Timezone Semantics §3)
**Category**: Edge Case

- **Given** a daily task at `<local_time>` with `tz = "Europe/Berlin"` spanning `<transition>`
- **When** occurrences on both sides of the transition are expanded
- **Then** the transition-day occurrence is `<expected>`
- **And** every day fires exactly once

**Examples**:

| local_time | transition | expected |
|------------|-----------|----------|
| 09:00 | spring-forward (late March) | 09:00 local both sides; UTC instants differ by 1 h |
| 09:00 | fall-back (late October) | 09:00 local both sides; UTC instants differ by 1 h |
| 02:30 | spring-forward (02:00→03:00 gap) | fires once at the normalized **03:30** (02:30 shifted forward by the one-hour gap — Go normalization, NOT 03:00) |
| 02:30 | fall-back (02:00–03:00 repeats) | fires once, at the first 02:30 (pre-transition offset) |

#### Scenario: Monthly-31 clamp lands on the last day of short months

**Traces to**: User Story 1 (D5)
**Category**: Edge Case

- **Given** a task built as "monthly on day 31" starting July 31
- **When** occurrences for July–October are expanded
- **Then** they fall on Jul 31, Aug 31, Sep 30, Oct 31

#### Scenario: Invalid occurrences range is rejected

**Traces to**: User Story 2 (endpoint contract)
**Category**: Error Path

- **Given** the occurrences endpoint
- **When** it is called with `from_ms ≥ to_ms` (or a span over 400 days)
- **Then** the response is 400 with a validation message

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|-------|-------|---------|
| Unit | Go: `pkg/task` validation (bounded scan, bounds), RRULE expansion (DST, clamp, ends), projection math. TS: `RecurrenceEditor` state⇄RRULE, presets-from-date, summary + clamp special-case, `eventMapping` buckets/markers, exclusion + filter predicates | Logic in isolation |
| Integration | Go: REST create/update rrule paths, occurrences endpoint (bucketed shape, caps, empty shapes), scheduler re-arm across all exit paths + edit-during-fire race (fake clock). TS: slide-over flows, legacy fallback UI, degrade path (vitest + RTL) | Components together |
| E2E | Embedded-SPA Playwright: create biweekly task via calendar, chips render, filter by agent, board exclusion | Full user view |

> CI discipline: Go suites run on the ci-omnipus worker or via a single narrowly-scoped local test (`-tags goolm,stdjson -run '^TestName$' -p 1`) — never the full suite in the devpod (project rule).

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | `TestValidateTrigger_RruleRequiredSiblings` | Unit (Go) | Adversarial rules are rejected… (rows 6–7) | rrule without dtstart_ms/tz, both cron+rrule, neither, bad tz → `ErrValidation` |
| 2 | `TestValidateTrigger_RruleBoundedMinGap` | Unit (Go) | Server rejects a sub-minute rule…; Adversarial rows 1–2 | bounded-window scan: irregular-gap BYSECOND bypass rejected; regular 60 s accepted; `COUNT=1` trivially accepted |
| 3 | `TestValidateTrigger_RruleInputBounds` | Unit (Go) | Adversarial rows 3–5, 7 | FREQ=SECONDLY reject, foreign BYSECOND reject, >512-char reject, never-matching Feb-31 reject within 1 s, UNTIL<DTSTART reject |
| 4 | `TestRruleExpansion_WallClockDST` | Unit (Go) | DST transitions follow the wall-clock policy (all 4 rows) | 09:00 both transitions + 02:30 nonexistent/ambiguous pinned to the normative policy (normalization layer if rrule-go differs) |
| 5 | `TestRruleExpansion_Monthly31Clamp` | Unit (Go) | Monthly-31 clamp lands on the last day… | Jul 31→Sep 30 sequence |
| 6 | `TestRruleExpansion_EndConditions` | Unit (Go) | End conditions stop both rendering and firing (both rows) | COUNT exhausted and past-UNTIL both yield empty beyond the end |
| 7 | `TestOccurrences_LegacyCronAndEveryMs` | Unit (Go) | Legacy cron task renders…; Sub-daily task aggregates… (every_ms flavor) | gronx server-zone expansion; every_ms projection: first entry == armed `State.NextRunAtMS` (engine-agreement assertion), then +k·interval |
| 8 | `TestOccurrences_BucketingAndCaps` | Unit (Go) | Sub-daily task aggregates…; Truncated expansion is visibly flagged | >3/day bucketed in >8-day spans on **query-tz** days (incl. rule tz ≠ query tz row); raw in ≤8-day spans incl. the 169 h fall-back week; 500-instant cap + 10k iteration budget enforced during iteration + truncated flag; irregular rule → interval_ms null |
| 9 | `TestTriggerScheduler_RruleRearmAllPaths` | Integration (Go, fake clock) | Dispatch failure does not kill the series; End conditions stop… | re-arm on success, overlap-skip, dispatch-error, SpawnReset-error (nil returned to engine — no backoff); task-unreadable exit → ERROR + sweep is the recovery; retire at exhaustion; sweep re-arms + WARNs **both** true-orphan shapes (no job; job with nil `NextRunAtMS` + `Running` false) and does NOT re-arm the false orphan (fire in flight: `Running` true, blocking hook) — post-fire exactly one armed job (replace-by-task) |
| 10 | `TestTriggerScheduler_EditDuringFire` | Integration (Go) | Editing a task while it is firing… | deterministically forced interleaving: a test hook blocks the re-arm between hash check and registration while a PUT completes → exactly one armed job, for the new rule (mutex-atomic guard) |
| 11 | `TestTriggerScheduler_LegacyPathUnchanged` | Integration (Go) | Legacy cron task renders… (regression) | cron/every/at job translation byte-identical to pre-change (guards heartbeats) |
| 12 | `TestRestTasks_CreateRecurringRrule` | Integration (Go) | Operator creates a biweekly task…; Replacing a legacy rule… (audit) | POST/PUT rrule → 201/200 + audit entry on legacy replacement AND on RRULE→RRULE rule change; invalid variants (incl. `COUNT=100001`) → 400 with message |
| 13 | `TestRestTasks_OccurrencesEndpoint` | Integration (Go) | Invalid occurrences range…; Truncated expansion… | 200 bucketed shape, terminal + heartbeat-surface tasks omitted (selection predicate), zero-occurrence tasks omitted, empty = `[]`, 400 on bad range/span/`tz` (incl. `from == to`), `occurrences` sub-path wins over ID parsing, `taskReadLimiter` (240/min) returns 429 past the ceiling |
| 14 | `recurrenceEditor.compile.test.ts` | Unit (TS) | Editor state compiles… (all 6 rows); Saved day-31 rule reopens… | UI-state → RRULE incl. UNTIL end-of-day UTC form; clamp round-trip recognition; tz display value |
| 15 | `recurrenceEditor.presets.test.ts` | Unit (TS) | Preset dropdown is computed from the clicked date | anchor → preset labels incl. ordinal weekday |
| 16 | `legacyTriggerPanel.test.ts` | Unit (TS) | Legacy task opens to an old-format note and a fresh picker | legacy trigger (cron AND every_ms) → panel model shows old-format note + next-run time, empty picker state, NO cron string in the rendered output (D8: no reverse-parsing exists) |
| 17 | `eventMapping.occurrences.test.ts` | Unit (TS) | Weekly task renders…; Sub-daily task aggregates…; Truncated expansion…; Clicking a due chip… | instants/buckets/marker → chips (labels from interval_ms, "N×/day" when null); occurrence chips `editable:false`; due/fire chips unchanged (regression rows) |
| 18 | `boardListExclusion.test.ts` | Unit (TS) | Recurring tasks are absent from Board and List | predicate on trigger.type for BoardView/ListView |
| 19 | `calendarAgentFilter.test.ts` | Unit (TS) | Agent filter narrows…; Unassigned filter… | filter predicate incl. milestones-exempt rule |
| 20 | `CalendarEventSlideOver.test.tsx` | Integration (TS) | Operator creates a biweekly…; Event slide-over opens with asserted defaults; "Does not repeat" save…; Title-only edit leaves the schedule untouched; Server rejects… inline; End date earlier… | RTL flows: default state ("Does not repeat", browser-zone label), rule build + save body, once-save defaults, title-only edit → byte-identical trigger payload / rule change → re-anchor summary shown, 400 → inline error, end-date disabled |
| 21 | `CalendarLegacyReplace.test.tsx` | Integration (TS) | Legacy task opens to an old-format note…; Replacing a legacy rule overwrites to RRULE…; Generic detail panel guards against recurring tasks… | RTL flow: legacy task chip → panel with old-format note (no cron string) → build new rule → save payload is `type: recurring` + rrule + browser-zone `tz`, old config keys gone; TaskDetailPanel defensive summary + calendar link when force-fed a recurring task |
| 22 | `CalendarScreen.occurrencesDegrade.test.tsx` | Integration (TS) | Occurrences query failure degrades… | mock 500 → grid renders due/fire chips + toast |
| 23 | `CreateTaskSlideOver.test.tsx` (update) | Integration (TS) | Board create form no longer offers recurring triggers | existing recurring-path tests replaced; non-recurring rows unchanged |
| 24 | `e2e: calendar-recurrence.spec.ts` | E2E | Operator creates a biweekly…; Weekly task renders…; Agent filter…; Recurring tasks absent from Board | embedded-SPA happy-path sweep |

### Test Datasets

#### Dataset: RRULE validation (server, `ValidateTrigger`)

| # | Input (config for type=recurring) | Boundary Type | Expected | Traces to | Notes |
|---|------|---------------|----------|-----------|-------|
| 1 | `rrule=FREQ=WEEKLY;BYDAY=MO`, dtstart, tz | happy | accept | Operator creates a biweekly task… | minimal valid |
| 2 | `rrule` present, `cron_expr` present | error | 400 | Adversarial rules… row 6 | exactly-one rule source |
| 3 | neither `rrule` nor `cron_expr` | error | 400 | same family | required |
| 4 | `rrule` valid, missing `tz` | error | 400 | same family | siblings required |
| 5 | `tz="Not/AZone"` | error | 400 | same family | unloadable zone |
| 6 | `rrule=FREQ=MINUTELY` (60 s apart) | boundary (min) | accept | Server rejects a sub-minute rule… | exactly at floor |
| 7 | `FREQ=DAILY;BYHOUR=9;BYMINUTE=0;BYSECOND=0,30`, dtstart 09:00:15 | adversarial | 400 | Adversarial rules… row 1 | rejected by §2 foreign-BYSECOND (its first pair is ≈24 h — a first-pair check alone would pass it; the §4 scan is defense-in-depth behind §2) |
| 8 | `FREQ=MINUTELY;BYSECOND=0,30` | adversarial | 400 | Adversarial rules… row 2 | 30 s gaps — rejected by §2 foreign-BYSECOND |
| 8a | `COUNT=100001` | boundary (max+1) | 400 | Adversarial rules… (validation family) | COUNT cap §6 |
| 9 | `FREQ=SECONDLY` | error | 400 | Adversarial rules… row 3 | frequency rejected outright |
| 10 | `FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=31` | adversarial | 400 in <1 s | Adversarial rules… row 4 | liveness bound, no stall |
| 11 | 10 KB rrule string | boundary (max+1) | 400 | Adversarial rules… row 5 | ≤512-char bound |
| 12 | `UNTIL` = dtstart − 1 day | error | 400 | Adversarial rules… row 7 | |
| 13 | `COUNT=1` | boundary (min occurrences) | accept | Server rejects… (trivial-pass rule) | <2 occurrences trivially satisfy floor |
| 14 | `rrule="FREQ=BOGUS"` | error | 400 | same family | parse failure |
| 15 | legacy `cron_expr="0 9 * * MON"` alone | regression | accept (unchanged) | Legacy cron task renders… | old path intact |

#### Dataset: Occurrence expansion (server)

| # | Input | Boundary Type | Expected | Traces to | Notes |
|---|-------|---------------|----------|-----------|-------|
| 1 | weekly MO 09:00, 4-Monday month range | happy | 4 instants, Mondays 09:00 rule-tz | Weekly task renders… | |
| 2 | daily 09:00 Berlin, both DST weeks | edge | all 09:00 local, UTC shifts 1 h | DST transitions… rows 1–2 | |
| 3 | daily 02:30 Berlin, spring-forward day | edge | fires once at the normalized 03:30 (02:30 + gap length) | DST transitions… row 3 | policy-pinned |
| 4 | daily 02:30 Berlin, fall-back day | edge | fires once at first 02:30 | DST transitions… row 4 | policy-pinned |
| 5 | monthly-31 clamp rule, Jul–Oct | edge | 31,31,30,31 | Monthly-31 clamp… | |
| 6 | COUNT=3, range after 3rd | boundary | task omitted from response | End conditions… row 1 | |
| 7 | UNTIL yesterday, future range | boundary | task omitted from response | End conditions… row 2 | |
| 8 | `every_ms=1800000` (30 min), 42-day range | happy | one DayBucket/day, count 48, interval_ms set | Sub-daily task aggregates… | no raw instants those days |
| 9 | `every_ms=1800000`, 1-day range | happy | 48 raw instants | Sub-daily task aggregates… (Day view) | ≤ 8-day span (detail mode) |
| 10 | `every_ms=60000`, 7-day range | boundary (cap) | 500 instants + truncated | Truncated expansion… | cap during iteration |
| 11 | irregular rule `BYHOUR=9,11,13,15` ×2 days span >7 days… | edge | bucket `interval_ms: null` → client "4×/day" | Sub-daily task aggregates… (label fallback) | 4/day > 3 threshold |
| 12 | `from_ms ≥ to_ms` (incl. `from == to`) | error | 400 | Invalid occurrences range… | empty half-open range is a 400, not an empty 200 |
| 13 | range span 401 days | boundary (max+1) | 400 | Invalid occurrences range… | |
| 14 | legacy cron `0 9 * * MON` same range as #1 | regression | instants match the scheduler's own fire computation | Legacy cron task renders… | engines agree |
| 15 | workspace with zero recurring tasks | boundary (empty) | `[]` (never null) | endpoint contract | zod shape |
| 16 | rule `tz=Asia/Tokyo` sub-daily, query `tz=Europe/Berlin`, 42-day span | edge | buckets on **Berlin** day boundaries, >3/day evaluated per Berlin day | Sub-daily task aggregates… | viewer-zone authority (MAJ-202) |
| 17 | valid plain `FREQ=MINUTELY` (regular), 400-day span | boundary (budget) | arithmetically-derived buckets, `truncated: false`, bounded server time | Sub-daily task aggregates… | regular ⇒ MUST-arithmetic, zero iteration (MAJ-303) |
| 17a | irregular dense rule (`FREQ=MINUTELY;BYHOUR=6,7,…,22` shaped to exceed 10k over the span) | boundary (budget) | `truncated: true` at the 10k iteration budget; marker rendered | Truncated expansion… | only irregular rules consume the budget |
| 17b | terminal (`done`) recurring task in range | boundary (selection) | task omitted from response | endpoint contract | scheduler-parity predicate (MAJ-302) |
| 17c | `surface: heartbeat` recurring task in range | boundary (selection) | task omitted from response | endpoint contract | never rendered (MAJ-302) |
| 17d | regular dense rule, DTSTART 2 years before the queried range | boundary (aged anchor) | correct in-range results in bounded server time (O(1) fast-forward, no DTSTART walk) | Sub-daily task aggregates… | MAJ-306 |
| 18 | fall-back Week range (7 d + 1 h = 169 h) | boundary (span) | raw instants for all days (detail mode: ≤ 8×24 h) | Sub-daily task aggregates… (Day view leg) | MIN-201 |
| 19 | `every_ms` task, range fully in the past | boundary | task omitted (forward-only projection) | endpoint contract | MIN-204 |
| 20 | `BYHOUR=2,3;BYMINUTE=30` across the spring-forward day | edge | collision day yields ONE instant (identical normalized instants de-duplicated) | DST transitions… | MIN-209 |

#### Dataset: Editor compile (TS, `recurrenceEditor.compile.test.ts`)

Rows = the Scenario Outline Examples table (6 rows, incl. the literal UNTIL end-of-day UTC value) **plus**:

| # | Input | Boundary Type | Expected | Traces to | Notes |
|---|-------|---------------|----------|-----------|-------|
| 7 | interval=0 typed in stepper | boundary (min-1) | clamped to 1, no invalid state | Editor state compiles… | prevention over correction |
| 8 | interval=100 | boundary (max+1) | clamped to documented max 99 | same | max = 99, documented |
| 9 | weekly with zero weekdays toggled | error-prevented | Save disabled + hint | same | unreachable invalid state |
| 10 | "After N occurrences" N=1 | boundary (min) | `COUNT=1` | same | |
| 11 | saved clamp form reopened | round-trip | picker shows "day 31" mode + clamp summary | Saved day-31 rule reopens… | MAJ-012 |
| 12 | title with unicode + 300 chars | edge | accepted; chip truncates with ellipsis | Weekly task renders… | display only |
| 13 | edit session: open a saved series, change title only, save | round-trip | trigger in the save payload byte-identical to the stored one | Title-only edit leaves the schedule untouched | FR-024 anchor invariance |

### Regression Test Requirements

Modifying existing functionality — behaviours that MUST be preserved:

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|--------------------|---------------|---------------------------|-------|
| Legacy cron/every/at triggers fire unchanged (incl. heartbeat surface) | `pkg/agent` trigger tests | Yes — `TestTriggerScheduler_LegacyPathUnchanged` | HIGH-risk seam |
| `once` fire chips + due chips render & drag-reschedule; due/fire chip click opens today's panel | calendar spec tests | Yes — regression rows in `eventMapping.occurrences.test.ts` (test 17) | occurrence events must not regress F-05/F-06 drag rules or panel routing |
| Board/List behaviour for manual/`once` tasks (dnd, rollups, milestone filter) | `BoardViewDnd.test.tsx`, `BoardViewRollup.test.tsx` | No — must continue passing unchanged | exclusion predicate is additive |
| Generic form still creates manual/`once` tasks | `CreateTaskSlideOver.test.tsx` | Updated (recurring rows removed), non-recurring rows unchanged | |
| Task validation 400 mapping (`isTaskValidationErr`) | gateway task tests | No — reused by new paths | |
| `verify-contracts` drift gate | CI | No — must stay green after regen | Constraint #8 |

---

## Functional Requirements

- **FR-001**: The calendar MUST provide a calendar-specific create/edit slide-over (event-style: title, agent, date & time with visible timezone, repeat). Routing: **occurrence/aggregated chips and calendar-created tasks open this slide-over; due chips and `once` fire chips keep today's task-panel behavior.** Board/List keep the generic form.
- **FR-002**: The recurrence editor MUST offer date-aware presets (Does not repeat / Daily / Weekly on \<weekday\> / Monthly on the \<nth weekday\> / Annually on \<date\> / Every weekday / Custom…).
- **FR-003**: The Custom editor MUST support frequency (minutes, hours, days, weeks, months, years), interval 1–99, weekday multi-toggle (weekly), day-of-month vs nth-weekday modes (monthly), and end conditions Never / On date / After N occurrences.
- **FR-004**: The editor MUST display a live plain-English summary of the current rule at all times, including the special-cased clamp wording of FR-007.
- **FR-005**: Saved repeat rules MUST be stored as RFC 5545 RRULE in `trigger.config.rrule` with required `dtstart_ms` and IANA `tz`. The raw-cron input MUST be removed from all task forms; recurring-trigger **editing** exists only in the calendar editor (the generic detail panel carries the FR-023 defensive read-only summary).
- **FR-006**: The server MUST validate RRULE triggers per the normative Validation section: exactly-one-of cron/rrule, required siblings, parseability, input bounds (≤512 chars; no `FREQ=SECONDLY`; no foreign `BYSECOND`), `UNTIL ≥ DTSTART`, `COUNT ≤ 100,000`, the bounded-window minimum-gap scan (min gap ≥ 60 s over the first 60 occurrences or 366 days; <2 occurrences trivially pass; retained as defense-in-depth — §2's hard rejects are the operative sub-minute mechanism), and the 5-year liveness bound — completing within ~1 s and rejecting with 400; the slide-over MUST surface that message inline at the Repeat section.
- **FR-006a**: "Ends on date D" MUST compile to `UNTIL = D 23:59:59 in the rule's tz, converted to UTC (Z-suffixed)` — inclusive end-of-day.
- **FR-007**: The editor MUST compile "monthly on day N" (N ≥ 29) to the canonical clamp form (D5), MUST recognize that form on reopen (rendering "day N" mode), and MUST summarize it as "Monthly on day N (moved to the last day in shorter months)" — never a BYMONTHDAY list.
- **FR-008**: Occurrence expansion MUST be server-side via `GET /api/v1/tasks/occurrences` per the normative endpoint section: required viewer-zone `tz` param as the bucketing day-boundary authority; half-open `[from_ms, to_ms)`; bucketed response (>3 occurrences per query-tz day → `DayBucket` when span > 8×24 h; raw instants for all days when span ≤ 8×24 h); caps enforced during iteration — 500 instants/task AND a 10,000-computed-occurrence total budget/task/request covering bucket enumeration (arithmetic derivation REQUIRED for provably regular triggers; aged-DTSTART fast-forward rules per the endpoint section) — with `truncated`; expanding only non-terminal, non-heartbeat tasks (the `OnTaskUpserted` predicate); zero-occurrence tasks omitted; `[]` never null; 400 on invalid ranges (from ≥ to, span > 400 days) or unloadable `tz`; guarded by the new `taskReadLimiter` (240/min); covering `rrule`, legacy `cron_expr` (server zone), and `every_ms`; the literal `occurrences` sub-path matched before `HandleTasks` ID parsing.
- **FR-008a**: `every_ms` expansion MUST be the documented forward-only projection — first occurrence equals the live job's `NextRunAtMS`, subsequent at fixed intervals, occurrences before *now* omitted (no backward extrapolation) — re-baselining after fires/restarts; the first entry MUST match the armed engine state.
- **FR-009**: The calendar MUST render the expanded occurrences (D2): individual chips for raw instants, one aggregated chip per `DayBucket` (label from `interval_ms`, or "{count}×/day" when null; tooltip "first at {time}" from `first_ms`), and a visible "more not shown" marker on the last covered day whenever `truncated` is true.
- **FR-010**: Recurrence MUST honor the Timezone Semantics section: wall-clock in the rule's `tz`; spring-forward nonexistent times resolve forward, fall-back ambiguous times take the first occurrence, each occurrence fires exactly once.
- **FR-011**: Workspace Board and List MUST exclude tasks with `trigger.type ∈ {every, recurring}`; the generic create form MUST offer only manual and `once` triggers (the `every` type remains valid on the wire/API but is no longer produced by any form).
- **FR-012**: Recurring occurrence and aggregated chips MUST open the calendar slide-over in series-edit mode and MUST NOT be draggable; due and `once` fire chips keep their existing panel and drag behavior.
- **FR-013**: Legacy triggers (`cron_expr` / `every_ms`, any wire-legal value down to the 1000 ms floor at `pkg/task/store.go:342-344`) MUST keep firing unchanged without migration and MUST render occurrences on the calendar (server expansion; `every_ms` per FR-008a). Per D8 there is **no reverse-mapping and no cron display**: opening a legacy task shows the "old schedule format" note + next run time + a fresh picker; saving writes `type: recurring` with the new RRULE (browser-zone anchor, Timezone §1) — fire times MAY shift, accepted. `rrule` keys never appear on non-`recurring` types. Note: a replacement rule is subject to the 60 s floor, so a sub-minute legacy interval cannot be reproduced post-replacement (it keeps firing only while never replaced).
- **FR-014**: The scheduler MUST implement the normative Scheduler rules: re-arm on every readable-task `RunScheduled` exit (success, overlap-skip, dispatch error, `SpawnReset` error; task-unreadable exits log ERROR and defer to the sweep), no retry-backoff participation (next occurrence is the retry, WARN logged), the **mutex-atomic** trigger-generation guard for edit-during-fire (exactly one armed job), skip-not-replay for missed occurrences with COUNT consumed by calendar time, boot `Reconcile` plus the 5-minute recovery-sweep ticker with the "armed = job exists ∧ `NextRunAtMS` non-nil ∧ ≥ now" predicate, and byte-identical legacy translation.
- **FR-015**: The calendar toolbar MUST provide an Agent filter (All agents default, roster entries, Unassigned) applied client-side with no refetch; milestone chips are exempt.
- **FR-016**: All wire changes (TaskTrigger config keys, TaskOccurrenceSet) MUST follow the contract-first process (schema → gen-contracts → generated types only, one atomic commit) and keep `make verify-contracts` green (Constraint #8).
- **FR-017**: The occurrences query failing MUST degrade non-blockingly (grid renders remaining events + toast), matching the existing calendar degrade pattern.
- **FR-018**: The binary MUST embed IANA tzdata (`time/tzdata`) so `tz` resolution works on minimal systems (Constraint #1 single-binary).
- **FR-019**: The editor SHOULD prevent invalid states at input time (end-date-before-start disabled, zero-weekday save disabled, interval clamped 1–99) rather than relying on server errors.
- **FR-020**: The system MAY show the next 2–3 upcoming fire times inside the slide-over, sourced from the occurrences endpoint (small forward range) — never computed client-side.
- **FR-021**: The rule `tz` MUST default to the browser's IANA zone and be displayed read-only beside the time in the editor (Timezone Semantics §1). No `server_tz` wire exposure exists (dropped under D8 — legacy times are never zone-labeled in the UI).
- **FR-022**: Every save that changes a recurrence trigger MUST emit an audit entry (task id, prior trigger, new trigger) via the existing audit pipeline — legacy→RRULE conversions AND RRULE→RRULE rule changes alike, so a re-anchor that moves every future fire is reconstructible after the fact.
- **FR-023**: **Defensive guard** (D10 — no Command Center surface exists): if the generic task detail panel (`TaskDetailPanel`, opened from Board/List) ever receives an `every`/`recurring` task, it MUST render the trigger as a read-only plain-English summary with an "Edit in workspace calendar" link — no trigger editing outside the calendar, ever. In PR 1 the panel moves to `src/components/workspaces/` and the legacy `command-center/` folder is deleted.
- **FR-024**: Edit-mode anchor semantics **for already-RRULE triggers**: opening an existing series MUST display its stored anchor; Save MUST preserve the trigger **byte-identical** when no recurrence or time field was touched (a title-only edit never re-anchors `dtstart_ms`, never restarts COUNT, never shifts biweekly phase); when recurrence/time fields DO change, the rule re-anchors at the new rule's next occurrence ≥ now and COUNT restarts — stated visibly in the panel's summary before saving. A save of a **legacy** (`cron_expr`/`every_ms`) trigger is governed by US-5.3 instead: the panel offers only a fresh rule, so any save replaces the trigger outright (D8, no equivalence guarantees) — FR-024's byte-identical rule never applies to the legacy class.

---

## Success Criteria

- **SC-001**: A user can create "every 2 weeks on Monday at 09:00, ends after 10 times" entirely through UI controls — zero characters of cron typed — and the task's stored trigger round-trips through `GET /api/v1/tasks/{id}` as a valid RRULE.
- **SC-002**: For a seeded month with a weekly task, a monthly-31 task, and an every-30-min task, the calendar renders exactly the server's expansion: 4 weekly chips, the clamp on Sep 30, and one aggregated bucket chip per day for the sub-daily task — with zero client-side recurrence math.
- **SC-003**: Board and List render 0 tasks with `every`/`recurring` triggers while `GET /api/v1/tasks` still returns them (presentation-only split).
- **SC-004**: Selecting an agent in the calendar filter changes the rendered event set with **zero** additional network requests (verified via devtools/Playwright network log).
- **SC-005**: A pre-existing `cron_expr` task fires at the same instants before and after deploying this feature (scheduler regression test green), and renders occurrences on the calendar without data migration.
- **SC-006**: DST tests: 09:00 wall-clock stability plus the 02:30 nonexistent/ambiguous policies all pass on both transition days (unit tests green, pinned to the normative policy).
- **SC-007**: All quality gates pass: `npm run typecheck`, `npx vitest run`, `make verify-contracts`, gofmt=0, golangci-lint, Go tests via ci-omnipus, govulncheck — 0 findings.
- **SC-008**: Every adversarial validation row (irregular-gap bypass, never-matching, oversized, SECONDLY) is rejected with 400 in under 1 second each.
- **SC-009**: With a dispatch failure injected on one fire, the series' next occurrence still fires (integration test), and a WARN with the task id is present in the log.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|------------------|--------------|
| FR-001 | US-1, US-2 | Operator creates a biweekly task…; Clicking a due chip keeps today's panel | CalendarEventSlideOver.test.tsx (20); eventMapping.occurrences.test.ts (17); e2e (24) |
| FR-002 | US-1 | Preset dropdown is computed from the clicked date | recurrenceEditor.presets.test.ts (15) |
| FR-003 | US-1 | Operator creates a biweekly task…; Editor state compiles… | recurrenceEditor.compile.test.ts (14) |
| FR-004 | US-1 | Operator creates a biweekly task… (summary); Saved day-31 rule reopens… (clamp wording) | CalendarEventSlideOver.test.tsx (20); recurrenceEditor.compile.test.ts (14) |
| FR-005 | US-1, US-3, US-5 | Operator creates a biweekly task…; Generic detail panel guards…; Replacing a legacy rule… | TestRestTasks_CreateRecurringRrule (12); CalendarLegacyReplace.test.tsx (21) |
| FR-006 | US-1 | Server rejects a sub-minute rule…; Adversarial rules are rejected… (all rows) | TestValidateTrigger_RruleRequiredSiblings (1); TestValidateTrigger_RruleBoundedMinGap (2); TestValidateTrigger_RruleInputBounds (3); CalendarEventSlideOver.test.tsx (20, inline error) |
| FR-006a | US-1 | Editor state compiles… (row 6, literal UNTIL) | recurrenceEditor.compile.test.ts (14) |
| FR-007 | US-1, US-5 | Monthly-31 clamp…; Saved day-31 rule reopens… | TestRruleExpansion_Monthly31Clamp (5); recurrenceEditor.compile.test.ts (14, row 11) |
| FR-008 | US-2 | Sub-daily task aggregates…; Truncated expansion…; Invalid occurrences range…; Legacy cron task renders… | TestOccurrences_BucketingAndCaps (8); TestRestTasks_OccurrencesEndpoint (13); TestOccurrences_LegacyCronAndEveryMs (7) |
| FR-008a | US-2, US-5 | Sub-daily task aggregates… (every_ms flavor) | TestOccurrences_LegacyCronAndEveryMs (7, engine-agreement) |
| FR-009 | US-2 | Weekly task renders…; Sub-daily task aggregates…; Truncated expansion… | eventMapping.occurrences.test.ts (17); e2e (24) |
| FR-010 | US-2 | DST transitions follow the wall-clock policy (4 rows) | TestRruleExpansion_WallClockDST (4) |
| FR-011 | US-3 | Recurring tasks are absent…; Board create form no longer offers… | boardListExclusion.test.ts (18); CreateTaskSlideOver.test.tsx (23) |
| FR-012 | US-2, US-5 | …chip is clicked (US-2.5); Clicking a due chip…; Legacy task opens to an old-format note… | eventMapping.occurrences.test.ts (17, editable:false + routing); CalendarEventSlideOver.test.tsx (20) |
| FR-013 | US-5 | Legacy cron task renders…; Legacy task opens to an old-format note…; Replacing a legacy rule… | legacyTriggerPanel.test.ts (16); CalendarLegacyReplace.test.tsx (21); TestTriggerScheduler_LegacyPathUnchanged (11); TestOccurrences_LegacyCronAndEveryMs (7) |
| FR-014 | US-1, US-2, US-5 | Dispatch failure does not kill the series; Editing a task while it is firing…; End conditions stop… | TestTriggerScheduler_RruleRearmAllPaths (9); TestTriggerScheduler_EditDuringFire (10); TestRruleExpansion_EndConditions (6) |
| FR-015 | US-4 | Agent filter narrows…; Unassigned filter… | calendarAgentFilter.test.ts (19); e2e (24) |
| FR-016 | all | (process requirement) | make verify-contracts in CI |
| FR-017 | US-2 | Occurrences query failure degrades… | CalendarScreen.occurrencesDegrade.test.tsx (22) |
| FR-018 | US-1, US-2 | DST transitions… (zone loading precondition) | TestRruleExpansion_WallClockDST (4, minimal-env run on CI worker) |
| FR-019 | US-1 | End date earlier than start…; editor dataset rows 7–10 | recurrenceEditor.compile.test.ts (14); CalendarEventSlideOver.test.tsx (20) |
| FR-020 | US-1 | (optional; if implemented, asserted alongside FR-004 tests) | CalendarEventSlideOver.test.tsx (20) |
| FR-021 | US-1 | Operator creates a biweekly task… (tz label); Event slide-over opens with asserted defaults | recurrenceEditor.compile.test.ts (14); CalendarEventSlideOver.test.tsx (20) |
| FR-022 | US-5 | Replacing a legacy rule overwrites to RRULE with an audit entry | TestRestTasks_CreateRecurringRrule (12, audit assertion) |
| FR-023 | US-3 | Generic detail panel guards against recurring tasks (defensive) | CalendarLegacyReplace.test.tsx (21) |
| FR-024 | US-5 | Title-only edit leaves the schedule untouched | CalendarEventSlideOver.test.tsx (20); recurrenceEditor.compile.test.ts (14, editor dataset row 13) |

**Completeness check**: every FR (001–024 incl. 006a/008a) has ≥1 scenario and ≥1 concrete test row (numbers reference the 24-test plan); every BDD scenario appears in at least one row ("'Does not repeat' save…" and "Event slide-over opens with asserted defaults" under FR-001/FR-019 via test 20; "Title-only edit…" under FR-024).

---

## PR Slicing & Sequencing

| Slice | Scope | Contract change | Depends on |
|-------|-------|-----------------|-----------|
| **PR 1 — Calendar agent filter** (US-4) | `CalendarToolbar` dropdown + filter predicate in `CalendarScreen`; reuse `FilterSelect`. *(The D9/D10 dead-code deletion — `command-center/` folder incl. Schedules components + the api.ts schedules client, `TaskDetailPanel` moved to `workspaces/` — was executed directly on the branch on 2026-07-19, verified by typecheck + full vitest; the do-not-reintroduce guard lives in CLAUDE.md "Retired surfaces".)* | none | — |
| **PR 2 — RRULE model + engine** (US-1 backend, US-5 backend; FR-005/006/008/010/014/018/022) | `TaskTrigger.yaml` additive keys, `TaskOccurrenceSet.yaml` + endpoint, rrule-go, validation incl. bounds, scheduler re-arm rules + sweep, audit hook, tzdata embed. **RRULE ADR (incl. input bounds + downgrade statement) lands with this slice.** | yes (atomic with regen) | — |
| **PR 3 — Editor + calendar-only split** (US-1 UI, US-2, US-3, US-5 UI) | `RecurrenceEditor`, `CalendarEventSlideOver`, `eventMapping` occurrences/buckets/markers, board/list exclusion, generic-form trigger trim, legacy replace flow (old-format note + fresh picker, D8), defensive read-only summary in the generic detail panel (FR-023) | none beyond PR 2 | PR 2 |

Each slice passes the 7-reviewer gate before merging to the feature base; whole-epic gate before the final PR (project rule).

---

## Ambiguity Warnings

| # | What's Ambiguous | Resolution status |
|---|------------------|-------------------|
| 1 | Should the global **Command Center** task views also hide recurring tasks? | **Question dissolved (D10)** — no Command Center surface exists; `/tasks`/`/automations` are redirects. FR-023 survives only as a defensive guard on the generic detail panel. |
| 2 | Do **milestone** chips respect the agent filter? | **Resolved: exempt** (milestones have no agent) — FR-015/US-4.3. |
| 3 | Are sub-daily frequencies (minutes/hours) kept in the Custom editor? | **Resolved: YES** — operator confirmed 2026-07-19. |
| 4 | One-way conversion on edit-save of legacy tasks? | **Superseded by D8** — legacy tasks are replaced outright (fresh rule, audit entry), never converted-with-equivalence. |
| 5 | Interval stepper maximum | **Resolved in R2**: 99, documented (FR-003, editor dataset row 8). |
| 6 | Reverse-mapping legacy cron into picker state? | **Deleted by D8** (operator: "no migration, no backward compatibility") — no reverse-mapping exists; the fresh-rule replacement path is the only legacy edit path. |

---

## Evaluation Scenarios (Holdout)

> **Post-implementation evaluation only.** Not visible to the implementing agent; excluded from the TDD plan and traceability matrix. For the operator / a separate evaluator against the real embedded-SPA gateway.

### Scenario: Payday reminder
- **Setup**: Fresh workspace; today is mid-month.
- **Action**: From the calendar, create "Payroll check" repeating monthly on the 31st at 08:00, assigned to Jim, no cron typed. Navigate through the next 4 months.
- **Expected outcome**: A chip appears in every month — on the 31st where it exists, on the month's last day otherwise (e.g. Sep 30). The chip opens the calendar-style panel showing "Monthly on day 31 (moved to the last day in shorter months)".
- **Category**: Happy Path

### Scenario: Sprint cadence
- **Setup**: Any workspace.
- **Action**: Create "Sprint report" every 2 weeks on Friday, ends after 6 occurrences. Count the chips across the next 4 months.
- **Expected outcome**: Exactly 6 chips, 14 days apart, all Fridays; nothing after the 6th.
- **Category**: Happy Path

### Scenario: The board stays clean
- **Setup**: Workspace with 2 normal tasks on the board.
- **Action**: Create a daily recurring task from the calendar; then open Board and List; then open the Board's "New task" form; then try old URLs `/tasks` and `/automations`.
- **Expected outcome**: Board/List still show exactly the 2 normal tasks; the recurring task is only on the calendar; the Board form offers no repeating trigger option; the old URLs redirect into the workspace Board and Calendar respectively (no Command Center, no Automations screen).
- **Category**: Happy Path

### Scenario: Impossible schedule
- **Setup**: Calendar event panel open; plus an API client (curl) with a valid token.
- **Action**: In the UI, try to build "repeat weekly, ends yesterday". Via the API, POST a recurring task with `FREQ=DAILY;BYHOUR=9;BYMINUTE=0;BYSECOND=0,30` anchored at 09:00:15.
- **Expected outcome**: Yesterday is unselectable in the end-date picker. The API request is rejected with 400 quickly (not accepted, not hanging) — the sneaky 30-second rule does not slip past validation.
- **Category**: Error

### Scenario: Broken backend, calm calendar
- **Setup**: Stop/deny the occurrences endpoint (e.g. firewall the route or patch a 500).
- **Action**: Load the calendar.
- **Expected outcome**: The grid still renders with due/one-shot chips; a single non-blocking toast mentions recurring occurrences; no blank screen, no console error storm.
- **Category**: Error

### Scenario: Clock change weekend
- **Setup**: Two daily tasks in a DST-observing zone: one at 09:00, one at 02:30; system date near the transition (or inspect expanded times around it).
- **Action**: Compare displayed/fired times for the days before, on, and after the change.
- **Expected outcome**: The 09:00 task stays at 09:00 local throughout. The 02:30 task fires exactly once on the transition day (shifted forward on spring-forward; at the first 02:30 on fall-back) — never zero times, never twice.
- **Category**: Edge Case

### Scenario: Grandfathered cron survives an upgrade — and an unplugged provider
- **Setup**: A task created on a pre-feature build with `cron_expr: "0 7 * * 2"` (Tuesdays 07:00).
- **Action**: Upgrade the binary; open the calendar; open the task from a chip; close without saving. Separately: break the LLM provider key, let one fire fail, restore the key.
- **Expected outcome**: Chips appear on Tuesdays; the panel shows an "old schedule format" note with the next Tuesday's run time and an empty repeat picker — no cron text anywhere; the stored trigger is unchanged after closing. After the failed fire, the next Tuesday still fires (the series did not die), and the gateway log contains a WARN for the failed one.
- **Category**: Edge Case

---

## Assumptions

- Single-operator context (no multi-user calendar permissions) — matches current product state.
- `rrule` npm and `rrule-go` are acceptable new dependencies (BSD-3 / MIT; pure Go, no CGo) — flagged in Integration Boundaries; ADR ratifies.
- The FullCalendar `listWeek` (Agenda) view treats occurrence events like any timed event — no special handling beyond the shared mapping.
- The task's separate `due` field is unrelated to recurrence and unchanged.
- Heartbeat-surface tasks remain out of every user-facing surface (already excluded via `surface !== 'user'`).
- Editor summary text is English-only in v1 (the SPA has no i18n layer today); the clamp special-case uses our own formatter, rrule.js `toText()` covers the rest.
- Agent tools / raw API may continue creating `cron_expr` recurring tasks (open class, OBS-003) — a follow-up issue steers the `create_task` tool description toward RRULE.

## Clarifications

### 2026-07-19 (operator Q&A, recorded verbatim decisions)

- Q: Prettier cron-compile UI vs full RRULE power? → A: **Full power incl. "every 2 weeks"** — adopt RRULE despite the bigger backend change (D1).
- Q: Show every occurrence or only the next run? → A: **Every occurrence** (D2).
- Q: Hide recurring from Board only, or Board + List? → A: **Both — calendar-only** (D3).
- Q: Which calendar filters in v1? → A: **Agent only** (D4).
- Q: Monthly on the 31st in a 30-day month? → A: **Run on the last day** (D5).
- Q: Sub-daily tasks flooding Month view? → A: **One chip per day**, real times in Week/Day (D6).
- Q: General shape? → A: Keep the slide-over pattern; the calendar's panel follows calendar-event conventions (incl. recurrence); the generic form stays for Board/List. Confirmed summary before spec: "Yes, go ahead."

### 2026-07-19 (Revision 2 — spec-level decisions from grill round 1, operator may override)

- Q: How is the min-interval guard made sound for RRULE? → A: Bounded-window minimum-gap scan (60 occurrences / 366 days) + hard rejects for SECONDLY/foreign BYSECOND + 5-year liveness bound (CRIT-001, MAJ-007).
- Q: How do sub-daily tasks coexist with the response cap? → A: Per-day buckets above the >3/day threshold in overview ranges; raw instants in ≤7-day ranges; visible truncation marker (CRIT-002).
- Q: How does the series survive failures? → A: Re-arm on every exit path, no retry backoff, generation guard, boot Reconcile + periodic detect-re-arm-WARN sweep (CRIT-003, MAJ-011).
- Q: Timezones? → A: Normative Timezone Semantics section — browser zone for new rules (shown read-only), server zone for legacy cron display/expansion/unchanged conversion, `server_tz` exposed on state, DST nonexistent→forward / ambiguous→first (MAJ-002/005/006).
- Q: What do non-recurring chips open? "Does not repeat" saves as? `every_ms` edit path? Command Center? → A: FR-001 routing; `once` trigger with generic-form defaults; every_ms reverse-maps + converts; CC read-only summary + link (MAJ-003/004/009/010).
- Q: Downgrade? → A: One-way door, accepted, documented in Operations & Rollback + the ADR (MAJ-014).

### 2026-07-19 (Revision 3 — spec-level decisions from grill round 2, operator may override)

- Q: Which limiter? → A: New `taskReadLimiter` 240/min on the occurrences endpoint only; task CRUD stays unlimited as today (MAJ-201).
- Q: Whose "day" for buckets? → A: The viewer's — required `tz` query param is the bucketing day-boundary authority (MAJ-202); detail/overview span boundary at 8×24 h (MIN-201).
- Q: Expansion work bound? → A: 10,000 computed occurrences/task/request incl. bucket enumeration; arithmetic counts for regular triggers (MAJ-203).
- Q: Sweep mechanics? → A: Dedicated 5-minute ticker in `TaskTriggerScheduler`; "armed" = job exists ∧ `NextRunAtMS` non-nil ∧ ≥ now (MAJ-204).
- Q: Spring-forward instant? → A: Go normalization — 02:30 → 03:30 across a one-hour gap; identical normalized instants de-duplicate to one fire (MAJ-205, MIN-209).
- Q: Guard atomicity? → A: Re-arm check+register+map-write and `OnTaskUpserted`'s full sequence share one mutex; test 10 forces the interleaving (MAJ-206).
- Q: Edit anchors? → A: FR-024 — title-only edits preserve the trigger byte-identical; recurrence changes re-anchor + restart COUNT, disclosed in the summary (MAJ-207).
- Q: Who verifies cross-zone stamping? → A: Test 21 asserts the save payload's `tz` both ways (MAJ-208).
- Q: `server_tz` exposure? → A: Authenticated callers only on `/api/v1/state` (OBS-201). `first_ms` consumer = aggregated-chip tooltip (OBS-202). Cross-split dependency chains: names resolve, rollups ignore trigger type (OBS-203).

### 2026-07-19 (Revision 4 — spec-level decisions from grill round 3, operator may override)

- Q: Sweep vs in-flight fire? → A: "armed" includes `State.Running`; every re-arm is replace-by-task in the rule-4 critical section (MAJ-301).
- Q: Which tasks does the endpoint expand? → A: Only tasks the scheduler would arm — non-terminal, non-heartbeat; client drops unjoinable task_ids (MAJ-302).
- Q: Does plain `FREQ=MINUTELY` truncate Month view? → A: No — arithmetic derivation is a MUST for regular triggers; only irregular rules consume the 10k budget (MAJ-303).
- Q: Legacy title-only edit? → A: Legacy saves ALWAYS convert (US-5.3); FR-024's byte-identical rule applies to already-RRULE triggers only (MAJ-304).
- Q: `every_ms=90000` / `30000`? → A: Only whole-minute multiples reverse-map; everything else takes the generalized read-only + Replace fallback; sub-minute is conversion-terminal (MAJ-305).
- Q: Aged DTSTART cost? → A: O(1) fast-forward for regular rules, period-anchored iteration for COUNT-free BY* rules, COUNT ≤ 100,000 bounds the rest (MAJ-306).
- Q: MINOR sweep? → A: Dataset row 3 → 03:30; §4 scan labeled defense-in-depth (rows 7–8 credit §2); `from ≥ to` → 400 everywhere; lock order scheduler→engine stated; row 9 note → 8-day; FR-022 audits RRULE→RRULE changes too (MIN-301–306). Expansion layer owns the 03:30 policy per ADR (OBS-301); occurrences query cache-keyed on tz (OBS-302); per-IP limiter + trust_xff note (OBS-303).

### 2026-07-19 (Revision 5 — operator directives)

- Q: Can users still type cron when creating a task? → A: No, nowhere — confirmed fully planned (both task forms lose the input; CC gets a read-only summary; the calendar picker never shows cron). Cron survives under the hood only (engine, API, heartbeats).
- Q: The Schedules screen with its raw-cron Custom mode? → A: Operator: "we do not have a scheduler screen anymore… do we have a dead page?" — verified: `SchedulesList.tsx`/`ScheduleFormSheet.tsx`/`cronUtils.ts` are unreachable dead code (import only each other); **deleted in PR 1** (D9). Backend entity + engine stay.
- Q: "Does not repeat" + time saves as? → A: **A task that runs then** (`once` trigger) — operator confirmed.
- Q: Fast repeats (every 30 min) in the picker? → A: **Keep** — operator confirmed.
- Q: Reverse-map old cron into the picker, or read-only + Replace? → A: Operator: "we do not migrate old tasks, no backward compatibility" → **D8**: no reverse-mapping, no conversion-equivalence machinery at all; legacy tasks keep firing + rendering, editing = fresh rule that overwrites; `server_tz` exposure dropped; tests 16/21 simplified accordingly.
