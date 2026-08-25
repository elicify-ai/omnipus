import { expect, test as baseTest } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { expectA11yClean } from './fixtures/a11y';
import { focusRingContrast, type FocusRingSample } from './fixtures/contrast';
import { popularTiles, stubOnboarding, stubProviderSignIn } from './fixtures/onboarding-stubs';

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

// ─────────────────────────────────────────────────────────────────────────────
// ADR-068 FR-041 / SC-005 / SC-007 — the named non-axe accessibility rows.
//
// The spec is explicit that axe alone does not discharge these (MAJ-012): axe
// has NO rule for focus-ring contrast (2.4.11) and its target-size rule is
// best-practice only, so each row below carries its OWN assertion. They run on
// onboarding step 3 — the one state on this branch that renders the shared
// ProviderPicker, its second-level ProviderDetailPanel and the Custom endpoint
// row together.
//
// NOT YET WRITABLE, and deliberately absent rather than faked (see the task's
// dependency list — T068-25/26/27 have not landed on this branch):
//   • axe on "Settings → Providers with the sheet AND the remove dialog open";
//   • `document.activeElement` === Cancel when the remove dialog opens;
//   • Esc closes that dialog with no DELETE request observed;
//   • `document.activeElement` stays inside the sheet after the "Discard key?"
//     Esc prompt;
//   • Escape closes the picker (the picker is INLINE on onboarding — it has no
//     dismiss affordance there; the row belongs to the Settings sheet).
// Each needs the rebuilt Settings → Providers screen (T068-25), the remove
// dialog (T068-26) and the dirty-key sheet (T068-25) to exist first. Adding
// them against components that do not exist would be a test that passes by
// finding nothing, which is the exact false-green this repo documents.
// ─────────────────────────────────────────────────────────────────────────────

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060';

/** The Popular band in catalog order — the keyboard row's expectation source. */
const POPULAR_TILES = popularTiles();
/** The FIRST Popular tile, used wherever "some tile" is enough. */
const FIRST_TILE = POPULAR_TILES[0];

/** Walk onboarding to step 3, where the shared picker renders. */
async function openOnboardingPicker(page: import('@playwright/test').Page): Promise<void> {
  await page.goto(`${BASE_URL}/#/onboarding`);
  await expect(page.getByText('Step 1 of 3').first()).toBeVisible({ timeout: 15_000 });
  await page.locator('#admin-username').fill('a11y-admin');
  await page.getByRole('button', { name: /^continue$/i }).click();
  await expect(page.getByText('Step 2 of 3').first()).toBeVisible();
  await page.locator('#admin-password').fill('a11y-passw0rd!');
  await page.locator('#admin-password-confirm').fill('a11y-passw0rd!');
  await page.getByRole('button', { name: /^continue$/i }).click();
  await expect(page.getByText('Step 3 of 3').first()).toBeVisible();
  await expect(page.getByTestId('picker-popular')).toBeVisible();
}

// ── axe: onboarding step 3, picker and second-level panel ────────────────────

baseTest(
  'axe reports 0 serious/critical on onboarding step 3 — picker, expanded list and detail panel',
  async ({ page }) => {
    await stubOnboarding(page);
    await openOnboardingPicker(page);

    // (a) the picker as it first renders.
    await expectA11yClean(page);

    // (b) with the full virtualised list expanded — this is where the
    // listbox/option/aria-setsize semantics live.
    await page.getByTestId('picker-all-toggle').click();
    await expect(page.getByTestId('picker-virtual-viewport')).toBeVisible();
    await expectA11yClean(page);

    // (c) with the second-level panel open — plan/region groups and the
    // auth-method segmented control (role=group + aria-pressed).
    await page.getByTestId('picker-all-toggle').click();
    await page.getByTestId(`picker-popular-${FIRST_TILE.id}`).click();
    await expect(page.getByTestId('provider-detail-panel')).toBeVisible();
    await expectA11yClean(page);
  },
);

// ── SC-007: target size ≥ 24 × 24 CSS px on a FIXED selector list ────────────

baseTest('every picker and panel target measures at least 24x24 CSS px', async ({ page }) => {
  await stubOnboarding(page);
  await openOnboardingPicker(page);
  await page.getByTestId('picker-all-toggle').click();
  await expect(page.getByTestId('picker-virtual-viewport')).toBeVisible();

  // A FIXED list, per the spec. Each selector must match at least one element:
  // a selector that matches nothing would otherwise "pass" by measuring nothing.
  const PICKER_TARGETS = [
    '[data-testid^="picker-popular-"]',
    '[data-testid="picker-all-toggle"]',
    '[data-testid="picker-custom-endpoint"]',
    '[data-testid="picker-virtual-viewport"] [role="option"]',
  ];

  for (const selector of PICKER_TARGETS) {
    const boxes = await page.locator(selector).evaluateAll((els) =>
      els.map((el) => {
        const r = el.getBoundingClientRect();
        return { w: r.width, h: r.height, testid: el.getAttribute('data-testid') ?? '' };
      }),
    );
    expect(boxes.length, `selector matched nothing: ${selector}`).toBeGreaterThan(0);
    for (const box of boxes) {
      expect(box.w, `${selector} (${box.testid}) width`).toBeGreaterThanOrEqual(24);
      expect(box.h, `${selector} (${box.testid}) height`).toBeGreaterThanOrEqual(24);
    }
  }

  // The second-level panel's own controls: every button and every input in it.
  await page.getByTestId('picker-all-toggle').click();
  await page.getByTestId(`picker-popular-${FIRST_TILE.id}`).click();
  const panel = page.getByTestId('provider-detail-panel');
  await expect(panel).toBeVisible();

  const panelBoxes = await panel.locator('button, input, select').evaluateAll((els) =>
    els.map((el) => {
      const r = el.getBoundingClientRect();
      return { w: r.width, h: r.height, testid: el.getAttribute('data-testid') ?? el.tagName };
    }),
  );
  expect(panelBoxes.length, 'the detail panel rendered no controls').toBeGreaterThan(0);
  for (const box of panelBoxes) {
    expect(box.w, `panel control ${box.testid} width`).toBeGreaterThanOrEqual(24);
    expect(box.h, `panel control ${box.testid} height`).toBeGreaterThanOrEqual(24);
  }
});

// ── 2.4.11: focus ring contrast ≥ 3:1 (axe has no such rule) ─────────────────

baseTest('the focus ring contrasts at least 3:1 against the surface behind it', async ({ page }) => {
  await stubOnboarding(page);
  await openOnboardingPicker(page);
  await page.getByTestId('picker-all-toggle').click();
  await expect(page.getByTestId('picker-virtual-viewport')).toBeVisible();

  // Chromium only matches :focus-visible for a scripted .focus() when the last
  // interaction was a keyboard one — which is exactly how the picker moves
  // focus. One real key press puts the page in that mode first, so the ring we
  // measure is the ring a keyboard user actually sees.
  await page.getByTestId('picker-search').press('Tab');

  const FOCUS_TARGETS = [
    '[data-testid="picker-all-toggle"]',
    '[data-testid="picker-custom-endpoint"]',
    '[data-testid="picker-search"]',
    `[data-testid="picker-popular-${FIRST_TILE.id}"]`,
  ];

  const samples: FocusRingSample[] = await page.evaluate((selectors) => {
    const opaqueBackground = (start: Element): string => {
      let node: Element | null = start;
      while (node) {
        const bg = getComputedStyle(node).backgroundColor;
        const m = /^rgba?\(\s*[\d.]+[,\s]+[\d.]+[,\s]+[\d.]+(?:[,/\s]+([\d.]+))?\s*\)$/.exec(bg);
        if (m && (m[1] === undefined || Number(m[1]) > 0)) return bg;
        node = node.parentElement;
      }
      return getComputedStyle(document.body).backgroundColor;
    };
    return selectors.map((selector) => {
      const el = document.querySelector(selector) as HTMLElement | null;
      if (!el) {
        return {
          selector,
          outlineColor: 'MISSING',
          outlineStyle: 'MISSING',
          outlineWidth: '0px',
          backgroundColor: 'MISSING',
        };
      }
      el.focus();
      const style = getComputedStyle(el);
      return {
        selector,
        outlineColor: style.outlineColor,
        outlineStyle: style.outlineStyle,
        outlineWidth: style.outlineWidth,
        backgroundColor: opaqueBackground(el),
      };
    });
  }, FOCUS_TARGETS);

  expect(samples.length).toBe(FOCUS_TARGETS.length);
  for (const sample of samples) {
    expect(sample.outlineStyle, `${sample.selector} draws no focus outline`).not.toBe('none');
    expect(
      parseFloat(sample.outlineWidth),
      `${sample.selector} focus outline width`,
    ).toBeGreaterThanOrEqual(1);
    const ratio = focusRingContrast(sample);
    expect(ratio, `${sample.selector} focus ring colour is unmeasurable`).not.toBeNull();
    expect(
      ratio as number,
      `${sample.selector} focus ring contrast (${sample.outlineColor} on ${sample.backgroundColor})`,
    ).toBeGreaterThanOrEqual(3);
  }
});

// ── 2.1.1: keyboard-only operation of the picker (FR-026) ───────────────────

baseTest('the picker is fully operable from the keyboard — ArrowDown, Enter, End, Home', async ({
  page,
}) => {
  await stubOnboarding(page);
  await openOnboardingPicker(page);

  const search = page.getByTestId('picker-search');
  await search.click();

  const activeTestId = () =>
    page.evaluate(() => document.activeElement?.getAttribute('data-testid') ?? null);

  // ArrowDown x3 lands on the THIRD Popular tile (the sequence starts at the
  // first tile, so three presses move -1 → 0 → 1 → 2).
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('ArrowDown');
  const thirdTile = POPULAR_TILES[2];
  await expect.poll(activeTestId).toBe(`picker-popular-${thirdTile.id}`);

  // Enter activates it — onboarding opens the second-level panel for THAT
  // company, and never for a neighbouring tile.
  await page.keyboard.press('Enter');
  const panel = page.getByTestId('provider-detail-panel');
  await expect(panel).toBeVisible();
  await expect(panel).toHaveAttribute('aria-label', `Configure ${thirdTile.company}`);

  // Back to the picker; End reaches Custom endpoint (the last row in the flat
  // sequence), Home returns to the first tile.
  await page.getByTestId('provider-detail-panel-cancel').click();
  await expect(page.getByTestId('picker-popular')).toBeVisible();
  await page.getByTestId('picker-search').click();

  await page.keyboard.press('End');
  await expect.poll(activeTestId).toBe('picker-custom-endpoint');

  await page.keyboard.press('Home');
  await expect.poll(activeTestId).toBe(`picker-popular-${FIRST_TILE.id}`);
});

// ── SC-005: the virtualised list renders a bounded number of options ────────

baseTest('the expanded provider list renders at most floor(height/40) + 10 options', async ({
  page,
}) => {
  await stubOnboarding(page);
  await openOnboardingPicker(page);
  await page.getByTestId('picker-all-toggle').click();

  const viewport = page.getByTestId('picker-virtual-viewport');
  await expect(viewport).toBeVisible();

  // The bound is computed from the REAL geometry, not from a copy of the
  // component's constants — a viewport that grows must widen the bound, and a
  // virtualiser that stops virtualising must still fail.
  const { height, options } = await viewport.evaluate((el) => ({
    height: el.clientHeight,
    options: el.querySelectorAll('[role="option"]').length,
  }));

  const bound = Math.floor(height / 40) + 10;
  expect(height, 'the virtual viewport has no height').toBeGreaterThan(0);
  expect(
    options,
    `rendered ${options} options in a ${height}px viewport (bound ${bound})`,
  ).toBeLessThanOrEqual(bound);
  // Differentiation: a list that rendered nothing would also satisfy the bound.
  expect(options, 'the expanded list rendered no options at all').toBeGreaterThan(0);
});

// ── ADR-068 §8b / FR-045 — the shared sign-in dialog (T068-33) ──────────────
//
// The dialog is the only surface on this branch that renders a modal over the
// picker, so its axe row is separate from the step-3 row above. FR-045 names
// three constraints axe cannot check on its own: focus must move INTO the
// dialog, Escape must close it, and the status line must be a polite live
// region — each has its own assertion here.

baseTest('axe is clean on the sign-in dialog, focus enters it, and Escape closes it', async ({
  page,
}) => {
  await stubOnboarding(page);
  const signIn = await stubProviderSignIn(page);
  await openOnboardingPicker(page);

  // `openai-chatgpt` is a standard-tier row — reachable through search.
  await page.getByTestId('picker-search').fill('chatgpt');
  await page.getByTestId('picker-row-ChatGPT').click();
  await expect(page.getByTestId('provider-detail-panel')).toBeVisible();
  await page.getByTestId('provider-detail-panel-auth-signin-start').click();

  const dialog = page.getByTestId('sign-in-dialog');
  await expect(dialog).toBeVisible();
  await expect(page.getByTestId('user-code')).toHaveText(/WDJB-MJHT/);
  expect(signIn.starts, 'the dialog did not start a sign-in').toContain('openai-chatgpt');

  await expectA11yClean(page);

  // FR-045: the verification link opens a new tab and is `rel="noopener"`.
  const link = page.getByTestId('verification-link');
  await expect(link).toHaveAttribute('target', '_blank');
  await expect(link).toHaveAttribute('rel', 'noopener');

  // FR-045: the status line is a polite live region.
  await expect(page.getByTestId('sign-in-status')).toHaveAttribute('aria-live', 'polite');

  // FR-045: focus is inside the dialog, not left behind on the picker.
  const focusInside = await page.evaluate(() => {
    const el = document.querySelector('[data-testid="sign-in-dialog"]');
    return !!el && !!document.activeElement && el.contains(document.activeElement);
  });
  expect(focusInside, 'focus stayed outside the sign-in dialog').toBe(true);

  // FR-045: Escape closes it, and the picker underneath is still there.
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(page.getByTestId('provider-detail-panel')).toBeVisible();
});
