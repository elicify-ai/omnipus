/**
 * settings-redesign.spec.ts — Settings redesign #1519, RED.
 *
 * Test Implementation Order row 29: E2E for the hands-on rows marked
 * "stable" that can run against a real gateway with the declared seams only.
 * Targeted file only (spec, Test Implementation Order row 29).
 *
 * Rows covered here (spec "Hands-on UAT rows"):
 *   - G-01 (US-1 AS1, AS2): 11 sections in 3 groups, no Sign-in; the open
 *     section survives a reload.
 *   - G-02 (US-1 AS3–AS7, spot-check incl. devices): D-3 links land per D-3.
 *   - G-03 (US-1 AS8–AS10): "web search" → moved to Skills & Tools; "zzqx"
 *     → "No settings match".
 *   - G-04 (US-1 AS9, US-9 AS5; FR-008, FR-070): at 390 px the list shows
 *     first, a section opens alone with the Settings back button, no
 *     sideways scroll, controls ≥ 44 px (3 sections spot-check).
 *   - G-05 (US-2 AS1–AS5, Port 80 only): the bar counts follow the values;
 *     80 blocks Save with the range text; a valid port saves.
 *   - G-07(a) (US-2 AS7): the DECLARED seam — Playwright route interception
 *     of PUT /settings/context answering 400 (the spec names this seam),
 *     method-scoped to the write only (CHECK F11) — real group expansion,
 *     the real password prompt answered, exactly the two write outcomes
 *     (Tries per goal saved server-side, Default context window keeps its
 *     draft with the seam's message under it, bar "1 change could not be
 *     saved") and exactly one write per endpoint.
 *     G-07(b): the real server's cli_path_invalid refusal under the Codex
 *     path (no seam). G-07(c): the screen refuses a 0 context length itself
 *     and sends nothing.
 *   - FR-068 / FR-069 / FR-065 / FR-075 / FR-063(new-address leg) / FR-109:
 *     the browser-mapped rows CHECK F11 found missing (skip link, stacked
 *     Discard-key confirm, loading state, reduced motion, restarted
 *     reconciliation on the new address, agent-overlay follow-link). The
 *     full G-10 direct/proxy restart journey stays the hands-on row.
 *   - G-13 (US-5 AS1–AS4): defaults 90/180/off; 30/365 persist after a
 *     reload; 366 blocked with the range text; Keep forever on switches the
 *     day field off.
 *
 * RED today: the gateway serves the CURRENT SPA (13 flat tabs, no save bar,
 * no search, no Limits section, no retention fields) — every assertion
 * fails for the spec's reason, never with a harness error.
 *
 * Paid models: none. These rows only read screens and drive forms; no LLM
 * is invoked.
 */

import { expect, test, type Page } from '@playwright/test'

const SETTINGS = '/#/settings'

async function openSettings(page: Page, search = '') {
  await page.goto(`${SETTINGS}${search}`)
  await expect(page.getByRole('heading', { name: 'Settings', level: 1 })).toBeVisible()
}

test.describe('G-01 — structure and reload (US-1 AS1, AS2)', () => {
  test('11 sections in three groups, no Sign-in; the open section survives a reload', async ({ page }) => {
    await openSettings(page)
    await expect(page.getByText('AI & agents')).toBeVisible()
    await expect(page.getByText('Security')).toBeVisible()
    await expect(page.getByText('System')).toBeVisible()
    for (const section of [
      'Providers',
      'Memory & recaps',
      'System agents',
      'Third-party agents',
      'Limits',
      'Protection',
      'Sandbox & network',
      'Keys & secrets',
      'Gateway',
      'Data & retention',
      'About',
    ]) {
      await expect(page.getByRole('link', { name: section }),
        `BLOCKED RED (G-01/FR-001): section ${section} must be in the grouped list`).toBeVisible()
    }
    await expect(page.getByRole('link', { name: 'Sign-in' })).toHaveCount(0)

    // AS2: open Sandbox & network, reload — it stays open.
    await page.getByRole('link', { name: 'Sandbox & network' }).click()
    await page.reload()
    await expect(page.getByRole('link', { name: 'Sandbox & network' })).toHaveAttribute(
      'aria-current',
      /page|true/,
    )
  })
})

test.describe('G-02 — old links land in the right place (US-1 AS3–AS7)', () => {
  const cases: Array<[string, string]> = [
    ['?tab=performance', 'Limits'],
    ['?tab=models', 'Limits'],
    ['?tab=security', 'Protection'],
    ['?tab=devices', 'Providers'], // hidden target opens Providers without an error (D-3 row 9)
  ]
  for (const [search, section] of cases) {
    test(`${search} opens ${section}`, async ({ page }) => {
      await openSettings(page, search)
      await expect(
        page.getByRole('heading', { name: section, level: 2 }),
        `BLOCKED RED (G-02/D-3): ${search} must open ${section}`,
      ).toBeVisible()
    })
  }
})

test.describe('G-03 — search (US-1 AS8–AS10)', () => {
  test('"web search" says it moved to Skills & Tools; "zzqx" shows the empty state', async ({ page }) => {
    await openSettings(page)
    const box = page.getByRole('searchbox', { name: /search/i })
    await box.fill('web search')
    await expect(page.getByText(/Skills & Tools/)).toBeVisible()
    await expect(page.getByRole('link', { name: /Skills & Tools/i })).toBeVisible()
    await box.fill('zzqx')
    await expect(page.getByText('No settings match')).toBeVisible()
    await expect(page.getByText(/zzqx/)).toBeVisible()
  })
})

test.describe('G-05 — the save bar on Gateway Port (US-2 AS1–AS5)', () => {
  test('counts follow the values; 80 blocks saving with the range text; a valid port saves', async ({ page }) => {
    await openSettings(page, '?tab=gateway')
    const port = page.getByLabel('Port')
    await port.fill('8081')
    await expect(page.getByText(/1 unsaved change/)).toBeVisible()
    await expect(page.getByText(/1 needs a gateway restart/)).toBeVisible()
    await expect(page.getByRole('button', { name: /save changes/i })).toBeVisible()

    await port.fill('80')
    await expect(page.getByText('Enter a whole number from 1,024 to 65,535.')).toBeVisible()
    await expect(page.getByText(/Fix 1 value before saving/)).toBeVisible()
    await expect(page.getByRole('button', { name: /save changes/i })).toHaveAttribute('aria-disabled', 'true')

    await port.fill('1024')
    await page.getByRole('button', { name: /save changes/i }).click()
    await expect(page.getByText(/Saved at/)).toBeVisible({ timeout: 10_000 })
  })
})

test.describe('G-07 — the split save (US-2 AS7)', () => {
  // The onboarding default password (tests/e2e/fixtures/onboard-via-api.ts
  // DEFAULT_PASSWORD; global-setup onboards with it) — the real prompt the
  // save raises must be answered with the admin's real password.
  const ADMIN_PASSWORD = process.env.OMNIPUS_ADMIN_PASSWORD ?? 'admin123'

  test('(a) the declared seam: the refused write keeps its draft and message, the other write saves, one write per endpoint', async ({ page }) => {
    // The DECLARED seam (spec G-07), method-scoped to the WRITE only
    // (CHECK F11): the reads that load the section keep reaching the real
    // gateway, so the intercept must not answer GET as well.
    const SEAM_MESSAGE = 'default_context_window must be ≥ 1 (send null to clear it)'
    const writes = { context: 0, performance: 0 }
    await page.route('**/api/v1/settings/context', async (route) => {
      if (route.request().method() !== 'PUT') return route.continue()
      writes.context += 1
      await route.fulfill({
        status: 400,
        contentType: 'application/json',
        body: JSON.stringify({ error: SEAM_MESSAGE }),
      })
    })
    await page.route('**/api/v1/performance', async (route) => {
      if (route.request().method() !== 'PUT') return route.continue()
      writes.performance += 1
      await route.continue()
    })

    await openSettings(page, '?tab=limits')
    // Real group expansion: both fields live in collapsed groups (FR-072).
    await page.getByRole('button', { name: /context and tool results/i }).click()
    await page.getByRole('button', { name: /agents and goals/i }).click()
    await page.getByLabel('Default context window').fill('32000')
    await page.getByLabel('Tries per goal').fill('8')
    await page.getByRole('button', { name: /save changes/i }).click()

    // Password steps (the gap CHECK F11 named): the save holds a
    // password-marked value (US-3 flow), so the real prompt must appear and
    // be answered BEFORE any write fires (FR-026: one prompt per Save).
    const prompt = page.getByRole('dialog', { name: /confirm your password/i })
    await expect(prompt, 'BLOCKED RED (G-07a/US-3): saving Limits must raise the password prompt').toBeVisible()
    expect(writes.context, 'no write may fire before the prompt is answered').toBe(0)
    expect(writes.performance, 'no write may fire before the prompt is answered').toBe(0)
    await prompt.getByLabel('Password').fill(ADMIN_PASSWORD)
    await prompt.getByRole('button', { name: 'Confirm' }).click()

    // Exact outcome 1 — not saved: the refused value keeps its draft with
    // the seam's message under it, and the bar counts exactly one failure
    // (spec G-07(a) expected result).
    await expect(page.getByLabel('Default context window')).toHaveValue('32000')
    await expect(page.getByText(SEAM_MESSAGE)).toBeVisible()
    await expect(page.getByText('1 change could not be saved')).toBeVisible()
    // Exact outcome 2 — saved: exactly one PUT reached the real gateway per
    // endpoint (FR-015: each changed value goes to its own endpoint once).
    expect(writes.performance, 'exactly one PUT /performance').toBe(1)
    expect(writes.context, 'exactly one PUT /settings/context (the seam)').toBe(1)
    // The saved value survives a reload from the real gateway; the refused
    // one must not be stored anywhere.
    await page.reload()
    await page.getByRole('button', { name: /context and tool results/i }).click()
    await page.getByRole('button', { name: /agents and goals/i }).click()
    await expect(page.getByLabel('Tries per goal')).toHaveValue('8')
    await expect(page.getByLabel('Default context window')).not.toHaveValue('32000')
  })

  test('(b) a real server refusal is shown under its field: the Codex path', async ({ page }) => {
    // No seam: the REAL gateway validates the path and refuses (spec G-07(b),
    // FR-015 BDD "A real server refusal is shown under its field").
    await openSettings(page, '?tab=third-party-agents')
    const codexPath = page.getByLabel('Codex program path')
    await expect(codexPath, 'BLOCKED RED (G-07b): the Third-party agents section must render the Codex row').toBeVisible()
    await codexPath.fill('/opt/omnipus-test/no-such-codex')
    await page.getByRole('button', { name: /save changes/i }).click()
    await expect(page.getByText(/cli_path_invalid/),
      'RED (G-07b): the real server cli_path_invalid refusal must appear under the Codex path field').toBeVisible({ timeout: 10_000 })
    await expect(page.getByLabel('Codex program path')).toHaveValue('/opt/omnipus-test/no-such-codex')
  })

  test('(c) the screen refuses a 0 Model-override context length itself and sends nothing', async ({ page }) => {
    const writes = { context: 0 }
    await page.route('**/api/v1/settings/context', async (route) => {
      if (route.request().method() !== 'PUT') return route.continue()
      writes.context += 1
      await route.continue()
    })
    await openSettings(page, '?tab=limits')
    await page.getByRole('button', { name: /model overrides/i }).click()
    await page.getByRole('button', { name: /add override/i }).click()
    await page.getByLabel('Override 1 provider').fill('openai')
    await page.getByLabel('Override 1 model').fill('gpt-x')
    await page.getByLabel('Override 1 context length, tokens').fill('0')
    // The screen refuses 0 itself, with the field's own minimum message.
    await expect(page.getByText('Context length must be a whole number of at least 1.'),
      'RED (G-07c/R2-MAJ-005): the screen must refuse a 0 override context length itself').toBeVisible()
    // And sends nothing (spec G-07(c): "the screen refuses 0 itself and
    // sends nothing").
    expect(writes.context, 'a client-refused save must not send the write').toBe(0)
  })
})

test.describe('G-13 — retention values (US-5 AS1–AS4)', () => {
  test('defaults 90/180/off; 30/365 persist; 366 is blocked; Keep forever switches the day field off', async ({ page }) => {
    await openSettings(page, '?tab=data')
    const chat = page.getByLabel('Keep chat history for')
    const memory = page.getByLabel('Keep memory for')
    await expect(chat).toHaveValue('90')
    await expect(memory).toHaveValue('180')

    await chat.fill('30')
    await memory.fill('365')
    await page.getByRole('button', { name: /save changes/i }).click()
    await expect(page.getByText(/Saved at/)).toBeVisible({ timeout: 10_000 })
    await page.reload()
    await expect(page.getByLabel('Keep chat history for')).toHaveValue('30')
    await expect(page.getByLabel('Keep memory for')).toHaveValue('365')

    await page.getByLabel('Keep chat history for').fill('366')
    await expect(page.getByText('Enter a whole number from 1 to 365.')).toBeVisible()
    await expect(page.getByRole('button', { name: /save changes/i })).toHaveAttribute('aria-disabled', 'true')

    await page.getByLabel('Keep chat history for').fill('30')
    await page.getByRole('switch', { name: /keep forever/i }).click()
    await expect(memory).toBeDisabled()
  })
})

test.describe('G-04 — phone layout (US-1 AS9, US-9 AS5; FR-008, FR-070)', () => {
  // G-04 spot-checks 3 sections (spec's hands-on note); the control-size
  // sweep runs over every visible control of each opened section.
  const SPOT_SECTIONS = ['Providers', 'Limits', 'Gateway'] as const

  test('list first; a section opens alone with the Settings back button; no sideways scroll; controls ≥ 44 px', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await openSettings(page)
    // FR-008: the list shows first — no section content beside it.
    await expect(page.getByRole('heading', { name: 'Providers', level: 2 }),
      'BLOCKED RED (G-04/FR-008): at 390 px the list must show first, no section beside it').not.toBeVisible()

    for (const section of SPOT_SECTIONS) {
      await page.getByRole('link', { name: section }).click()
      await expect(page.getByRole('heading', { name: section, level: 2 })).toBeVisible()
      // FR-008: the section shows alone, with the Settings back button.
      await expect(page.getByRole('button', { name: 'Settings', exact: true }),
        `RED (G-04/FR-008): ${section} must offer the Settings back button at 390 px`).toBeVisible()

      // FR-070: the page must not scroll sideways.
      const scroll = await page.evaluate(() => ({
        sw: document.documentElement.scrollWidth,
        iw: window.innerWidth,
      }))
      expect(scroll.sw, `FR-070: ${section} must not scroll sideways at 390 px`).toBeLessThanOrEqual(scroll.iw + 1)

      // FR-070: every visible control measures at least 44 × 44 px.
      const tooSmall = await page.evaluate(() => {
        const nodes = Array.from(document.querySelectorAll<HTMLElement>(
          '#settings-content button, #settings-content a, #settings-content input, #settings-content select, #settings-content [role="switch"]',
        ))
        return nodes
          .filter((el) => {
            const r = el.getBoundingClientRect()
            return r.width > 0 && r.height > 0
          })
          .map((el) => {
            const r = el.getBoundingClientRect()
            return { name: el.getAttribute('aria-label') ?? el.textContent?.trim().slice(0, 30) ?? el.tagName, w: Math.round(r.width), h: Math.round(r.height) }
          })
          .filter((m) => m.w < 44 || m.h < 44)
      })
      expect(tooSmall,
        `RED (G-04/FR-070): controls under 44 px in ${section}`).toEqual([])
    }

    // FR-008: the back button returns to the list.
    await page.getByRole('button', { name: 'Settings', exact: true }).click()
    await expect(page.getByRole('link', { name: 'Providers' })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Gateway', level: 2 })).not.toBeVisible()
  })
})

test.describe('US-9 — accessibility and states (FR-068, FR-069, FR-065, FR-075, FR-063, FR-109)', () => {
  test('FR-068: the skip link is the first Tab stop and moves focus without changing the address', async ({ page }) => {
    await openSettings(page)
    await page.keyboard.press('Tab')
    await expect(page.getByRole('link', { name: 'Skip to settings content' }),
      'RED (FR-068): the skip link must be the first Tab stop').toBeFocused()
    const addressBefore = page.url()
    await page.keyboard.press('Enter')
    await expect(page.locator('#settings-content'),
      'RED (FR-068): Enter must move focus to the section content').toBeFocused()
    expect(page.url(), 'FR-068: the address must not change').toBe(addressBefore)
  })

  test('FR-069: Escape closes the Discard-key confirm one at a time and focus returns inside the panel', async ({ page }) => {
    // The spec's own stacked scenario: the Configure provider panel with the
    // "Discard key?" confirm open over it.
    await openSettings(page, '?tab=providers')
    await page.getByLabel(/configure /i).first().click()
    const sheet = page.getByTestId('provider-config-sheet')
    await expect(sheet, 'BLOCKED RED (FR-069): the Configure provider panel must open').toBeVisible()
    // Dirty the key so a close would discard it (FR-033 asks instead).
    await sheet.locator('[data-testid^="api-key-input-"]').fill('sk-discard-check')

    await page.keyboard.press('Escape')
    await expect(page.getByTestId('discard-key-keep'),
      'RED (FR-033/FR-069): the Discard-key confirm must open over the panel').toBeVisible()
    await expect(sheet, 'the panel must stay open under the confirm').toBeVisible()

    await page.keyboard.press('Escape')
    await expect(page.getByTestId('discard-key-keep'),
      'RED (FR-069): the first Escape must close only the confirm').not.toBeVisible()
    await expect(sheet, 'RED (FR-069): the panel must stay open when only the confirm closes').toBeVisible()
    const focusInside = await page.evaluate(() => {
      const active = document.activeElement as HTMLElement | null
      const panel = document.querySelector('[data-testid="provider-config-sheet"]')
      return !!active && !!panel && (active === panel || panel.contains(active)) && active.tabIndex >= 0
    })
    expect(focusInside,
      'RED (FR-069): focus must return to a control inside the panel that stacked the confirm').toBeTruthy()
  })

  test('FR-065: a slow section read shows Loading with a spinner and skeleton cards until it answers', async ({ page }) => {
    // Declared read seam: hold the retention read so the loading state stays
    // mounted long enough to assert on.
    let release: (() => void) | undefined
    const held = new Promise<void>((resolve) => { release = resolve })
    await page.route('**/api/v1/settings/retention', async (route) => {
      await held
      await route.continue()
    })
    await openSettings(page, '?tab=data')
    await expect(page.getByText('Loading Data & retention…'),
      'RED (FR-065): the section must show Loading <section>… while its reads answer').toBeVisible()
    // FR-065: a spinner AND skeleton cards during the load.
    const spinner = page.locator('#settings-content .animate-spin')
    await expect(spinner.first(), 'RED (FR-065): the loading state must carry a spinner').toBeVisible()
    const skeletonCount = await page.locator('#settings-content [data-slot="skeleton"], #settings-content .skeleton-shimmer, #settings-content [class*="skeleton"]').count()
    expect(skeletonCount, 'RED (FR-065): the loading state must show skeleton cards').toBeGreaterThan(0)

    release?.()
    await expect(page.getByRole('heading', { name: 'Data & retention', level: 2 })).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText('Loading Data & retention…')).not.toBeVisible()
  })

  test('FR-075: under reduced motion the spinner rotation and skeleton pulse stop', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    let release: (() => void) | undefined
    const held = new Promise<void>((resolve) => { release = resolve })
    await page.route('**/api/v1/settings/retention', async (route) => {
      await held
      await route.continue()
    })
    await openSettings(page, '?tab=data')
    await expect(page.getByText('Loading Data & retention…')).toBeVisible()

    // FR-075: Skeleton pulse, spinner rotation and row highlight MUST stop —
    // computed animation must be off for every animated element in the
    // loading state.
    const animated = await page.evaluate(() => {
      const nodes = Array.from(document.querySelectorAll<HTMLElement>('#settings-content *'))
      return nodes
        .map((el) => ({ el, name: getComputedStyle(el).animationName }))
        .filter((x) => x.name && x.name !== 'none')
        .map((x) => ({ name: x.name, cls: x.el.className?.toString().slice(0, 60) ?? '' }))
    })
    expect(animated,
      'RED (FR-075): no animation may run in the loading state under reduced motion').toEqual([])

    release?.()
    await expect(page.getByRole('heading', { name: 'Data & retention', level: 2 })).toBeVisible({ timeout: 10_000 })
  })

  test('FR-063 (G-10 new-address leg): restarted=1 says the gateway restarted on an empty list, shows the banner when a key is still listed', async ({ page }) => {
    // Declared read seam: this leg is driven on the new address, where the
    // real gateway would answer; the suite intercepts only the
    // pending-restart read. The full G-10 direct/proxy restart journey (a
    // real restart on a production-served build) stays the hands-on row.
    const listed = JSON.stringify([
      { key: 'gateway.port', persisted_value: 8080, applied_value: 8081, reason: 'restart_required' },
    ])
    await page.route('**/api/v1/config/pending-restart', async (route) => {
      if (route.request().method() !== 'GET') return route.continue()
      await route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
    })
    await openSettings(page, '?tab=gateway&restarted=1')
    await expect(page.getByTestId('settings-restarted'),
      'RED (FR-063): at the new address with an empty pending list, Settings must say the gateway restarted').toBeVisible()
    await expect(page.getByTestId('settings-restart-banner')).toHaveCount(0)

    await page.route('**/api/v1/config/pending-restart', async (route) => {
      if (route.request().method() !== 'GET') return route.continue()
      await route.fulfill({ status: 200, contentType: 'application/json', body: listed })
    })
    await openSettings(page, '?tab=gateway')
    await expect(page.getByTestId('settings-restarted')).toHaveCount(0)
    await expect(page.getByTestId('settings-restart-banner'),
      'RED (FR-063): with the key still listed, the banner must show instead').toBeVisible()
  })

  test('FR-109: following a link from an open agent edit panel leaves no overlay over Settings', async ({ page }) => {
    await openSettings(page, '?tab=system-agents')
    await page.getByRole('button', { name: /^Edit / }).first().click()
    const programLink = page.getByTestId('profile-program-settings-link')
    await expect(programLink,
      'BLOCKED RED (FR-109): the agent edit panel must expose its program-settings link').toBeVisible()
    await programLink.click()
    await expect(page.getByRole('heading', { name: 'Third-party agents', level: 2 })).toBeVisible()
    const overlayOver = await page.evaluate(() => {
      const el = document.elementFromPoint(window.innerWidth / 2, window.innerHeight / 2)
      return !!el && !!el.closest('[role="dialog"], [role="alertdialog"], [data-radix-popper-content-wrapper]')
    })
    expect(overlayOver,
      'RED (FR-109): no overlay may remain over Settings after following the panel link').toBe(false)
  })
})
