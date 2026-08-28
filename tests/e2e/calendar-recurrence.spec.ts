/**
 * calendar-recurrence.spec.ts — E2E happy-path sweep for the Calendar
 * Recurrence Redesign.
 *
 * Spec: docs/internal/specs/calendar-recurrence-redesign-spec.md (Rev 6)
 * TDD plan Test 24: `e2e: calendar-recurrence.spec.ts` — "embedded-SPA
 * happy-path sweep", tracing:
 *   - "Operator creates a biweekly task…" (User Story 1, spec line 392)
 *   - "Weekly task renders…" (User Story 2, spec line 504)
 *   - "Agent filter…" (User Story 4, spec line 644)
 *   - "Recurring tasks absent from Board" (User Story 3, spec line 615) —
 *     scenario BROADENED per operator ruling 2026-08-07 ("recurring and ALL
 *     scheduled tasks are calendar-only", implemented in commit 364d00b2):
 *     `once`/`every`/`recurring` triggers are ALL Board/List-excluded now,
 *     not just `every`/`recurring`. See the (d) test below and
 *     src/components/workspaces/boardListExclusion.test.ts for the unit-level
 *     boundary this E2E scenario exercises end-to-end.
 *
 * LLM-independent: no agent turns, no model calls required — every
 * assertion is deterministic UI/REST behaviour (matches the calendar.spec.ts
 * convention of preferring REST-seeded fixtures + direct UI interaction over
 * agent execution wherever the BDD scenario doesn't require an agent turn).
 *
 * Convention mirrors tests/e2e/calendar.spec.ts (read in full before writing
 * this file):
 *   - Uses `test` from './fixtures/console-errors' (console-error guard fixture)
 *   - Uses pre-authenticated global storageState (playwright.config.ts)
 *   - HashRouter: navigate via `page.goto('/#/workspaces/{id}/calendar')` /
 *     `.../board`
 *   - A single throwaway workspace is created via REST in `beforeAll` and
 *     deleted in `afterAll`; each test creates/deletes its OWN task(s) via
 *     try/finally so the shared workspace stays clean between tests
 *     (workers:1/fullyParallel:false in playwright.config.ts guarantees
 *     these tests never interleave).
 *
 * Team note: a fresh workspace created via `POST /api/v1/workspaces` with no
 * explicit `core_team` now seeds ONLY Ava (`newWorkspaceSetupTeam`,
 * pkg/gateway/rest_workspace_delegation.go) — too narrow for the agent-filter
 * scenario, which needs two distinct agents. This file passes an explicit
 * `core_team: ['mia', 'jim']` on creation (handleWorkspaceCreate honours a
 * caller-supplied core_team verbatim), so both agents are assignable and
 * both appear in the calendar toolbar's Agent filter dropdown.
 */

import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { newAdminApiContext } from './fixtures/admin-api';

// REST setup/teardown authenticates with the SHARED admin session
// (fixtures/admin-api.ts). Do NOT add a POST /api/v1/auth/login here to get a
// bearer token: login re-mints the single-slot session_token_hash and silently
// invalidates the storageState cookie for every spec that runs later.
// scripts/check-e2e-login-crosstalk.sh enforces this.

// ── Workspace lifecycle helpers ────────────────────────────────────────────────

/** Local IANA zone — used both as the `tz` for REST-seeded rrule tasks and to
 *  reason about placement; the browser context has no explicit `timezoneId`
 *  override in playwright.config.ts, so it shares the OS zone with this
 *  Node process (same container in CI). */
const LOCAL_TZ = Intl.DateTimeFormat().resolvedOptions().timeZone;

interface ApiTaskTrigger {
  type: string;
  config: Record<string, unknown>;
}

interface ApiTask {
  id: string;
  title: string;
  agent_id?: string;
  trigger?: ApiTaskTrigger;
}

/**
 * Create a throwaway workspace and return its ID. Passing `coreTeam`
 * explicitly bypasses the Ava-only default seed (see file header note) so
 * the agent-filter scenario has two assignable agents.
 */
async function createTestWorkspace(coreTeam?: string[]): Promise<string> {
  const ctx = await newAdminApiContext();
  try {
    const name = `E2E Calendar Recurrence Workspace ${Date.now()}`;
    const data: Record<string, unknown> = { name };
    if (coreTeam && coreTeam.length > 0) data.core_team = coreTeam;
    const res = await ctx.post('/api/v1/workspaces', { data });
    if (!res.ok()) {
      const body = await res.text();
      throw new Error(`calendar-recurrence.spec.ts: createWorkspace failed ${res.status()}: ${body}`);
    }
    const ws = (await res.json()) as { id: string };
    return ws.id;
  } finally {
    await ctx.dispose();
  }
}

async function deleteTestWorkspace(workspaceId: string): Promise<void> {
  const ctx = await newAdminApiContext();
  try {
    await ctx.delete(`/api/v1/workspaces/${encodeURIComponent(workspaceId)}`);
  } catch {
    // Best-effort cleanup; do not fail the test.
  } finally {
    await ctx.dispose();
  }
}

// ── Task REST helpers (LLM-independent seeding + persistence checks) ──────────

async function createTaskApi(body: Record<string, unknown>): Promise<ApiTask> {
  const ctx = await newAdminApiContext();
  try {
    const res = await ctx.post('/api/v1/tasks', { data: body });
    if (!res.ok()) {
      const respBody = await res.text();
      throw new Error(`createTaskApi failed ${res.status()}: ${respBody}`);
    }
    return (await res.json()) as ApiTask;
  } finally {
    await ctx.dispose();
  }
}

async function fetchTasksApi(wsId: string): Promise<ApiTask[]> {
  const ctx = await newAdminApiContext();
  try {
    const res = await ctx.get(`/api/v1/tasks?workspace_id=${encodeURIComponent(wsId)}`);
    if (!res.ok()) {
      const body = await res.text();
      throw new Error(`fetchTasksApi failed ${res.status()}: ${body}`);
    }
    return (await res.json()) as ApiTask[];
  } finally {
    await ctx.dispose();
  }
}

async function deleteTaskApi(taskId: string): Promise<void> {
  const ctx = await newAdminApiContext();
  try {
    await ctx.delete(`/api/v1/tasks/${encodeURIComponent(taskId)}`);
  } catch {
    // best-effort
  } finally {
    await ctx.dispose();
  }
}

// ── Date helpers (deterministic regardless of the day the suite runs) ─────────

/** The Monday on/after `base` (today if `base` is already a Monday), at local midnight. */
function nextOrTodayMonday(base: Date): Date {
  const d = new Date(base.getTime());
  const dow = d.getDay(); // 0=Sun..6=Sat
  const add = (8 - dow) % 7; // 0 when `base` is already Monday
  d.setDate(d.getDate() + add);
  d.setHours(0, 0, 0, 0);
  return d;
}

/** A Monday at least `minDaysBack` days before `base`, at 09:00 local — used as
 *  an rrule `dtstart_ms` far enough in the past that every Monday visible in
 *  the CURRENT month/week grid is a real occurrence, regardless of what day
 *  the suite happens to run on. */
function priorMonday(base: Date, minDaysBack: number): Date {
  const d = new Date(base.getTime());
  d.setDate(d.getDate() - minDaysBack);
  const dow = d.getDay();
  const diff = dow === 0 ? 6 : dow - 1; // days since the most recent Monday
  d.setDate(d.getDate() - diff);
  d.setHours(9, 0, 0, 0);
  return d;
}

/** Today's date at a fixed local hour/minute — always within the calendar's
 *  default (today's month) view, so there is no month-boundary edge case. */
function todayAt(hour: number, minute = 0): Date {
  const d = new Date();
  d.setHours(hour, minute, 0, 0);
  return d;
}

function formatYMD(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const dd = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${dd}`;
}

// ── Test state ────────────────────────────────────────────────────────────────

let workspaceId: string;

test.beforeAll(async () => {
  workspaceId = await createTestWorkspace(['mia', 'jim']);
});

test.afterAll(async () => {
  if (workspaceId) {
    await deleteTestWorkspace(workspaceId);
  }
});

// ── Navigation helpers ──────────────────────────────────────────────────────

/** Navigate to the workspace Calendar tab and wait for it to mount (mirrors
 *  calendar.spec.ts's navigateToCalendar, including its retry-on-transient-
 *  load-error resilience). */
async function navigateToCalendar(page: import('@playwright/test').Page): Promise<void> {
  const fc = page.locator('.fc');
  const loadError = page.getByText('Failed to load workspace');
  for (let attempt = 0; attempt < 3; attempt++) {
    await page.goto(`/#/workspaces/${workspaceId}/calendar`);
    await Promise.race([
      expect(fc).toBeVisible({ timeout: 15_000 }).catch(() => {}),
      expect(loadError).toBeVisible({ timeout: 15_000 }).catch(() => {}),
    ]);
    if (await fc.isVisible()) return;
    await page.waitForTimeout(1_500);
  }
  await expect(fc).toBeVisible({ timeout: 30_000 });
}

/** Navigate to the workspace Board tab and wait for the Inbox column to mount
 *  (every newly-created task lands in `inbox` per TaskCreateRequest's landing
 *  rule, so this is always the right column to wait on). */
async function navigateToBoard(page: import('@playwright/test').Page): Promise<void> {
  const inboxColumn = page.locator('[aria-label="Inbox column"]');
  const loadError = page.getByText('Failed to load workspace');
  for (let attempt = 0; attempt < 3; attempt++) {
    await page.goto(`/#/workspaces/${workspaceId}/board`);
    await Promise.race([
      expect(inboxColumn).toBeVisible({ timeout: 15_000 }).catch(() => {}),
      expect(loadError).toBeVisible({ timeout: 15_000 }).catch(() => {}),
    ]);
    if (await inboxColumn.isVisible()) return;
    await page.waitForTimeout(1_500);
  }
  await expect(inboxColumn).toBeVisible({ timeout: 30_000 });
}

// ── (a) US-1: create a biweekly recurring task via the calendar UI ────────────

test(
  'Operator creates a biweekly task from a preset-plus-custom flow (US-1)',
  async ({ page }) => {
    // Traces to: calendar-recurrence-redesign-spec.md line 392 (Scenario:
    // Operator creates a biweekly task from a preset-plus-custom flow) /
    // User Story 1, Acceptance Scenarios 1–3 / FR-001/002/003/004/005/006a /
    // TDD plan Test 24.
    // BDD: Given the operator clicked Monday <date> on the Month grid, and
    // the event slide-over opened pre-filled with "Does not repeat";
    // When the operator selects Custom, sets interval 2 weeks, keeps Monday,
    // chooses "After 10 occurrences", and saves;
    // Then the live summary reads "Repeats every 2 weeks on Monday, 10 times"
    // before saving, and GET /api/v1/tasks/{id} returns a `recurring` trigger
    // with an RRULE equivalent to FREQ=WEEKLY;INTERVAL=2;BYDAY=MO;COUNT=10,
    // dtstart_ms matching the clicked day at 09:00, and no cron_expr.
    test.setTimeout(90_000);

    const mondayDate = nextOrTodayMonday(new Date());
    const mondayDataDate = formatYMD(mondayDate);
    const title = `E2E Recurrence Biweekly ${Date.now()}`;

    let taskId: string | undefined;
    try {
      await navigateToCalendar(page);

      // Click the Monday day cell — CalendarScreen.handleDateClick opens the
      // calendar-specific event slide-over (not the generic CreateTaskSlideOver).
      const dayCell = page.locator(`.fc-daygrid-day[data-date="${mondayDataDate}"]`);
      await expect(dayCell).toBeVisible({ timeout: 15_000 });
      await dayCell.click();

      const slideOver = page.locator('[role="dialog"][data-state="open"]');
      await expect(slideOver).toBeVisible({ timeout: 10_000 });
      // Confirms the CALENDAR event slide-over opened (not the generic "New task"
      // panel) — the routing behaviour US-1/AS-1 depends on.
      await expect(page.getByText('New event', { exact: true })).toBeVisible();

      // Default state assertion (US-1/AS-1): "Does not repeat" before any edit.
      await expect(page.locator('#recurrence-preset')).toHaveText('Does not repeat');

      await page.locator('#ces-title').fill(title);

      // A scheduled agent task requires an assigned agent AND an instruction
      // (an unassigned or instruction-less scheduled task has no-one / nothing
      // to run — enforced client-side by a disabled Save and server-side by a
      // 400). Fill both here, BEFORE the recurrence rejection sub-check below,
      // so that sub-check isolates recurrence validity (not agent/prompt).
      // The Agent field is a SmartSelect (src/components/ui/smart-select.tsx)
      // whose trigger is always a Radix Select with an explicit
      // role="combobox" (see CalendarEventSlideOver.test.tsx's own
      // `selectAgent()` helper, which uses the same locator) — NOT a bare
      // `role="button"`. Do not "fix" this back to `role: 'button'`: before
      // the workspace-team scoping fix (useWorkspaceTeamIds / Fix B), the
      // picker's loading state fed the full UNSCOPED global agent roster
      // (commonly >5) into SmartSelect, which flips to its search-popover
      // variant — a bare `<button aria-haspopup="dialog">` with no explicit
      // role — whenever item count exceeds smart-select.tsx's
      // SEARCHABLE_THRESHOLD (5). That transient, count-driven identity swap
      // is what made a `role: 'button'` locator time out here: it matched
      // the disabled loading-state button, but the resolved (team-scoped,
      // <=5-item) state re-renders as role="combobox", so the click wait
      // never found an enabled match. CreateTaskSlideOver.tsx /
      // CalendarEventSlideOver.tsx now pin the picker to an empty item set
      // while `teamLoading` is true, keeping it on the combobox branch for
      // the whole loading→resolved transition.
      await page.getByRole('combobox', { name: 'Agent' }).click();
      await page.getByRole('option', { name: /Mia/ }).first().click();
      await page.locator('#ces-prompt').fill('Post the sprint status summary to the team channel.');

      // Open the Repeat dropdown and select "Custom…".
      await page.locator('#recurrence-preset').click();
      await page.getByRole('option', { name: 'Custom…' }).click();

      // POTENTIAL CRITICAL FINDING — read before treating a failure here as
      // flaky/environmental: src/lib/calendar/recurrence.ts's `matchPreset`
      // (~line 690) NEVER equality-matches the 'custom' id itself (it's
      // always the else-branch fallback), and the 'custom' preset's own SEED
      // value in `computePresets` (~line 649) is literally `weeklyState` —
      // byte-identical to the 'weekly' preset's own state for the SAME
      // anchor. In CREATE mode (fresh "Does not repeat" → Custom), the state
      // handed to `onChange` by `handlePresetChange('custom')` is exactly
      // `defaultRecurrenceState(anchor)`, which is ALSO byte-identical to
      // the 'weekly' preset's state. `matchPreset` therefore resolves the
      // very next render to 'weekly' (checked before 'custom' is ever
      // considered), the Select's displayed value snaps to "Weekly on
      // <Weekday>", and the `{selectedId === 'custom' && (...)}` panel
      // (RecurrenceEditor.tsx line ~225) — which owns #recurrence-interval,
      // the weekday toggles, and the end-condition controls — never renders
      // at all. Re-opening the dropdown and re-selecting "Custom…" carries
      // the SAME state forward and collapses again — every named preset has
      // this same self-referential seed, so this reproduces for ANY anchor
      // date, not just Monday. src/components/calendar/CalendarEventSlideOver.test.tsx
      // (~line 319-326) independently documents the identical round-trip and
      // explicitly avoids relying on it to reach the panel, using an EDIT-mode
      // task with a pre-existing non-preset-matching trigger instead — an
      // escape hatch CREATE mode does not have. If this assertion times out,
      // it is very likely exposing that the "select Custom, configure a
      // multi-field custom rule" happy path (User Story 1, the RECORDED P1
      // bug this whole feature exists to fix) is UNREACHABLE when creating a
      // brand-new event — flag as CRITICAL, do not silently retry/skip.
      const intervalInput = page.locator('#recurrence-interval');
      await expect(
        intervalInput,
        'Custom panel (#recurrence-interval) did not appear after selecting "Custom…" from a fresh ' +
          '"Does not repeat" state — see the POTENTIAL CRITICAL FINDING comment above this assertion.',
      ).toBeVisible({ timeout: 5_000 });

      const mondayToggle = page.getByRole('button', { name: 'Monday', exact: true });
      await expect(mondayToggle).toHaveAttribute('aria-pressed', 'true');

      // Set the interval to 2 BEFORE the rejection sub-check below: at
      // interval=1 the post-toggle state would exactly match the "weekly"
      // NAMED preset (matchPreset/recurrenceStatesEqual compare interval
      // too), which would flip the Select's value away from "custom" and
      // unmount this very panel out from under the test. Doing it first
      // guarantees the state never again round-trips to a named preset.
      await intervalInput.fill('2');

      // ── Rejection sub-check (FR-019): deselecting the only weekday must
      // disable Save and show the inline validation message — proves the
      // validator is real, not a decorative disabled-forever button.
      await mondayToggle.click();
      await expect(mondayToggle).toHaveAttribute('aria-pressed', 'false');
      await expect(page.getByText('Select at least one day.')).toBeVisible();
      const createBtn = page.getByRole('button', { name: 'Create', exact: true });
      await expect(createBtn).toBeDisabled();

      // Re-select Monday — validity (and the button) must recover.
      await mondayToggle.click();
      await expect(mondayToggle).toHaveAttribute('aria-pressed', 'true');
      await expect(page.getByText('Select at least one day.')).not.toBeVisible();
      await expect(createBtn).toBeEnabled();

      await page.getByRole('button', { name: 'After', exact: true }).click();
      await page.getByLabel('Number of occurrences').fill('10');

      // FR-004: live plain-English summary, asserted BEFORE saving.
      await expect(page.getByTestId('recurrence-summary')).toHaveText(
        'Repeats every 2 weeks on Monday, 10 times',
      );

      await createBtn.click();
      await expect(slideOver).not.toBeVisible({ timeout: 10_000 });

      // Persistence: read the created task back via REST — a hardcoded/no-op
      // save would either produce no task at all, or one whose trigger isn't
      // a real, distinct RRULE (caught by the assertions below).
      const tasks = await fetchTasksApi(workspaceId);
      const created = tasks.find((t) => t.title === title);
      expect(created, `created task with title "${title}" must be found via GET /api/v1/tasks`).toBeTruthy();
      taskId = created!.id;

      expect(created!.trigger?.type).toBe('recurring');
      const config = created!.trigger!.config;
      expect(config.cron_expr).toBeUndefined();
      expect(typeof config.rrule).toBe('string');
      const rrule = config.rrule as string;
      expect(rrule).toContain('FREQ=WEEKLY');
      expect(rrule).toContain('INTERVAL=2');
      expect(rrule).toMatch(/BYDAY=(?:[A-Z]{2},)*MO(?:,[A-Z]{2})*/);
      expect(rrule).toContain('COUNT=10');

      expect(typeof config.dtstart_ms).toBe('number');
      const dtstart = new Date(config.dtstart_ms as number);
      expect(dtstart.getFullYear()).toBe(mondayDate.getFullYear());
      expect(dtstart.getMonth()).toBe(mondayDate.getMonth());
      expect(dtstart.getDate()).toBe(mondayDate.getDate());
      expect(dtstart.getHours()).toBe(9); // withDefaultHour(date, 9) — all-day cell click default

      expect(typeof config.tz).toBe('string');
      expect(config.tz).not.toBe('');
    } finally {
      if (taskId) await deleteTaskApi(taskId);
    }
  },
);

// ── (b) US-2: a weekly recurring task renders an occurrence chip on every ────
//     Monday in Month view, and in Week view ──────────────────────────────────

test(
  'Weekly task renders on every Monday of the month (US-2)',
  async ({ page }) => {
    // Traces to: calendar-recurrence-redesign-spec.md line 504 (Scenario:
    // Weekly task renders on every Monday of the month) / User Story 2,
    // Acceptance Scenario 1 / FR-008/009 / TDD plan Test 24.
    // BDD: Given a task repeating weekly on Monday at 09:00 exists in Ops,
    // When the operator views a month containing several Mondays,
    // Then a chip for that task renders on each Monday,
    // And switching to Week view shows the chip too.
    test.setTimeout(90_000);

    const title = `E2E Weekly Report ${Date.now()}`;
    // Far enough in the past that every Monday in the current Month/Week grid
    // is a real occurrence of this ongoing (no COUNT/UNTIL) weekly rule,
    // regardless of what date the suite runs on.
    const dtstart = priorMonday(new Date(), 60);

    const task = await createTaskApi({
      title,
      action: 'llm',
      workspace_id: workspaceId,
      surface: 'user',
      // A scheduled (recurring) task requires an assigned agent (backend
      // validation); the workspace's core_team seeds mia + jim.
      agent_id: 'mia',
      trigger: {
        type: 'recurring',
        config: {
          rrule: 'FREQ=WEEKLY;BYDAY=MO',
          dtstart_ms: dtstart.getTime(),
          tz: LOCAL_TZ,
        },
      },
    });

    try {
      await navigateToCalendar(page);

      // Differentiation from a one-off task: several chips, not one.
      const monthChips = page.locator('.fc-event', { hasText: title });
      await expect(monthChips.first()).toBeVisible({ timeout: 15_000 });
      const chipDates = await monthChips.evaluateAll((els) =>
        els.map((el) => el.closest('.fc-daygrid-day')?.getAttribute('data-date') ?? null),
      );
      expect(chipDates.length).toBeGreaterThanOrEqual(4); // any month grid covers >=4 Mondays
      expect(chipDates.length).toBeLessThanOrEqual(6);
      for (const dateStr of chipDates) {
        expect(dateStr, 'every occurrence chip must sit in a cell with a data-date').not.toBeNull();
        const [y, m, d] = (dateStr as string).split('-').map(Number);
        const cellDate = new Date(y, m - 1, d);
        expect(cellDate.getDay(), `${dateStr} must be a Monday`).toBe(1);
      }

      // Switching to Week view (US-2/AS-1): the currently-displayed week
      // always contains exactly one Monday, so exactly one chip renders.
      await page.getByTestId('calendar-view-timeGridWeek').click();
      await expect(page.locator('.fc-timeGridWeek-view')).toBeVisible({ timeout: 10_000 });
      const weekChips = page.locator('.fc-event', { hasText: title });
      await expect(weekChips).toHaveCount(1, { timeout: 10_000 });
    } finally {
      await deleteTaskApi(task.id);
    }
  },
);

// ── (c) US-4: agent filter narrows calendar chips, client-side ────────────────

test(
  'Agent filter narrows all task event kinds instantly (US-4)',
  async ({ page }) => {
    // Traces to: calendar-recurrence-redesign-spec.md line 644 (Scenario:
    // Agent filter narrows all task event kinds instantly) / User Story 4,
    // Acceptance Scenarios 1–3 / FR-015 / SC-004 / TDD plan Test 24.
    // BDD: Given Mia has a due-dated/fire task this week and Jim has one too,
    // When the operator selects "Mia" in the toolbar Agent dropdown,
    // Then only Mia's chips remain, with no network request issued for the
    // filtering itself.
    test.setTimeout(90_000);

    const miaTitle = `E2E Mia Fire ${Date.now()}`;
    const jimTitle = `E2E Jim Fire ${Date.now()}`;

    const miaTask = await createTaskApi({
      title: miaTitle,
      action: 'llm',
      workspace_id: workspaceId,
      surface: 'user',
      agent_id: 'mia',
      trigger: { type: 'once', config: { at_ms: todayAt(9, 0).getTime() } },
    });
    const jimTask = await createTaskApi({
      title: jimTitle,
      action: 'llm',
      workspace_id: workspaceId,
      surface: 'user',
      agent_id: 'jim',
      trigger: { type: 'once', config: { at_ms: todayAt(15, 0).getTime() } },
    });

    try {
      await navigateToCalendar(page);

      // Day view sidesteps FullCalendar's dayMaxEvents "+more" collapsing in
      // Month view — both timed fire chips are guaranteed visible here.
      await page.getByTestId('calendar-view-timeGridDay').click();
      await expect(page.locator('.fc-timeGridDay-view')).toBeVisible({ timeout: 10_000 });

      const miaChip = page.locator('.fc-event', { hasText: miaTitle });
      const jimChip = page.locator('.fc-event', { hasText: jimTitle });
      await expect(miaChip).toBeVisible({ timeout: 15_000 });
      await expect(jimChip).toBeVisible({ timeout: 10_000 });

      // Track network calls from this point on — SC-004 requires the filter
      // itself to issue ZERO additional requests (pure client-side predicate).
      const apiCallsAfterLoad: string[] = [];
      page.on('request', (req) => {
        if (req.url().includes('/api/v1/tasks')) apiCallsAfterLoad.push(req.url());
      });
      const countBeforeFilter = apiCallsAfterLoad.length;

      await page.getByTestId('calendar-agent-filter').click();
      await page.getByRole('option', { name: /Mia/i }).click();

      await expect(miaChip).toBeVisible({ timeout: 10_000 });
      await expect(jimChip).not.toBeVisible({ timeout: 10_000 });

      // Give any accidental refetch a moment to fire before asserting none did.
      await page.waitForTimeout(700);
      expect(
        apiCallsAfterLoad.length,
        `agent filter selection must not issue a task/occurrence request; saw: ${apiCallsAfterLoad.slice(countBeforeFilter).join(', ')}`,
      ).toBe(countBeforeFilter);
    } finally {
      await deleteTaskApi(miaTask.id);
      await deleteTaskApi(jimTask.id);
    }
  },
);

// ── (d) US-3: scheduled tasks (once/every/recurring) are calendar-only ─────────

test(
  'Recurring tasks are absent from Board and List (US-3)',
  async ({ page }) => {
    // Traces to: calendar-recurrence-redesign-spec.md line 615 (Scenario:
    // Recurring tasks are absent from Board and List) / User Story 3,
    // Acceptance Scenarios 1–2 / FR-011 / TDD plan Test 24.
    //
    // BROADENED per operator ruling 2026-08-07 ("recurring and ALL scheduled
    // tasks are calendar-only"), implemented in commit 364d00b2
    // (`isScheduledTrigger` in src/components/workspaces/taskFormFields.ts,
    // now matching `once`/`every`/`recurring` — not just `every`/`recurring`
    // — in both BoardView.tsx and ListView.tsx). The original US-3 boundary
    // only excluded the recurring task and kept the once task visible; that
    // is no longer the rule. See boardListExclusion.test.ts for the unit
    // coverage of the predicate itself — this test proves the same boundary
    // holds end-to-end AND that excluded tasks actually surface on the
    // Calendar tab rather than merely vanishing.
    //
    // BDD: Given Ops contains a manual task, a once task, and a recurring
    // task, When the operator opens the Board, Then it shows exactly the
    // manual task — the once and recurring tasks appear in neither Board nor
    // List, and instead render as events on the workspace Calendar.
    test.setTimeout(90_000);

    const manualTitle = `E2E Board Manual ${Date.now()}`;
    const onceTitle = `E2E Board Once ${Date.now()}`;
    const recurringTitle = `E2E Board Recurring ${Date.now()}`;
    const dtstart = priorMonday(new Date(), 60);

    const manualTask = await createTaskApi({
      title: manualTitle,
      action: 'llm',
      workspace_id: workspaceId,
      surface: 'user',
      trigger: { type: 'manual', config: {} },
    });
    const onceTask = await createTaskApi({
      title: onceTitle,
      action: 'llm',
      workspace_id: workspaceId,
      surface: 'user',
      // once is a scheduled trigger → agent_id required (backend validation).
      agent_id: 'mia',
      trigger: { type: 'once', config: { at_ms: todayAt(11, 0).getTime() } },
    });
    const recurringTask = await createTaskApi({
      title: recurringTitle,
      action: 'llm',
      workspace_id: workspaceId,
      surface: 'user',
      // recurring is a scheduled trigger → agent_id required (backend validation).
      agent_id: 'mia',
      trigger: {
        type: 'recurring',
        config: { rrule: 'FREQ=WEEKLY;BYDAY=TU', dtstart_ms: dtstart.getTime(), tz: LOCAL_TZ },
      },
    });

    try {
      await navigateToBoard(page);

      // Give the board's own task fetch a moment past the Inbox-column mount
      // before asserting absence (a false negative from "not rendered yet"
      // would be indistinguishable from a real exclusion otherwise).
      // The manual task is the Binding-Rule-4 positive control: a filter
      // that hides everything must fail this assertion.
      await expect(page.getByText(manualTitle)).toBeVisible({ timeout: 15_000 });

      // Neither scheduled task — once OR recurring — may appear anywhere on
      // the Board. `once` staying visible was the OLD (now-superseded)
      // boundary; asserting its absence here is the whole point of this fix.
      await expect(page.getByText(onceTitle)).not.toBeVisible({ timeout: 10_000 });
      await expect(page.getByText(recurringTitle)).not.toBeVisible({ timeout: 5_000 });
      const onceCount = await page.getByText(onceTitle).count();
      const recurringCount = await page.getByText(recurringTitle).count();
      expect(onceCount, 'once task title must not be present anywhere on the Board DOM').toBe(0);
      expect(recurringCount, 'recurring task title must not be present anywhere on the Board DOM').toBe(0);

      // Prove "moved to Calendar", not merely "hidden": both scheduled tasks
      // must render as event chips on the workspace Calendar tab. Month view
      // (the calendar's default on a fresh mount — CalendarScreen.tsx's
      // `currentView` initial state is 'dayGridMonth', not persisted across
      // navigations) covers both — the once task fires today at 11:00 (in
      // the currently-displayed month) and the recurring rule's Tuesday
      // dtstart is far enough in the past (priorMonday(…, 60)) that the
      // current month grid contains a real occurrence, mirroring the (b)
      // "Weekly task renders" assertion above.
      await navigateToCalendar(page);
      await expect(page.locator('.fc-event', { hasText: onceTitle }).first()).toBeVisible({
        timeout: 15_000,
      });
      await expect(page.locator('.fc-event', { hasText: recurringTitle }).first()).toBeVisible({
        timeout: 10_000,
      });
    } finally {
      await deleteTaskApi(manualTask.id);
      await deleteTaskApi(onceTask.id);
      await deleteTaskApi(recurringTask.id);
    }
  },
);
