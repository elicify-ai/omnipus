/**
 * ProviderWaitingIndicator.attempt1664.test.tsx — issue #1664 B4 (founder
 * decisions 2026-11-07, Q1 A + Q2): the waiting line names the attempt and
 * each attempt's end time from the FIRST notice —
 *   "The model hasn't responded for 45s — still waiting. If nothing arrives
 *    by {time}, this attempt ends (attempt 1 of 3)."
 * and on the last attempt the whole call ends:
 *   "… If nothing arrives by {time}, the call ends (attempt 3 of 3)."
 * It never says "contact support". Oracles from
 * /workspace/logs/fix-1664/verify-report.md §B4 — never from the component.
 *
 * The attempt props arrive with the B3 contract fields; the component does
 * not declare them yet, so they are passed shape-compatibly (a Record
 * spread) — this file typechecks before and after the fix. RED today: the
 * component has no attempt clause.
 */
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render } from '@testing-library/react'
import { ProviderWaitingIndicator } from './ProviderWaitingIndicator'

// Server wall clock at emission (sent_at), fixed: all derivations are exact.
const SENT_AT = new Date('2026-11-07T14:00:00.000Z')
// The provider went silent 45 s before the frame was sent.
const SILENT_SINCE = new Date(SENT_AT.getTime() - 45_000).toISOString()
// The attempt's configured guard ends it 7 s after silence began (B1); the
// displayed time is the viewer's local short time of that instant.
const STALL_AT = new Date(SENT_AT.getTime() - 45_000 + 7_000).toISOString()

const attemptProps = (attempt: number, maxAttempts: number): Record<string, unknown> => ({
  attempt,
  maxAttempts,
})

function renderWaiting(overrides: Record<string, unknown> = {}) {
  const props = {
    silentSince: SILENT_SINCE,
    sentAt: SENT_AT.toISOString(),
    receivedAt: SENT_AT.toISOString(), // live start: receipt == sent_at, elapsed 45 s
    stallAt: STALL_AT,
    ...overrides,
  }
  return render(<ProviderWaitingIndicator {...props} />)
}

const expectedStallTime = () =>
  new Intl.DateTimeFormat(undefined, { timeStyle: 'short' }).format(new Date(STALL_AT))

afterEach(() => {
  vi.useRealTimers()
})

describe('ProviderWaitingIndicator — the attempt clause (issue #1664 B4, founder Q2)', () => {
  it('announces the full line: elapsed, end time and "this attempt ends (attempt 1 of 3)"', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderWaiting(attemptProps(1, 3))
    expect(container.textContent).toContain(
      `The model hasn't responded for 45s — still waiting. If nothing arrives by ${expectedStallTime()}, this attempt ends (attempt 1 of 3).`,
    )
  })

  it('on the last attempt the whole call ends: "the call ends (attempt 3 of 3)"', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderWaiting(attemptProps(3, 3))
    expect(container.textContent).toContain(
      `The model hasn't responded for 45s — still waiting. If nothing arrives by ${expectedStallTime()}, the call ends (attempt 3 of 3).`,
    )
  })

  it('never says "contact support" (B4)', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    for (const [attempt, maxAttempts] of [
      [1, 3],
      [3, 3],
    ] as const) {
      const { container } = renderWaiting(attemptProps(attempt, maxAttempts))
      expect(container.textContent).not.toContain('contact support')
    }
  })
})
