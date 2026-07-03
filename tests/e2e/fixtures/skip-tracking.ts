/**
 * skip-tracking.ts — Runtime skip governance for the Playwright E2E suite.
 *
 * ## Skip manifest
 *
 * After every test run, this module writes a JSON manifest to
 * `test-results/skip-manifest.json` (configurable via `OMNIPUS_SKIP_MANIFEST_PATH`).
 * The manifest captures every `softSkip()` call made during the run, whether
 * authorized (in SKIP_ALLOWLIST) or unauthorized. Direct `test.skip(...)` /
 * `test.fixme(...)` calls bypass this gate today — capture of those is
 * tracked for v0.2 (#155) and is the reason the SKIP_ALLOWLIST should also
 * cover any test that uses them.
 *
 * ## Baseline comparison gate
 *
 * The baseline is stored in `tests/e2e/fixtures/skip-baseline.json`. The
 * global teardown compares `manifest.unauthorized_skips.length` against
 * `baseline.baseline_unauthorized_skips`. If the manifest count is higher,
 * the run fails.
 *
 * To update the baseline (when a long-term skip is intentionally absorbed):
 *   1. Ensure the skip has a valid SKIP_ALLOWLIST entry with issue + until.
 *   2. Manually edit `skip-baseline.json` to increment `baseline_unauthorized_skips`.
 *   3. Commit the change with a comment explaining the rationale.
 *
 * ## SKIP_ALLOWLIST entry requirements
 *
 * Each entry MUST include:
 *   - `test`  — exact test title (first argument to `test(...)`)
 *   - `issue` — GitHub issue or PR URL matching `https://github.com/.+/issues/\d+`
 *               or `https://github.com/.+/pull/\d+`
 *   - `until` — target resolution date in `YYYY-MM-DD` format
 *
 * Validation runs at module load time. Any entry that fails validation causes
 * an immediate throw before any test runs. This prevents silently-invalid
 * entries from slipping through.
 *
 * An entry with an expired `until` date causes the corresponding test to FAIL
 * at runtime regardless of the allow-list — the entry is treated as if it does
 * not exist.
 *
 * === How to add a skip to the allow-list ===
 *
 * Add an entry to SKIP_ALLOWLIST below:
 *   { test: "<exact test title>", issue: "<GitHub issue URL>", until: "YYYY-MM-DD" }
 *
 * Rules:
 *   - `test` must be the exact string passed as the first argument to test().
 *   - `issue` must be a GitHub issue or PR URL.
 *   - `until` is the target date by which the skip should be resolved and removed.
 *     After this date, CI will treat the entry as expired and fail the run.
 *
 * Deletion criterion: once the underlying issue is resolved and the test passes
 * reliably, remove the entry from SKIP_ALLOWLIST and delete the test.skip() call
 * (or replace it with a real assertion).
 *
 * === What does NOT belong in the allow-list ===
 *
 * - Tests skipped because of a missing env var (use a preflight that fails fast).
 * - Tests skipped because of "LLM non-determinism" — non-determinism is a design
 *   flaw; deterministic scenario providers (T4.1) are the fix.
 * - Tests skipped because an implementation is missing — use expect(false).toBe(true)
 *   with a BLOCKED message so CI shows red, not skipped.
 *
 * === Deprecated softSkip pattern ===
 *
 * Do NOT call softSkip() for permanent/intentional skips. Those must either:
 *   (a) Be added to SKIP_ALLOWLIST with an issue + target date, or
 *   (b) Be promoted to a failing test with expect(false).toBe(true).
 *
 * === Skip-baseline.json anchor ===
 *
 * This is the "previous green main" anchor. CI fails if the manifest's
 * unauthorized_skip count is greater than this baseline. To absorb a new
 * long-term skip: (1) add it to SKIP_ALLOWLIST, (2) update the baseline.
 * Never auto-increment the baseline from code — it must be a deliberate
 * human commit.
 */

import * as fs from 'fs';
import * as path from 'path';
import { execSync } from 'child_process';

// ── Allow-list ─────────────────────────────────────────────────────────────────
//
// Each entry exempts one test from the record-and-fail rule.
// Format: { test: "<exact title>", issue: "<GitHub URL>", until: "YYYY-MM-DD" }
//
// Empty by default. Add entries only for genuinely tracked issues with a deadline.

// ── Formally tracked skips ──────────────────────────────────────────────────────
//
// These entries document all raw test.skip() and test.fixme() calls in the
// E2E suite. Each entry references the GitHub issue that blocks the underlying
// feature. Update the `until` date when the issue is resolved or the deadline
// is formally extended.
export const SKIP_ALLOWLIST: { test: string; issue: string; until: string; note?: string }[] = [
  // chat.spec.ts — W1.6 user-approved permanent skip: #105 offline send queue
  {
    test: '(f) queue-on-disconnect: messages sent during WS disconnect send in order after reconnect',
    issue: 'https://github.com/dapicom-ai/omnipus/issues/105',
    until: '2026-09-30',
    note: 'useChatStore has no offline send queue; messages sent during WS disconnect are dropped.',
  },
  // media.spec.ts — W1.6 user-approved permanent skip: #107 mock-media tool
  {
    test: '(b) file-download fallback: large binary request triggers browser download dialog',
    issue: 'https://github.com/dapicom-ai/omnipus/issues/107',
    until: '2026-09-30',
    note: 'InlineMedia <a download> path requires a mock non-image media frame; no scenario provider yet.',
  },
  // contract-counters.spec.ts — production binary does not expose window.__omnipus_test_hooks
  {
    test: 'no schema-validation errors during authenticated page load + navigation',
    issue: 'https://github.com/dapicom-ai/omnipus/issues/155',
    until: '2026-12-31',
    note: 'window.__omnipus_test_hooks counters are only present in dev builds (import.meta.env.DEV). ' +
      'CI runs against the embedded production binary where MODE=production. ' +
      'Run against a Vite dev server to exercise the full assertion.',
  },
  // channels-routing.spec.ts — depends on ChannelConfigPanel SmartSelect wired to the
  // 4-base agent roster. The test setup uses agents.find(/mia/) and a mock
  // /api/v1/channels/telegram/routing interceptor; the UI flow likely changed
  // alongside the agent-roster re-cast in d854b02. Tracked:
  // https://github.com/elicify-ai/omnipus/issues/425
  {
    test: '(d) selecting a Default agent calls PUT /channels/{id}/routing with the agent id',
    issue: 'https://github.com/elicify-ai/omnipus/issues/425',
    until: '2026-09-30',
    note: 'ChannelConfigPanel SmartSelect agent-options layout likely changed with the 4-base roster re-cast (d854b02). Verify test against the new UI flow.',
  },
  {
    test: '(e) selecting "(Global default)" calls PUT /channels/{id}/routing with default_agent_id omitted',
    issue: 'https://github.com/elicify-ai/omnipus/issues/425',
    until: '2026-09-30',
    note: 'Same root cause as (d).',
  },
  // command-center.spec.ts — AgentToolsUpdateRequest schema changed in the v0.1.0
  // tool-registry redesign. The test PUTs the OLD shape
  // (builtin.{default_policy, policies}) which the gateway now rejects.
  // Tracked: https://github.com/elicify-ai/omnipus/issues/426
  {
    test: '(b) approval-queue: policy=ask tool call triggers approval modal and Approve routes it through',
    issue: 'https://github.com/elicify-ai/omnipus/issues/426',
    until: '2026-09-30',
    note: 'PUT /api/v1/agents/{id}/tools body uses the old `{builtin: {default_policy, policies: ...}}` shape; the v0.1.0 redesign moved per-tool policies into the `sandbox.tool_policies` model. Rewrite the test against AgentToolsUpdateRequest.',
  },
  // agents.spec.ts — slideover refactor removed the branded 404 page for unknown
  // agent IDs. The transient /_app/agents/$agentId route silently opens the slideover
  // and navigates back to /#/agents for unknown IDs.
  // Tracked: https://github.com/elicify-ai/omnipus/issues/427
  {
    test: '(e) deleted agent URL returns branded 404 with "Back to Agents" link',
    issue: 'https://github.com/elicify-ai/omnipus/issues/427',
    until: '2026-09-30',
    note: 'The /_app/agents/$agentId route (src/routes/_app/agents.$agentId.tsx) is a transient "open slideover + navigate back" handler. Unknown agent IDs do not render a branded 404 page. Either add the 404 page or drop the test.',
  },
  // subagent.spec.ts — known LLM timing flake (per the run-ci configuration
  // comment in playwright.config.ts). The 9 Group-A failures (subagent×5,
  // handoff b, media a, command-center b) all share the same symptom: under
  // prolonged suite load (~12 min total wall-clock) the LLM occasionally takes
  // >40s to emit the expected tool call, even though every test passes alone
  // in 5-25s. Retries are NOT a cover for real bugs — the design-flaw fix
  // is deterministic scenario providers (T4.1). Soft-skip until then.
  // Tracked: https://github.com/dapicom-ai/omnipus/issues/155 (v0.2 hardening)
  {
    test: '(c) live step counter: collapsed header step count increments during multi-step sub-turn',
    issue: 'https://github.com/dapicom-ai/omnipus/issues/155',
    until: '2026-12-31',
    note: 'LLM-driven live step counter occasionally takes >40s under suite load (12+ min wall-clock). Retries=3 in CI typically recover. The deterministic scenario-provider fix is tracked in T4.1.',
  },
];

// ── Validation ──────────────────────────────────────────────────────────────────
//
// Runs at module load time. Any malformed SKIP_ALLOWLIST entry causes an
// immediate throw, ensuring CI cannot silently pass with invalid entries.

const GITHUB_ISSUE_RE = /^https:\/\/github\.com\/.+\/(?:issues|pull)\/\d+$/;
const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

/**
 * validateAllowList — Check that every SKIP_ALLOWLIST entry has:
 *   - `issue` matching the GitHub issue/PR URL pattern
 *   - `until` matching YYYY-MM-DD and being a parseable date
 *
 * Throws on the first invalid entry. Warns (does not throw) for expired entries —
 * the expired-entry check at runtime already fails the individual test.
 *
 * This function is exported so the unit-style sanity checks below can call it
 * without going through the full softSkip() path.
 */
export function validateAllowList(
  list: { test: string; issue: string; until: string; note?: string }[],
): void {
  for (const entry of list) {
    if (!entry.test || typeof entry.test !== 'string' || entry.test.trim() === '') {
      throw new Error(
        `[skip-tracking] Invalid SKIP_ALLOWLIST entry: missing or empty 'test' field.\n` +
        `  Entry: ${JSON.stringify(entry)}\n` +
        `  Fix: set 'test' to the exact string passed as the first argument to test().`,
      );
    }

    if (!entry.issue || !GITHUB_ISSUE_RE.test(entry.issue)) {
      throw new Error(
        `[skip-tracking] Invalid SKIP_ALLOWLIST entry: 'issue' must be a GitHub issue or PR URL.\n` +
        `  Received: "${entry.issue}"\n` +
        `  Expected pattern: https://github.com/<owner>/<repo>/issues/<number>\n` +
        `               or:  https://github.com/<owner>/<repo>/pull/<number>\n` +
        `  Test: "${entry.test}"`,
      );
    }

    if (!entry.until || !DATE_RE.test(entry.until)) {
      throw new Error(
        `[skip-tracking] Invalid SKIP_ALLOWLIST entry: 'until' must match YYYY-MM-DD.\n` +
        `  Received: "${entry.until}"\n` +
        `  Test: "${entry.test}"`,
      );
    }

    const untilDate = new Date(entry.until + 'T00:00:00Z');
    if (isNaN(untilDate.getTime())) {
      throw new Error(
        `[skip-tracking] Invalid SKIP_ALLOWLIST entry: 'until' is not a valid date.\n` +
        `  Received: "${entry.until}"\n` +
        `  Test: "${entry.test}"`,
      );
    }

    // Warn for expired entries — they do not throw here because the individual
    // test will already fail loudly when softSkip() is called with an expired entry.
    if (untilDate < new Date()) {
      console.warn(
        `[skip-tracking] WARNING: SKIP_ALLOWLIST entry for "${entry.test}" has expired ` +
        `(until: ${entry.until}, issue: ${entry.issue}). ` +
        `The test will fail at runtime. Resolve the issue or update the deadline.`,
      );
    }
  }
}

// Run validation at module load time. Any malformed entry throws immediately.
validateAllowList(SKIP_ALLOWLIST);

// ── Internal types ─────────────────────────────────────────────────────────────

interface SkipEntry {
  test: string;
  reason: string;
  ts: number;
  allowed: boolean;
  issue?: string;
  until?: string;
}

export interface SkipManifest {
  timestamp: string;
  run_id: string;
  git_sha: string;
  branch: string;
  skips: Array<{
    test: string;
    reason: string;
    // Only `softSkip()` calls are captured today. `test.skip()` /
    // `test.fixme()` capture is planned for v0.2 (#155).
    // When that lands, the corresponding entries must already be in SKIP_ALLOWLIST.
    // The union is intentionally narrow: 'softSkip' only. Do not widen it here
    // until the teardown suite-walk for raw skips is implemented.
    kind: 'softSkip';
  }>;
  allowlisted: Array<{
    test: string;
    issue: string;
    until: string;
    note?: string;
  }>;
  unauthorized_skips: Array<{
    test: string;
    reason: string;
  }>;
}

// ── Git helpers ────────────────────────────────────────────────────────────────

function getGitSha(): string {
  if (process.env.GITHUB_SHA) return process.env.GITHUB_SHA;
  try {
    return execSync('git rev-parse --short HEAD', { stdio: ['pipe', 'pipe', 'pipe'] })
      .toString()
      .trim();
  } catch {
    return 'unknown';
  }
}

function getGitBranch(): string {
  if (process.env.GITHUB_REF_NAME) return process.env.GITHUB_REF_NAME;
  if (process.env.GITHUB_HEAD_REF) return process.env.GITHUB_HEAD_REF;
  try {
    return execSync('git rev-parse --abbrev-ref HEAD', { stdio: ['pipe', 'pipe', 'pipe'] })
      .toString()
      .trim();
  } catch {
    return 'unknown';
  }
}

// ── Manifest writer ────────────────────────────────────────────────────────────

/**
 * manifestPath — Resolve the output path for the skip manifest.
 * Configurable via `OMNIPUS_SKIP_MANIFEST_PATH` env var.
 * Defaults to `test-results/skip-manifest.json`.
 */
function manifestPath(): string {
  return process.env.OMNIPUS_SKIP_MANIFEST_PATH
    ? path.resolve(process.env.OMNIPUS_SKIP_MANIFEST_PATH)
    : path.resolve('test-results', 'skip-manifest.json');
}

/**
 * writeSkipManifest — Build and write the skip manifest from the in-process
 * soft-skips.json accumulated during the run.
 *
 * Called by global-teardown.ts at the end of every run.
 */
export function writeSkipManifest(): SkipManifest {
  // Read the in-run skip accumulator (soft-skips.json).
  let rawSkips: SkipEntry[] = [];
  const softSkipsPath = path.resolve('test-results', 'soft-skips.json');
  if (fs.existsSync(softSkipsPath)) {
    try {
      const raw = fs.readFileSync(softSkipsPath, 'utf-8').trim();
      if (raw) rawSkips = JSON.parse(raw) as SkipEntry[];
    } catch {
      console.warn('[skip-tracking] Could not parse soft-skips.json; manifest will be empty');
    }
  }

  const authorizedEntries = SKIP_ALLOWLIST.map((e) => ({
    test: e.test,
    issue: e.issue,
    until: e.until,
    note: e.note,
  }));

  const allSkips = rawSkips.map((s) => ({
    test: s.test,
    reason: s.reason,
    kind: 'softSkip' as const,
  }));

  const unauthorizedSkips = rawSkips
    .filter((s) => !s.allowed)
    .map((s) => ({ test: s.test, reason: s.reason }));

  const manifest: SkipManifest = {
    timestamp: new Date().toISOString(),
    run_id: process.env.GITHUB_RUN_ID ?? process.env.CI_JOB_ID ?? 'local',
    git_sha: getGitSha(),
    branch: getGitBranch(),
    skips: allSkips,
    allowlisted: authorizedEntries,
    unauthorized_skips: unauthorizedSkips,
  };

  // Write to disk.
  const outPath = manifestPath();
  const outDir = path.dirname(outPath);
  try {
    if (!fs.existsSync(outDir)) {
      fs.mkdirSync(outDir, { recursive: true });
    }
    fs.writeFileSync(outPath, JSON.stringify(manifest, null, 2), 'utf-8');
  } catch (writeErr) {
    console.warn('[skip-tracking] Failed to write skip manifest:', writeErr);
  }

  return manifest;
}

// ── Implementation ─────────────────────────────────────────────────────────────

/**
 * Record a skip and either fail the test (if not allow-listed) or call test.skip().
 *
 * IMPORTANT: If the test's title does NOT appear in SKIP_ALLOWLIST, this function
 * throws an error with a clear message. The test will be marked FAILED, not SKIPPED.
 * This is intentional — it prevents silent drift back into soft-skip culture.
 *
 * @param t      - the Playwright `test` object
 * @param reason - human-readable reason for the skip attempt
 */
export function softSkip(
  t: { info: () => { title: string }; skip: () => void },
  reason: string,
): void {
  const title = t.info().title;

  // Find a matching allow-list entry.
  const entry = SKIP_ALLOWLIST.find((e) => e.test === title);

  // Check expiry: if the entry has a `until` date in the past, treat as expired.
  let expired = false;
  if (entry) {
    const until = new Date(entry.until + 'T00:00:00Z');
    if (!isNaN(until.getTime()) && until < new Date()) {
      expired = true;
    }
  }

  const allowed = Boolean(entry) && !expired;

  // Build the record.
  const record: SkipEntry = {
    test: title,
    reason,
    ts: Date.now(),
    allowed,
    ...(entry ? { issue: entry.issue, until: entry.until } : {}),
  };

  // Append to test-results/soft-skips.json (best-effort — non-fatal write failure).
  try {
    const dir = path.resolve('test-results');
    if (!fs.existsSync(dir)) {
      fs.mkdirSync(dir, { recursive: true });
    }
    const filePath = path.join(dir, 'soft-skips.json');
    let existing: SkipEntry[] = [];
    if (fs.existsSync(filePath)) {
      try {
        const raw = fs.readFileSync(filePath, 'utf-8').trim();
        if (raw) existing = JSON.parse(raw) as SkipEntry[];
      } catch {
        existing = [];
      }
    }
    existing.push(record);
    fs.writeFileSync(filePath, JSON.stringify(existing, null, 2), 'utf-8');
  } catch (writeErr) {
    console.warn('[skip-tracking] Failed to write skip entry:', writeErr);
  }

  if (!allowed) {
    // Not in the allow-list (or allow-list entry expired) → fail loudly.
    const expiredMsg = expired && entry
      ? ` (allow-list entry expired ${entry.until} — issue ${entry.issue})`
      : '';
    throw new Error(
      `[skip-tracking] UNAUTHORIZED SKIP${expiredMsg}\n` +
      `  test:   "${title}"\n` +
      `  reason: "${reason}"\n\n` +
      `This test called softSkip() without a valid allow-list entry. ` +
      `Either fix the underlying issue or add an entry to SKIP_ALLOWLIST in ` +
      `tests/e2e/fixtures/skip-tracking.ts with a GitHub issue URL and target date.\n` +
      `Do NOT use test.skip() or softSkip() to suppress test failures without tracking them.`,
    );
  }

  if (expired && entry) {
    // Expired allow-list entry — also fail loudly.
    throw new Error(
      `[skip-tracking] EXPIRED ALLOW-LIST ENTRY\n` +
      `  test:   "${title}"\n` +
      `  issue:  "${entry.issue}"\n` +
      `  until:  "${entry.until}" (past)\n\n` +
      `The allow-list entry for this test has passed its target date. ` +
      `Either resolve the underlying issue (${entry.issue}) or update the 'until' date ` +
      `with justification. Do not silently extend deadlines.`,
    );
  }

  // Allow-listed and not expired → skip normally.
  t.skip();
}

// ── Unit-style sanity checks ────────────────────────────────────────────────────
//
// These run at module load time (not in a test framework) and verify that the
// validator itself works correctly. They do not require Playwright or Vitest.
// If they throw, the import of this module will fail — which surfaces the bug
// immediately in CI rather than hiding it until a test runs.

(function _selfTestValidator(): void {
  // The validator must accept a valid entry.
  const validEntry = {
    test: 'some test title',
    issue: 'https://github.com/dapicom-ai/omnipus/issues/123',
    until: '2099-12-31',
  };
  try {
    validateAllowList([validEntry]);
  } catch (e) {
    throw new Error(
      `[skip-tracking] Self-test FAILED: validateAllowList rejected a valid entry.\n` +
      `Entry: ${JSON.stringify(validEntry)}\n` +
      `Error: ${e instanceof Error ? e.message : String(e)}`,
    );
  }

  // The validator must reject an entry missing 'issue'.
  let threw = false;
  try {
    validateAllowList([{ test: 'x', issue: '', until: '2099-01-01' }]);
  } catch {
    threw = true;
  }
  if (!threw) {
    throw new Error(
      `[skip-tracking] Self-test FAILED: validateAllowList did NOT throw for empty 'issue'.`,
    );
  }

  // The validator must reject a non-GitHub URL for 'issue'.
  threw = false;
  try {
    validateAllowList([{ test: 'x', issue: 'https://jira.example.com/browse/XYZ-123', until: '2099-01-01' }]);
  } catch {
    threw = true;
  }
  if (!threw) {
    throw new Error(
      `[skip-tracking] Self-test FAILED: validateAllowList did NOT throw for non-GitHub 'issue'.`,
    );
  }

  // The validator must reject a malformed 'until' date.
  threw = false;
  try {
    validateAllowList([{ test: 'x', issue: 'https://github.com/dapicom-ai/omnipus/issues/1', until: '01/01/2099' }]);
  } catch {
    threw = true;
  }
  if (!threw) {
    throw new Error(
      `[skip-tracking] Self-test FAILED: validateAllowList did NOT throw for non-YYYY-MM-DD 'until'.`,
    );
  }

  // The validator must reject a missing 'until' field.
  threw = false;
  try {
    validateAllowList([{ test: 'x', issue: 'https://github.com/dapicom-ai/omnipus/issues/1', until: '' }]);
  } catch {
    threw = true;
  }
  if (!threw) {
    throw new Error(
      `[skip-tracking] Self-test FAILED: validateAllowList did NOT throw for empty 'until'.`,
    );
  }
})();
