import { defineConfig } from '@playwright/test';

// The fixture is served by page.route; no gateway, credentials or remote origin.
export default defineConfig({
  testDir: '.',
  testMatch: 'input-connection-fixture.spec.ts',
  forbidOnly: !!process.env.CI,
  workers: 1,
  retries: 0,
  timeout: 30000,
  reporter: [['list']],
  use: {
    browserName: 'chromium',
    viewport: { width: 1440, height: 1000 },
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    launchOptions: { args: ['--autoplay-policy=no-user-gesture-required', '--disable-renderer-backgrounding', '--disable-backgrounding-occluded-windows', '--disable-background-timer-throttling'] },
  },
});
