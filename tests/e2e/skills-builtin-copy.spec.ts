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
  // The row label is the display name ("Plan", from metadata.display_name; the
  // slug is `plan`), so the row is found through its stable control, the
  // "Make a copy of Plan" button, and the card that holds it.
  const makeCopy = page.getByRole('button', { name: 'Make a copy of Plan', exact: true });
  await expect(makeCopy).toBeVisible({ timeout: 10_000 });
  const planRow = makeCopy.locator('xpath=ancestor::div[contains(@class,"items-start")][1]');
  await expect(planRow.getByText('Plan', { exact: true })).toBeVisible();
  await expect(planRow.getByText('Built-in · read-only', { exact: true })).toBeVisible();
  await expect(planRow.getByRole('button', { name: /^Remove / })).toHaveCount(0);
  await makeCopy.click();

  // The copy dialog: a name field and Create copy.
  const dialog = page.locator('[role="dialog"]').first();
  await expect(dialog).toBeVisible();
  const copyName = dialog.getByLabel('Name of your copy', { exact: true });
  await expect(copyName).toHaveValue('plan-copy');
  await expect(dialog.getByText('Your copy will not update with Omnipus. Grant it to agents in their settings.', { exact: true })).toBeVisible();
  await copyName.fill('plan-copy');
  await dialog.getByRole('button', { name: /Create copy/i }).click();

  // 201 committed: the list now shows the copy (a user-owned row, which
  // itself carries a delete control). The copy's label is its own name
  // (plan-copy) or, when the loader falls back to the document heading, "Plan"
  // (F-2: the copy no longer carries the built-in's display_name), so the row
  // is found by what only a user-owned row has: a Remove control.
  const removeCopy = page.getByRole('button', { name: /^Remove (plan-copy|Plan)$/ });
  await expect(removeCopy).toHaveCount(1, { timeout: 10_000 });
  await expect(removeCopy).toBeVisible();
  const copyRow = removeCopy.locator('xpath=ancestor::div[contains(@class,"items-start")][1]');
  await expect(copyRow.getByText(/^(plan-copy|Plan)$/)).toBeVisible();
  await expect(copyRow.getByText('Built-in · read-only', { exact: true })).toHaveCount(0);
});
