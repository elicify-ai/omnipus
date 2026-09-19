import { defineConfig } from '@playwright/test'

const baseURL = process.env.STORYBOOK_URL ?? 'http://127.0.0.1:6006'

export default defineConfig({
  testDir: './tests/design-system',
  testMatch: 'browser.spec.ts',
  outputDir: 'test-results/design-system-browser-artifacts',
  timeout: 30_000,
  expect: { timeout: 5_000 },
  retries: 0,
  fullyParallel: true,
  reporter: process.env.CI
    ? [['line'], ['json', { outputFile: 'test-results/design-system-browser.json' }]]
    : [['list'], ['json', { outputFile: 'test-results/design-system-browser.json' }]],
  use: { baseURL, trace: 'retain-on-failure', screenshot: 'off', video: 'off' },
  webServer: process.env.STORYBOOK_URL ? undefined : {
    command: 'npm run storybook -- --ci --no-open',
    url: baseURL,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
    { name: 'firefox', use: { browserName: 'firefox' } },
    { name: 'webkit', use: { browserName: 'webkit' } },
    { name: 'chromium-coarse-pointer', use: { browserName: 'chromium', hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } } },
  ],
})
