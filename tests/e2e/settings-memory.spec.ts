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

// ── Global-settings safety net ────────────────────────────────────────────────
//
// Both tests below mutate GLOBAL gateway memory settings (auto_recap_enabled,
// idle_timeout_minutes, memory_retros_days, bootstrap_recap_*). Each test also restores
// them inline, but an assertion that throws BEFORE that inline restore would leak the
// mutated settings to sibling specs sharing this shard's gateway. These hooks snapshot
// the settings before each test and restore them afterEach EVEN ON FAILURE, making the
// restore exception-safe. Best-effort (errors swallowed) so the safety net can never
// itself fail a test; the per-test inline restores remain as harmless redundancy.
let memoryBaseline: MemorySettingsWire | null = null;

test.beforeEach(async ({ page }) => {
  const resp = await page.request.get(`${BASE_URL}/api/v1/settings/memory`, {
    headers: await authHeaders(page),
  });
  memoryBaseline = resp.ok() ? ((await resp.json()) as MemorySettingsWire) : null;
});

test.afterEach(async ({ page }) => {
  if (!memoryBaseline) return;
  const b = memoryBaseline;
  memoryBaseline = null;
  const restorePayload: MemorySettingsWire = {
    auto_recap_enabled: b.auto_recap_enabled ?? false,
    idle_timeout_minutes: b.idle_timeout_minutes ?? 30,
    bootstrap_recap_enabled: b.bootstrap_recap_enabled ?? false,
    bootstrap_recap_max_per_minute: b.bootstrap_recap_max_per_minute ?? 5,
    session_days: b.session_days ?? 90,
    memory_retros_days: b.memory_retros_days ?? 180,
  };
  await page.request
    .put(`${BASE_URL}/api/v1/settings/memory`, {
      headers: await authHeaders(page),
      data: JSON.stringify(restorePayload),
    })
    .catch(() => {});
});

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

// ── DELETED: "auto-recap toggle changes runtime behaviour (saved != ignored)" ──
//
// That test triggered the recap pipeline by sending a WS `session_close` frame.
// `session_close` / `session_close_ack` were RETIRED by DEL-08 (handover,
// explicit close and SSE removed) — it exists in no contract and in no Go
// source today, so the frame is silently ignored by the decoder. With the
// trigger gone the test could only produce a FALSE GREEN on its "disabled ->
// file absent" half and a hard failure on its "enabled -> file present" half,
// neither of which measured what it claimed.
//
// The live trigger for a human chat's recap is now the idle ticker
// (pkg/agent/loop_idle.go::resetIdleTicker -> CloseSession(idle), driven by
// agents.defaults.idle_timeout_minutes). It has no browser-reachable test seam:
// loop_idle.go::fireIdleTimeout is a Go-only seam, and the real timer's minimum
// is one minute. Porting this test onto it would also mean lowering a GLOBAL
// setting (idle_timeout_minutes=1) for ~90 s in the shared e2e gateway, which
// can close other specs' sessions mid-test.
//
// The chain itself (CloseSession -> runRecap -> last-session.md -> next-session
// injection) stays covered by Go tests: pkg/agent/session_end_*_test.go,
// pkg/agent/idle_timeout_seam_test.go and pkg/agent/session_end_smoke_test.go.
// What is LOST is the end-to-end browser hop through the real gateway + WS +
// LLM + filesystem. Reported as a coverage gap; the port recipe is in the pack
// report. Do not re-add a `session_close` send here.
