// Local runner for the S1 browser specs (programs.spec.ts, S-01/S-12/S-33/S-53/S-81;
// connectors-programs.spec.ts, S-148). Each spec boots and tears down its own
// isolated gateway, so this config never starts or reseeds the generic shared
// gateway. CI runs the same files in shard ui-3 under the shared config.
// The shared projects carry their own testMatch, so pass the spec file path.
import { defineConfig } from '@playwright/test';
import shared from '../../playwright.config';

export default defineConfig({
  ...shared,
  testDir: '.',
  testMatch: ['programs.spec.ts', 'connectors-programs.spec.ts'],
  globalSetup: undefined,
  globalTeardown: undefined,
  webServer: undefined,
  workers: 1,
  retries: 0,
  use: { ...shared.use, storageState: { cookies: [], origins: [] } },
});
