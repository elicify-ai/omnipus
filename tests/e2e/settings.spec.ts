import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { expectA11yClean } from './fixtures/a11y';

// Global storageState provides pre-authenticated session (see playwright.config.ts + global-setup.ts).

test.beforeEach(async ({ page }) => {
  // HashRouter: routes live in the fragment, not the pathname.
  await page.goto('/#/settings');
});

test('(a) Providers tab shows a "Connected" badge next to configured provider', async ({
  page,
}) => {
  // Settings uses shadcn Tabs (settings.tsx:31) — TabsTrigger renders button[role="tab"]
  const providersTab = page.locator('button[role="tab"]', { hasText: 'Providers' });
  await expect(providersTab).toBeVisible({ timeout: 10_000 });
  await providersTab.click();

  // ProvidersSection renders a Badge with text "Connected" when connected=true (ProvidersSection.tsx:98-100)
  // Badge has no testid — match by text content
  const connectedBadge = page.locator('body').getByText('Connected').first();
  await expect(connectedBadge).toBeVisible({ timeout: 15_000 });

  await expectA11yClean(page);
});

test('(b) Security tab loads without console errors', async ({ page }) => {
  const securityTab = page.locator('button[role="tab"]', { hasText: 'Security' });
  await expect(securityTab).toBeVisible({ timeout: 10_000 });
  await securityTab.click();

  // Radix Tabs renders ALL tabpanels in the DOM but marks inactive ones with hidden.
  // Use [data-state="active"] to target the currently visible panel.
  const tabPanel = page.locator('[role="tabpanel"][data-state="active"]').first();
  await expect(tabPanel).toBeVisible({ timeout: 10_000 });
});

test('(c) About tab shows build info (version)', async ({ page }) => {
  const aboutTab = page.locator('button[role="tab"]', { hasText: 'About' });
  await expect(aboutTab).toBeVisible({ timeout: 10_000 });
  await aboutTab.click();

  // AboutSection (AboutSection.tsx:86) renders InfoRow with label "Version" and mono value.
  // Assert the word "Version" is present and the section renders system info
  await expect(page.locator('body')).toContainText(/version/i, { timeout: 10_000 });

  // The version value comes from /api/v1/about — wait for it to load
  // It renders as a <dd> or span in InfoRow — match a semver-ish pattern in the active panel
  const tabPanel = page.locator('[role="tabpanel"][data-state="active"]').first();
  await expect(tabPanel).toContainText(/\d+\.\d+/, { timeout: 15_000 });
});

test('(d) all tabs reachable via keyboard navigation (ArrowKeys)', async ({ page }) => {
  // ARIA automatic activation: each ArrowRight focuses and selects the next
  // tab, exposes its associated panel, and wraps from the last tab to the first.
  const tabList = page.locator('[role="tablist"]').first();
  await expect(tabList).toBeVisible({ timeout: 10_000 });
  const tabs = tabList.locator('[role="tab"]');
  const panelIds = await tabs.evaluateAll((elements) =>
    elements.map((element) => element.getAttribute('aria-controls')),
  );
  expect(panelIds.length).toBeGreaterThan(0);
  for (const panelId of panelIds) {
    expect(panelId).toEqual(expect.any(String));
    expect(panelId).not.toBe('');
  }
  expect(new Set(panelIds).size).toBe(panelIds.length);

  const initialActive = tabList.locator('[role="tab"][data-state="active"]').first();
  await expect(initialActive).toBeVisible({ timeout: 5_000 });
  await initialActive.focus();
  await expect(initialActive).toBeFocused();
  const initialAriaControls = await initialActive.getAttribute('aria-controls');
  const initialIndex = panelIds.indexOf(initialAriaControls);
  expect(initialIndex).toBeGreaterThanOrEqual(0);
  await expect(page.locator(`[role="tabpanel"][id=${JSON.stringify(initialAriaControls)}]`))
    .toBeVisible({ timeout: 10_000 });

  // Snapshot the ordered identities before pressing any key. Radix defers
  // focus movement; checking whichever tab is currently focused can succeed
  // on the OLD tab and let subsequent keypresses overtake that movement.
  // Require the exact successor at every step, including the final wrap.
  for (let step = 1; step <= panelIds.length; step++) {
    const expectedPanelId = panelIds[(initialIndex + step) % panelIds.length];
    const expectedTab = tabList.locator(`[role="tab"][aria-controls=${JSON.stringify(expectedPanelId)}]`);
    await page.keyboard.press('ArrowRight');
    await expect(expectedTab).toBeFocused({ timeout: 5_000 });
    await expect(expectedTab).toHaveAttribute('data-state', 'active', { timeout: 5_000 });
    await expect(page.locator(`[role="tabpanel"][id=${JSON.stringify(expectedPanelId)}]`))
      .toBeVisible({ timeout: 5_000 });
  }
});

// (e) retired: 'tool-policy "Always Allow" toggle persists across page reload'.
// Skills & Tools redesign, C-26 point 1 / S-119 / R2-Q1: no approval card offers
// Always Allow any more, and its replacement, a timed allowance, is not a
// setting (C-26 point 3: it is held in memory, ends at its time or when the
// gateway restarts, and has no storage object), so Settings has nothing for a
// page reload to keep. There is no C-26 equivalent of this test, so it is
// deleted (spec b27c64109, Regression Test Requirements, row "Always Allow on
// approval cards": "A case with no C-26 equivalent is deleted, citing R2-Q1").
//
// The old test could not fail on its subject: the debug hook it called,
// window.__omnipus_set_pending_approval, is defined nowhere in the repository,
// so the toggle branch never ran, and the else branch only checked that the
// Security tab was visible. It also never reloaded the page.
//
// Where its subject lives now: a stored permission surviving a reload is
// tests/e2e/skills-tools.spec.ts (S-03 "survives reload"); a timed allowance's
// scope and lifetime are the Go tests pkg/agent TestTimedAllowance_* and
// pkg/gateway TestTimedAllowance_ReachesScheduledRun.
