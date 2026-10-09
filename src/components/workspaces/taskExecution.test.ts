// Review coverage: independent format literals and a clock-skew inversion.
import { expect, it } from 'vitest'
import { executionTimeLabel, taskExecutionSeconds } from './taskExecution'

it.each([
  [45, '45s'],
  [3599, '59m 59s'],
  [3600, '1h 00m 00s'],
  [3661, '1h 01m 01s'],
] as const)('formats %i execution seconds as %s', (seconds, expected) => {
  expect(executionTimeLabel(seconds)).toBe(expected)
})

it('rejects terminal execution timestamps whose completion precedes their start', () => {
  expect(taskExecutionSeconds({ status: 'failed', started_at: '2026-10-08T18:01:00Z', completed_at: '2026-10-08T18:00:00Z' }, Date.parse('2026-10-08T19:00:00Z'))).toBeNull()
})
