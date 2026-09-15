/**
 * view-kinds.spec.ts — the browser half of view-kinds-design-2026-09-03.
 *
 * The API tests (pkg/gateway/rest_knowledge_view*_test.go) already prove the
 * server computes per-unit totals and refuses to combine them. They cannot
 * prove the thing this file is for: that a person clicking around the Library
 * SEES those numbers, sees WHY there is no combined figure, and is told which
 * rows were left out. A correct number nobody is shown is not a delivered
 * feature.
 *
 * ── The fixture, and why it is not committed ────────────────────────────────
 * Everything below runs against the vault that
 * `scripts/uat/gen-viewkinds-e2e-fixture.mjs` writes and
 * `scripts/uat/setup-viewkinds-instance.sh` mounts into a throwaway
 * OMNIPUS_HOME. Its numbers are literal and its answer key is computed by the
 * generator BEFORE any server sees the files, so an assertion on "3450 g" is
 * an assertion against ground truth rather than against whatever came back.
 *
 * The facts this file needs are read out of `e2e-facts.json` at runtime rather
 * than duplicated here, so the fixture and the spec cannot drift into
 * agreeing with each other about a number neither of them checked.
 *
 * ── retries: 0, at describe level ───────────────────────────────────────────
 * The suite's global retry count exists for real-LLM latency variance. Nothing
 * in this file involves a model. "The combined figure is absent" and "the
 * excluded counter is shown" are not properties a second attempt establishes,
 * and a renderer regression that fails two runs in three would ship green.
 *
 * ── Proof the selectors can fail ────────────────────────────────────────────
 * Each assertion group was run once with its subject deliberately broken (a
 * mistyped testid, an inverted expectation) and observed to FAIL before being
 * committed in its final form; the run log is in the UAT report. A selector
 * never observed failing is a selector that might match nothing.
 */

import { test, expect, type Page } from '@playwright/test'
import fs from 'node:fs'

test.describe.configure({ retries: 0 })

/** Where the generator wrote its answer key. Named by the runner. */
const FACTS_FILE = process.env.VIEWKINDS_E2E_FACTS
if (FACTS_FILE === undefined || !fs.existsSync(FACTS_FILE)) {
  throw new Error(
    '[view-kinds] VIEWKINDS_E2E_FACTS must name the e2e-facts.json written by\n'
    + 'scripts/uat/gen-viewkinds-e2e-fixture.mjs. Without it this file would have to\n'
    + 'hard-code the fixture\'s numbers, and a hard-coded number that drifts from the\n'
    + 'fixture is a test that passes against the wrong vault.',
  )
}

interface Facts {
  base_file: string
  orphan_base_file: string
  damaged_base_file: string
  chart_base_file: string
  base_view_tabs: string[]
  base_view_labels: string[]
  summary_view: string
  chart_view: string
  chart_values: number[]
  per_unit_totals: { unit: string; value: string; count: number }[]
  excluded_count: number
  forbidden_combined_value: string
}
const FACTS = JSON.parse(fs.readFileSync(FACTS_FILE, 'utf8')) as Facts

/** The vault is mounted under this folder inside the workspace work tree. */
const VAULT = 'vault'
const p = (rel: string) => `${VAULT}/${rel}`

/**
 * Strip the renderer's thousands separators before comparing a figure.
 *
 * The fixture's answer key holds exact decimal text ("3450"); the renderer
 * groups it for a human ("3,450"). Both are right, and comparing them raw
 * fails for a formatting reason that has nothing to do with the rule under
 * test. Normalising only commas keeps the comparison an EQUALITY against the
 * pre-computed key rather than weakening it to a substring-of-digits match:
 * "3468.9" still does not appear in "3,450 … 9.4 … 9.5" after this.
 */
const unformat = (s: string) => s.replace(/,/g, '')

/**
 * The workspace the fixture vault lives in.
 *
 * Found by NAME, not by `is_default`: the setup script creates a dedicated
 * workspace for the fixture, and the gateway's own default workspace has no
 * vault in it. Picking the default would open an empty Library and every
 * assertion below would fail for a reason that has nothing to do with views.
 */
async function fixtureWorkspaceId(page: Page): Promise<string> {
  const res = await page.request.get('/api/v1/workspaces')
  expect(res.status(), 'GET /api/v1/workspaces must succeed').toBe(200)
  const body = (await res.json()) as { workspaces?: { id: string; name: string }[] } | { id: string; name: string }[]
  const list = Array.isArray(body) ? body : (body.workspaces ?? [])
  const ws = list.find((w) => w.name.startsWith('UAT '))
  expect(ws, `a workspace named "UAT …" must exist; got ${list.map((w) => w.name).join(', ')}`)
    .toBeDefined()
  return (ws as { id: string }).id
}

async function openLibrary(page: Page, workspaceId: string): Promise<void> {
  await page.goto(`/#/library?workspace=${workspaceId}`)
  await expect(page.getByTestId('library-explorer')).toBeVisible({ timeout: 30_000 })
}

/** Walk into a folder in the listing, then select a file inside it. */
async function openEntry(page: Page, entryPath: string): Promise<void> {
  const parts = entryPath.split('/')
  let acc = ''
  for (const seg of parts.slice(0, -1)) {
    acc = acc === '' ? seg : `${acc}/${seg}`
    await page.getByTestId(`library-row-${acc}`).click()
  }
  await page.getByTestId(`library-row-${entryPath}`).click()
}

// ---------------------------------------------------------------------------
// (a) A .base opens VIEWS, and the tab names are the SERVER's
// ---------------------------------------------------------------------------

test('clicking a .base opens server-enumerated view tabs, and does not download', async ({ page }) => {
  const ws = await fixtureWorkspaceId(page)

  // The download check has to be armed BEFORE the click. A `.base` used to be
  // an unrecognised binary-ish file, and the honest failure mode of losing the
  // preview is that the Library falls back to handing the user a file — which
  // looks like nothing happening at all.
  const downloads: string[] = []
  page.on('download', (d) => downloads.push(d.suggestedFilename()))

  // Capture the server's OWN answer for this file, so the tab set can be
  // compared against it rather than against a list written in this spec. A
  // hard-coded expectation would keep passing if the SPA went back to parsing
  // the `.base` itself and happened to guess the same names.
  const answered = page.waitForResponse(
    (r) => r.url().includes('/knowledge/base-views') && r.url().includes('Recipes.base') && r.status() === 200,
  )

  await openLibrary(page, ws)
  await openEntry(page, p(FACTS.base_file))

  const served = (await (await answered).json()) as { views: { name: string; label: string }[] }
  const serverNames = served.views.map((v) => v.name).sort()
  expect(serverNames.length, 'the fixture base must own more than one view, or "tabs" proves nothing')
    .toBeGreaterThan(1)

  await expect(page.getByTestId('base-preview')).toBeVisible({ timeout: 30_000 })
  const tablist = page.getByRole('tablist', { name: 'Views' })
  await expect(tablist).toBeVisible()

  for (const name of serverNames) {
    await expect(
      page.getByTestId(`base-view-tab-${name}`),
      `a tab must exist for the server-named view "${name}"`,
    ).toBeVisible()
  }
  // And no EXTRA tabs: the count must equal the server's, so a SPA that
  // invented a ninth view from the file's own text would fail here.
  await expect(tablist.getByRole('tab')).toHaveCount(serverNames.length)

  expect(downloads, 'clicking a .base must render it, never hand the user a file').toEqual([])
})

// ---------------------------------------------------------------------------
// (b) The summary view: group subtotals, per-unit totals, and the reason
// ---------------------------------------------------------------------------

test('a summary view shows per-unit totals, group subtotals, and why there is no combined figure', async ({ page }) => {
  const ws = await fixtureWorkspaceId(page)
  await openLibrary(page, ws)
  await openEntry(page, p(FACTS.base_file))
  await expect(page.getByTestId('base-preview')).toBeVisible({ timeout: 30_000 })

  await page.getByTestId(`base-view-tab-${FACTS.summary_view}`).click()
  await expect(page.getByTestId('view-parts')).toBeVisible({ timeout: 30_000 })
  await expect(page.getByTestId('view-part-figures')).toBeVisible()

  // 1. ONE figure per unit, each one carrying its unit and its exact value.
  const figures = page.getByTestId('viewpart-figure')
  await expect(figures).toHaveCount(FACTS.per_unit_totals.length)
  const figureText = unformat((await figures.allInnerTexts()).join(' | '))
  for (const t of FACTS.per_unit_totals) {
    expect(figureText, `the figures row must state ${t.value} ${t.unit}`).toContain(t.unit)
    expect(figureText, `the figures row must state ${t.value} ${t.unit}`).toContain(t.value)
  }

  // 2. THE forbidden number, checked as an equality against a value the
  //    generator computed before the run — not "does it look combined".
  const pageText = unformat(await page.getByTestId('view-parts').innerText())
  expect(
    pageText,
    `a total summed across units would read ${FACTS.forbidden_combined_value}; it must appear nowhere`,
  ).not.toContain(FACTS.forbidden_combined_value)

  // 3. The reason, said out loud. A missing combined figure with no
  //    explanation is indistinguishable from a renderer that forgot to draw
  //    one, which is exactly the ambiguity design §3 G2 asks the footer to
  //    remove.
  await expect(page.getByTestId('viewpart-no-grand-total').first()).toBeVisible()
  const reason = await page.getByTestId('viewpart-no-grand-total').first().innerText()
  expect(reason.toLowerCase(), 'the reason must speak about units').toMatch(/unit|currenc/)

  // 4. Per-group subtotal rows, and at least one group carrying MORE THAN ONE
  //    unit — a fixture where every group is single-unit would pass with the
  //    per-group split broken.
  const subtotals = page.getByTestId('viewpart-group-subtotal')
  await expect(subtotals.first()).toBeVisible()
  expect(await subtotals.count(), 'a grouped summary must draw per-group subtotal rows')
    .toBeGreaterThan(FACTS.per_unit_totals.length)
})

// ---------------------------------------------------------------------------
// (c) The excluded-rows counter
// ---------------------------------------------------------------------------

test('rows whose unit is missing are counted and named, not silently dropped', async ({ page }) => {
  const ws = await fixtureWorkspaceId(page)
  await openLibrary(page, ws)
  await openEntry(page, p(FACTS.base_file))
  await expect(page.getByTestId('base-preview')).toBeVisible({ timeout: 30_000 })
  await page.getByTestId(`base-view-tab-${FACTS.summary_view}`).click()
  await expect(page.getByTestId('view-parts')).toBeVisible({ timeout: 30_000 })

  const line = page.getByTestId('viewpart-excluded-line').first()
  await expect(line, 'the G3 excluded-rows line must be shown').toBeVisible()
  expect(
    await line.innerText(),
    `the fixture has exactly ${FACTS.excluded_count} rows with no unit`,
  ).toContain(String(FACTS.excluded_count))

  // Excluded rows are SHOWN, not filtered out — "shown, excluded, counted".
  // The rows are still in the table and carry a mark of their own.
  await expect(page.getByTestId('viewpart-excluded-mark').first()).toBeVisible()
})

// ---------------------------------------------------------------------------
// (d) Negative chart values are drawn, not clipped away
// ---------------------------------------------------------------------------

test('a chart with negative values draws them below the zero line', async ({ page }) => {
  expect(
    FACTS.chart_values.some((v) => v < 0) && FACTS.chart_values.some((v) => v > 0),
    'the fixture must straddle zero, or this test cannot see the clipping bug',
  ).toBe(true)

  const ws = await fixtureWorkspaceId(page)
  await openLibrary(page, ws)
  await openEntry(page, p(FACTS.chart_base_file))
  await expect(page.getByTestId('base-preview')).toBeVisible({ timeout: 30_000 })
  await page.getByTestId(`base-view-tab-${FACTS.chart_view}`).click()
  await expect(page.getByTestId('view-part-chart')).toBeVisible({ timeout: 30_000 })

  // Zero is INTERIOR to the domain, so the renderer must draw its own zero
  // line. Its absence would mean the domain never included the negatives.
  //
  // `toBeAttached`, not `toBeVisible`: an SVG <line> is a zero-height box, and
  // Playwright's visibility rule (a non-empty bounding box) reports every
  // horizontal rule as hidden. Asserting visibility here failed against a
  // renderer that was drawing the line perfectly — a false failure. What CAN
  // be asserted about being seen is asserted instead: the chart's own svg is
  // visible, and every plotted point below lands inside its viewBox.
  const chartSvg = page.locator('[data-testid="view-part-chart"] svg').first()
  await expect(chartSvg, 'the chart itself must be on screen').toBeVisible()
  const zeroLine = page.getByTestId('viewpart-chart-zero-line')
  await expect(zeroLine, 'a series straddling zero must draw an interior zero line').toBeAttached()
  const yZero = Number(await zeroLine.getAttribute('y1'))
  expect(Number.isFinite(yZero), 'the zero line must have a numeric y').toBe(true)

  // The polyline's own points, read out of the DOM. In SVG y grows DOWNWARD,
  // so a genuinely drawn negative value sits at a y GREATER than the zero
  // line's. The clipping bug produced either a negative height (dropped by the
  // browser) or a value pinned to the axis; both leave every point at or above
  // zero, which is what this assertion catches.
  const polyline = page.getByTestId('viewpart-chart-line').first()
  await expect(polyline).toBeVisible()
  const points = (await polyline.getAttribute('points')) ?? ''
  const ys = points.trim().split(/\s+/).map((pt) => Number(pt.split(',')[1])).filter(Number.isFinite)
  expect(ys.length, 'the chart must plot every fixture point').toBe(FACTS.chart_values.length)
  expect(
    ys.some((y) => y > yZero + 1),
    `at least one point must sit below the zero line (yZero=${yZero}, ys=${ys.join(',')})`,
  ).toBe(true)
  expect(
    ys.some((y) => y < yZero - 1),
    `and at least one above it (yZero=${yZero}, ys=${ys.join(',')})`,
  ).toBe(true)

  // Every point stays inside the drawing area. A value plotted outside the
  // viewBox is invisible in exactly the way the clipping bug was.
  const viewBox = (await page.locator('[data-testid="view-part-chart"] svg').first().getAttribute('viewBox')) ?? ''
  const height = Number(viewBox.split(/\s+/)[3])
  expect(Number.isFinite(height), 'the chart svg must declare a viewBox').toBe(true)
  for (const y of ys) {
    expect(y, `point y=${y} must be inside the 0..${height} drawing area`).toBeGreaterThanOrEqual(0)
    expect(y, `point y=${y} must be inside the 0..${height} drawing area`).toBeLessThanOrEqual(height)
  }
})

// ---------------------------------------------------------------------------
// (e) The escape hatch: a base whose views cannot be loaded still shows bytes
// ---------------------------------------------------------------------------

test('a base file whose views could not be loaded still offers View raw', async ({ page }) => {
  const ws = await fixtureWorkspaceId(page)
  await openLibrary(page, ws)
  await openEntry(page, p(FACTS.damaged_base_file))
  await expect(page.getByTestId('base-preview')).toBeVisible({ timeout: 30_000 })

  const message = page.getByTestId('base-preview-no-views')
  await expect(message).toBeVisible({ timeout: 30_000 })
  // "could not be loaded" is a DIFFERENT fact from "declares no views", and
  // saying the second over a file whose views all failed would be false.
  expect(
    (await message.innerText()).toLowerCase(),
    'a base with rejected views must say they could not be loaded',
  ).toContain('could not be loaded')

  const raw = page.getByTestId('base-preview-view-raw')
  await expect(raw, 'the escape hatch must be offered').toBeVisible()
  await raw.click()
  await expect(page.getByTestId('base-preview-raw')).toBeVisible({ timeout: 30_000 })
  // The bytes themselves, not merely a pane: a reader who cannot see the file
  // has not escaped anything.
  await expect(page.getByTestId('base-preview-raw')).toContainText('views:')
})

test('a base file that imported no views also offers View raw', async ({ page }) => {
  const ws = await fixtureWorkspaceId(page)
  await openLibrary(page, ws)
  await openEntry(page, p(FACTS.orphan_base_file))
  await expect(page.getByTestId('base-preview')).toBeVisible({ timeout: 30_000 })

  const message = page.getByTestId('base-preview-no-views')
  await expect(message).toBeVisible({ timeout: 30_000 })
  expect(
    (await message.innerText()).toLowerCase(),
    'a base with nothing imported must say so, not claim its views failed',
  ).toContain('no views were imported')

  await page.getByTestId('base-preview-view-raw').click()
  await expect(page.getByTestId('base-preview-raw')).toBeVisible({ timeout: 30_000 })
  // The bytes, for the same reason the damaged case asserts them: a raw pane
  // that opens and shows nothing is not an escape hatch, and an assertion that
  // stops at "the pane appeared" cannot tell the two apart. This test passed
  // on its first run while showing an empty pane — the assertion below is what
  // turned that into the finding it was.
  await expect(page.getByTestId('base-preview-raw')).toContainText('Never Imported')
})
