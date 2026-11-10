import { defineConfig } from '@playwright/test'

// Narrow offline local runner, following t8-private.config.ts. The one spec
// owns a disposable Go gateway and all API/WS fixture edges. Shared global
// setup would unnecessarily require/probe live-model credentials. CI still
// runs the same spec via the regular default project and its shard assignment.
export default defineConfig({
  testDir: '.',
  testMatch: 'providers-toast.spec.ts',
  workers: 1,
  fullyParallel: false,
  retries: 0,
  timeout: 90_000,
  expect: { timeout: 15_000 },
  reporter: [['list']],
  use: {
    browserName: 'chromium',
    storageState: { cookies: [], origins: [] },
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    video: 'retain-on-failure',
  },
})
