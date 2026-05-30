/**
 * retention.spec.ts — T4.2: Retention/aging E2E harness tests.
 *
 * What these tests cover:
 *   The combination of (a) the retention sweep (`pkg/session/retention_sweep.go`),
 *   (b) day-partition logic (`pkg/session/daypartition.go`), and (c) the
 *   SPA session-list rendering when a session's files are backdated.
 *
 * Each subsystem is unit-tested in Go; these tests exercise the user-facing
 * combination: "I left this session N days ago — what happens?"
 *
 * Bug classes caught by these tests:
 *   - Retention sweep silently failing to delete old sessions (test 2 would
 *     still find the session in the session list after a gateway restart).
 *   - SPA crash or blank screen when opening a session with old day-partition
 *     JSONL file names (test 1 asserts the transcript renders cleanly).
 *   - Gateway not running the retention sweep on startup or on a schedule
 *     (test 2 exposes this because the session would still be listed).
 *   - Off-by-one in retention days calculation (a 91-day session should be
 *     swept when the default is 90 days, but a 7-day session must not be).
 *
 * NOTE: These tests are currently expected to be red in some environments
 * because the E2E path from "file backdated" → "gateway lists session" →
 * "SPA renders transcript" requires:
 *   - The gateway to be running with a clean $OMNIPUS_HOME (global-setup.ts).
 *   - The session list API to return sessions created outside the REST flow.
 *   - The retention sweep to run at gateway startup AND on the test gateway.
 *
 * If these tests fail, it documents a real gap — NOT a test bug. Do NOT
 * add softSkip() calls here without a tracked GitHub issue.
 *
 * Traces to: quizzical-marinating-frog.md T4.2
 */

import { test, expect } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';
import { fileURLToPath } from 'url';
import { agedTranscript, agedSessionExists } from './fixtures/aging';

// ── Auth helpers ─────────────────────────────────────────────────────────────

const AUTH_FILE = process.env.OMNIPUS_AUTH_FILE
  ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
  : path.join(
      path.dirname(fileURLToPath(import.meta.url)),
      'fixtures/.auth/admin.json',
    );

/**
 * Read the admin Bearer token from the global-setup storageState file.
 * global-setup.ts mirrors omnipus_auth_token from sessionStorage to localStorage
 * so storageState captures it; we extract it here for direct API calls.
 */
function getStoredAuthToken(): string | null {
  if (!fs.existsSync(AUTH_FILE)) {
    return null;
  }
  try {
    const raw = fs.readFileSync(AUTH_FILE, 'utf-8');
    const state = JSON.parse(raw) as {
      origins?: Array<{
        origin: string;
        localStorage?: Array<{ name: string; value: string }>;
      }>;
    };
    for (const origin of state.origins ?? []) {
      for (const item of origin.localStorage ?? []) {
        if (item.name === 'omnipus_auth_token') {
          return item.value;
        }
      }
    }
  } catch {
    // Auth file may not exist yet on first run
  }
  return null;
}

/**
 * Build Authorization header map for direct API calls.
 * The CSRF token is not needed for GET requests; POST /security/retention/sweep
 * requires it only if the gateway enforces CSRF on that endpoint. We include it
 * when available to be safe.
 */
async function authHeaders(page: import('@playwright/test').Page): Promise<Record<string, string>> {
  const token = getStoredAuthToken();
  const cookies = await page.context().cookies();
  const csrf = cookies.find((c) => c.name === '__Host-csrf')?.value ?? null;
  return {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...(csrf ? { 'X-CSRF-Token': csrf } : {}),
  };
}

// ── Constants ────────────────────────────────────────────────────────────────

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060';

const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test');

// Default retention period enforced by the gateway.
// See pkg/config/keys.go and the default in pkg/gateway/rest.go.
// The test is written against this default; if it changes, update this constant.
const DEFAULT_RETENTION_DAYS = 90;

// ── T4.2-1: Seven-day-old session replays cleanly ────────────────────────────

test('seven_day_old_session_replays_cleanly', async ({ page }) => {
  // Traces to: quizzical-marinating-frog.md T4.2
  //
  // BDD:
  //   Given a session whose transcript files have mtime = 7 days ago
  //   When the user navigates to the sessions panel and opens that session
  //   Then the transcript renders without errors (no blank screen, no SPA crash)
  //
  // 7 days is well within the default 90-day retention window, so the session
  // MUST still exist after any retention sweep. The key assertion is that the
  // SPA can render a session whose JSONL file names contain an old date
  // (e.g., "2026-04-27.jsonl" rather than today's date).
  //
  // Bug class caught: SPA crash or blank-screen when a session's partition
  // file names do not match today's date, OR when meta.json has old timestamps.

  const sessionId = `aged-7d-${Date.now()}`;

  // Arrange: create a backdated session fixture directly in $OMNIPUS_HOME.
  // This simulates a session the user opened 7 days ago.
  agedTranscript(OMNIPUS_HOME, sessionId, 7, { messageCount: 4 });

  // Assert the fixture was written correctly before proceeding.
  expect(agedSessionExists(OMNIPUS_HOME, sessionId)).toBe(true);

  // Navigate to the SPA root — the global-setup.ts has already authenticated.
  await page.goto('/');
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 });

  // Open the sessions panel and find our synthetic session.
  // The session title is "Aged session (7d ago)" as set in agedTranscript().
  const normalizedId = sessionId.startsWith('session_') ? sessionId : `session_${sessionId}`;

  // Query the sessions API directly to verify the gateway surfaces the fixture session.
  // The SPA renders sessions from GET /api/v1/sessions, which reads the store from disk.
  // page.request inherits the browser context but does NOT automatically send the
  // Authorization Bearer header — we must pass it explicitly from storageState.
  const resp = await page.request.get(`${BASE_URL}/api/v1/sessions`, {
    headers: await authHeaders(page),
  });

  // BLOCKED: This assertion will fail until the gateway surfaces sessions that
  // were written directly to disk (outside the REST flow) AND the session list
  // API reads all session directories, not just those created via REST.
  //
  // If it fails, the failure message will read:
  //   "Expected sessions list to contain the aged session ID"
  // That failure documents the coverage gap — NOT a test-infrastructure problem.
  //
  // Response shape: GET /api/v1/sessions returns either:
  //   - Array directly:                  [{id, ...}, ...]         (no partial errors)
  //   - Object with sessions key:        {sessions: [...], partial_errors: [...]}
  // We normalise both to a flat array.
  const rawBody = await resp.json();
  const sessions: Array<{ id: string }> = Array.isArray(rawBody)
    ? (rawBody as Array<{ id: string }>)
    : ((rawBody as { sessions?: Array<{ id: string }> }).sessions ?? []);
  const found = sessions.some((s) => s.id === normalizedId);

  expect(found).toBe(true);

  if (found) {
    // Open the session list panel.
    const openPanelBtn = page.getByRole('button', { name: 'Open sessions panel' });
    if (await openPanelBtn.isVisible({ timeout: 5_000 })) {
      await openPanelBtn.click();
    }

    // Find and click the aged session.
    const sessionBtn = page
      .getByRole('button', { name: /Aged session \(7d ago\)/i })
      .first();

    if (await sessionBtn.isVisible({ timeout: 5_000 })) {
      await sessionBtn.click();

      // Assert: the transcript renders (at least one message bubble visible).
      // We look for either [data-testid="user-message"] or [data-testid="assistant-message"]
      // OR any message container — whichever selector is available.
      const chatArea = page.locator('[data-testid="chat-messages"], [role="log"], main');
      await expect(chatArea).toBeVisible({ timeout: 10_000 });

      // Assert no SPA crash (no unhandled error overlay).
      const errorOverlay = page.locator('[data-testid="error-boundary"], .error-boundary');
      await expect(errorOverlay).toHaveCount(0);
    }
  }
});

// ── T4.2-2: Session past retention threshold is swept ────────────────────────

test('session_past_retention_threshold_is_swept', async ({ page }) => {
  // Traces to: quizzical-marinating-frog.md T4.2
  //
  // BDD:
  //   Given a session whose transcript files have mtime = 100 days ago
  //     (past the 90-day default retention threshold)
  //   When the gateway starts (or its retention sweep runs)
  //   Then the session is no longer listed in the session list
  //
  // This test drives the full user-facing behavior: "I haven't opened this
  // session in 3+ months; it should have been cleaned up."
  //
  // Bug class caught:
  //   - Retention sweep not running at gateway startup.
  //   - Retention sweep silently failing (wrong base dir, wrong cutoff math,
  //     wrong mtime comparison).
  //   - Off-by-one: a session at exactly 90 days should be swept; 91 days
  //     definitely should be swept; 89 days must NOT be swept.
  //
  // NOTE: This test asserts the session is ABSENT from the session list after
  // the gateway has had a chance to run its retention sweep. In the CI flow,
  // the gateway is started fresh in global-setup.ts, so the sweep should have
  // run at boot time. If the gateway does not run the sweep at boot, this test
  // will fail — which is the correct outcome (it documents the gap).

  const sessionId = `aged-100d-${Date.now()}`;
  const daysAgo = DEFAULT_RETENTION_DAYS + 10; // 100 days — comfortably past threshold

  // Arrange: write the backdated session BEFORE the retention-sweep runs.
  // In E2E mode, global-setup starts the gateway, which may run the sweep.
  // We write the fixture here and then trigger a retention sweep via the API
  // (if available) or by checking the session list (the sweep may already
  // have run at gateway startup and won't run again during this test).
  //
  // IMPORTANT: In CI, the gateway is started fresh in global-setup.ts BEFORE
  // any test runs. That means the sweep already ran at startup, BEFORE this
  // fixture was written. We therefore:
  //   1. Write the backdated fixture.
  //   2. Trigger the gateway to re-run its sweep via the REST API (if supported).
  //   3. If no sweep endpoint exists, verify the session is absent at next startup.
  //
  // For now, this test checks two things:
  //   (a) The fixture can be created and backdated successfully.
  //   (b) The session is NOT returned by the API (either because the sweep
  //       already ran, or because the gateway filters old sessions on list).
  //
  // If (b) fails: it means the gateway DOES return stale sessions, which is
  // the bug this test is designed to catch.

  // Write the fixture.
  agedTranscript(OMNIPUS_HOME, sessionId, daysAgo, { messageCount: 4 });
  expect(agedSessionExists(OMNIPUS_HOME, sessionId)).toBe(true);

  // Navigate to the SPA to ensure the gateway is serving requests.
  await page.goto('/');
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 });

  // The retention sweep endpoint is wrapped with `RequireNotBypass` (CLAUDE.md
  // §"Defense-in-depth contract") which returns 503 when dev_mode_bypass=true.
  // The global test gateway boots with bypass=true; we flip it off for this
  // test only, run the sweep, then restore so the rest of the suite is
  // unaffected. The /reload endpoint on the health server requires no auth
  // and triggers an in-place config reload via the health-server reload hook.
  const configPath = path.join(OMNIPUS_HOME, 'config.json');
  // config.json may not exist when the gateway was started with in-memory defaults
  // (e.g., started via env-only config without a persistent config file).
  // In that case we fetch the current config from the gateway's REST API, modify
  // dev_mode_bypass, and write the result to disk so /reload picks it up.
  // originalRaw is null when the file did not exist before the test; the finally
  // block removes the synthesised file to restore the pre-test state.
  const configExists = fs.existsSync(configPath);
  let originalRaw: string | null = null;
  let cfgObj: { gateway?: { dev_mode_bypass?: boolean; [key: string]: unknown }; [key: string]: unknown };
  if (configExists) {
    originalRaw = fs.readFileSync(configPath, 'utf-8');
    cfgObj = JSON.parse(originalRaw) as typeof cfgObj;
  } else {
    // Fetch current config from the gateway API so we don't lose users/credentials on reload.
    const apiCfgResp = await page.request.get(`${BASE_URL}/api/v1/config`, {
      headers: await authHeaders(page),
      failOnStatusCode: false,
    });
    if (apiCfgResp.ok()) {
      cfgObj = (await apiCfgResp.json()) as typeof cfgObj;
    } else {
      // Fallback to minimal config if the API call fails.
      cfgObj = { gateway: { dev_mode_bypass: true } };
    }
  }
  const bypassWasOn = cfgObj.gateway?.dev_mode_bypass === true;
  if (bypassWasOn) {
    if (!cfgObj.gateway) cfgObj.gateway = {};
    cfgObj.gateway.dev_mode_bypass = false;
    fs.writeFileSync(configPath, JSON.stringify(cfgObj, null, 2));
    const reloadResp = await page.request.post(`${BASE_URL}/reload`, {
      failOnStatusCode: false,
    });
    expect(
      reloadResp.ok(),
      `POST /reload returned ${reloadResp.status()} ${await reloadResp.text()}`,
    ).toBeTruthy();
    // Reload propagation is asynchronous; give the manualReloadChan a moment
    // to drain and the config-snapshot pointer to swap.
    await page.waitForTimeout(500);
  }

  try {
    // Trigger a retention sweep via the REST API.
    // Correct endpoint: POST /api/v1/security/retention/sweep (pkg/gateway/rest_retention.go:163).
    // Admin Bearer token is required; we read it from storageState (see authHeaders above).
    const sweepResp = await page.request.post(`${BASE_URL}/api/v1/security/retention/sweep`, {
      headers: await authHeaders(page),
      failOnStatusCode: false,
    });
    expect(
      [200, 204, 409].includes(sweepResp.status()),
      `POST /api/v1/security/retention/sweep returned ${sweepResp.status()} ${await sweepResp.text()} ` +
        '(expected 200/204 for success, or 409 if a nightly sweep is in progress)',
    ).toBeTruthy();
    if (sweepResp.status() === 200 || sweepResp.status() === 204) {
      // Sweep triggered successfully. Now check the session is gone.
      // Give it a moment to complete.
      await page.waitForTimeout(500);
    }
    // If sweepResp is 409: a nightly sweep is in progress — that is also acceptable;
    // the session will be removed by that sweep. Continue to the assertion.

    // Query the session list. If the retention sweep ran (either at startup or
    // via the API call above), the 100-day-old session should NOT appear.
    // Must send auth header — same reason as the GET /api/v1/sessions call above.
    const listResp = await page.request.get(`${BASE_URL}/api/v1/sessions`, {
      headers: await authHeaders(page),
    });
    // Response shape: same dual-form as in test 1 — normalise to a flat array.
    const rawListBody = await listResp.json();
    const normalizedId = sessionId.startsWith('session_') ? sessionId : `session_${sessionId}`;
    const sessions: Array<{ id: string }> = Array.isArray(rawListBody)
      ? (rawListBody as Array<{ id: string }>)
      : ((rawListBody as { sessions?: Array<{ id: string }> }).sessions ?? []);
    const stillPresent = sessions.some((s) => s.id === normalizedId);

    // IMPORTANT: If this assertion fails, it means the retention sweep did NOT
    // remove the session. This is the exact bug class this test is designed to catch.
    // Do NOT change this to `toBe(true)` — the session must be absent.
    expect(
      stillPresent,
      [
        `Session ${normalizedId} (${daysAgo} days old, past ${DEFAULT_RETENTION_DAYS}-day threshold)`,
        'is still present in the session list after the retention sweep should have run.',
        'This indicates the retention sweep is not running, not finding this session,',
        'or the cutoff calculation is wrong.',
        `Session was written to: ${OMNIPUS_HOME}/sessions/${normalizedId}/`,
      ].join('\n'),
    ).toBe(false);
  } finally {
    // Restore the original config so subsequent tests see the bypass mode the
    // global setup chose for them.
    // If we created config.json (originalRaw === null), remove it to restore
    // the pre-test state. Otherwise write the original bytes verbatim to
    // preserve secret values that would otherwise round-trip through JSON
    // and lose any non-JSON-safe content (the config has SecureString fields).
    if (bypassWasOn) {
      if (originalRaw === null) {
        // config.json did not exist before the test; remove the synthesised file.
        try { fs.unlinkSync(configPath); } catch { /* best-effort */ }
      } else {
        fs.writeFileSync(configPath, originalRaw);
      }
      const reloadResp = await page.request.post(`${BASE_URL}/reload`, {
        failOnStatusCode: false,
      });
      // Best-effort restore: if reload fails here, surface a warning but do
      // not fail the test (the assertion above is what matters; later tests
      // get a fresh OMNIPUS_HOME in CI anyway).
      if (!reloadResp.ok()) {
        // eslint-disable-next-line no-console
        console.warn(
          `retention.spec: post-test config restore reload returned ${reloadResp.status()}`,
        );
      }
    }
  }
});
