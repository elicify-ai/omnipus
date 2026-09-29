/**
 * side-panel-shell-spec.md FR-009 / SP-9 / SP-18 / SP-30, click-test W6:
 * the sidebar Library entry reuses and focuses an app-opened full-screen tab.
 * Two real Chromium pages are necessary: jsdom's Window.focus has no browser
 * background-tab policy. Fail as BLOCKED if this browser cannot distinguish
 * foreground from background before the click; never count that as product RED.
 */
import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { newAdminApiContext } from './fixtures/admin-api'

let workspaceId: string

test.describe.configure({ retries: 0 })

test.beforeAll(async () => {
  const api = await newAdminApiContext()
  try {
    const created = await api.post('/api/v1/workspaces', {
      data: { name: `E2E sidebar focus ${Date.now()}` },
    })
    expect(created.ok(), `workspace setup failed: ${created.status()}`).toBe(true)
    workspaceId = ((await created.json()) as { id: string }).id
  } finally {
    await api.dispose()
  }
})

test.afterAll(async () => {
  if (!workspaceId) return
  const api = await newAdminApiContext()
  try {
    const current = await api.get(`/api/v1/workspaces/${encodeURIComponent(workspaceId)}`)
    expect(current.ok(), `workspace teardown read failed: ${current.status()}`).toBe(true)
    const { revision } = (await current.json()) as { revision?: string }
    if (!revision) throw new Error('workspace teardown needs the current revision')
    const deleted = await api.delete(`/api/v1/workspaces/${encodeURIComponent(workspaceId)}?revision=${encodeURIComponent(revision)}`)
    expect(deleted.ok(), `workspace teardown failed: ${deleted.status()} ${await deleted.text()}`).toBe(true)
  } finally {
    await api.dispose()
  }
})

test('sidebar Library switches focus to its already-open full-screen tab', async ({ page, context }) => {
  await page.goto(`/#/workspaces/${workspaceId}/chat`)
  // Open via the sidebar so Expand and the second sidebar click refer to the
  // SAME Library identity (the sidebar starts at Library's app/virtual root).
  await page.locator('#sidebar-hamburger').click()
  await expect(page.getByTestId('sidebar-library-button')).toBeVisible()
  await page.getByTestId('sidebar-library-button').click()
  await expect(page.getByTestId('side-panel-header')).toContainText('Library')
  const opened = context.waitForEvent('page')
  await page.getByTestId('panel-expand').click()
  const fullScreen = await opened
  await expect(fullScreen.getByTestId('library-panel-fullscreen')).toBeVisible()
  await expect(fullScreen).toHaveURL(/\/#\/panel\/library\?/)
  await expect(page.getByTestId('side-panel')).toHaveCount(0)
  expect(context.pages().length, 'Expand must have opened exactly one other page').toBe(2)

  await page.bringToFront()
  const [sourceBefore, targetBefore] = await Promise.all([
    page.evaluate(() => ({ focused: document.hasFocus(), visibility: document.visibilityState })),
    fullScreen.evaluate(() => ({ focused: document.hasFocus(), visibility: document.visibilityState })),
  ])
  // Chromium may report hasFocus()=true for BOTH pages, even when headed.
  // In that case visibility is usable only if it independently distinguishes them.
  const focusSignal = sourceBefore.focused && !targetBefore.focused
    ? 'focus'
    : sourceBefore.visibility === 'visible' && targetBefore.visibility === 'hidden'
      ? 'visibility'
      : null
  await page.locator('#sidebar-hamburger').click()
  await expect(page.getByTestId('sidebar-library-button')).toBeVisible()
  await page.getByTestId('sidebar-library-button').click()
  expect(context.pages().length, 'reusing the tab must not open a third page').toBe(2)
  await expect(page.getByTestId('side-panel')).toHaveCount(0)
  await expect(fullScreen).toHaveURL(/\/#\/panel\/library\?/)
  if (!focusSignal) {
    throw new Error(`BLOCKED: Chromium cannot distinguish foreground tabs after bringToFront: source=${JSON.stringify(sourceBefore)} target=${JSON.stringify(targetBefore)}`)
  }
  await expect.poll(async () => Promise.all([
    page.evaluate((signal) => signal === 'focus' ? document.hasFocus() : document.visibilityState === 'visible', focusSignal),
    fullScreen.evaluate((signal) => signal === 'focus' ? document.hasFocus() : document.visibilityState === 'visible', focusSignal),
  ]), {
    message: 'the existing Library tab must become foreground and the source must become background',
    timeout: 2_000,
  }).toEqual([false, true])
})
