// PANEL-CALENDAR-TOOLBAR-GEOMETRY-RED-2137 — rendered regression, not a class scan.
// Oracles derived BEFORE execution: side-panel-shell-spec SP-17/SP-39 gives
// the 320px dock floor and equal Week/Day/Month choices; design-system-definition
// D7/D13 requires 320px reflow without loss of function and 44x44px coarse targets.
// Boundary: actual CalendarScreen, CalendarToolbar, FullCalendar, Radix controls,
// editor and compiled application CSS. Only HTTP is faked, at the network edge.
// Driver follows application-style.check.mjs: Vite configFile:false/write:false
// and a private Playwright browser. No app Vite config, E2E setup, gateway,
// account, auth cookie, persistent profile, provider, or source mutation is used.
// Instrument proof clips only the OUTER TEST FIXTURE, then restores it. It is
// not an implementation mutant. GREEN and implementation mutants belong to CHECK.
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:http'
import { resolve, join } from 'node:path'
import { after, before, test } from 'node:test'
import { build } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { chromium, expect } from '@playwright/test'

const FILE = resolve('tests/design-system/calendar-toolbar-geometry.test.mjs')
const RECEIPTS = resolve(process.env.CALENDAR_GEOMETRY_RECEIPTS ?? 'test-results/calendar-toolbar-geometry')
const WORKSPACE = 'calendar-geometry-fixture'
// Spec inputs, not observed implementation output. 319px is outside the
// supported floor; 321px exercises floor+1, and 720px is the normal dock maximum.
const ROOT_PIXELS = 14
const MINIMUM_TOUCH = 44
const CONTROLS = [
  { id: 'calendar-view-timeGridWeek', name: 'Week', role: 'button', text: 'Week' },
  { id: 'calendar-view-timeGridDay', name: 'Day', role: 'button', text: 'Day' },
  { id: 'calendar-view-dayGridMonth', name: 'Month', role: 'button', text: 'Month' },
  { id: 'calendar-agent-filter', name: 'Filter by agent', role: 'combobox', text: 'All agents' },
  { id: 'calendar-new-task', name: 'Create a new task', role: 'button', text: 'New task' },
]
const sha256 = (input) => createHash('sha256').update(input).digest('hex')
let browser, server, origin, sourceContext

function save(name, receipt) {
  mkdirSync(RECEIPTS, { recursive: true, mode: 0o700 })
  writeFileSync(join(RECEIPTS, `${name}.json`), JSON.stringify(receipt, null, 2) + '\n', { mode: 0o600 })
}

function fixtureModule() {
  return `
    import { createElement } from 'react';
    import { createRoot } from 'react-dom/client';
    import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
    import { CalendarScreen } from ${JSON.stringify(resolve('src/components/screens/CalendarScreen.tsx'))};
    import ${JSON.stringify(resolve('src/styles/globals.css'))};
    const client = new QueryClient({ defaultOptions: {
      queries: { retry: false }, mutations: { retry: false }
    }});
    createRoot(globalThis.document.getElementById('fixture-panel')).render(
      createElement(QueryClientProvider, { client },
        createElement(CalendarScreen, { workspaceId: ${JSON.stringify(WORKSPACE)} }))
    );
  `
}

async function compileFixture() {
  const input = 'virtual:calendar-toolbar-geometry.tsx'
  const id = '\0' + input
  const result = await build({
    configFile: false, publicDir: false,
    plugins: [
      { name: 'calendar-geometry-test-entry',
        resolveId: (request) => request === input ? id : undefined,
        load: (request) => request === id ? fixtureModule() : undefined },
      tailwindcss(), react(),
    ],
    resolve: { alias: { '@': resolve('src') } },
    build: { write: false, cssMinify: false, rollupOptions: { input } },
  })
  const outputs = Array.isArray(result) ? result.flatMap((item) => item.output) : result.output
  const entry = outputs.find((item) => item.type === 'chunk' && item.isEntry)
  const styles = outputs.filter((item) => item.type === 'asset' && item.fileName.endsWith('.css'))
  assert.ok(entry, 'Actual CalendarScreen must compile to a browser entry')
  assert.ok(styles.length > 0, 'Actual application styles must compile, not be replaced by fixture CSS')
  const assets = new Map(outputs.map((item) => [
    '/' + item.fileName, Buffer.from(item.type === 'chunk' ? item.code : item.source),
  ]))
  // These dimensions define ONLY the isolated outer panel, not any control.
  // The actual screen supplies overflow-x-hidden and its @container host.
  const html = `<!doctype html><html><head>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <link rel="icon" href="data:,">
    ${styles.map((item) => `<link rel="stylesheet" href="/${item.fileName}">`).join('\n')}
    </head><body><div id="fixture-panel" style="width:720px;height:100dvh"></div>
    <script type="module" src="/${entry.fileName}"></script></body></html>`
  assets.set('/', Buffer.from(html))
  return assets
}

before(async () => {
  mkdirSync(RECEIPTS, { recursive: true, mode: 0o700 })
  sourceContext = {
    sourceSHA: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
    sourceTree: execFileSync('git', ['rev-parse', 'HEAD^{tree}'], { encoding: 'utf8' }).trim(),
    testFile: FILE, testSHA256: sha256(readFileSync(FILE)), rootPixels: ROOT_PIXELS,
    sourceFiles: Object.fromEntries([
      'src/components/screens/CalendarScreen.tsx', 'src/components/calendar/CalendarToolbar.tsx',
      'src/components/ui/segmented-control.tsx', 'src/styles/globals.css',
    ].map((file) => [resolve(file), sha256(readFileSync(resolve(file)))])),
  }
  const assets = await compileFixture()
  save('compiled-source', { ...sourceContext, assets: Object.fromEntries(
    [...assets].map(([path, bytes]) => [path, sha256(bytes)])),
  })
  server = createServer((request, response) => {
    const pathname = new URL(request.url, 'http://127.0.0.1').pathname
    const asset = assets.get(pathname)
    response.statusCode = asset ? 200 : 404
    const mime = pathname.endsWith('.js') ? 'text/javascript'
      : pathname.endsWith('.css') ? 'text/css' : pathname.endsWith('.woff2') ? 'font/woff2'
        : pathname.endsWith('.woff') ? 'font/woff' : 'text/html'
    response.setHeader('Content-Type', mime)
    response.end(asset ?? 'Unlisted fixture asset')
  })
  await new Promise((resolveReady, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', resolveReady)
  })
  origin = `http://127.0.0.1:${server.address().port}`
  browser = await chromium.launch() // Playwright's private temporary profile, no extra browser flags.
  sourceContext.browserVersion = browser.version()
  sourceContext.origin = origin
})

after(async () => {
  if (browser) await browser.close()
  if (server) await new Promise((done, reject) => server.close((error) => error ? reject(error) : done()))
})

function httpFixture(pathname) {
  if (['/api/v1/agents', '/api/v1/tasks', '/api/v1/tasks/occurrences'].includes(pathname)) return []
  if (pathname === `/api/v1/workspaces/${WORKSPACE}/delegation`) return {
    revision: '0'.repeat(64), workspace_id: WORKSPACE, edges: [], default_depth: 3, team: [],
  }
  return undefined
}

async function openPanel({ viewport, panelWidth, phone = false }) {
  const context = await browser.newContext({
    viewport, hasTouch: true, isMobile: phone, timezoneId: 'UTC', serviceWorkers: 'block',
  })
  const unexpected = [], requests = [], errors = []
  await context.route('**/*', async (route) => {
    const request = route.request(), url = new URL(request.url())
    if (url.origin !== origin) {
      unexpected.push(`Blocked external request: ${request.method()} ${url.origin}${url.pathname}`)
      await route.abort('blockedbyclient'); return
    }
    if (url.pathname.startsWith('/api/')) {
      const response = request.method() === 'GET' ? httpFixture(url.pathname) : undefined
      requests.push(`${request.method()} ${url.pathname}`)
      if (response === undefined) {
        unexpected.push(`Unlisted fixture HTTP: ${request.method()} ${url.pathname}`)
        await route.fulfill({ status: 501, json: { error: 'Unlisted fixture HTTP' } }); return
      }
      await route.fulfill({ status: 200, json: response }); return
    }
    await route.continue()
  })
  await context.routeWebSocket('**/*', (socket) => {
    unexpected.push('Blocked WebSocket: ' + new URL(socket.url()).pathname)
    socket.close()
  })
  const page = await context.newPage()
  page.on('pageerror', (error) => errors.push(error.message))
  try {
    await page.goto(origin)
    await page.locator('#fixture-panel').evaluate((element, width) => { element.style.width = `${width}px` }, panelWidth)
    await expect(page.getByTestId('calendar-view-timeGridWeek')).toHaveAttribute('aria-pressed', 'true')
    await page.waitForFunction(() => globalThis.document.querySelector('[data-testid="calendar-title"]')?.textContent.trim().length > 0)
    await expect(page.getByTestId('calendar-grid').locator('.fc')).toHaveCount(1)
    await page.evaluate(() => globalThis.document.fonts.ready)
    await settle(page)
    assert.deepEqual(await page.evaluate(() => ({
      root: parseFloat(globalThis.getComputedStyle(globalThis.document.documentElement).fontSize),
      coarse: globalThis.matchMedia('(pointer: coarse)').matches, viewport: globalThis.innerWidth,
      panel: globalThis.document.getElementById('fixture-panel').getBoundingClientRect().width,
    })), { root: ROOT_PIXELS, coarse: true, viewport: viewport.width, panel: panelWidth }, 'The real compiled-style browser context must match the spec inputs')
    for (const control of CONTROLS) {
      const target = page.getByRole(control.role, { name: control.name, exact: true })
      await expect(target).toHaveCount(1)
      await expect(target).toHaveAttribute('data-testid', control.id)
      await expect(target).toHaveText(control.text)
      await expect(target).toBeEnabled()
    }
    return { page, context, unexpected, requests, errors }
  } catch (error) {
    await context.close(); throw error
  }
}

async function settle(page) {
  await page.evaluate(() => new Promise((done) => globalThis.requestAnimationFrame(() => globalThis.requestAnimationFrame(done))))
}

async function measureControl(page, control) {
  return page.getByTestId(control.id).evaluate((element, { id, minimum }) => {
    const box = (rect) => ({ x: rect.x, y: rect.y, width: rect.width, height: rect.height })
    const rect = element.getBoundingClientRect()
    const panel = globalThis.document.getElementById('fixture-panel').getBoundingClientRect()
    const before = globalThis.getComputedStyle(element, '::before')
    const hasHitRegion = before.content !== 'none' && before.content !== 'normal' && before.pointerEvents !== 'none'
    // Read the actual compiled pseudo-element's size, never hand-enter boxes.
    const width = hasHitRegion ? Math.max(rect.width, parseFloat(before.width)) : rect.width
    const height = hasHitRegion ? Math.max(rect.height, parseFloat(before.height)) : rect.height
    const hit = { x: rect.x + (rect.width - width) / 2, y: rect.y + (rect.height - height) / 2, width, height }
    let left = Math.max(0, panel.left), top = Math.max(0, panel.top)
    let right = Math.min(globalThis.innerWidth, panel.right), bottom = Math.min(globalThis.innerHeight, panel.bottom)
    const clips = []
    for (let ancestor = element.parentElement; ancestor; ancestor = ancestor.parentElement) {
      const style = globalThis.getComputedStyle(ancestor), bounds = ancestor.getBoundingClientRect()
      const clipX = ['hidden', 'clip', 'scroll', 'auto'].includes(style.overflowX)
      const clipY = ['hidden', 'clip', 'scroll', 'auto'].includes(style.overflowY)
      if (clipX) { left = Math.max(left, bounds.left); right = Math.min(right, bounds.right) }
      if (clipY) { top = Math.max(top, bounds.top); bottom = Math.min(bottom, bounds.bottom) }
      if (clipX || clipY) clips.push({ tag: ancestor.tagName, ...box(bounds), overflowX: style.overflowX, overflowY: style.overflowY })
    }
    // Probe the centre, four mid-edges, and rounded-corner-safe diagonals of
    // each ACTUAL effective region. 1px inset avoids border rasterisation.
    const center = { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 }
    const rx = width / 2 - 1, ry = height / 2 - 1
    const radius = parseFloat(globalThis.getComputedStyle(element).borderTopLeftRadius) || 0
    const dx = Math.max(0, rx - radius), dy = Math.max(0, ry - radius)
    const samples = [[0, 0], [-rx, 0], [rx, 0], [0, -ry], [0, ry], [-dx, -dy], [dx, -dy], [-dx, dy], [dx, dy]]
      .map(([x, y]) => {
        const point = { x: center.x + x, y: center.y + y }
        const target = globalThis.document.elementFromPoint(point.x, point.y)?.closest('button')
        return { ...point, targetId: target?.getAttribute('data-testid') ?? null, ownTarget: target === element }
      })
    const failures = []
    // 0.01 CSS pixel is solely a floating-point rectangle-rounding allowance,
    // not a content-width or touch-size tolerance; 44px itself stays exact.
    const inside = (b) => b.x >= left - 0.01 && b.y >= top - 0.01 && b.x + b.width <= right + 0.01 && b.y + b.height <= bottom + 0.01
    if (!inside(box(rect))) failures.push(`${id}: visible control is clipped/offscreen`)
    if (!inside(hit)) failures.push(`${id}: effective hit region is clipped/offscreen`)
    if (!(width >= minimum && height >= minimum)) failures.push(`${id}: touch target is below ${minimum}x${minimum}px`)
    if (samples.some((point) => !point.ownTarget)) failures.push(`${id}: browser hit testing selects another control or no control`)
    return { id, control: box(rect), hit, panel: box(panel), clippedTo: { left, top, right, bottom }, clips, samples, failures }
  }, { id: control.id, minimum: MINIMUM_TOUCH })
}

async function measureToolbar(page) {
  const controls = []
  for (const control of CONTROLS) controls.push(await measureControl(page, control))
  const failures = controls.flatMap((control) => control.failures)
  for (let a = 0; a < controls.length; a++) for (let b = a + 1; b < controls.length; b++) {
    const x = controls[a].hit, y = controls[b].hit
    if (x.x < y.x + y.width && y.x < x.x + x.width && x.y < y.y + y.height && y.y < x.y + x.height) {
      failures.push(`${controls[a].id}/${controls[b].id}: effective touch regions overlap`)
    }
  }
  return { controls, failures }
}

function assertReachable(measurement) {
  assert.deepEqual(measurement.failures, [], 'Every named Calendar toolbar control must fit the panel/viewport with an unoccluded 44x44px touch region')
}

async function realTap(page, control) {
  const measurement = await measureControl(page, control)
  assert.deepEqual(measurement.failures, [], `${control.id}: cannot activate an offscreen or occluded target`)
  await page.evaluate(() => {
    globalThis.__calendarGeometryPointer = null
    globalThis.document.addEventListener('pointerdown', (event) => {
      globalThis.__calendarGeometryPointer = {
        targetId: event.target instanceof globalThis.Element ? event.target.closest('button')?.getAttribute('data-testid') ?? null : null,
        trusted: event.isTrusted, pointerType: event.pointerType,
      }
    }, { once: true, capture: true })
  })
  const { x, y, width, height } = measurement.control
  // Native coordinate tap, NOT locator.click (which can scroll a clipped
  // element into view), dispatchEvent, force:true, or a mocked callback.
  await page.touchscreen.tap(x + width / 2, y + height / 2)
  assert.deepEqual(await page.evaluate(() => globalThis.__calendarGeometryPointer), {
    targetId: control.id, trusted: true, pointerType: 'touch',
  }, `${control.id}: native tap must reach this exact control`)
}

async function proveActions(page) {
  // Change away from the initial Week so the Week tap must do real work too.
  for (const name of ['Day', 'Week', 'Month']) {
    const control = CONTROLS.find((item) => item.name === name)
    await realTap(page, control)
    for (const view of CONTROLS.slice(0, 3)) {
      await expect(page.getByTestId(view.id)).toHaveAttribute('aria-pressed', String(view === control))
    }
    if (name === 'Month') await expect(page.getByTestId('calendar-month-grid')).toBeVisible()
    else await expect(page.getByTestId('calendar-grid').locator(name === 'Day' ? '.fc-timeGridDay-view' : '.fc-timeGridWeek-view')).toBeVisible()
  }
  await realTap(page, CONTROLS[3])
  await expect(page.getByRole('listbox')).toBeVisible()
  await page.getByRole('option', { name: 'Unassigned', exact: true }).tap()
  await expect(page.getByTestId(CONTROLS[3].id)).toHaveText('Unassigned')
  await expect(page.getByRole('listbox')).toHaveCount(0)
  await realTap(page, CONTROLS[4])
  await expect(page.getByRole('dialog', { name: 'New event', exact: true })).toBeVisible()
  // The real required-field label includes its asterisk (ces-title).
  await expect(page.getByRole('textbox', { name: 'Title *', exact: true })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog', { name: 'New event', exact: true })).toHaveCount(0)
}

async function withPanel(name, inputs, run) {
  const fixture = await openPanel(inputs)
  try {
    await run(fixture.page)
  } finally {
    // Only this test's synthetic page is captured. No cookies/storage/profile.
    await fixture.page.screenshot({ path: join(RECEIPTS, `${name}.png`) })
    save(name, { ...sourceContext, ...inputs, requests: fixture.requests,
      unexpectedRequests: fixture.unexpected, pageErrors: fixture.errors,
      measurement: await measureToolbar(fixture.page) })
    await fixture.context.close()
    assert.deepEqual(fixture.unexpected, [], 'No external, backend, provider, or unlisted fixture request is allowed')
    assert.deepEqual(fixture.errors, [], 'A render crash is not geometry RED')
  }
}

test('instrument rejects a deliberately clipped New task control and passes after fixture restoration', { concurrency: false }, async () => {
  await withPanel('instrument-clipped-restored', { viewport: { width: 1280, height: 900 }, panelWidth: 720 }, async (page) => {
    assertReachable(await measureToolbar(page))
    await page.locator('#fixture-panel').evaluate((element) => { element.style.width = '44px' })
    try {
      await settle(page)
      const bad = await measureToolbar(page)
      save('instrument-clipped', { ...sourceContext, measurement: bad })
      await page.screenshot({ path: join(RECEIPTS, 'instrument-clipped.png') })
      assert.ok(bad.failures.includes('calendar-new-task: visible control is clipped/offscreen'), 'Instrument must name the deliberately clipped New task control')
      assert.throws(() => assertReachable(bad), { name: 'AssertionError', message: /Every named Calendar toolbar control must fit/ })
    } finally {
      await page.locator('#fixture-panel').evaluate((element) => { element.style.width = '720px' })
      await settle(page)
    }
    assertReachable(await measureToolbar(page))
  })
})

test('wide 720px panel preserves all five real controls and coarse-pointer actions', { concurrency: false }, async () => {
  await withPanel('wide-coarse', { viewport: { width: 1280, height: 900 }, panelWidth: 720 }, async (page) => {
    assertReachable(await measureToolbar(page))
    await proveActions(page)
  })
})

for (const [name, inputs] of [
  ['320px dock', { viewport: { width: 1280, height: 900 }, panelWidth: 320 }],
  ['320px phone', { viewport: { width: 320, height: 900 }, panelWidth: 320, phone: true }],
  ['321px phone floor+1', { viewport: { width: 321, height: 900 }, panelWidth: 321, phone: true }],
]) {
  test(`${name}: Week Day Month agent filter and New task remain reachable with 44px targets`, { concurrency: false }, async () => {
    await withPanel(name.replaceAll(' ', '-'), inputs, async (page) => {
      const measured = await measureToolbar(page)
      save(name.replaceAll(' ', '-') + '-before-activation', { ...sourceContext, ...inputs, measurement: measured })
      assertReachable(measured)
      await proveActions(page)
    })
  })
}
