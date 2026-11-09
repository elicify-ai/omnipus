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
 * Round 3 / CHECK F1: check the visible span and the hidden live region
 * independently. The count/end-time copy exists now; a truncating style
 * still hides the suffix at narrow widths. jsdom does not measure layout,
 * so the readability oracle explicitly rejects truncation on that span.
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

function waitingSurfaces(container: HTMLElement) {
  const live = container.querySelector('[data-testid="provider-waiting-indicator"] [role="status"]')
  const visible = container.querySelector<HTMLElement>(
    '[data-testid="provider-waiting-indicator"] > span:not([aria-hidden])',
  )
  expect(live, 'the hidden live region must exist').not.toBeNull()
  expect(visible, 'the visible sentence span must exist').not.toBeNull()
  return { live: live!, visible: visible! }
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
    const full = `The model hasn't responded for 45s — still waiting. If nothing arrives by ${expectedStallTime()}, this attempt ends (attempt 1 of 3).`
    // A whole-container text query merges the hidden announcement with
    // the sighted-user line. Neither surface may substitute for the other.
    const { live, visible } = waitingSurfaces(container)
    expect(live.textContent).toBe(full)
    expect(visible.textContent).toBe(full)
    expect(visible).toBeVisible()
  })

  it('the visible sentence does not use a truncating style — end time and count stay readable at a narrow chat width', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderWaiting(attemptProps(1, 3))
    const { visible } = waitingSurfaces(container)
    // CHECK F1: jsdom cannot establish rendered pixel width. Assert the
    // permitted no-truncation style oracle, not an invented layout result.
    expect(visible.className).not.toContain('truncate')
    expect(visible.className).not.toContain('line-clamp')
    expect(visible.className).not.toContain('ellipsis')
    const style = getComputedStyle(visible)
    expect(style.textOverflow).not.toBe('ellipsis')
    expect(style.whiteSpace).not.toBe('nowrap')
    expect(visible.textContent).toContain(`If nothing arrives by ${expectedStallTime()}`)
    expect(visible.textContent).toContain('(attempt 1 of 3)')
  })

  it('on the last attempt the whole call ends: "the call ends (attempt 3 of 3)"', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderWaiting(attemptProps(3, 3))
    const full = `The model hasn't responded for 45s — still waiting. If nothing arrives by ${expectedStallTime()}, the call ends (attempt 3 of 3).`
    const { live, visible } = waitingSurfaces(container)
    expect(visible.textContent).toBe(full)
    expect(live.textContent).toBe(full)
  })

  it('never says "contact support" (B4)', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    for (const [attempt, maxAttempts] of [
      [1, 3],
      [3, 3],
    ] as const) {
      const { container } = renderWaiting(attemptProps(attempt, maxAttempts))
      const { live, visible } = waitingSurfaces(container)
      expect(visible.textContent).not.toContain('contact support')
      expect(live.textContent).not.toContain('contact support')
    }
  })
})
