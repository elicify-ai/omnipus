import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { formatRelative } from '@/lib/formatRelative'

// Fix "now" so relative-time math is deterministic.
const NOW = new Date('2026-07-16T12:00:00.000Z')

describe('formatRelative', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('returns "just now" for a timestamp under a minute old', () => {
    expect(formatRelative(new Date(NOW.getTime() - 30_000).toISOString())).toBe('just now')
  })

  it('returns minutes-ago for a timestamp under an hour old', () => {
    expect(formatRelative(new Date(NOW.getTime() - 5 * 60_000).toISOString())).toBe('5m ago')
  })

  it('returns hours-ago for a timestamp under a day old', () => {
    expect(formatRelative(new Date(NOW.getTime() - 2 * 60 * 60_000).toISOString())).toBe('2h ago')
  })

  it('returns days-ago for a timestamp under a week old', () => {
    expect(formatRelative(new Date(NOW.getTime() - 3 * 24 * 60 * 60_000).toISOString())).toBe('3d ago')
  })

  it('falls back to a short absolute date once the age reaches a week', () => {
    const eightDaysAgo = new Date(NOW.getTime() - 8 * 24 * 60 * 60_000)
    expect(formatRelative(eightDaysAgo.toISOString())).toBe(
      eightDaysAgo.toLocaleDateString(undefined, { month: 'short', day: 'numeric' }),
    )
  })

  it('returns an empty string for an unparseable date', () => {
    expect(formatRelative('not-a-date')).toBe('')
  })
})
