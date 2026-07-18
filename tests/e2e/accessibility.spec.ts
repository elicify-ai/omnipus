import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { expectA11yClean } from './fixtures/a11y';

// Global storageState provides pre-authenticated session (see playwright.config.ts + global-setup.ts).

// Valid SPA routes — HashRouter: all routes use the fragment (/#/<path>).
// The Go gateway serves the SPA HTML for all non-API paths, so the HTTP response
// is always 200. For hash-only navigation (same-document), page.goto() returns null
// because no HTTP request is made. We navigate to the full hash URL and verify that
// the fragment is reflected in page.url() instead.
const ROUTES = [
  '/#/',
  '/#/agents',
  '/#/skills',
  '/#/settings',
  '/#/settings?tab=about',
];

test('all major routes pass axe serious/critical accessibility checks', async ({ page }) => {
  for (const route of ROUTES) {
    await page.goto(route, { waitUntil: 'networkidle' });

    // Hash navigation may return null response (no HTTP request if only fragment changes).
    // Instead verify that the page URL reflects the navigated route, proving the SPA rendered it.
    const currentUrl = page.url();
    expect(
      currentUrl,
      `After navigating to ${route}, URL was "${currentUrl}" — route may not have loaded`,
    ).toContain(route.replace(/\/$/, '')); // strip trailing slash for root match

    await expectA11yClean(page);
  }
});
