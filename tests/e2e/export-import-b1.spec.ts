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
 * API like agent-picker-freshness. The seeded agent is created with a known
 * id and DELETED in the finally block (together with the "(imported)" copy
 * the flow makes), so re-runs on the same gateway start clean.
 *
 * RED: the /#/export-import route does not exist yet — the test fails at the
 * first missing surface, which is the right reason. Collection-verified in
 * this environment (no gateway runs here); the live row runs on the agreed
 * exact-version instance at the cut.
 */
import { test, expect } from '@playwright/test';
import { expectA11yClean } from './fixtures/a11y';
import { statSync, unlinkSync } from 'node:fs';

const baseURL = process.env.OMNIPUS_URL || 'http://localhost:6060';
const AGENT_NAME = 'Writer B1';

test.describe.configure({ retries: 0 });
test.setTimeout(180_000);

test('B1 end to end: export a custom agent, import it back, and reach it from the sheets', async ({ page }) => {
  let seededAgentId = '';
  let savedPath: string | null = null;
  try {
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
    seededAgentId = ((await created.json()) as { id: string }).id;
    expect(seededAgentId, 'the seeded agent id is retained for the cleanup').toBeTruthy();

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
    // The real bytes arrived through the browser download event: a bundle
    // carries the manifest and payload, so it is not an empty stub.
    expect(statSync(savedPath!).size, 'the downloaded bundle is non-trivial').toBeGreaterThan(1024);

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

    await page.getByText(AGENT_NAME).first().click();
    const writerSheet = page.getByRole('dialog');
    await expect(writerSheet, 'the seeded agent sheet is open').toBeVisible();
    await writerSheet.getByRole('button', { name: 'Export' }).click();
    await expect(page).toHaveURL(/export-import/);
    await expect(page.getByRole('radio', { name: new RegExp(AGENT_NAME) })).toBeChecked();
  } finally {
    // Isolation (CHECK W03): the seeded agent is deleted by its id; the
    // "(imported)" copy the flow created is deleted by name lookup, so
    // repeated runs on the same gateway start clean.
    if (seededAgentId) {
      await page.request.delete(`${baseURL}/api/v1/agents/${seededAgentId}`);
    }
    const listed = await page.request.get(`${baseURL}/api/v1/agents`);
    if (listed.ok()) {
      const agents = (await listed.json()) as Array<{ id: string; name: string }>;
      for (const a of Array.isArray(agents) ? agents : []) {
        if (a.name === `${AGENT_NAME} (imported)`) {
          await page.request.delete(`${baseURL}/api/v1/agents/${a.id}`);
        }
      }
    }
    if (savedPath) {
      try {
        unlinkSync(savedPath);
      } catch {
        // best-effort temp cleanup; the download dir is the harness's
      }
    }
  }
});
