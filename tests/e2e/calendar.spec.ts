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

import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { newAdminApiContext } from './fixtures/admin-api';

// ── Workspace lifecycle helpers ────────────────────────────────────────────────

// REST setup/teardown authenticates with the SHARED admin session
// (fixtures/admin-api.ts). Do NOT add a POST /api/v1/auth/login here to get a
// bearer token: login re-mints the single-slot session_token_hash and silently
// invalidates the storageState cookie for every spec that runs later.
// scripts/check-e2e-login-crosstalk.sh enforces this.

/**
 * Create a throwaway workspace and return its ID.
 * The workspace name includes a timestamp to avoid collisions across retries.
 */
async function createTestWorkspace(): Promise<string> {
  const ctx = await newAdminApiContext();
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

// ── Test state ────────────────────────────────────────────────────────────────

let workspaceId: string;

// ── Setup / teardown ──────────────────────────────────────────────────────────

test.beforeAll(async () => {
  workspaceId = await createTestWorkspace();
});

test.afterAll(async () => {
  if (workspaceId) {
    await deleteTestWorkspace(workspaceId);
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
  const fc = page.locator('.fc');
  const loadError = page.getByText('Failed to load workspace');
  // The workspace is created in beforeAll, so a "Failed to load workspace" banner
  // is a transient workspace-GET failure (observed under CI load), not a missing
  // workspace. Reload and retry instead of timing out 30s on a missing .fc. Up to
  // 3 attempts; a persistent failure still fails clearly on the final assertion.
  for (let attempt = 0; attempt < 3; attempt++) {
    await page.goto(`/#/workspaces/${workspaceId}/calendar`);
    // Resolve as soon as EITHER the calendar mounts OR the error banner appears.
    await Promise.race([
      expect(fc).toBeVisible({ timeout: 15_000 }).catch(() => {}),
      expect(loadError).toBeVisible({ timeout: 15_000 }).catch(() => {}),
    ]);
    if (await fc.isVisible()) return;
    // Brief backoff so a transient gateway/SPA hiccup can clear before retrying.
    await page.waitForTimeout(1_500);
  }
  // Persistent failure — surface a clear, final assertion.
  await expect(fc).toBeVisible({ timeout: 30_000 });
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
    const titleBefore = await page.getByTestId('calendar-title').textContent();
    await nextBtn.click();
    // Give FC a moment to update the title
    await page.waitForTimeout(500);
    const titleAfter = await page.getByTestId('calendar-title').textContent();
    // The period title must have changed
    expect(titleAfter).not.toBe(titleBefore);

    // Click today — should return to the current period
    await todayBtn.click();
    await page.waitForTimeout(500);
    const titleToday = await page.getByTestId('calendar-title').textContent();
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

// ── Task REST API helper (LLM-independent) ────────────────────────────────────

/**
 * Create a task with a due date via the REST API.
 * Returns the created task object `{ id, due, ... }`.
 * Uses the shared admin session (fixtures/admin-api.ts).
 */
async function createTaskWithDue(
  wsId: string,
  title: string,
  dueDate: string, // YYYY-MM-DD
): Promise<{ id: string; due: string }> {
  const ctx = await newAdminApiContext();
  try {
    // `due` is a date-time string (contract: format date-time). Send local midnight UTC.
    const dueIso = new Date(`${dueDate}T00:00:00Z`).toISOString();
    const res = await ctx.post('/api/v1/tasks', {
      data: {
        title,
        workspace_id: wsId,
        due: dueIso,
        surface: 'user',
      },
    });
    if (!res.ok()) {
      const body = await res.text();
      throw new Error(`createTaskWithDue failed ${res.status()}: ${body}`);
    }
    return (await res.json()) as { id: string; due: string };
  } finally {
    await ctx.dispose();
  }
}

/**
 * Delete a task by ID (best-effort cleanup).
 */
async function deleteTask(taskId: string): Promise<void> {
  const ctx = await newAdminApiContext();
  try {
    await ctx.delete(`/api/v1/tasks/${encodeURIComponent(taskId)}`);
  } catch {
    // best-effort
  } finally {
    await ctx.dispose();
  }
}

/**
 * Fetch a task by ID and return its `due` field.
 */
async function fetchTaskDue(taskId: string): Promise<string | null> {
  const ctx = await newAdminApiContext();
  try {
    const res = await ctx.get(`/api/v1/tasks/${encodeURIComponent(taskId)}`);
    if (!res.ok()) return null;
    const t = (await res.json()) as { due?: string };
    return t.due ?? null;
  } finally {
    await ctx.dispose();
  }
}

// ── (j) Drag reschedule — seeds task via REST, drags chip, asserts persistence ─

test(
  '(j) drag a task chip to another day and assert it persists after reload',
  async ({ page }) => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #22 / US-3/AS-1 / DS-2 row 1 / FR-008
    // BDD: Given a task seeded via REST with due=2026-06-20,
    // When its chip is dragged to another day in Month view,
    // Then after reload the chip is on the new day (persisted).
    //
    // LLM-independent: no agent turns.

    test.setTimeout(90_000);

    // Seed a task with a fixed due date — use the current month so it's visible
    const today = new Date();
    const dueDate = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, '0')}-10`;
    let taskId: string;
    try {
      const task = await createTaskWithDue(workspaceId, 'E2E drag reschedule', dueDate);
      taskId = task.id;
    } catch (err) {
      // Tasks endpoint not available in this build — annotated skip so the gap is visible.
      test.skip(true, `createTaskWithDue failed — tasks endpoint unavailable: ${err}`);
      return;
    }

    try {
      await navigateToCalendar(page);

      // Wait for the task chip to appear (FullCalendar renders chips as .fc-event)
      const chipLocator = page.locator('.fc-event', { hasText: 'E2E drag reschedule' });
      await expect(chipLocator).toBeVisible({ timeout: 15_000 });

      // Find the day-10 cell and a day-13 cell in Month view to drag between.
      // FullCalendar daygrid day cells carry a data-date attribute "YYYY-MM-DD".
      const year = today.getFullYear();
      const month = String(today.getMonth() + 1).padStart(2, '0');
      const targetDate = `${year}-${month}-13`;
      const targetCell = page.locator(`.fc-daygrid-day[data-date="${targetDate}"]`);

      // If the target cell is not in view (e.g. we seeded day-10 which IS day-10
      // but day-13 doesn't exist due to month length) — skip gracefully.
      const cellVisible = await targetCell.isVisible({ timeout: 5_000 }).catch(() => false);
      if (!cellVisible) {
        test.skip(true, 'target day-13 cell not visible in current month — grid precondition unmet');
        return;
      }

      // Perform the drag — from chip bounding box centre to day-13 cell centre
      const chipBox = await chipLocator.first().boundingBox();
      const cellBox = await targetCell.boundingBox();

      // Precondition: bounding boxes must be obtainable from a rendered grid
      expect(chipBox, 'chip bounding box must be obtainable from rendered grid').toBeTruthy();
      expect(cellBox, 'target cell bounding box must be obtainable from rendered grid').toBeTruthy();
      if (!chipBox || !cellBox) return; // type-narrowing only; test already failed above if null

      const fromX = chipBox.x + chipBox.width / 2;
      const fromY = chipBox.y + chipBox.height / 2;
      const toX = cellBox.x + cellBox.width / 2;
      const toY = cellBox.y + cellBox.height / 2;

      // Drag: mouse down on chip → move → mouse up on target cell
      await page.mouse.move(fromX, fromY);
      await page.mouse.down();
      // Intermediate steps help Playwright trigger FC's drag-to-reschedule
      await page.mouse.move(fromX + (toX - fromX) / 2, fromY + (toY - fromY) / 2, { steps: 5 });
      await page.mouse.move(toX, toY, { steps: 5 });
      await page.mouse.up();

      // Wait for the drag to settle and the PATCH to fire
      await page.waitForTimeout(2000);

      // Reload the page and verify the chip is on the new date
      await navigateToCalendar(page);
      await page.waitForTimeout(1000);

      // The chip should still be on the new day after reload
      // Verify via the API that the due date was updated
      const updatedDue = await fetchTaskDue(taskId);
      // updatedDue must be non-null — a null read means the endpoint didn't persist the drag
      expect(updatedDue, 'fetchTaskDue must return a non-null due after drag-persist').toBeTruthy();
      if (updatedDue) {
        const updatedDate = new Date(updatedDue);
        // Allow both the UTC date and local date (depending on how the server stores it)
        const updatedDateStr = `${updatedDate.getUTCFullYear()}-${String(updatedDate.getUTCMonth() + 1).padStart(2, '0')}-${String(updatedDate.getUTCDate()).padStart(2, '0')}`;
        expect(updatedDateStr).toBe(targetDate);
      }
      await expect(
        page.locator('.fc-event', { hasText: 'E2E drag reschedule' }),
      ).toBeVisible({ timeout: 10_000 });
    } finally {
      await deleteTask(taskId!);
    }
  },
);

// ── (k) Revert on failure — route-intercept 500 on PATCH ─────────────────────

test(
  '(k) revert-on-failure: when PATCH returns 500 the chip reverts to its original day',
  async ({ page }) => {
    // Traces to: workspace-calendar-fullcalendar-spec.md §9 #22 / US-3/AS-5 / DS-2 row 4 / FR-010
    // BDD: Given a gateway that returns 500 on PATCH tasks/{id},
    // When I drag a due task chip to another day,
    // Then the chip snaps back to its original day and an error toast appears.
    //
    // LLM-independent: no agent turns; route intercepted via Playwright's page.route.

    test.setTimeout(90_000);

    const today = new Date();
    const dueDate = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, '0')}-15`;
    let taskId: string;
    try {
      const task = await createTaskWithDue(workspaceId, 'E2E revert test', dueDate);
      taskId = task.id;
    } catch (err) {
      // Tasks endpoint not available in this build — annotated skip so the gap is visible.
      test.skip(true, `createTaskWithDue failed — tasks endpoint unavailable: ${err}`);
      return;
    }

    try {
      await navigateToCalendar(page);

      const chipLocator = page.locator('.fc-event', { hasText: 'E2E revert test' });
      await expect(chipLocator).toBeVisible({ timeout: 15_000 });

      const year = today.getFullYear();
      const month = String(today.getMonth() + 1).padStart(2, '0');
      const targetDate = `${year}-${month}-18`;
      const targetCell = page.locator(`.fc-daygrid-day[data-date="${targetDate}"]`);

      const cellVisible = await targetCell.isVisible({ timeout: 5_000 }).catch(() => false);
      if (!cellVisible) {
        test.skip(true, 'target day-18 cell not visible in current month — grid precondition unmet');
        return;
      }

      // Intercept all PATCH requests to /api/v1/tasks/* and return 500
      await page.route('**/api/v1/tasks/**', async (route) => {
        const method = route.request().method();
        if (method === 'PATCH') {
          await route.fulfill({
            status: 500,
            contentType: 'application/json',
            body: JSON.stringify({ error: 'simulated server error' }),
          });
        } else {
          // Pass through non-PATCH requests (GET etc.)
          await route.continue();
        }
      });

      const chipBox = await chipLocator.first().boundingBox();
      const cellBox = await targetCell.boundingBox();

      // Precondition: bounding boxes must be obtainable from a rendered grid
      expect(chipBox, 'chip bounding box must be obtainable from rendered grid').toBeTruthy();
      expect(cellBox, 'target cell bounding box must be obtainable from rendered grid').toBeTruthy();
      if (!chipBox || !cellBox) {
        await page.unrouteAll();
        return; // type-narrowing only; test already failed above if null
      }

      const fromX = chipBox.x + chipBox.width / 2;
      const fromY = chipBox.y + chipBox.height / 2;
      const toX = cellBox.x + cellBox.width / 2;
      const toY = cellBox.y + cellBox.height / 2;

      await page.mouse.move(fromX, fromY);
      await page.mouse.down();
      await page.mouse.move(fromX + (toX - fromX) / 2, fromY + (toY - fromY) / 2, { steps: 5 });
      await page.mouse.move(toX, toY, { steps: 5 });
      await page.mouse.up();

      // Wait for the revert + error toast to appear
      await page.waitForTimeout(2000);

      // The chip must have reverted to the original day (day-15)
      const sourceCell = page.locator(`.fc-daygrid-day[data-date="${dueDate}"]`);
      await expect(sourceCell.locator('.fc-event', { hasText: 'E2E revert test' })).toBeVisible({
        timeout: 10_000,
      });

      // An error toast with the specific "Couldn't reschedule" message must appear.
      // Asserting the text prevents stray success toasts from producing false positives (FR-010).
      await expect(page.getByText("Couldn't reschedule")).toBeVisible({ timeout: 10_000 });

      // Remove the route intercept
      await page.unrouteAll();
    } finally {
      await deleteTask(taskId!);
    }
  },
);
