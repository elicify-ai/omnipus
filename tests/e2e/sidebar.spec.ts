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
  // A fresh visit is not pinned: the sidebar starts closed and the header
  // shows the hamburger (Show sidebar). Show opens it as an overlay; choosing
  // a destination closes the overlay again, so each leg re-opens it. The
  // hamburger is only on screen while the sidebar is off screen.
  const showSidebar = page.getByRole('button', { name: 'Show sidebar' });
  const nav = page.locator('nav[aria-label="Main navigation"]');
  await expect(showSidebar).toBeVisible({ timeout: 10_000 });

  for (const item of LIBRARY_NAV_ITEMS) {
    await showSidebar.click();
    await expect(nav).toBeVisible({ timeout: 5_000 });
    // HashRouter: links have href="/#/<path>"
    const link = nav.locator(`a[href="${item.href}"]`).first();
    await expect(link).toBeVisible({ timeout: 5_000 });
    await link.click();
    await expect(page).toHaveURL(item.urlPattern, { timeout: 10_000 });
  }

  // Settings: the sidebar's profile dropdown, then its portaled
  // "Settings" menu item.
  await showSidebar.click();
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

test('(c) D8: Settings navigated to via the profile menu is clickable on the FIRST click', async ({ page }) => {
  // D8 regression. Root cause: the profile dropdown's Usage/Profile/Settings
  // items never called the sidebar store's close() — unlike every other nav
  // link (Library items, workspace rows, the dropdown's own Notifications
  // item). The dropdown menu itself always closes on select (Radix), but the
  // Sidebar's OWN `isOpen` state stayed true, so the full-viewport
  // `aria-hidden` backdrop (Sidebar.tsx, z-30, absolute inset-0, no
  // background dimming — so it's invisible) kept rendering over the newly
  // navigated Settings page. The backdrop's onClick is `close()`, so the
  // FIRST click anywhere on the new page — including directly on a settings
  // tab trigger — hit the backdrop instead of the tab, silently closing the
  // (already-hidden) backdrop and doing nothing else. Only the SECOND click
  // actually reached the tab.
  // Narrow window: below 1024px a pin is ignored and a fresh visit is not
  // pinned, so the sidebar starts closed and Show sidebar (the hamburger)
  // opens the overlay — the path whose backdrop used to swallow the first
  // click on the next page.
  await page.setViewportSize({ width: 800, height: 720 });
  await page.reload();
  const showSidebar = page.getByRole('button', { name: 'Show sidebar' });
  await expect(showSidebar).toBeVisible({ timeout: 10_000 });
  await showSidebar.click();

  const nav = page.locator('nav[aria-label="Main navigation"]');
  await expect(nav).toBeVisible({ timeout: 5_000 });

  const profileTrigger = page.locator('[data-testid="sidebar-profile-trigger"]');
  await expect(profileTrigger).toBeVisible({ timeout: 5_000 });
  await profileTrigger.click();

  const settingsItem = page.getByRole('menuitem', { name: 'Settings' });
  await expect(settingsItem).toBeVisible({ timeout: 5_000 });
  await settingsItem.click();
  await expect(page).toHaveURL(/settings/, { timeout: 10_000 });

  // Settings defaults to the "Providers" tab. Click "Integrations" exactly
  // ONCE and assert it activates immediately — on the unfixed code this first
  // click is swallowed by the stale backdrop and the tab stays on Providers.
  const integrationsTab = page.getByTestId('settings-tab-integrations');
  await expect(integrationsTab).toBeVisible({ timeout: 10_000 });
  await integrationsTab.click();
  await expect(integrationsTab).toHaveAttribute('aria-selected', 'true', { timeout: 2_000 });

  const providersTab = page.getByTestId('settings-tab-providers');
  await expect(providersTab).toHaveAttribute('aria-selected', 'false');

  await expectA11yClean(page);
});

test('(b) pinning persists across reload; a fresh visit stays closed until shown', async ({ page }) => {
  // Fresh profile, no saved pin: the sidebar is closed and the header shows
  // the hamburger (Show sidebar).
  const nav = page.locator('[aria-label="Main navigation"]');
  await expect(page.getByRole('button', { name: 'Show sidebar' })).toBeVisible({ timeout: 10_000 });
  await expect(nav).toHaveCount(0);

  // Opening it does not leave Show sidebar on screen — the hamburger is
  // hidden while the sidebar is open, and opening never pins.
  const showSidebar = page.getByRole('button', { name: 'Show sidebar' });
  await showSidebar.click();
  await expect(nav.first()).toBeVisible({ timeout: 5_000 });
  await expect(showSidebar).toHaveCount(0);

  // A click outside closes it again and brings the hamburger back.
  await page.mouse.click(900, 300);
  await expect(nav).toHaveCount(0);
  await expect(showSidebar).toBeVisible({ timeout: 5_000 });

  // The persistence check: pin from inside the sidebar, reload, and the pin
  // survives — the sidebar is on screen with no Show sidebar anywhere.
  await showSidebar.click();
  await expect(nav.first()).toBeVisible({ timeout: 5_000 });
  await page.getByRole('button', { name: 'Pin sidebar' }).click();
  await expect(page.getByRole('button', { name: 'Unpin sidebar' })).toBeVisible({ timeout: 5_000 });

  await page.reload();
  await page.waitForLoadState('networkidle');
  await expect(nav.first()).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole('button', { name: 'Show sidebar' })).toHaveCount(0);
});
