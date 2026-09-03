/**
 * uat-shell-and-screens.spec.ts — UAT group "shell and screens".
 *
 * Two things from `docs/internal/specs/browser-rework-uat-plan.md` that a human
 * tester checked by hand and that must not silently regress:
 *
 *   1. UAT-47 (P1, D1 US-21/AC1–AC3) — adding an agent to a workspace team must
 *      disclose, AT THE POINT OF THE DECISION, that the agent gains the
 *      workspace's live logins. The plan names three silent failures for this
 *      case and each one is asserted against separately below:
 *        - the disclosure appears only AFTER confirming;
 *        - it is a tooltip you have to hover for;
 *        - it describes the MECHANISM ("agents share a browser profile")
 *          instead of the CONSEQUENCE, which is what a non-engineer reads.
 *      A release note is explicitly NOT sufficient, so nothing here reads the
 *      changelog — the oracle is what is on screen before the click lands.
 *
 *   2. "Zero console errors" is a pass criterion for every screen the plan
 *      touches, so the screen walk below asserts it directly.
 *
 *      It deliberately does NOT use `fixtures/console-errors`. That fixture is
 *      a plain (non-`auto`) Playwright fixture, so its listener is attached and
 *      its teardown assertion runs ONLY for a test that names `consoleErrors`
 *      in its arguments. 34 spec files import its `test` export and 2 name the
 *      fixture — for every other one the console gate is dead code that reports
 *      green having watched nothing. Verified on this gateway: the Settings
 *      screen emits 5 `console.error` entries that a test written against that
 *      fixture, but not destructuring it, passes straight through. Rather than
 *      quietly inherit that, this spec collects the messages itself.
 *
 * Oracle note: the disclosure assertions are written from the plan's wording
 * ("act as whoever this workspace is signed in as … including on turns nobody
 * is watching"), NOT from the string in AddAgentPicker.tsx. They are four
 * independent semantic requirements, so a rewrite that keeps the meaning still
 * passes and a rewrite that drops the unattended-turns half still fails.
 */

import { expect, test } from '@playwright/test';

import { restoreAdminSession } from './fixtures/admin-api';

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060';


/**
 * The route this spec asks "is my session usable?".
 *
 * NOT `/api/v1/auth/validate`, which is the obvious choice and cannot see the
 * session at all on an e2e gateway. Those run with `gateway.dev_mode_bypass:
 * true`, and `checkBearerAuth` (pkg/gateway/auth.go) short-circuits to the
 * synthetic `_dev_bypass` identity BEFORE its cookie branch for any request
 * with no `Authorization: Bearer` header — which is every SPA request since
 * ADR-044. Measured against a throwaway gateway seeded from a real e2e home
 * (2026-09-03, bypass on, the CI config):
 *
 *     no cookie at all        → 200  {"username":"_dev_bypass"}
 *     garbage/rotated cookie  → 200
 *     30th call within 1 min  → 429, Retry-After: 60
 *
 * The cookie is never read, so a non-200 there could NEVER have meant "the
 * session is dead" — the only reachable non-200 is `validateLimiter`'s 429
 * (30/min per IP, pkg/gateway/rest_auth.go). That is what actually failed this
 * spec in CI run 33696333101: nine tests in this file passed, two failed
 * claiming a rotated token, and the third attempt passed 2s later with no
 * login in between — which a rotated single-slot hash cannot do, and a
 * draining sliding window does. Reproduced deterministically by exhausting the
 * bucket with 32 curls while the storageState cookie was proven alive.
 *
 * `/api/v1/providers` is `withOptionalAuth` + `requireAuthOutsideOnboarding`,
 * which deliberately does not honour bypass, and it carries no rate limiter
 * (40 consecutive calls, no 429). Measured on the same gateway with bypass
 * ON: no cookie → 401, garbage cookie → 401, real session cookie → 200. It is
 * therefore a real oracle for the session in both bypass modes. It is also the
 * exact route the 2026-08-28 create-agent incident surfaced on, for the same
 * reason.
 */
const SESSION_PROBE_PATH = '/api/v1/providers';

/**
 * Status of `SESSION_PROBE_PATH` for the session this page context carries.
 *
 * Deliberately an IN-PAGE `fetch`, not `page.request.get`. The out-of-band
 * `page.request` version is tidier on paper — its responses never reach the
 * `page.on('response')` listener the console-error tests install — but it made
 * three tests fail: it answers without ever touching the page, so
 * `ensureSession` returned before the SPA on `/#/` had booted, and the caller's
 * next `page.goto` (same document, hash-only change) then raced the router's
 * mount and lost. The app landed on its default workspace Chat route and every
 * screen assertion timed out looking for a screen it never navigated to.
 * An in-page `fetch` has to wait for the page's JS execution context, which is
 * the readiness barrier this function has always implicitly provided.
 */
async function probeSession(page: import('@playwright/test').Page): Promise<number> {
  return page.evaluate(async (path) => {
    const res = await fetch(path, { credentials: 'include' });
    return res.status;
  }, SESSION_PROBE_PATH);
}

/**
 * Guard the session every assertion in this file depends on — and when it is
 * not usable, report WHAT WAS OBSERVED, never a mechanism this spec did not
 * measure. The previous version of this function named one specific cause (a
 * concurrent login rotating the single-slot `session_token_hash`) that it had
 * no way to detect and that was not, in fact, what was happening; a confident
 * wrong diagnosis sends the next reader down the wrong path.
 */
async function ensureSession(page: import('@playwright/test').Page): Promise<void> {
  await page.goto(`${BASE_URL}/#/`);
  const before = await probeSession(page);
  if (before === 200) return;

  // Re-arm from the SHARED storageState rather than logging in. A login here
  // would re-mint the single slot and kill the next spec's cookie in turn —
  // the crosstalk scripts/check-e2e-login-crosstalk.sh exists to stop, and the
  // cause of the 2026-08-28 create-agent incident. Copying the file forward
  // recovers the recoverable case (this context is stale, the file on disk was
  // rewritten by auth.spec.ts's afterAll) and mints nothing.
  await restoreAdminSession(page);
  await page.reload();
  const after = await probeSession(page);
  if (after === 200) return;

  // Everything below is an observation or a named possibility — no cause is
  // asserted, because this spec cannot distinguish between them.
  const reading =
    `GET ${SESSION_PROBE_PATH} answered ${before}, and ${after} after re-applying the ` +
    'cookies from the shared storageState file (which mints nothing).';
  const meaning =
    after === 401
      ? 'A 401 means the gateway did not accept this cookie. This spec cannot tell you why. ' +
        'Candidates, in the order worth checking: the cookie expired (24 h max-age, ' +
        'middleware.SessionCookieMaxAge); another login as the same admin re-minted the ' +
        'single-slot session_token_hash (HandleLogin, pkg/gateway/rest_auth.go) — real, but ' +
        'only if something actually logged in, so check the gateway log before assuming it; ' +
        'or the gateway is running against a different OMNIPUS_HOME than global-setup ' +
        'onboarded.'
      : after === 429
        ? 'A 429 is the per-IP rate limiter refusing the probe, NOT a verdict on the session. ' +
          'Re-run; if it persists, the suite is outrunning that route\'s budget.'
        : 'That status is neither 200 nor an authentication answer — treat it as the gateway ' +
          'being unhealthy and check its log before touching this spec.';

  throw new Error(
    `BLOCKED: this spec's admin session is not usable. ${reading} ${meaning} ` +
      'Whatever the cause: do NOT log in from a spec — that re-mints the single slot and ' +
      'breaks the next spec in turn (scripts/check-e2e-login-crosstalk.sh enforces this).',
  );
}

/** Resolve a workspace id to drive against — the default one always exists. */
async function defaultWorkspaceId(page: import('@playwright/test').Page): Promise<string> {
  const rows = await page.evaluate(async () => {
    const res = await fetch('/api/v1/workspaces', { credentials: 'include' });
    if (!res.ok) throw new Error(`GET /api/v1/workspaces → ${res.status}`);
    return (await res.json()) as Array<{ id: string; is_default?: boolean }>;
  });
  const row = rows.find((w) => w.is_default) ?? rows[0];
  if (!row) throw new Error('no workspace exists to drive the Team tab against');
  return row.id;
}

/** Open Alpha/default → Team and click "Add agent"; returns the popover locator. */
async function openAddAgentPicker(page: import('@playwright/test').Page) {
  const wsId = await defaultWorkspaceId(page);
  await page.goto(`${BASE_URL}/#/workspaces/${wsId}/team`);

  const addAgent = page.getByTestId('team-add-agent');
  await expect(addAgent).toBeVisible({ timeout: 20_000 });

  // The nav drawer is an overlay and can sit over the Team canvas after a
  // fresh load; Escape closes it without navigating away.
  await page.keyboard.press('Escape');
  await addAgent.click();

  const popover = page.locator('[data-radix-popper-content-wrapper]').last();
  await expect(popover).toBeVisible({ timeout: 10_000 });
  return popover;
}

// ── UAT-47 ────────────────────────────────────────────────────────────────────

test('UAT-47 (a) the add-agent control discloses the live-login grant before anything is confirmed', async ({
  page,
}) => {
  await ensureSession(page);
  const wsId = await defaultWorkspaceId(page);

  // Silent failure #1, first half: the Team tab must not be relying on a
  // disclosure that is only reachable after the roster has already changed.
  // Before the picker is even opened there is nothing to read — which is fine,
  // and is exactly why the text has to be IN the picker.
  await page.goto(`${BASE_URL}/#/workspaces/${wsId}/team`);
  await expect(page.getByTestId('team-add-agent')).toBeVisible({ timeout: 20_000 });

  const popover = await openAddAgentPicker(page);
  const disclosure = popover.getByText(/this workspace/i).first();
  await expect(
    disclosure,
    'no disclosure at all in the add-agent picker (UAT-47 silent failure #3)',
  ).toBeVisible({ timeout: 10_000 });

  const text = ((await disclosure.textContent()) ?? '').toLowerCase();

  // Four independent semantic requirements from the plan's expected result.
  expect(text, 'disclosure does not mention the workspace browser').toMatch(/browser/);
  expect(text, 'disclosure does not say the browser stays signed in').toMatch(
    /signed in|logged in|logins/,
  );
  expect(
    text,
    'disclosure describes the mechanism but not the consequence — it must say the agent can ACT AS ' +
      'whoever the workspace is signed in as (UAT-47 silent failure #2)',
  ).toMatch(/act as|acts as|acting as/);
  expect(
    text,
    'disclosure omits the unattended half — scheduled/background turns are the part an operator ' +
      'cannot otherwise see (UAT-47 silent failure #2)',
  ).toMatch(/nobody is watching|unattended|background|scheduled/);
});

test('UAT-47 (b) the disclosure is plain text in the picker, not a tooltip and not gated on a hover', async ({
  page,
}) => {
  await ensureSession(page);
  const popover = await openAddAgentPicker(page);

  const disclosure = popover.getByText(/act as whoever|acts as whoever/i).first();
  await expect(disclosure).toBeVisible({ timeout: 10_000 });

  // Silent failure #1, second half + the tooltip failure. A tooltip is rendered
  // on demand and carries tooltip semantics; a disclosure the operator is meant
  // to read before deciding must be laid out, non-empty and on screen with no
  // pointer anywhere near it. Playwright has not hovered anything at this point.
  const role = await disclosure.evaluate((el) => el.closest('[role]')?.getAttribute('role') ?? '');
  expect(role, 'the disclosure is inside a tooltip — it must be plain text in the picker').not.toBe(
    'tooltip',
  );

  const box = await disclosure.boundingBox();
  expect(box, 'the disclosure has no layout box').not.toBeNull();
  expect(box!.width, 'the disclosure is collapsed to zero width').toBeGreaterThan(0);
  expect(box!.height, 'the disclosure is collapsed to zero height').toBeGreaterThan(0);

  // …and it is above the agent list, i.e. read before a name is picked, not
  // tucked underneath it where the choice has already been made.
  const firstOption = popover.getByRole('option').or(popover.locator('[data-agent-id]')).first();
  if (await firstOption.count()) {
    const optionBox = await firstOption.boundingBox();
    if (optionBox) {
      expect(
        box!.y,
        'the disclosure sits below the agent list — the operator picks first and reads after',
      ).toBeLessThanOrEqual(optionBox.y);
    }
  }
});

// ── Screen walk: every screen the plan touches, zero console errors ───────────

const SCREENS: ReadonlyArray<{ name: string; path: string; expect: RegExp }> = [
  { name: 'Agents', path: '/#/agents', expect: /Browse, configure, and create your AI agents/i },
  { name: 'Skills & Tools', path: '/#/skills', expect: /Manage agent capabilities/i },
  { name: 'Connectors', path: '/#/connectors', expect: /Connect Telegram, Discord, Slack/i },
  { name: 'Library', path: '/#/library', expect: /items/i },
  { name: 'Profile', path: '/#/profile', expect: /Your personal preferences/i },
  { name: 'Settings', path: '/#/settings', expect: /Configure providers, integrations/i },
];

/**
 * Routes `RequireNotBypass` answers 503 on while `gateway.dev_mode_bypass` is
 * on — which it is on every throwaway test home and on no production install.
 * They surface as "Failed to load resource" console errors whose text names no
 * URL, so they are correlated by response rather than matched by text: a 503,
 * 401 or 500 from anywhere else still fails the screen.
 */
const BYPASS_GUARDED = [
  '/api/v1/performance',
  '/api/v1/gateway/god-mode',
  '/api/v1/security/skill-trust',
  '/api/v1/providers/default-model',
  '/api/v1/config/pending-restart',
];

for (const screen of SCREENS) {
  test(`shell: ${screen.name} renders and logs no console errors`, async ({ page }) => {
    const consoleErrors: string[] = [];
    const failedURLs: string[] = [];
    page.on('console', (msg) => {
      if (msg.type() !== 'error') return;
      if (/WebSocket|reconnect/i.test(msg.text())) return; // allowed by the plan
      consoleErrors.push(msg.text());
    });
    page.on('response', (res) => {
      if (res.status() >= 400) failedURLs.push(`${res.status()} ${new URL(res.url()).pathname}`);
    });

    await ensureSession(page);
    await page.goto(`${BASE_URL}${screen.path}`);
    await expect(page.getByText(screen.expect).first()).toBeVisible({ timeout: 20_000 });
    // Settle: these screens fire their data fetches after first paint, so an
    // assertion that returns the moment the heading appears tears the page down
    // before a failing request can reach the console.
    await page.waitForLoadState('networkidle').catch(() => undefined);
    await page.waitForTimeout(3_000);

    // Anything the browser logged that is not a failed network resource is an
    // outright defect — an exception, a React error, a dropped zod payload.
    const scriptErrors = consoleErrors.filter((t) => !/Failed to load resource/i.test(t));
    expect(scriptErrors, `${screen.name}: console errors`).toEqual([]);

    // Every failed resource must be one of the bypass-guarded admin routes.
    const unexpected = failedURLs.filter(
      (entry) => !BYPASS_GUARDED.some((route) => entry.endsWith(route)),
    );
    expect(
      unexpected,
      `${screen.name}: failing requests that the dev_mode_bypass guard does not explain. ` +
        `Read the status: a 401 means the gateway refused this run's session cookie on that ` +
        `route (ensureSession's BLOCKED message lists the candidate causes — do not assume ` +
        `one); a 429 is a per-IP rate limiter, not an auth verdict; a 5xx is the screen's own ` +
        `defect.`,
    ).toEqual([]);
  });
}

test('shell: the workspace tabs the plan drives all render', async ({ page }) => {
  await ensureSession(page);
  const wsId = await defaultWorkspaceId(page);

  for (const [tab, marker] of [
    ['chat', /Message|Welcome to omnipus/i],
    ['board', /Team Task Backlog|No tasks yet/i],
    ['calendar', /No scheduled items|Today/i],
    ['team', /Team & delegation/i],
    ['settings', /Workspace settings/i],
  ] as const) {
    await page.goto(`${BASE_URL}/#/workspaces/${wsId}/${tab}`);
    await expect(page.getByText(marker).first(), `workspace ${tab} tab did not render`).toBeVisible({
      timeout: 20_000,
    });
  }
});

test('shell: the retired Command Center surfaces stay retired (plan N-14)', async ({ page }) => {
  await ensureSession(page);
  // /tasks and /automations must be redirects into the workspace board and
  // calendar, never screens of their own.
  await page.goto(`${BASE_URL}/#/tasks`);
  await expect(page).toHaveURL(/\/workspaces\/[^/]+\/board/, { timeout: 20_000 });

  await page.goto(`${BASE_URL}/#/automations`);
  await expect(page).toHaveURL(/\/workspaces\/[^/]+\/calendar/, { timeout: 20_000 });
});
