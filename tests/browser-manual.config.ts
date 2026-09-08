import { defineConfig } from '@playwright/test';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { browserRuntimeTarget } from './browser-runtime-target';

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

const baseURL = browserRuntimeTarget(process.env.OMNIPUS_URL).origin;
const runtimeHome = fs.realpathSync(absolutePath('SOAK_RUNTIME_HOME'));
if (!fs.statSync(runtimeHome).isDirectory()) throw new Error('SOAK_RUNTIME_HOME must be an existing directory');
const storageState = fs.realpathSync(absolutePath('OMNIPUS_AUTH_FILE'));
if (!fs.statSync(storageState).isFile()) throw new Error('OMNIPUS_AUTH_FILE must be an existing authentication state file');
const outputDir = absolutePath('BROWSER_PROBE_OUTPUT_DIR');
const executablePath = process.env.BROWSER_PROBE_EXECUTABLE
  ? fs.realpathSync(absolutePath('BROWSER_PROBE_EXECUTABLE')) : undefined;

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
    screenshot: 'only-on-failure',
    viewport: { width: 1280, height: 900 },
    launchOptions: {
      executablePath,
      args: [
        '--autoplay-policy=no-user-gesture-required',
        '--disable-renderer-backgrounding',
        '--disable-backgrounding-occluded-windows',
        '--disable-background-timer-throttling',
      ],
    },
  },
});
