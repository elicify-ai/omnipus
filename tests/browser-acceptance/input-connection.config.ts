import { defineConfig } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

function file(name: string) {
  const value = process.env[name];
  if (!value || !path.isAbsolute(value) || !fs.statSync(value).isFile()) throw new Error(`${name} must be an existing absolute file`);
  return value;
}
if (process.platform !== 'darwin') throw new Error('Run the remote comparison from the Mac viewer, not the Amsterdam server');
if (process.env.OMNIPUS_URL !== 'https://uat-omnipus.fly.dev') throw new Error('Only the approved Amsterdam UAT origin is allowed');
const outputDir = process.env.BROWSER_PROBE_OUTPUT_DIR;
if (!outputDir || !path.isAbsolute(outputDir)) throw new Error('An absolute BROWSER_PROBE_OUTPUT_DIR is required');
file('BROWSER_INPUT_FIXTURE_CONFIG');
file('BROWSER_INPUT_PROVENANCE');
export default defineConfig({
  testDir: '.', testMatch: 'input-connection.spec.ts', workers: 1, retries: 0,
  timeout: 180000, expect: { timeout: 15000 }, outputDir,
  reporter: [['list']],
  use: {
    baseURL: 'https://uat-omnipus.fly.dev', storageState: file('OMNIPUS_AUTH_FILE'),
    viewport: { width: 1440, height: 1000 }, screenshot: 'only-on-failure', trace: 'retain-on-failure',
    actionTimeout: 30000, navigationTimeout: 30000,
    launchOptions: { args: ['--autoplay-policy=no-user-gesture-required', '--disable-renderer-backgrounding', '--disable-backgrounding-occluded-windows', '--disable-background-timer-throttling'] },
  },
});
