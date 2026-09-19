import { defineConfig, devices } from '@playwright/test';
import { AUTH_STATE_PATH } from './tests/touch-in-context/lib/paths';

/**
 * The D17 in-context touch check — design-system-definition.md D17
 * "Enforcement": runs the real application routes from the checked-in
 * inventory (design-system/surfaces.json) as a touch phone and a touch
 * tablet, in both Chromium and WebKit, plus a desktop pointer baseline.
 *
 * This is NOT a screenshot-comparison suite — it measures the live DOM
 * (clipping, overlap, key-row height) and writes a JSON report; see
 * tests/touch-in-context/README.md for what each assertion checks and how
 * to read the report.
 *
 * ## Running it
 *
 * This suite drives an already-running gateway — it never starts or builds
 * one itself (no `webServer` block below), matching
 * playwright.viewkinds.config.ts's approach and this program's shared
 * house rule ("never touch another lane's gateway"). Point it at yours:
 *
 *   OMNIPUS_URL=http://127.0.0.1:<port> \
 *   TOUCH_CHECK_USER=admin \
 *   TOUCH_CHECK_PASS=<that home's admin password> \
 *     npx playwright test --config=playwright.touch-in-context.config.ts
 *
 * ## Project names are load-bearing
 *
 * tests/touch-in-context/lib/env.ts derives each check's EnvKind (which
 * key-row budget column applies) from the project name prefix
 * ("touch-phone-" / "touch-tablet-" / "desktop-pointer-"). Renaming a
 * project here without updating env.ts's mapping breaks that derivation
 * loudly (it throws), not silently.
 *
 * ## `workers: 1` is load-bearing, not a perf default
 *
 * `HandleLogin` (pkg/gateway/rest_auth.go) keeps the session cookie
 * SINGLE-SLOT per user: every login for "admin" — anywhere — invalidates
 * whatever session cookie existed before it. This suite's "sign in" task
 * flow performs a REAL login (D17 requires it), so if it ran concurrently
 * with another authenticated test, it would invalidate that test's session
 * mid-run and silently redirect it to the login page (observed in this
 * suite's first run — every route-sweep/model-picker/compose check after the
 * first concurrent "sign in" completed showed the login form's 3 controls
 * instead of the real page). Each authenticated test re-authenticates for
 * itself immediately before it runs (tests/touch-in-context/lib/reauth.ts);
 * `workers: 1` is what guarantees no second login can land while that
 * session is still in use. Do not raise this without also removing the
 * single-session constraint (or seeding one dedicated account per project).
 */
export default defineConfig({
  testDir: './tests/touch-in-context/specs',
  globalSetup: './tests/touch-in-context/lib/global-setup.ts',
  globalTeardown: './tests/touch-in-context/lib/global-teardown.ts',
  outputDir: 'test-results/touch-in-context-artifacts',
  // The "authenticated route sweep" test iterates ~27 routes in one test,
  // and each route's settle() (tests/touch-in-context/specs/in-context-touch.spec.ts)
  // rides out up to 5s when a route is still mounting content — observed
  // sweep durations run 26-54s on this shared, contended machine, occasionally
  // exceeding a 60s test timeout on the slowest engine (WebKit) under load.
  // 180s gives that real headroom without masking a genuine hang (a route
  // that never settles still only costs its own 5s, not the whole budget).
  timeout: 180_000,
  expect: { timeout: 10_000 },
  // No real LLM latency is in play here — a control either clips/overlaps/
  // busts its budget or it does not. Retries would just let a real,
  // intermittent overlap or clip pass as "flaky" instead of failing.
  retries: 0,
  workers: 1,
  fullyParallel: false,
  reporter: process.env.CI
    ? [['line'], ['json', { outputFile: 'test-results/touch-in-context.json' }]]
    : [['list'], ['json', { outputFile: 'test-results/touch-in-context.json' }]],
  use: {
    baseURL: process.env.OMNIPUS_URL ?? 'http://127.0.0.1:6060',
    storageState: AUTH_STATE_PATH,
    trace: 'retain-on-failure',
    // Screenshots are taken explicitly, only on a violation (see
    // lib/measure.ts callers) — never Playwright's own pass/fail screenshot,
    // which would also fire on an unrelated assertion failure.
    screenshot: 'off',
    video: 'off',
  },
  projects: [
    {
      name: 'desktop-pointer-chromium',
      use: { ...devices['Desktop Chrome'], browserName: 'chromium', viewport: { width: 1280, height: 800 } },
    },
    { name: 'touch-phone-chromium', use: { ...devices['iPhone 13'], browserName: 'chromium' } },
    { name: 'touch-phone-webkit', use: { ...devices['iPhone 13'], browserName: 'webkit' } },
    {
      name: 'touch-tablet-chromium',
      use: { ...devices['iPad Pro 11 landscape'], browserName: 'chromium' },
    },
    {
      name: 'touch-tablet-webkit',
      use: { ...devices['iPad Pro 11 landscape'], browserName: 'webkit' },
    },
  ],
});
