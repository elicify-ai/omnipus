# Calendar (UI)

Recurring and scheduled work lives exclusively in the workspace Calendar tab.
The screen shell `CalendarScreen.tsx` lives in `src/components/screens/` (a
leftover dump — new calendar code belongs here, not there); this folder holds
the views and editors (`FullCalendarView`, `CalendarToolbar`,
`CalendarEventSlideOver`, `RecurrenceEditor`).

## Design system

Read `.claude/skills/omnipus-design-system/SKILL.md` before adding or changing any
control, color, spacing, or type value here — it states the CI-enforced rules and cites
the script or test for each.

## No raw cron — anywhere in the UI

- Product-wide ban: no cron entry or display in any UI. Recurrence is edited
  only through `RecurrenceEditor.tsx` (RRULE-based); `CalendarEventSlideOver`'s
  own header states the contract: "No cron string is ever rendered anywhere in
  this UI". Cron survives under the hood only (engine, API, heartbeats).
- Spec: `docs/internal/specs/calendar-recurrence-redesign-spec.md`.
- Legacy tasks still carry `cron_expr`/`every_ms` internally (see
  `CalendarEventSlideOver`'s trigger classification) — the ban is on rendering
  and authoring cron strings, not on the legacy fields existing.

## Tests

CI group `components-misc` (pattern includes `src/components/calendar/`).
Local: `npx vitest run src/components/calendar/`. A test file matching no group
pattern runs in NO CI job while CI stays green;
`scripts/check-vitest-coverage.mjs` is the tripwire.
