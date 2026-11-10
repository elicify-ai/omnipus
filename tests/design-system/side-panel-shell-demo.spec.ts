// side-panel-shell-demo.spec.ts — the §9 wave-0 real-browser click test
// (spec rows 1–16) against the STATIC Storybook build (dist/storybook,
// served the same way the design-system gates serve it). Every row: real
// clicks/keys/touches, geometry assertions, and a screenshot as evidence
// under uat/evidence/2026-09-26-side-panel-wave0/.
//
// Executed with: STORYBOOK_STATIC_DIR=dist/storybook npx playwright
// side-panel-shell-demo.spec.ts --config=playwright.side-panel-demo.config.ts

import { test, expect, type Page } from '@playwright/test'

const ROW_1280 = 1280
const CEILING_1280 = Math.min(ROW_1280 * 0.7, ROW_1280 - 0 - 360) // 896 (SP-17)
const DEFAULT_1280 = Math.max(320, Math.min(720, Math.min(ROW_1280 * 0.7, ROW_1280 - 360), Math.max(0.45 * ROW_1280, 320))) // 576
const DEFAULT_680 = 320 // panelDefaultWidth(680, 0): the 680px ceiling is exactly 320
const EVIDENCE_DIR = 'uat/evidence/2026-09-27-side-panel-wave0-fixes'
const PANEL_MIN = 320

const STORY = 'side-panel-shell-demo-workspace'
const storyUrl = (story: string, viewport: string) =>
  `iframe.html?id=${STORY}--${story}&viewMode=story&globals=viewport:${viewport}`

async function openStory(page: Page, story: string, viewport: string) {
  await page.goto(`/${storyUrl(story, viewport)}`)
  await expect(page.getByTestId('panel-shell-row')).toBeVisible()
}
/** Current docked panel + chat column widths (rounded). */
async function widths(page: Page) {
  const container = page.getByTestId('side-panel-container')
  await expect(container).toBeVisible()
  const w = (await container.boundingBox())?.width ?? 0
  const chat = page.getByTestId('chat-column')
  const chatW = (await chat.boundingBox())?.width ?? 0
  return { panel: Math.round(w), chat: Math.round(chatW) }
}

/** Drag the separator: dx > 0 rightward (narrows), dx < 0 leftward (widens). */
async function dragSeparator(page: Page, dx: number) {
  const sep = page.getByTestId('panel-resize-separator')
  const box = (await sep.boundingBox())!
  const y = box.y + box.height / 4
  const startX = box.x + box.width / 2
  await page.mouse.move(startX, y)
  await page.mouse.down()
  const steps = 12
  for (let i = 1; i <= steps; i++) {
    await page.mouse.move(startX + dx * (i / steps), y, { steps: 1 })
    await page.mouse.move(startX + dx * (i / steps), y, { steps: 1 })
  }
  await page.mouse.up()
}

/** Screenshot helper (evidence dir). */
async function shot(page: Page, name: string) {
  await page.screenshot({ path: `${EVIDENCE_DIR}/${name}.png`, fullPage: false })
}

/**
 * Toggle a panel through its REAL trigger (§9 rows: "click the tab entry").
 * When the toolbar's container is ≥ 72rem the full strip shows the trigger
 * button; below it (a docked panel narrowed the chat column, or a narrow
 * viewport) the strip is the §11 compact dropdown and the toggle goes
 * through the mirrored menu entry — same handler, same pressed semantics.
 */
async function clickPanelTrigger(page: Page, id: string) {
  const strip = page.getByTestId(`panel-trigger-${id}`)
  if (await strip.isVisible()) {
    await strip.click()
    return
  }
  await page.getByTestId('demo-panels-menu-trigger').click()
  await page.getByTestId(`panel-menu-${id}`).click()
}

/** Same compact/full split for the demo's "Mark Library dirty" control. */
async function markLibraryDirty(page: Page) {
  const strip = page.getByTestId('demo-set-dirty')
  if (await strip.isVisible()) {
    await strip.click()
    return
  }
  await page.getByTestId('demo-panels-menu-trigger').click()
  await page.getByTestId('demo-set-dirty-menu').click()
}

// ─────────────────────────── Docked rows (1280×800) ───────────────────────

test('row 1: Library tab click docks the panel, trigger pressed', async ({ page }) => {
  await openStory(page, 'docked', 'panel1280x800')
  await page.getByTestId('panel-trigger-library').click()
  await expect(page.getByTestId('side-panel')).toBeVisible()
  await expect(page.getByTestId('side-panel-container')).toBeVisible()
  await expect(page.getByTestId('panel-trigger-library')).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByTestId('side-panel-header')).toContainText('Library')
  await shot(page, 'row-01-library-docked')
})

test('row 2: drag toward chat narrows to the 320px floor, chat stays interactive', async ({ page }) => {
  await openStory(page, 'docked', 'panel1280x800')
  await page.getByTestId('panel-trigger-library').click()
  await dragSeparator(page, 2000) // rightward = narrowing
  const w = await widths(page)
  expect(w.panel).toBe(PANEL_MIN)
  expect(w.chat).toBe(ROW_1280 - PANEL_MIN)
  // Chat still interactive: type into the composer.
  await page.getByTestId('chat-input').fill('row 2 chat interactive')
  await expect(page.getByTestId('chat-input')).toHaveValue('row 2 chat interactive')
  await shot(page, 'row-02-narrow-floor-320')
})

test('row 3: drag away widens to the SP-17 ceiling, chat never below 360px', async ({ page }) => {
  await openStory(page, 'docked', 'panel1280x800')
  await page.getByTestId('panel-trigger-library').click()
  await dragSeparator(page, -2000) // leftward = widening
  const w = await widths(page)
  expect(w.panel).toBe(CEILING_1280)
  expect(w.chat).toBe(ROW_1280 - CEILING_1280)
  expect(w.chat).toBeGreaterThanOrEqual(360)
  await shot(page, 'row-03-ceiling-896-chat-384')
})

test('row 4: double-click resets to default; width memory survives close/reopen until reset', async ({ page }) => {
  await openStory(page, 'docked', 'panel1280x800')
  await page.getByTestId('panel-trigger-library').click()
  await dragSeparator(page, -76) // widen 576 -> 652
  // Settle: the commit writes the width memory on release.
  await expect(page.getByTestId('panel-resize-separator')).toHaveAttribute('aria-valuenow', '652')
  await page.getByTestId('panel-close').click()
  await expect(page.getByTestId('side-panel')).toBeHidden()
  await page.getByTestId('panel-trigger-library').click()
  await expect(page.getByTestId('side-panel')).toBeVisible()
  expect((await widths(page)).panel).toBe(652) // width memory (SP-13)
  await page.getByTestId('panel-resize-separator').dblclick() // US-2 AS-3 reset
  await expect(page.getByTestId('panel-resize-separator')).toHaveAttribute('aria-valuenow', String(DEFAULT_1280))
  await page.getByTestId('panel-close').click()
  await page.getByTestId('panel-trigger-library').click()
  await expect(page.getByTestId('side-panel')).toBeVisible()
  expect((await widths(page)).panel).toBe(DEFAULT_1280) // reset persists
  await shot(page, 'row-04-reset-default-576')
})

test('row 5: width memory is per workspace (A vs B)', async ({ page }) => {
  await openStory(page, 'docked', 'panel1280x800')
  await page.getByTestId('panel-trigger-library').click()
  await dragSeparator(page, -76) // 576 -> 652 in workspace A
  await expect(page.getByTestId('panel-resize-separator')).toHaveAttribute('aria-valuenow', '652')
  await page.getByTestId('panel-close').click()
  await page.getByTestId('workspace-switch-ws-beta').click()
  await page.getByTestId('panel-trigger-library').click()
  await expect(page.getByTestId('panel-resize-separator')).toHaveAttribute('aria-valuenow', String(DEFAULT_1280))
  await page.getByTestId('panel-close').click()
  await page.getByTestId('workspace-switch-ws-alpha').click()
  await page.getByTestId('panel-trigger-library').click()
  await expect(page.getByTestId('panel-resize-separator')).toHaveAttribute('aria-valuenow', '652')
  await shot(page, 'row-05-per-workspace-width')
})

// ─────────────────────────── Guard + toggle rows ──────────────────────────

test('row 6: unsaved-edit guard CANCEL keeps Library exactly as it was', async ({ page }) => {
  await openStory(page, 'docked', 'panel1280x800')
  await page.getByTestId('panel-trigger-library').click()
  await markLibraryDirty(page)
  await clickPanelTrigger(page, 'tasks')
  const dialog = page.getByRole('alertdialog')
  await expect(dialog).toBeVisible()
  await expect(dialog).toContainText('Discard unsaved changes?')
  await dialog.locator('[data-confirm-dialog-cancel]').click() // CANCEL
  await expect(dialog).toBeHidden()
  await expect(page.getByTestId('side-panel-header')).toContainText('Library')
  await expect(page.getByTestId('panel-trigger-library')).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByTestId('panel-trigger-tasks')).toHaveAttribute('aria-pressed', 'false')
  await shot(page, 'row-06-guard-cancel-stays')
})

test('row 7: guard CONTINUE leaves Library, Tasks docks, pressed state moves', async ({ page }) => {
  await openStory(page, 'docked', 'panel1280x800')
  await page.getByTestId('panel-trigger-library').click()
  await markLibraryDirty(page)
  await clickPanelTrigger(page, 'tasks')
  const dialog = page.getByRole('alertdialog')
  await expect(dialog).toBeVisible()
  await dialog.getByRole('button', { name: 'Discard' }).click() // CONTINUE
  await expect(dialog).toBeHidden()
  await expect(page.getByTestId('side-panel-header')).toContainText('Tasks')
  await expect(page.getByTestId('panel-content-tasks')).toBeVisible()
  await expect(page.getByTestId('panel-trigger-library')).toHaveAttribute('aria-pressed', 'false')
  await expect(page.getByTestId('panel-trigger-tasks')).toHaveAttribute('aria-pressed', 'true')
  await shot(page, 'row-07-guard-continue-tasks')
})

test('row 8: second Tasks click closes the panel, pressed clears', async ({ page }) => {
  await openStory(page, 'docked', 'panel1280x800')
  await page.getByTestId('panel-trigger-tasks').click() // opens (full strip)
  await expect(page.getByTestId('side-panel')).toBeVisible()
  await clickPanelTrigger(page, 'tasks') // second click closes (compact strip)
  await expect(page.getByTestId('side-panel')).toBeHidden()
  await expect(page.getByTestId('panel-trigger-tasks')).toHaveAttribute('aria-pressed', 'false')
  await shot(page, 'row-08-toggle-close')
})

test('row 9: Browser then Calendar — one at a time, no stranded state', async ({ page }) => {
  await openStory(page, 'docked', 'panel1280x800')
  await page.getByTestId('panel-trigger-browser').click() // opens Browser
  await expect(page.getByTestId('side-panel-header')).toContainText('Browser')
  await clickPanelTrigger(page, 'calendar') // one-at-a-time switch (compact strip)
  await expect(page.getByTestId('side-panel-header')).toContainText('Calendar')
  await expect(page.getByTestId('panel-content-calendar')).toBeVisible()
  await expect(page.getByTestId('panel-trigger-browser')).toHaveAttribute('aria-pressed', 'false')
  await expect(page.getByTestId('panel-trigger-calendar')).toHaveAttribute('aria-pressed', 'true')
  await shot(page, 'row-09-one-at-a-time')
})

// ─────────────────────────── Takeover rows (679×900) ──────────────────────

test('row 10: at 679px Mail takes over — full row, chat inert, no resize handle', async ({ page }) => {
  await page.setViewportSize({ width: 679, height: 900 })
  await openStory(page, 'docked', 'panel679x900')
  await clickPanelTrigger(page, 'mail') // 679px < 72rem: compact strip
  const panel = page.getByTestId('side-panel')
  await expect(panel).toBeVisible()
  await expect(panel).toHaveAttribute('data-takeover', 'true')
  await expect(page.getByTestId('chat-column')).toBeHidden()
  await expect(page.getByTestId('panel-resize-separator')).toHaveCount(0)
  await expect(page.getByTestId('panel-content-mail')).toBeVisible()
  await shot(page, 'row-10-takeover-mail')
})

test('row 11: at exactly 680px the docked floors fit — chat 360, panel 320', async ({ page }) => {
  await page.setViewportSize({ width: 680, height: 900 })
  await openStory(page, 'docked', 'panel680x900')
  await clickPanelTrigger(page, 'library') // 680px < 72rem: compact strip
  const w = await widths(page)
  expect(w.panel).toBe(DEFAULT_680)
  expect(w.chat).toBe(360)
  await shot(page, 'row-11-boundary-exact')
})

test('row 12: phone X closes the takeover panel, chat visible again', async ({ page }) => {
  await page.setViewportSize({ width: 679, height: 900 })
  await openStory(page, 'docked', 'panel679x900')
  await clickPanelTrigger(page, 'mail') // 679px < 72rem: compact strip
  await expect(page.getByTestId('side-panel')).toBeVisible()
  await page.getByTestId('panel-close').click()
  await expect(page.getByTestId('side-panel')).toBeHidden()
  await expect(page.getByTestId('chat-column')).toBeVisible()
  await shot(page, 'row-12-phone-close-x')
})

test('row 13: phone Back closes and history returns to the pre-open entry', async ({ page }) => {
  await page.setViewportSize({ width: 679, height: 900 })
  await openStory(page, 'phone-back', 'panel679x900')
  await page.waitForTimeout(800)
  // Count the APP's pushes: SP-26 pushes exactly ONE history entry per open
  // panel. (history.length itself counts ALL entries — back() moves the
  // pointer without shrinking it — so only the push count and the popstate
  // prove the "returns to the pre-open entry" requirement.)
  await page.evaluate(() => {
    const w = window as typeof window & { __panelPushes?: number }
    w.__panelPushes = 0
    const orig = w.history.pushState.bind(w.history)
    w.history.pushState = (...a) => { w.__panelPushes = (w.__panelPushes ?? 0) + 1; return orig(...a) }
  })
  await clickPanelTrigger(page, 'mail') // 679px < 72rem: compact strip
  await expect(page.getByTestId('side-panel')).toBeVisible()
  await page.waitForTimeout(800)
  expect(await page.evaluate(() => (window as typeof window & { __panelPushes?: number }).__panelPushes)).toBe(1)
  // Back must run a REAL history pop (popstate), not just hide the panel.
  const popped = page.evaluate(() => new Promise<void>((resolve) => window.addEventListener('popstate', () => resolve(), { once: true })))
  await page.getByTestId('panel-back').click()
  await expect(popped).resolves.toBeUndefined()
  await expect(page.getByTestId('side-panel')).toBeHidden()
  await expect(page.getByTestId('chat-column')).toBeVisible()
  await shot(page, 'row-13-phone-back-history')
})

// Rows 14–15 need real touch input (the recognizer listens to touch events
// and native scrolling needs the browser input pipeline): CDP touch points
// on a hasTouch chromium context.
test.use({ hasTouch: true })

declare module '@playwright/test' {}

async function touchSwipe(page: Page, startX: number, startY: number, endX: number) {
  const session = await page.context().newCDPSession(page)
  await session.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: startX, y: startY }] })
  const steps = 8
  for (let i = 1; i <= steps; i++) {
    const x = startX + ((endX - startX) * i) / steps
    await session.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: startY }] })
    // Frame-paced: a back-to-back dispatch in one JS tick coalesces into a
    // single instantaneous jump — the compositor never gets a frame between
    // moves, so native panning (row 15's scroll) never starts.
    await page.waitForTimeout(16)
  }
  await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
}

test('row 14: swipe right from the left edge closes the takeover panel', async ({ page }) => {
  await page.setViewportSize({ width: 679, height: 900 })
  await openStory(page, 'docked', 'panel679x900')
  await clickPanelTrigger(page, 'mail') // 679px < 72rem: compact strip
  await expect(page.getByTestId('side-panel')).toBeVisible()
  // Start INSIDE the 24px edge zone, drag right well past 96px.
  await touchSwipe(page, 12, 450, 12 + 160)
  await expect(page.getByTestId('side-panel')).toBeHidden()
  await expect(page.getByTestId('chat-column')).toBeVisible()
  await shot(page, 'row-14-swipe-closes')
})

test('row 15: swipe inside the mail carousel scrolls content, never closes', async ({ page }) => {
  await page.setViewportSize({ width: 679, height: 900 })
  await openStory(page, 'docked', 'panel679x900')
  await clickPanelTrigger(page, 'mail') // 679px < 72rem: compact strip
  await expect(page.getByTestId('side-panel')).toBeVisible()
  const carousel = page.getByTestId('mail-carousel')
  await expect(carousel).toBeVisible()
  const box = (await carousel.boundingBox())!
  const y = box.y + box.height / 2
  // A horizontal carousel scrolls when the finger drags LEFTWARD (revealing
  // the cards to the right) — a rightward drag at scrollLeft 0 has nothing
  // to scroll. Start well inside the strip (past the 24px edge zone) and
  // drag left across it: the strip scrolls, the panel must NOT close.
  const startX = box.x + Math.min(260, box.width - 80)
  await touchSwipe(page, startX, y, startX - 220)
  await expect(page.getByTestId('side-panel')).toBeVisible() // must NOT close
  await expect
    .poll(() => carousel.evaluate((el) => el.scrollLeft), { timeout: 5_000 })
    .toBeGreaterThan(0) // and the strip must actually scroll
  await shot(page, 'row-15-carousel-scrolls')
})

// ─────────────────────────── Keyboard walkthrough ──────────────────────────

test('row 16: keyboard — tab to separator, arrows, Home/End, Escape with focus return; Browser Escape releases driving (SP-19)', async ({ page }) => {
  await openStory(page, 'docked', 'panel1280x800')
  await page.getByTestId('panel-trigger-library').click()
  await expect(page.getByTestId('side-panel')).toBeVisible()

  // Tab to the separator: with the panel docked the strip is the compact
  // dropdown, so the walk crosses the compact trigger, chat input, send —
  // then the separator. Walk defensively (cap 30) instead of hardcoding.
  await page.getByTestId('panel-trigger-library').focus()
  const sep = page.getByTestId('panel-resize-separator')
  let pressed = 0
  for (; pressed < 30; pressed++) {
    await page.keyboard.press('Tab')
    if (await sep.evaluate((el) => el === document.activeElement)) break
  }
  expect(pressed).toBeLessThan(30)
  await expect(sep).toBeFocused()
  await expect(sep).toHaveAttribute('aria-valuenow', String(DEFAULT_1280)) // 576 default

  // 16px steps, mirrored: right narrows, left widens (US-9 AS-2).
  await page.keyboard.press('ArrowRight')
  await expect(sep).toHaveAttribute('aria-valuenow', '560')
  await page.keyboard.press('ArrowLeft')
  await expect(sep).toHaveAttribute('aria-valuenow', String(DEFAULT_1280))
  await page.keyboard.press('Home')
  await expect(sep).toHaveAttribute('aria-valuenow', String(PANEL_MIN)) // 320
  await page.keyboard.press('End')
  await expect(sep).toHaveAttribute('aria-valuenow', String(CEILING_1280)) // 896

  // Keyboard settle (MIN-205: commit 300ms after the last keypress) writes
  // the width memory for this panel's scope (SP-13: user × panel × workspace).
  await page.waitForTimeout(400)
  const stored = await page.evaluate(() =>
    localStorage.getItem('panel-width:demo-user:library:ws-alpha'),
  )
  expect(stored).toBe(String(CEILING_1280))

  // Escape closes with focus returning to the opening trigger (US-9).
  await page.keyboard.press('Escape')
  await expect(page.getByTestId('side-panel')).toBeHidden()
  await expect(page.getByTestId('panel-trigger-library')).toBeFocused()

  // SP-19: Escape on the Browser panel NEVER closes it — it releases driving.
  await clickPanelTrigger(page, 'browser') // Library docked: compact strip
  await expect(page.getByTestId('side-panel')).toBeVisible()
  await page.getByTestId('browser-driving-surface').focus()
  await page.keyboard.press('Escape')
  await expect(page.getByTestId('side-panel')).toBeVisible() // still open
  await expect(page.getByTestId('panel-content-browser')).toContainText('Driving released (Escape)')
  await page.getByTestId('panel-close').click() // only the header ✕ closes it
  await expect(page.getByTestId('side-panel')).toBeHidden()
  await shot(page, 'row-16-keyboard-walkthrough')
})
