import { test, request as pwRequest } from '@playwright/test';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { discoverDynamicIds, loadRouteSurfaces, resolveRoutes, type ResolvedRoute } from '../lib/routes';
import { INTERACTIVE_SELECTOR, measurePage, type KeyRowBudget } from '../lib/measure';
import { recordResult, type ResultEntry } from '../lib/report';
import { projectNameToEnvKind } from '../lib/env';
import { reauthenticate } from '../lib/reauth';
import { readCredentials } from '../lib/credentials';
import { RAW_RESULTS_DIR, SCREENSHOTS_DIR } from '../lib/paths';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

interface KeyRowBudgetFileEntry {
  id: string;
  selector: string;
  appliesToRouteIds: string[];
  budgetPx: Record<string, number>;
}

const keyRowBudgetFile = JSON.parse(
  readFileSync(path.resolve(__dirname, '../budgets/key-rows.json'), 'utf8'),
) as { rows: KeyRowBudgetFileEntry[] };

function budgetsForRoute(routeId: string): KeyRowBudget[] {
  return keyRowBudgetFile.rows
    .filter((row) => row.appliesToRouteIds.includes(routeId))
    .map((row) => ({ id: row.id, selector: row.selector, budgetPx: row.budgetPx as KeyRowBudget['budgetPx'] }));
}

/**
 * Resolved once per worker (each project's spec file runs in its own
 * process) via a lightweight authenticated APIRequestContext — the same
 * REST endpoints the SPA itself calls (see lib/routes.ts), not a UI probe.
 *
 * Logs in for itself rather than reusing global-setup.ts's storageState
 * file: the session cookie is single-slot per user (see lib/reauth.ts's doc
 * comment), so this stays correct even if file execution order ever changes.
 */
let resolvedRoutes: ResolvedRoute[] = [];

test.beforeAll(async () => {
  const baseURL = process.env.OMNIPUS_URL ?? 'http://127.0.0.1:6060';
  const { username, password } = readCredentials();
  const apiContext = await pwRequest.newContext({ baseURL });
  try {
    const loginResp = await apiContext.post('/api/v1/auth/login', { data: { username, password } });
    if (!loginResp.ok()) {
      throw new Error(`[touch-in-context] route discovery login failed: ${loginResp.status()}`);
    }
    const ids = await discoverDynamicIds(apiContext);
    resolvedRoutes = resolveRoutes(loadRouteSurfaces(), ids);
  } finally {
    await apiContext.dispose();
  }
});

/**
 * Best-effort settle after navigation. Deliberately NOT `waitForLoadState('networkidle')`:
 * chat routes hold a persistent WebSocket open, and several other routes poll —
 * neither is expected to ever go idle.
 *
 * Two earlier versions of this both undercounted, against this suite's own
 * baseline runs:
 *   - v1: a flat 800ms wait — 33 of 140 route checks (23.6%) reported zero
 *     interactive controls, including routes that unmistakably have some
 *     (`_app/agents` renders agent cards fetched over the network).
 *   - v2: wait for "at least one candidate control exists" — WORSE (83/140
 *     at zero), because AppShell's persistent nav (`a[href]` links in the
 *     sidebar) already matches that condition before any route-specific,
 *     fetched content (the agent cards themselves) has rendered, so it
 *     resolved instantly and measured too early anyway.
 *
 * The persistent-chrome problem means "something exists" can never be the
 * right signal — the sidebar always satisfies it. What actually indicates
 * "this route is done adding controls" is the CONTROL COUNT GOING STABLE:
 * poll it every 300ms and stop once two consecutive polls agree (600ms with
 * no new controls appearing), capped at 5s total. A route that's still
 * growing at 5s settles for whatever it has then — real data about a slow
 * route, not a harness bug to chase further.
 */
async function settle(page: import('@playwright/test').Page): Promise<void> {
  const deadline = Date.now() + 5_000;
  let previousCount = -1;
  let stableStreak = 0;
  while (Date.now() < deadline) {
    const count = await page
      .evaluate((sel) => document.querySelectorAll(sel).length, INTERACTIVE_SELECTOR)
      .catch(() => 0);
    // A plateau AT ZERO never counts as "settled" — this early in a fresh
    // navigation, zero controls almost always means "React hasn't mounted
    // yet", not "this route genuinely has none". Two consecutive equal
    // polls only short-circuits the wait once something real has appeared;
    // a route that is still at zero when the 5s deadline hits rides out the
    // full budget and that IS a genuine, reportable data point.
    if (count > 0 && count === previousCount) {
      stableStreak++;
      if (stableStreak >= 2) return;
    } else {
      stableStreak = 0;
    }
    previousCount = count;
    await page.waitForTimeout(300);
  }
}

async function runRouteCheck(
  page: import('@playwright/test').Page,
  route: ResolvedRoute,
  projectName: string,
  browserName: string,
): Promise<void> {
  const envKind = projectNameToEnvKind(projectName);
  const baseEntry = {
    kind: 'route-check' as const,
    id: route.id,
    templatePath: route.templatePath,
    project: projectName,
    envKind,
    browserName,
    timestamp: new Date().toISOString(),
  };

  if (route.skippedReason) {
    recordResult(RAW_RESULTS_DIR, { ...baseEntry, status: 'skipped', skippedReason: route.skippedReason, violations: [] });
    return;
  }

  const resolvedPath = route.resolvedPath as string;
  const hashUrl = `/#${resolvedPath}`;

  let entry: ResultEntry;
  try {
    await page.goto(hashUrl, { waitUntil: 'domcontentloaded' });
    await settle(page);
    const measurement = await measurePage(page, envKind, budgetsForRoute(route.id));
    const status = measurement.violations.length > 0 ? 'violations' : 'ok';
    entry = {
      ...baseEntry,
      status,
      controlsChecked: measurement.controlsChecked,
      violations: measurement.violations,
      keyRows: measurement.keyRows,
      resolvedPath: hashUrl,
    };
    if (status === 'violations') {
      const shotName = `${route.id.replace(/[^a-zA-Z0-9_.-]/g, '_')}-${projectName}.png`;
      const shotPath = path.join(SCREENSHOTS_DIR, shotName);
      await page.screenshot({ path: shotPath }).catch(() => undefined);
      entry.screenshotPath = shotPath;
    }
  } catch (err) {
    entry = {
      ...baseEntry,
      status: 'error',
      violations: [],
      resolvedPath: hashUrl,
      errorMessage: err instanceof Error ? err.message : String(err),
    };
  }
  recordResult(RAW_RESULTS_DIR, entry);
}

test.describe('authenticated route sweep', () => {
  // Re-authenticates immediately before this test's own work starts — see
  // lib/reauth.ts's doc comment for why a shared, previously-captured
  // storageState is not safe to rely on (the session cookie is single-slot
  // per user; workers:1 in the config is what makes "immediately before"
  // actually mean "still valid for the rest of this test").
  test('sweeps every inventoried route for D17 in-context violations', async ({ page, baseURL }, testInfo) => {
    await reauthenticate(page.context(), baseURL ?? 'http://127.0.0.1:6060');
    const browserName = testInfo.project.use.browserName ?? testInfo.project.name;
    for (const route of resolvedRoutes.filter((r) => r.id !== 'login')) {
      await test.step(`route ${route.id} (${route.templatePath})`, async () => {
        await runRouteCheck(page, route, testInfo.project.name, String(browserName));
      });
    }
  });
});

test.describe('login route (unauthenticated)', () => {
  test.use({ storageState: { cookies: [], origins: [] } });

  test('checks the login route itself for D17 in-context violations', async ({ page }, testInfo) => {
    const route = resolvedRoutes.find((r) => r.id === 'login');
    if (!route) {
      throw new Error('touch-in-context: design-system/surfaces.json no longer declares a "login" route surface');
    }
    const browserName = testInfo.project.use.browserName ?? testInfo.project.name;
    await runRouteCheck(page, route, testInfo.project.name, String(browserName));
  });
});
