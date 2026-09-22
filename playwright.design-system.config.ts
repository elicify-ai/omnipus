import { defineConfig } from '@playwright/test'

// STORYBOOK_STATIC_DIR (e.g. `dist/storybook`) serves that directory with
// Python's stdlib http.server instead of starting the Storybook dev server --
// the appearance gate (screenshot.spec.ts) must never run against the dev
// server (see that file's header comment for the 16-of-18-vs-18-of-18
// evidence). Unset by default, so test:design-system:browser's existing
// dev-server-by-default behavior is unchanged; test:design-system:screenshot
// sets it in package.json/.github/workflows/pr.yml.
const staticDir = process.env.STORYBOOK_STATIC_DIR
const defaultPort = staticDir ? 6007 : 6006
const baseURL = process.env.STORYBOOK_URL ?? `http://127.0.0.1:${defaultPort}`

// Appearance-gate decisions (issue #753 section 2; screenshot generation
// lives in tests/design-system/screenshot.spec.ts, which explains WHICH
// stories are captured -- these two decisions are about HOW they're diffed).
//
// Projects: chromium and chromium-coarse-pointer only. Firefox and WebKit
// are excluded from screenshot.spec.ts specifically (not from
// browser.spec.ts, which still runs its structural/behavioural checks on all
// four) via each project's testIgnore below. Reason: font rasterisation
// differs enough between engines to produce a permanent false diff on every
// text-bearing component, on every run, regardless of whether anything
// changed -- that is noise the gate would need to suppress rather than a
// regression it could catch, and chromium-coarse-pointer already gives touch
// target sizing a second, materially different viewport to verify against.
//
// Threshold: maxDiffPixels 50, NOT a ratio. This was tested, not assumed --
// the first attempt used maxDiffPixelRatio: 0.02 and it did NOT fail on a
// real, deliberate regression: screenshot.spec.ts's capture is a FULL-PAGE
// screenshot (needed because several components portal their open content --
// Dialog, Popover, DropdownMenu, Sheet, Command, SmartSelect -- to
// document.body, outside the local story root, so an element-scoped
// screenshot would miss exactly the content most likely to regress). Most
// catalogued components render small against that full page, so a real,
// visible local change gets diluted into a tiny fraction of total page
// pixels: shifting Button's horizontal padding by 6px on each side (its
// rendered width moving from 85.75px to 97.75px, confirmed via
// getComputedStyle) changed 592 pixels out of 921,600 on a 1280x720 capture
// -- a ratio of 0.0006, comfortably UNDER 0.02, so the gate would have passed
// straight through a padding regression. Measuring the actual noise floor
// instead of guessing at it: two identical, unmodified captures of the same
// story differ by 0 pixels (Chromium's render of this static build is
// bit-for-bit deterministic run-to-run on one machine). maxDiffPixels: 50
// sits with wide margin above that zero floor -- room for legitimate minor
// AA differences between Chromium builds -- and more than 10x below the
// 592-pixel regression it needs to catch. Animations are disabled by
// toHaveScreenshot's own default before every capture, which also freezes
// Skeleton's pulse and Progress's indeterminate bar -- see
// screenshot.spec.ts's decision comment for the remaining dynamic-content
// cases (DatePicker/DateTimePicker story overrides, JobStatus's timing
// margin) that needed a different kind of handling.
//
// PLATFORM CAVEAT (read before trusting any committed baseline): Playwright
// names screenshot baselines with the capturing OS baked in
// (`*-chromium-darwin.png` locally on this Mac; CI's `ubuntu-latest` runner
// needs `*-chromium-linux.png`). A baseline captured on macOS is invisible to
// the Linux CI job -- not a silent pass, Playwright's CI mode refuses to
// auto-write a missing snapshot and fails loudly instead, which is the
// correct failure mode but still means a macOS-captured baseline is not a
// usable deliverable. The real baseline must be captured by running this
// suite with --update-snapshots inside the `design-system` CI job (or an
// equivalent ubuntu-latest/Playwright-image environment) against the static
// `dist/storybook` build, then committing the resulting `-linux` PNGs.
const screenshotExpect = { toHaveScreenshot: { maxDiffPixels: 50, maxDiffPixelRatio: 0.01 } }

export default defineConfig({
  testDir: './tests/design-system',
  testMatch: ['browser.spec.ts', 'screenshot.spec.ts'],
  outputDir: 'test-results/design-system-browser-artifacts',
  timeout: 30_000,
  expect: { timeout: 5_000, ...screenshotExpect },
  retries: 0,
  fullyParallel: true,
  reporter: process.env.CI
    ? [['line'], ['json', { outputFile: 'test-results/design-system-browser.json' }]]
    : [['list'], ['json', { outputFile: 'test-results/design-system-browser.json' }]],
  use: { baseURL, trace: 'retain-on-failure', screenshot: 'off', video: 'off' },
  webServer: process.env.STORYBOOK_URL ? undefined : {
    command: staticDir
      ? `python3 -m http.server ${defaultPort} --directory ${staticDir}`
      : 'npm run storybook -- --ci --no-open',
    url: baseURL,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
    { name: 'firefox', use: { browserName: 'firefox' }, testIgnore: 'screenshot.spec.ts' },
    { name: 'webkit', use: { browserName: 'webkit' }, testIgnore: 'screenshot.spec.ts' },
    { name: 'chromium-coarse-pointer', use: { browserName: 'chromium', hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } } },
  ],
})
