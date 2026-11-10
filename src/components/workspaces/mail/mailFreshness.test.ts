// W3 RED pack — U11–U13 (spec §8.3, file name per spec §8.3's table).
//
// Oracle source: spec §4 US-2 (exact strings), §11 states S-3/S-4/S-5/S-25,
// §7 scenarios 1.1/2.1/2.5/2.6, dataset D-1, U12's vocabulary row, and the
// F6 "No date" rule (§2.1 mail-format row + §8.3 U13). Every expected string
// below was derived from the spec BEFORE the formatter was read
// (receipts/w3-red-derivation.md). Units are the real formatters — pure
// functions, no mocks (no I/O edge exists).
import { describe, it, expect } from 'vitest'
import {
  formatMailTime,
  formatMailDate,
  formatMailRelativeAge,
  formatMailFreshnessLine,
  formatMailRefreshFailedLine,
} from './mail-format'

const NOW = new Date('2026-10-02T10:00:00Z')
const iso = (msBeforeNow: number): string => new Date(NOW.getTime() - msBeforeNow).toISOString()

const freshness = (over: {
  source?: 'live' | 'memory' | 'encrypted_disk' | 'none'
  last_validated_at?: string | null
  stale?: boolean
  refresh_needed?: boolean
}) => ({
  source: 'memory' as const,
  last_validated_at: iso(40_000),
  stale: false,
  refresh_needed: false,
  ...over,
})

describe('mailFreshness — the US-2 label pins (U11–U13)', () => {
  describe('U11 — the four sources label correctly (D-1 matrix, scenarios 2.1/2.5/2.6)', () => {
    it('D-1 row 1: source=live, validated now → "Checked just now" (S-3)', () => {
      expect(
        formatMailFreshnessLine(freshness({ source: 'live', last_validated_at: NOW.toISOString() }), false, NOW),
      ).toBe('Checked just now')
    })

    it('D-1 row 2: source=memory, −2 min, fresh → "Checked 2 minutes ago" (S-4)', () => {
      expect(
        formatMailFreshnessLine(freshness({ source: 'memory', last_validated_at: iso(2 * 60_000) }), false, NOW),
      ).toBe('Checked 2 minutes ago')
    })

    it('D-1 row 3: stale and checking → "Last checked 7 minutes ago · Checking…" (S-5, scenario 2.1)', () => {
      expect(
        formatMailFreshnessLine(
          freshness({ source: 'memory', last_validated_at: iso(7 * 60_000), stale: true, refresh_needed: true }),
          true,
          NOW,
        ),
      ).toBe('Last checked 7 minutes ago · Checking…')
    })

    it('scenario 2.5’s pin: unknown time with a refresh in flight → "Last checked unknown · Checking…"', () => {
      expect(
        formatMailFreshnessLine(
          freshness({ source: 'memory', last_validated_at: null, refresh_needed: true }),
          true,
          NOW,
        ),
      ).toBe('Last checked unknown · Checking…')
    })

    it('scenario 2.6’s pin: unknown time, no refresh in flight → "Last checked unknown." with no Checking…', () => {
      const line = formatMailFreshnessLine(
        freshness({ source: 'live', last_validated_at: null, stale: false, refresh_needed: false }),
        false,
        NOW,
      )
      expect(line).toBe('Last checked unknown.')
      expect(line).not.toContain('Checking')
    })

    it('a null last-validated time NEVER presents as "just checked" (US-2 AS-7)', () => {
      for (const source of ['live', 'memory', 'encrypted_disk'] as const) {
        const line = formatMailFreshnessLine(freshness({ source, last_validated_at: null }), false, NOW)
        expect(line).toBe('Last checked unknown.')
        expect(line.toLowerCase()).not.toContain('just now')
      }
    })

    it('D-1 row 4’s vocabulary: encrypted_disk labels the validated age like any source, never implying a live check (Phase 1: the mapping-metadata surface)', () => {
      const line = formatMailFreshnessLine(
        freshness({ source: 'encrypted_disk', last_validated_at: iso(26 * 60 * 60_000), stale: true, refresh_needed: true }),
        false,
        NOW,
      )
      // The spec pins the vocabulary, not the rounding at the hours/days
      // boundary (U12 pins 30 s…2 d only): the age slot must be the same
      // relative vocabulary, never a live-check claim and never "Checking…".
      expect(line.startsWith('Checked ')).toBe(true)
      expect(line).toBe(`Checked ${formatMailRelativeAge(iso(26 * 60 * 60_000), NOW)}`)
      expect(line.toLowerCase()).not.toContain('live')
      expect(line).not.toContain('Checking')
    })
  })

  describe('U12 — the relative-time vocabulary is stable and honest', () => {
    const cases: Array<[number, string]> = [
      [30_000, '30 seconds ago'],
      [40_000, '40 seconds ago'], // scenario 1.1’s pinned example
      [2 * 60_000, '2 minutes ago'],
      [7 * 60_000, '7 minutes ago'],
      [3 * 60 * 60_000, '3 hours ago'],
      [2 * 24 * 60 * 60_000, '2 days ago'],
    ]
    it.each(cases)('an age of %d ms labels "%s"', (ms, expected) => {
      expect(formatMailRelativeAge(iso(ms), NOW)).toBe(expected)
    })

    it('a null/unparseable validation time fills the vocabulary’s "unknown" slot — never a zero or "just now"', () => {
      expect(formatMailRelativeAge(null, NOW)).toBe('unknown')
      expect(formatMailRelativeAge('not-a-date', NOW)).toBe('unknown')
      expect(formatMailRelativeAge('0001-01-01T00:00:00Z', NOW)).toBe('unknown')
    })
  })

  describe('U13 — the F6 "No date" rule (null → "No date"; year-one/zero → "No date"; never "1 Jan 1", never blank)', () => {
    // The null leg: the landed wire `date` is still non-nullable pending the
    // scheduled amendment (spec §2.3/§2.4), but the formatter contract is
    // testable now and the amendment must not change it.
    it('a null date renders "No date" in both formatters (F6, post-amendment leg)', () => {
      expect(formatMailTime(null)).toBe('No date')
      expect(formatMailDate(null)).toBe('No date')
    })

    // The year-one/zero leg: the Go zero time (year one) is the spec's
    // "year-one/zero" value — §2.1: "null → No date; year-one/zero → No
    // date; never blank and never '1 Jan 1'". (The implementation cites a
    // conflicting F8 pin from the drafts era; W3 §2.1 + §8.3 U13 are the
    // oracle for this pack, and the disagreement is reported, not resolved
    // in the code's favour.)
    it('the year-one/zero date renders "No date" — never blank, never "1 Jan 1" (F6)', () => {
      expect(formatMailTime('0001-01-01T00:00:00Z')).toBe('No date')
      expect(formatMailDate('0001-01-01T00:00:00Z')).toBe('No date')
    })

    it('no surface renders the raw year-one string', () => {
      expect(formatMailTime('0001-01-01T00:00:00Z')).not.toMatch(/1 Jan 1/)
      expect(formatMailDate('0001-01-01T00:00:00Z')).not.toMatch(/1 Jan 1/)
    })

    it('a real date keeps its date — the rule never erases a genuine value', () => {
      // The spec pins the day-month-year SHAPE (the format docstring's
      // "2 Jan 2006" style), never a month-abbreviation spelling, which is
      // ICU-dependent. The property: a real date renders dated, day-first
      // with a year, and is never blank and never "No date".
      const rendered = formatMailDate('2026-09-28T10:00:00Z')
      expect(rendered).toMatch(/^\d{1,2} \w+ \d{4}$/)
      expect(rendered).not.toBe('No date')
      expect(rendered).not.toBe('')
    })

    it('the failed-refresh line keeps the ORIGINAL checked time (US-2 AS-4: timestamps never advance on failure)', () => {
      const original = '2026-10-02T09:53:00Z'
      const line = formatMailRefreshFailedLine(original)
      expect(line).toBe(`Couldn't refresh — showing messages as of ${formatMailTime(original)}.`)
      expect(line).toContain('Couldn’t refresh — showing messages as of'.replace('’', "'"))
    })

    it('the failed-refresh line with an unknown original time stays honest (never a fabricated time)', () => {
      expect(formatMailRefreshFailedLine(null)).toBe("Couldn't refresh — showing messages as of an unknown time.")
    })
  })
})
