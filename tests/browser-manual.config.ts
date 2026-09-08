import { defineConfig } from '@playwright/test';
import * as fs from 'node:fs';
import * as path from 'node:path';

// Operator-owned isolated runtime only. No CI/global setup, server launch, or
// machine-specific fallback: missing prerequisites must fail before collection.
function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required for manual browser acceptance/diagnostics`);
  return value;
}

function absolutePath(name: string): string {
  const value = required(name);
  if (!path.isAbsolute(value)) throw new Error(`${name} must be an absolute path`);
  return value;
}

const baseURL = required('OMNIPUS_URL');
const origin = new URL(baseURL);
if (origin.protocol !== 'http:' || !['localhost', '127.0.0.1'].includes(origin.hostname) || origin.port !== '11094' || origin.username || origin.password || origin.pathname !== '/' || origin.search || origin.hash) {
  throw new Error('OMNIPUS_URL must be the isolated http://localhost:11094 or http://127.0.0.1:11094 origin');
}
const runtimeHome = fs.realpathSync(absolutePath('SOAK_RUNTIME_HOME'));
if (!fs.statSync(runtimeHome).isDirectory()) throw new Error('SOAK_RUNTIME_HOME must be an existing directory');
const storageState = fs.realpathSync(absolutePath('OMNIPUS_AUTH_FILE'));
if (!fs.statSync(storageState).isFile()) throw new Error('OMNIPUS_AUTH_FILE must be an existing authentication state file');
const outputDir = absolutePath('BROWSER_PROBE_OUTPUT_DIR');

export default defineConfig({
  testDir: '.',
  testMatch: ['browser-acceptance/*.spec.ts', 'browser-diagnostics/*.spec.ts'],
  workers: 1,
  fullyParallel: false,
  retries: 0,
  timeout: 90_000,
  expect: { timeout: 15_000 },
  outputDir,
  reporter: [['line']],
  use: {
    baseURL,
    storageState,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    launchOptions: {
      args: [
        '--autoplay-policy=no-user-gesture-required',
        '--disable-renderer-backgrounding',
        '--disable-backgrounding-occluded-windows',
        '--disable-background-timer-throttling',
      ],
    },
  },
});
