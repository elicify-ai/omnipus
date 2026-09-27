import { defineConfig } from '@playwright/test'

// playwright.side-panel-demo.config.ts — the wave-0 demo's 16-row click-test
// runner (spec §9 wave-0 list). Runs the SAME way the design-system gate does:
// against the STATIC Storybook build (dist/storybook) served by Python's
// stdlib http.server — never the dev server (design-system skill rule 11).
// Chromium only: the rows assert shell GEOMETRY and interaction semantics
// (resize, takeover, guard, history, swipe), not cross-engine rasterisation;
// the design-system gate already covers the four-engine matrix.
//
// Executed with:
//   npm run build:storybook
//   STORYBOOK_STATIC_DIR=dist/storybook npx playwright test side-panel-shell-demo.spec.ts --config=playwright.side-panel-demo.config.ts

const staticDir = process.env.STORYBOOK_STATIC_DIR
if (process.env.CI && !staticDir) {
  throw new Error(
    'playwright.side-panel-demo.config.ts: CI must run the demo rows against the static '
    + 'Storybook build. Run `npm run build:storybook` and set STORYBOOK_STATIC_DIR=dist/storybook.',
  )
}
const port = 6008
const baseURL = `http://127.0.0.1:${port}`

export default defineConfig({
  testDir: './tests/design-system',
  testMatch: ['side-panel-shell-demo.spec.ts'],
  outputDir: 'test-results/side-panel-demo-artifacts',
  timeout: 30_000,
  expect: { timeout: 5_000 },
  retries: 0,
  fullyParallel: true,
  reporter: [['list']],
  use: { baseURL, trace: 'retain-on-failure', screenshot: 'off', video: 'off' },
  webServer: {
    command: `python3 -m http.server ${port} --directory ${staticDir ?? 'dist/storybook'}`,
    url: baseURL,
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
  projects: [
    {
      name: 'chromium',
      use: { browserName: 'chromium' },
    },
  ],
})
