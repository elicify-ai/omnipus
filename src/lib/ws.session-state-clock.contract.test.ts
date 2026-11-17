// #1537 Q1: execute the generated SPA schema, never a test-owned validator.
// Oracle: helper-model-notices-spec.md Contract Changes SessionStateFrame / FR-008.
import { expect, it } from 'vitest'
import { SessionStateFrame } from './api/generated/ws-schemas'

it('Q1 generated SPA accepts optional integer emitted_at_ms and rejects string/fraction/negative/null', () => {
  const legacy = { type: 'session_state', user_id: 'q1-reader', pending_approvals: [], emitted_at: '2026-11-15T12:00:00Z' }
  expect(SessionStateFrame.safeParse(legacy).success, 'Q1 SPA contract: old absent-field frame remains valid').toBe(true)
  for (const value of [0, 1, 1794744000900]) {
    // Align both fields even at the epoch boundaries; schema shape is not a clock mock.
    const emitted_at = new Date(value).toISOString().replace(/\.\d{3}Z$/, 'Z')
    const parsed = SessionStateFrame.safeParse({ ...legacy, emitted_at, emitted_at_ms: value })
    expect.soft(parsed.success, `Q1 SPA contract: generated schema accepts emitted_at_ms=${value}`).toBe(true)
    if (parsed.success) expect.soft(parsed.data, 'Q1 SPA contract: the millisecond sample survives parsing').toHaveProperty('emitted_at_ms', value)
  }
  for (const value of ['1794744000900', -1, 0.5, null]) {
    expect.soft(SessionStateFrame.safeParse({ ...legacy, emitted_at_ms: value }).success,
      `Q1 SPA contract: generated schema rejects invalid emitted_at_ms=${JSON.stringify(value)}`).toBe(false)
  }
})
