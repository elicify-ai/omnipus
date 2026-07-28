import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests/e2e',
  globalSetup: './tests/e2e/global-setup.ts',
  // Global teardown validates that no unauthorized skips occurred.
  // If any test called softSkip() without a valid allow-list entry, the teardown
  // reads soft-skips.json and fails the run. This prevents silent skip accumulation.
  globalTeardown: './tests/e2e/global-teardown.ts',
  timeout: 90_000,
  expect: { timeout: 15_000 },
  // retries: 3 in CI / 2 locally for real-LLM flakes under suite load. The
  // 9 remaining Group-A failures (subagent×5, handoff b, media a, command-
  // center b) all share the same symptom: under prolonged suite load
  // (~12 min total wall-clock) the LLM occasionally takes >40s to emit the
  // expected tool call, even though every one of these tests passes alone
  // in 5-25s. Retries are NOT a cover for real bugs — orphan watchdog +
  // browser port + isReplaying race were all root-caused and fixed
  // separately. The per-test toBeVisible timeouts on these assertions
  // were also bumped to 60s. Retries cover the residual real-LLM variance.
  retries: process.env.CI ? 3 : 2,
  // Single worker: shared gateway config/credentials cannot tolerate concurrent writes.
  // See CLAUDE.md concurrency model (single-writer goroutine + advisory flock).
  workers: 1,
  fullyParallel: false,
  reporter: [['html'], ['list']],
  use: {
    baseURL: process.env.OMNIPUS_URL || 'http://localhost:6060',
    storageState: process.env.OMNIPUS_AUTH_FILE || './tests/e2e/fixtures/.auth/admin.json',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    launchOptions: {
      args: [
        // browser-live-video.spec.ts needs the VIEWER's (this Playwright-
        // driven Chromium's) own WebAudio AnalyserNode to run immediately —
        // no prior user gesture required — so it can sample the live
        // <video> sink's audio track without any autoplay-policy ambiguity.
        // Harmless for every other spec: it only relaxes a restriction,
        // never tightens one, and no other spec asserts on gesture-gated
        // audio/autoplay behavior either way.
        '--autoplay-policy=no-user-gesture-required',
      ],
    },
  },
});
