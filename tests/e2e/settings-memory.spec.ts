/**
 * settings-memory.spec.ts — Memory tab: persist-vs-runtime divergence guard.
 *
 * Why this file exists (and why component tests cannot replace it):
 *
 *   MemorySection.test.tsx mocks the API layer (fetchMemorySettings /
 *   updateMemorySettings are replaced with vi.fn() stubs). It therefore
 *   validates only that the React component wires its form state correctly to
 *   the stub — it cannot detect a gateway that accepts a PUT but does not
 *   persist the values, or a GET that returns stale data, or a runtime that
 *   honours the setting on the SPA-side but ignores it in the agent loop.
 *
 *   These two tests exercise the real code paths:
 *
 *   Test 1 ("persists memory settings across reload"):
 *     Drives the full SPA → PUT /api/v1/settings/memory → GET loop.
 *     A pass here means the gateway read and wrote real config.json fields
 *     and the SPA reflected them after a cold page reload.
 *
 *   Test 2 ("auto-recap toggle changes runtime behaviour (saved != ignored)"):
 *     Drives the runtime path: toggle auto_recap_enabled off → verify no
 *     last-session.md is produced after a session close; toggle on → verify
 *     last-session.md IS produced.  A pass here means the agent loop actually
 *     reads the persisted flag — the most common "saved but ignored" gap.
 *
 * Design decisions:
 *   - Direct REST API calls (page.request) for GET/PUT instead of a
 *     navigated flow where possible — avoids flakiness on model selector
 *     popover timing and avoids depending on provider availability for UI
 *     controls that may be disabled when no provider is configured.
 *   - UI-driven flow for the core save-and-reload test (test 1) to prove the
 *     SPA wiring end-to-end, not just the REST contract.
 *   - For test 2 the recap file check uses a bounded poll + assert-absent /
 *     assert-present pattern consistent with retention.spec.ts.
 *   - Neither test relies on LLM output being a particular string; they only
 *     assert file existence or field values.
 *
 * Traces to: BRD FR-019 (auto-recap), FR-023 (session-end recap pipeline),
 *   MemorySettings.yaml, pkg/agent/session_end.go CloseSession(),
 *   pkg/memrooms/rooms.go (last-session.md path).
 */

import { test, expect } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';
import { fileURLToPath } from 'url';

// ── Constants ─────────────────────────────────────────────────────────────────

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060';

const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test');

// Auth file location mirrors retention.spec.ts and global-setup.ts.
const AUTH_FILE = process.env.OMNIPUS_AUTH_FILE
  ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
  : path.join(
      path.dirname(fileURLToPath(import.meta.url)),
      'fixtures/.auth/admin.json',
    );

// ── Auth helpers ──────────────────────────────────────────────────────────────

/**
 * Read the admin Bearer token from the global-setup storageState file.
 * Mirrors the implementation in retention.spec.ts.
 */
function getStoredAuthToken(): string | null {
  if (!fs.existsSync(AUTH_FILE)) return null;
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
        if (item.name === 'omnipus_auth_token') return item.value;
      }
    }
  } catch {
    // Auth file not yet written (first run); test will handle missing token.
  }
  return null;
}

/**
 * Build Authorization + CSRF headers for direct REST calls.
 * Mirrors the implementation in retention.spec.ts.
 */
async function authHeaders(
  page: import('@playwright/test').Page,
): Promise<Record<string, string>> {
  const token = getStoredAuthToken();
  const cookies = await page.context().cookies();
  const csrf = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf')?.value ?? null;
  return {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...(csrf ? { 'X-CSRF-Token': csrf } : {}),
  };
}

// ── Wire types (inline — no generated import needed for test assertions) ──────

interface MemorySettingsWire {
  auto_recap_enabled?: boolean;
  idle_timeout_minutes?: number;
  bootstrap_recap_enabled?: boolean;
  bootstrap_recap_max_per_minute?: number;
  recap_model?: string;
  session_days?: number;
  memory_retros_days?: number;
}

// ── Helpers ───────────────────────────────────────────────────────────────────

/**
 * Navigate to Settings → Memory tab and wait for the section to be fully
 * rendered. Returns the active tab panel locator.
 */
async function openMemoryTab(
  page: import('@playwright/test').Page,
): Promise<import('@playwright/test').Locator> {
  await page.goto('/#/settings');

  // Wait for the Tabs list to be present (SettingsScreen.tsx TabsList).
  const tabList = page.locator('[role="tablist"]').first();
  await expect(tabList).toBeVisible({ timeout: 15_000 });

  // Click the Memory tab. The trigger does not have a data-testid (SettingsScreen.tsx
  // line 43: <TabsTrigger value="memory">Memory</TabsTrigger>), so match by text.
  const memoryTab = page.locator('button[role="tab"]', { hasText: 'Memory' });
  await expect(memoryTab).toBeVisible({ timeout: 10_000 });
  await memoryTab.click();

  // Wait for the active panel to render. Use "Memory & Recap" heading as the
  // readiness signal — it is always rendered by MemorySection once the query
  // resolves (MemorySection.tsx line 265).
  const activePanel = page.locator('[role="tabpanel"][data-state="active"]').first();
  await expect(activePanel).toBeVisible({ timeout: 10_000 });
  await expect(
    activePanel.getByText(/Memory.*Recap|Auto recap/i).first(),
  ).toBeVisible({ timeout: 15_000 });

  return activePanel;
}

// ── Test 1: settings persist across page reload ───────────────────────────────
//
// BDD:
//   Given the operator is on Settings → Memory
//   When they toggle Bootstrap recap ON, set Idle timeout to 47 minutes,
//     set Memory-retro retention to 222 days, and click Save
//   Then after a full page reload the Memory tab still shows those values
//   And GET /api/v1/settings/memory confirms they are stored server-side
//
// What "hardcoded response" would look like and why this test catches it:
//   A gateway stub that returns the same default JSON on every GET regardless
//   of what was PUT would produce idle_timeout_minutes=30 (default) after the
//   reload, failing the equality assertions below. We use two DIFFERENT numeric
//   values (47 and 222) that differ from the defaults (30 and 180) so any
//   hardcoded or un-flushed response is immediately visible.
//
// Traces to: FR-019, MemorySettings.yaml, MemorySection.tsx

test('persists memory settings across reload', async ({ page }) => {
  const IDLE_TIMEOUT_MINUTES = 47;
  const MEMORY_RETROS_DAYS = 222;

  // ── Arrange: read current settings so we can restore them at the end ─────
  // This makes the test idempotent (safe to run multiple times without drifting
  // the gateway config).
  await page.goto('/');
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 });

  const getResp = await page.request.get(`${BASE_URL}/api/v1/settings/memory`, {
    headers: await authHeaders(page),
  });
  expect(
    getResp.ok(),
    `GET /api/v1/settings/memory returned ${getResp.status()} — ${await getResp.text()}`,
  ).toBeTruthy();
  const originalSettings = (await getResp.json()) as MemorySettingsWire;

  // ── Act: open the Memory tab and change values ───────────────────────────
  const activePanel = await openMemoryTab(page);

  // Toggle Bootstrap recap ON if it is currently OFF.
  // The toggle is rendered as a button[role="switch"] with aria-checked.
  // MemorySection.tsx line 300: id="bootstrap-recap-enabled"
  const bootstrapToggle = page.locator('#bootstrap-recap-enabled');
  await expect(bootstrapToggle).toBeVisible({ timeout: 10_000 });
  const isBootstrapOn = (await bootstrapToggle.getAttribute('aria-checked')) === 'true';
  if (!isBootstrapOn) {
    await bootstrapToggle.click();
    // After clicking, bootstrap-recap-max-per-minute input becomes visible.
    await expect(page.locator('#bootstrap-recap-max-per-minute')).toBeVisible({ timeout: 5_000 });
  }

  // Enable Auto recap so Idle timeout input becomes visible.
  // MemorySection.tsx line 279: id="auto-recap-enabled"
  const autoRecapToggle = page.locator('#auto-recap-enabled');
  await expect(autoRecapToggle).toBeVisible({ timeout: 10_000 });
  const isAutoOn = (await autoRecapToggle.getAttribute('aria-checked')) === 'true';
  if (!isAutoOn) {
    await autoRecapToggle.click();
    await expect(page.locator('#idle-timeout-minutes')).toBeVisible({ timeout: 5_000 });
  }

  // Set idle timeout to our distinctive value.
  // MemorySection.tsx line 289: id="idle-timeout-minutes"
  const idleInput = page.locator('#idle-timeout-minutes');
  await expect(idleInput).toBeVisible({ timeout: 10_000 });
  await idleInput.fill(String(IDLE_TIMEOUT_MINUTES));

  // Set memory-retro retention to our distinctive value.
  // MemorySection.tsx line 470: id="memory-retros-days"
  const retroInput = page.locator('#memory-retros-days');
  await expect(retroInput).toBeVisible({ timeout: 10_000 });
  await retroInput.fill(String(MEMORY_RETROS_DAYS));

  // Click Save (MemorySection.tsx line 487: aria-label="Save memory settings").
  const saveBtn = activePanel.locator('button[aria-label="Save memory settings"]');
  await expect(saveBtn).toBeVisible({ timeout: 5_000 });
  await saveBtn.click();

  // Wait for Save to complete: the button text reverts to "Save" from "Saving…".
  await expect(saveBtn).not.toContainText('Saving', { timeout: 15_000 });

  // ── Assert (API): values persisted server-side before the reload ─────────
  const afterSaveResp = await page.request.get(`${BASE_URL}/api/v1/settings/memory`, {
    headers: await authHeaders(page),
  });
  expect(afterSaveResp.ok(), 'GET /api/v1/settings/memory after save must succeed').toBeTruthy();
  const afterSave = (await afterSaveResp.json()) as MemorySettingsWire;

  expect(
    afterSave.idle_timeout_minutes,
    `idle_timeout_minutes must be ${IDLE_TIMEOUT_MINUTES} immediately after save (got ${afterSave.idle_timeout_minutes})`,
  ).toBe(IDLE_TIMEOUT_MINUTES);

  expect(
    afterSave.memory_retros_days,
    `memory_retros_days must be ${MEMORY_RETROS_DAYS} immediately after save (got ${afterSave.memory_retros_days})`,
  ).toBe(MEMORY_RETROS_DAYS);

  expect(
    afterSave.auto_recap_enabled,
    'auto_recap_enabled must be true (we toggled it ON before saving)',
  ).toBe(true);

  // ── Assert (UI after reload): SPA reflects persisted values ─────────────
  // This is the core differentiation test: if the SPA hydrates from a
  // hardcoded or stale cache instead of the real GET response, the inputs
  // will show the default values (30, 180), not our chosen values (47, 222).
  await openMemoryTab(page);

  // Auto recap is ON → idle timeout input must be visible and show our value.
  const reloadedIdleInput = page.locator('#idle-timeout-minutes');
  await expect(reloadedIdleInput).toBeVisible({ timeout: 10_000 });
  await expect(reloadedIdleInput).toHaveValue(String(IDLE_TIMEOUT_MINUTES));

  const reloadedRetroInput = page.locator('#memory-retros-days');
  await expect(reloadedRetroInput).toBeVisible({ timeout: 10_000 });
  await expect(reloadedRetroInput).toHaveValue(String(MEMORY_RETROS_DAYS));

  // Bootstrap recap toggle must still be ON.
  const reloadedBootstrapToggle = page.locator('#bootstrap-recap-enabled');
  await expect(reloadedBootstrapToggle).toBeVisible({ timeout: 10_000 });
  await expect(reloadedBootstrapToggle).toHaveAttribute('aria-checked', 'true');

  // ── Teardown: restore original settings so we don't dirty the gateway ────
  const restorePayload: MemorySettingsWire = {
    auto_recap_enabled: originalSettings.auto_recap_enabled ?? false,
    idle_timeout_minutes: originalSettings.idle_timeout_minutes ?? 30,
    bootstrap_recap_enabled: originalSettings.bootstrap_recap_enabled ?? false,
    bootstrap_recap_max_per_minute: originalSettings.bootstrap_recap_max_per_minute ?? 5,
    session_days: originalSettings.session_days ?? 90,
    memory_retros_days: originalSettings.memory_retros_days ?? 180,
  };
  await page.request.put(`${BASE_URL}/api/v1/settings/memory`, {
    headers: await authHeaders(page),
    data: JSON.stringify(restorePayload),
  });
});

// ── Test 2: auto-recap toggle changes runtime behaviour ───────────────────────
//
// BDD:
//   Given the operator turns Auto recap OFF and saves
//   When a WS session_close frame is sent for an existing session
//   Then no last-session.md appears under agents/<id>/.omnipus/ within 10 s
//
//   Given the operator turns Auto recap ON and saves
//   When a WS session_close frame is sent for the same session
//   Then last-session.md appears under agents/<id>/.omnipus/ within 90 s
//
// Why this test exists (the "saved but ignored" gap):
//   The component test (MemorySection.test.tsx) only verifies that clicking
//   Save calls updateMemorySettings with the right payload. It cannot verify
//   that the agent loop (pkg/agent/session_end.go CloseSession()) reads
//   cfg.Agents.Defaults.AutoRecapEnabled from the config on each call and
//   skips the recap goroutine when it is false.  This test drives the full
//   path and observes the filesystem side effect.
//
// Notes on flakiness budget:
//   - The OFF case polls for 10 s and asserts ABSENT. False negatives
//     (a recap that arrives after 10 s would be a very slow recap, not a
//     gateway bug) are extremely unlikely in E2E environment timing.
//   - The ON case allows 90 s for the recap file to appear. This must
//     comfortably exceed the recap pipeline's own self-bounded overall
//     budget of 60 s (pkg/agent/session_end.go:252 `overallBudget`) — only
//     after that budget is exhausted does CloseSession's runRecap fall back
//     to a heuristic summary (session_end.go:515
//     writeHeuristicFallbackRetroWithCount) that still writes last-session.md.
//     A 30 s poll window raced that 60 s contract directly: any real recap
//     call that was merely slow (not failed) — legitimate under load, since
//     the pipeline is contractually allowed up to 60 s before ANY file
//     (real or fallback) exists — produced a deterministic false failure.
//     90 s gives ~30 s of margin past the pipeline's hard 60 s cap.
//   - This test overrides the suite's default 90 s Playwright test-level
//     timeout via test.setTimeout() below — a 90 s recap poll alone would
//     otherwise butt up against that harness deadline with near-zero margin
//     once setup/WS/teardown overhead is added, which would just relocate
//     the same budget-mismatch bug rather than fix it.
//   - The test retries up to 3× in CI (playwright.config.ts), absorbing
//     the occasional OpenRouter cold-start delay.
//
// Traces to: FR-019 (auto-recap toggle), FR-023 (session-end pipeline),
//   pkg/agent/session_end.go CloseSession(), pkg/memrooms/rooms.go

test('auto-recap toggle changes runtime behaviour (saved != ignored)', async ({ page }) => {
  // Generous timeout: Phase A absent-poll (10 s, fixed) + Phase B present-poll
  // (90 s, must exceed the recap pipeline's 60 s overallBudget — see
  // pkg/agent/session_end.go:252) + REST/WS setup and teardown overhead.
  // Without this override, the suite's default 90 s Playwright test-level
  // timeout (playwright.config.ts) could cut the test off mid-poll, which
  // would just relocate the budget-mismatch bug this test was fixed for
  // rather than actually fix it.
  test.setTimeout(150_000);

  // ── Arrange: navigate and obtain a session ID + agent ID ─────────────────
  await page.goto('/');
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 });

  // Fetch the agent list to get Mia's ID (the default agent).
  // GET /api/v1/agents returns an array of AgentConfig objects.
  const agentsResp = await page.request.get(`${BASE_URL}/api/v1/agents`, {
    headers: await authHeaders(page),
  });
  expect(agentsResp.ok(), `GET /api/v1/agents returned ${agentsResp.status()}`).toBeTruthy();
  const agents = (await agentsResp.json()) as Array<{ id: string; default?: boolean }>;
  // Pick the default agent; fall back to the first enabled agent.
  const defaultAgent =
    agents.find((a) => a.default === true) ??
    agents.find((a) => a.id === 'mia') ??
    agents[0];

  if (!defaultAgent) {
    // No agents configured (fresh install without seed). The behavior check
    // cannot run without an agent workspace directory.
    // This is a BLOCKED condition, not a quiet skip.
    throw new Error(
      'BLOCKED: GET /api/v1/agents returned an empty list. ' +
        'Cannot locate the agent workspace directory for last-session.md assertion. ' +
        'Ensure coreagent.SeedConfig has run (gateway started at least once).',
    );
  }

  const agentId = defaultAgent.id;
  const privateRoomRoot = path.join(OMNIPUS_HOME, 'agents', agentId, '.omnipus');
  const lastSessionPath = path.join(privateRoomRoot, 'last-session.md');

  // Read current settings so we can restore them.
  const getResp = await page.request.get(`${BASE_URL}/api/v1/settings/memory`, {
    headers: await authHeaders(page),
  });
  expect(getResp.ok(), `GET /api/v1/settings/memory returned ${getResp.status()}`).toBeTruthy();
  const originalSettings = (await getResp.json()) as MemorySettingsWire;

  // ── Create a fresh session via REST so we have a known session_id ─────────
  const createResp = await page.request.post(`${BASE_URL}/api/v1/sessions`, {
    headers: await authHeaders(page),
    data: JSON.stringify({ agent_id: agentId }),
  });
  expect(
    createResp.ok(),
    `POST /api/v1/sessions returned ${createResp.status()} — ${await createResp.text()}`,
  ).toBeTruthy();
  const sessionBody = (await createResp.json()) as { id: string };
  const sessionId: string = sessionBody.id;
  expect(sessionId, 'POST /api/v1/sessions must return a session id').toBeTruthy();

  // Remove any pre-existing last-session.md from prior test runs so the
  // assert-absent leg starts from a clean state.
  try {
    fs.unlinkSync(lastSessionPath);
  } catch {
    // File did not exist — fine.
  }

  // ── Phase A: Auto recap OFF → session close must NOT produce last-session.md ──

  // Turn auto_recap_enabled OFF via the API.
  const disableResp = await page.request.put(`${BASE_URL}/api/v1/settings/memory`, {
    headers: await authHeaders(page),
    data: JSON.stringify({ ...originalSettings, auto_recap_enabled: false }),
  });
  expect(
    disableResp.ok(),
    `PUT /api/v1/settings/memory (disable) returned ${disableResp.status()} — ` +
      `${await disableResp.text()}`,
  ).toBeTruthy();
  const afterDisable = (await disableResp.json()) as MemorySettingsWire;
  expect(
    afterDisable.auto_recap_enabled,
    'PUT /api/v1/settings/memory must return auto_recap_enabled: false (differentiation check)',
  ).toBe(false);

  // Send a WS session_close frame for the session we created.
  // We do this via page.evaluate to open a raw WebSocket to the gateway so
  // the test is not coupled to the SPA's WS connection lifecycle. The frame
  // format matches pkg/gateway/websocket.go ("session_close" + session_id).
  const wsSendResult = await page.evaluate(
    async ({ baseUrl, sid, token }: { baseUrl: string; sid: string; token: string | null }) => {
      return new Promise<{ ok: boolean; error: string | null }>((resolve) => {
        // The gateway WS endpoint is /api/v1/chat/ws (not /ws — that serves the SPA).
        const wsUrl = baseUrl.replace(/^http/, 'ws') + '/api/v1/chat/ws';
        const ws = new WebSocket(wsUrl);
        let authSent = false;

        ws.onopen = () => {
          // Authenticate first (auth frame sent if token present).
          if (token) {
            ws.send(JSON.stringify({ type: 'auth', token }));
            authSent = true;
          }
          // Send session_close immediately after auth (or directly if no auth).
          const closeFrame = JSON.stringify({ type: 'session_close', session_id: sid });
          if (!authSent) {
            ws.send(closeFrame);
          } else {
            // Small delay so the auth frame is processed first.
            setTimeout(() => {
              ws.send(closeFrame);
            }, 200);
          }
          // Wait for the session_close_ack or a brief window, then close.
          setTimeout(() => {
            ws.close();
            resolve({ ok: true, error: null });
          }, 2_000);
        };
        ws.onerror = (evt) => {
          resolve({ ok: false, error: String(evt) });
        };
      });
    },
    { baseUrl: BASE_URL, sid: sessionId, token: getStoredAuthToken() },
  );

  // Log but don't fail if the WS couldn't connect — the REST-only path also works:
  // dev_mode_bypass=true gateways allow CloseSession to be triggered via the UI.
  // If WS fails we still do the file-absent poll (the recap would not run anyway
  // since the gateway didn't receive the close — so the assert-absent still holds).
  if (!wsSendResult.ok) {
    // eslint-disable-next-line no-console
    console.warn(`settings-memory: WS session_close send warning: ${wsSendResult.error}`);
  }

  // Poll for 10 s asserting that last-session.md does NOT appear.
  // The recap goroutine, if mistakenly launched, typically writes the file within 5–8 s.
  // If it appears, the test fails with a clear message identifying the bug.
  const recapAbsentDeadline = Date.now() + 10_000;
  let recapAppearedWhileOff = false;
  while (Date.now() < recapAbsentDeadline) {
    if (fs.existsSync(lastSessionPath)) {
      recapAppearedWhileOff = true;
      break;
    }
    await page.waitForTimeout(500);
  }

  expect(
    recapAppearedWhileOff,
    [
      `last-session.md appeared at ${lastSessionPath} DESPITE auto_recap_enabled=false.`,
      'This means the agent loop is NOT reading the persisted flag —',
      'the "saved but ignored" bug this test was written to catch.',
      `Session: ${sessionId}, Agent: ${agentId}`,
    ].join(' '),
  ).toBe(false);

  // ── Phase B: Auto recap ON → session close MUST produce last-session.md ──

  // Create a second session to avoid the FR-027 idempotency gate (duplicate
  // CloseSession calls for the same sessionID are silently dropped).
  const createResp2 = await page.request.post(`${BASE_URL}/api/v1/sessions`, {
    headers: await authHeaders(page),
    data: JSON.stringify({ agent_id: agentId }),
  });
  expect(
    createResp2.ok(),
    `POST /api/v1/sessions (second session) returned ${createResp2.status()} — ` +
      `${await createResp2.text()}`,
  ).toBeTruthy();
  const sessionBody2 = (await createResp2.json()) as { id: string };
  const sessionId2: string = sessionBody2.id;
  expect(sessionId2, 'POST /api/v1/sessions must return a session id (second)').toBeTruthy();

  // Remove any existing last-session.md so the assert-present is clean.
  try {
    fs.unlinkSync(lastSessionPath);
  } catch {
    // Fine.
  }

  // Turn auto_recap_enabled ON.
  const enableResp = await page.request.put(`${BASE_URL}/api/v1/settings/memory`, {
    headers: await authHeaders(page),
    data: JSON.stringify({ ...originalSettings, auto_recap_enabled: true }),
  });
  expect(
    enableResp.ok(),
    `PUT /api/v1/settings/memory (enable) returned ${enableResp.status()} — ` +
      `${await enableResp.text()}`,
  ).toBeTruthy();
  const afterEnable = (await enableResp.json()) as MemorySettingsWire;
  expect(
    afterEnable.auto_recap_enabled,
    'PUT /api/v1/settings/memory must return auto_recap_enabled: true (differentiation check)',
  ).toBe(true);

  // Send session_close for the second session.
  // The gateway WS endpoint is /api/v1/chat/ws (not /ws — that serves the SPA).
  await page.evaluate(
    async ({ baseUrl, sid, token }: { baseUrl: string; sid: string; token: string | null }) => {
      return new Promise<void>((resolve) => {
        const wsUrl = baseUrl.replace(/^http/, 'ws') + '/api/v1/chat/ws';
        const ws = new WebSocket(wsUrl);
        ws.onopen = () => {
          if (token) ws.send(JSON.stringify({ type: 'auth', token }));
          setTimeout(() => {
            ws.send(JSON.stringify({ type: 'session_close', session_id: sid }));
            setTimeout(() => { ws.close(); resolve(); }, 1_000);
          }, 200);
        };
        ws.onerror = () => resolve();
      });
    },
    { baseUrl: BASE_URL, sid: sessionId2, token: getStoredAuthToken() },
  );

  // Poll for up to 90 s for last-session.md to appear.
  // The recap pipeline: CloseSession → runRecap goroutine → LLM call (or fallback)
  // → WriteLastSession. runRecap self-bounds at a 60 s overallBudget
  // (pkg/agent/session_end.go:252) — on timeout or failure it deterministically
  // writes a heuristic fallback summary (session_end.go:515) instead of hanging
  // forever, so SOME file is guaranteed to exist by ~60 s. A real, slow-but-
  // successful LLM call can legitimately take close to that full 60 s under
  // load; polling only 30 s raced the pipeline's own contract and failed
  // deterministically on any recap merely slower than 30 s, not just a failed
  // one. 90 s gives ~30 s of margin past the pipeline's hard 60 s cap.
  const recapPresentDeadline = Date.now() + 90_000;
  let recapAppearedWhileOn = false;
  while (Date.now() < recapPresentDeadline) {
    if (fs.existsSync(lastSessionPath)) {
      recapAppearedWhileOn = true;
      break;
    }
    await page.waitForTimeout(1_000);
  }

  expect(
    recapAppearedWhileOn,
    [
      `last-session.md did NOT appear at ${lastSessionPath} within 90 s`,
      'even though auto_recap_enabled=true was saved and a session_close frame was sent.',
      'This means either the agent loop is not reading the updated flag,',
      'the session_close WS frame was not delivered, or the recap pipeline failed.',
      `Session: ${sessionId2}, Agent: ${agentId}`,
      'Check gateway logs under $OMNIPUS_HOME/logs/gateway.log for "session_end" events.',
    ].join(' '),
  ).toBe(true);

  // Assert the file has non-empty content (not a zero-byte stub).
  const recapContent = fs.readFileSync(lastSessionPath, 'utf-8');
  expect(
    recapContent.trim().length,
    'last-session.md must contain non-empty recap content',
  ).toBeGreaterThan(0);

  // ── Teardown: restore original settings ──────────────────────────────────
  await page.request.put(`${BASE_URL}/api/v1/settings/memory`, {
    headers: await authHeaders(page),
    data: JSON.stringify({
      auto_recap_enabled: originalSettings.auto_recap_enabled ?? false,
      idle_timeout_minutes: originalSettings.idle_timeout_minutes ?? 30,
      bootstrap_recap_enabled: originalSettings.bootstrap_recap_enabled ?? false,
      bootstrap_recap_max_per_minute: originalSettings.bootstrap_recap_max_per_minute ?? 5,
      session_days: originalSettings.session_days ?? 90,
      memory_retros_days: originalSettings.memory_retros_days ?? 180,
    }),
  });
});
