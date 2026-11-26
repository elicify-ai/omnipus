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
 *   - G-03 (US-1 AS8–AS10): "web search" → Providers; Tool permissions /
 *     Skill trust → temporarily unavailable with no destination; "zzqx"
 *     → "No settings match". Revised by Q1 A in
 *     /workspace/omnipus-coordination/decisions/2026-11-23-founder-q1-q2.md
 *     and team-lead ruling 2026-11-23 implementing Q1 A (part 32).
 *     This revision has static checks only; browser execution is deferred
 *     to the cut, not claimed as observed RED or GREEN.
 *   - G-04 (US-1 AS9, US-9 AS5; FR-008, FR-070): at 390 px the list shows
 *     first, every spot-checked section is reached from the list and
 *     returns to it (R2-05), with the Settings back button, no sideways
 *     scroll, controls ≥ 44 px — section controls AND the chrome (nav,
 *     search, global controls).
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
 *   - FR-068 / FR-069 / FR-065 / FR-075 / FR-063(new-address legs) / FR-109:
 *     the browser-mapped rows CHECK F11 found missing (skip link, stacked
 *     Discard-key confirm — with the Cancel leg and an exact opener
 *     (R2-05), the loading state in ALL eleven sections (R2-05), reduced
 *     motion incl. the list row highlight (R2-05), restarted reconciliation
 *     on BOTH new-address legs (R2-05), and the agent-overlay follow-link
 *     driven on a seeded external worker's Runtime tab (R2-05)). The full
 *     G-10 direct/proxy restart journey stays the hands-on row.
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

import { expect, test, type Page, type Route } from '@playwright/test'

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
  test('Q1 A: "web search" opens Providers, unavailable permissions lead nowhere, and "zzqx" keeps the empty state', async ({ page }) => {
    // Starting in Gateway makes an ignored result click fail the destination check.
    await openSettings(page, '?tab=gateway')
    await expect(page.getByRole('heading', { name: 'Gateway', level: 2 })).toBeVisible()
    const box = page.getByRole('searchbox', { name: /search/i })
    await box.fill('web search')
    const results = page.getByTestId('settings-search-results')
    await expect(results.getByText('Providers', { exact: true })).toBeVisible()
    await expect(results.getByText(/moved|Skills & Tools/i)).toHaveCount(0)
    await results.getByRole('link', { name: /Web search/i }).click()
    await expect(page).toHaveURL(/[?#&]tab=providers(?:&|$)/)
    await expect(page.getByRole('heading', { name: 'Providers', level: 2 })).toBeVisible()
    for (const setting of ['Tool permissions', 'Skill trust']) {
      await box.fill(setting)
      await expect(results.getByText(setting, { exact: true })).toBeVisible()
      await expect(results.getByText('Temporarily unavailable until the Skills & Tools update lands.', { exact: true })).toBeVisible()
      await expect(results.getByRole('link')).toHaveCount(0)
      await expect(results.getByRole('button')).toHaveCount(0)
      await expect(results.getByText(/moved to/i)).toHaveCount(0)
    }
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
  // sweep runs over every visible control of each opened section AND, on the
  // list, over the chrome a person uses to get there — nav, search, global
  // shell controls (R2-05).
  const SPOT_SECTIONS = ['Providers', 'Limits', 'Gateway'] as const

  test('list first; every section is reached from the list and returns to it; no sideways scroll; controls ≥ 44 px', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await openSettings(page)
    // FR-008: the list shows first — no section content beside it.
    await expect(page.getByRole('heading', { name: 'Providers', level: 2 }),
      'BLOCKED RED (G-04/FR-008): at 390 px the list must show first, no section beside it').not.toBeVisible()

    // FR-070 (R2-05): the chrome sweep — nav links/buttons, the search box
    // and the global shell controls are the controls a person touches to
    // reach a section at 390 px; they measure too.
    const chromeTooSmall = await page.evaluate(() => {
      const area = (el: Element) =>
        el.closest('nav') ? 'nav'
        : el.closest('header') ? 'header'
        : el.getAttribute('role') === 'searchbox' || el.matches('input[type="search"]') ? 'search'
        : 'settings'
      return Array.from(document.querySelectorAll<HTMLElement>(
        [
          'nav a', 'nav button',
          '[role="searchbox"]', 'input[type="search"]',
          'header button', 'header a',
        ].join(', '),
      ))
        .filter((el) => {
          const r = el.getBoundingClientRect()
          return r.width > 0 && r.height > 0
        })
        .map((el) => {
          const r = el.getBoundingClientRect()
          return {
            where: area(el),
            name: el.getAttribute('aria-label') ?? el.textContent?.trim().slice(0, 30) ?? el.tagName,
            w: Math.round(r.width),
            h: Math.round(r.height),
          }
        })
        .filter((m) => m.w < 44 || m.h < 44)
    })

    for (const section of SPOT_SECTIONS) {
      // FR-008: each section opens FROM the list, alone, with the back button.
      await page.getByRole('link', { name: section }).click()
      await expect(page.getByRole('heading', { name: section, level: 2 })).toBeVisible()
      await expect(page.getByRole('button', { name: 'Settings', exact: true }),
        `RED (G-04/FR-008): ${section} must offer the Settings back button at 390 px`).toBeVisible()

      // FR-070: the page must not scroll sideways.
      const scroll = await page.evaluate(() => ({
        sw: document.documentElement.scrollWidth,
        iw: window.innerWidth,
      }))
      expect(scroll.sw, `FR-070: ${section} must not scroll sideways at 390 px`).toBeLessThanOrEqual(scroll.iw + 1)

      // FR-070: every visible section control measures at least 44 × 44 px.
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

      // FR-008 (R2-05): the back button returns to the list BETWEEN sections
      // — the next section is again reached from the list, never by
      // navigating section to section.
      await page.getByRole('button', { name: 'Settings', exact: true }).click()
      await expect(page.getByRole('heading', { name: section, level: 2 })).not.toBeVisible()
      await expect(page.getByRole('link', { name: 'Providers' })).toBeVisible()
    }

    expect(chromeTooSmall,
      'RED (G-04/FR-070): controls under 44 px in the chrome (nav, search, global controls)').toEqual([])
  })
})

// U1-5 proof added on the hands-on cut 5a8c4355c0. DO NOT claim browser
// RED from the DOM-only unit checks: this exact coarse-pointer row must run
// on the rebuilt cut. All earlier G-04 cases and helpers remain unchanged.
test.describe('U1-5 — 390px real touch hit geometry (US-9 AS5, FR-070)', () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true })

  test('switches, Settings back, Limits disclosure, Port and Sandbox remove address have non-overlapping 44x44 hit regions', async ({ page }) => {
    await page.goto(`${SETTINGS}?tab=gateway`)
    await expect(page.getByRole('heading', { name: 'Gateway', level: 2 })).toBeVisible()
    expect(await page.evaluate(() => window.matchMedia('(pointer: coarse)').matches),
      'instrument: hasTouch must really select coarse-pointer CSS').toBe(true)

    const assertTarget = async (control: ReturnType<Page['getByRole']>, name: string) => {
      await expect(control).toBeVisible()
      await control.scrollIntoViewIfNeeded()
      const hit = await control.evaluate((node) => {
        const rect = node.getBoundingClientRect()
        const before = window.getComputedStyle(node, '::before')
        const expanded = node.hasAttribute('data-ds-action') && before.content !== 'none'
          && before.content !== 'normal' && before.pointerEvents !== 'none' && before.position === 'absolute'
        const width = expanded ? Math.max(rect.width, Number.parseFloat(before.width) || 0) : rect.width
        const height = expanded ? Math.max(rect.height, Number.parseFloat(before.height) || 0) : rect.height
        const x = rect.x + rect.width / 2
        const y = rect.y + rect.height / 2
        // Corners just inside the required 44px square must hit THIS control,
        // not its neighbour or a clipped/covered region. No rounding up.
        const points = [[x, y], ...[-21.5, 21.5].flatMap((dx) => [-21.5, 21.5].map((dy) => [x + dx, y + dy]))]
        const misses = points.filter(([px, py]) => {
          const target = document.elementFromPoint(px, py)
          return target !== node && !node.contains(target)
        })
        return { width, height, misses }
      })
      expect(hit.width, `U1-5: ${name} hit width`).toBeGreaterThanOrEqual(44)
      expect(hit.height, `U1-5: ${name} hit height`).toBeGreaterThanOrEqual(44)
      expect(hit.misses, `U1-5: ${name} expanded target is not clipped or stolen by adjacent controls`).toEqual([])
    }

    await assertTarget(page.getByRole('switch', { name: 'Preview server' }), 'Preview server switch')
    await assertTarget(page.getByRole('button', { name: 'Settings', exact: true }), 'Settings back')
    await assertTarget(page.getByRole('spinbutton', { name: 'Port' }), 'Port input')
    await page.goto(`${SETTINGS}?tab=limits`)
    await assertTarget(page.getByRole('button', { name: 'Context and tool results' }), 'Limits disclosure')

    // Read-only HTTP fixture: make one custom-address remove control
    // reachable without editing the server's actual network policy.
    await page.route('**/api/v1/security/sandbox-config', async (route) => {
      if (route.request().method() !== 'GET') return route.continue()
      const response = await route.fetch()
      const config = await response.json()
      await route.fulfill({ response, json: { ...config, ssrf: { ...config.ssrf, allow_internal: ['10.20.30.40'] } } })
    })
    await page.goto(`${SETTINGS}?tab=sandbox`)
    const advanced = page.getByRole('button', { name: 'Advanced (custom list)' })
    await expect(advanced).toBeVisible()
    if (await advanced.getAttribute('aria-expanded') !== 'true') await advanced.click()
    await assertTarget(page.getByRole('button', { name: 'Delete SSRF entry 10.20.30.40' }), 'Sandbox remove address')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
      'U1-5/FR-070: no horizontal scroll at 390px').toBe(true)
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

  test('FR-069: Escape closes the Discard-key confirm one at a time; Cancel keeps the typed key; focus returns inside the panel', async ({ page }) => {
    // The spec's own stacked scenario: the Configure provider panel with the
    // "Discard key?" confirm open over it. R2-05: the opener is clicked by
    // its EXACT accessible name (Configure <provider>), read from the page —
    // a fuzzy first-match click could open a different panel than the one
    // this row means. No provider rows on the gateway at all is a BLOCKED
    // failure naming it, never a pass.
    await openSettings(page, '?tab=providers')
    const anyConfigure = page.getByRole('button', { name: /^Configure / }).first()
    if ((await anyConfigure.count()) === 0) {
      await expect(page.getByTestId('provider-config-sheet'),
        'BLOCKED RED (FR-069): no provider row offers a Configure button on this gateway').toBeVisible()
    }
    const openerName = ((await anyConfigure.getAttribute('aria-label')) ?? (await anyConfigure.textContent()) ?? '').trim()
    await page.getByRole('button', { name: openerName, exact: true }).click()
    const sheet = page.getByTestId('provider-config-sheet')
    await expect(sheet, 'BLOCKED RED (FR-069): the Configure provider panel must open').toBeVisible()
    // Dirty the key so a close would discard it (FR-033 asks instead).
    const keyInput = sheet.locator('[data-testid^="api-key-input-"]')
    await keyInput.fill('sk-discard-check')

    // R4-06 premise, witnessed (not assumed): the key input actually holds
    // focus before the Escape that opens the inner confirm — FR-069's
    // "returning focus to the control that opened each" is only meaningful
    // if the opener really was the focused control the gesture came from.
    await expect(keyInput,
      'FR-069 premise: the key input holds focus before the Escape that opens the confirm').toBeFocused()

    await page.keyboard.press('Escape')
    await expect(page.getByTestId('discard-key-keep'),
      'RED (FR-033/FR-069): the Discard-key confirm must open over the panel').toBeVisible()
    await expect(sheet, 'the panel must stay open under the confirm').toBeVisible()

    // R2-05: the stacked confirm's Cancel keeps the typed key — the confirm
    // closes, the panel stays, the input still holds what was typed.
    await page.getByTestId('discard-key-keep').click()
    await expect(page.getByTestId('discard-key-keep'),
      'RED (FR-069): Cancel must close only the confirm').not.toBeVisible()
    await expect(sheet, 'RED (FR-069): Cancel must keep the panel open').toBeVisible()
    await expect(keyInput, 'RED (FR-069): Cancel must keep the typed key').toHaveValue('sk-discard-check')

    // Stacked again: Escape over the kept panel asks once more, and the next
    // Escape closes only the confirm — returning focus to the EXACT opener
    // (R3-06): the key input is the control that was focused when the confirm
    // opened (the close gesture came from it), so FR-069's "returning focus
    // to the control that opened each" names THAT control, not any tabbable
    // element inside the panel (the old oracle accepted any of them).
    // R4-06: that premise is WITNESSED before the gesture — focus is on the
    // key input again after the first Keep, so the next Escape provably
    // comes from the opener.
    await expect(keyInput,
      'FR-069 premise: after the first Keep, focus is back on the key input before the next Escape').toBeFocused()
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('discard-key-keep'),
      'RED (FR-069): Escape over the kept panel must ask again').toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('discard-key-keep'),
      'RED (FR-069): the first Escape must close only the confirm').not.toBeVisible()
    await expect(sheet, 'RED (FR-069): the panel must stay open when only the confirm closes').toBeVisible()
    await expect(keyInput,
      'RED (FR-069/R3-06): focus must return to the exact control that opened the confirm — the key input').toBeFocused()

    // Discard closes SEVERAL stacked dialogs at once (FR-069, clarified:
    // spec :1995, team-lead clarification 2026-11-21 — commit 993eecf061,
    // CHECK R4-06): one action tears the confirm AND its side panel down
    // together, so focus goes to the control that opened the OUTERMOST
    // dialog that closed — this panel's own Configure opener, still in the
    // section's row list. Replaces part 19's heading oracle (5dd3db78b9),
    // which read the first wording's "no dialog is left open" as the
    // heading arm's premise; the clarified rule names the outermost opener,
    // and the section heading applies ONLY when that control no longer
    // exists either. The heading arm is deliberately not exercised here:
    // no gesture in this suite removes the row's Configure button while its
    // sheet is open — FR-069's own one-at-a-time rule closes the sheet
    // before any such gesture — and the picker → connect flow restores
    // focus to the persistent Connect-a-provider control, so the arm has no
    // stable real witness in the UI (reported to team-lead; the build keeps
    // the focusHeading wiring for the arm).
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('discard-key-keep'),
      'the third Escape must ask once more before the Discard leg').toBeVisible()
    await page.getByTestId('discard-key-discard').click()
    // The premise, asserted before the focus oracle: one action closed the
    // confirm AND the panel — the stacked-closing case the clarified rule
    // writes down.
    await expect(page.getByTestId('discard-key-keep'),
      'FR-069 ruling premise: the confirm is closed').not.toBeVisible()
    await expect(sheet, 'FR-069 ruling premise: the panel is closed — both stacked dialogs closed at once').not.toBeVisible()
    await expect(page.getByRole('button', { name: openerName, exact: true }),
      'RED (FR-069/R4-06 clarified): after one action closes the confirm and its side panel, focus must go to the control that opened the outermost dialog that closed — the Configure opener').toBeFocused()
  })

  test('FR-065: every section shows Loading with a spinner and skeleton cards while its reads answer (R2-05: all eleven sections)', async ({ page }) => {
    // Declared read seam per section: hold THAT section's own reads so its
    // loading state stays mounted long enough to assert on. The shell's boot
    // reads are never held, so the shell and the list render and the opened
    // section is the only thing pending. Paths are the sections' real GET
    // endpoints (src/lib/api/*).
    const sections: Array<{ section: string; tab: string; reads: string[] }> = [
      { section: 'Providers', tab: 'providers', reads: ['**/api/v1/providers', '**/api/v1/providers/default-model', '**/api/v1/integrations/providers'] },
      { section: 'Memory & recaps', tab: 'memory', reads: ['**/api/v1/settings/memory'] },
      { section: 'System agents', tab: 'system-agents', reads: ['**/api/v1/agents'] },
      { section: 'Third-party agents', tab: 'third-party-agents', reads: ['**/api/v1/settings/third-party-agents'] },
      { section: 'Limits', tab: 'limits', reads: ['**/api/v1/settings/context', '**/api/v1/performance'] },
      { section: 'Protection', tab: 'protection', reads: ['**/api/v1/gateway/god-mode', '**/api/v1/security/sandbox-config', '**/api/v1/security/rate-limits', '**/api/v1/security/prompt-guard'] },
      { section: 'Sandbox & network', tab: 'sandbox', reads: ['**/api/v1/security/sandbox-status', '**/api/v1/security/sandbox-config'] },
      { section: 'Keys & secrets', tab: 'keys', reads: ['**/api/v1/credentials'] },
      { section: 'Gateway', tab: 'gateway', reads: ['**/api/v1/config', '**/api/v1/status'] },
      { section: 'Data & retention', tab: 'data', reads: ['**/api/v1/settings/retention', '**/api/v1/storage/stats'] },
      { section: 'About', tab: 'about', reads: ['**/api/v1/about', '**/api/v1/version'] },
    ]
    for (const { section, tab, reads } of sections) {
      let release: (() => void) | undefined
      const held = new Promise<void>((resolve) => { release = resolve })
      const hold = async (route: Route): Promise<void> => {
        await held
        await route.continue()
      }
      for (const pattern of reads) await page.route(pattern, hold)
      await openSettings(page, `?tab=${tab}`)
      await expect(page.getByText(`Loading ${section}…`),
        `RED (FR-065): ${section} must show Loading <section>… while its reads answer`).toBeVisible()
      // FR-065: a spinner AND skeleton cards during the load.
      const spinner = page.locator('#settings-content .animate-spin')
      await expect(spinner.first(), `RED (FR-065): ${section}'s loading state must carry a spinner`).toBeVisible()
      const skeletonCount = await page.locator('#settings-content [data-slot="skeleton"], #settings-content .skeleton-shimmer, #settings-content [class*="skeleton"]').count()
      expect(skeletonCount, `RED (FR-065): ${section}'s loading state must show skeleton cards`).toBeGreaterThan(0)

      release?.()
      await expect(page.getByRole('heading', { name: section, level: 2 })).toBeVisible({ timeout: 10_000 })
      await expect(page.getByText(`Loading ${section}…`)).not.toBeVisible()
      for (const pattern of reads) await page.unroute(pattern, hold)
    }
  })

  test('FR-075: under reduced motion the spinner rotation, skeleton pulse and the list row highlight stop', async ({ page }) => {
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

    // FR-075 (R4-05): the row highlight the spec names is the flash on the
    // setting row a person reaches from a Settings SEARCH RESULT or a deep
    // link — driven here through the real search jump onto the Port row.
    // Under reduced motion the row is still scrolled into view and MARKED
    // (the mark reaching assistive tech), and no highlight layer — the row
    // or either pseudo-element — may animate. Replaces the R3-06 locator
    // (`getByLabel('Port')` → ancestor `[data-settings-field]`), which named
    // an attribute the Gateway rows do not carry; the replaced assertions
    // cite FR-075's clarified definition (spec :2001, team-lead
    // clarification 2026-11-21, CHECK R4-05). The `data-settings-row*`
    // names are the build's selectors, not the oracle.
    await page.getByTestId('settings-search').fill('port')
    await page.getByTestId('settings-search-results').getByRole('link').first().click()
    await expect(page.getByRole('heading', { name: 'Gateway', level: 2 })).toBeVisible({ timeout: 10_000 })
    const row = page.locator('[data-settings-row="port"]')
    await expect(row, 'RED (FR-075/R4-05): the searched row must exist in the opened section').toBeVisible()
    await expect(row,
      'RED (FR-075/R4-05): under reduced motion the reached row must still be MARKED').toHaveAttribute('data-settings-row-target')
    await expect(row,
      'RED (FR-075/R4-05): the mark must reach assistive tech (aria-current)').toHaveAttribute('aria-current', 'true')
    const highlightAnimated = await row.evaluate((el) => [
      getComputedStyle(el).animationName,
      getComputedStyle(el, '::before').animationName,
      getComputedStyle(el, '::after').animationName,
    ].filter((name) => name && name !== 'none'))
    expect(highlightAnimated,
      'RED (FR-075/R4-05): the searched setting row\'s highlight must not animate under reduced motion').toEqual([])
  })

  test('FR-075 (R4-05): the searched setting-row flash runs once under normal motion — the positive witness the reduced-motion check needs', async ({ page }) => {
    // Without this witness a MISSING row highlight would pass the
    // reduced-motion check vacuously (nothing there cannot animate). FR-075
    // names a row highlight, so under normal motion the reached row must
    // actually run one; its absence is a production finding, not a pass.
    // Same real trigger as the reduced-motion leg: the search jump onto the
    // Port row. FR-075 (spec :2001, CHECK R4-05): the flash runs ONCE, so
    // the witness pins a running animation AND its one-iteration count — a
    // persistent or looping glow is not the flash the spec names.
    await openSettings(page)
    await page.getByTestId('settings-search').fill('port')
    await page.getByTestId('settings-search-results').getByRole('link').first().click()
    await expect(page.getByRole('heading', { name: 'Gateway', level: 2 })).toBeVisible({ timeout: 10_000 })
    const row = page.locator('[data-settings-row="port"]')
    await expect(row, 'RED (FR-075/R4-05): the searched row must exist in the opened section').toBeVisible()
    await expect(row,
      'RED (FR-075/R4-05): the reached row must be MARKED under normal motion').toHaveAttribute('data-settings-row-target')
    const highlight = await row.evaluate((el) => [
      getComputedStyle(el).animationName,
      getComputedStyle(el, '::before').animationName,
      getComputedStyle(el, '::after').animationName,
    ].filter((name) => name && name !== 'none'))
    expect(highlight,
      'RED (FR-075/R4-05): the searched setting row must carry a highlight animation under normal motion — without it the reduced-motion check passes vacuously').not.toEqual([])
    const iterations = await row.evaluate((el) => getComputedStyle(el).animationIterationCount)
    expect(iterations,
      'FR-075: the flash runs once — the highlight must be a one-iteration animation, not a persistent or looping glow').toBe('1')
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
    // R2-05: the non-empty leg is driven on the new address too — arrived
    // with restarted=1, the still-listed key must win: the banner shows and
    // the restarted notice stays away (FR-063's reconciliation is by the
    // pending list, not by the URL alone).
    await openSettings(page, '?tab=gateway&restarted=1')
    await expect(page.getByTestId('settings-restarted')).toHaveCount(0)
    await expect(page.getByTestId('settings-restart-banner'),
      'RED (FR-063): with the key still listed, the banner must show instead — even when the address carries restarted=1').toBeVisible()
  })

  test('FR-109: an external worker\'s Runtime tab links to program settings; following it leaves no overlay over Settings', async ({ page }) => {
    // R2-05: the row is driven on an EXTERNAL worker (subagent_3p) — the
    // Runtime tab and its program-settings link exist only for an
    // external-CLI agent (AgentProfile renders tab-runtime under
    // isExternalAgent). REST is the only non-UI create path (the app has no
    // manual create form, FR-108); the worker is seeded onto its own
    // workspace's core team so the Team tab renders it and its card opens
    // the ONE shared agent editor (AppShell-hosted AgentProfile).
    await openSettings(page, '?tab=system-agents')
    // The SPA origin: the storageState cookie and the CSRF cookie exist here
    // (page.request shares the browser context's cookie jar; the gateway's
    // CSRF middleware needs the cookie value echoed as X-CSRF-Token —
    // bug-regression.spec.ts's seed pattern).
    const cookies = await page.context().cookies()
    const csrfCookie = cookies.find((c) => c.name === 'csrf' || c.name === '__Host-csrf')
    const authHeaders: Record<string, string> = {
      'Content-Type': 'application/json',
      ...(csrfCookie ? { 'X-CSRF-Token': csrfCookie.value } : {}),
    }
    const workerResp = await page.request.post('/api/v1/agents', {
      headers: authHeaders,
      data: {
        type: 'subagent_3p',
        name: `Settings FR-109 worker ${Date.now()}`,
        description: 'settings-redesign FR-109 fixture worker',
        executor: { cli: 'codex' },
      },
    })
    expect(workerResp.ok(), `POST /agents failed: ${workerResp.status()} ${await workerResp.text()}`).toBe(true)
    const worker = (await workerResp.json()) as { id: string; name: string }
    const workspaceResp = await page.request.post('/api/v1/workspaces', {
      headers: authHeaders,
      data: { name: `Settings FR-109 ${worker.id}`, core_team: [worker.id] },
    })
    expect(workspaceResp.ok(), `POST /workspaces failed: ${workspaceResp.status()} ${await workspaceResp.text()}`).toBe(true)
    const workspace = (await workspaceResp.json()) as { id: string }

    // The worker's Team tab: its card opens the shared editor through its
    // REAL controls — Actions (TeamListView.tsx::TeamAgentCard's Popover) and
    // its Edit button — the only path that opens the shared editor; clicking
    // the plain name never reaches the overlay under test (R3-04). The
    // Runtime tab carries the Program line with the Change-in-Settings link
    // (r17 17.6 / T11 FR-8).
    await page.goto(`/#/workspaces/${workspace.id}/team`)
    const card = page.getByTestId(`team-card-${worker.id}`)
    await expect(card,
      'BLOCKED RED (FR-109): the external worker\'s card must render on the Team tab').toBeVisible()
    await page.getByTestId(`team-card-actions-${worker.id}`).click()
    await page.getByRole('button', { name: `Edit ${worker.name}`, exact: true }).click()
    const runtime = page.getByTestId('tab-runtime')
    await expect(runtime,
      'BLOCKED RED (FR-109): the external worker must expose the Runtime tab').toBeVisible()
    await runtime.click()
    const programLink = page.getByTestId('profile-program-settings-link')
    await expect(programLink,
      'BLOCKED RED (FR-109): the Runtime tab must expose its program-settings link').toBeVisible()
    await programLink.click()
    await expect(page.getByRole('heading', { name: 'Third-party agents', level: 2 })).toBeVisible()
    const overlayOver = await page.evaluate(() => {
      const el = document.elementFromPoint(window.innerWidth / 2, window.innerHeight / 2)
      return !!el && !!el.closest('[role="dialog"], [role="alertdialog"], [data-radix-popper-content-wrapper]')
    })
    expect(overlayOver,
      'RED (FR-109): no overlay may remain over Settings after following the panel link').toBe(false)

    // Best-effort fixture cleanup; results are not asserted.
    await page.request.delete(`/api/v1/agents/${worker.id}`, { headers: authHeaders })
    await page.request.delete(`/api/v1/workspaces/${workspace.id}`, { headers: authHeaders })
  })
})
