import { test, expect, request as pwRequest } from '@playwright/test';
import path from 'node:path';
import { discoverDynamicIds } from '../lib/routes';
import { measurePage } from '../lib/measure';
import { recordResult, type ResultEntry } from '../lib/report';
import { projectNameToEnvKind } from '../lib/env';
import { reauthenticate } from '../lib/reauth';
import { RAW_RESULTS_DIR, SCREENSHOTS_DIR } from '../lib/paths';
import { readCredentials } from '../lib/credentials';
// Reuses the established, ground-truth-cited selector helpers from the main
// e2e suite (tests/e2e/fixtures) instead of re-deriving them — chatInput and
// loginAs already carry the exact aria-label/id citations and the
// dev_mode_bypass-aware auth detection this suite would otherwise duplicate.
import { chatInput } from '../../e2e/fixtures/selectors';
import { loginAs } from '../../e2e/fixtures/login';

let workspaceId: string | null = null;

/**
 * Discovery logs in FOR ITSELF (rather than reusing a storageState file
 * written earlier by global-setup.ts) so it is never left holding a stale
 * cookie — see lib/reauth.ts's doc comment: the session cookie is
 * single-slot per user, so any login anywhere invalidates an earlier one.
 * This runs once, before every test in this file (workers:1 — see
 * playwright.touch-in-context.config.ts — guarantees nothing else logs in
 * while it's mid-flight).
 */
test.beforeAll(async () => {
  const baseURL = process.env.OMNIPUS_URL ?? 'http://127.0.0.1:6060';
  const { username, password } = readCredentials();
  const apiContext = await pwRequest.newContext({ baseURL });
  try {
    const loginResp = await apiContext.post('/api/v1/auth/login', { data: { username, password } });
    if (!loginResp.ok()) {
      throw new Error(`[touch-in-context] task-flows discovery login failed: ${loginResp.status()}`);
    }
    const ids = await discoverDynamicIds(apiContext);
    workspaceId = ids.workspaceId;
  } finally {
    await apiContext.dispose();
  }
});

function record(entry: ResultEntry): void {
  recordResult(RAW_RESULTS_DIR, entry);
}

test.describe('D17 task flows', () => {
  test('model picker: open, see at least 3 options, pick one', async ({ page, baseURL }, testInfo) => {
    test.skip(!workspaceId, 'no live workspace discovered — cannot reach a chat route to open the model picker');
    const envKind = projectNameToEnvKind(testInfo.project.name);
    const timestamp = new Date().toISOString();
    try {
      await reauthenticate(page.context(), baseURL ?? 'http://127.0.0.1:6060');
      await page.goto(`/#/workspaces/${workspaceId}/chat`, { waitUntil: 'domcontentloaded' });
      const trigger = page.locator('[data-testid="composer-model-selector"]');
      await expect(trigger).toBeVisible({ timeout: 15_000 });
      // Real, observed gap (not a harness workaround): ModelPicker
      // (src/components/chat/composer/ModelPicker.tsx) never forwards its
      // providers `useQuery`'s loading state into ModelSelector's
      // `catalogStatus` prop. While that query is in flight, ModelSelector's
      // `models` prop is genuinely empty, and with no `catalogStatus:
      // 'loading'` override it renders the CATALOG-EMPTY placeholder — a
      // plain, non-interactive <div> reading "Connect a provider to pick a
      // model" — rather than the interactive "Loading models…" combobox the
      // component supports for exactly this window (see that file's own
      // "Loading models…"/click-swallowing comment). A click during this
      // window lands on a div with no open handler and does nothing. Wait
      // for the placeholder to clear before clicking — this IS the
      // real-world race a real person hits opening the picker quickly after
      // page load; recorded in tests/touch-in-context/README.md's findings,
      // not silently absorbed here.
      await expect(trigger).not.toContainText('Connect a provider', { timeout: 10_000 });
      await trigger.click();

      const options = page.getByRole('option');
      await expect(options.first()).toBeVisible({ timeout: 10_000 });
      const optionCount = await options.count();
      expect(optionCount, 'model picker must offer at least 3 options').toBeGreaterThanOrEqual(3);

      const measurement = await measurePage(page, envKind, []);

      await options.first().click();
      await expect(page.getByRole('option')).toHaveCount(0, { timeout: 5_000 }).catch(() => undefined);

      const status = measurement.violations.length > 0 ? 'violations' : 'ok';
      const entry: ResultEntry = {
        kind: 'task-flow',
        id: 'model-picker',
        project: testInfo.project.name,
        envKind,
        browserName: String(testInfo.project.use.browserName ?? testInfo.project.name),
        status,
        controlsChecked: measurement.controlsChecked,
        violations: measurement.violations,
        timestamp,
      };
      if (status === 'violations') {
        const shotPath = path.join(SCREENSHOTS_DIR, `task-flow-model-picker-${testInfo.project.name}.png`);
        await page.screenshot({ path: shotPath }).catch(() => undefined);
        entry.screenshotPath = shotPath;
      }
      record(entry);
    } catch (err) {
      record({
        kind: 'task-flow',
        id: 'model-picker',
        project: testInfo.project.name,
        envKind,
        browserName: String(testInfo.project.use.browserName ?? testInfo.project.name),
        status: 'error',
        violations: [],
        errorMessage: err instanceof Error ? err.message : String(err),
        timestamp,
      });
      throw err;
    }
  });

  test('compose: type a chat message with the composer focused', async ({ page, baseURL }, testInfo) => {
    test.skip(!workspaceId, 'no live workspace discovered — cannot reach a chat route to focus the composer');
    const envKind = projectNameToEnvKind(testInfo.project.name);
    const timestamp = new Date().toISOString();
    try {
      await reauthenticate(page.context(), baseURL ?? 'http://127.0.0.1:6060');
      await page.goto(`/#/workspaces/${workspaceId}/chat`, { waitUntil: 'domcontentloaded' });
      const input = chatInput(page);
      await expect(input).toBeVisible({ timeout: 15_000 });
      await input.click();
      const message = `D17 in-context touch check ${new Date().toISOString()}`;
      await input.pressSequentially(message);
      await expect(input).toHaveValue(message);

      const measurement = await measurePage(page, envKind, []);

      const status = measurement.violations.length > 0 ? 'violations' : 'ok';
      const entry: ResultEntry = {
        kind: 'task-flow',
        id: 'compose-message',
        project: testInfo.project.name,
        envKind,
        browserName: String(testInfo.project.use.browserName ?? testInfo.project.name),
        status,
        controlsChecked: measurement.controlsChecked,
        violations: measurement.violations,
        timestamp,
      };
      if (status === 'violations') {
        const shotPath = path.join(SCREENSHOTS_DIR, `task-flow-compose-${testInfo.project.name}.png`);
        await page.screenshot({ path: shotPath }).catch(() => undefined);
        entry.screenshotPath = shotPath;
      }
      record(entry);
    } catch (err) {
      record({
        kind: 'task-flow',
        id: 'compose-message',
        project: testInfo.project.name,
        envKind,
        browserName: String(testInfo.project.use.browserName ?? testInfo.project.name),
        status: 'error',
        violations: [],
        errorMessage: err instanceof Error ? err.message : String(err),
        timestamp,
      });
      throw err;
    }
  });
});

test.describe('D17 task flow: sign in', () => {
  // A real sign-in has to start unauthenticated — the project's default
  // storageState (the API-minted session from global-setup.ts) is
  // deliberately overridden here, same pattern as tests/e2e/auth.spec.ts.
  test.use({ storageState: { cookies: [], origins: [] } });

  test('sign in via the real login form', async ({ page }, testInfo) => {
    const envKind = projectNameToEnvKind(testInfo.project.name);
    const timestamp = new Date().toISOString();
    const { username, password } = readCredentials();
    try {
      await loginAs(page, username, password);
      await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 });
      const cookies = await page.context().cookies();
      const sessionCookie = cookies.find((c) => c.name === 'omnipus-session');
      expect(sessionCookie, 'a real omnipus-session cookie must be set after sign-in').toBeTruthy();

      const measurement = await measurePage(page, envKind, []);
      const status = measurement.violations.length > 0 ? 'violations' : 'ok';
      const entry: ResultEntry = {
        kind: 'task-flow',
        id: 'sign-in',
        project: testInfo.project.name,
        envKind,
        browserName: String(testInfo.project.use.browserName ?? testInfo.project.name),
        status,
        controlsChecked: measurement.controlsChecked,
        violations: measurement.violations,
        timestamp,
      };
      if (status === 'violations') {
        const shotPath = path.join(SCREENSHOTS_DIR, `task-flow-sign-in-${testInfo.project.name}.png`);
        await page.screenshot({ path: shotPath }).catch(() => undefined);
        entry.screenshotPath = shotPath;
      }
      record(entry);
    } catch (err) {
      record({
        kind: 'task-flow',
        id: 'sign-in',
        project: testInfo.project.name,
        envKind,
        browserName: String(testInfo.project.use.browserName ?? testInfo.project.name),
        status: 'error',
        violations: [],
        errorMessage: err instanceof Error ? err.message : String(err),
        timestamp,
      });
      throw err;
    }
  });
});
