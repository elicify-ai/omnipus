// PANEL-LIST-BROWSER-COLUMNS-2222 — frozen61f TC2 coverage, not a claimed bug fix.
// Oracles fixed before execution: side-panel-shell-spec.md §10 Wave 3 and §13
// SP-33/SP-35 require container-width responsiveness, both Tags/Updated columns
// behind the real overflow control, and no loss of the other columns' function.
// The dispatch fixes the delivered 648px boundary: "below" means <648, not <=.
// 720px is the normal dock maximum; 320px is the supported dock floor (SP-17).
// Actual ListView/TaskRow, React state, Date, Radix menus and compiled app Tailwind
// CSS stay real. Only synthetic input props and process edges are controlled.
// No live app, account, API/provider, persistent browser or E2E setup.
// A compiler-output CSS fault is in-memory instrument calibration, NOT production
// pre-change RED, an implementation mutation, or independent CHECK certification.
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { chmodSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:http'
import { join, resolve } from 'node:path'
import { after, before, test } from 'node:test'
import { build } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import postcss from 'postcss'
import { chromium, expect } from '@playwright/test'

const ROOT = process.cwd()
const FILE = resolve('tests/design-system/list-columns-geometry.test.mjs')
const RECEIPTS = resolve(process.env.LIST_COLUMNS_GEOMETRY_RECEIPTS ?? 'test-results/list-columns-geometry')
const FAULT = process.env.LIST_COLUMNS_GEOMETRY_FAULT ?? ''
const VIEWPORT = { width: 1280, height: 900 }
const AUTO_LABEL = 'Show or hide Tags and Updated columns'
const SHOWN_LABEL = 'Hide Tags and Updated columns'
const HIDDEN_LABEL = 'Show Tags and Updated columns'
const PRIMARY_TITLE = 'Alpha synthetic task'
const PRIMARY_ROW = 'tbody tr:has(button[title="Alpha synthetic task"])'
const TASKS = [
  { id: 'fixture-alpha', title: PRIMARY_TITLE, priority: 1, status: 'inbox',
    agent_id: 'fixture-mira', tags: ['fixture-tag', 'synthetic-only'],
    updated_at: '2026-06-20T10:00:00Z' },
  { id: 'fixture-zulu', title: 'Zulu synthetic task', priority: 4, status: 'next',
    agent_id: 'fixture-nilo', tags: ['fixture-other'],
    updated_at: '2026-06-19T10:00:00Z' },
].map((task) => ({
  action: 'llm', workspace_id: 'fixture-workspace', surface: 'user',
  owner: 'fixture-user', created_by: 'fixture-user',
  created_at: '2026-06-19T10:00:00Z', ...task,
}))
const AGENTS = [
  { id: 'fixture-mira', name: 'Mira' },
  { id: 'fixture-nilo', name: 'Nilo' },
]
// Native table positions, identified by the public header labels, not classes.
const GOVERNED = [
  { name: 'Tags', column: 4, headerName: 'Tags column — filter' },
  { name: 'Updated', column: 6, headerName: 'Updated column — sort' },
]
const CORE = [
  { name: 'Priority', column: 1, headerName: 'Pri column — sort and filter', value: 'P1',
    sort: 'descending', order: ['Zulu synthetic task', PRIMARY_TITLE] },
  { name: 'Title', column: 2, headerName: 'Title column — sort', value: PRIMARY_TITLE,
    sort: 'ascending', order: [PRIMARY_TITLE, 'Zulu synthetic task'] },
  { name: 'Status', column: 3, headerName: 'Status column — sort and filter', value: 'Inbox',
    sort: 'descending', order: ['Zulu synthetic task', PRIMARY_TITLE] },
  { name: 'Agent', column: 5, headerName: 'Agent column — sort and filter', value: 'Mira',
    sort: 'ascending', order: [PRIMARY_TITLE, 'Zulu synthetic task'] },
]
const sha256 = (bytes) => createHash('sha256').update(bytes).digest('hex')
let browser, server, origin, assets, stylesheetPath, originalCSS, sourceContext

function save(name, value) {
  mkdirSync(RECEIPTS, { recursive: true, mode: 0o700 })
  chmodSync(RECEIPTS, 0o700)
  const file = join(RECEIPTS, `${name}.json`)
  writeFileSync(file, JSON.stringify(value, null, 2) + '\n', { mode: 0o600 })
  chmodSync(file, 0o600)
}

async function screenshot(page, name) {
  const file = join(RECEIPTS, `${name}.png`)
  await page.screenshot({ path: file })
  chmodSync(file, 0o600)
}

function fixtureModule() {
  return `
    import { createElement } from 'react';
    import { createRoot } from 'react-dom/client';
    import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
    import { ListView } from ${JSON.stringify(resolve('src/components/workspaces/ListView.tsx'))};
    import ${JSON.stringify(resolve('src/styles/globals.css'))};
    const client = new QueryClient({ defaultOptions: {
      queries: { retry: false }, mutations: { retry: false }
    }});
    globalThis.__listGeometryTaskClicks = [];
    createRoot(globalThis.document.getElementById('fixture-panel')).render(
      createElement(QueryClientProvider, { client },
        createElement(ListView, {
          tasks: ${JSON.stringify(TASKS)}, agents: ${JSON.stringify(AGENTS)},
          onTaskClick: (task) => globalThis.__listGeometryTaskClicks.push({
            id: task.id, title: task.title, tags: task.tags, updated_at: task.updated_at
          })
        }))
    );
  `
}

function removeNarrowHideRule(css) {
  const sheet = postcss.parse(css)
  const removed = []
  // Exact selector: leave the narrow-probe :block rule and every other utility
  // untouched. Parse the compiler output rather than guessing brace positions.
  sheet.walkRules((rule) => {
    if (rule.selector !== String.raw`.\@max-\[648px\]\:hidden`) return
    assert.equal(rule.parent.type, 'atrule', 'The responsive rule must live inside its compiled query')
    assert.equal(rule.parent.name, 'container', 'The responsive boundary must be a container query, not a viewport query')
    assert.match(rule.parent.params, /^not\s*\(min-width:\s*648px\)$/,
      'The fault boundary must target only the compiled below-648px container rule')
    assert.match(rule.toString(), /display:\s*none/,
      'The removed rule must actually hide columns')
    removed.push({ query: rule.parent.params, rule: rule.toString() })
    rule.remove()
  })
  assert.equal(removed.length, 1, 'Exactly one narrowly targeted compiled responsive rule must be removed')
  return { css: sheet.toString(), removed }
}

async function compileFixture() {
  const entryId = 'virtual:list-columns-geometry.tsx'
  const virtualId = '\0' + entryId
  const result = await build({
    configFile: false, publicDir: false,
    plugins: [
      { name: 'list-columns-real-fixture',
        resolveId: (request) => request === entryId ? virtualId : undefined,
        load: (request) => request === virtualId ? fixtureModule() : undefined },
      tailwindcss(), react(),
    ],
    resolve: { alias: { '@': resolve('src') } },
    build: { write: false, cssMinify: false, rollupOptions: { input: entryId } },
  })
  const outputs = Array.isArray(result) ? result.flatMap((item) => item.output) : result.output
  const entry = outputs.find((item) => item.type === 'chunk' && item.isEntry)
  const styles = outputs.filter((item) => item.type === 'asset' && item.fileName.endsWith('.css'))
  assert.ok(entry, 'The actual ListView/TaskRow must compile to a real browser entry')
  assert.equal(styles.length, 1, 'The actual app Tailwind pipeline must produce its stylesheet')
  stylesheetPath = '/' + styles[0].fileName
  originalCSS = String(styles[0].source)
  const fault = removeNarrowHideRule(originalCSS)
  assets = new Map(outputs.map((item) => [
    '/' + item.fileName, Buffer.from(item.type === 'chunk' ? item.code : item.source),
  ]))
  if (FAULT) assets.set(stylesheetPath, Buffer.from(fault.css))
  // Only the private OUTER fixture gets dimensions. No table/cell/probe/button
  // styles are injected, and globals.css supplies its real default root size.
  assets.set('/', Buffer.from(`<!doctype html><html><head>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <link rel="icon" href="data:,">
    <link rel="stylesheet" href="${stylesheetPath}">
    </head><body><div id="fixture-panel" style="width:720px;height:100dvh"></div>
    <script type="module" src="/${entry.fileName}"></script></body></html>`))
  const sourceFiles = new Set([
    'src/styles/globals.css', 'src/styles/tokens.generated.css',
    'src/components/workspaces/ListView.narrow.test.tsx', 'package.json', 'package-lock.json',
  ].map((file) => resolve(file)))
  for (const chunk of outputs.filter((item) => item.type === 'chunk')) {
    for (const id of chunk.moduleIds) {
      const file = id.split('?')[0]
      if (file.startsWith(ROOT + '/src/')) sourceFiles.add(file)
    }
  }
  sourceContext.sourceFiles = Object.fromEntries([...sourceFiles].sort().map((file) => [file, sha256(readFileSync(file))]))
  save('compiled-source', {
    ...sourceContext, stylesheetPath, originalCSSSHA256: sha256(originalCSS),
    servedCSSSHA256: sha256(assets.get(stylesheetPath)),
    removedRule: FAULT ? fault.removed : [],
    assets: Object.fromEntries([...assets].map(([path, bytes]) => [path, sha256(bytes)])),
  })
}

before(async () => {
  assert.ok(FAULT === '' || FAULT === 'remove-narrow-hidden', 'Unknown CSS fault mode is a setup error, never a skip')
  sourceContext = {
    unit: 'PANEL-LIST-BROWSER-COLUMNS-2222', fault: FAULT || 'none',
    sourceSHA: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
    sourceTree: execFileSync('git', ['rev-parse', 'HEAD^{tree}'], { encoding: 'utf8' }).trim(),
    testFile: FILE, testSHA256: sha256(readFileSync(FILE)), viewport: VIEWPORT,
    clock: 'real browser Date; no clock override', syntheticOnly: true, productionPreChangeRED: 'not claimed',
  }
  await compileFixture()
  server = createServer((request, response) => {
    const pathname = new URL(request.url, 'http://127.0.0.1').pathname
    const bytes = request.method === 'GET' ? assets.get(pathname) : undefined
    response.statusCode = bytes ? 200 : 404
    response.setHeader('Cache-Control', 'no-store')
    response.setHeader('Content-Type', pathname.endsWith('.js') ? 'text/javascript'
      : pathname.endsWith('.css') ? 'text/css' : pathname.endsWith('.woff2') ? 'font/woff2'
        : pathname.endsWith('.woff') ? 'font/woff' : 'text/html')
    response.end(bytes ?? 'Unlisted fixture asset')
  })
  await new Promise((ready, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', ready)
  })
  origin = `http://127.0.0.1:${server.address().port}`
  browser = await chromium.launch()
  sourceContext.browserVersion = browser.version()
  sourceContext.origin = origin
})

after(async () => {
  if (browser) await browser.close()
  if (server) await new Promise((done, reject) => server.close((error) => error ? reject(error) : done()))
})

async function settle(page) {
  await page.evaluate(() => new Promise((done) => globalThis.requestAnimationFrame(() => globalThis.requestAnimationFrame(done))))
}

async function resizePanel(page, width) {
  await page.locator('#fixture-panel').evaluate((element, pixels) => { element.style.width = `${pixels}px` }, width)
  await settle(page)
  assert.deepEqual(await page.evaluate(() => {
    const panel = globalThis.document.getElementById('fixture-panel')
    const list = panel.firstElementChild
    return {
      viewport: globalThis.innerWidth,
      panel: panel.getBoundingClientRect().width,
      list: list.getBoundingClientRect().width,
      containerType: globalThis.getComputedStyle(list).containerType,
      root: globalThis.getComputedStyle(globalThis.document.documentElement).fontSize,
    }
  }), { viewport: 1280, panel: width, list: width, containerType: 'inline-size', root: '14px' },
  'Setup control: resize only the real List container, not the 1280px window or the app root-size default')
}

async function withPanel(name, run) {
  const context = await browser.newContext({
    viewport: VIEWPORT, timezoneId: 'UTC', serviceWorkers: 'block', acceptDownloads: false,
  })
  const unexpected = [], requests = [], errors = [], observations = []
  await context.route('**/*', async (route) => {
    const request = route.request(), url = new URL(request.url())
    if (url.origin !== origin || request.method() !== 'GET' || !assets.has(url.pathname)) {
      unexpected.push(`Blocked: ${request.method()} ${url.origin}${url.pathname}`)
      await route.abort('blockedbyclient')
      return
    }
    requests.push(`${request.method()} ${url.pathname}`)
    await route.continue()
  })
  await context.routeWebSocket('**/*', (socket) => {
    unexpected.push('Blocked WebSocket: ' + new URL(socket.url()).pathname)
    socket.close()
  })
  const page = await context.newPage()
  page.on('pageerror', (error) => errors.push(error.message))
  page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()) })
  const session = await context.newCDPSession(page)
  let failure
  try {
    await page.goto(origin)
    await expect(page.locator('tbody tr')).toHaveCount(2)
    await page.evaluate(() => globalThis.document.fonts.ready)
    await resizePanel(page, 720)
    await page.evaluate((selector) => {
      globalThis.__listGeometryOriginalRow = globalThis.document.querySelector(selector)
      globalThis.__listGeometryInitialUpdated = globalThis.__listGeometryOriginalRow.cells[5].textContent.trim()
    }, PRIMARY_ROW)
    await run({ page, session, observations })
  } catch (error) {
    failure = { name: error.name, code: error.code, message: error.message }
    throw error
  } finally {
    try {
      await screenshot(page, name)
      save(name, { ...sourceContext, observations, failure: failure ?? null,
        requests, unexpectedRequests: unexpected, pageErrors: errors })
    } finally {
      await session.detach()
      await context.close()
    }
    assert.deepEqual(unexpected, [], 'No external, live backend, account, provider or unlisted fixture request is allowed')
    assert.deepEqual(errors, [], 'A render/console error is a setup obstruction, not column-geometry RED')
  }
}

async function accessibleNode(session, selector) {
  const { root } = await session.send('DOM.getDocument')
  const { nodeId } = await session.send('DOM.querySelector', { nodeId: root.nodeId, selector })
  assert.notEqual(nodeId, 0, `The actual native table node must still exist: ${selector}`)
  const { node } = await session.send('DOM.describeNode', { nodeId })
  const { nodes } = await session.send('Accessibility.getPartialAXTree', {
    backendNodeId: node.backendNodeId, fetchRelatives: false,
  })
  const actual = nodes.find((item) => item.backendDOMNodeId === node.backendNodeId)
  assert.ok(actual, `Chromium must return the accessibility exposure of this exact DOM node: ${selector}`)
  return { ignored: actual.ignored, role: actual.role?.value,
    name: actual.name?.value, ignoredReasons: actual.ignoredReasons }
}

async function columnMeasurement(page, session, column) {
  const header = `thead th:nth-child(${column.column})`
  const cell = `${PRIMARY_ROW} td:nth-child(${column.column})`
  const measure = async (selector) => ({
    ...await page.locator(selector).evaluate((element) => {
      const rect = element.getBoundingClientRect()
      return {
        display: globalThis.getComputedStyle(element).display,
        text: element.textContent.trim(), rects: element.getClientRects().length,
        width: rect.width, height: rect.height,
      }
    }),
    accessibility: await accessibleNode(session, selector),
  })
  return { name: column.name, header: await measure(header), cell: await measure(cell) }
}

async function measureColumns(page, session) {
  const columns = []
  for (const column of GOVERNED) columns.push(await columnMeasurement(page, session, column))
  return columns
}

function assertVisibility(columns, visible, label) {
  for (const column of columns) for (const part of ['header', 'cell']) {
    const actual = column[part]
    assert.equal(actual.display, visible ? 'table-cell' : 'none',
      `${label}: ${column.name} ${part} must have computed display ${visible ? 'table-cell' : 'none'}, got ${actual.display}`)
    assert.equal(actual.rects, visible ? 1 : 0, `${label}: ${column.name} ${part} must ${visible ? 'have' : 'not have'} a rendered rectangle`)
    assert.equal(actual.accessibility.ignored, !visible,
      `${label}: ${column.name} ${part} must ${visible ? 'return to' : 'leave'} the real browser accessibility tree`)
    if (visible) {
      assert.ok(actual.width > 0 && actual.height > 0, `${label}: ${column.name} ${part} must have nonzero real geometry`)
    } else {
      assert.equal(actual.width, 0, `${label}: hidden ${column.name} ${part} must occupy zero width`)
      assert.equal(actual.height, 0, `${label}: hidden ${column.name} ${part} must occupy zero height`)
    }
  }
}

async function assertColumns(page, session, visible, label, observations) {
  const columns = await measureColumns(page, session)
  observations.push({ label, visibleExpected: visible, columns })
  assertVisibility(columns, visible, label)
  for (const column of GOVERNED) {
    await expect(page.getByRole('button', { name: column.headerName, exact: true })).toHaveCount(visible ? 1 : 0)
    await expect(page.getByRole('button', { name: column.headerName, exact: true, includeHidden: true })).toHaveCount(1)
  }
  const data = await page.locator(PRIMARY_ROW).evaluate((row) => ({
    sameRow: row === globalThis.__listGeometryOriginalRow,
    tags: [...row.cells[3].querySelectorAll('span[title]')].map((tag) => tag.textContent),
    updated: row.cells[5].textContent.trim(),
    initialUpdated: globalThis.__listGeometryInitialUpdated,
    title: row.querySelector('button[title]').getAttribute('title'),
    cellCount: row.cells.length,
  }))
  assert.deepEqual({ sameRow: data.sameRow, tags: data.tags, title: data.title, cellCount: data.cellCount },
    { sameRow: true, tags: TASKS[0].tags, title: PRIMARY_TITLE, cellCount: 7 },
    `${label}: the SAME synthetic task row retains its DOM data; hidden data is not claimed accessible`)
  // Wall-clock-relative wording is intentionally not frozen. The valid
  // synthetic date must render a nonempty relative value, conserved by toggles.
  assert.match(data.updated, /^(?:just now|\d+[mhd] ago)$/,
    `${label}: Updated must retain a nonempty relative-date value, not a missing-date placeholder`)
  assert.equal(data.updated, data.initialUpdated, `${label}: hiding/revealing must conserve the Updated cell's data`)
}

async function measureControl(locator) {
  return locator.evaluate((element) => {
    const rect = element.getBoundingClientRect()
    const panel = globalThis.document.getElementById('fixture-panel').getBoundingClientRect()
    let left = Math.max(0, panel.left), top = Math.max(0, panel.top)
    let right = Math.min(globalThis.innerWidth, panel.right), bottom = Math.min(globalThis.innerHeight, panel.bottom)
    for (let parent = element.parentElement; parent; parent = parent.parentElement) {
      const css = globalThis.getComputedStyle(parent), bounds = parent.getBoundingClientRect()
      if (['hidden', 'clip', 'auto', 'scroll'].includes(css.overflowX)) {
        left = Math.max(left, bounds.left); right = Math.min(right, bounds.right)
      }
      if (['hidden', 'clip', 'auto', 'scroll'].includes(css.overflowY)) {
        top = Math.max(top, bounds.top); bottom = Math.min(bottom, bounds.bottom)
      }
    }
    const x = rect.x + rect.width / 2, y = rect.y + rect.height / 2
    return {
      name: element.getAttribute('aria-label') ?? element.textContent.trim(),
      display: globalThis.getComputedStyle(element).display,
      rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height },
      clippedTo: { left, top, right, bottom },
      // 0.01 CSS px is only a floating-point rounding allowance, not extra room.
      inside: rect.left >= left - 0.01 && rect.right <= right + 0.01
        && rect.top >= top - 0.01 && rect.bottom <= bottom + 0.01,
      hit: globalThis.document.elementFromPoint(x, y)?.closest('button') === element,
    }
  })
}

async function realClick(page, locator, label, observations) {
  await expect(locator).toHaveCount(1)
  await expect(locator).toBeVisible()
  await expect(locator).toBeEnabled()
  const control = await measureControl(locator)
  observations.push({ label, control })
  assert.ok(control.rect.width > 0 && control.rect.height > 0, `${label}: real control must have nonzero geometry`)
  assert.equal(control.inside, true, `${label}: real control must fit inside the List container without clipping`)
  assert.equal(control.hit, true, `${label}: actual browser hit testing must reach this control, not another column`)
  await locator.evaluate((element) => {
    globalThis.__listGeometryPointer = null
    element.addEventListener('pointerdown', (event) => {
      globalThis.__listGeometryPointer = { trusted: event.isTrusted, type: event.pointerType }
    }, { capture: true, once: true })
  })
  await page.mouse.click(control.rect.x + control.rect.width / 2, control.rect.y + control.rect.height / 2)
  assert.deepEqual(await page.evaluate(() => globalThis.__listGeometryPointer), { trusted: true, type: 'mouse' },
    `${label}: a native trusted mouse event must reach the real handler`)
  await settle(page)
}

async function proveCoreUsable(page, session, label, observations) {
  for (const column of CORE) {
    const selector = `${PRIMARY_ROW} td:nth-child(${column.column})`
    await expect(page.locator(selector)).toHaveCSS('display', 'table-cell')
    await expect(page.locator(selector)).toHaveText(column.value)
    assert.equal((await accessibleNode(session, selector)).ignored, false, `${label}: ${column.name} data remains accessible`)
    const trigger = page.getByRole('button', { name: column.headerName, exact: true })
    await realClick(page, trigger, `${label}: ${column.name} header`, observations)
    const menuItem = page.getByRole('menuitem', { name: `Sort ${column.sort}`, exact: true })
    await expect(menuItem).toBeVisible()
    await menuItem.click()
    await expect(page.locator(`thead th:nth-child(${column.column})`)).toHaveAttribute('aria-sort', column.sort)
    assert.deepEqual(await page.locator('tbody tr button[title]').evaluateAll((buttons) => buttons.map((button) => button.getAttribute('title'))),
      column.order, `${label}: ${column.name} sorting must operate on the actual synthetic task rows`)
  }
  const title = page.getByRole('button', { name: `${PRIMARY_TITLE}, status Inbox`, exact: true })
  const beforeCount = await page.evaluate(() => globalThis.__listGeometryTaskClicks.length)
  await realClick(page, title, `${label}: Title task activation`, observations)
  assert.deepEqual(await page.evaluate((start) => globalThis.__listGeometryTaskClicks.slice(start), beforeCount),
    [{ id: TASKS[0].id, title: PRIMARY_TITLE, tags: TASKS[0].tags, updated_at: TASKS[0].updated_at }],
    `${label}: actual Title click delivers the intact task exactly once to the parent callback`)
}

async function toggleRoundTrip(page, session, width, visibleAuto, observations) {
  const label = `${width}px auto`
  await assertColumns(page, session, visibleAuto, label, observations)
  await proveCoreUsable(page, session, label, observations)
  const toggle = page.getByRole('button', { name: /Tags and Updated columns/, exact: false })
  await expect(toggle).toHaveAttribute('aria-label', AUTO_LABEL)
  await realClick(page, toggle, `${width}px first three-dot click`, observations)
  await expect(toggle).toHaveAttribute('aria-label', visibleAuto ? HIDDEN_LABEL : SHOWN_LABEL)
  await assertColumns(page, session, !visibleAuto, `${width}px first click`, observations)
  await proveCoreUsable(page, session, `${width}px first click`, observations)
  await realClick(page, toggle, `${width}px second three-dot click`, observations)
  await expect(toggle).toHaveAttribute('aria-label', AUTO_LABEL)
  await assertColumns(page, session, visibleAuto, `${width}px restored auto`, observations)
  await proveCoreUsable(page, session, `${width}px restored auto`, observations)
}

test('wide browser positive control: 720px List exposes both headers/cells and all core actions', { concurrency: false }, async () => {
  await withPanel('wide-positive-control', async ({ page, session, observations }) => {
    await toggleRoundTrip(page, session, 720, true, observations)
  })
})

test('constant 1280px window: SAME task row crosses 649/648/647px with compiled CSS and real toggle round-trips', { concurrency: false }, async () => {
  await withPanel('boundary-same-row', async ({ page, session, observations }) => {
    for (const [width, visible] of [[649, true], [648, true], [647, false]]) {
      await resizePanel(page, width)
      await toggleRoundTrip(page, session, width, visible, observations)
      await screenshot(page, `boundary-${width}-restored-auto`)
    }
    await resizePanel(page, 720)
    await assertColumns(page, session, true, 'narrow-to-wide recovery', observations)
    await proveCoreUsable(page, session, 'narrow-to-wide recovery', observations)
  })
})

test('320px dock floor: both headers/cells hide, reveal, reset without losing core-column usability or data', { concurrency: false }, async () => {
  await withPanel('dock-floor-320', async ({ page, session, observations }) => {
    await resizePanel(page, 320)
    await toggleRoundTrip(page, session, 320, false, observations)
  })
})

test('instrument: removing only the compiled narrow-hide rule kills the SAME oracle and restored CSS recovers', { concurrency: false }, async () => {
  await withPanel('instrument-css-restored', async ({ page, session, observations }) => {
    await resizePanel(page, 647)
    await assertColumns(page, session, false, 'instrument baseline', observations)
    await proveCoreUsable(page, session, 'instrument baseline controls', observations)
    const pristine = Buffer.from(assets.get(stylesheetPath))
    const fault = removeNarrowHideRule(originalCSS)
    let rejection
    try {
      assets.set(stylesheetPath, Buffer.from(fault.css))
      await page.reload()
      await expect(page.locator('tbody tr')).toHaveCount(2)
      await page.evaluate(() => globalThis.document.fonts.ready)
      await resizePanel(page, 647)
      await page.evaluate((selector) => {
        globalThis.__listGeometryOriginalRow = globalThis.document.querySelector(selector)
        globalThis.__listGeometryInitialUpdated = globalThis.__listGeometryOriginalRow.cells[5].textContent.trim()
      }, PRIMARY_ROW)
      await proveCoreUsable(page, session, 'instrument fault controls', observations)
      const mutant = await measureColumns(page, session)
      // Establish the deliberate fault affected all four intended nodes, not a
      // crash, different viewport, absent data, probe stub, or fake rectangle.
      assert.deepEqual(mutant.map((column) => ({ name: column.name, header: column.header.display, cell: column.cell.display })),
        [{ name: 'Tags', header: 'table-cell', cell: 'table-cell' },
          { name: 'Updated', header: 'table-cell', cell: 'table-cell' }],
        'Fault setup control: the removed compiled rule leaves BOTH columns visibly rendered')
      try {
        assertVisibility(mutant, false, 'instrument fault')
        assert.fail('The unchanged narrow visibility oracle must reject the deliberate CSS fault')
      } catch (error) {
        assert.equal(error.code, 'ERR_ASSERTION', 'Instrument rejection must be an assertion, not a setup error')
        assert.match(error.message, /^instrument fault: Tags header must have computed display none, got table-cell/,
          'The SAME oracle must name the actual responsive-rule fault')
        rejection = { name: error.name, code: error.code, message: error.message }
      }
      save('fault-probe', { ...sourceContext, kind: 'in-memory compiler-output instrument probe',
        productionPreChangeRED: 'not claimed', childTestExit: 'see separate external runner receipt; caught oracle rejection is not a child exit',
        originalCSSSHA256: sha256(originalCSS), mutantCSSSHA256: sha256(fault.css),
        removedRule: fault.removed, mutant, rejection })
      await screenshot(page, 'fault-probe-synthetic')
    } finally {
      assets.set(stylesheetPath, pristine)
      await page.reload()
      await expect(page.locator('tbody tr')).toHaveCount(2)
      await page.evaluate(() => globalThis.document.fonts.ready)
      await resizePanel(page, 647)
      await page.evaluate((selector) => {
        globalThis.__listGeometryOriginalRow = globalThis.document.querySelector(selector)
        globalThis.__listGeometryInitialUpdated = globalThis.__listGeometryOriginalRow.cells[5].textContent.trim()
      }, PRIMARY_ROW)
    }
    assert.equal(sha256(assets.get(stylesheetPath)), sha256(pristine), 'Restoration must restore the exact compiled stylesheet bytes')
    await assertColumns(page, session, false, 'instrument restored CSS', observations)
    await proveCoreUsable(page, session, 'instrument restored controls', observations)
    save('fault-probe-restored', { ...sourceContext, compiledCSSSHA256: sha256(pristine), restored: true, rejection })
  })
})
