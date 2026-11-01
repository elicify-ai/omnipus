// skills-builtin-copy.spec.ts — r15 S-6.4, §17.1 row 21, T-N3.
// Requires a live gateway serving the candidate SPA and isolated OMNIPUS_HOME.
// Execution/setup receipts (not an assumed PASS) live in the RED traceability doc.

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
  // Make a copy control.
  const planRow = page.getByText('plan', { exact: true }).locator('xpath=ancestor::div[.//button][1]');
  await expect(planRow).toBeVisible({ timeout: 10_000 });
  await expect(planRow.getByText('Built-in · read-only', { exact: true })).toBeVisible();
  await expect(planRow.getByRole('button', { name: /Remove plan/ })).toHaveCount(0);

  const makeCopy = planRow.getByRole('button', { name: /Make a copy/i });
  await expect(makeCopy).toBeVisible();
  await makeCopy.click();

  // The copy dialog: a name field and Create copy.
  const dialog = page.locator('[role="dialog"]').first();
  await expect(dialog).toBeVisible();
  const copyName = dialog.getByLabel('Name of your copy', { exact: true });
  await expect(copyName).toHaveValue('plan-copy');
  await expect(dialog.getByText('Your copy will not update with Omnipus. Grant it to agents in their settings.', { exact: true })).toBeVisible();
  await copyName.fill('plan-copy');
  await dialog.getByRole('button', { name: /Create copy/i }).click();

  // 201 committed: the list now shows plan-copy (a user-owned row, which
  // itself carries a delete control).
  const copyRow = page.getByText('plan-copy', { exact: true })
    .locator('xpath=ancestor::div[.//button][1]');
  await expect(copyRow).toBeVisible({ timeout: 10_000 });
  await expect(copyRow.getByText('plan-copy', { exact: true })).toBeVisible();
  await expect(copyRow.getByRole('button', { name: 'Remove plan-copy', exact: true })).toBeVisible();
});
