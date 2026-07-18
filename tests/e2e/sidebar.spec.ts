import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { expectA11yClean } from './fixtures/a11y';

// Global storageState provides pre-authenticated session (see playwright.config.ts + global-setup.ts).

// HashRouter: TanStack Router generates href="/#/<path>" links (not href="/<path>").
// Workspace-as-project IA: the top-level Chat (/#/) and Monitor (/#/monitor)
// nav items were removed and Connectors replaced the old Channels item. The
// main navigation's fixed-bottom "Library" group (Sidebar.tsx LIBRARY_ITEMS)
// now exposes only Agents · Skills & Tools · Connectors as direct links.
//
// Settings is NOT one of them: it moved into the "Open user menu" profile
// dropdown at the very bottom of the sidebar (Sidebar.tsx: DropdownMenuItem
// asChild > Link to="/settings"). That dropdown's content is rendered via a
// Radix Portal (dropdown-menu.tsx's DropdownMenuContent wraps
// DropdownMenuPrimitive.Portal), so it is NOT a DOM descendant of
// `nav[aria-label="Main navigation"]` even though it is opened from a button
// inside that nav — a `nav ... a[href="/#/settings"]` selector can never
// match it. It has to be reached by opening the profile menu instead.
const LIBRARY_NAV_ITEMS = [
  { href: '/#/agents', urlPattern: /agents/ },
  { href: '/#/connectors', urlPattern: /connectors/ },
  { href: '/#/skills', urlPattern: /skills/ },
] as const;

test.beforeEach(async ({ page }) => {
  await page.goto('/');
});

test('(a) every nav item routes correctly', async ({ page }) => {
  const hamburger = page.locator('#sidebar-hamburger');

  for (const item of LIBRARY_NAV_ITEMS) {
    // Open (or re-open) sidebar before each nav click — overlay closes on navigation
    await expect(hamburger).toBeVisible({ timeout: 10_000 });
    await hamburger.click();

    // Wait for nav to appear and the target link to be visible
    const nav = page.locator('nav[aria-label="Main navigation"]');
    await expect(nav).toBeVisible({ timeout: 5_000 });

    // HashRouter: links have href="/#/<path>"
    const link = nav.locator(`a[href="${item.href}"]`).first();
    await expect(link).toBeVisible({ timeout: 5_000 });
    await link.click();
    await expect(page).toHaveURL(item.urlPattern, { timeout: 10_000 });
  }

  // Settings: open the sidebar, open the profile dropdown
  // (data-testid="sidebar-profile-trigger"), then click its portaled
  // "Settings" menu item.
  await expect(hamburger).toBeVisible({ timeout: 10_000 });
  await hamburger.click();
  const nav = page.locator('nav[aria-label="Main navigation"]');
  await expect(nav).toBeVisible({ timeout: 5_000 });

  const profileTrigger = page.locator('[data-testid="sidebar-profile-trigger"]');
  await expect(profileTrigger).toBeVisible({ timeout: 5_000 });
  await profileTrigger.click();

  const settingsItem = page.getByRole('menuitem', { name: 'Settings' });
  await expect(settingsItem).toBeVisible({ timeout: 5_000 });
  await settingsItem.click();
  await expect(page).toHaveURL(/settings/, { timeout: 10_000 });

  await expectA11yClean(page);
});

test('(b) pinning sidebar persists across reload', async ({ page }) => {
  // Open the sidebar first
  const hamburger = page.locator('#sidebar-hamburger');
  await expect(hamburger).toBeVisible({ timeout: 10_000 });
  await hamburger.click();

  // Use .first() to handle the case where both overlay + aside nav exist during transition
  const nav = page.locator('[aria-label="Main navigation"]').first();
  await expect(nav).toBeVisible({ timeout: 5_000 });

  // Pin toggle button: aria-pressed attribute (Sidebar.tsx:185)
  // title is "Pin sidebar" when not pinned, "Unpin sidebar" when pinned
  const pinBtn = page.locator('button[aria-pressed][title="Pin sidebar"]');
  await expect(pinBtn).toBeVisible({ timeout: 8_000 });
  await pinBtn.click();

  // After pinning, the nav/aside should remain visible
  await expect(nav).toBeVisible({ timeout: 5_000 });

  await page.reload();
  await page.waitForLoadState('networkidle');

  // After reload, if the sidebar is pinned it is rendered as an <aside> element.
  // Use a broader selector that matches both nav and aside pinned states.
  const pinnedSidebar = page.locator('[aria-label="Main navigation"]').first();
  await expect(pinnedSidebar).toBeVisible({ timeout: 10_000 });
});
