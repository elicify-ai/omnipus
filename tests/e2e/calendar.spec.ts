/**
 * calendar.spec.ts — E2E tests for the workspace Calendar (FullCalendar v6).
 *
 * Spec: docs/internal/specs/workspace-calendar-fullcalendar-spec.md (v2)
 * TDD plan #22: grid renders (incl. empty), switch all views, New-task button.
 * Traces to: spec §9 #22 / US-1..US-4 / SC-001 / SC-003 / FR-001 / FR-006
 *
 * LLM-independent: no agent turns, no model calls required.
 * A temporary workspace is created via API and deleted in afterAll.
 *
 * jsdom limitation (spec F-03): real FullCalendar grid rendering, view-class
 * toggling, and pointer-event geometry are NOT testable in unit/integration
 * tests. This E2E file is the authoritative test for those behaviors.
 *
 * Convention mirrors existing tests/e2e/*.spec.ts files:
 *   - Uses `test` from './fixtures/console-errors' (console-error guard fixture)
 *   - Uses pre-authenticated global storageState (playwright.config.ts)
 *   - HashRouter: navigate via `page.goto('/#/workspaces/{id}/calendar')`
 *   - Workspace created via REST in beforeAll and deleted in afterAll
 */

import { expect, request as apiRequest } from '@playwright/test';
import { test } from './fixtures/console-errors';

// ── Workspace lifecycle helpers ────────────────────────────────────────────────

const OMNIPUS_URL = process.env.OMNIPUS_URL || 'http://localhost:6060';
const ADMIN_USERNAME = 'admin';
const ADMIN_PASSWORD = 'admin123';

/**
 * Obtain a fresh bearer token for REST calls inside the test.
 * Uses the pre-onboarded admin credentials (seeded by global-setup.ts).
 */
async function getAdminToken(): Promise<string> {
  const ctx = await apiRequest.newContext({ baseURL: OMNIPUS_URL });
  try {
    const res = await ctx.post('/api/v1/auth/login', {
      data: { username: ADMIN_USERNAME, password: ADMIN_PASSWORD },
    });
    if (!res.ok()) {
      const body = await res.text();
      throw new Error(`calendar.spec.ts: login failed ${res.status()}: ${body}`);
    }
    const json = (await res.json()) as { token: string };
    return json.token;
  } finally {
    await ctx.dispose();
  }
}

/**
 * Create a throwaway workspace and return its ID.
 * The workspace name includes a timestamp to avoid collisions across retries.
 */
async function createTestWorkspace(token: string): Promise<string> {
  const ctx = await apiRequest.newContext({
    baseURL: OMNIPUS_URL,
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  });
  try {
    const name = `E2E Calendar Workspace ${Date.now()}`;
    const res = await ctx.post('/api/v1/workspaces', {
      data: { name },
    });
    if (!res.ok()) {
      const body = await res.text();
      throw new Error(`calendar.spec.ts: createWorkspace failed ${res.status()}: ${body}`);
    }
    const ws = (await res.json()) as { id: string };
    return ws.id;
  } finally {
    await ctx.dispose();
  }
}

/**
 * Delete a workspace by ID (best-effort — ignore failures in cleanup).
 */
async function deleteTestWorkspace(token: string, workspaceId: string): Promise<void> {
  const ctx = await apiRequest.newContext({
    baseURL: OMNIPUS_URL,
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  });
  try {
    await ctx.delete(`/api/v1/workspaces/${encodeURIComponent(workspaceId)}`);
  } catch {
    // Best-effort cleanup; do not fail the test.
  } finally {
    await ctx.dispose();
  }
}

// ── Test state ────────────────────────────────────────────────────────────────

let adminToken: string;
let workspaceId: string;

// ── Setup / teardown ──────────────────────────────────────────────────────────

test.beforeAll(async () => {
  adminToken = await getAdminToken();
  workspaceId = await createTestWorkspace(adminToken);
});

test.afterAll(async () => {
  if (workspaceId && adminToken) {
    await deleteTestWorkspace(adminToken, workspaceId);
  }
});

// ── Helper: navigate to the calendar tab ────────────────────────────────────

/**
 * Navigate to the workspace Calendar tab and wait for it to mount.
 *
 * FullCalendar v6 renders a `.fc` wrapper element on mount. We wait for it
 * before asserting on any calendar-specific content. The lazy `CalendarScreen`
 * chunk is loaded on first visit, which may take 2–5 s on first load.
 *
 * Resilient to an empty workspace (no tasks / no milestones): the grid always
 * renders per spec FR-001 / SC-001.
 */
async function navigateToCalendar(page: import('@playwright/test').Page): Promise<void> {
  await page.goto(`/#/workspaces/${workspaceId}/calendar`);
  // Wait for the FullCalendar root element — always rendered even with 0 events (FR-001)
  await expect(page.locator('.fc')).toBeVisible({ timeout: 30_000 });
}

// ── Tests ─────────────────────────────────────────────────────────────────────

test(
  '(a) calendar grid renders even with no tasks or milestones',
  async ({ page }) => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #22 / SC-001 / FR-001 / US-1/AS-1
    // BDD: Given a workspace with no tasks and no milestones,
    // When I open the Calendar tab,
    // Then the month grid renders with weekday headers and day cells — always.

    await navigateToCalendar(page);

    // FullCalendar's daygrid renders a .fc-daygrid-body (or .fc-daygrid) element
    // containing the day cells. Assert it exists (SC-001).
    await expect(page.locator('.fc-daygrid')).toBeVisible({ timeout: 10_000 });

    // The toolbar we built (CalendarToolbar.tsx) should also be visible
    await expect(page.getByTestId('calendar-toolbar')).toBeVisible({ timeout: 10_000 });

    // Month view is the default — .fc-dayGridMonth-view must be in the DOM
    await expect(page.locator('.fc-dayGridMonth-view')).toBeVisible({ timeout: 10_000 });
  },
);

test(
  '(b) Month view has day cells and weekday header row',
  async ({ page }) => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #22 / SC-001 / US-1/AS-1
    // BDD: The grid must have weekday headers + day cells (28–31 per SC-001).

    await navigateToCalendar(page);

    // Weekday header row (Mon, Tue, ... Sun) rendered by FullCalendar daygrid
    const dayHeaders = page.locator('.fc-col-header-cell');
    await expect(dayHeaders.first()).toBeVisible({ timeout: 10_000 });
    // There must be exactly 7 column headers (one per weekday)
    await expect(dayHeaders).toHaveCount(7, { timeout: 10_000 });

    // Day cells — the month grid renders between 28 and 42 .fc-daygrid-day cells
    const dayCells = page.locator('.fc-daygrid-day');
    const cellCount = await dayCells.count();
    expect(cellCount).toBeGreaterThanOrEqual(28);
    expect(cellCount).toBeLessThanOrEqual(42);
  },
);

test(
  '(c) switch to Week view renders the time-grid (timeGridWeek)',
  async ({ page }) => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #22 / SC-003 / FR-006 / US-2/AS-1
    // BDD: When I select Week via the toolbar, timeGridWeek renders the week grid.

    await navigateToCalendar(page);

    // Click the Week tab button via testid
    const weekBtn = page.getByTestId('calendar-view-timeGridWeek');
    await expect(weekBtn).toBeVisible({ timeout: 10_000 });
    await weekBtn.click();

    // FullCalendar adds fc-timeGridWeek-view to the view container
    await expect(page.locator('.fc-timeGridWeek-view')).toBeVisible({ timeout: 10_000 });

    // Month view class must be gone
    await expect(page.locator('.fc-dayGridMonth-view')).not.toBeVisible({ timeout: 5_000 });
  },
);

test(
  '(d) switch to Day view renders the time-grid (timeGridDay)',
  async ({ page }) => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #22 / SC-003 / FR-006 / US-2/AS-1
    // BDD: When I select Day via the toolbar, timeGridDay renders.

    await navigateToCalendar(page);

    const dayBtn = page.getByTestId('calendar-view-timeGridDay');
    await expect(dayBtn).toBeVisible({ timeout: 10_000 });
    await dayBtn.click();

    await expect(page.locator('.fc-timeGridDay-view')).toBeVisible({ timeout: 10_000 });
    await expect(page.locator('.fc-dayGridMonth-view')).not.toBeVisible({ timeout: 5_000 });
  },
);

test(
  '(e) switch to Agenda view renders the list (listWeek)',
  async ({ page }) => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #22 / SC-003 / FR-006 / US-2/AS-3
    // BDD: When I select Agenda via the toolbar, listWeek renders.
    // With no events: a themed "No events" / "No scheduled items" message appears.

    await navigateToCalendar(page);

    const agendaBtn = page.getByTestId('calendar-view-listWeek');
    await expect(agendaBtn).toBeVisible({ timeout: 10_000 });
    await agendaBtn.click();

    // FullCalendar adds fc-listWeek-view to the view container
    await expect(page.locator('.fc-listWeek-view')).toBeVisible({ timeout: 10_000 });
  },
);

test(
  '(f) switch back to Month view after visiting other views',
  async ({ page }) => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #22 / SC-003 / FR-006
    // BDD: Switching views and returning to Month restores the month daygrid.

    await navigateToCalendar(page);

    // Go to Week
    await page.getByTestId('calendar-view-timeGridWeek').click();
    await expect(page.locator('.fc-timeGridWeek-view')).toBeVisible({ timeout: 10_000 });

    // Return to Month
    await page.getByTestId('calendar-view-dayGridMonth').click();
    await expect(page.locator('.fc-dayGridMonth-view')).toBeVisible({ timeout: 10_000 });
  },
);

test(
  '(g) toolbar prev / next / today buttons are present and clickable',
  async ({ page }) => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #22 / FR-006 / US-2/AS-2
    // BDD: Clicking prev / next / today navigates the calendar period.

    await navigateToCalendar(page);

    const prevBtn = page.getByTestId('calendar-prev');
    const nextBtn = page.getByTestId('calendar-next');
    const todayBtn = page.getByTestId('calendar-today');

    await expect(prevBtn).toBeVisible({ timeout: 10_000 });
    await expect(nextBtn).toBeVisible({ timeout: 10_000 });
    await expect(todayBtn).toBeVisible({ timeout: 10_000 });

    // Click next — the view should re-render (title changes)
    const titleBefore = await page.locator('[aria-live="polite"]').first().textContent();
    await nextBtn.click();
    // Give FC a moment to update the title
    await page.waitForTimeout(500);
    const titleAfter = await page.locator('[aria-live="polite"]').first().textContent();
    // The period title must have changed
    expect(titleAfter).not.toBe(titleBefore);

    // Click today — should return to the current period
    await todayBtn.click();
    await page.waitForTimeout(500);
    const titleToday = await page.locator('[aria-live="polite"]').first().textContent();
    // After today, title should match what it was before we clicked next
    expect(titleToday).toBe(titleBefore);
  },
);

test(
  '(h) New-task button opens the create slide-over',
  async ({ page }) => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #22 / FR-012 / US-4/AS-1
    // BDD: Clicking "New task" in the toolbar opens CreateTaskSlideOver.
    //
    // CreateTaskSlideOver renders a Sheet panel (src/components/ui/sheet.tsx).
    // When open, Radix Sheet renders a [data-state="open"] dialog element.

    await navigateToCalendar(page);

    const newTaskBtn = page.getByTestId('calendar-new-task');
    await expect(newTaskBtn).toBeVisible({ timeout: 10_000 });
    await newTaskBtn.click();

    // CreateTaskSlideOver uses Radix Sheet — look for a Sheet dialog in 'open' state.
    // The SheetContent renders as role="dialog" with data-state="open" when visible.
    const slideOver = page.locator('[role="dialog"][data-state="open"]');
    await expect(slideOver).toBeVisible({ timeout: 10_000 });

    // The Create Task form must have a Title field (CreateTaskSlideOver renders it)
    await expect(page.getByLabel(/title/i)).toBeVisible({ timeout: 5_000 });
  },
);

test(
  '(i) all four view tabs are present in the toolbar',
  async ({ page }) => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #22 / FR-006 / US-2/AS-1
    // BDD: All four views (Month/Week/Day/Agenda) must be reachable via the toolbar.

    await navigateToCalendar(page);

    await expect(page.getByTestId('calendar-view-dayGridMonth')).toBeVisible({ timeout: 10_000 });
    await expect(page.getByTestId('calendar-view-timeGridWeek')).toBeVisible({ timeout: 10_000 });
    await expect(page.getByTestId('calendar-view-timeGridDay')).toBeVisible({ timeout: 10_000 });
    await expect(page.getByTestId('calendar-view-listWeek')).toBeVisible({ timeout: 10_000 });
  },
);
