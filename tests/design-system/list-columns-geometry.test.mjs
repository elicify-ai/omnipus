// PANEL-LIST-BROWSER-COLUMNS-2222 — real compiled List geometry and controls.
// Founder T6/T11 (2026-10-07) supersede hidden Tags/Updated and the reveal toggle:
// every column remains in the native table/accessibility tree at every width.
// Scrolling happens INSIDE the table viewport, with aligned header/body cells.
// Keep the original 649/648/647px SAME-row sweep and 320px dock-floor coverage,
// plus native sorting, Tags filtering, exact-once Title activation and data
// conservation. A missing compiled overflow-auto rule must fail the same oracle.
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
const PRIMARY_TITLE = 'Alpha synthetic task'
const PRIMARY_ROW = 'tbody tr:has(button[data-task-open][aria-label="Alpha synthetic task, status Inbox"])'
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
  { name: 'Tags', column: 6, headerName: 'Tags column — filter' },
  { name: 'Updated', column: 8, headerName: 'Updated column — sort' },
]
const CORE = [
  { name: 'Priority', column: 1, headerName: 'Pri column — sort and filter', value: 'P1',
    sort: 'descending', order: ['Zulu synthetic task', PRIMARY_TITLE] },
  { name: 'Title', column: 2, headerName: 'Title column — sort', value: PRIMARY_TITLE,
    sort: 'ascending', order: [PRIMARY_TITLE, 'Zulu synthetic task'] },
  { name: 'Status', column: 3, headerName: 'Status column — sort and filter', value: 'Inbox',
    sort: 'descending', order: ['Zulu synthetic task', PRIMARY_TITLE] },
  { name: 'Agent', column: 7, headerName: 'Agent column — sort and filter', value: 'Mira',
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

function removeInternalScrollRule(css) {
  const sheet = postcss.parse(css)
  const removed = []
  // Exact compiled utility: no fabricated row/cell styles or viewport changes.
  sheet.walkRules((rule) => {
    if (rule.selector !== '.overflow-auto') return
    assert.match(rule.toString(), /overflow:\s*auto/,
      'The removed rule must own the native internal table scrolling')
    removed.push({ rule: rule.toString() })
    rule.remove()
  })
  assert.equal(removed.length, 1, 'Exactly one narrowly targeted compiled scrolling rule must be removed')
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
  const fault = removeInternalScrollRule(originalCSS)
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
  assert.ok(FAULT === '' || FAULT === 'remove-internal-scroll', 'Unknown CSS fault mode is a setup error, never a skip')
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
      globalThis.__listGeometryInitialUpdated = globalThis.__listGeometryOriginalRow.cells[7].textContent.trim()
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
    tags: [...row.cells[5].querySelectorAll('[data-task-tag]')].map((tag) => tag.firstElementChild.textContent),
    updated: row.cells[7].textContent.trim(),
    initialUpdated: globalThis.__listGeometryInitialUpdated,
    title: row.querySelector('button[data-task-open]').textContent,
    cellCount: row.cells.length,
  }))
  assert.deepEqual({ sameRow: data.sameRow, tags: data.tags, title: data.title, cellCount: data.cellCount },
    { sameRow: true, tags: TASKS[0].tags, title: PRIMARY_TITLE, cellCount: 8 },
    `${label}: the SAME synthetic task row retains its accessible DOM data`)
  // Wall-clock-relative wording is intentionally not frozen. The valid
  // synthetic date must render a nonempty relative value, conserved by toggles.
  assert.match(data.updated, /^(?:just now|\d+[mhd] ago)$/,
    `${label}: Updated must retain a nonempty relative-date value, not a missing-date placeholder`)
  assert.equal(data.updated, data.initialUpdated, `${label}: scrolling/resizing must conserve the Updated cell's data`)
  const alignment = await page.locator(PRIMARY_ROW).evaluate((row) => {
    const headers = row.closest('table').querySelectorAll('thead th')
    return [...row.cells].map((cell, index) => ({
      headerLeft: headers[index].getBoundingClientRect().left,
      cellLeft: cell.getBoundingClientRect().left,
      headerWidth: headers[index].getBoundingClientRect().width,
      cellWidth: cell.getBoundingClientRect().width,
    }))
  })
  assert.equal(alignment.length, 8, `${label}: every column has a matching header and cell`)
  for (const pair of alignment) {
    assert.ok(Math.abs(pair.headerLeft - pair.cellLeft) <= 1, `${label}: header and body left edges align`)
    assert.ok(Math.abs(pair.headerWidth - pair.cellWidth) <= 1, `${label}: header and body widths align`)
  }
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

async function scrollControlWithinPanel(page, locator, label, observations) {
  const measure = () => locator.evaluate((element) => {
    const panel = globalThis.document.getElementById('fixture-panel')
    const document = globalThis.document.scrollingElement
    const rect = element.getBoundingClientRect(), panelRect = panel.getBoundingClientRect()
    let owner = element.parentElement
    while (owner && panel.contains(owner)) {
      if (['auto', 'scroll'].includes(globalThis.getComputedStyle(owner).overflowX)) break
      owner = owner.parentElement
    }
    const contained = !!owner && panel.contains(owner)
    const bounds = contained ? owner.getBoundingClientRect() : null
    return {
      page: { x: globalThis.scrollX, y: globalThis.scrollY,
        left: document.scrollLeft, top: document.scrollTop,
        width: document.scrollWidth, clientWidth: document.clientWidth },
      owner: contained ? { insidePanel: true, overflowX: globalThis.getComputedStyle(owner).overflowX,
        left: owner.scrollLeft, width: owner.scrollWidth, clientWidth: owner.clientWidth,
        boundedByPanel: bounds.left >= panelRect.left && bounds.right <= panelRect.right } : null,
      horizontalScrollNeeded: contained && (rect.left < bounds.left || rect.right > bounds.right),
    }
  })
  const before = await measure()
  observations.push({ label: `${label}: before panel-local scroll`, scroll: before })
  assert.ok(before.owner, `${label}: a real horizontal scroll owner must exist INSIDE the List panel`)
  assert.equal(before.owner.boundedByPanel, true, `${label}: the scroll owner's bounds must remain inside the panel`)
  assert.ok(before.page.width <= before.page.clientWidth, `${label}: revealed columns must not make the whole page scroll sideways`)
  // Native scrolling only: never alter layout, product state, or CSS to fit.
  await locator.scrollIntoViewIfNeeded()
  await settle(page)
  const after = await measure()
  observations.push({ label: `${label}: after panel-local scroll`, scroll: after })
  assert.deepEqual(after.page, before.page, `${label}: scrolling a control must not scroll or widen the whole page`)
  assert.ok(after.owner, `${label}: the horizontal scroll owner must stay inside the panel`)
  assert.equal(after.owner.boundedByPanel, true, `${label}: the scrolled owner must still fit inside the panel`)
  if (before.horizontalScrollNeeded) {
    assert.notEqual(after.owner.left, before.owner.left, `${label}: an offscreen control must move the PANEL's horizontal scroll position`)
  }
}

async function proveCoreUsable(page, session, label, observations, revealedNarrow = false) {
  if (revealedNarrow) {
    const title = page.getByRole('button', { name: `${PRIMARY_TITLE}, status Inbox`, exact: true })
    const control = await measureControl(title)
    observations.push({ label: `${label}: revealed Title nonzero control`, control })
    assert.ok(control.rect.width > 0 && control.rect.height > 0,
      `${label}: revealed Title task control must have nonzero usable geometry, got ${control.rect.width}px by ${control.rect.height}px`)
  }
  for (const column of CORE) {
    const selector = `${PRIMARY_ROW} td:nth-child(${column.column})`
    await expect(page.locator(selector)).toHaveCSS('display', 'table-cell')
    if (column.name === 'Status') await expect(page.locator(selector)).toHaveAccessibleName(column.value) // T18 dot is decorative/aria-hidden.
    else await expect(page.locator(selector)).toHaveText(column.value)
    assert.equal((await accessibleNode(session, selector)).ignored, false, `${label}: ${column.name} data remains accessible`)
    const trigger = page.getByRole('button', { name: column.headerName, exact: true })
    if (revealedNarrow) await scrollControlWithinPanel(page, trigger, `${label}: ${column.name} header`, observations)
    await realClick(page, trigger, `${label}: ${column.name} header`, observations)
    const menuItem = page.getByRole('menuitem', { name: `Sort ${column.sort}`, exact: true })
    await expect(menuItem).toBeVisible()
    await menuItem.click()
    await expect(page.locator(`thead th:nth-child(${column.column})`)).toHaveAttribute('aria-sort', column.sort)
    assert.deepEqual(await page.locator('tbody tr button[data-task-open]').evaluateAll((buttons) => buttons.map((button) => button.textContent)),
      column.order, `${label}: ${column.name} sorting must operate on the actual synthetic task rows`)
  }
  const title = page.getByRole('button', { name: `${PRIMARY_TITLE}, status Inbox`, exact: true })
  const beforeCount = await page.evaluate(() => globalThis.__listGeometryTaskClicks.length)
  if (revealedNarrow) await scrollControlWithinPanel(page, title, `${label}: Title task activation`, observations)
  await realClick(page, title, `${label}: Title task activation`, observations)
  assert.deepEqual(await page.evaluate((start) => globalThis.__listGeometryTaskClicks.slice(start), beforeCount),
    [{ id: TASKS[0].id, title: PRIMARY_TITLE, tags: TASKS[0].tags, updated_at: TASKS[0].updated_at }],
    `${label}: actual Title click delivers the intact task exactly once to the parent callback`)
}

async function proveRevealedHeadersUsable(page, label, observations) {
  const tags = page.getByRole('button', { name: GOVERNED[0].headerName, exact: true })
  await scrollControlWithinPanel(page, tags, `${label}: Tags header`, observations)
  await realClick(page, tags, `${label}: Tags header`, observations)
  const tag = page.getByRole('menuitemcheckbox', { name: TASKS[0].tags[0], exact: true })
  await tag.click()
  await expect(tag).toHaveAttribute('aria-checked', 'true')
  assert.deepEqual(await page.locator('tbody tr button[data-task-open]').evaluateAll((buttons) => buttons.map((button) => button.textContent)),
    [PRIMARY_TITLE], `${label}: revealed Tags filter must select the actual matching task, not merely open a menu`)
  await page.getByRole('menuitem', { name: 'Clear filter', exact: true }).click()
  await expect(page.locator('tbody tr')).toHaveCount(2)
  assert.deepEqual(await page.locator('tbody tr button[data-task-open]').evaluateAll((buttons) => buttons.map((button) => button.textContent)),
    [PRIMARY_TITLE, 'Zulu synthetic task'], `${label}: clearing the Tags filter restores both actual tasks in the existing Agent order`)

  const updated = page.getByRole('button', { name: GOVERNED[1].headerName, exact: true })
  await scrollControlWithinPanel(page, updated, `${label}: Updated header`, observations)
  await realClick(page, updated, `${label}: Updated header`, observations)
  await page.getByRole('menuitem', { name: 'Sort ascending', exact: true }).click()
  await expect(page.locator('thead th:nth-child(8)')).toHaveAttribute('aria-sort', 'ascending')
  assert.deepEqual(await page.locator('tbody tr button[data-task-open]').evaluateAll((buttons) => buttons.map((button) => button.textContent)),
    ['Zulu synthetic task', PRIMARY_TITLE], `${label}: revealed Updated sorting orders the actual June 19/June 20 fixture dates`)
}

async function exerciseColumns(page, session, width, observations) {
  const label = `${width}px all columns`
  await assertColumns(page, session, true, label, observations)
  await expect(page.getByRole('button', { name: /Tags and Updated columns/ })).toHaveCount(0)
  await proveCoreUsable(page, session, label, observations, true)
  await proveRevealedHeadersUsable(page, label, observations)
  await assertColumns(page, session, true, `${width}px after native scroll/actions`, observations)
}

test('wide browser positive control: 720px List exposes every header/cell and all core actions', { concurrency: false }, async () => {
  await withPanel('wide-positive-control', async ({ page, session, observations }) => {
    await exerciseColumns(page, session, 720, observations)
  })
})

test('constant 1280px window: SAME task row keeps all columns across 649/648/647px with compiled CSS and native scroll', { concurrency: false }, async () => {
  await withPanel('boundary-same-row', async ({ page, session, observations }) => {
    for (const width of [649, 648, 647]) {
      await resizePanel(page, width)
      await exerciseColumns(page, session, width, observations)
      await screenshot(page, `boundary-${width}-all-columns`)
    }
    await resizePanel(page, 720)
    await assertColumns(page, session, true, 'narrow-to-wide recovery', observations)
    await proveCoreUsable(page, session, 'narrow-to-wide recovery', observations, true)
  })
})

test('320px dock floor: all headers/cells remain reachable by table-only scroll without losing core usability or data', { concurrency: false }, async () => {
  await withPanel('dock-floor-320', async ({ page, session, observations }) => {
    await resizePanel(page, 320)
    await exerciseColumns(page, session, 320, observations)
  })
})

test('instrument: removing only the compiled internal-scroll rule kills the SAME oracle and restored CSS recovers', { concurrency: false }, async () => {
  await withPanel('instrument-css-restored', async ({ page, session, observations }) => {
    await resizePanel(page, 320)
    await assertColumns(page, session, true, 'instrument baseline', observations)
    await proveCoreUsable(page, session, 'instrument baseline controls', observations, true)
    const pristine = Buffer.from(assets.get(stylesheetPath))
    const fault = removeInternalScrollRule(originalCSS)
    let rejection
    try {
      assets.set(stylesheetPath, Buffer.from(fault.css))
      await page.reload()
      await expect(page.locator('tbody tr')).toHaveCount(2)
      await page.evaluate(() => globalThis.document.fonts.ready)
      await resizePanel(page, 320)
      await page.evaluate((selector) => {
        globalThis.__listGeometryOriginalRow = globalThis.document.querySelector(selector)
        globalThis.__listGeometryInitialUpdated = globalThis.__listGeometryOriginalRow.cells[7].textContent.trim()
      }, PRIMARY_ROW)
      const mutant = await measureColumns(page, session)
      // The data/accessibility nodes remain real and present. Only their
      // native horizontal scroll owner is missing, so no crash can fake RED.
      assertVisibility(mutant, true, 'instrument fault data still rendered')
      await assertColumns(page, session, true, 'instrument fault data conserved', observations)
      try {
        await scrollControlWithinPanel(page,
          page.getByRole('button', { name: GOVERNED[1].headerName, exact: true }),
          'instrument fault: Updated header', observations)
        assert.fail('The unchanged scroll oracle must reject the deliberate CSS fault')
      } catch (error) {
        assert.equal(error.code, 'ERR_ASSERTION', 'Instrument rejection must be an assertion, not a setup error')
        assert.match(error.message, /real horizontal scroll owner must exist INSIDE the List panel/,
          'The SAME oracle must name the actual missing-scroll fault')
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
      await resizePanel(page, 320)
      await page.evaluate((selector) => {
        globalThis.__listGeometryOriginalRow = globalThis.document.querySelector(selector)
        globalThis.__listGeometryInitialUpdated = globalThis.__listGeometryOriginalRow.cells[7].textContent.trim()
      }, PRIMARY_ROW)
    }
    assert.equal(sha256(assets.get(stylesheetPath)), sha256(pristine), 'Restoration must restore the exact compiled stylesheet bytes')
    await assertColumns(page, session, true, 'instrument restored CSS', observations)
    await proveCoreUsable(page, session, 'instrument restored controls', observations, true)
    save('fault-probe-restored', { ...sourceContext, compiledCSSSHA256: sha256(pristine), restored: true, rejection })
  })
})
