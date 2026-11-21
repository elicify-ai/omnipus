/**
 * export-import-b1.spec.ts — spec order 29 (issue #1967, slice B1).
 *
 * ONE test over the whole feature: sidebar entry → export a seeded custom
 * agent → real download → import that file → "(imported)" result → Team shows
 * it → Mia's sheet has no Export → the custom agent's sheet Export lands on
 * Choose with the agent ticked. axe serious/critical check on the landing and
 * each step (spec "Acceptance & Traceability" row 29).
 *
 * Shared gateway (pre-authenticated global storageState), seeded through the
 * API like agent-picker-freshness. RED: the /#/export-import route does not
 * exist yet — the test fails at the first missing surface, which is the right
 * reason.
 */
import { test, expect } from '@playwright/test';
import { expectA11yClean } from './fixtures/a11y';

const baseURL = process.env.OMNIPUS_URL || 'http://localhost:6060';
const AGENT_NAME = 'Writer B1';

test.describe.configure({ retries: 0 });
test.setTimeout(180_000);

test('B1 end to end: export a custom agent, import it back, and reach it from the sheets', async ({ page }) => {
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

  await page.getByRole('button', { name: 'Download' }).click();
  const download = await page.waitForEvent('download', { timeout: 30_000 });
  const savedPath = await download.path();
  const bundle = await page.request.get(`${baseURL}/api/v1/packages/export`);
  void bundle; // the real bytes came through the browser download event
  expect(download.suggestedFilename()).toBe(`${AGENT_NAME}.omnpkg`);
  await expect(page.getByText(new RegExp(`Saved ${AGENT_NAME}\\.omnpkg \\(`))).toBeVisible();
  expect((await import('node:fs')).statSync(savedPath!).size).toBeGreaterThan(0);

  // ── US-4: import the same bundle back (a name clash is expected). ──
  await page.getByRole('button', { name: 'Done' }).click();
  await page.getByRole('button', { name: 'Start import' }).click();
  await expect(page.getByText('Bundle files end in .omnpkg')).toBeVisible();
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
  await page.getByText('Mia').first().click();
  await expect(page.getByRole('button', { name: 'Export' })).toHaveCount(0);
  await page.keyboard.press('Escape');

  await page.getByText(AGENT_NAME).first().click();
  await page.getByRole('button', { name: 'Export' }).click();
  await expect(page).toHaveURL(/export-import/);
  await expect(page.getByRole('radio', { name: new RegExp(AGENT_NAME) })).toBeChecked();
});
