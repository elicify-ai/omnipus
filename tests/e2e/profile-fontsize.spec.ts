/**
 * profile-fontsize.spec.ts — E2E-5: font-size slider changes the root CSS
 * custom property `--user-font-size`.
 *
 * Covers:
 *   AC1 (US-5): Given the operator changes the font-size slider, Then the
 *     app's base font-size changes (root consumes `var(--user-font-size)`),
 *     bounded min/max, and persists across reload.
 *
 * BDD scenario (Section 6):
 *   Given the operator sets the font-size slider to 18px
 *   When the value is applied
 *   Then `document.documentElement.style.getPropertyValue('--user-font-size')`
 *     is '18px' and persists across reload.
 *
 * Approach: deterministic, no REST stubbing required.
 *   - Auth is provided via storageState (applied globally in playwright.config.ts).
 *   - Navigate to `/#/settings`, click the Profile tab.
 *   - Locate the Radix Slider by role="slider" (one slider on this page).
 *   - Drive value changes via keyboard (ArrowRight/ArrowLeft) — Radix Slider
 *     responds to keyboard; fill() and drag are not reliable for Radix.
 *   - Assert `--user-font-size` on `document.documentElement` (set by the
 *     component's useEffect via `setProperty`).
 *   - Persist test: reload the page, re-open Profile tab, re-read the property.
 *
 * Why we assert `--user-font-size` and not `getComputedStyle(html).fontSize`:
 *   ProfileSection.tsx sets the CSS custom property directly:
 *     document.documentElement.style.setProperty('--user-font-size', `${fontSize}px`)
 *   It does NOT set `html { font-size: ... }` — so getComputedStyle().fontSize
 *   would not reflect slider changes. The custom property is the correct target.
 *
 * Why we use base @playwright/test rather than the console-errors fixture:
 *   The test manipulates localStorage via page.evaluate and drives keyboard
 *   events. No WS or API calls produce novel console output. Using the base
 *   import avoids any fixture interaction with the cancelOnTeardown auto-fixture
 *   (not relevant here — no streaming turn is started).
 *
 * Test dataset for boundary values (from spec):
 *   11 (min-1) → clamps to 12   (ArrowLeft past min stays at min)
 *   12 (min)   → 12px
 *   18          → 18px
 *   20 (max)   → 20px
 *   21 (max+1) → clamps to 20   (ArrowRight past max stays at max)
 *
 * Traces to: E2E-5 (SC-105) — font-size slider changes --user-font-size.
 *
 * CLAUDE.md — "E2E tests always target the embedded SPA (Go binary)"
 */

import { expect, test } from '@playwright/test'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

// ── Helpers ───────────────────────────────────────────────────────────────────

/**
 * Read the current value of `--user-font-size` from the document root.
 * Returns the raw string (e.g. '18px') or empty string if not set.
 */
async function getRootFontSizeVar(page: import('@playwright/test').Page): Promise<string> {
  return page.evaluate(() =>
    document.documentElement.style.getPropertyValue('--user-font-size'),
  )
}

/**
 * Navigate to the Profile screen and wait for it to mount.
 *
 * Pre-merge (pre-v0.1.0-foundation), Profile was a tab inside the Settings screen
 * (/#/settings → "Profile" tab). After the v0.1.0-foundation consolidation (commit
 * d854b02), Profile was promoted to a top-level route at /#/profile (see
 * src/routes/_app/profile.tsx + src/components/screens/ProfileScreen.tsx). Settings
 * tabs no longer include Profile; the sidebar nav links directly to /#/profile.
 */
async function openProfileTab(page: import('@playwright/test').Page): Promise<void> {
  await page.goto(`${BASE_URL}/#/profile`)
  await expect(page).toHaveURL(/\/profile/, { timeout: 10_000 })

  // ProfileSection renders the font-size slider directly (not inside a tab panel
  // anymore). Wait for the slider to be present as the "page mounted" signal.
  await expect(page.getByRole('slider')).toBeVisible({ timeout: 10_000 })
}

/**
 * Return the numeric aria-valuenow of the slider.
 */
async function getSliderValue(page: import('@playwright/test').Page): Promise<number> {
  const raw = await page.getByRole('slider').getAttribute('aria-valuenow')
  return parseInt(raw ?? '0', 10)
}

/**
 * Press ArrowRight or ArrowLeft on the slider `count` times.
 * Each keypress moves the Radix Slider by `step` (1px).
 */
async function pressSlider(
  page: import('@playwright/test').Page,
  direction: 'ArrowRight' | 'ArrowLeft',
  count: number,
): Promise<void> {
  const slider = page.getByRole('slider')
  await slider.focus()
  for (let i = 0; i < count; i++) {
    await slider.press(direction)
  }
}

/**
 * Drive the slider to a target pixel value from its current value using
 * keyboard arrow keys. Assumes the slider step is 1 and range is [12, 20].
 */
async function setSliderTo(
  page: import('@playwright/test').Page,
  target: number,
): Promise<void> {
  const current = await getSliderValue(page)
  const delta = target - current
  if (delta === 0) return
  const direction = delta > 0 ? 'ArrowRight' : 'ArrowLeft'
  await pressSlider(page, direction, Math.abs(delta))
}

// ── E2E-5: slider changes --user-font-size ────────────────────────────────────
// BDD: Given the operator navigates to Profile settings
//      And the font-size slider is visible with aria-valuemin=12, aria-valuemax=20
//      When the operator changes the slider value (e.g. to 18)
//      Then document.documentElement.style.getPropertyValue('--user-font-size')
//        equals '18px'
//      And the value persists after a full page reload.
//
// Traces to: E2E-5 / SC-105

test(
  'font-size slider at default renders --user-font-size on the document root',
  async ({ page }) => {
    // The component sets --user-font-size in a useEffect that fires on mount.
    // This test verifies the initial value is applied without any user interaction.
    await openProfileTab(page)

    // The slider must be present with the correct ARIA range attributes.
    const slider = page.getByRole('slider')
    await expect(slider).toBeVisible({ timeout: 10_000 })
    await expect(slider).toHaveAttribute('aria-valuemin', '12')
    await expect(slider).toHaveAttribute('aria-valuemax', '20')

    // Default value is 14 (ProfileSection.tsx:67 — loadPref('font_size', 14)).
    // Unless localStorage was pre-seeded by a prior test, aria-valuenow is 14.
    // We don't hard-assert the exact default because storageState may carry a
    // value from a prior test run; instead we assert the property IS set.
    const cssVar = await getRootFontSizeVar(page)
    expect(cssVar).toMatch(/^\d+px$/)
  },
)

test(
  'font-size slider changing to 18px sets --user-font-size to 18px',
  async ({ page }) => {
    // Seed localStorage so the slider starts at a known value (14px) regardless
    // of any prior test's localStorage state carried through storageState.
    await page.addInitScript(() => {
      localStorage.setItem('omnipus_pref_font_size', '14')
    })

    await openProfileTab(page)

    const slider = page.getByRole('slider')
    await expect(slider).toBeVisible({ timeout: 10_000 })

    // Drive to 18 from the seeded starting value of 14 (4 × ArrowRight).
    await setSliderTo(page, 18)

    // aria-valuenow reflects the slider's internal value synchronously.
    await expect(slider).toHaveAttribute('aria-valuenow', '18')

    // The useEffect fires on every fontSize state change; --user-font-size is
    // set synchronously within the React render cycle. No extra wait needed.
    const cssVar = await getRootFontSizeVar(page)
    expect(cssVar).toBe('18px')
  },
)

test(
  'font-size slider changing to 14px then 20px sets --user-font-size to 20px',
  async ({ page }) => {
    // Seed at 14 so the delta to 20 is predictable.
    await page.addInitScript(() => {
      localStorage.setItem('omnipus_pref_font_size', '14')
    })

    await openProfileTab(page)

    const slider = page.getByRole('slider')
    await expect(slider).toBeVisible({ timeout: 10_000 })

    // Move to 20 (max) — 6 × ArrowRight from 14.
    await setSliderTo(page, 20)

    await expect(slider).toHaveAttribute('aria-valuenow', '20')

    const cssVar = await getRootFontSizeVar(page)
    expect(cssVar).toBe('20px')
  },
)

test(
  'font-size slider at min (12px) — ArrowLeft clamps at 12, --user-font-size is 12px',
  async ({ page }) => {
    // Seed at 13 so we need only one ArrowLeft to reach 12, then one more to
    // verify clamping (the spec says 11 → clamps to 12).
    await page.addInitScript(() => {
      localStorage.setItem('omnipus_pref_font_size', '13')
    })

    await openProfileTab(page)

    const slider = page.getByRole('slider')
    await expect(slider).toBeVisible({ timeout: 10_000 })

    // Move to 12.
    await setSliderTo(page, 12)
    await expect(slider).toHaveAttribute('aria-valuenow', '12')

    // One more ArrowLeft: Radix clamps at aria-valuemin — value stays 12.
    await pressSlider(page, 'ArrowLeft', 1)
    await expect(slider).toHaveAttribute('aria-valuenow', '12')

    const cssVar = await getRootFontSizeVar(page)
    expect(cssVar).toBe('12px')
  },
)

test(
  'font-size slider at max (20px) — ArrowRight clamps at 20, --user-font-size is 20px',
  async ({ page }) => {
    // Seed at 19 so one ArrowRight reaches 20, then one more to verify clamping.
    await page.addInitScript(() => {
      localStorage.setItem('omnipus_pref_font_size', '19')
    })

    await openProfileTab(page)

    const slider = page.getByRole('slider')
    await expect(slider).toBeVisible({ timeout: 10_000 })

    // Move to 20.
    await setSliderTo(page, 20)
    await expect(slider).toHaveAttribute('aria-valuenow', '20')

    // One more ArrowRight: Radix clamps at aria-valuemax — value stays 20.
    await pressSlider(page, 'ArrowRight', 1)
    await expect(slider).toHaveAttribute('aria-valuenow', '20')

    const cssVar = await getRootFontSizeVar(page)
    expect(cssVar).toBe('20px')
  },
)

test(
  'font-size preference persists across page reload (18px survives reload)',
  async ({ page }) => {
    // Navigate first so we are on the correct origin and can set localStorage
    // via page.evaluate(). We deliberately do NOT use addInitScript here because
    // addInitScript re-runs on every navigation — including page.reload() — which
    // would reset the stored value back to 14 and defeat the persistence assertion.
    await page.goto(`${BASE_URL}/#/profile`)
    await expect(page).toHaveURL(/\/profile/, { timeout: 10_000 })

    // Seed localStorage to 14 for a predictable starting point, then reload so
    // the ProfileSection mounts fresh with the seeded value.
    await page.evaluate(() => localStorage.setItem('omnipus_pref_font_size', '14'))
    await page.reload()
    await expect(page).toHaveURL(/\/profile/, { timeout: 10_000 })

    // Profile is now a top-level route (not a Settings tab) — slider is on the
    // page itself, no tab activation needed.
    await expect(page.getByRole('slider')).toBeVisible({ timeout: 10_000 })

    const slider = page.getByRole('slider')
    await expect(slider).toBeVisible({ timeout: 10_000 })

    // Set slider to 18.
    await setSliderTo(page, 18)
    await expect(slider).toHaveAttribute('aria-valuenow', '18')

    // Verify --user-font-size is set before reload.
    expect(await getRootFontSizeVar(page)).toBe('18px')

    // ── Persistence check: reload and re-open Profile tab.
    // The useAutoSave hook debounces and persists to localStorage. Wait briefly
    // so the debounced save has had time to write before reloading.
    await page.waitForTimeout(600)

    await page.reload()
    await expect(page).toHaveURL(/settings/, { timeout: 10_000 })

    // Re-activate Profile tab after reload — openProfileTab navigates to
    // /#/settings internally, but since we are already there we just click the tab.
    const profileTabAfter = page.locator('button[role="tab"]', { hasText: 'Profile' })
    await expect(profileTabAfter).toBeVisible({ timeout: 10_000 })
    await profileTabAfter.click()
    const profilePanelAfter = page.locator('[role="tabpanel"][data-state="active"]').first()
    await expect(profilePanelAfter).toBeVisible({ timeout: 10_000 })

    const sliderAfterReload = page.getByRole('slider')
    await expect(sliderAfterReload).toBeVisible({ timeout: 10_000 })

    // aria-valuenow must reflect the persisted value.
    await expect(sliderAfterReload).toHaveAttribute('aria-valuenow', '18')

    // The useEffect runs on mount and sets --user-font-size from the loaded value.
    expect(await getRootFontSizeVar(page)).toBe('18px')
  },
)
