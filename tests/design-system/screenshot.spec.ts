import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { expect, test, type Page } from '@playwright/test'

// THE APPEARANCE GATE (GitHub issue #753)
//
// This directory's README says, correctly, that "no screenshots or snapshot
// baselines belong in" browser.spec.ts. That suite proves STRUCTURE and
// BEHAVIOUR -- the right DOM shape, ARIA state, keyboard/pointer contracts,
// forced-colors contracts. None of those checks can catch a component that
// renders with the wrong padding, the wrong colour or the wrong font weight
// while every attribute and every behavioural assertion still passes. This
// file is the gate for THAT: pixel screenshots of each catalogued component,
// captured against the static `dist/storybook` build, diffed on every PR.
//
// It must run against the static build, never the dev server. Evidence
// (2026-09-20, this commit): the generated browser checks in this same
// directory failed 16-of-18 against the dev server and passed 18-of-18
// against `dist/storybook` -- the dev server compiles on demand and loses
// the race under parallel load. A screenshot gate on a dev server would be
// pure noise. `playwright.design-system.config.ts` only starts the dev-server
// `webServer` when STORYBOOK_URL is unset; CI always sets it, pointed at a
// static file server over `dist/storybook` (built by `npm run
// build:storybook` immediately before `npm run test:design-system:browser`
// in .github/workflows/pr.yml -- this suite runs in the same job, after the
// same build, before that dev-server fallback could ever be reached).
//
// Decisions recorded here trace to issue #753 section 2. See
// playwright.design-system.config.ts for the "which projects" and
// "threshold" decisions (they live in the `expect`/`projects` config, not
// here). The two decisions specific to WHICH stories get captured:

type Manifest = {
  component: string
  variants?: string[]
  stories: Array<{ file: string; exports: string[] }>
  checks: Array<{ id: string; kind: string; story?: string; applicable: boolean }>
}
type StoryIndex = { entries: Record<string, { id: string; importPath: string; exportName: string; type: string }> }
type StoryFinished = { storyId?: string; status?: string; reporters?: Array<{ status?: string }> }

const manifestDir = resolve('design-system/manifests')
const manifests: Manifest[] = existsSync(manifestDir)
  ? readdirSync(manifestDir).filter((file) => file.endsWith('.json')).sort()
      .map((file) => JSON.parse(readFileSync(resolve(manifestDir, file), 'utf8')))
  : []

// Decision -- "states": a full per-component states matrix (hover,
// focus-visible, disabled, error, loading, empty, ...) needs a manifest that
// declares which story renders which state; today's `manifest.states` is a
// prose label list ("focus-visible", "idle") with no reliable mapping to a
// story export name, and adding that mapping means editing
// design-system/manifests/*.json -- outside this lane's scope
// (tests/design-system/, playwright.design-system.config.ts, package.json,
// .github/workflows/pr.yml only). Flagged as a follow-up in the report
// instead of guessed at here.
//
// What ships without touching a manifest: every manifest's baseline story
// (the same story its own "browser" check already treats as the component's
// canonical render -- reusing that existing declaration rather than
// re-deciding "which story is representative" a second time) PLUS every
// `variants` entry that has a matching story export. Variants are the
// highest-risk case for the C2 substitution this gate exists for: a swapped
// component can keep every ARIA attribute and every behavioural assertion
// identical while rendering a visually different `secondary`/`outline`/
// `destructive` look. 26 variant images across 12 components come along for
// free from data the manifests already declare.
function pascalCase(value: string): string {
  return value.split(/[-_ ]/).map((word) => word.charAt(0).toUpperCase() + word.slice(1)).join('')
}

// Decision -- "dynamic content": DatePicker and DateTimePicker's "Default"
// story opens (or leaves open) a calendar defaulted to the REAL current
// month because neither passes a fixed `defaultMonth` and their play()
// clicks a day inside that live month view. The story's final rendered pixels
// therefore depend on the wall-clock date the baseline was captured on --
// captured today it shows September 2026, captured next month a different
// grid, and every future run would diff 100% noise, not a regression. Both
// components ship a "ReadOnly" story rendering a FIXED date
// (2026-06-22 / 2026-06-22T14:30) with the picker closed; that is the
// deterministic stand-in captured instead.
//
// Everything else in the catalog was checked (grep for
// `Date.now|new Date(|Math.random` across src/components/ui/*.tsx, excluding
// stories/tests) and found stable enough to capture as-is: Calendar pins
// `defaultMonth` to a fixed PAST month at the story-meta level, so it can
// never again coincide with a real "today" highlight. JobStatus's stall
// indicator needs a full `--motion-loading-escalation` (10s) of real
// wall-clock time after mount to change what it renders. Under a fully
// parallel suite, scheduling delay can push the capture past that threshold,
// so `openStoryForScreenshot` freezes time for JobStatus (see that function).
// It is the only `src/components/ui` component whose render changes on a
// delayed timer; AutoSaveIndicator and date-time-picker read the clock only
// synchronously at render and stay unfrozen. CSS
// animation (Skeleton's pulse, Progress's indeterminate bar) is handled by
// `toHaveScreenshot`'s own default, which disables animations before every
// capture -- nothing here does that work.
const storyOverrides: Record<string, string> = {
  DatePicker: 'ReadOnly',
  DateTimePicker: 'ReadOnly',
}

type Target = { manifest: Manifest; story: string; stateLabel: string }

const targets: Target[] = manifests.flatMap((manifest) => {
  const storyExports = new Set(manifest.stories.flatMap((story) => story.exports))
  const browserCheck = manifest.checks.find((check) => check.kind === 'browser' && check.applicable)
  const declaredBase = browserCheck?.story ?? manifest.stories[0]?.exports[0]
  if (!declaredBase) return []
  const baseStory = storyOverrides[manifest.component] ?? declaredBase
  const result: Target[] = [{ manifest, story: baseStory, stateLabel: 'default' }]
  for (const variant of manifest.variants ?? []) {
    const exportName = pascalCase(variant)
    if (exportName === declaredBase || exportName === baseStory) continue
    if (storyExports.has(exportName)) result.push({ manifest, story: exportName, stateLabel: `variant-${variant}` })
  }
  return result
})

async function findStoryEntry(page: Page, manifest: Manifest, storyName: string) {
  const index = await page.request.get('/index.json').then((response) => response.json() as Promise<StoryIndex>)
  const declared = manifest.stories.find((story) => story.exports.includes(storyName))
  expect(declared, `${manifest.component} must declare story export ${storyName}`).toBeTruthy()
  const entry = Object.values(index.entries).find((candidate) =>
    candidate.type === 'story' && candidate.exportName === storyName &&
    candidate.importPath.replace(/^\.\//, '').endsWith(declared!.file.replace(/^\.\//, '')))
  expect(entry, `${declared!.file}#${storyName} must exist in Storybook index`).toBeTruthy()
  return entry!
}

async function openStoryForScreenshot(page: Page, manifest: Manifest, storyName: string, freezeClockAt?: Date) {
  const entry = await findStoryEntry(page, manifest, storyName)
  if (freezeClockAt) {
    // JobStatus only. `setFixedTime` freezes Date.now() so the component's
    // elapsed-time arithmetic reads zero; it does not stop real timers, so the
    // init script below also neutralises setTimeout calls of 5s or more — the
    // escalation timer — while every shorter timer (React, Storybook, the
    // attach() retry below) keeps the native implementation. A fully paused
    // clock is not used: installed before navigation, it stalls page load.
    await page.clock.setFixedTime(freezeClockAt)
  }
  await page.addInitScript((neutralizeLongTimers: boolean) => {
    if (neutralizeLongTimers) {
      const nativeSetTimeout = window.setTimeout.bind(window)
      const ESCALATION_GUARD_MS = 5_000 // well under --motion-loading-escalation's 10s, well above any legitimate short-delay bootstrap timer
      window.setTimeout = ((handler: TimerHandler, timeout?: number, ...args: unknown[]) => {
        if (typeof timeout === 'number' && timeout >= ESCALATION_GUARD_MS) return 0 as unknown as ReturnType<typeof window.setTimeout>
        return nativeSetTimeout(handler, timeout, ...args)
      }) as typeof window.setTimeout
    }
    const state = window as Window & {
      __designSystemStoryFinished?: StoryFinished
      __STORYBOOK_ADDONS_CHANNEL__?: { on: (event: string, listener: (payload: unknown) => void) => void }
    }
    state.__designSystemStoryFinished = undefined
    const attach = () => {
      if (!state.__STORYBOOK_ADDONS_CHANNEL__) {
        window.setTimeout(attach, 0)
        return
      }
      state.__STORYBOOK_ADDONS_CHANNEL__.on('storyFinished', (payload) => {
        state.__designSystemStoryFinished = payload as StoryFinished
      })
    }
    attach()
  }, Boolean(freezeClockAt))
  await page.goto(`/iframe.html?id=${entry.id}&viewMode=story`)
  await expect(page.locator('[data-design-system-config]')).toBeAttached()
  await page.waitForFunction((storyId) => {
    const state = window as Window & { __designSystemStoryFinished?: StoryFinished }
    return state.__designSystemStoryFinished?.storyId === storyId
  }, entry.id, { timeout: 30_000 })
  const finished = await page.evaluate(() => (window as Window & { __designSystemStoryFinished?: StoryFinished }).__designSystemStoryFinished)
  expect(finished, `${entry.id} play/report lifecycle must finish successfully before its baseline can be trusted`).toMatchObject({ storyId: entry.id, status: 'success' })
  expect(finished?.reporters?.some((report) => report.status === 'failed') ?? false, `${entry.id} must not contain a failed Storybook report`).toBe(false)
  return entry
}

test('manifest coverage exists before the appearance gate can pass', () => {
  expect(manifests.length, 'design-system/manifests contains no component manifests').toBeGreaterThan(0)
  expect(targets.length, 'no screenshot targets were derived from the manifests').toBeGreaterThan(0)
})

for (const target of targets) {
  test(`${target.manifest.component} [appearance:${target.stateLabel}]`, async ({ page }, testInfo) => {
    testInfo.setTimeout(45_000)
    // See the decision comment above `storyOverrides`: JobStatus's stall
    // indicator is wall-clock-driven, so its capture freezes `page.clock`.
    const freezeClockAt = target.manifest.component === 'JobStatus' ? new Date('2026-06-22T12:00:00Z') : undefined
    await openStoryForScreenshot(page, target.manifest, target.story, freezeClockAt)
    // maxDiffPixels/maxDiffPixelRatio and animation handling are set
    // globally in playwright.design-system.config.ts's
    // `expect.toHaveScreenshot` block -- see the decision comment there for
    // the threshold's justification (tested empirically, not assumed).
    await expect(page).toHaveScreenshot(`${target.manifest.component}-${target.stateLabel}.png`)
  })
}
