/**
 * side-panel-expand-multitab.spec.ts — side-panel-shell-spec.md §9 W4, W5, W7.
 *
 * WRITTEN, NOT EXECUTED in the RED pass. Parse is proved with
 * `npx playwright test --list`. Multi-tab rows (OBS-201).
 *
 * W4 Expand opens the full-page Library and closes the docked panel.
 * W5 Clicking the toggle again focuses the app-opened tab; no second tab.
 * W7 A manually opened full-page tab is not duplicated; the affordance shows.
 */
import { expect, type Page, type TestInfo } from '@playwright/test'
import { readDockSnapshot, dockViolations, toggleWorkspacePanel, type DockPanel } from './fixtures/side-panel-docking'
import { test } from './fixtures/console-errors'
import { newAdminApiContext } from './fixtures/admin-api'
import { softSkip } from './fixtures/skip-tracking'

async function createWorkspace(): Promise<string> {
  const ctx = await newAdminApiContext()
  try {
    const res = await ctx.post('/api/v1/workspaces', {
      data: { name: `E2E expand ${Date.now()}` },
    })
    if (!res.ok()) throw new Error(`createWorkspace ${res.status()}: ${await res.text()}`)
    return ((await res.json()) as { id: string }).id
  } finally {
    await ctx.dispose()
  }
}

async function deleteWorkspace(id: string): Promise<void> {
  const ctx = await newAdminApiContext()
  try {
    await ctx.delete(`/api/v1/workspaces/${encodeURIComponent(id)}`)
  } catch {
    // best-effort
  } finally {
    await ctx.dispose()
  }
}

let workspaceId: string

test.beforeAll(async () => {
  workspaceId = await createWorkspace()
})

test.afterAll(async () => {
  if (workspaceId) await deleteWorkspace(workspaceId)
})

test('W4 — Expand opens full-page Library and closes the docked panel', async ({ page, context }) => {
  await page.goto(`/#/workspaces/${workspaceId}/chat?panel=library`)
  await expect(page.getByTestId('side-panel-header')).toContainText('Library')
  const opened = context.waitForEvent('page')
  await page.getByTestId('panel-expand').click()
  const pop = await opened
  await expect(pop).toHaveURL(/\/library/)
  await expect(page.getByTestId('side-panel')).toHaveCount(0)
})

test('W5 — a second Library toggle focuses the existing tab and does not open another', async ({ page, context }) => {
  await page.goto(`/#/workspaces/${workspaceId}/chat?panel=library`)
  const opened = context.waitForEvent('page')
  await page.getByTestId('panel-expand').click()
  await opened
  const before = context.pages().length
  await page.getByTestId('workspace-tab-media').click()
  await expect.poll(() => context.pages().length).toBe(before)
})

test('W7 — a manually opened Library tab is not duplicated; the switch affordance shows', async ({ page, context }) => {
  // Temporary quarantine: the current CI run failed before passing on retry.
  // https://github.com/elicify-ai/omnipus/issues/1180 — expires 2026-10-10.
  softSkip(test, 'W7 current CI failure on first attempt: https://github.com/elicify-ai/omnipus/issues/1180; expires 2026-10-10')
  await page.goto(`/#/workspaces/${workspaceId}/chat`)
  const manual = await context.newPage()
  await manual.goto(`/#/library?workspace=${workspaceId}`)
  await page.bringToFront()
  await page.getByRole('button', { name: /^library$/i }).click()
  await expect(page.getByText(/already open/i)).toBeVisible()
  await expect(page.getByTestId('side-panel')).toHaveCount(0)
  expect(context.pages().filter((p) => p.url().includes('/library')).length).toBe(1)
})

// Docking recovery RED — native tests, not isolated Board CSS/class strings.
// Oracle: side-panel-shell-spec.md FR-001/003/014; §10 SP-33/37/38/39 and
// founder's 1600×1000 Tasks/Calendar spill report. Real application, router,
// shell, registry, Tasks/Calendar roots and CSS; NO component/network/style
// mocks. Existing ui-3 E2E shard discovers this file without workflow changes.
// Local authenticated execution is NOT authorised by this pack: the default
// global setup writes admin/provider state. RED native criteria are relayed
// by squad-lead to the existing private tester; CI/GREEN/CHECK are separate.
// Planned independent CHECK mutants: screen escapes its body; toolbar/header
// intercepts Close; viewport breakpoint replaces container; Month/Expand loses
// workspace or opens a tab. RED author never mutates production or self-CHECKs.

async function openNativeDock(page: Page, panel: 'tasks' | 'calendar' | 'team') {
  await page.goto(`/#/workspaces/${workspaceId}/chat`)
  await expect(page.getByTestId('workspace-top-bar')).toBeVisible()
  await expect(page.getByTestId('chat-input')).toBeVisible()
  const chat = await page.getByTestId('chat-column').elementHandle()
  if (!chat) throw new Error('BLOCKED: real chat did not render — FR-001')
  const baseline = { path: new URL(page.url()).hash.split('?')[0], tabs: page.context().pages().length, chat }
  expect(baseline.tabs, 'a fresh E2E context starts with one application tab').toBe(1)
  await toggleWorkspacePanel(page, panel)
  await expect(page.getByTestId('side-panel')).toBeVisible()
  await expect(page.getByTestId('side-panel-header')).toContainText(panel === 'tasks' ? 'Tasks' : panel === 'calendar' ? 'Calendar' : 'Team')
  const marker = panel === 'tasks' ? page.getByTestId('tasks-heading')
    : panel === 'calendar' ? page.getByTestId('calendar-toolbar') : page.getByRole('heading', { name: 'Team & delegation', exact: true })
  await expect(marker, 'real registered content must render, not a fake shell frame').toBeVisible()
  if (panel === 'calendar') await expect(page.getByTestId('calendar-grid')).toBeVisible()
  await expect(page.getByRole('dialog'), 'containment is measured with no editor Sheet open').toHaveCount(0)
  await expect(page.getByTestId('fullscreen-panel'), 'a toggle must not silently expand').toHaveCount(0)
  return baseline
}

async function assertSameNativeChat(page: Page, baseline: Awaited<ReturnType<typeof openNativeDock>>, panel?: string) {
  const hash = new URL(page.url()).hash
  expect(hash.split('?')[0], 'the original workspace Chat route stays under the dock').toBe(baseline.path)
  expect(new URLSearchParams(hash.split('?')[1]).get('panel')).toBe(panel ?? null)
  expect(page.context().pages().length, 'no new tab without an explicit Library/Mail/Browser Expand').toBe(baseline.tabs)
  expect(await baseline.chat.evaluate((el) => el.isConnected && el === document.querySelector('[data-testid="chat-column"]')),
    'opening/resizing/closing must preserve the actual chat node').toBe(true)
}

async function recordNativeDock(page: Page, panel: DockPanel, info: TestInfo) {
  const snapshot = await readDockSnapshot(page, panel)
  await info.attach(`${panel}-native-geometry.json`, { body: JSON.stringify(snapshot, null, 2), contentType: 'application/json' })
  await info.attach(`${panel}-native-dock.png`, { body: await page.screenshot(), contentType: 'image/png' })
  return snapshot
}

async function assertNativeHeaderHit(page: Page, id: 'panel-close' | 'panel-expand') {
  const control = page.getByTestId(id)
  await expect(control).toBeVisible()
  const hit = await control.evaluate((button) => {
    const r = button.getBoundingClientRect()
    const top = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2)
    return { width: r.width, height: r.height, onTop: top !== null && button.contains(top), hit: top?.textContent?.slice(0, 100) }
  })
  expect(hit.width).toBeGreaterThan(0)
  expect(hit.height).toBeGreaterThan(0)
  expect(hit.onTop, `${id} must receive an ordinary pointer click; actual hit: ${hit.hit}`).toBe(true)
}

// Deliberate coverage gaps: agent token counts/APIs/Graph layout unchanged;
// this bounded recovery pack does not replace Mail/Library/Browser's suites or
// the final corrected-static-app + real-backend, no-mock acceptance campaign.
test.describe('native docking regression', () => {
  test.use({ viewport: { width: 1600, height: 1000 } })
  test.describe.configure({ retries: 0 })

  for (const panel of ['tasks', 'calendar'] as const) {
    for (const width of ['default', 'Home', 'End'] as const) {
      test(`${panel} stays inside its real dock body at ${width} width, with working Close and unchanged chat`, async ({ page }, info) => {
        const baseline = await openNativeDock(page, panel)
        if (width !== 'default') {
          const separator = page.getByRole('separator', { name: `Resize ${panel === 'tasks' ? 'Tasks' : 'Calendar'} panel`, exact: true })
          await separator.focus()
          await separator.press(width)
          const bound = await separator.getAttribute(width === 'Home' ? 'aria-valuemin' : 'aria-valuemax')
          if (bound === null) throw new Error('BLOCKED: resize separator missing native bound — FR-003')
          await expect(separator).toHaveAttribute('aria-valuenow', bound)
        }
        const snapshot = await recordNativeDock(page, panel, info)
        expect(snapshot.viewport, 'window stays wide and constant while PANEL width changes').toEqual({ width: 1600, height: 1000 })
        expect(dockViolations(snapshot), 'FR-001: actual content/toolbar/Plans-or-grid and header hits must fit the real body').toEqual([])
        if (panel === 'calendar') await expect(page.getByTestId('calendar-view-timeGridWeek')).toHaveAttribute('aria-pressed', 'true')
        await assertSameNativeChat(page, baseline, panel)
        await assertNativeHeaderHit(page, 'panel-close')
        await page.getByTestId('panel-close').click() // never force-click an intercepted header
        await expect(page.getByTestId('side-panel')).toHaveCount(0)
        await assertSameNativeChat(page, baseline)
      })
    }
  }

  test('instrument rejects an escaped rectangle and an intercepted header without changing the real DOM', async ({ page }, info) => {
    await openNativeDock(page, 'tasks')
    const actual = await recordNativeDock(page, 'tasks', info)
    // This is checker calibration ONLY, not a passing native-behaviour claim:
    // body/frame/chat remain real; the copied packet establishes a known-good
    // predicate input, then introduces TWO known-bad inputs, never CSS fixes.
    const control = { ...actual, content: actual.body, toolbar: actual.body, detail: actual.body,
      controls: actual.controls.map((item) => ({ ...item, onTop: true })) }
    expect(dockViolations(control), 'positive instrument control').toEqual([])
    const escaped = { ...control, content: { ...control.body, left: control.body.left - 100, width: control.body.width + 100 } }
    expect(dockViolations(escaped)).toContain(`content.left ${escaped.content.left} < body.left ${control.body.left}`)
    expect(dockViolations(escaped)).toContain('registered content paints across chat')
    const covered = { ...control, controls: control.controls.map((item) => ({ ...item, onTop: false, hit: 'Plans header' })) }
    expect(dockViolations(covered)).toEqual(['Close Tasks is intercepted by Plans header', 'Expand Tasks panel is intercepted by Plans header'])
  })

  for (const panel of ['tasks', 'calendar', 'team'] as const) {
    test(`${panel} real toggle then Expand carries workspace context into this tab's chrome-less route`, async ({ page }, info) => {
      const baseline = await openNativeDock(page, panel)
      await assertNativeHeaderHit(page, 'panel-expand')
      await page.getByTestId('panel-expand').click() // GENERATED URL, never a pre-filled valid full-screen link
      await page.waitForURL(new RegExp(`/panel/${panel}`))
      const hash = new URL(page.url()).hash
      await info.attach(`${panel}-generated-expand.json`, { body: JSON.stringify({ hash, tabs: page.context().pages().length }), contentType: 'application/json' })
      await info.attach(`${panel}-generated-expand.png`, { body: await page.screenshot(), contentType: 'image/png' })
      expect(hash.split('?')[0]).toBe(`#/panel/${panel}`)
      expect(new URLSearchParams(hash.split('?')[1]).get('workspace'), 'SP-38: Expand must carry the SAME workspace, not an incomplete link').toBe(workspaceId)
      expect(page.context().pages().length, 'SP-38: same-tab route, not a new tab').toBe(baseline.tabs)
      await expect(page.getByText("Can't open this panel", { exact: true })).toHaveCount(0)
      await expect(page.getByTestId('fullscreen-panel')).toBeVisible()
      const marker = panel === 'tasks' ? page.getByTestId('tasks-heading')
        : panel === 'calendar' ? page.getByTestId('calendar-toolbar') : page.getByRole('heading', { name: 'Team & delegation', exact: true })
      await expect(marker, 'the expanded route renders the real registered workspace content').toBeVisible()
      await expect(page.getByTestId('workspace-top-bar'), 'expanded presentation has no app chrome').toHaveCount(0)
      await page.getByRole('button', { name: /back to chat/i }).click()
      await expect(page.getByTestId('side-panel')).toBeVisible()
      expect(new URL(page.url()).hash.split('?')[0]).toBe(baseline.path)
      expect(new URLSearchParams(new URL(page.url()).hash.split('?')[1]).get('panel')).toBe(panel)
      expect(page.context().pages().length).toBe(baseline.tabs)
    })
  }
})

test.describe('native docking controls — SP-33/37/39', () => {
  test.use({ viewport: { width: 1600, height: 1000 } })
  test.describe.configure({ retries: 0 })

  test('SP-37 New Plan remains an intentional WINDOW-edge Sheet, never clipped into the dock', async ({ page }, info) => {
    const baseline = await openNativeDock(page, 'tasks')
    await page.getByRole('button', { name: 'New Plan', exact: true }).click()
    const editor = page.getByRole('dialog', { name: 'New plan', exact: true })
    await expect(editor).toBeVisible()
    await expect.poll(async () => {
      const bounds = await editor.boundingBox()
      return bounds ? Math.abs(bounds.x + bounds.width - 1600) <= 1 && Math.abs(bounds.y) <= 1 : false
    }, { message: 'SP-37 editor is anchored to the actual window right/top, not the dock body' }).toBe(true)
    expect(await editor.evaluate((el) => document.querySelector('[data-testid="side-panel"]')?.contains(el)),
      'SP-37 keeps the real Sheet portal outside the dock content').toBe(false)
    await info.attach('intentional-window-edge-plan-editor.png', { body: await page.screenshot(), contentType: 'image/png' })
    await page.keyboard.press('Escape')
    await expect(editor).toHaveCount(0)
    await expect(page.getByTestId('side-panel')).toBeVisible()
    await assertSameNativeChat(page, baseline, 'tasks')
  })

  for (const viewportWidth of [679, 680, 681, 1600]) {
    test(`SP-39 Month stays in Calendar at ${viewportWidth}px; takeover only BELOW 680`, async ({ page }, info) => {
      // FR-003's exact takeover boundary: min-1, min, min+1 plus the actual
      // founder reproduction window. No viewport enlargement for Month.
      await page.setViewportSize({ width: viewportWidth, height: 1000 })
      const baseline = await openNativeDock(page, 'calendar')
      await expect(page.getByTestId('calendar-view-timeGridWeek')).toHaveAttribute('aria-pressed', 'true')
      await page.getByTestId('calendar-view-dayGridMonth').click()
      await expect(page.getByTestId('calendar-view-dayGridMonth')).toHaveAttribute('aria-pressed', 'true')
      const month = page.getByTestId('calendar-month-grid')
      await expect(month).toBeVisible()
      const snapshot = await recordNativeDock(page, 'calendar', info)
      const monthBounds = await month.boundingBox()
      if (!monthBounds) throw new Error('BLOCKED: real Month grid has no native bounds — SP-39')
      expect(monthBounds.width).toBeGreaterThan(0)
      expect(monthBounds.height).toBeGreaterThan(0)
      expect(monthBounds.x).toBeGreaterThanOrEqual(snapshot.body.left - 1)
      expect(monthBounds.y).toBeGreaterThanOrEqual(snapshot.body.top - 1)
      expect(monthBounds.x + monthBounds.width).toBeLessThanOrEqual(snapshot.body.right + 1)
      expect(monthBounds.y + monthBounds.height).toBeLessThanOrEqual(snapshot.body.bottom + 1)
      expect(snapshot.viewport).toEqual({ width: viewportWidth, height: 1000 })
      expect(snapshot.takeover).toBe(viewportWidth < 680)
      expect(await page.getByTestId('panel-resize-separator').count()).toBe(viewportWidth < 680 ? 0 : 1)
      if (viewportWidth < 680) await expect(page.getByTestId('chat-column')).toHaveAttribute('inert', '')
      expect(dockViolations(snapshot), 'Month does not escape to another/wider surface or cover the shell header').toEqual([])
      await assertSameNativeChat(page, baseline, 'calendar')
    })
  }

  test('SP-33 Board changes actual column geometry with PANEL resize at one constant wide window', async ({ page }, info) => {
    // 2400px is a test INPUT, not a product breakpoint: it gives the resize
    // ceiling room above the spec's ≈970px full-board content width. At the
    // original 1600px window the ceiling is narrower than a full board.
    await page.setViewportSize({ width: 2400, height: 1000 })
    const baseline = await openNativeDock(page, 'tasks')
    const separator = page.getByRole('separator', { name: 'Resize Tasks panel', exact: true })
    const groups = page.getByTestId('side-panel').getByRole('group', { name: / column$/ })
    const labels = ['Inbox column', 'Next column', 'In Progress column', 'Blocked column', 'Done column', 'Failed column']
    await expect(groups).toHaveCount(labels.length)
    expect(await groups.evaluateAll((els) => els.map((el) => el.getAttribute('aria-label')))).toEqual(labels)
    await separator.focus()
    await separator.press('End')
    await expect.poll(async () => (await readDockSnapshot(page, 'tasks')).body.width).toBeGreaterThan(970)
    const wide = await groups.evaluateAll((els) => els.map((el) => ({ x: el.getBoundingClientRect().x, y: el.getBoundingClientRect().y, width: el.getBoundingClientRect().width, height: el.getBoundingClientRect().height })))
    for (let i = 0; i < wide.length; i += 1) {
      expect(wide[i].width).toBeGreaterThan(0)
      expect(wide[i].height).toBeGreaterThan(0)
      expect(Math.abs(wide[i].y - wide[0].y)).toBeLessThanOrEqual(1)
      if (i > 0) expect(wide[i].x).toBeGreaterThan(wide[i - 1].x)
    }
    await separator.press('Home')
    await expect(separator).toHaveAttribute('aria-valuenow', '320')
    const narrow = await groups.evaluateAll((els) => els.map((el) => ({ x: el.getBoundingClientRect().x, y: el.getBoundingClientRect().y, width: el.getBoundingClientRect().width, height: el.getBoundingClientRect().height })))
    for (let i = 0; i < narrow.length; i += 1) {
      expect(narrow[i].width).toBeGreaterThan(0)
      expect(narrow[i].height).toBeGreaterThan(0)
      expect(Math.abs(narrow[i].x - narrow[0].x), labels[i]).toBeLessThanOrEqual(1)
      if (i > 0) expect(narrow[i].y, labels[i]).toBeGreaterThan(narrow[i - 1].y)
    }
    const snapshot = await recordNativeDock(page, 'tasks', info)
    expect(snapshot.viewport).toEqual({ width: 2400, height: 1000 })
    expect(dockViolations(snapshot)).toEqual([])
    await assertSameNativeChat(page, baseline, 'tasks')
  })
})
