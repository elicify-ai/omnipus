import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(__dirname, '../../..');

/** Per-result JSON files land here (one file per check — see report.ts). */
export const RAW_RESULTS_DIR = path.join(REPO_ROOT, 'test-results', 'touch-in-context', 'raw');

/** Violation screenshots — only ever written when a check found a violation. */
export const SCREENSHOTS_DIR = path.join(REPO_ROOT, 'test-results', 'touch-in-context', 'screenshots');

/** The final aggregated JSON report, written by global-teardown.ts after the whole run. */
export const REPORT_PATH =
  process.env.TOUCH_CHECK_REPORT_PATH ?? path.join(REPO_ROOT, 'test-results', 'touch-in-context', 'report.json');

/**
 * Shared authenticated storageState, written once by global-setup.ts via a
 * real POST /api/v1/auth/login (not a UI-driven login — see that file's doc
 * comment for why an API login is still "real" auth). Cookies are not
 * device-specific, so every project references this same file; specs that
 * need to test the UNauthenticated state (the login route itself, and the
 * explicit "sign in" task flow) override it with an empty storageState —
 * see tests/touch-in-context/specs for both call sites.
 */
export const AUTH_STATE_PATH = path.join(REPO_ROOT, 'test-results', 'touch-in-context', 'auth-state.json');
