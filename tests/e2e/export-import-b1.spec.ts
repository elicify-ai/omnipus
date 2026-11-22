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
 * API like agent-picker-freshness. Every agent THIS run creates (the seed and
 * the import's own "(imported)" copy, discovered by id) is DELETED by id in
 * the finally block, and each deletion's outcome is collected — a failed
 * cleanup is reported after the finally, never swallowed, and never masks the
 * flow's own failure (CHECK 2 W03).
 *
 * RED: the /#/export-import route does not exist yet — the test fails at the
 * first missing surface, which is the right reason. Collection-verified in
 * this environment (no gateway runs here; still true after round 2); the
 * live row runs on the agreed exact-version instance at the cut.
 */
import { test, expect } from '@playwright/test';
import { expectA11yClean } from './fixtures/a11y';
import { statSync, unlinkSync } from 'node:fs';

const baseURL = process.env.OMNIPUS_URL || 'http://localhost:6060';
const AGENT_NAME = 'Writer B1';

test.describe.configure({ retries: 0 });
test.setTimeout(180_000);

test('B1 end to end: export a custom agent, import it back, and reach it from the sheets', async ({ page }) => {
  let savedPath: string | null = null;
  // Isolation bookkeeping (CHECK 2 W03): the ids this run CREATED (seeded +
  // imported), the "(imported)" ids that already existed before the run (a
  // previous run's leftovers — never ours to delete), and every cleanup
  // problem. Problems are thrown AFTER the finally — which only runs when the
  // flow itself completed, so cleanup can never mask the flow's own failure.
  const createdIds = new Set<string>();
  const preExistingImportedIds = new Set<string>();
  const problems: string[] = [];
  try {
    // Snapshot the "(imported)" agents that already exist BEFORE the run, so
    // a rerun on a shared gateway deletes only what it created (a leftover
    // suffixed copy like "Writer B1 (imported 2)" belongs to the earlier run).
    const before = await page.request.get(`${baseURL}/api/v1/agents`);
    if (before.ok()) {
      const agents = (await before.json()) as Array<{ id: string; name: string }>;
      for (const a of Array.isArray(agents) ? agents : []) {
        if (new RegExp(`^${AGENT_NAME} \\(imported( \\d+)?\\)$`).test(a.name)) {
          preExistingImportedIds.add(a.id);
        }
      }
    } else {
      problems.push(`pre-run agents list failed: ${before.status()}`);
    }

    // ── Seed one exportable custom agent through the API (storageState auth). ──
    const soulText =
      'You are the B1 bundle writer. You turn short outlines into polished text and nothing else. '.repeat(40);
    const created = await page.request.post(`${baseURL}/api/v1/agents`, {
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
    await expect(page.getByText(new RegExp(`${AGENT_NAME} \\(imported\\)`))).toBeVisible({ timeout: 30_000 });
    await expectA11yClean(page);

    await page.getByRole('button', { name: 'Continue' }).click();
    await expect(page.getByText('Added as new. Existing agents are never changed.')).toBeVisible();
    await expectA11yClean(page);
    await page.getByRole('button', { name: 'Import' }).click();
    await expect(page.getByText(new RegExp(`${AGENT_NAME} \\(imported\\) was added to`))).toBeVisible({ timeout: 30_000 });
    await expectA11yClean(page);

    // Retain the import's own product (CHECK 2 W03): the new agent's id is
    // discovered by exact created name (including the spec's suffix rule,
    // e.g. "(imported 2)" when a rerun inherited a copy) and cleaned up by
    // id below — never by a shared constant name.
    const afterImport = await page.request.get(`${baseURL}/api/v1/agents`);
    expect(afterImport.ok(), 'the agents list answers after the import').toBeTruthy();
    const listed = (await afterImport.json()) as Array<{ id: string; name: string }>;
    for (const a of Array.isArray(listed) ? listed : []) {
      if (new RegExp(`^${AGENT_NAME} \\(imported( \\d+)?\\)$`).test(a.name) && !preExistingImportedIds.has(a.id)) {
        createdIds.add(a.id);
      }
    }
    expect(createdIds.size, 'the import created exactly one new agent this run').toBe(2);

    // ── The imported agent is reachable in the workspace Team. ──
    await page.getByRole('button', { name: 'Open Team' }).click();
    await expect(page.getByText(new RegExp(`${AGENT_NAME} \\(imported\\)`))).toBeVisible({ timeout: 20_000 });

    // ── US-3: the sheets — no Export on Mia, Export lands on Choose ticked. ──
    // The sheet must be OPEN before its buttons are judged (CHECK W03): a
    // closed sheet would also have no Export button, proving nothing.
    await page.getByText('Mia').first().click();
    const miaSheet = page.getByRole('dialog');
    await expect(miaSheet, "Mia's sheet is open before its controls are judged").toBeVisible();
    await expect(miaSheet.getByRole('button', { name: 'Export' })).toHaveCount(0);
    await page.keyboard.press('Escape');

    // Exact created-agent selection (CHECK 2 W03): after the import, both
    // "Writer B1" and "Writer B1 (imported)" exist — a substring locator
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
  } finally {
    // Isolation (CHECK W03/2): every agent THIS run created is deleted by id,
    // and each deletion's outcome is asserted — a failed cleanup is a
    // problem, never a silent pass. Agents that only matched the imported
    // name before the run are left alone (they belong to an earlier run).
    for (const id of createdIds) {
      const del = await page.request.delete(`${baseURL}/api/v1/agents/${id}`);
      if (!del.ok() && del.status() !== 404) {
        problems.push(`delete ${id} failed: ${del.status()}`);
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
  // Reached only when the flow itself completed: report every cleanup
  // problem loudly instead of ending on a silent partial cleanup (CHECK 2
  // W03 — no swallowed unlink, no ignored DELETE outcome).
  if (problems.length > 0) {
    throw new Error(`cleanup problems on a completed run: ${problems.join('; ')}`);
  }
});
