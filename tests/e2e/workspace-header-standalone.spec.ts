// Oracles: founder fix round LIVE-1 / F1 / F2 / CR-1 and R44/E requirements.
// Native styled SPA, registered panels, router and isolated REAL gateway.
// Only standalone matchMedia is emulated; no API data/component/style mocks.
import { test, expect, type Browser, type BrowserContext, type Page, type TestInfo } from '@playwright/test'
import { existsSync } from 'node:fs'
import { resolve, sep } from 'node:path'
import { GatewayProcess } from './fixtures/gateway-process'
import { toggleWorkspacePanel } from './fixtures/side-panel-docking'
import type { Workspace, WorkspaceUpdateRequest, LibraryContentRequest, LibraryEntry } from '../../src/lib/api/generated/openapi-types'

// The isolated fixture owns authentication. Never re-login to the shared
// gateway or reuse its account/session, even during a targeted local run.
test.use({ storageState: { cookies: [], origins: [] } })
test.describe.configure({ retries: 0 })

let gateway: GatewayProcess
let workspaceId: string
const fileName = 'Header-regression.txt'
const fileText = 'Header regression selected file'
const contexts: BrowserContext[] = []

test.beforeAll(async () => {
  gateway = await GatewayProcess.start({ model: 'deepseek/deepseek-v4.1-flash' })
  const listed = await gateway.apiFetch<Workspace[]>('GET', '/api/v1/workspaces')
  expect(listed.ok, `Workspace listing failed (${listed.status})`).toBe(true)
  const workspace = listed.body.find((entry) => entry.is_default) ?? listed.body[0]
  expect(workspace, 'Onboarding must create a real default workspace').toBeDefined()
  if (!workspace) throw new Error('BLOCKED: isolated gateway has no workspace')
  workspaceId = workspace.id
  const update: WorkspaceUpdateRequest = { revision: workspace.revision, name: 'A' }
  const renamed = await gateway.apiFetch<Workspace>('PUT', `/api/v1/workspaces/${workspaceId}`, update)
  expect(renamed.ok, `Workspace naming failed (${renamed.status})`).toBe(true)
  expect(renamed.body.name).toBe('A')
  const content: LibraryContentRequest = { path: fileName, content: fileText, expect_version: 'v1:absent' }
  const seeded = await gateway.apiFetch<LibraryEntry>('PUT', `/api/v1/library/${workspaceId}/content`, content)
  expect(seeded.ok, `Library fixture creation failed (${seeded.status})`).toBe(true)
  expect(seeded.body.path).toBe(fileName)
})
test.afterEach(async () => {
  for (const context of contexts.splice(0)) await context.close()
})
test.afterAll(async () => { await gateway?.stop() })

async function realPage(browser: Browser, standalone = false, width = 1440) {
  const context = await browser.newContext({
    baseURL: gateway.baseURL,
    storageState: await gateway.browserStorageState(),
    viewport: { width, height: 900 },
  })
  contexts.push(context)
  // Chromium's Local Network Access permission is required when the main
  // document is supplied by the static-SPA route edge but its WebSocket is
  // real loopback traffic. Grant that origin's native permission; never
  // suppress console errors or disable the browser's security checks.
  await context.grantPermissions(['local-network-access'], { origin: gateway.baseURL })
  if (standalone) {
    // Explicit browser-process edge: other media queries stay native. This
    // must never become a flag/global set by application production code.
    await context.addInitScript(() => {
      const nativeMatchMedia = window.matchMedia.bind(window)
      window.matchMedia = (query: string): MediaQueryList => query === '(display-mode: standalone)' ? {
        matches: true, media: query, onchange: null,
        addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent() { return true },
      } : nativeMatchMedia(query)
    })
  }
  // A local binary may embed an older SPA. As in mail-panel.spec.ts, serve
  // the exact newly built static frontend at this gateway's origin while
  // leaving every API/WebSocket request with the real isolated backend.
  const spaDirectory = process.env.OMNIPUS_HEADER_E2E_SPA_DIR
  if (spaDirectory) {
    const root = resolve(spaDirectory)
    if (!existsSync(resolve(root, 'index.html'))) throw new Error('BLOCKED: requested header SPA build is missing')
    await context.route('**/*', async (route) => {
      const url = new URL(route.request().url())
      if (url.origin !== gateway.baseURL || url.pathname.startsWith('/api/')) return route.continue()
      const asset = resolve(root, url.pathname === '/' ? 'index.html' : url.pathname.slice(1))
      if (!(asset.startsWith(root + sep) && existsSync(asset))) return route.continue()
      await route.fulfill({ path: asset })
    })
  }
  const page = await context.newPage()
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()) })
  return { page, context, errors }
}

async function openChat(page: Page, panel?: 'tasks' | 'library') {
  await page.goto(`/#/workspaces/${workspaceId}/chat${panel ? `?panel=${panel}` : ''}`)
  await expect(page.getByTestId('workspace-top-bar')).toBeVisible()
  await page.evaluate(() => { document.documentElement.style.fontSize = '14px' })
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).fontSize)).toBe('14px')
  if (panel) await expect(page.getByTestId('side-panel-header')).toContainText(panel === 'tasks' ? 'Tasks' : 'Library')
}

async function screenshot(page: Page, info: TestInfo, name: string) {
  await info.attach(name, { body: await page.screenshot(), contentType: 'image/png' })
}

test('F1: 1440x900 at 14px, sidebar closed and Tasks open keeps the full labelled row and browser control', async ({ browser }, info) => {
  const { page, errors } = await realPage(browser)
  await openChat(page)
  const header = page.getByTestId('workspace-top-bar')
  await expect(header.getByRole('button', { name: 'Show sidebar', exact: true })).toBeVisible()
  await page.getByTestId('workspace-tab-board').click()
  await expect(page.getByTestId('tasks-heading')).toBeVisible()
  await expect(page.getByTestId('workspace-header-entries')).toHaveAttribute('data-mode', 'full')
  for (const label of ['Tasks', 'Calendar', 'Library', 'Mail', 'Team']) {
    await expect(header.getByRole('button', { name: label, exact: true })).toBeVisible()
    await expect(header.getByRole('button', { name: label, exact: true })).toHaveText(label)
  }
  await expect(header.getByRole('button', { name: 'Show sidebar', exact: true })).toHaveCount(1)
  await expect(page.getByTestId('workspace-view-switcher')).toHaveCount(0)
  const menuShape = await header.getByRole('button', { name: 'Show sidebar', exact: true }).locator('svg').innerHTML()
  const menuIcons = await header.getByRole('button').evaluateAll((buttons, shape) => buttons.filter((button) => button.querySelector('svg')?.innerHTML === shape).map((button) => button.getAttribute('aria-label')), menuShape)
  expect(menuIcons).toEqual(['Show sidebar'])
  const browserControl = header.getByRole('button', { name: 'Open browser', exact: true })
  await expect(browserControl).toBeVisible()
  const bounds = await header.evaluate((element) => {
    const headerRect = element.getBoundingClientRect()
    const strip = element.querySelector('[data-testid="workspace-tab-strip"]')!.getBoundingClientRect()
    const browser = element.querySelector('[aria-label="Open browser"]')!.getBoundingClientRect()
    const available = element.querySelector('[data-testid="workspace-header-entries"]')!.getBoundingClientRect()
    const full = element.querySelector('[data-workspace-header-measure="full"]')!.getBoundingClientRect()
    return { header: { left: headerRect.left, right: headerRect.right, top: headerRect.top, bottom: headerRect.bottom }, stripRight: strip.right, browser: { left: browser.left, right: browser.right, top: browser.top, bottom: browser.bottom }, availableWidth: available.width, fullWidth: full.width }
  })
  expect(bounds.fullWidth).toBeLessThanOrEqual(bounds.availableWidth)
  // One CSS pixel accommodates subpixel border/rect rounding only.
  expect(bounds.stripRight).toBeLessThanOrEqual(bounds.browser.left + 1)
  expect(bounds.browser.left).toBeGreaterThanOrEqual(bounds.header.left - 1)
  expect(bounds.browser.right).toBeLessThanOrEqual(bounds.header.right + 1)
  expect(bounds.browser.top).toBeGreaterThanOrEqual(bounds.header.top - 1)
  expect(bounds.browser.bottom).toBeLessThanOrEqual(bounds.header.bottom + 1)
  await info.attach('native-header-bounds.json', { body: JSON.stringify(bounds, null, 2), contentType: 'application/json' })
  await screenshot(page, info, 'full-labelled-header.png')
  expect(errors).toEqual([])
})

for (const panel of ['tasks', 'library'] as const) {
  test(`LIVE-1: standalone ${panel} expands with real context and Back reopens in the same window`, async ({ browser }, info) => {
    const { page, context, errors } = await realPage(browser, true)
    await openChat(page, panel)
    expect(await page.evaluate(() => matchMedia('(display-mode: standalone)').matches)).toBe(true)
    if (panel === 'library') {
      await page.getByTestId(`library-row-${fileName}`).click()
      await expect(page.getByText(fileText, { exact: true })).toBeVisible()
    } else await expect(page.getByTestId('tasks-heading')).toBeVisible()
    await page.getByRole('button', { name: `Expand ${panel === 'tasks' ? 'Tasks' : 'Library'} panel`, exact: true }).click()
    await expect(page.getByTestId('fullscreen-panel')).toBeVisible()
    await expect(page.getByText("Can't open this panel", { exact: true })).toHaveCount(0)
    const url = new URL(page.url())
    expect(url.hash.split('?')[0]).toBe(`#/panel/${panel}`)
    expect(new URLSearchParams(url.hash.split('?')[1]).get('workspace')).toBe(workspaceId)
    if (panel === 'library') {
      expect(new URLSearchParams(url.hash.split('?')[1]).get('path')).toBe(fileName)
      await expect(page.getByText(fileText, { exact: true })).toBeVisible()
    } else await expect(page.getByTestId('tasks-heading')).toBeVisible()
    expect(context.pages()).toHaveLength(1)
    await screenshot(page, info, `${panel}-standalone-expanded.png`)
    await page.getByRole('button', { name: 'Back to chat', exact: true }).click()
    await expect(page.getByTestId('side-panel')).toBeVisible()
    await expect(page.getByTestId('side-panel-header')).toContainText(panel === 'tasks' ? 'Tasks' : 'Library')
    const returned = new URL(page.url()).hash
    expect(returned.split('?')[0]).toBe(`#/workspaces/${workspaceId}/chat`)
    expect(new URLSearchParams(returned.split('?')[1]).get('panel')).toBe(panel)
    expect(context.pages()).toHaveLength(1)
    if (panel === 'library') await expect(page.getByTestId(`library-row-${fileName}`)).toBeVisible()
    else await expect(page.getByTestId('tasks-heading')).toBeVisible()
    await screenshot(page, info, `${panel}-standalone-returned.png`)
    expect(errors).toEqual([])
  })
}

test('F2: narrow standalone takeover expands and Back reopens takeover without cleanup navigation', async ({ browser }, info) => {
  const { page, context, errors } = await realPage(browser, true, 640)
  // Start on visible Chat, then use the real toggle. A direct panel link
  // already takes over and intentionally hides the workspace header.
  await openChat(page)
  await toggleWorkspacePanel(page, 'tasks')
  await expect(page.getByTestId('side-panel')).toHaveAttribute('data-takeover', 'true')
  await page.getByRole('button', { name: 'Expand Tasks panel', exact: true }).click()
  await expect(page.getByTestId('fullscreen-panel')).toBeVisible()
  const expanded = new URL(page.url()).hash
  expect(expanded.split('?')[0]).toBe('#/panel/tasks')
  expect(new URLSearchParams(expanded.split('?')[1]).get('workspace')).toBe(workspaceId)
  await page.getByRole('button', { name: 'Back to chat', exact: true }).click()
  await expect(page.getByTestId('side-panel')).toHaveAttribute('data-takeover', 'true')
  await expect(page.getByTestId('tasks-heading')).toBeVisible()
  expect(new URL(page.url()).hash.split('?')[0]).toBe(`#/workspaces/${workspaceId}/chat`)
  expect(context.pages()).toHaveLength(1)
  await screenshot(page, info, 'narrow-standalone-returned.png')
  expect(errors).toEqual([])
})

test('CR-1: pinned short-name icon tooltip is content-sized and readable inside the clipping column', async ({ browser }, info) => {
  const { page, errors } = await realPage(browser, false, 1280)
  await openChat(page)
  await page.getByRole('button', { name: 'Show sidebar', exact: true }).click()
  await page.getByRole('button', { name: 'Pin sidebar', exact: true }).click()
  await page.getByTestId('workspace-tab-board').click()
  await expect(page.getByTestId('tasks-heading')).toBeVisible()
  await page.evaluate(() => { document.documentElement.style.fontSize = '20px' })
  // Resize the actual divider, not a supplied rectangle or forced header mode.
  const separator = page.getByTestId('panel-resize-separator')
  const rect = await separator.boundingBox()
  if (!rect) throw new Error('BLOCKED: native panel divider did not render')
  await page.mouse.move(rect.x + rect.width / 2, rect.y + rect.height / 2)
  await page.mouse.down()
  await page.mouse.move(rect.x - 180, rect.y + rect.height / 2, { steps: 12 })
  await page.mouse.up()
  await expect(page.getByTestId('workspace-header-entries')).toHaveAttribute('data-mode', 'icons')
  await page.getByRole('button', { name: 'Tasks', exact: true }).hover()
  const tooltip = page.getByRole('tooltip')
  await expect(tooltip).toBeVisible()
  await expect(tooltip).toHaveText('Tasks')
  const bounds = await tooltip.evaluate((bubble) => {
    const text = bubble.firstChild!
    const range = document.createRange()
    range.selectNodeContents(text)
    const label = range.getBoundingClientRect()
    const bubbleRect = bubble.getBoundingClientRect()
    const column = bubble.closest('[data-testid="workspace-top-bar"]')!.getBoundingClientRect()
    const style = getComputedStyle(bubble)
    return { label: { left: label.left, right: label.right, width: label.width }, bubble: { left: bubbleRect.left, right: bubbleRect.right, width: bubbleRect.width }, column: { left: column.left, right: column.right }, horizontalChrome: parseFloat(style.paddingLeft) + parseFloat(style.paddingRight) + parseFloat(style.borderLeftWidth) + parseFloat(style.borderRightWidth) }
  })
  expect(bounds.bubble.width).toBeLessThanOrEqual(bounds.label.width + bounds.horizontalChrome + 1)
  expect(bounds.label.left).toBeGreaterThanOrEqual(bounds.column.left - 1)
  expect(bounds.label.right).toBeLessThanOrEqual(bounds.column.right + 1)
  await info.attach('native-tooltip-bounds.json', { body: JSON.stringify(bounds, null, 2), contentType: 'application/json' })
  await screenshot(page, info, 'content-sized-icon-tooltip.png')
  expect(errors).toEqual([])
})
