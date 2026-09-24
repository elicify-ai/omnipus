import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'

type Check = { id: string; kind: string; file?: string; test?: string; story?: string; applicable: boolean }
type Manifest = { component: string; stories: Array<{ file: string; exports: string[] }>; checks: Check[] }
type StoryIndex = { entries: Record<string, { id: string; importPath: string; exportName: string; type: string }> }
type AxeViolation = { id: string; nodes: Array<{ target: unknown[]; html: string; failureSummary?: string }> }
type AccessibilityException = {
  id: string; owner: string; component: string; story: string; check: string; rule: string; node: string
  trigger: string; focusScope: string; requiredAutomatedProofs: string[]; manualEvidence: string; reference: string; rationale: string
}
type Metadata = {
  keyboard?: Array<{
    trigger: string; key: string; expectFocus?: string; expectExpanded?: boolean
    expectScroll?: { selector: string; axis: 'x' | 'y'; direction: 'increase' | 'decrease' }
  }>
  pointerTargets?: string[]
  motionTargets?: string[]
  motionSetup?: { trigger: string; key: string }
  forcedColorTargets?: string[]
  forcedColors?: ForcedColorsContract
  reflowExemptions?: Array<{ selector: string; reason: string }>
  browserAssertions?: Array<{ selector: string; attribute?: string; property?: string; value?: string; text?: string }>
}
type ColorProperty = 'color' | 'backgroundColor' | 'borderTopColor' | 'outlineColor'
type ForcedColorDifference = {
  cue: 'foreground' | 'indicator' | 'progress'
  selector: string
  property: ColorProperty
  againstSelector: string
  againstProperty: ColorProperty
  actualSystemColor?: 'ButtonText' | 'Highlight' | 'HighlightText' | 'CanvasText'
  againstSystemColor?: 'ButtonFace' | 'Canvas' | 'Highlight'
}
type ForcedColorsContract = {
  boundaries?: string[]
  differences?: ForcedColorDifference[]
  focus?: string[]
  states?: Array<{ selector: string; attribute: string; value: string }>
  stateTransitions?: Array<{
    selector: string; attribute: string; from: string; action: 'click'; to: string
    differences: ForcedColorDifference[]
  }>
}

type StoryReportViolation = {
  id?: string; impact?: string | null
  nodes?: Array<{ target?: unknown[]; any?: Array<{ data?: unknown }> }>
}
type StoryReport = { type?: string; status?: string; result?: { violations?: StoryReportViolation[] } }
type StoryFinished = { storyId?: string; status?: string; reporters?: StoryReport[] }
// Storybook announces why a story errored on these channel events; STORY_FINISHED itself
// only says "error". Captured so a failure names its cause instead of a bare status diff.
const storyErrorEvents = ['storyErrored', 'storyThrewException', 'playFunctionThrewException', 'unhandledErrorsWhilePlaying'] as const
type StoryErrorEvent = { event: string; payload: unknown }

function describeStoryFailure(finished: StoryFinished | undefined, errors: StoryErrorEvent[] = []) {
  const lines: string[] = []
  for (const report of finished?.reporters ?? []) {
    if (report.status !== 'failed') continue
    const violations = report.result?.violations ?? []
    if (violations.length === 0) lines.push(`failed ${report.type ?? 'unknown'} report`)
    for (const violation of violations) {
      for (const node of violation.nodes ?? []) {
        const data = node.any?.find((check) => check.data !== undefined && check.data !== null)?.data
        lines.push(`failed ${report.type ?? 'unknown'} report: ${violation.id} (${violation.impact ?? 'no impact'}) at ${JSON.stringify(node.target)}${data === undefined ? '' : ` ${JSON.stringify(data)}`}`)
      }
    }
  }
  for (const error of errors) lines.push(`${error.event}: ${JSON.stringify(error.payload)}`)
  return lines.length === 0 ? '' : `\n${lines.join('\n')}`
}

function assertSuccessfulStoryFinished(finished: StoryFinished | undefined, storyId: string, errors: StoryErrorEvent[] = []) {
  const cause = describeStoryFailure(finished, errors)
  expect(finished, `${storyId} play/report lifecycle must finish successfully${cause}`).toMatchObject({ storyId, status: 'success' })
  expect(finished?.reporters?.some((report) => report.status === 'failed') ?? false, `${storyId} must not contain a failed Storybook report${cause}`).toBe(false)
}

const manifestDir = resolve('design-system/manifests')
const exceptionRegistry = JSON.parse(readFileSync(resolve('design-system/accessibility-exceptions.json'), 'utf8')) as { version: number; exceptions: AccessibilityException[] }
const expectedException = {
  id: 'radix-dropdown-menu-modal-trigger', owner: 'design-system', component: 'DropdownMenu', story: 'MenuContract', check: 'dropdown-menu-axe',
  rule: 'aria-hidden-focus', node: '#storybook-root', trigger: '[data-ds-trigger][aria-haspopup="menu"][aria-expanded="true"]', focusScope: '[role="menu"]',
  requiredAutomatedProofs: ['single-reviewed-axe-node', 'open-menu-full-axe', 'single-hidden-root-focusable', 'initial-focus-in-menu', 'tab-contained', 'shift-tab-contained', 'programmatic-focus-contained', 'escape-restores-trigger', 'restored-document-full-axe'],
  manualEvidence: 'outstanding-screen-reader', reference: 'https://github.com/radix-ui/primitives/discussions/3994',
}
if (exceptionRegistry.version !== 1 || exceptionRegistry.exceptions.length !== 1) throw new Error('Accessibility exception registry has unknown version or entries')
const dropdownMenuException = exceptionRegistry.exceptions[0]
for (const [key, value] of Object.entries(expectedException)) {
  if (JSON.stringify(dropdownMenuException[key as keyof AccessibilityException]) !== JSON.stringify(value)) throw new Error(`Accessibility exception registry drift: ${key}`)
}
const manifests: Manifest[] = existsSync(manifestDir)
  ? readdirSync(manifestDir).filter((file) => file.endsWith('.json')).sort()
      .map((file) => JSON.parse(readFileSync(resolve(manifestDir, file), 'utf8')))
  : []
const browserKinds = new Set(['axe', 'keyboard', 'browser', 'pointer', 'reduced-motion', 'forced-colors', 'root-size', 'zoom', 'reflow'])

async function openStory(page: Page, manifest: Manifest, check: Check) {
  const index = await page.request.get('/index.json').then((response) => response.json() as Promise<StoryIndex>)
  const declared = manifest.stories.find((story) => story.exports.includes(check.story ?? ''))
  expect(declared, `${manifest.component}/${check.id} must declare story export ${check.story}`).toBeTruthy()
  const entry = Object.values(index.entries).find((candidate) =>
    candidate.type === 'story' && candidate.exportName === check.story &&
    candidate.importPath.replace(/^\.\//, '').endsWith(declared!.file.replace(/^\.\//, '')))
  expect(entry, `${declared!.file}#${check.story} must exist in Storybook index`).toBeTruthy()
  await page.addInitScript((errorEvents) => {
    const state = window as Window & {
      __designSystemStoryFinished?: StoryFinished
      __designSystemStoryErrors?: StoryErrorEvent[]
      __STORYBOOK_ADDONS_CHANNEL__?: { on: (event: string, listener: (payload: unknown) => void) => void }
    }
    state.__designSystemStoryFinished = undefined
    state.__designSystemStoryErrors = []
    const attach = () => {
      if (!state.__STORYBOOK_ADDONS_CHANNEL__) {
        window.setTimeout(attach, 0)
        return
      }
      state.__STORYBOOK_ADDONS_CHANNEL__.on('storyFinished', (payload) => {
        state.__designSystemStoryFinished = payload as StoryFinished
      })
      for (const event of errorEvents) {
        state.__STORYBOOK_ADDONS_CHANNEL__.on(event, (payload) => {
          state.__designSystemStoryErrors?.push({ event, payload })
        })
      }
    }
    attach()
  }, [...storyErrorEvents])
  await page.goto(`/iframe.html?id=${entry!.id}&viewMode=story`)
  await expect(page.locator('[data-design-system-config]')).toBeAttached()
  await page.waitForFunction((storyId) => {
    const state = window as Window & { __designSystemStoryFinished?: { storyId?: string } }
    return state.__designSystemStoryFinished?.storyId === storyId
  }, entry!.id, { timeout: 30_000 })
  const { finished, errors } = await page.evaluate(() => {
    const state = window as Window & { __designSystemStoryFinished?: StoryFinished; __designSystemStoryErrors?: StoryErrorEvent[] }
    return { finished: state.__designSystemStoryFinished, errors: state.__designSystemStoryErrors ?? [] }
  })
  assertSuccessfulStoryFinished(finished, entry!.id, errors)
  const metadata = JSON.parse(await page.locator('[data-design-system-config]').getAttribute('data-design-system-config') ?? '{}') as Metadata
  const renderedTarget = check.kind === 'pointer' ? metadata.pointerTargets?.[0]
    : check.kind === 'keyboard' ? metadata.keyboard?.[0]?.trigger
      : check.kind === 'forced-colors' ? (metadata.forcedColors?.boundaries?.[0] ?? metadata.forcedColors?.differences?.[0]?.selector)
        : check.kind === 'reduced-motion' ? (metadata.motionSetup?.trigger ?? metadata.motionTargets?.[0])
          : metadata.browserAssertions?.[0]?.selector ?? metadata.pointerTargets?.[0]
            ?? metadata.forcedColors?.boundaries?.[0] ?? metadata.forcedColors?.differences?.[0]?.selector
            ?? metadata.motionTargets?.[0] ?? metadata.keyboard?.[0]?.trigger
  expect(renderedTarget, `${entry!.id} must declare a selector for its rendered component`).toBeTruthy()
  await expect(page.locator(renderedTarget!)).toBeVisible()
  return metadata
}

async function assertNoOverflow(page: Page, exemptions: Metadata['reflowExemptions'] = []) {
  const overflow = await page.evaluate((allowed) => {
    const exempt = new Set(allowed.map((entry) => entry.selector))
    return [...document.querySelectorAll<HTMLElement>('body *')]
      .filter((element) => {
        const style = getComputedStyle(element)
        return style.clip !== 'rect(0px, 0px, 0px, 0px)' && !element.classList.contains('sr-only')
      })
      .filter((element) => ![...exempt].some((selector) => element.matches(selector) || element.closest(selector)))
      .map((element) => {
        const rect = element.getBoundingClientRect()
        let left = rect.left
        let right = rect.right
        for (let ancestor = element.parentElement; ancestor; ancestor = ancestor.parentElement) {
          const style = getComputedStyle(ancestor)
          if (!['hidden', 'clip', 'auto', 'scroll'].includes(style.overflowX)) continue
          const clippingRect = ancestor.getBoundingClientRect()
          left = Math.max(left, clippingRect.left)
          right = Math.min(right, clippingRect.right)
        }
        return { tag: element.tagName, className: element.className, left, right, viewportWidth: document.documentElement.clientWidth }
      })
      .filter((entry) => entry.right > entry.left)
      .filter((entry) => entry.left < -1 || entry.right > entry.viewportWidth + 1)
  }, exemptions)
  expect(overflow, 'content must reflow without unregistered horizontal overflow').toEqual([])
}

async function assertForcedColorsContract(page: Page, contract: ForcedColorsContract | undefined) {
  expect(contract, 'forced-colors checks require parameters.designSystem.forcedColors').toBeTruthy()
  const assertionCount = (contract?.boundaries?.length ?? 0) + (contract?.differences?.length ?? 0)
    + (contract?.focus?.length ?? 0) + (contract?.states?.length ?? 0) + (contract?.stateTransitions?.length ?? 0)
  expect(assertionCount, 'forced-colors contract must declare at least one observable cue').toBeGreaterThan(0)

  for (const selector of contract?.boundaries ?? []) {
    const boundary = await page.locator(selector).evaluate((element) => {
      const style = getComputedStyle(element)
      const sides = ['Top', 'Right', 'Bottom', 'Left'].map((side) => ({
        style: style[`border${side}Style` as keyof CSSStyleDeclaration] as string,
        width: parseFloat(style[`border${side}Width` as keyof CSSStyleDeclaration] as string),
        color: style[`border${side}Color` as keyof CSSStyleDeclaration] as string,
      }))
      return { sides, background: style.backgroundColor }
    })
    const visibleSides = boundary.sides.filter((side) => side.style !== 'none' && side.width >= 1 && side.color !== boundary.background)
    expect(visibleSides.length, `${selector} must retain a contrasting boundary of at least one CSS pixel on one or more sides in forced colors`).toBeGreaterThan(0)
  }

  for (const difference of contract?.differences ?? []) {
    const readColors = async () => ({
      actual: await page.locator(difference.selector).evaluate((element, property) => getComputedStyle(element)[property], difference.property),
      against: await page.locator(difference.againstSelector).evaluate((element, property) => getComputedStyle(element)[property], difference.againstProperty),
    })
    await expect.poll(async () => { const colors = await readColors(); return colors.actual !== colors.against }, {
      message: `${difference.cue} cue ${difference.selector}.${difference.property} must differ from ${difference.againstSelector}.${difference.againstProperty} in forced colors`,
    }).toBe(true)
    if (difference.actualSystemColor || difference.againstSystemColor) {
      const expected = await page.evaluate(({ actualSystemColor, againstSystemColor }) => {
        const probe = document.createElement('span')
        probe.style.color = actualSystemColor ?? 'CanvasText'
        probe.style.backgroundColor = againstSystemColor ?? 'Canvas'
        document.body.append(probe)
        const style = getComputedStyle(probe)
        const colors = { actual: style.color, against: style.backgroundColor }
        probe.remove()
        return colors
      }, difference)
      await expect.poll(readColors, { message: `${difference.cue} cue must settle to its declared system-color pair` }).toEqual({
        actual: difference.actualSystemColor ? expected.actual : (await readColors()).actual,
        against: difference.againstSystemColor ? expected.against : (await readColors()).against,
      })
    }
  }

  for (const selector of contract?.focus ?? []) {
    const target = page.locator(selector)
    await target.focus()
    await expect(target).toBeFocused()
    const indicator = await target.evaluate((element) => {
      const style = getComputedStyle(element)
      let surrounding = element.parentElement
      let surroundingBackground = 'rgba(0, 0, 0, 0)'
      while (surrounding) {
        surroundingBackground = getComputedStyle(surrounding).backgroundColor
        if (surroundingBackground !== 'rgba(0, 0, 0, 0)' && surroundingBackground !== 'transparent') break
        surrounding = surrounding.parentElement
      }
      return {
        style: style.outlineStyle,
        width: parseFloat(style.outlineWidth),
        offset: parseFloat(style.outlineOffset),
        color: style.outlineColor,
        background: style.backgroundColor,
        surroundingBackground,
      }
    })
    expect(indicator.style, `${selector} must retain a focus outline in forced colors`).not.toBe('none')
    expect(indicator.width, `${selector} forced-color focus outline must be at least one CSS pixel`).toBeGreaterThanOrEqual(1)
    const paintedAgainst = indicator.offset >= 0 ? indicator.surroundingBackground : indicator.background
    expect(indicator.color, `${selector} focus outline must differ from the surface it paints over in forced colors`).not.toBe(paintedAgainst)
  }

  for (const state of contract?.states ?? []) {
    await expect(page.locator(state.selector), `${state.selector} must expose its meaningful state in forced colors`).toHaveAttribute(state.attribute, state.value)
  }

  for (const transition of contract?.stateTransitions ?? []) {
    const target = page.locator(transition.selector)
    await expect(target).toHaveAttribute(transition.attribute, transition.from)
    await target.click()
    await expect(target).toHaveAttribute(transition.attribute, transition.to)
    for (const difference of transition.differences) {
      const readColors = async () => ({
        actual: await page.locator(difference.selector).evaluate((element, property) => getComputedStyle(element)[property], difference.property),
        against: await page.locator(difference.againstSelector).evaluate((element, property) => getComputedStyle(element)[property], difference.againstProperty),
      })
      await expect.poll(async () => { const colors = await readColors(); return colors.actual !== colors.against }, {
        message: `${difference.cue} cue after ${transition.to} must remain distinguishable in forced colors`,
      }).toBe(true)
      if (difference.actualSystemColor || difference.againstSystemColor) {
        const expected = await page.evaluate(({ actualSystemColor, againstSystemColor }) => {
          const probe = document.createElement('span')
          probe.style.color = actualSystemColor ?? 'CanvasText'
          probe.style.backgroundColor = againstSystemColor ?? 'Canvas'
          document.body.append(probe)
          const style = getComputedStyle(probe)
          const colors = { actual: style.color, against: style.backgroundColor }
          probe.remove()
          return colors
        }, difference)
        await expect.poll(readColors, { message: `${difference.cue} cue after ${transition.to} must settle to its declared system-color pair` }).toEqual({
          actual: difference.actualSystemColor ? expected.actual : (await readColors()).actual,
          against: difference.againstSystemColor ? expected.against : (await readColors()).against,
        })
      }
    }
  }
}

function assertDropdownMenuExceptionBoundary(manifest: Manifest, check: Check, violations: AxeViolation[]) {
  expect({ component: manifest.component, story: check.story, check: check.id }).toEqual({
    component: dropdownMenuException.component, story: dropdownMenuException.story, check: dropdownMenuException.check,
  })
  expect(violations.map((violation) => violation.id)).toEqual([dropdownMenuException.rule])
  const nodes = violations[0].nodes
  expect(nodes).toHaveLength(1)
  expect(nodes[0].target).toEqual([dropdownMenuException.node])
  expect(nodes[0].html).toContain(`id="${dropdownMenuException.node.slice(1)}"`)
  return nodes[0]
}

async function proveDropdownMenuFocusContainment(page: Page) {
  const outcome: Record<string, unknown> = {}
  const menu = page.locator(dropdownMenuException.focusScope)
  const trigger = page.locator('[data-ds-trigger][aria-haspopup="menu"]')
  const root = page.locator(dropdownMenuException.node)
  await expect(menu).toBeVisible()
  await expect(trigger).toHaveAttribute('aria-expanded', 'true')
  await expect(menu).toBeFocused()
  outcome.initialFocus = true
  const focusables = root.locator('button:not([disabled]), a[href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])')
  expect(await focusables.count()).toBe(1)
  await expect(focusables).toHaveAttribute('data-ds-trigger', 'true')
  outcome.hiddenRootFocusables = 1
  for (const key of ['Tab', 'Shift+Tab']) {
    await page.keyboard.press(key)
    await expect(menu).toBeFocused()
    outcome[key === 'Tab' ? 'tabContained' : 'shiftTabContained'] = true
  }
  await trigger.evaluate((element) => (element as HTMLElement).focus())
  await expect.poll(() => menu.evaluate((element) => element.contains(document.activeElement))).toBe(true)
  await expect(trigger).not.toBeFocused()
  outcome.programmaticFocusContained = true
  await page.keyboard.press('Escape')
  await expect(menu).toBeHidden()
  await expect(trigger).toBeFocused()
  outcome.escapeRestoredTrigger = true
  return outcome
}

async function pointerHitsTarget(page: Page, selector: string, x: number, y: number, coarse: boolean) {
  await page.evaluate(({ selector }) => {
    const state = window as Window & { __designSystemPointerHit?: boolean | null }
    state.__designSystemPointerHit = null
    document.addEventListener('click', (event) => {
      event.preventDefault()
      event.stopImmediatePropagation()
    }, { capture: true, once: true })
    document.addEventListener('pointerdown', (event) => {
      event.preventDefault()
      event.stopImmediatePropagation()
      state.__designSystemPointerHit = event.target instanceof Element && event.target.closest(selector) !== null
    }, { capture: true, once: true })
  }, { selector })
  if (coarse) await page.touchscreen.tap(x, y)
  else await page.mouse.click(x, y)
  await page.waitForFunction(() => (window as Window & { __designSystemPointerHit?: boolean | null }).__designSystemPointerHit !== null)
  return page.evaluate(() => (window as Window & { __designSystemPointerHit?: boolean }).__designSystemPointerHit === true)
}

async function performKeyboardStep(page: Page, step: NonNullable<Metadata['keyboard']>[number]) {
  const scrollBefore = step.expectScroll
    ? await page.locator(step.expectScroll.selector).evaluate((element, axis) => axis === 'x' ? element.scrollLeft : element.scrollTop, step.expectScroll.axis)
    : undefined
  await page.locator(step.trigger).press(step.key)
  if (step.expectFocus) await expect(page.locator(step.expectFocus)).toBeFocused()
  if (step.expectExpanded !== undefined) await expect(page.locator(step.trigger)).toHaveAttribute('aria-expanded', String(step.expectExpanded))
  if (step.expectScroll && scrollBefore !== undefined) {
    const { selector, axis, direction } = step.expectScroll
    const currentOffset = () => page.locator(selector).evaluate((element, expectedAxis) => expectedAxis === 'x' ? element.scrollLeft : element.scrollTop, axis)
    const message = `${selector} must scroll ${axis === 'x' ? 'horizontally' : 'vertically'} after ${step.key}`
    if (direction === 'increase') await expect.poll(currentOffset, { message }).toBeGreaterThan(scrollBefore)
    else await expect.poll(currentOffset, { message }).toBeLessThan(scrollBefore)
  }
}

test('manifest coverage exists before browser verification can pass', async () => {
  expect(manifests.length, 'design-system/manifests contains no component manifests').toBeGreaterThan(0)
})

test('reviewed DropdownMenu exception rejects an extra violation, extra node and missing focus trap', async ({ page }) => {
  const manifest = { component: 'DropdownMenu', stories: [], checks: [] }
  const check = { id: 'dropdown-menu-axe', kind: 'axe', story: 'MenuContract', applicable: true }
  const reviewedNode = { target: ['#storybook-root'], html: '<div id="storybook-root" aria-hidden="true"></div>' }
  expect(() => assertDropdownMenuExceptionBoundary(manifest, check, [
    { id: 'aria-hidden-focus', nodes: [reviewedNode] },
    { id: 'button-name', nodes: [] },
  ])).toThrow()
  expect(() => assertDropdownMenuExceptionBoundary(manifest, check, [{
    id: 'aria-hidden-focus', nodes: [reviewedNode, { target: ['#other'], html: '<div id="other"></div>' }],
  }])).toThrow()

  await page.setContent('<div id="storybook-root" aria-hidden="true"><button data-ds-trigger="true" aria-haspopup="menu" aria-expanded="true">Menu</button></div><div role="menu" tabindex="-1">Item</div>')
  await page.locator('[role="menu"]').focus()
  await expect(proveDropdownMenuFocusContainment(page)).rejects.toThrow()
})

test('forced-colors contract rejects stripped boundaries and indicators', async ({ page }) => {
  await page.emulateMedia({ forcedColors: 'active' })
  await page.setContent('<button id="boundary" style="border:1px none CanvasText;background:Canvas;color:CanvasText">Action</button><div id="track" style="background:Canvas"><span id="indicator" style="background:Canvas">40%</span></div>')
  await expect(assertForcedColorsContract(page, { boundaries: ['#boundary'] })).rejects.toThrow(/contrasting boundary/)
  await page.setContent('<aside id="side-boundary" style="border-left:1px solid CanvasText;background:Canvas;color:CanvasText">Panel</aside>')
  await assertForcedColorsContract(page, { boundaries: ['#side-boundary'] })
  await page.setContent('<div style="background:Canvas"><button id="focus" style="background:Highlight;outline:2px solid Canvas;outline-offset:2px">Action</button></div>')
  await expect(assertForcedColorsContract(page, { focus: ['#focus'] })).rejects.toThrow(/surface it paints over/)
  await page.locator('#focus').evaluate((element) => { element.style.outlineColor = 'Highlight' })
  await assertForcedColorsContract(page, { focus: ['#focus'] })
  await expect(assertForcedColorsContract(page, {
    differences: [{ cue: 'progress', selector: '#indicator', property: 'backgroundColor', againstSelector: '#track', againstProperty: 'backgroundColor' }],
  })).rejects.toThrow(/progress cue/)
})

test('keyboard scroll outcome rejects a non-scrollable target', async ({ page }) => {
  await page.setContent('<div id="viewport" tabindex="0" style="width:100px;overflow:hidden"><div style="width:100px">No overflow</div></div>')
  await page.locator('#viewport').focus()
  await expect(performKeyboardStep(page, {
    trigger: '#viewport', key: 'ArrowRight',
    expectScroll: { selector: '#viewport', axis: 'x', direction: 'increase' },
  })).rejects.toThrow(/must scroll horizontally/)
})

test('story readiness rejects wrong-story and failed-reporter terminal events', () => {
  expect(() => assertSuccessfulStoryFinished({ storyId: 'other-story', status: 'success', reporters: [{ status: 'success' }] }, 'expected-story')).toThrow(/expected-story/)
  expect(() => assertSuccessfulStoryFinished({ storyId: 'expected-story', status: 'success', reporters: [{ status: 'failed' }] }, 'expected-story')).toThrow(/failed Storybook report/)
  const contrastFailure = {
    storyId: 'expected-story', status: 'error',
    reporters: [{ type: 'a11y', status: 'failed', result: { violations: [{ id: 'color-contrast', impact: 'serious', nodes: [{ target: ['button'], any: [{ data: { contrastRatio: 2.1 } }] }] }] } }],
  }
  expect(() => assertSuccessfulStoryFinished(contrastFailure, 'expected-story')).toThrow(/color-contrast \(serious\) at \["button"\] \{"contrastRatio":2\.1\}/)
  expect(() => assertSuccessfulStoryFinished({ storyId: 'expected-story', status: 'error', reporters: [] }, 'expected-story', [{ event: 'playFunctionThrewException', payload: { message: 'boom' } }])).toThrow(/playFunctionThrewException: \{"message":"boom"\}/)
})

test('reflow rejects viewport protrusion without treating internal control paint as page overflow', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 720 })
  await page.setContent('<meta name="viewport" content="width=device-width, initial-scale=1"><div id="wide" style="width:500px">Wide</div>')
  await expect(assertNoOverflow(page)).rejects.toThrow(/content must reflow/)
  await page.setContent('<button style="width:12px"><span style="display:block;width:18px">✓</span></button>')
  await assertNoOverflow(page)
  await page.setContent('<div id="clip" style="width:100px;overflow:hidden"><div style="width:500px">Clipped paint</div></div>')
  await assertNoOverflow(page)
  await page.locator('#clip').evaluate((element) => { element.style.overflow = 'visible' })
  await expect(assertNoOverflow(page)).rejects.toThrow(/content must reflow/)
})

for (const manifest of manifests) {
  for (const check of manifest.checks.filter((item) => item.applicable && browserKinds.has(item.kind))) {
    test(`${manifest.component} [check:${check.id}] ${check.kind}`, async ({ page }, testInfo) => {
      // Storybook's automatic a11y reporter can finish after 10s under a saturated
      // cross-browser matrix. Reserve 30s for its terminal event and 15s for the
      // component assertion; exact story/status/reporter checks remain fail-closed.
      testInfo.setTimeout(45_000)
      if (check.kind === 'forced-colors') await page.emulateMedia({ forcedColors: 'active' })
      const metadata = await openStory(page, manifest, check)

      if (check.kind === 'axe') {
        if (manifest.component === dropdownMenuException.component && check.id === dropdownMenuException.check && check.story === dropdownMenuException.story) {
          // A modal menu intentionally makes the host Storybook document inert. Scan the
          // component subtree with every axe rule, then run the one reviewed document-level
          // rule separately so host-page landmark rules cannot masquerade as component defects.
          const componentResult = await new AxeBuilder({ page }).include(dropdownMenuException.focusScope).analyze()
          expect(componentResult.violations, 'DropdownMenu component subtree must pass the complete axe ruleset').toEqual([])
          const exceptionResult = await new AxeBuilder({ page }).withRules([dropdownMenuException.rule]).analyze()
          const rawFinding = assertDropdownMenuExceptionBoundary(manifest, check, exceptionResult.violations as AxeViolation[])
          const focusProofs = await proveDropdownMenuFocusContainment(page)
          const restoredDocumentResult = await new AxeBuilder({ page }).analyze()
          expect(restoredDocumentResult.violations, 'DropdownMenu host document must pass the complete axe ruleset after modal focus restoration').toEqual([])
          await testInfo.attach('reviewed-accessibility-exception.json', {
            body: Buffer.from(JSON.stringify({
              exception: dropdownMenuException,
              componentViolations: componentResult.violations,
              rawFinding,
              focusProofs,
              restoredDocumentViolations: restoredDocumentResult.violations,
            }, null, 2)),
            contentType: 'application/json',
          })
        } else {
          const result = await new AxeBuilder({ page }).analyze()
          expect(result.violations).toEqual([])
        }
      } else if (check.kind === 'keyboard') {
        expect(metadata.keyboard?.length, 'keyboard checks require parameters.designSystem.keyboard').toBeGreaterThan(0)
        for (const step of metadata.keyboard ?? []) {
          expect(step.expectFocus || step.expectExpanded !== undefined || step.expectScroll, 'every keyboard step requires an observable focus, expanded-state or scroll outcome').toBeTruthy()
          await performKeyboardStep(page, step)
        }
      } else if (check.kind === 'pointer') {
        const coarse = testInfo.project.name.includes('coarse-pointer')
        expect(await page.evaluate(() => matchMedia('(pointer: coarse)').matches), `project must expose a ${coarse ? 'coarse' : 'fine'} pointer`).toBe(coarse)
        expect(metadata.pointerTargets?.length, 'pointer checks require parameters.designSystem.pointerTargets').toBeGreaterThan(0)
        const targets = await Promise.all((metadata.pointerTargets ?? []).map(async (selector) => ({
          box: await page.locator(selector).boundingBox(),
          borderRadius: await page.locator(selector).evaluate((element) => parseFloat(getComputedStyle(element).borderTopLeftRadius) || 0),
        })))
        const minimum = coarse ? 44 : 24
        const effective = []
        for (let index = 0; index < targets.length; index += 1) {
          const { box, borderRadius } = targets[index]
          expect(box, `pointer target ${metadata.pointerTargets![index]} must render`).not.toBeNull()
          const center = { x: box!.x + box!.width / 2, y: box!.y + box!.height / 2 }
          const width = Math.max(box!.width, minimum)
          const height = Math.max(box!.height, minimum)
          const radiusX = width / 2 - 1
          const radiusY = height / 2 - 1
          const cornerX = radiusX - (box!.width >= minimum ? Math.min(borderRadius + 1, radiusX - 1) : 0)
          const cornerY = radiusY - (box!.height >= minimum ? Math.min(borderRadius + 1, radiusY - 1) : 0)
          for (const point of [
            center,
            { x: center.x - radiusX, y: center.y }, { x: center.x + radiusX, y: center.y },
            { x: center.x, y: center.y - radiusY }, { x: center.x, y: center.y + radiusY },
            { x: center.x - cornerX, y: center.y - cornerY }, { x: center.x + cornerX, y: center.y - cornerY },
            { x: center.x - cornerX, y: center.y + cornerY }, { x: center.x + cornerX, y: center.y + cornerY },
          ]) {
            expect(await pointerHitsTarget(page, metadata.pointerTargets![index], point.x, point.y, coarse), `${metadata.pointerTargets![index]} must receive a real pointer event at (${point.x}, ${point.y}) across its ${minimum}px effective region`).toBe(true)
          }
          effective.push({ x: center.x - width / 2, y: center.y - height / 2, width, height })
        }
        for (let left = 0; left < effective.length; left += 1) for (let right = left + 1; right < effective.length; right += 1) {
          const a = effective[left]; const b = effective[right]
          expect(a.x + a.width <= b.x || b.x + b.width <= a.x || a.y + a.height <= b.y || b.y + b.height <= a.y, 'pointer hit regions must not overlap').toBe(true)
        }
      } else if (check.kind === 'reduced-motion') {
        await page.emulateMedia({ reducedMotion: 'reduce' })
        expect(await page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches)).toBe(true)
        if (metadata.motionSetup) await page.locator(metadata.motionSetup.trigger).press(metadata.motionSetup.key)
        expect(metadata.motionTargets?.length, 'reduced-motion checks require parameters.designSystem.motionTargets').toBeGreaterThan(0)
        for (const selector of metadata.motionTargets ?? []) {
          const timing = await page.locator(selector).evaluate((element) => {
            const style = getComputedStyle(element)
            return {
              animationNames: style.animationName.split(',').map((value) => value.trim()),
              animationDurations: style.animationDuration.split(',').map((value) => parseFloat(value)),
              transitionProperties: style.transitionProperty.split(',').map((value) => value.trim()),
              transitionDurations: style.transitionDuration.split(',').map((value) => parseFloat(value)),
            }
          })
          const animationsInactive = timing.animationNames.every((name, index) => name === 'none' || (timing.animationDurations[index % timing.animationDurations.length] ?? 0) <= 0.01)
          const transitionsInactive = timing.transitionProperties.every((property, index) => property === 'none' || (timing.transitionDurations[index % timing.transitionDurations.length] ?? 0) <= 0.01)
          expect({ animationsInactive, transitionsInactive }, `${selector} must expose no active animation or transition under reduced motion`).toEqual({ animationsInactive: true, transitionsInactive: true })
        }
      } else if (check.kind === 'forced-colors') {
        expect(await page.evaluate(() => matchMedia('(forced-colors: active)').matches), `forced colors unsupported in ${testInfo.project.name}; record as a coverage gap`).toBe(true)
        await assertForcedColorsContract(page, metadata.forcedColors)
      } else if (check.kind === 'root-size') {
        for (const [input, expected] of [[10, 12], [12, 12], [14, 14], [20, 20], [22, 20]]) {
          await page.evaluate((size) => { document.documentElement.style.setProperty('--user-font-size', `${size}px`) }, input)
          expect(await page.evaluate(() => parseFloat(getComputedStyle(document.documentElement).fontSize))).toBe(expected)
          const undersized = await page.evaluate(() => [...document.querySelectorAll<HTMLElement>('body *')]
            .filter((element) => [...element.childNodes].some((node) => node.nodeType === Node.TEXT_NODE && node.textContent?.trim()))
            .filter((element) => {
              const style = getComputedStyle(element)
              return style.display !== 'none' && style.visibility !== 'hidden' && element.getBoundingClientRect().width > 0
            })
            .map((element) => ({ element: element.tagName, text: element.textContent?.trim().slice(0, 80), pixels: parseFloat(getComputedStyle(element).fontSize) }))
            .filter((entry) => entry.pixels < 12))
          expect(undersized, `visible text must remain at least 12px for --user-font-size:${input}px`).toEqual([])
          await expect(page.locator('[data-design-system-config]')).toBeAttached()
        }
      } else if (check.kind === 'zoom') {
        await page.setViewportSize({ width: 640, height: 720 })
        await page.evaluate(() => { document.documentElement.style.zoom = '2' })
        expect(await page.evaluate(() => parseFloat(getComputedStyle(document.documentElement).zoom))).toBe(2)
        await assertNoOverflow(page, metadata.reflowExemptions)
      } else if (check.kind === 'reflow') {
        await page.setViewportSize({ width: 320, height: 720 })
        await assertNoOverflow(page, metadata.reflowExemptions)
      } else {
        expect(metadata.browserAssertions?.length, 'browser checks require parameters.designSystem.browserAssertions').toBeGreaterThan(0)
        for (const assertion of metadata.browserAssertions ?? []) {
          expect(assertion.attribute || assertion.property || assertion.text, 'browser assertions require an attribute, property or text outcome beyond visibility').toBeTruthy()
          const target = page.locator(assertion.selector)
          await expect(target).toBeVisible()
          if (assertion.attribute) {
            if (assertion.value === undefined) await expect(target).toHaveAttribute(assertion.attribute)
            else await expect(target).toHaveAttribute(assertion.attribute, assertion.value)
          }
          if (assertion.property) {
            expect(assertion.value, 'browser property assertions require an expected value').toBeDefined()
            await expect.poll(() => target.evaluate((element, property) => String((element as unknown as Record<string, unknown>)[property]), assertion.property!)).toBe(assertion.value)
          }
          if (assertion.text) await expect(target).toContainText(assertion.text)
        }
        if (manifest.component === 'Button' && check.id === 'button-auxiliary-browser') {
          const nativeMiddleClick = async (selector: string) => {
            const link = page.locator(selector)
            await expect(link).toBeVisible()
            await page.evaluate((targetSelector) => {
              const state = window as Window & { __auxiliaryProof?: { matches: boolean; trusted: boolean; button: number; prevented: boolean } }
              delete state.__auxiliaryProof
              document.addEventListener('auxclick', (event) => {
                window.setTimeout(() => {
                  state.__auxiliaryProof = {
                    matches: event.target instanceof Element && event.target.closest(targetSelector) !== null,
                    trusted: event.isTrusted,
                    button: event.button,
                    prevented: event.defaultPrevented,
                  }
                }, 0)
              }, { capture: true, once: true })
            }, selector)
            const bounds = await link.boundingBox()
            expect(bounds, 'link must have a real mouse target').not.toBeNull()
            const opened = page.context().waitForEvent('page', { timeout: 1_000 }).catch((error: unknown) => {
              if (error instanceof Error && error.name === 'TimeoutError') return null
              throw error
            })
            await page.mouse.click(bounds!.x + bounds!.width / 2, bounds!.y + bounds!.height / 2, { button: 'middle' })
            const newPage = await opened
            await expect.poll(() => page.evaluate(() => (window as Window & { __auxiliaryProof?: unknown }).__auxiliaryProof)).toBeDefined()
            const event = await page.evaluate(() => (window as Window & { __auxiliaryProof?: unknown }).__auxiliaryProof)
            return { newPage, event }
          }
          const enabled = await nativeMiddleClick('[data-testid="enabled-link"]')
          expect(enabled.event).toEqual({ matches: true, trusted: true, button: 1, prevented: false })
          if (testInfo.project.use.browserName === 'webkit' && process.platform === 'darwin') {
            // Playwright's macOS WebKit dispatches trusted auxclick but does not open a
            // tab for an unmodified middle click, including on plain anchors. Its Linux
            // build (the CI runner) does open one, so Linux WebKit takes the branch below
            // and must prove the tab opens at the link's own URL.
            expect(enabled.newPage).toBeNull()
          } else {
            expect(enabled.newPage, 'enabled middle click must open a tab in this engine').not.toBeNull()
            await expect(enabled.newPage!).toHaveURL(/#auxiliary-enabled$/)
            await enabled.newPage!.close()
          }
          for (const state of ['disabled', 'pending']) {
            const blocked = await nativeMiddleClick(`[data-testid="${state}-link"]`)
            if (blocked.newPage) await blocked.newPage.close()
            expect(blocked.event).toEqual({ matches: true, trusted: true, button: 1, prevented: true })
            expect(blocked.newPage, `${state} href-backed Button must suppress native middle-click navigation`).toBeNull()
          }
        }
      }
    })
  }
}
