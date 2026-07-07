import { describe, it, expect } from 'vitest'
import { formatDuration } from './formatDuration'

// Direct unit coverage for the shared duration formatter (previously only
// exercised indirectly via SubagentBlock.test.tsx, ActivityPanel.test.tsx,
// ToolCallBadge.test.tsx, GenericToolCall.test.tsx). See formatDuration.ts's
// file header: canonical behavior is that a genuine duration always renders
// (including exactly 0ms), and only a missing (null/undefined) value renders
// as ''.

describe('formatDuration — missing value', () => {
  it('returns empty string for undefined', () => {
    expect(formatDuration(undefined)).toBe('')
  })

  it('returns empty string for null, even though the public signature only advertises number | undefined', () => {
    // `ms == null` covers both null and undefined at runtime; some callers
    // pass a nullable duration (e.g. a WS frame field typed nullable on the
    // wire), so this guards the actual runtime behavior, not just the type.
    expect(formatDuration(null as unknown as number)).toBe('')
  })

  it('returns empty string when called with no argument at all', () => {
    expect(formatDuration()).toBe('')
  })
})

describe('formatDuration — genuine zero duration', () => {
  it('renders "0ms" for an exact zero duration, distinct from the empty-string missing-value case', () => {
    // This is the specific behavior asserted as intentional in formatDuration.ts's
    // header comment ("including exactly 0ms") and previously had zero direct
    // test coverage anywhere in the repo.
    expect(formatDuration(0)).toBe('0ms')
  })
})

describe('formatDuration — sub-second durations (< 1000ms)', () => {
  it.each([
    [1, '1ms'],
    [42, '42ms'],
    [420, '420ms'],
    [999, '999ms'],
  ])('formats %ims as %s', (ms, expected) => {
    expect(formatDuration(ms)).toBe(expected)
  })
})

describe('formatDuration — second-scale durations (>= 1000ms)', () => {
  it.each([
    [1000, '1.0s'],
    [1300, '1.3s'],
    [2300, '2.3s'],
    [59900, '59.9s'],
    [125000, '125.0s'],
  ])('formats %ims as %s', (ms, expected) => {
    expect(formatDuration(ms)).toBe(expected)
  })

  it('boundary: 999ms stays milliseconds, 1000ms crosses into seconds', () => {
    expect(formatDuration(999)).toBe('999ms')
    expect(formatDuration(1000)).toBe('1.0s')
  })
})
