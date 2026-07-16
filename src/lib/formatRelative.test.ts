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

  describe('boundaries (R2-3 T8)', () => {
    it('59.999s is still "just now" (just under the 60s boundary)', () => {
      expect(formatRelative(new Date(NOW.getTime() - 59_999).toISOString())).toBe('just now')
    })

    it('exactly 60s crosses into "1m ago"', () => {
      expect(formatRelative(new Date(NOW.getTime() - 60_000).toISOString())).toBe('1m ago')
    })

    it('59m59.999s is still "59m ago" (just under the 60m boundary)', () => {
      expect(formatRelative(new Date(NOW.getTime() - (60 * 60_000 - 1)).toISOString())).toBe('59m ago')
    })

    it('exactly 60m crosses into "1h ago"', () => {
      expect(formatRelative(new Date(NOW.getTime() - 60 * 60_000).toISOString())).toBe('1h ago')
    })

    it('23h59m59.999s is still "23h ago" (just under the 24h boundary)', () => {
      expect(formatRelative(new Date(NOW.getTime() - (24 * 60 * 60_000 - 1)).toISOString())).toBe('23h ago')
    })

    it('exactly 24h crosses into "1d ago"', () => {
      expect(formatRelative(new Date(NOW.getTime() - 24 * 60 * 60_000).toISOString())).toBe('1d ago')
    })

    it('6d23h59m59.999s is still "6d ago" (just under the 7d boundary)', () => {
      expect(formatRelative(new Date(NOW.getTime() - (7 * 24 * 60 * 60_000 - 1)).toISOString())).toBe('6d ago')
    })

    it('exactly 7d crosses into the absolute short-date fallback', () => {
      const sevenDaysAgo = new Date(NOW.getTime() - 7 * 24 * 60 * 60_000)
      expect(formatRelative(sevenDaysAgo.toISOString())).toBe(
        sevenDaysAgo.toLocaleDateString(undefined, { month: 'short', day: 'numeric' }),
      )
    })

    it('a future date never renders a negative value — documents the current "just now" fallback', () => {
      // mins = floor((now - future)/60000) is negative for any future date,
      // and `mins < 1` is true for every negative number — so formatRelative
      // currently collapses ANY future timestamp (1 second or 10 days ahead)
      // to "just now" rather than surfacing a negative count. This test
      // documents that behavior so a future change to it is a deliberate,
      // reviewed decision rather than an accidental regression.
      const inTenDays = new Date(NOW.getTime() + 10 * 24 * 60 * 60_000)
      const result = formatRelative(inTenDays.toISOString())
      expect(result).toBe('just now')
      expect(result).not.toMatch(/-/)
    })
  })
})
