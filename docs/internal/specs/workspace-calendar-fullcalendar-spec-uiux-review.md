# UI/UX Design Review — Workspace Calendar (FullCalendar v6 Spec)

> Produced by the `elicify-UI-UX-Design` REVIEW methodology against
> `workspace-calendar-fullcalendar-spec.md`. Threshold-anchored; every finding cites a number or named law.
> **Verdict: FAIL** (5 Critical · 9 Important · 5 Minor). Companion to the adversarial `…-spec-review.md` (grill-spec).

Surface type: data-dense calendar / scheduling surface + slide-over forms. Domains run: Cognitive load, Visual hierarchy, Accessibility (full), Happy-path friction, Color/contrast, States (loading/empty/error), Motion, Touch targets.

---

## CRITICAL FINDINGS

### C-1 — Drag-and-drop has no keyboard / single-pointer alternative (WCAG 2.5.7 AA, 2.1.1 A)
WCAG 2.5.7 (Dragging Movements, AA, new in 2.2) requires every drag to have a single-pointer alternative; 2.1.1 requires keyboard operability. The spec defines reschedule entirely via `eventDrop` (FR-008/US-3). No keyboard path, "Move event" menu, or click-based date picker is specified. Milestones are worst: US-5/AS-2 makes click a no-op, so milestones have *zero* non-pointer reschedule path.
**Fix:** Name an explicit keyboard/single-pointer reschedule path. Task chips: Enter opens `TaskDetailSlideOver` (edit date there). Milestones: Enter opens an inline date-picker popover (or defer milestone drag to v1.1). Write into FR-008 + new FR + BDD scenarios.

### C-2 — Status color encoding has no non-color cue (WCAG 1.4.1 A)
US-1/AS-5 conveys status by **hue only** (gold/emerald/blue/amber/red/muted). ~8% of males (red-green CVD) cannot distinguish emerald (done) from red (failed). Level A, legally required.
**Fix:** Add a non-color cue per status in the `eventContent` renderer — a Phosphor icon prefix (CheckCircle=done, XCircle=failed, Clock=in_progress, Lock=blocked, Flag=milestone) is cheapest and on-brand. Add an `icon`/`aria-label` column to the §8 BDD table.

### C-3 — Event chip text color unspecified; white/silver text fails WCAG 1.4.3 at every status color
White (or Liquid Silver #E2E8F0) text on the bright chips: Gold 2.10:1, Emerald 1.92:1, Blue 2.54:1, Amber 1.67:1, Red 2.77:1 — all fail 4.5:1 (and 3:1 large). Chip-vs-page non-text contrast (1.4.11) is fine (7.15–10.29:1); the problem is **text on chip**.
**Fix:** Chip text MUST be near-black #0A0A0B (Deep Space Black). Black on chips passes: Gold 9.99:1, Emerald 10.92:1, Blue 8.26:1, Amber 12.58:1, Red 7.59:1. Name the exact muted-chip hex too.

### C-4 — Focus management for slide-overs opened from calendar gestures unspecified (WCAG 2.4.3 A)
`dateClick`/`eventClick` open slide-overs, but the spec doesn't require focus to move into the dialog on open and restore to the triggering chip/cell on close. The trigger is a FullCalendar-managed DOM node, so restoration needs the calendar to hold a ref to the triggering element.
**Fix:** Add acceptance criteria to US-4/US-5: focus moves to first interactive field on open; returns to the day cell/event chip on close. Add an integration test.

### C-5 — Milestone eventClick is a keyboard-operable dead end (WCAG 2.1.1 A)
FullCalendar chips are focusable by default. US-5/AS-2's silent no-op means a SR user tabs to a milestone, presses Enter, hears nothing. Combined with C-1, a milestone is pointer-drag-only.
**Fix:** Either make milestone chips non-interactive (unfocusable, no handler, `pointer:default`) if detail is deferred, *and* carry `aria-label="[Name] milestone — [date] — edit coming soon"`; or add a minimal date-edit popover on click/Enter.

---

## IMPORTANT FINDINGS

### I-1 — No loading state to distinguish "loading" from "genuinely empty" (Nielsen response limits)
The grid always renders, but during the tasks/milestones `useQuery` the empty-state hint can flash before events appear (CLS-equivalent on slow APIs).
**Fix:** Require a skeleton/spinner while `isLoading`; only show the empty hint when `!isLoading && events.length === 0`. Make it a required behavior, not implementer discretion.

### I-2 — Milestone-query failure degrades silently; "non-blocking error" is unspecified
§7 says surface a non-blocking error but gives no FR/BDD/text.
**Fix:** Add a BDD scenario (milestones 500 → tasks render + toast "Could not load milestones") and specify the toast text in the integration-boundaries table.

### I-3 — Click-to-create discoverability gap; empty hint is "subtle" (Recognition over Recall)
The only cue for click-to-create (FR-011) is a "subtle" text hint. Day cells have no hover/focus affordance signaling tappability.
**Fix:** Require a day-cell hover/focus affordance — pointer cursor + a faint "+" in the corner via `dayCellContent`/`dayCellDidMount`. Keep the hint as a secondary cue.

### I-4 — Week/Day time-grid on phone: friction unquantified (Fitts, WCAG 2.5.8)
At 390px, `timeGridWeek` renders 7 columns ~55px wide; event chips can be ~24px tall — fails WCAG 2.5.8 AA (24×24) as interactive targets. FullCalendar compresses columns rather than scrolling by default.
**Fix:** Either hide Week/Day under 640px (Agenda as the non-Month alt — Hick's Law) or spec horizontal scroll with a fixed time column and `min-height:44px` on `.fc-timegrid-event`. Make it an FR, not implicit.

### I-5 — No undo / confirmation for accidental drags (Regret Test)
`info.revert()` covers API failure, not accidental success. A one-cell mis-drag on touch silently rewrites the due date. A fully-informed user would regret it.
**Fix:** Add a 5-second "Undo" toast (`role="status"`) after a successful reschedule, or explicitly document accidental-drag as an accepted limitation. (Avoid a per-drag confirm dialog — it kills the direct-manipulation feel.)

### I-6 — Success feedback after reschedule is absent
Only failure is acknowledged (FR-009). Optimistic move shows "success" before the server confirms; a late 500 after 30s jumps the event back jarringly.
**Fix:** Brief success toast ("Rescheduled to [date]") on `eventChange` completion + a "saving" indicator on the chip during the in-flight window. Add to the US-3 happy path.

### I-7 — "+N more" overflow popover theming + focus not verified
FullCalendar's `dayMaxEvents` popover defaults to a near-white background — breaks dark-first; it has historically had focus-trap/Escape issues.
**Fix:** `fullcalendar-theme.css` must override `.fc-popover` bg/border/text; add an acceptance criterion + note that popover focus/Escape must be validated.

### I-8 — Toast not specified as an `aria-live` region (WCAG 4.1.3 AA)
"Error toast is shown" doesn't guarantee `role="alert"`/`aria-live`. SR users may never hear it.
**Fix:** Require error toast `role="alert"`, success toast `role="status"`. Reference `src/store/ui.ts` if it already does this; otherwise it's a new requirement on the toast system.

### I-9 — Toolbar density on mobile violates Hick's Law
8 interactive elements (prev/next/today + 4 view tabs + New Task) in one row at 390px ≈ 48px each, no hierarchy.
**Fix:** Responsive toolbar — two rows on mobile (nav+date / views+New Task) or icon-only tabs. Tie to FR-007.

---

## MINOR FINDINGS
- **M-1** DST: dragging a `once`-trigger across a DST boundary can be off-by-one-hour (no `luxon`). Acceptable v1 limitation — document explicitly.
- **M-2** `nowIndicator` defaults to red; set `--fc-now-indicator-color: var(--color-accent)` (Forge Gold).
- **M-3** Agenda `listWeek` empty state uses FullCalendar's default text — theme via `noEventsContent`/`listEmptyText`.
- **M-4** `firstDay={1}` (Monday) is in the reference but not codified in an FR — make explicit (else defaults to Sunday).
- **M-5** No `prefers-reduced-motion` handling for drag transitions — add `@media (prefers-reduced-motion: reduce){ .fc-event{ transition:none } }`.

---

## VERDICT: FAIL
Five criticals create WCAG violations as written: C-1 (2.5.7/2.1.1), C-2 (1.4.1), C-3 (1.4.3), C-4 (2.4.3), C-5 (2.1.1). The spec is otherwise well-structured (traceability, BDD, TDD, optimistic/revert are solid); the criticals are specific and fixable without architectural change.

### Top 3 Fixes by Impact
1. **Chip = near-black text + per-status Phosphor icon** (resolves C-2 + C-3): add `icon` + `textColor:'#0A0A0B'` to `CalendarEvent`; render `<Icon/> {title}` in `eventContent`. ~2h.
2. **Define a keyboard/single-pointer reschedule path** (resolves C-1 + C-5): Enter on a task chip → `TaskDetailSlideOver`; Enter on a milestone → inline date-picker popover. Add to FR-008 + BDD. Milestone popover ~1d.
3. **Focus management on slide-over open/close** (resolves C-4): focus in on open, restore to triggering chip/cell on close; add integration test. ~2–4h.
