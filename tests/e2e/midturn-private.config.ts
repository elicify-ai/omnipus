import { defineConfig } from '@playwright/test';

/**
 * Narrow local runner for the #1661 deterministic mid-turn tests (the nine
 * rewritten in cancel-cross-channel / catchup-scenarios / reconnect-mid-turn).
 *
 * Every one of those tests owns its isolated GatewayProcess pointed at the
 * held-stream provider fixture, so the shared suite's live-provider keys,
 * global gateway boot and auth storage state are neither used nor needed —
 * the same shape as t8-private.config.ts. Select the tests with --grep on
 * their titles, e.g.:
 *
 *   OMNIPUS_BINARY=/tmp/omnipus-ci npx playwright test \
 *     --config=tests/e2e/midturn-private.config.ts \
 *     -g "T21|T22|T23|T26|b: dead|c: tab|h: gateway|S-10 offline"
 *
 * The regular CI config and shard assignment are unchanged. Nothing here can
 * or may reach a live provider: these tests are model-independent by design.
 */
export default defineConfig({
  testDir: '.',
  testMatch: /(cancel-cross-channel|catchup-scenarios|reconnect-mid-turn)\.spec\.ts/,
  workers: 1,
  fullyParallel: false,
  retries: 0,
  timeout: 420_000,
  expect: { timeout: 15_000 },
  outputDir: process.env.MIDTURN_EVIDENCE_DIR ?? 'test-results/midturn',
  reporter: [['list']],
  use: {
    browserName: 'chromium',
    storageState: { cookies: [], origins: [] },
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
});
