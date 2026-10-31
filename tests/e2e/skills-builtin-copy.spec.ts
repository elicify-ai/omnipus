// skills-builtin-copy.spec.ts — RED e2e spec, landing/skills (T9): S-6.4's
// Playwright legs (spec §17.1 row 21, T-N3). NOT RUN in the RED phase: it
// needs a live gateway serving the T9 SPA (built SPA + OMNIPUS_HOME); it is
// written to the existing e2e conventions here so the implementing worker
// can run it against a candidate build.
// Traces to: skills-spec-r13.md S-6.4.

import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';

// Global storageState provides a pre-authenticated session
// (playwright.config.ts + global-setup.ts).

test.beforeEach(async ({ page }) => {
  // HashRouter: routes live in the fragment, not the pathname.
  await page.goto('/#/skills');
});

test('S-6.4: a built-in row offers Make a copy and the dialog commits plan-copy', async ({ page }) => {
  await expect(page).toHaveURL(/skills/, { timeout: 10_000 });

  // The built-in row: read-only badge, no delete control, and the new
  // Make a copy control (RED until T9 lands).
  const planRow = page.locator('div', { hasText: /^plan$/ }).locator('xpath=ancestor::*[self::div][1]');
  await expect(planRow).toBeVisible({ timeout: 10_000 });
  await expect(planRow.getByText('Built-in')).toBeVisible();
  await expect(planRow.getByRole('button', { name: /Remove plan/ })).toHaveCount(0);

  const makeCopy = planRow.getByRole('button', { name: /Make a copy/i });
  await expect(makeCopy).toBeVisible();
  await makeCopy.click();

  // The copy dialog: a name field and Create copy.
  const dialog = page.locator('[role="dialog"]').first();
  await expect(dialog).toBeVisible();
  await dialog.getByLabel(/copy name/i).fill('plan-copy');
  await dialog.getByRole('button', { name: /Create copy/i }).click();

  // 201 committed: the list now shows plan-copy (a user-owned row, which
  // itself carries a delete control).
  await expect(page.getByText('plan-copy')).toBeVisible({ timeout: 10_000 });
});
