/**
 * telemetry.test.ts — verify the W2-36 production-telemetry sink.
 *
 * The sink must:
 *   - emit a single-line JSON record via console.error (production only;
 *     vitest's MODE === 'test' is gated to keep CI logs clean — W4-16)
 *   - rate-limit bursts so a contract-drift flood doesn't spam the log
 *
 * Tests probe the rate-limit logic via the exported `_resetTelemetryForTest`
 * helper + a custom logger hook (the production code path is gated in
 * test mode, so we cannot assert on console.error directly).
 */

import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { logError, _resetTelemetryForTest } from './telemetry'

// The production logError path is gated behind `import.meta.env.MODE !==
// 'test'` (W4-16) so a CI run that fires many telemetry events does not
// spam the captured log. To verify the rate-limit logic in a unit test we
// monkey-patch console.error to a noop and drive the function directly —
// the gate does not interfere with the in-process rate-limit state, only
// with the console.error side effect. We assert via the side effect of
// console.error calls in production-mode runs (the tests below override
// import.meta.env.MODE per test).
//
// In a vitest run, MODE === 'test' so the production path is a noop. To
// test the rate-limit branch we re-export a thin wrapper that calls the
// internal _shouldEmit logic; since that's not exported, we use a
// "force-emit" path: spying on console.error and triggering the inner
// branch by replacing import.meta.env.MODE at the call site is not
// possible in vitest, so we test the rate-limit by using the
// _resetTelemetryForTest helper + direct console.error monitoring with
// the gate temporarily bypassed via the test wrapper below.

// We expose a `forceEmit` helper that bypasses the test-mode gate so the
// tests can observe the rate-limit + window-reset behaviour. This is
// imported from a test-only sub-module to keep it out of production
// call sites.
import { _emitTelemetryRaw } from './telemetry'

describe('W2-36 telemetry.logError', () => {
  let errorSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    // W4-22: the rate-limit state (_sampleWindowStart, _sampleCount)
    // is module-level and persists across tests. Reset it before each
    // test so the assertions below observe a clean slate.
    _resetTelemetryForTest()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('emits a single-line JSON record with the telemetry prefix', () => {
    // Drive the raw emitter (bypasses the test-mode gate) and observe
    // console.error directly. This is the production wire format that
    // a log-collector (journald, Loki, Sentry console capture) would
    // pick up.
    _emitTelemetryRaw({ event: 'wsFrameDropped', frameType: 'message', totalDropped: 1 })
    expect(errorSpy).toHaveBeenCalledTimes(1)
    const arg = errorSpy.mock.calls[0][0]
    expect(typeof arg).toBe('string')
    const parsed = JSON.parse(arg as string)
    expect(parsed.telemetry).toBe('error')
    expect(parsed.event).toBe('wsFrameDropped')
    expect(parsed.frameType).toBe('message')
    expect(parsed.totalDropped).toBe(1)
  })

  it('does not throw on null/undefined property values', () => {
    expect(() =>
      _emitTelemetryRaw({ event: 'apiSchemaError', endpoint: '/upload', issueCount: null, totalErrors: undefined })
    ).not.toThrow()
    expect(errorSpy).toHaveBeenCalledTimes(1)
  })

  it('logError (gated) is a noop in test mode (W4-16)', () => {
    // The production-facing logError MUST NOT emit when MODE === 'test'
    // (vitest run). This is the W4-16 contract: CI log hygiene.
    logError({ event: 'wsFrameDropped', frameType: 'message', totalDropped: 1 })
    expect(errorSpy).not.toHaveBeenCalled()
  })

  it('rate-limits bursts (over SAMPLE_LIMIT in SAMPLE_WINDOW_MS)', () => {
    // W4-22: previous assertion was `toBeLessThanOrEqual(10)` which
    // accepts 0/1/10 alike and would mask a regression that never
    // emitted anything. Assert EXACTLY 10 — the first SAMPLE_LIMIT
    // events emit, the rest are throttled within the window.
    //
    // We freeze Date.now so the SAMPLE_WINDOW_MS window does not
    // advance during the loop (a real-time burst that crosses the
    // 60s boundary would reset the counter mid-test, masking the
    // rate-limit invariant we are verifying).
    const realNow = Date.now
    const frozen = 1_700_000_000_000
    Date.now = () => frozen
    try {
      for (let i = 0; i < 20; i++) {
        _emitTelemetryRaw({ event: 'wsFrameDropped', frameType: 'message', totalDropped: i })
      }
      expect(errorSpy).toHaveBeenCalledTimes(10)
    } finally {
      Date.now = realNow
    }
  })

  it('resets the sample window after SAMPLE_WINDOW_MS and resumes emitting', () => {
    // W4-22: the SAMPLE_WINDOW_MS reset path is a separate branch from
    // the rate-limit branch. A regression that always returns false
    // from _shouldEmit (e.g. an off-by-one on _sampleCount) would
    // pass the rate-limit test above but fail this one.
    const realNow = Date.now
    const baseTime = 1_700_000_000_000
    let now = baseTime
    Date.now = () => now
    try {
      // Burn the 10 emissions in the initial window.
      for (let i = 0; i < 12; i++) {
        _emitTelemetryRaw({ event: 'wsFrameDropped', frameType: 'message', totalDropped: i })
      }
      const callsAfterBurn = errorSpy.mock.calls.length
      expect(callsAfterBurn).toBe(10)

      // Advance past the window. The next emission MUST be observed.
      now += 60_001
      _emitTelemetryRaw({ event: 'wsFrameDropped', frameType: 'message', totalDropped: 999 })
      expect(errorSpy.mock.calls.length).toBe(callsAfterBurn + 1)
      // And the new emission carried the post-reset payload.
      const last = errorSpy.mock.calls.at(-1)?.[0] as string
      const parsed = JSON.parse(last)
      expect(parsed.totalDropped).toBe(999)
    } finally {
      Date.now = realNow
    }
  })
})
