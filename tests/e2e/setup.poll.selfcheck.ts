/**
 * setup.poll.selfcheck.ts — unit tests for pollUntilHealthy (tests/e2e/setup.ts)
 *
 * These are NOT Playwright specs (deliberately named so Playwright's default
 * testMatch, "**\/*.@(spec|test).?(c|m)[jt]s?(x)", does NOT pick this file up —
 * it would try to load it as a Playwright test file, but it uses node:test's
 * `test`, not @playwright/test's, and would either be silently ignored or
 * mis-collected). This file needs no gateway binary, no browser, no
 * globalSetup, and no npm install — it exercises the pure polling decision
 * logic in complete isolation, using Node's built-in test runner and its
 * experimental TypeScript type-stripping support (both available out of the
 * box on Node 22, no extra dependency).
 *
 * Run directly:
 *   node --experimental-strip-types --test tests/e2e/setup.poll.selfcheck.ts
 *
 * Traces to: the CI-blocking test-harness-defect fix task (2026-07-29) —
 * "N consecutive failed probes, not wall-clock deadline" — TypeScript twin of
 * pkg/agent/testutil/gateway_harness_poll_test.go (Go side).
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { pollUntilHealthy, type ProbeOutcome } from './setup.ts';

/** A deterministic, manually-advanced clock. Advancing it from inside a
 * probe/sleep closure simulates variable probe latency, including a
 * multi-second "host freeze" during which nothing runs at all. All tests
 * below run in well under a millisecond of REAL time regardless of how much
 * SIMULATED time they model. */
class FakeClock {
  private ms = 1_700_000_000_000;
  now = (): number => this.ms;
  advance(deltaMs: number): void {
    this.ms += deltaMs;
  }
}

// CASE 1 — "A simulated host freeze (no probes run for several seconds, then
// the gateway is healthy) passes where it previously failed."
//
// The freeze is modeled as: the first probe attempt only happens after 20s of
// (simulated) wall-clock time has silently passed — nothing was polling, the
// host was unscheduled — and even that first attempt catches the gateway a
// moment before it starts listening (a transient, single failure: exactly the
// "unlucky" case a bare wall-clock deadline would treat as terminal, since
// 20s already exceeds the historical 15s deadline before a single real probe
// ran). The very next probe, 200ms later, succeeds.
//
// The OLD design (`while (Date.now() < deadline)`) would fail here: as soon
// as the freeze ends, elapsed time already exceeds any 15s-class deadline, so
// the loop body would never even execute once. The NEW design must succeed,
// because a freeze produces FEW probes, not FAILED ones, and only 1
// consecutive failure has occurred here — nowhere near the 75-consecutive-
// failure threshold used for a 15s timeout at a 200ms interval.
test('pollUntilHealthy: host freeze then healthy succeeds', async () => {
  const clock = new FakeClock();
  let probeCalls = 0;
  const probe = async (): Promise<ProbeOutcome> => {
    probeCalls++;
    if (probeCalls === 1) {
      clock.advance(20_000); // the freeze: no probes ran during this window
      return { ok: false, error: new Error('fetch failed: ECONNREFUSED') };
    }
    return { ok: true }; // healthy on the very next attempt
  };

  const result = await pollUntilHealthy({
    probe,
    intervalMs: 200,
    consecutiveFailThreshold: 75, // 75 * 200ms == 15s, the historical default budget
    hardBackstopMs: 30_000,
    now: clock.now,
    sleep: async (ms) => {
      clock.advance(ms);
    },
  });

  assert.equal(result.kind, 'ready', 'a freeze followed by a healthy probe must succeed regardless of elapsed time');
  assert.equal(probeCalls, 2);
  assert.equal(result.attempts, 2);
  assert.ok(result.elapsedMs >= 20_000, 'elapsed must reflect the real freeze duration, not hide it');
});

// CASE 2 — "A genuine boot failure fails fast and reports the actual boot
// error, not a timeout."
//
// checkFatalError reports a failure on the very first check, before any HTTP
// probe is even attempted (mirrors: the gateway process already exited). The
// old design had no equivalent fast-fail check at all — a crash right after
// the "listening" log line would still cost the entire polling budget before
// being reported. The new design must fail on the very first iteration, with
// zero elapsed simulated time and zero probe attempts, carrying the real
// cause.
test('pollUntilHealthy: fatal error check fails fast', async () => {
  const clock = new FakeClock();
  const fatal = new Error('gateway process exited with code 1 while waiting for /health');
  let probeCalls = 0;
  let sleepCalls = 0;

  const result = await pollUntilHealthy({
    probe: async () => {
      probeCalls++;
      return { ok: false, error: new Error('probe should never be reached once a fatal error is present') };
    },
    checkFatalError: () => fatal,
    intervalMs: 200,
    consecutiveFailThreshold: 75,
    hardBackstopMs: 30_000,
    now: clock.now,
    sleep: async (ms) => {
      sleepCalls++;
      clock.advance(ms);
    },
  });

  assert.equal(result.kind, 'fatal-error');
  assert.equal(probeCalls, 0, 'a fatal error must short-circuit before ever attempting a health probe');
  assert.equal(sleepCalls, 0, 'must not sleep on a fast-fail fatal error');
  assert.equal(result.attempts, 0);
  assert.equal(result.elapsedMs, 0, 'must fail before consuming any wall-clock budget');
  assert.equal(result.fatalError, fatal);
});

// CASE 3a — the "genuine boot failure that never surfaces via a fatal-error
// check" half of "a genuinely wedged gateway still fails, bounded, with a
// message that distinguishes it from case 1": the health endpoint fails on
// every fast attempt (e.g. the process is up but /health never starts
// returning 200) and there is no process-exit signal. The consecutive-
// failure threshold — not the hard backstop — must be what trips, and it
// must trip well within the hard backstop, proving the two are genuinely
// distinct signals rather than the same check wearing two names.
test('pollUntilHealthy: consecutive failures trip the threshold, not the backstop', async () => {
  const clock = new FakeClock();
  const probeErr = new Error('health endpoint returned status 503');
  let probeCalls = 0;

  const result = await pollUntilHealthy({
    probe: async () => {
      probeCalls++;
      return { ok: false, error: probeErr };
    },
    intervalMs: 200,
    consecutiveFailThreshold: 75,
    hardBackstopMs: 30_000,
    now: clock.now,
    sleep: async (ms) => {
      clock.advance(ms);
    },
  });

  assert.equal(result.kind, 'consecutive-failures');
  assert.equal(probeCalls, 75);
  assert.equal(result.attempts, 75);
  assert.equal(result.consecutiveFailures, 75);
  assert.equal(result.lastProbeError, probeErr);
  // 75 attempts at a 200ms interval == 74 sleeps == 14,800ms elapsed —
  // comfortably under the 30,000ms hard backstop, proving the THRESHOLD (not
  // the backstop) is what tripped here.
  assert.equal(result.elapsedMs, 74 * 200);
  assert.ok(result.elapsedMs < 30_000);
});

// CASE 3b — the other half of "genuinely wedged": a gateway that accepts
// connections but hangs on every request (each probe itself takes ~1s to
// fail, e.g. a proxy/TCP-level timeout) rather than failing fast. At that
// latency, reaching the 75-consecutive-failure threshold would take about a
// minute — too long to make CI wait. The absolute hardBackstopMs ceiling must
// trip instead, and after far fewer attempts than the threshold would
// require — proving it is a genuinely distinct (and reachable) ceiling, not
// a signal that only fires in theory.
test('pollUntilHealthy: hard backstop trips for slow/wedged probes', async () => {
  const clock = new FakeClock();
  const probeErr = new Error('fetch failed: The operation was aborted due to timeout');
  let probeCalls = 0;

  const result = await pollUntilHealthy({
    probe: async () => {
      probeCalls++;
      clock.advance(1_000); // simulate a hanging request
      return { ok: false, error: probeErr };
    },
    intervalMs: 200,
    consecutiveFailThreshold: 75, // would need ~75 * 1.2s ≈ 90s to reach — must never get there
    hardBackstopMs: 30_000,
    now: clock.now,
    sleep: async (ms) => {
      clock.advance(ms);
    },
  });

  assert.equal(result.kind, 'hard-backstop');
  assert.ok(result.attempts < 75, 'the backstop, not the consecutive-failure threshold, must be what trips');
  assert.ok(result.elapsedMs >= 30_000);
  assert.equal(result.lastProbeError, probeErr);
  // Cross-check the implementation's self-reported attempt count against the
  // actual number of probe invocations — the same guard CASE 3a applies —
  // so a miscount inside pollUntilHealthy's attempt bookkeeping can't hide
  // behind a `result.attempts < 75` bound that a wrong count could also satisfy.
  assert.equal(result.attempts, probeCalls);
});

// Sanity/regression check for the ordinary happy path: a handful of
// connection-refused probes while the listener is still binding, then
// success — the overwhelmingly common real-world case. Must succeed quickly
// and report an accurate attempt count.
test('pollUntilHealthy: eventual success after a few transient failures succeeds', async () => {
  const clock = new FakeClock();
  const failuresBeforeSuccess = 4;
  let probeCalls = 0;

  const result = await pollUntilHealthy({
    probe: async () => {
      probeCalls++;
      if (probeCalls <= failuresBeforeSuccess) {
        return { ok: false, error: new Error('connect ECONNREFUSED') };
      }
      return { ok: true };
    },
    intervalMs: 200,
    consecutiveFailThreshold: 75,
    hardBackstopMs: 30_000,
    now: clock.now,
    sleep: async (ms) => {
      clock.advance(ms);
    },
  });

  assert.equal(result.kind, 'ready');
  assert.equal(result.attempts, failuresBeforeSuccess + 1);
  assert.equal(result.elapsedMs, failuresBeforeSuccess * 200);
});
