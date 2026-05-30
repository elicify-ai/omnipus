import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';

// Global storageState provides pre-authenticated session (see playwright.config.ts + global-setup.ts).

test('mock stale build hash triggers "New version available" toast', async ({ page }) => {
  // Step 1: Intercept the FIRST /api/v1/version call to return sha=v1,
  // subsequent calls return sha=v2-new to simulate a deployment.
  let callCount = 0;
  await page.route('**/api/v1/version', async (route) => {
    callCount++;
    const sha = callCount === 1 ? 'sha-v1-old' : 'sha-v2-new';
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ version: '0.1.0', build_sha: sha }),
    });
  });

  await page.goto('/');
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 });

  // Wait a bit for initial version fetch to complete
  await page.waitForTimeout(500);

  // Step 2: Trigger a focus event to cause a re-fetch (which will return sha-v2-new)
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));

  // Step 3: Toast must appear
  const toast = page.getByTestId('version-toast');
  await expect(toast).toBeVisible({ timeout: 10_000 });
  await expect(toast).toContainText(/new version/i);
});
