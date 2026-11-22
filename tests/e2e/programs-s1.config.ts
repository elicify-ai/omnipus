// Private Programs fixture owns boot/login/teardown. Never start or reseed the
// generic shared gateway when collecting or running this RED file by itself.
import { defineConfig } from '@playwright/test';
import shared from '../../playwright.config';

export default defineConfig({
  ...shared,
  testDir: '.',
  testMatch: 'programs.spec.ts',
  globalSetup: undefined,
  globalTeardown: undefined,
  webServer: undefined,
  workers: 1,
  retries: 0,
  use: { ...shared.use, storageState: { cookies: [], origins: [] } },
});
