/**
 * global-teardown.ts — Fail the run if unauthorized skips occurred
 * or if the skip count rises versus the previous-green-main baseline.
 *
 * ## What this teardown does
 *
 * 1. Invokes `writeSkipManifest()` from skip-tracking.ts to build and write
 *    `test-results/skip-manifest.json` with all skips recorded during the run.
 *
 * 2. Reads `tests/e2e/fixtures/skip-baseline.json` (the "previous green main"
 *    anchor) and compares `manifest.unauthorized_skips.length` against
 *    `baseline.baseline_unauthorized_skips`.
 *
 * 3. Exits with code 1 (failing the CI run) if:
 *    (a) The manifest's unauthorized skip count is greater than the baseline.
 *    (b) Any allow-listed entry in SKIP_ALLOWLIST has an expired `until` date.
 *
 * 4. Emits a human-readable summary to stdout/stderr for CI log readability.
 *
 * ## Note on softSkip() vs teardown responsibility
 *
 * When a test calls `softSkip()` without a valid allow-list entry, softSkip()
 * already throws and the TEST is marked FAILED. The teardown does not re-fail
 * those — it summarizes them and checks the aggregate count against the baseline.
 *
 * The teardown's primary job is the BASELINE GATE: prevent the unauthorized-skip
 * count from silently creeping up across commits. The per-test FAILED status is
 * the canonical CI signal; the teardown is defense-in-depth.
 */

import * as fs from 'fs';
import * as path from 'path';
import { writeSkipManifest, SKIP_ALLOWLIST, type SkipManifest } from './fixtures/skip-tracking.js';

interface SkipBaseline {
  baseline_skip_count: number;
  baseline_unauthorized_skips: number;
  comment?: string;
}

async function globalTeardown(): Promise<void> {
  // ── 1. Write the skip manifest ────────────────────────────────────────────

  let manifest: SkipManifest;
  try {
    manifest = writeSkipManifest();
  } catch (manifestErr) {
    console.warn('[skip-tracking teardown] Failed to write skip manifest:', manifestErr);
    // Fall back to reading soft-skips.json directly for backward compatibility.
    const fallbackManifest: SkipManifest = {
      timestamp: new Date().toISOString(),
      run_id: 'local',
      git_sha: 'unknown',
      branch: 'unknown',
      skips: [],
      allowlisted: [],
      unauthorized_skips: [],
    };
    manifest = fallbackManifest;
  }

  // ── 2. Read the baseline ──────────────────────────────────────────────────

  const baselinePath = path.resolve('tests/e2e/fixtures/skip-baseline.json');
  let baseline: SkipBaseline = { baseline_skip_count: 0, baseline_unauthorized_skips: 0 };
  if (fs.existsSync(baselinePath)) {
    try {
      const raw = fs.readFileSync(baselinePath, 'utf-8').trim();
      if (raw) baseline = JSON.parse(raw) as SkipBaseline;
    } catch {
      console.warn('[skip-tracking teardown] Could not parse skip-baseline.json; using zero baseline');
    }
  } else {
    console.warn(
      '[skip-tracking teardown] skip-baseline.json not found at ' + baselinePath +
      '; treating baseline as zero. Create this file to anchor the gate.',
    );
  }

  // ── 3. Check for expired allow-list entries ───────────────────────────────

  const today = new Date();
  const expiredEntries = SKIP_ALLOWLIST.filter((e) => {
    const until = new Date(e.until + 'T00:00:00Z');
    return !isNaN(until.getTime()) && until < today;
  });

  // ── 4. Summarize and gate ─────────────────────────────────────────────────

  const unauthorizedCount = manifest.unauthorized_skips.length;
  const authorizedCount = manifest.allowlisted.length;
  const totalSkipCount = manifest.skips.length;

  // Print the one-line summary regardless of gate outcome.
  const manifestPath = process.env.OMNIPUS_SKIP_MANIFEST_PATH
    ? path.resolve(process.env.OMNIPUS_SKIP_MANIFEST_PATH)
    : path.resolve('test-results', 'skip-manifest.json');

  console.log(
    `\n[skip-tracking] Run summary:` +
    ` ${totalSkipCount} total skip(s),` +
    ` ${authorizedCount} allowlisted,` +
    ` ${unauthorizedCount} unauthorized.` +
    ` Manifest: ${manifestPath}`,
  );

  if (authorizedCount > 0) {
    console.log('[skip-tracking] Authorized skips this run:');
    for (const e of manifest.allowlisted) {
      console.log(`  - "${e.test}" (${e.issue}, until ${e.until})`);
    }
  }

  if (unauthorizedCount > 0) {
    console.error(`\n[skip-tracking] UNAUTHORIZED SKIPS DETECTED (${unauthorizedCount}):`);
    for (const e of manifest.unauthorized_skips) {
      console.error(`  - "${e.test}": ${e.reason}`);
    }
    console.error(
      '\nThese tests called softSkip() without a valid SKIP_ALLOWLIST entry. ' +
      'The tests above should already be marked FAILED in the test report. ' +
      'To resolve: fix the underlying issue, or add an entry to SKIP_ALLOWLIST ' +
      'in tests/e2e/fixtures/skip-tracking.ts with a GitHub issue URL and target date.\n',
    );
  }

  // ── Gate: unauthorized skip count vs baseline ─────────────────────────────

  let shouldFail = false;

  if (unauthorizedCount > baseline.baseline_unauthorized_skips) {
    shouldFail = true;
    console.error(
      `\n[skip-tracking] BASELINE GATE FAILED:\n` +
      `  Unauthorized skips this run:  ${unauthorizedCount}\n` +
      `  Baseline (last green main):   ${baseline.baseline_unauthorized_skips}\n` +
      `\n` +
      `  The skip count has risen above the baseline. This run FAILS.\n` +
      `\n` +
      `  To resolve one of:\n` +
      `    (a) Fix the underlying issue so the test no longer skips.\n` +
      `    (b) Add the skip to SKIP_ALLOWLIST with a GitHub issue URL and target date.\n` +
      `        Then update baseline_unauthorized_skips in skip-baseline.json and commit.\n` +
      `\n` +
      `  DO NOT increment the baseline without adding a tracked SKIP_ALLOWLIST entry.\n`,
    );
  }

  // ── Gate: expired allow-list entries ─────────────────────────────────────

  if (expiredEntries.length > 0) {
    shouldFail = true;
    console.error(
      `\n[skip-tracking] EXPIRED ALLOW-LIST ENTRIES (${expiredEntries.length}):\n`,
    );
    for (const e of expiredEntries) {
      console.error(`  - "${e.test}"\n    issue: ${e.issue}\n    until: ${e.until} (PAST)\n`);
    }
    console.error(
      `  These allow-list entries have passed their target dates. This run FAILS.\n` +
      `  Resolve each underlying issue or update the 'until' date with justification.\n` +
      `  Do not silently extend deadlines.\n`,
    );
  }

  if (!shouldFail) {
    console.log(
      `[skip-tracking] OK — unauthorized skip count (${unauthorizedCount}) ` +
      `<= baseline (${baseline.baseline_unauthorized_skips}); ` +
      `no expired allow-list entries.`,
    );
  }

  if (shouldFail) {
    // Use process.exit(1) rather than throwing, to avoid confusing Playwright's
    // internal teardown error handling with a secondary stack trace. The
    // console.error() output above is the canonical failure signal.
    process.exit(1);
  }
}

export default globalTeardown;
