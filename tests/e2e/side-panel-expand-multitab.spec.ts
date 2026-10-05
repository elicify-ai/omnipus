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
// workspace/context or fails PE1's NEW-tab requirement. RED author never
// mutates production or self-CHECKs. PE1 supersedes ONLY the same-tab Expand
// cases below; containment, phone, Month and editor controls remain unchanged.

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

async function assertSameNativeChat(page: Page, baseline: Awaited<ReturnType<typeof openNativeDock>>, panel?: string, expectedTabs = baseline.tabs) {
  const hash = new URL(page.url()).hash
  expect(hash.split('?')[0], 'the original workspace Chat route stays under the dock').toBe(baseline.path)
  expect(new URLSearchParams(hash.split('?')[1]).get('panel')).toBe(panel ?? null)
  expect(page.context().pages().length, 'only an explicit Expand may add its ONE child tab (PE1)').toBe(expectedTabs)
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
    test(`${panel} real toggle then Expand opens a NEW workspace child; reuse does not reload; Back restores source`, async ({ page }, info) => {
      // PE1 replaces only the former same-tab expectation. This observes the
      // ACTUAL returned child, not page.goto(a preset valid target) or a mock.
      const baseline = await openNativeDock(page, panel)
      await assertNativeHeaderHit(page, 'panel-expand')
      const opened = page.waitForEvent('popup')
      await page.getByTestId('panel-expand').click()
      const child = await opened
      await child.waitForURL(new RegExp(`/panel/${panel}\\?`))
      const hash = new URL(child.url()).hash
      const search = new URLSearchParams(hash.split('?')[1])
      expect(hash.split('?')[0]).toBe(`#/panel/${panel}`)
      expect(search.get('workspace'), 'PE1: generated child keeps the SAME workspace').toBe(workspaceId)
      expect(search.get('popout'), 'FR-008/018: generated opener-owned identity').toMatch(/^[A-Za-z0-9-]+$/)
      expect([...search.keys()].sort(), 'codec workspace plus opener-owned popout, no session/auth leakage').toEqual(['popout', 'workspace'])
      expect(page.context().pages().length, 'PE1: actual source + exactly ONE new child').toBe(baseline.tabs + 1)
      expect(await child.evaluate(() => ({ opener: window.opener, name: window.name })),
        'FR-008: severed opener and no stable window name').toEqual({ opener: null, name: '' })
      await expect(page.getByTestId('side-panel'), 'source closes only after the child opens').toHaveCount(0)
      await assertSameNativeChat(page, baseline, undefined, baseline.tabs + 1)
      await expect(child.getByText("Can't open this panel", { exact: true })).toHaveCount(0)
      await expect(child.getByText('Something went wrong', { exact: true })).toHaveCount(0)
      await expect(child.getByTestId('fullscreen-panel')).toBeVisible()
      const marker = panel === 'tasks' ? child.getByTestId('tasks-heading')
        : panel === 'calendar' ? child.getByTestId('calendar-toolbar') : child.getByRole('heading', { name: 'Team & delegation', exact: true })
      await expect(marker, 'authenticated child renders REAL meaningful content').toBeVisible()
      if (panel === 'tasks') await expect(child.getByRole('heading', { name: 'Plans', exact: true })).toBeVisible()
      if (panel === 'calendar') await expect(child.getByTestId('calendar-grid')).toBeVisible()
      await expect(child.getByTestId('workspace-top-bar'), 'expanded presentation has no app chrome').toHaveCount(0)
      const contentNode = await marker.elementHandle()
      if (!contentNode) throw new Error(`BLOCKED: ${panel} child content did not render — PE1`)
      await info.attach(`${panel}-generated-expand.json`, { body: JSON.stringify({ sourceHash: new URL(page.url()).hash, childHash: hash, tabs: page.context().pages().length }), contentType: 'application/json' })
      await info.attach(`${panel}-generated-source.png`, { body: await page.screenshot(), contentType: 'image/png' })
      await info.attach(`${panel}-generated-child.png`, { body: await child.screenshot(), contentType: 'image/png' })

      await page.bringToFront()
      await toggleWorkspacePanel(page, panel)
      await expect(page.getByTestId('side-panel'), 'same-scope re-entry focuses, never duplicates the dock').toHaveCount(0)
      expect(page.context().pages().length).toBe(baseline.tabs + 1)
      expect(child.url()).toContain(hash)
      expect(await contentNode.evaluate((node) => node.isConnected),
        'FR-009: retained child content survives reuse; no blanking/re-navigation').toBe(true)
      await expect(marker).toBeVisible()

      const closed = child.waitForEvent('close')
      await child.getByRole('button', { name: /back to chat/i }).click()
      await closed
      await expect(page.getByTestId('side-panel'), 'FR-018: child Back re-docks ONLY in its source').toBeVisible()
      await assertSameNativeChat(page, baseline, panel)
      const restored = panel === 'tasks' ? page.getByTestId('tasks-heading')
        : panel === 'calendar' ? page.getByTestId('calendar-toolbar') : page.getByRole('heading', { name: 'Team & delegation', exact: true })
      await expect(restored).toBeVisible()
      await info.attach(`${panel}-returned-source.png`, { body: await page.screenshot(), contentType: 'image/png' })
    })

    test(`${panel} actual AppShell manual-tab affordance is VISIBLE; real Switch never duplicates; leave clears it`, async ({ page, context }, info) => {
      // Q1 B: visibility transferred from unit STORE proof, not removed.
      // Native application/backend only. Obtain the address from an ACTUAL
      // first Expand; never hand-complete a URL to hide broken transport.
      const baseline = await openNativeDock(page, panel)
      const opened = page.waitForEvent('popup')
      await page.getByTestId('panel-expand').click()
      const ownedChild = await opened
      await ownedChild.waitForURL(new RegExp(`/panel/${panel}\\?`))
      const generated = new URL(ownedChild.url())
      const query = new URLSearchParams(generated.hash.split('?')[1])
      expect(query.get('workspace')).toBe(workspaceId)
      expect(query.get('popout')).toMatch(/^[A-Za-z0-9-]+$/)
      query.delete('popout') // a MANUAL tab has no opener-owned lifecycle tag
      generated.hash = `${generated.hash.split('?')[0]}?${query.toString()}`
      const closed = ownedChild.waitForEvent('close')
      await ownedChild.getByRole('button', { name: /back to chat/i }).click()
      await closed
      await expect(page.getByTestId('side-panel')).toBeVisible()
      await assertNativeHeaderHit(page, 'panel-close')
      await page.getByTestId('panel-close').click()
      await expect(page.getByTestId('side-panel')).toHaveCount(0)
      await assertSameNativeChat(page, baseline)

      const manual = await context.newPage()
      await manual.goto(generated.href)
      const marker = panel === 'tasks' ? manual.getByTestId('tasks-heading')
        : panel === 'calendar' ? manual.getByTestId('calendar-toolbar') : manual.getByRole('heading', { name: 'Team & delegation', exact: true })
      await expect(marker).toBeVisible()
      const contentNode = await marker.elementHandle()
      if (!contentNode) throw new Error(`BLOCKED: manual ${panel} real content missing — FR-009`)
      await page.bringToFront()
      await toggleWorkspacePanel(page, panel)
      const title = panel === 'tasks' ? 'Tasks' : panel === 'team' ? 'Team' : 'Calendar'
      await expect(page.getByText(`${title} is already open in another tab — switch.`, { exact: true }),
        'FR-009: actual APPLICATION toast is visible, never a store-only claim').toBeVisible()
      await expect(page.getByRole('button', { name: 'Switch', exact: true })).toBeVisible()
      await expect(page.getByTestId('side-panel')).toHaveCount(0)
      expect(context.pages().length).toBe(baseline.tabs + 1)
      await info.attach(`${panel}-manual-affordance-source.png`, { body: await page.screenshot(), contentType: 'image/png' })
      await page.getByRole('button', { name: 'Switch', exact: true }).click()
      // Programmatic focus is best-effort by §8.3; no guaranteed frontmost
      // assertion. The REAL action must preserve target content/address and
      // cannot open a duplicate tab or local dock.
      expect(manual.url()).toBe(generated.href)
      expect(await contentNode.evaluate((node) => node.isConnected)).toBe(true)
      await expect(marker).toBeVisible()
      await assertSameNativeChat(page, baseline, undefined, baseline.tabs + 1)
      await expect(page.getByTestId('side-panel')).toHaveCount(0)
      await info.attach(`${panel}-manual-switch-child.png`, { body: await manual.screenshot(), contentType: 'image/png' })
      await manual.close()
      await page.bringToFront()
      // Spec §8.3 / US-6.4: when the manual tab closes its presence clears and
      // the 5 s focus fallback (panelTabSwitch.ts::showPanelTabSwitch ->
      // panelTabPresence.ts::armPanelFocusFallback) RE-OPENS the dock on its
      // own — no click. Oracles: PanelTabPresenceBridge.advisory.test.tsx and
      // PanelTabFocusFallback.regression.test.tsx.
      await expect(page.getByTestId('side-panel'), 'leave clears exclusive presence; the focus fallback re-opens the dock without a click').toBeVisible({ timeout: 15_000 })
      await assertSameNativeChat(page, baseline, panel)
      const alreadyOpen = page.getByText(`${title} is already open in another tab — switch.`, { exact: true })
      await expect(alreadyOpen, 'presence/affordance is cleared: no stale already-open toast').toHaveCount(0)
      // The same entry now toggles normally: closes, then opens again with no
      // exclusive-presence redirect.
      await toggleWorkspacePanel(page, panel)
      await expect(page.getByTestId('side-panel'), 'presence cleared: entry toggle closes the restored dock').toHaveCount(0)
      await toggleWorkspacePanel(page, panel)
      await expect(page.getByTestId('side-panel'), 'presence cleared: same entry opens normally').toBeVisible()
      await expect(alreadyOpen).toHaveCount(0)
      await assertSameNativeChat(page, baseline, panel)
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
