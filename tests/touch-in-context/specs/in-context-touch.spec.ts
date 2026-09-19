import { test, request as pwRequest } from '@playwright/test';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { discoverDynamicIds, loadRouteSurfaces, resolveRoutes, type ResolvedRoute } from '../lib/routes';
import { measurePage, type KeyRowBudget } from '../lib/measure';
import { recordResult, type ResultEntry } from '../lib/report';
import { projectNameToEnvKind } from '../lib/env';
import { reauthenticate } from '../lib/reauth';
import { readCredentials } from '../lib/credentials';
import { RAW_RESULTS_DIR, SCREENSHOTS_DIR } from '../lib/paths';
import { settle } from '../lib/settle';

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
    const settleResult = await settle(page, route.id);
    const measurement = await measurePage(page, envKind, budgetsForRoute(route.id));
    // A settle timeout is reported as its own status, never silently folded
    // into 'ok' zero-violations — see report.ts's ResultEntry.status doc
    // comment and lib/settle.ts's SettleResult doc comment.
    const status = !settleResult.settled
      ? 'settle-timeout'
      : measurement.violations.length > 0
        ? 'violations'
        : 'ok';
    entry = {
      ...baseEntry,
      status,
      controlsChecked: measurement.controlsChecked,
      violations: measurement.violations,
      keyRows: measurement.keyRows,
      resolvedPath: hashUrl,
      settled: settleResult.settled,
      settleReason: settleResult.reason,
    };
    if (status === 'violations' || status === 'settle-timeout') {
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
