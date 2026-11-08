import { defineConfig } from '@playwright/test';

// Narrow local runner for the self-managed T8 approval spec only. That spec
// owns its real gateway, onboarding, account and fake HTTP model edge, so the
// shared suite's live-provider/global-gateway setup is neither used nor needed.
// The regular CI config and shard assignment are unchanged.
export default defineConfig({
  testDir: '.',
  testMatch: 'agent-parity-approval.spec.ts',
  workers: 1,
  fullyParallel: false,
  retries: 0,
  timeout: 120_000,
  expect: { timeout: 15_000 },
  outputDir: process.env.T8_EVIDENCE_DIR ?? 'test-results/t8-approval',
  reporter: [['list']],
  use: {
    browserName: 'chromium',
    storageState: { cookies: [], origins: [] },
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
});
