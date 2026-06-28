# Spec — Workspace Calendar (Outlook-style, FullCalendar) — **v2**

| | |
|---|---|
| **Status** | Revised (v2) — addresses both review passes; ready to re-`/grill-spec` |
| **Author** | Daniel Piatkowski |
| **Date** | 2026-06-28 |
| **Release phase** | v0.1.0 UAT fixes (branch `feat/0.1.0-uat-fixes`) — UI-only, no backend |
| **Supersedes** | The list-style `CalendarScreen.tsx` (month-grouped event list) |
| **Grounding** | FullCalendar v6 docs (`/docs/react`, `/docs/editable`, `/docs/date-clicking-selecting`, `/docs/css-customization`) + `robskinney/shadcn-ui-fullcalendar-example` source (read 2026-06-28) |

### Revision log — v1 → v2
Resolves the two review passes (`…-spec-review.md` = grill-spec **BLOCK**; `…-spec-uiux-review.md` = UI/UX **FAIL**). Every Critical and Major/Important is folded in:
- **Contract correctness:** `surface` is `user|heartbeat` not `system` (F-01); the reused create form emits a datetime, not a date-only `due` (F-02); jsdom can't render/drag FullCalendar → tests re-levelled to E2E (F-03); status→colour map pinned, `next`=muted (F-04); `once`-trigger PATCH sends the whole `TaskTrigger` (F-05); a precise TZ-safe date rule (F-06, F-09); controlled-open prefill ownership (F-07); named-export route seam corrected (F-08); `every`/`recurring` both deferred (F-10).
- **Accessibility:** keyboard/single-pointer reschedule path for tasks **and** milestones (C-1, C-5); per-status icon + near-black chip text (C-2, C-3); dialog focus management (C-4); loading-vs-empty state (I-1); milestone-failure toast (I-2); click-to-create affordance (I-3); mobile time-grid 44px chips + responsive toolbar (I-4, I-9); undo + success feedback (I-5, I-6); themed/focus-safe "+N more" popover (I-7); `aria-live` toasts (I-8).
- **Version:** pin `@fullcalendar/* 6.1.21` (rationale in §1; v7 deferred — F-13).

---

## 1. Overview

### Problem
The workspace **Calendar** tab is not a calendar. `CalendarScreen.tsx` renders a *list grouped by month*: it builds month sections only from events that exist (`:235` gates the grid behind `monthKeys.length > 0`). With **zero events** nothing draws except an empty message (`:221-233`). There is no week/day/agenda view and no direct manipulation.

### Goal
Replace it with an **Outlook/Google-style calendar** on **FullCalendar v6** that always renders a grid, offers **Month/Week/Day/Agenda**, supports **drag-to-reschedule** and **click-to-create**, opens existing slide-overs on event activation, and is themed to **The Sovereign Deep** — meeting **WCAG 2.2 AA**.

### Dependency choice (F-13)
Pin **`@fullcalendar/* 6.1.21`** (npm `latest`). Rationale: it is the current stable line, its React peer range includes `^19` (verified), and it matches the reference template. **v7.0.0** is published only under the `next` dist-tag and pulls a **`temporal-polyfill` runtime dependency** (Temporal-API migration) — deferred until it reaches `latest` and the polyfill cost is justified.

### Non-goal (this iteration)
No new backend/endpoints, no contract changes, no server-computed cron `next_fire_at`, no event *resize*, no `every`/`recurring` rendering (deferred — §5).

---

## 2. Actors
| Actor | Role |
|---|---|
| **Workspace user (operator)** | Views, switches views, drags to reschedule, clicks/keys to create/open. Only interactive actor. |
| **Agents** | Don't use this UI; their tasks/milestones appear as events. |
| **Gateway REST API** | Serves reads; accepts `PATCH /tasks/{id}`, `POST /tasks`, milestone update. Unchanged. |

---

## 3. Available Reference Patterns
| Source | Pattern | Mapping |
|---|---|---|
| FC `/docs/react` | `<FullCalendar ref>` + `getApi().changeView/next/prev/today`; `headerToolbar={false}` | `CalendarToolbar` drives the calendar; Sovereign-Deep controls |
| FC `/docs/editable` | `editable` + per-event `editable:false`; `eventDrop(info)`/`info.revert()`; `eventDurationEditable={false}` | drag-reschedule + revert; recurring/every absent; resize off |
| FC `/docs/date-clicking-selecting` | `dateClick`, `selectable`+`select` | open `CreateTaskSlideOver` prefilled |
| FC `/docs/css-customization` | `--fc-*` vars; v6 auto-injects base CSS via JS | `fullcalendar-theme.css` maps `--fc-*`→Sovereign-Deep |
| template `calendar.tsx` | `eventContent`/`dayCellContent`/`dayHeaderContent`; `nowIndicator`, `firstDay={1}`, bounded slots | custom chips (icon+dark text), today pill, Monday-first |
| template `calendar-nav.tsx` | custom toolbar driving `calendarRef` | blueprint for `CalendarToolbar` (lucide→Phosphor) |
| template `styles/calendar.css` | `--fc-*` + `.fc-*` `@apply` tweaks | reference for `fullcalendar-theme.css` |

> Consulted, not copied. We port Next.js+lucide → Vite/React 19/TanStack/Phosphor and wire real data.

---

## 4. Existing Codebase Context

### Symbols Involved
| Symbol | Role | Context |
|---|---|---|
| `screens/CalendarScreen.tsx` | **rewrite** | Keeps named export `CalendarScreen` + prop `{ workspaceId }`. |
| `routes/_app/workspaces.$workspaceId.calendar.tsx` | calls (unchanged) | **Named-export adapter** seam: `lazy(() => import('@/components/screens/CalendarScreen').then((m) => ({ default: m.CalendarScreen })))` — preserve the `.then(...)` mapping (it is **not** a default export) (F-08). |
| `workspaces/CreateTaskSlideOver.tsx` | **extend** | Controlled (`open`, `onOpenChange`, `workspaceId`). Add optional `initialDue?: string` (datetime-local) seeded via `useEffect([open, initialDue])`; queries already gate on `open`. Caller (`CalendarScreen`) owns `open` + the prefill (F-07). Existing callers unaffected (props optional). |
| `workspaces/taskFormFields.ts` `datetimeLocalToIso` (`:55`) | calls | `new Date(value).toISOString()` — **always emits a full datetime** (F-02). Drives the all-day decision in §7. |
| `workspaces/TaskDetailSlideOver.tsx` `({task,onClose})` | calls | Opened on task `eventClick`/Enter — also the keyboard reschedule path (C-1). |
| `lib/api.ts` `updateTask` (`:1548`) | calls | `PATCH /tasks/{id}` with `TaskUpdateRequest{ due?, clear_due?, trigger? }`. `trigger` is a **whole `TaskTrigger`** (F-05). |
| `lib/api.ts` `createTask` (`:1544`) | calls (via slide-over) | `POST /tasks`. |
| `lib/api.ts` `updateMilestone` (`:2807`) | calls | **`(workspaceId, milestoneId, body)` — 3 args** (F-09); `due_date` is a nullable string. |
| `lib/api.ts` `fetchTasks`/`fetchMilestones`, query keys | calls | data + cache keys for optimistic writes. |
| `store/ui.ts` toast | calls | error (`role="alert"`) + success/undo (`role="status"`) toasts (I-8). Verify it sets `aria-live`; if not, that's an added requirement. |
| `lib/calendar/eventMapping.ts` | **new (pure fn)** | `mapToCalendarEvents(tasks, milestones)` → `CalendarEvent[]`; TZ-safe date parsing; status→{bg,icon,text,editable}. Unit-tested. |
| `components/calendar/FullCalendarView.tsx` | **new** | Themed `<FullCalendar>` wrapper (co-located, not `ui/` — single consumer, F-17). |
| `components/calendar/CalendarToolbar.tsx` | **new** | Responsive Sovereign-Deep prev/next/today + view tabs + New task. |
| `components/calendar/MilestoneDatePopover.tsx` | **new** | Inline date-edit popover for milestone reschedule (keyboard/single-pointer path — C-5). |
| `styles/fullcalendar-theme.css` | **new** | `--fc-*` overrides + `.fc-*`/`.fc-popover` tweaks + reduced-motion. |

### Impact Assessment
| Symbol | Risk | d=1 | d=2 |
|---|---|---|---|
| `CalendarScreen.tsx` (rewrite) | **MEDIUM** | calendar route (named-export adapter) | tab shell; no existing calendar tests |
| `CreateTaskSlideOver.tsx` (+optional `initialDue`, open-effect seed) | **LOW** | `WorkspaceTasksTab`, `CalendarScreen` | `CreateTaskSlideOver.test.tsx` must pass unchanged (props optional) |
| `package.json`/lockfile (+6 `@fullcalendar/*`) | **LOW–MEDIUM** | Vite build, embedded SPA | FC libs land in the **calendar-route chunk** (SC-007) |

### Relevant Execution Flows
| Flow | Relevance |
|---|---|
| **Render** | route → lazy `CalendarScreen` → `useQuery` tasks+milestones → `mapToCalendarEvents` → `<FullCalendarView>`. Empty-fix lives here; loading vs empty distinguished (§7, I-1). |
| **Reschedule** | `eventDrop`/keyboard → optimistic cache write → `updateTask`/`updateMilestone` → success toast+undo (I-5/I-6) / `revert()`+error toast (FR-009). |
| **Create** | `dateClick`/`select` → `CalendarScreen` opens `CreateTaskSlideOver` (prefilled) → `createTask` → invalidate → event appears; focus returns to trigger (C-4). |
| **Open** | task `eventClick`/Enter → `TaskDetailSlideOver`; milestone click/Enter → `MilestoneDatePopover`. |

---

## 5. Scope

### In scope
- FullCalendar **Month/Week/Day/Agenda** (`dayGridMonth`/`timeGridWeek`/`timeGridDay`/`listWeek`).
- Events from **tasks** (`due`, `once`-trigger) and **milestones** (`due_date`).
- **Drag-to-reschedule** + a **keyboard/single-pointer reschedule path** (C-1) with optimistic update, **undo + success toast** (I-5/I-6) and **revert + error toast** on failure.
- **Click/slot-select to create** a task via `CreateTaskSlideOver` (prefilled, controlled by `CalendarScreen`).
- **Event activation:** task → `TaskDetailSlideOver`; milestone → `MilestoneDatePopover`.
- Per-status **icon + near-black chip text** (C-2/C-3); Sovereign-Deep theming; ≥44px touch targets incl. time-grid chips; responsive toolbar.

### Out of scope (deferred / future)
- **`every` and `recurring` triggers** — both **omitted in v1** (F-10): no client interval/cron expansion; a drifting "next" marker is noise. They render once server `next_fire_at` exists.
- New backend endpoints / contract changes; event resize; milestone creation from the calendar.
- `@fullcalendar/multimonth` (year view), `luxon`, FullCalendar v7.
- **No new telemetry/logging** (project no-telemetry); reschedule auditing is whatever the existing `PATCH /tasks` path records (F-12).

---

## 6. User Stories & Acceptance Criteria

### Canonical status → chip map (single source of truth — FR-005, resolves F-04/C-2/C-3)
Chip text is **`#0A0A0B` (near-black)** for all — every chip clears **WCAG AAA 7:1** (measured floor is `failed` red `#F87171` at **7.15:1**; gold 9.41, emerald 10.29, blue 7.78, amber 11.85, slate 7.72) (N-01). Each chip leads with a Phosphor icon (non-color cue — WCAG 1.4.1). Event id encodes kind: `task:{id}:due`, `task:{id}:fire`, `milestone:{id}` (F-19).

| Kind / status | chip bg | icon | draggable |
|---|---|---|---|
| **milestone** | Forge Gold `#D4AF37` | `Flag` | yes (→ `updateMilestone`) |
| task `done` | emerald `#34D399` | `CheckCircle` | yes |
| task `in_progress` | blue `#60A5FA` | `CircleNotch` | yes |
| task `blocked` | amber `#FBBF24` | `Prohibit` | yes |
| task `failed` | red `#F87171` | `XCircle` | yes |
| task `inbox`/`next`/`planning` | slate `#94A3B8` | `Circle` | yes |
| `once`-trigger "fires" chip (any status) | status bg above | `Clock` (overrides) | yes (→ trigger.at_ms) |

`next` is **muted/slate**, matching existing code (overrides v1's stray "gold"). `inbox`/`planning` = slate.

### US-1 — Always-on grid with my tasks & milestones — **P0**
*Always show a real calendar with my scheduled items, even when empty.*
**Why P0:** the headline bug + foundation. **Independent test:** open with 0 events and with events; grid renders both; events land on correct days.
1. **Given** no tasks/milestones, **When** I open Calendar, **Then** the current-month grid renders (day cells + weekday headers).
2. **Given** a `surface:"user"` task with a `due` in view, **Then** an all-day chip (status color + icon + near-black text) shows on the due day.
3. **Given** a task with `surface:"heartbeat"`, **Then** it is **not** shown. *(was `system` — that enum value does not exist; F-01.)*
4. **Given** a milestone with `due_date`, **Then** a gold `Flag` chip shows on that date.
5. **Given** tasks of varying status, **Then** each chip uses the **canonical map** above (color **and** icon).
6. **Given** the tasks/milestones query is still loading, **Then** the grid renders with a subtle loading affordance and the empty hint does **not** flash (I-1).

### US-2 — Switch Month/Week/Day/Agenda — **P0**
**Independent test:** switch each view; the period containing `calendarApi.getDate()` is preserved (F-11).
1. **Given** Month view on a date, **When** I pick Week, **Then** `timeGridWeek` renders the week containing the calendar's current date.
2. **Given** any view, **When** I click prev/next/today, **Then** the period moves by one unit / returns to today.
3. **Given** events, **When** I pick Agenda, **Then** `listWeek` lists them (themed empty text when none).
4. **Given** a phone-width viewport, **Then** the initial view is Month; all four remain available; the toolbar is two rows (nav+date / views+New task) (I-9).
5. **Given** a coarse pointer, **Then** toolbar controls **and** time-grid event chips have ≥44px targets (I-4).

### US-3 — Reschedule by drag or keyboard — **P1**
*Reschedule by direct manipulation, with a keyboard/single-pointer alternative.*
**Independent test:** drag a due task → reload shows new day; force a 500 → revert + toast; Tab to a chip, Enter → reschedule path opens.
1. **Given** a due task chip, **When** I drag it to another day, **Then** `updateTask` sends `due` as the dropped day's **local-midnight RFC3339 datetime** (the contract field is `format: date-time`; F-06) and it stays after refetch.
2. **Given** a `once`-trigger chip in Week/Day, **When** I drag it to a slot, **Then** `updateTask` sends the **whole `trigger`** = `{...task.trigger, config:{...task.trigger.config, at_ms:newMs}}` (preserving siblings — F-05).
3. **Given** a milestone chip, **When** I drag it, **Then** `updateMilestone(workspaceId, milestone.id, {due_date:'YYYY-MM-DD'})` is sent (F-09).
4. **Given** any reschedule **succeeds**, **Then** a success toast "Rescheduled to {date}" (`role="status"`) with a 5-second **Undo** appears (I-5/I-6); Undo restores the prior date.
5. **Given** any reschedule **fails**, **Then** the event reverts (`info.revert()`) and an error toast (`role="alert"`) appears (FR-009).
6. **Given** a reschedule, **Then** the optimistic cache patch moves the event instantly and a success (or error) toast confirms the outcome — the write is near-instant, so no separate per-chip spinner is required (I-6).
7. **Given** a focused task chip, **When** I press Enter/Space, **Then** `TaskDetailSlideOver` opens (keyboard reschedule path — C-1).
8. **Given** a focused milestone chip, **When** I press Enter/Space, **Then** `MilestoneDatePopover` opens to edit `due_date` (C-1/C-5).
9. **Given** any event, **Then** it cannot be resized (`eventDurationEditable={false}`).

### US-4 — Click/select to create — **P1**
**Independent test:** click an empty Month day → create slide-over prefilled (that day); slot-select in Week → prefilled with start time; on close focus returns.
1. **Given** Month view, **When** I click an empty day, **Then** `CalendarScreen` opens `CreateTaskSlideOver` with `workspaceId` + `initialDue` = that day (local **midnight** datetime — see §7; F-02).
2. **Given** Week/Day, **When** I drag-select a slot, **Then** the slide-over opens prefilled with the selected start datetime.
3. **Given** the slide-over open from the calendar, **When** I submit a valid task, **Then** `POST /tasks` runs and the event appears after the tasks query invalidates.
4. **Given** the slide-over open, **When** I cancel **or** submit, **Then** focus returns to the originating day cell (C-4).
5. **Given** a day cell, **When** I hover/focus it, **Then** a `cursor:pointer` + faint "+" affordance signals it is creatable (I-3).

### US-5 — Activate an event's detail — **P2**
1. **Given** a task chip, **When** I click/Enter, **Then** `TaskDetailSlideOver` opens; on close focus returns to the chip (C-4).
2. **Given** a milestone chip, **When** I click/Enter, **Then** `MilestoneDatePopover` opens to edit `due_date` (no silent no-op — C-5); on close focus returns.

### Edge Cases
- **Unparseable dates** (`due`/`at_ms`/`due_date`) → item silently excluded; grid still renders.
- **Date-only vs datetime parsing (TZ-safe, F-06):** a date-only `due`/`due_date` (`YYYY-MM-DD`) is parsed via **component construction** `new Date(y, m-1, d)` (local), **never** `new Date(str)` (which is UTC and shifts the day). A datetime `due` uses its **local** date for all-day placement. Drag-writes emit `YYYY-MM-DD` local. This round-trips with no off-by-one in any timezone.
- **Task with both `due` and `once`-trigger** → two chips (`:due` all-day + `:fire` timed), each independently draggable.
- **`once`-trigger across a DST boundary** → `at_ms` may be off by one hour (no `luxon`); documented v1 limitation (M-1).
- **Out-of-range timed events:** default time-grid window is 06:00–22:00 with `scrollTime` to first event; events outside the window are reachable by scrolling and always visible in Month/Agenda (F-15).
- **"+N more"** (many events/day) → FullCalendar popover, **themed dark** (`.fc-popover`) and Escape/focus-validated (I-7).
- **Concurrent edit** → optimistic drop then post-mutation refetch reconciles (last-write-wins; silent).

---

## 7. Behavioral Contract / Non-Behaviors / Integration Boundaries

### Behavioral Contract
- The Calendar **always renders a month grid**; while loading it shows a subtle affordance, not a replacement spinner; the empty hint shows only when `!isLoading && events.length === 0`.
- Each schedulable item maps to an event colored **and iconed** by the canonical map; `surface!=='user'` and unparseable dates are excluded.
- Reschedule (drag or keyboard) **optimistically updates and persists**; success → toast+undo; failure → revert+error toast.
- Click/select on empty date **opens the prefilled create slide-over**; activating a task opens its detail; activating a milestone opens its date popover.
- Focus moves into any opened dialog/popover and **returns to the triggering chip/cell** on close.

### Explicit Non-Behaviors
- **Must not** add/modify any wire/contract type (CalendarEvent is `not-wire-format`), backend endpoint, or event resize.
- **Must not** render `every`/`recurring` events in v1 (no reliable next-time).
- **Must not** write a **date-only `due` via the create slide-over** — that component emits a datetime; calendar-created tasks carry a **local-midnight datetime** `due` (accepted; read-mapping places it on the local date). Drag-writes, by contrast, emit a date-only `YYYY-MM-DD`. (Resolves the v1 contradiction — F-02.)
- **Must not** leave a milestone chip as a focusable no-op (C-5) — it opens the date popover.
- **Must not** convey status by **color alone** (C-2) — every chip carries an icon.
- **Must not** block grid render on either query failing — degrade and surface a **non-blocking toast** (tasks-fail → grid + "Couldn't load tasks"; milestones-fail → tasks shown + "Couldn't load milestones") (I-1/I-2).
- **Must not** add telemetry/logging (no-telemetry charter — F-12).

### Integration Boundaries
| System | In/Out | Contract | Failure | Dev |
|---|---|---|---|---|
| `GET /tasks?workspace_id` | out `Task[]` | existing `Task` (`surface: user\|heartbeat`) | grid renders + "Couldn't load tasks" toast | TanStack Query; mocked in unit |
| `GET /workspaces/{id}/milestones` | out `Milestone[]` | existing `Milestone` | tasks shown + "Couldn't load milestones" toast (I-2) | same |
| `PATCH /tasks/{id}` | in `TaskUpdateRequest{ due:'YYYY-MM-DD' \| trigger:TaskTrigger }` | existing | non-2xx → revert + error toast | E2E forces a single 500 via Playwright route interception (F-16) |
| `POST /tasks` | in `TaskCreateRequest` (via slide-over) | existing | slide-over handles validation | reuse component |
| `updateMilestone(wsId, id, {due_date:'YYYY-MM-DD'})` | in `MilestoneUpdateRequest` | existing (3-arg) | non-2xx → revert + error toast | mocked/E2E |

---

## 8. BDD Scenarios
> Every scenario has `Traces to:` (US.AS). Categories: Happy / Alternate / Error / Edge.

```gherkin
Feature: Workspace Calendar (FullCalendar)

  # US-1
  Scenario: Empty calendar still renders the month grid                 # Happy/Edge
    Given a workspace with no tasks and no milestones, fully loaded
    When I open the Calendar tab
    Then the current-month grid renders with weekday headers and day cells
    And a non-blocking "No scheduled items — click a day to add one" hint shows within the grid
    Traces to: US-1/AS-1

  Scenario: Loading does not flash the empty hint                       # Edge
    Given the tasks query has not yet resolved
    When the calendar mounts
    Then the grid renders with a subtle loading affordance
    And the empty hint is NOT shown until isLoading is false
    Traces to: US-1/AS-6

  Scenario: A due task appears as an all-day chip with icon            # Happy
    Given a task "Ship" due "2026-06-20", surface "user", status "next"
    When the calendar loads in Month view for June 2026
    Then an all-day chip "Ship" with a slate background, a Circle icon, and #0A0A0B text shows on 2026-06-20
    Traces to: US-1/AS-2, US-1/AS-5

  Scenario: Heartbeat-surface tasks are hidden                         # Edge
    Given a task with surface "heartbeat" and a due date
    Then no event for it is shown
    Traces to: US-1/AS-3

  Scenario: A milestone appears as a gold flag chip                    # Happy
    Given a milestone "Beta" with due_date "2026-06-25"
    Then a gold all-day chip "Beta" with a Flag icon shows on 2026-06-25
    Traces to: US-1/AS-4

  Scenario Outline: Status maps to canonical color AND icon           # Edge
    Given a task with status "<status>" and a due date
    Then its chip uses "<bg>" with the "<icon>" icon and #0A0A0B text
    Traces to: US-1/AS-5
    Examples:
      | status      | bg      | icon        |
      | done        | emerald | CheckCircle |
      | in_progress | blue    | CircleNotch |
      | blocked     | amber   | Prohibit    |
      | failed      | red     | XCircle     |
      | next        | slate   | Circle      |
      | inbox       | slate   | Circle      |
      | planning    | slate   | Circle      |

  # US-2
  Scenario: Switch Month to Week preserves the anchor date            # Happy
    Given Month view with the calendar's current date in June 2026
    When I select Week
    Then timeGridWeek renders the week containing calendarApi.getDate()
    Traces to: US-2/AS-1

  Scenario: Navigate prev/next/today                                  # Happy
    Given any view
    When I click next then today
    Then the period advances one unit then returns to the current period
    Traces to: US-2/AS-2

  Scenario: Agenda lists upcoming events with themed empty text       # Happy
    Given upcoming events exist
    When I select Agenda
    Then a listWeek agenda renders; when none, a themed "No events" message shows
    Traces to: US-2/AS-3

  Scenario: Phone defaults to Month with a two-row toolbar            # Alternate
    Given a 390px viewport
    Then the initial view is Month, all four views are available, and the toolbar is two rows
    Traces to: US-2/AS-4

  Scenario: Coarse-pointer targets are >=44px                         # Edge
    Given a coarse pointer
    Then toolbar controls and timeGrid event chips have >=44px hit targets
    Traces to: US-2/AS-5

  # US-3
  Scenario: Drag a due task writes a local-midnight datetime         # Happy
    Given a due task chip on 2026-06-20 and timezone America/Los_Angeles
    When I drag it to 2026-06-23
    Then updateTask is called with due = the local-midnight RFC3339 instant of 2026-06-23
    And the chip stays on 2026-06-23 after refetch (read maps the instant to the local date)
    Traces to: US-3/AS-1

  Scenario: Drag a once-trigger preserves sibling config keys         # Happy
    Given a once-trigger task at 2026-06-20T09:00 with trigger.config {at_ms, foo:"bar"}
    When I drag it to 14:00
    Then updateTask trigger = {type:"once", config:{at_ms:<14:00>, foo:"bar"}}
    Traces to: US-3/AS-2

  Scenario: Drag a milestone calls updateMilestone with 3 args        # Happy
    Given a milestone chip on 2026-06-25 in workspace ws-1
    When I drag it to 2026-06-28
    Then updateMilestone("ws-1", milestone.id, {due_date:"2026-06-28"}) is called
    Traces to: US-3/AS-3

  Scenario: Successful reschedule shows undo                          # Happy
    Given a due task chip
    When I drag it and the PATCH succeeds
    Then a role=status toast "Rescheduled to Jun 23" with a 5s Undo appears
    And clicking Undo restores the original date
    Traces to: US-3/AS-4

  Scenario: Failed reschedule reverts and alerts                      # Error
    Given a gateway that returns 500 on PATCH (route-intercepted)
    When I drag a due task
    Then the chip snaps back and a role=alert error toast shows
    Traces to: US-3/AS-5

  Scenario: Keyboard reschedule path for a task                       # Happy
    Given a focused task chip
    When I press Enter
    Then TaskDetailSlideOver opens with the task
    Traces to: US-3/AS-7

  Scenario: Keyboard/click reschedule path for a milestone           # Happy
    Given a focused milestone chip
    When I press Enter
    Then MilestoneDatePopover opens to edit due_date
    Traces to: US-3/AS-8, US-5/AS-2

  Scenario: Events cannot be resized                                  # Edge
    Given any event
    Then no resize handle is offered
    Traces to: US-3/AS-9

  # US-4
  Scenario: Click empty Month day opens prefilled create             # Happy
    Given Month view
    When I click the empty day cell 2026-06-22
    Then CreateTaskSlideOver opens with workspaceId and initialDue = 2026-06-22T00:00 (local)
    Traces to: US-4/AS-1

  Scenario: Slot-select in Week opens timed prefilled create         # Happy
    Given Week view
    When I drag-select 2026-06-22T10:00–11:00
    Then CreateTaskSlideOver opens prefilled with start 2026-06-22T10:00
    Traces to: US-4/AS-2

  Scenario: Submitting create adds the event                         # Happy
    Given the create slide-over opened from 2026-06-22
    When I submit a titled task
    Then POST /tasks runs and an event appears on 2026-06-22 after invalidation
    Traces to: US-4/AS-3

  Scenario: Closing create returns focus to the day cell             # Alternate
    Given the create slide-over opened from a day cell
    When I cancel
    Then no task is created and focus returns to that day cell
    Traces to: US-4/AS-4

  Scenario: Day cell shows a create affordance on hover/focus        # Edge
    Given Month view
    When I hover or focus an empty day cell
    Then the cursor is pointer and a faint "+" affordance appears
    Traces to: US-4/AS-5

  # Degradation
  Scenario: Milestones query failure degrades to tasks-only          # Error
    Given the milestones request returns 500
    When the calendar loads
    Then the grid renders with tasks and a "Couldn't load milestones" toast
    Traces to: US-1/AS-1 (Non-Behavior: degrade)

  # Mapping edges
  Scenario: Date-only due is parsed without a timezone shift         # Edge
    Given a task with due "2026-06-22" (date-only) and timezone America/Los_Angeles
    When events are mapped
    Then the chip is placed on 2026-06-22 (not 06-21)
    Traces to: US-1/AS-2

  Scenario: Task with both due and once-trigger yields two chips      # Edge
    Given a task with due 2026-06-20 and a once-trigger at 2026-06-21T09:00
    Then a ":due" all-day chip on 06-20 and a ":fire" timed chip on 06-21 both appear
    Traces to: US-1/AS-2

  Scenario: every/recurring tasks produce no events in v1            # Edge
    Given tasks with "every" and "recurring" triggers and no due
    Then no events are produced for them
    Traces to: US-1/AS-1 (Scope: deferred)

  Scenario: Unparseable dates are skipped without error             # Edge
    Given a task whose due is "not-a-date"
    Then no event is produced and the grid still renders
    Traces to: US-1/AS-1
```

---

## 9. TDD Plan

### Test levels (F-03): jsdom (`vite.config.ts` `css:false`) cannot compute FullCalendar layout/pointer geometry. So **grid rendering, event placement, real drag, dateClick coordinates, and focus traversal are E2E-only**. Unit/integration cover the **pure mapping** and **handler wiring** (FC mocked; call `eventDrop`/`dateClick`/`eventClick` handlers with synthetic `info`).

| # | Test | Level | Traces | Notes |
|---|---|---|---|---|
| 1 | `map: due task → all-day chip {bg,icon,text,editable}` | Unit | AS-2/5 | canonical map |
| 2 | `map: once-trigger → timed ":fire" chip` | Unit | US-3/AS-2 | |
| 3 | `map: every & recurring → no events` | Unit | Scope | F-10 |
| 4 | `map: surface "heartbeat" excluded` | Unit | US-1/AS-3 | F-01 |
| 5 | `map: milestone → gold Flag chip` | Unit | US-1/AS-4 | |
| 6 | `map: both due+once → two ids (:due,:fire)` | Unit | Edge | F-19 |
| 7 | `map: unparseable dates skipped` | Unit | Edge | |
| 8 | `map: date-only due parsed local (TZ=America/Los_Angeles, no shift)` | Unit | Edge | **F-06** |
| 9 | `map: status→{bg,icon} table (all 7 + milestone + fire)` | Unit | AS-5 | F-04/C-2/C-3 |
| 10 | `handler: eventDrop(due) → updateTask due "YYYY-MM-DD" local` | Unit (FC mocked) | US-3/AS-1 | F-06 |
| 11 | `handler: eventDrop(once) → trigger spread preserves siblings` | Unit | US-3/AS-2 | **F-05** |
| 12 | `handler: eventDrop(milestone) → updateMilestone(ws,id,{due_date})` | Unit | US-3/AS-3 | **F-09** |
| 13 | `handler: drop failure → revert() + error toast(role=alert)` | Integration | US-3/AS-5 | |
| 14 | `handler: drop success → success toast(role=status) + undo restores` | Integration | US-3/AS-4 | I-5/I-6 |
| 15 | `handler: dateClick → opens CreateTaskSlideOver w/ initialDue` | Integration | US-4/AS-1 | F-07 |
| 16 | `CreateTaskSlideOver: initialDue seeds form.due via open-effect; default unchanged` | Integration | US-4/AS-1 | F-07 regression |
| 17 | `handler: eventClick(task) → TaskDetailSlideOver` | Integration | US-3/AS-7,US-5/AS-1 | |
| 18 | `handler: eventClick(milestone) → MilestoneDatePopover` | Integration | US-3/AS-8,US-5/AS-2 | C-5 |
| 19 | `CalendarToolbar: view tab → changeView; prev/next/today → API` | Integration | US-2/AS-1,2 | |
| 20 | `CalendarScreen: tasks-loading → grid + affordance, no empty flash` | Integration | US-1/AS-6 | I-1 |
| 21 | `CalendarScreen: milestones 500 → grid+tasks + toast` | Integration | Degrade | I-2 |
| 22 | `e2e: grid renders (incl. empty), switch all views, real drag→persist, drop→revert(500 intercept), click-create, keyboard Enter reschedule, focus returns` | E2E (Playwright) | US-1..US-5 | F-03/F-16/C-1/C-4 |

### Datasets
**DS-1 — mapping** (rows include TZ + every/recurring + heartbeat):
| Row | Input | Expected | Traces |
|---|---|---|---|
| 1 | task{due:"2026-06-20",surface:"user",status:"next"} | slate chip, Circle, editable | AS-2/5 |
| 2 | task{trigger:once@at_ms,surface:"user"} | timed ":fire" chip, Clock | US-3/AS-2 |
| 3 | task{trigger:every}/task{trigger:recurring} | 0 events | Scope |
| 4 | task{surface:"heartbeat",due:set} | 0 events | AS-3 |
| 5 | task{due:"not-a-date"} | 0 events | Edge |
| 6 | task{due:set,trigger:once} | 2 chips (:due,:fire) | Edge |
| 7 | milestone{due_date:"2026-06-25"} | gold Flag chip | AS-4 |
| 8 | milestone{due_date:null} | 0 events | Edge |
| 9 | task{due:"2026-06-22"} under TZ=America/Los_Angeles | placed 06-22 (not 06-21) | **F-06** |
| 10 | task per status done/in_progress/blocked/failed/inbox/next/planning | correct {bg,icon} | AS-5 |

**DS-2 — reschedule** (route-intercept for the 500):
| Row | Drag | API | Expected | Traces |
|---|---|---|---|---|
| 1 | due → +3d | 200 | updateTask due "YYYY-MM-DD"; success toast+undo | US-3/AS-1,4 |
| 2 | once → +5h | 200 | trigger spread {type,config{at_ms,...siblings}} | US-3/AS-2 |
| 3 | milestone → +3d | 200 | updateMilestone(ws,id,{due_date}) | US-3/AS-3 |
| 4 | due → +1d | 500 (intercept) | revert + error toast | US-3/AS-5 |
| 5 | undo within 5s | inverse 200 | original date restored | US-3/AS-4 |

### Regression
- `CreateTaskSlideOver` gains optional `initialDue` + an open-effect → existing `CreateTaskSlideOver.test.tsx`/`WorkspaceTasksTab` must pass unchanged (test #16 guards the new path; defaults preserve old behavior).
- Old list-style `CalendarScreen` has **no tests** → "No calendar-test regression — new capability." Route seam (named-export adapter) covered by #22.

---

## 10. Functional Requirements
- **FR-001 (MUST):** Render a month grid on load regardless of event count; show a loading affordance while either query is loading; show the empty hint only when `!isLoading && events.length === 0`.
- **FR-002 (MUST):** Map each `surface==='user'` task with a `due` to an all-day event, and each `once`-trigger to a timed `:fire` event. **Do not** render `every`/`recurring`.
- **FR-003 (MUST):** Exclude `surface!=='user'` (e.g. `heartbeat`) and unparseable-date items without throwing.
- **FR-004 (MUST):** Map each milestone with a `due_date` to a gold all-day `Flag` event encoding `milestone:{id}`.
- **FR-005 (MUST):** Color **and** icon every chip per the §6 canonical map; chip text `#0A0A0B`. (WCAG 1.4.1, 1.4.3.)
- **FR-006 (MUST):** Offer Month/Week/Day/Agenda via a custom toolbar driving the FC API; the week starts Monday (`firstDay=1`); view switches keep `calendarApi.getDate()` (F-11).
- **FR-007 (MUST):** On phone-width: default Month, all views available, two-row toolbar; ≥44px targets for toolbar controls **and** time-grid chips (`min-height:44px`).
- **FR-008 (MUST):** Drag/keyboard reschedule MUST: task due → `updateTask{due: <dropped day's local-midnight RFC3339 instant, e.g. start.toISOString()>}` (`TaskUpdateRequest.due` is `format: date-time` — a date-only string is rejected 400); once-trigger → `updateTask{trigger: {...task.trigger, config:{...config, at_ms}}}`; milestone → `updateMilestone(workspaceId, id, {due_date:'YYYY-MM-DD'})` (`Milestone.due_date` is a plain ISO date string).
- **FR-009 (MUST):** Provide a keyboard/single-pointer reschedule path: Enter/Space on a task chip → `TaskDetailSlideOver`; on a milestone chip → `MilestoneDatePopover` (WCAG 2.5.7, 2.1.1).
- **FR-010 (MUST):** On reschedule success → success toast (`role="status"`) with a 5-second Undo that restores the prior date; on failure → `info.revert()` + error toast (`role="alert"`). Feedback uses the optimistic move + `aria-live` toast (WCAG 4.1.3); the near-instant optimistic-cache patch makes a separate per-chip spinner unnecessary.
- **FR-011 (MUST):** Recurring/every absent; **no event resize** (`eventDurationEditable={false}`).
- **FR-012 (MUST):** Clicking/selecting an empty date opens `CreateTaskSlideOver` (owned by `CalendarScreen`) with `workspaceId` + `initialDue` (local-midnight in Month, slot time in Week/Day), seeded via `useEffect([open, initialDue])`; day cells show a hover/focus create affordance.
- **FR-013 (MUST):** On open of any slide-over/popover from a calendar gesture, move focus into it; on close, restore focus to the triggering chip/cell (WCAG 2.4.3).
- **FR-014 (MUST NOT):** Add/change any contract/wire type, backend endpoint, telemetry, or event resize.
- **FR-015 (MUST):** Date parsing is TZ-safe: date-only strings via `new Date(y,m-1,d)` (local), datetime via local date. Task-due drag-writes emit a **local-midnight RFC3339 datetime** (contract requires date-time); milestone drag-writes emit `YYYY-MM-DD`. Both round-trip via the local-date read with no off-by-one in any timezone.
- **FR-016 (MUST):** Degrade on query failure: render the grid; tasks-fail → "Couldn't load tasks" toast; milestones-fail → tasks shown + "Couldn't load milestones" toast.
- **FR-017 (SHOULD):** Theme FC via `--fc-*` (incl. `--fc-now-indicator-color:var(--color-accent)`, `.fc-popover` dark) and honor `prefers-reduced-motion`; keep the route code-split.

## 11. Success Criteria
- **SC-001:** Empty workspace → grid with 28–31 day cells (current month) + 7 weekday headers.
- **SC-002:** A `user` task with a due in view → exactly one chip on the correct local day (no TZ shift in `TZ=America/Los_Angeles`); `heartbeat` → zero.
- **SC-003:** All four views reachable; switch renders within 300 ms (no reload).
- **SC-004:** Successful PATCH → chip persists on the new day ≥99% and shows undo; a route-intercepted 500 → chip reverts and an `role="alert"` toast appears 100% of the time.
- **SC-005:** Clicking an empty day opens the create slide-over with that date prefilled (field value asserted); focus returns to the cell on close.
- **SC-006a:** Each chip exposes a non-color status cue (a Phosphor icon) — WCAG 1.4.1.
- **SC-006b:** Chip text `#0A0A0B` clears **≥7:1 (WCAG AAA)** on every chip background (measured floor: `failed` red at 7.15:1) — WCAG 1.4.3.
- **SC-006c:** axe serious/critical violations = 0 on the calendar route.
- **SC-007:** The `@fullcalendar/*` libraries appear **only in the calendar-route chunk** (verified in the build chunk graph), not the main entry (F-20).
- **SC-008:** `make verify-contracts` → no diff; `tsc -b` + `vitest` + `golangci-lint` + `go test` pass.

## 12. Traceability Matrix
| FR | US | BDD | Tests |
|---|---|---|---|
| FR-001 | US-1 | Empty grid; Loading no-flash | #20, #22 |
| FR-002 | US-1 | Due chip; once :fire; every/recurring none | #1,#2,#3,#6 |
| FR-003 | US-1 | Heartbeat hidden; unparseable skipped | #4,#7 |
| FR-004 | US-1 | Milestone flag | #5 |
| FR-005 | US-1 | Status→color+icon outline | #9, DS-1/10 |
| FR-006 | US-2 | Switch anchor; navigate; agenda | #19,#22 |
| FR-007 | US-2 | Phone two-row; 44px chips | #22 |
| FR-008 | US-3 | Drag due/once/milestone | #10,#11,#12, DS-2/1-3 |
| FR-009 | US-3/US-5 | Keyboard task/milestone reschedule | #17,#18,#22 |
| FR-010 | US-3 | Undo/success; revert/error | #13,#14, DS-2/4-5 |
| FR-011 | US-3 | every/recurring none; no resize | #3,#22 |
| FR-012 | US-4 | Click/slot create; affordance | #15,#16,#22 |
| FR-013 | US-4/US-5 | Focus returns on close | #22 |
| FR-014 | — (constraint) | — | #22 + verify-contracts |
| FR-015 | US-1/US-3 | Date-only TZ-safe parse | #8,#10 |
| FR-016 | US-1 | Milestones-500 degrade | #21 |
| FR-017 | US-1/US-2 | (theming/motion — SC-006a-c/007) | build/axe |

> Every BDD scenario traces to a US (`Traces to:` lines) and ≥1 FR; every FR appears here.

---

## 13. Ambiguity Self-Audit (v2)
| # | Item | Resolution |
|---|---|---|
| A1 | Mobile views | All four, default Month, two-row toolbar (operator). |
| A2 | Create UI | Reuse `CreateTaskSlideOver`, controlled by `CalendarScreen`, seeded via open-effect (F-07). |
| A3 | Task activation | `TaskDetailSlideOver` (also the keyboard reschedule path). |
| A4 | Milestone activation | `MilestoneDatePopover` (NOT a no-op — C-5). |
| A5 | `every`/`recurring` | **Deferred** from v1 (F-10). |
| A6 | All-day `due` shape | Create → local-midnight datetime (component limit); drag → date-only `YYYY-MM-DD`; read → local date. No off-by-one (F-02/F-06). |
| A7 | Time window | 06:00–22:00 + `scrollTime`; out-of-range surfaced in Month/Agenda (F-15). |
| A8 | `next` status color | **Muted/slate** (matches code), overriding v1 (F-04). |
| A9 | FC version | **6.1.21**; v7 deferred (needs temporal-polyfill; `next`-tag only) (F-13). |
| A10 | E2E fault injection | Playwright route interception of one PATCH→500 (not gateway kill) (F-16). |

---

## 14. Holdout Evaluation Scenarios (post-implementation; NOT in traceability)
- **H1:** Real workspace → Calendar shows the current month with your real tasks/milestones on the right days.
- **H2:** Drag a real due task to next week; refresh → still next week; Tasks tab due date matches; a success toast offered Undo.
- **H3:** Click a future empty day → create "Eval task" → appears that day and in Board/List; focus returned to the cell.
- **H4:** Use only the keyboard: Tab to a task chip, Enter → detail opens; Tab to a milestone, Enter → date popover.
- **H5 (error):** With dev-tools throttling/route-fail one PATCH → the dragged event snaps back with an alert toast; no silent loss.
- **H6 (edge):** Color-blind simulation → you can still tell done from failed (icons differ).
- **H7 (edge):** iPhone-size → Month default, two-row toolbar, tappable chips; switch to Agenda works.

## 15. Assumptions & Future Work
- **Assumes** `trigger.config.at_ms` is epoch-ms; `due`/`due_date` are ISO strings (date or datetime).
- **Future:** server `next_fire_at` → render `every`/`recurring` (still non-draggable); milestone detail panel; event resize if a duration model appears; revisit FullCalendar v7 when it hits `latest`.
- **Bundle:** 6 `@fullcalendar/*` packages add weight; mitigated by the lazy calendar-route chunk (SC-007).
