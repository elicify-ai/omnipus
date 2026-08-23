import { defineConfig } from '@playwright/test';

/**
 * ADR-067 §13.4 — the browser matrix, and why retries are dangerous here.
 *
 * ## The trap this file now carries
 *
 * Before this change there was NO `projects` array, so Playwright ran every spec
 * under one implicit Chromium project. The moment a `projects` array exists,
 * Playwright runs ONLY what some project matches — a spec matched by no project
 * stops running entirely AND THE SUITE STILL REPORTS GREEN. That is precisely the
 * false-green the vitest-group gap produced (11 `src/components/library/` test
 * files running nowhere), arriving inside its own fix.
 *
 * The defence is structural, in three parts:
 *   1. `defaultProject` matches Playwright's own default spec pattern — i.e. EVERY
 *      spec on disk — and subtracts exactly `PROJECT_SCOPED_SPEC_GLOBS`. It is a
 *      subtraction, never an enumeration, so a new spec file is covered the moment
 *      it lands rather than when someone remembers to list it.
 *   2. The isolation/headed spec lists are exported below so the unit test
 *      (`playwrightConfig.test.ts`, spec test 120) can assert that each project's
 *      match resolves to exactly the intended files and that the default project
 *      ignores those and only those.
 *   3. `scripts/e2e-shards.sh check` independently fails CI if any
 *      `tests/e2e/*.spec.ts` is assigned to no shard. Two unrelated mechanisms
 *      have to fail together for a spec to go silently unrun.
 *
 * ## Why the isolation and headed projects pin `retries: 0`
 *
 * The global `retries: process.env.CI ? 3 : 2` was added for real-LLM latency
 * variance under prolonged suite load. That is a sound reason for the tests it was
 * written for, and none of those tests are these.
 *
 * "The cookie was not readable" and "nothing reached the external origin" are not
 * properties a fourth attempt establishes. A security assertion allowed to retry
 * reports identically to one that passed first time, so a policy regression that
 * fails two runs in three would ship as green. Retries stay off here, at PROJECT
 * level, so raising the global number can never reach them.
 */

/**
 * The two isolation spec files (ADR-067 §13.4 item 1). Run on all three engines.
 * Scoping is not cosmetic: an unscoped Firefox project would re-run all ~50
 * existing specs — most of them real-LLM — a second and a third time.
 */
export const ISOLATION_SPEC_FILES = [
  'tests/e2e/preview-isolation.spec.ts',
  'tests/e2e/preview-bundle.spec.ts',
  // ADR-067 §10.4's `.svg` row — spec tests 94 and 122, plus the type-confusion
  // pair (FR-008a, FR-015, FR-016). It belongs on all three engines for the same
  // reason the other two do: "an <img> runs SVG in secure static mode" and "the
  // sandbox seals the origin" are ENGINE behaviours, so a Chromium-only pass
  // says nothing about the other two. It also carries a file-level
  // `test.describe.configure({ retries: 0 })`, so the zero survives even if this
  // list is edited.
  'tests/e2e/preview-svg.spec.ts',
] as const;

/**
 * The headed spec files (ADR-067 §13.4 item 4, narrowed by §0's derivation).
 *
 * Headed is required ONLY where the browser's OWN PDF handling is what is being
 * measured: the top-level `.pdf` type-confusion case (headless Chromium turns
 * every `.pdf` navigation into a download, so "no script ran" would be true for
 * the wrong reason) and the browser-viewer negative control. Everything else —
 * including PDF.js rendering, which draws into a canvas and so renders identically
 * headless — stays headless.
 */
export const HEADED_SPEC_FILES = [
  'tests/e2e/preview-pdf-toplevel.spec.ts',
  'tests/e2e/preview-pdf-viewer-control.spec.ts',
] as const;

/** Every spec claimed by a non-default project. The default project subtracts exactly this set. */
export const PROJECT_SCOPED_SPEC_FILES = [
  ...ISOLATION_SPEC_FILES,
  ...HEADED_SPEC_FILES,
] as const;

/**
 * Glob forms of the above, for `testMatch` / `testIgnore`.
 *
 * Playwright matches these against the file path, so a `**​/` prefix is used rather
 * than a testDir-relative path — the same string then works for both `testMatch`
 * (project-scoped) and `testIgnore` (default project) with no second spelling to
 * keep in sync.
 */
const toGlob = (p: string) => `**/${p.replace(/^tests\/e2e\//, '')}`;
export const ISOLATION_SPEC_GLOBS = ISOLATION_SPEC_FILES.map(toGlob);
export const HEADED_SPEC_GLOBS = HEADED_SPEC_FILES.map(toGlob);
export const PROJECT_SCOPED_SPEC_GLOBS = PROJECT_SCOPED_SPEC_FILES.map(toGlob);

/**
 * The default project's match: every end-to-end spec, and nothing else.
 *
 * Spelled out rather than omitted, for two reasons.
 *
 * FIRST, it makes the default project's coverage an assertable VALUE rather than
 * an implicit fallback — test 120 reads it, and "matches everything" is otherwise
 * not a thing a test can check.
 *
 * SECOND, it narrows Playwright's own default (`**​/*.@(spec|test).?(c|m)[jt]s?(x)`)
 * by dropping `.test.` — which fixes a real, PRE-EXISTING breakage rather than
 * introducing one. `tests/e2e/fixtures/selectors.test.ts` is a VITEST file living
 * under `testDir`, and Playwright's default pattern collects it: on this branch,
 * before any of this change, a bare `npx playwright test --list` dies with
 * `TypeError: Cannot read properties of undefined (reading 'config')` and reports
 * `Total: 0 tests in 0 files`. It has gone unnoticed because both CI surfaces
 * always pass explicit spec paths (`npx playwright test ${{ matrix.specs }}` /
 * `runci.sh`'s per-shard invocation), which filters that file out before collection.
 *
 * `.spec.ts` is also exactly the set `scripts/e2e-shards.sh` enumerates, so the
 * shard plan and this config now govern the same population of files — which is
 * what lets the two act as independent checks on each other.
 */
export const DEFAULT_PROJECT_TEST_MATCH = '**/*.spec.ts';

/**
 * Chromium flags shared by the existing suite. Kept on the default project only.
 *
 * They exist for `browser-live-video.spec.ts` (autoplay policy + three
 * anti-throttling flags, mirroring the agent's own managed Chrome). They are
 * Chromium-specific — passing them to Firefox or WebKit would be handing an
 * unknown argument to a different binary — and they are deliberately NOT applied
 * to the isolation projects even for Chromium, so the three engines are launched
 * as identically as the matrix can make them. Nothing in the isolation specs
 * touches autoplay or renderer backgrounding.
 */
const CHROMIUM_SUITE_ARGS = [
  // browser-live-video.spec.ts needs the VIEWER's (this Playwright-
  // driven Chromium's) own WebAudio AnalyserNode to run immediately —
  // no prior user gesture required — so it can sample the live
  // <video> sink's audio track without any autoplay-policy ambiguity.
  // Harmless for every other spec: it only relaxes a restriction,
  // never tightens one, and no other spec asserts on gesture-gated
  // audio/autoplay behavior either way.
  '--autoplay-policy=no-user-gesture-required',
  // Keep THIS (the viewer) Chromium's renderer awake. browser-live-video
  // .spec.ts samples the live <video> sink by drawing it to a canvas at
  // two moments and asserting the pixels CHANGED. That reads whatever
  // frame the renderer last painted — so if Chromium backgrounds or
  // treats this renderer as occluded, the painted frame stops advancing
  // and two samples 1.5s apart come back BYTE-IDENTICAL, failing as
  // "the video is frozen" even though the WebRTC stream is perfectly
  // healthy and still delivering frames. Observed intermittently on the
  // 2026-07-28 real-Chrome runs (pass, then flaky, then flaky, with
  // identical pixel data to 15 decimal places each time it failed).
  //
  // These are the same three flags the AGENT's managed Chrome already
  // launches with (pkg/tools/browser/exec_resolver.go) and for the same
  // reason; they were simply never applied to Playwright's own browser,
  // which is a separate process we launch separately. They only relax
  // throttling, so they cannot mask a real product stall: a genuinely
  // frozen stream still produces identical frames and still fails.
  '--disable-renderer-backgrounding',
  '--disable-backgrounding-occluded-windows',
  '--disable-background-timer-throttling',
];

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
  //
  // This value reaches the DEFAULT project only. Every project below that
  // asserts a security property overrides it to 0 — see the header comment.
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
  },
  projects: [
    /**
     * The existing behaviour, unchanged: Chromium, the suite's launch flags, and
     * the global retry count. It matches EVERY spec on disk and subtracts exactly
     * the project-scoped files — never an allow-list, so a spec added tomorrow
     * runs tomorrow.
     */
    {
      name: 'default',
      testMatch: DEFAULT_PROJECT_TEST_MATCH,
      testIgnore: [...PROJECT_SCOPED_SPEC_GLOBS],
      use: {
        browserName: 'chromium',
        launchOptions: { args: CHROMIUM_SUITE_ARGS },
      },
    },

    // ── Isolation matrix: the same two specs, three engines, no retries ──────
    // The measured policy (spec §10.3) was verified on Chromium 151, Firefox 153
    // and WebKit 26.5; 24 of 25 compared rows were identical across engines. The
    // matrix exists to keep that true, so all three run the same files.
    {
      name: 'isolation-chromium',
      testMatch: [...ISOLATION_SPEC_GLOBS],
      retries: 0,
      use: { browserName: 'chromium', launchOptions: { args: [] } },
    },
    {
      name: 'isolation-firefox',
      testMatch: [...ISOLATION_SPEC_GLOBS],
      retries: 0,
      use: { browserName: 'firefox', launchOptions: { args: [] } },
    },
    {
      name: 'isolation-webkit',
      testMatch: [...ISOLATION_SPEC_GLOBS],
      retries: 0,
      use: { browserName: 'webkit', launchOptions: { args: [] } },
    },

    /**
     * Headed Chromium, for the two cases §0's derivation earns — see
     * HEADED_SPEC_FILES. `retries: 0` for the same reason as the isolation
     * projects: the top-level `.pdf` case is a type-confusion security
     * assertion, and "the script did not run" is not a property a retry
     * establishes. Chromium only: what is being measured is a browser's own PDF
     * viewer, and the engine whose headless behaviour created the original false
     * negative is the one worth measuring headed.
     */
    {
      name: 'preview-headed',
      testMatch: [...HEADED_SPEC_GLOBS],
      retries: 0,
      use: {
        browserName: 'chromium',
        headless: false,
        launchOptions: { args: [] },
      },
    },
  ],
});
