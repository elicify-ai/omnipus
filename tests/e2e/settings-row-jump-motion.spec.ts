/**
 * settings-row-jump-motion.spec.ts — Settings redesign #1519, FR-075 motion
 * in a real browser (CHECK round 9, R9-02/R9-03; team-lead stop rule).
 *
 * The jsdom oracle (settingsRowJump.rendered.test.tsx) reads the shipped
 * stylesheet's effective cascade because jsdom computes no animation; its
 * written grammar has NAMED limits (CHECK round 9, N121–N128, stated in the
 * reader's own comment). This spec is the complement the stop rule names:
 * the SAME FR-075 claims, proven by the browser's own animation objects —
 * element.getAnimations() in real Chromium — so no hand-written CSS reader
 * stands between the sheet and the claim.
 *
 * FR-075 (spec docs/internal/specs/settings-redesign-spec.md :2001): the row
 * a person reaches from a Settings search result or deep link flashes once
 * on normal motion; under prefers-reduced-motion: reduce there is no
 * animation and the row is still scrolled into view and marked. Expected
 * values come from FR-075, never from the sheet: exactly ONE animation on
 * the marked row, iterations === 1 (a flash, not a glow), duration > 0, and
 * a playState of running or finished (it must actually play); under reduced
 * motion zero animations with the mark and the scroll still in place.
 *
 * Rows driven: one setting OUTSIDE any group (Gateway → Network: Port) and
 * one INSIDE a group that starts closed (Limits → Model overrides — D-9 row
 * 2, "collapsed, S-Q12 A"; FR-075 requires the jump to open that group
 * first), so both reachability shapes carry the motion proof.
 *
 * Harness: the standard e2e harness — real gateway at OMNIPUS_URL
 * (default http://localhost:6060), admin storage state, global-setup
 * onboarding (see tests/e2e/global-setup.ts). Paid models: none — these
 * tests read screens and animations only.
 */

import { expect, test, type Page } from '@playwright/test'

const SETTINGS = '/#/settings'

async function openSettings(page: Page, search = '') {
  await page.goto(`${SETTINGS}${search}`)
  await expect(page.getByRole('heading', { name: 'Settings', level: 1 })).toBeVisible()
}

/**
 * The real search jump of FR-075's first arm: type the setting's name, click
 * its result, land on the section with the row marked. Returns the row once
 * the mark exists, read promptly — the flash lives about 1.2 s, so the
 * animation sample below is taken while it is still alive.
 */
async function jumpToRow(page: Page, query: string, sectionHeading: string, rowId: string) {
  await openSettings(page)
  await page.getByTestId('settings-search').fill(query)
  await page.getByTestId('settings-search-results').getByRole('link').first().click()
  await expect(page.getByRole('heading', { name: sectionHeading, level: 2 })).toBeVisible({ timeout: 10_000 })
  const row = page.locator(`[data-settings-row="${rowId}"]`)
  await expect(row, `FR-075: the reached ${rowId} row must exist in the opened section`).toBeVisible()
  await page.waitForFunction(
    (id) => document.querySelector(`[data-settings-row="${id}"]`)?.hasAttribute('data-settings-row-target'),
    rowId,
    { timeout: 10_000 },
  )
  return row
}

/** The marked row's animation facts, read from the browser's own objects. */
interface FlashSample {
  playState: string
  iterations: number
  durationMs: number
}

async function readFlash(row: ReturnType<Page['locator']>): Promise<FlashSample[]> {
  return row.evaluate((el) =>
    el.getAnimations().map((animation) => {
      const timing = animation.effect?.getComputedTiming()
      const duration = timing?.duration
      return {
        playState: animation.playState,
        iterations: timing?.iterations ?? 0,
        durationMs: typeof duration === 'number' ? duration : 0,
      }
    }),
  )
}

test.describe('FR-075 motion — the search-reached row flash, in real Chromium', () => {
  test('normal motion: one flash animation, once, non-zero — Port (outside any group) and Model overrides (inside a closed group)', async ({ page }) => {
    for (const [query, section, rowId] of [
      ['port', 'Gateway', 'port'],
      ['model overrides', 'Limits', 'model-overrides'],
    ] as const) {
      const row = await jumpToRow(page, query, section, rowId)
      if (rowId === 'model-overrides') {
        // R10-02: the section heading is Limits; the grouped-row claim is its
        // own FR-075 leg — the Model overrides GROUP (LimitsSection's
        // `overrides` group) must be OPEN, asserted on its disclosure control
        // by role and name. "Context and tool results" is a sibling group the
        // jump need not open; nothing is asserted on it.
        await expect(
          page.getByRole('button', { name: 'Model overrides' }),
          'FR-075: a row inside a closed group — the jump must open that group first',
        ).toHaveAttribute('aria-expanded', 'true')
      }
      await expect(row,
        `FR-075: the reached ${rowId} row must be marked to assistive tech`).toHaveAttribute('aria-current', 'true')
      const flashes = await readFlash(row)
      expect(flashes,
        `FR-075: exactly one highlight animation on the reached ${rowId} row (a flash, not a layered or absent glow)`).toHaveLength(1)
      expect(flashes[0].iterations,
        `FR-075: the ${rowId} flash runs ONCE — a persistent or looping glow is not the flash the spec names`).toBe(1)
      expect(flashes[0].durationMs,
        `FR-075: the ${rowId} flash must be visible — its duration is greater than zero`).toBeGreaterThan(0)
      expect(['running', 'finished'],
        `FR-075: the ${rowId} flash must actually play`).toContain(flashes[0].playState)
    }
  })

  test('reduced motion: no animation on the reached row, still scrolled into view and marked — both rows', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    for (const [query, section, rowId] of [
      ['port', 'Gateway', 'port'],
      ['model overrides', 'Limits', 'model-overrides'],
    ] as const) {
      const row = await jumpToRow(page, query, section, rowId)
      if (rowId === 'model-overrides') {
        // R10-02: same grouped-row leg as the normal-motion arm — the Model
        // overrides group's own disclosure must be open; the sibling
        // "Context and tool results" group is not asserted.
        await expect(
          page.getByRole('button', { name: 'Model overrides' }),
          'FR-075: a row inside a closed group — the jump must open that group first',
        ).toHaveAttribute('aria-expanded', 'true')
      }
      const count = await row.evaluate((el) => el.getAnimations().length)
      expect(count,
        `FR-075: under prefers-reduced-motion: reduce the reached ${rowId} row must not animate`).toBe(0)
      await expect(row,
        `FR-075: under reduced motion the reached ${rowId} row is still MARKED`).toHaveAttribute('data-settings-row-target')
      await expect(row,
        `FR-075: the mark must reach assistive tech (aria-current)`).toHaveAttribute('aria-current', 'true')
      await expect(row,
        `FR-075: under reduced motion the reached ${rowId} row is still scrolled into view`).toBeInViewport()
    }
  })
})
