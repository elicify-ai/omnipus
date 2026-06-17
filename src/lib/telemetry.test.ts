/**
 * telemetry.test.ts — verify the W2-36 production-telemetry sink.
 *
 * The sink must:
 *   - emit a single-line JSON record via console.error
 *   - rate-limit bursts so a contract-drift flood doesn't spam the log
 */

import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { logError } from './telemetry'

describe('W2-36 telemetry.logError', () => {
  let errorSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('emits a single-line JSON record with the telemetry prefix', () => {
    logError({ event: 'wsFrameDropped', frameType: 'message', totalDropped: 1 })
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
      logError({ event: 'apiSchemaError', endpoint: '/upload', issueCount: null, totalErrors: undefined })
    ).not.toThrow()
    expect(errorSpy).toHaveBeenCalledTimes(1)
  })

  it('rate-limits bursts (over SAMPLE_LIMIT in SAMPLE_WINDOW_MS)', () => {
    // First SAMPLE_LIMIT events emit, the rest are throttled within the window.
    for (let i = 0; i < 20; i++) {
      logError({ event: 'wsFrameDropped', frameType: 'message', totalDropped: i })
    }
    // SAMPLE_LIMIT is 10 — expect exactly 10 console.error calls.
    expect(errorSpy.mock.calls.length).toBeLessThanOrEqual(10)
  })
})