/**
 * export-import-b1.spec.ts — spec order 29 (issue #1967, slice B1).
 *
 * ONE test over the whole feature: sidebar entry → export a seeded custom
 * agent → real download → import that file → "(imported)" result → Team shows
 * it → Mia's sheet has no Export → the custom agent's sheet Export lands on
 * Choose with the agent ticked. axe serious/critical check on the landing and
 * EACH step, including Export Download and Import File (spec "Acceptance &
 * Traceability" row 29; CHECK W03).
 *
 * Shared gateway (pre-authenticated global storageState), seeded through the
 * API like agent-picker-freshness. Ownership (CHECK 3 W03/N05): the import's
 * created agent id is captured from the apply RESPONSE at the request/
 * response boundary — before any later UI assertion can fail — so the finally
 * block always owns and deletes exactly what this run created (the seed and
 * the imported copy), by id, never by a shared name.
 *
 * Failure-path reporting (CHECK 3 W03): the finally block ALWAYS runs — on a
 * failed flow too. Cleanup problems there are reported (console.warn) and the
 * original flow error is rethrown unmasked; on a completed flow any cleanup
 * problem fails the test explicitly. The rerun accepts the spec's numbered
 * suffix ("(imported 2)") wherever the imported name is asserted.
 *
 * RED: the /#/export-import route does not exist yet — the test fails at the
 * first missing surface, which is the right reason. Collection-verified in
 * this environment (no gateway runs here); the live row runs on the agreed
 * exact-version instance at the cut.
 */
import { test, expect, type Page } from '@playwright/test';
import { expectA11yClean } from './fixtures/a11y';
import { existsSync, readFileSync, statSync, unlinkSync } from 'node:fs';
import path from 'node:path';

const baseURL = process.env.OMNIPUS_URL || 'http://localhost:6060';

// ── Auth helpers (same pattern as agent-grep.spec.ts, no shared export) ─────
// The seeding, verification and cleanup calls ride page.request on the shared
// gateway with the global storageState; the gateway's CSRF middleware still
// requires the X-CSRF-Token header on its requests (CI PR #1971, job
// "E2E - ui-4": the seeding POST answered 403 "csrf header missing"). The
// token comes from the SAME storageState file the Playwright config mounts
// (playwright.config.ts storageState = OMNIPUS_AUTH_FILE or
// fixtures/.auth/admin.json), so cookie, token and page share one identity.
const AUTH_FILE = process.env.OMNIPUS_AUTH_FILE
  ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
  : path.join(path.dirname(new URL(import.meta.url).pathname), 'fixtures/.auth/admin.json');

function getStoredAuthToken(): string | null {
  if (!existsSync(AUTH_FILE)) return null;
  try {
    const raw = readFileSync(AUTH_FILE, 'utf-8');
    const state = JSON.parse(raw) as {
      origins?: Array<{ origin: string; localStorage?: Array<{ name: string; value: string }> }>;
    };
    for (const origin of state.origins ?? []) {
      for (const item of origin.localStorage ?? []) {
        if (item.name === 'omnipus_auth_token') return item.value;
      }
    }
  } catch {
    // Auth file may not exist on first run
  }
  return null;
}

async function apiHeaders(page: Page): Promise<Record<string, string>> {
  const token = getStoredAuthToken();
  const cookies = await page.context().cookies();
  const csrf = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf')?.value ?? null;
  return {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...(csrf ? { 'X-CSRF-Token': csrf } : {}),
  };
}

test.describe.configure({ retries: 0 });
test.setTimeout(180_000);

test('B1 end to end: export a custom agent, import it back, and reach it from the sheets', async ({ page }) => {
  // W03 (CHECK 4 warning): the fixture name carries a run-unique token, so
  // an EARLIER run's "Writer B1 …(imported)" leftovers on the shared gateway
  // can never match this run's chooser or Team observations — every
  // predicate below is anchored on this run's exact seeded identity, and a
  // foreign agent is never picked (and never cleaned up).
  const AGENT_NAME = `Writer B1 ${Date.now().toString(36)}`;
  // The spec's name-suffix rule: a name clash proposes "<name> (imported)",
  // and a rerun that inherits a copy gets "(imported 2)", "(imported 3)", …
  // — anchored on THIS run's unique token, so only this run's import matches.
  const IMPORTED_NAME = new RegExp(`${AGENT_NAME} \\(imported( \\d+)?\\)`);
  let savedPath: string | null = null;
  // Ownership bookkeeping (CHECK 3 W03/N05; CHECK 4 N06): ONLY ids this run
  // provably created — the seeded agent and EVERY id the apply response
  // returns. Nothing is inferred from names or before/after list
  // differences, so this run can never delete another run's agent on the
  // shared gateway.
  const createdIds = new Set<string>();
  const problems: string[] = [];
  let flowError: unknown = null;
  try {
    // ── Seed one exportable custom agent through the API (storageState auth). ──
    const soulText =
      'You are the B1 bundle writer. You turn short outlines into polished text and nothing else. '.repeat(40);
    const created = await page.request.post(`${baseURL}/api/v1/agents`, {
      headers: await apiHeaders(page),
      data: {
        type: 'Main',
        name: AGENT_NAME,
        soul: soulText,
        description: 'Seeded for the export/import B1 end-to-end row',
      },
    });
    expect(created.status(), await created.text()).toBe(201);
    const seededAgentId = ((await created.json()) as { id: string }).id;
    expect(seededAgentId, 'the seeded agent id is retained for the cleanup').toBeTruthy();
    createdIds.add(seededAgentId);

    // ── The app loads with the workspace chrome. ──
    await page.goto('/');
    await expect(page.getByTestId('workspace-top-bar')).toBeVisible({ timeout: 20_000 });

    // ── US-1: the sidebar entry opens the landing. ──
    await page.getByRole('link', { name: 'Export & Import' }).click();
    await expect(page.getByRole('heading', { name: 'Export & Import' })).toBeVisible();
    await expectA11yClean(page);

    // ── US-2: export the seeded agent. ──
    await page.getByRole('button', { name: 'Start export' }).click();
    await expect(page.getByText('A bundle holds one agent in this version.')).toBeVisible();
    await expectA11yClean(page);
    await page.getByRole('radio', { name: new RegExp(AGENT_NAME) }).check();
    await page.getByRole('button', { name: 'Review' }).click();
    await expect(page.getByText('Left out')).toBeVisible({ timeout: 20_000 });
    await expectA11yClean(page);

    // The download listener is armed BEFORE the click (CHECK W03): an early
    // download event cannot be missed.
    const downloadPromise = page.waitForEvent('download', { timeout: 30_000 });
    await page.getByRole('button', { name: 'Download' }).click();
    const download = await downloadPromise;
    savedPath = await download.path();
    expect(download.suggestedFilename()).toBe(`${AGENT_NAME}.omnpkg`);
    await expect(page.getByText(new RegExp(`Saved ${AGENT_NAME}\\.omnpkg \\(`))).toBeVisible();
    await expectA11yClean(page); // the Download step (row 29: each step)
    // The real bytes arrived through the browser download event. The spec
    // defines no compressed minimum (the seeded prompt is highly repetitive
    // and compresses small — CHECK 2 W03), so the floor is only "not empty";
    // the CONTENT proof is the re-import below: a corrupt or empty bundle is
    // refused as not_a_package before any preview renders.
    expect(statSync(savedPath!).size, 'the downloaded bundle is not empty').toBeGreaterThan(0);

    // ── US-4: import the same bundle back (a name clash is expected). ──
    await page.getByRole('button', { name: 'Done' }).click();
    await page.getByRole('button', { name: 'Start import' }).click();
    await expect(page.getByText('Bundle files end in .omnpkg')).toBeVisible();
    await expectA11yClean(page); // the Import File step (row 29: each step)
    await page.setInputFiles('input[type="file"]', savedPath!);
    await expect(page.getByText(IMPORTED_NAME)).toBeVisible({ timeout: 30_000 });
    await expectA11yClean(page);

    await page.getByRole('button', { name: 'Continue' }).click();
    await expect(page.getByText('Added as new. Existing agents are never changed.')).toBeVisible();
    await expectA11yClean(page);

    // Ownership at the request/response boundary (CHECK 3 W03/N05; CHECK 4
    // N06): the apply listener is armed BEFORE the click, and EVERY agent id
    // the response returns is recorded the moment it arrives — BEFORE the
    // result screen's assertions — so if apply commits and any later check
    // fails, the finally block still owns and deletes everything this run
    // created. B1's one-agent scope means the response creates EXACTLY ONE
    // agent; that cardinality assertion runs AFTER ownership is recorded, so
    // a wrong response shape still cleans up after itself.
    const applyResponsePromise = page.waitForResponse(
      (r) => r.request().method() === 'POST' && r.url().endsWith('/apply'),
      { timeout: 30_000 },
    );
    await page.getByRole('button', { name: 'Import' }).click();
    const applyResponse = await applyResponsePromise;
    expect(applyResponse.status(), await applyResponse.text()).toBe(200);
    const applyBody = (await applyResponse.json()) as { created?: Array<{ new_id?: string }> };
    const returnedIds = (applyBody.created ?? [])
      .map((entry) => entry?.new_id)
      .filter((id): id is string => typeof id === 'string' && id.length > 0);
    for (const id of returnedIds) {
      createdIds.add(id);
    }
    expect(returnedIds, 'the apply response creates exactly one new agent (B1 is one agent per bundle)').toHaveLength(1);
    const importedId = returnedIds[0];
    // N06: the response's identity is the one this run observes downstream —
    // the created agent is fetched BY ID, tying the following UI
    // observations to the apply response's new_id, not merely to a name.
    const importedFetch = await page.request.get(`${baseURL}/api/v1/agents/${importedId}`, {
      headers: await apiHeaders(page),
    });
    expect(importedFetch.status(), await importedFetch.text()).toBe(200);

    await expect(page.getByText(new RegExp(`${AGENT_NAME} \\(imported( \\d+)?\\) was added to`))).toBeVisible({
      timeout: 30_000,
    });
    await expectA11yClean(page);

    // ── The imported agent is reachable in the workspace Team. ──
    await page.getByRole('button', { name: 'Open Team' }).click();
    await expect(page.getByText(IMPORTED_NAME)).toBeVisible({ timeout: 20_000 });

    // ── US-3: the sheets — no Export on Mia, Export lands on Choose ticked. ──
    // The sheet must be OPEN before its buttons are judged (CHECK W03): a
    // closed sheet would also have no Export button, proving nothing.
    await page.getByText('Mia').first().click();
    const miaSheet = page.getByRole('dialog');
    await expect(miaSheet, "Mia's sheet is open before its controls are judged").toBeVisible();
    await expect(miaSheet.getByRole('button', { name: 'Export' })).toHaveCount(0);
    await page.keyboard.press('Escape');

    // Exact created-agent selection (CHECK 2 W03): after the import, both
    // "Writer B1" and a suffixed imported copy exist — a substring locator
    // would match either. The seeded agent's row is matched by its exact
    // name.
    await page.getByText(AGENT_NAME, { exact: true }).click();
    const writerSheet = page.getByRole('dialog');
    await expect(writerSheet, 'the seeded agent sheet is open').toBeVisible();
    await writerSheet.getByRole('button', { name: 'Export' }).click();
    await expect(page).toHaveURL(/export-import/);
    // The Choose step landed with THE SEEDED agent ticked — the imported copy
    // must not satisfy this assertion, so the radio is matched exactly too.
    await expect(page.getByRole('radio', { name: AGENT_NAME, exact: true })).toBeChecked();
  } catch (e) {
    // The finally block below runs on this path too; keep the primary error
    // so cleanup can never mask or replace it (CHECK 3 W03).
    flowError = e;
  } finally {
    // Isolation: every agent THIS run created is deleted by id. Each step is
    // individually guarded — one throwing DELETE must not stop the remaining
    // cleanup or replace the flow's own failure.
    for (const id of createdIds) {
      try {
        const del = await page.request.delete(`${baseURL}/api/v1/agents/${id}`, {
          headers: await apiHeaders(page),
        });
        if (!del.ok() && del.status() !== 404) {
          problems.push(`delete ${id} failed: ${del.status()}`);
        }
      } catch (e) {
        problems.push(`delete ${id} threw: ${String(e)}`);
      }
    }
    if (savedPath) {
      try {
        unlinkSync(savedPath);
      } catch (e) {
        problems.push(`download temp cleanup failed: ${String(e)}`);
      }
    }
  }
  // Report cleanup outcomes on a failed flow without masking the primary
  // error, and fail a completed run on any cleanup problem (CHECK 3 W03 —
  // no swallowed unlink, no ignored DELETE outcome, no omitted problems).
  if (flowError !== null) {
    if (problems.length > 0) {
      console.warn(`[export-import-b1] cleanup problems on a failed run: ${problems.join('; ')}`);
    }
    throw flowError;
  }
  if (problems.length > 0) {
    throw new Error(`cleanup problems on a completed run: ${problems.join('; ')}`);
  }
});
