/**
 * ProviderWaitingIndicator.test.tsx — #1478 SPA wording (t6 worker FE brief):
 * the `provider_status` "waiting" notice — the calm line the chat shows once
 * the model has said nothing for 30 seconds. Oracles from the user-facing
 * docs (docs/troubleshooting.md, "The model has gone quiet"):
 *   - "the model hasn't responded for … seconds, still waiting"
 *   - "When the model is streaming, the notice also says when the silence
 *     limit would end the call."
 *   - the notice disappears as soon as anything arrives again (the store's
 *     responding/recovered handling — frame-cases.provider-frames.test.ts).
 *
 * Countdown grammar mirrors ProviderRetryIndicator's C-11 skew correction:
 * serverNow = sent_at + (clientNow − receivedAt), never a direct clock
 * comparison. Live-region contract (MAJ-016): the full line is announced
 * ONCE per waiting frame; the per-second ticking "Ns" sits OUTSIDE the live
 * region so a 1-second tick never re-announces.
 *
 * COMPILE-RED: src/components/chat/ProviderWaitingIndicator.tsx does not
 * exist yet — this file pins the contract GREEN implements.
 */

import { describe, it, expect, vi, afterEach } from 'vitest'
import { render } from '@testing-library/react'
import { ProviderWaitingIndicator, waitingElapsedSeconds } from './ProviderWaitingIndicator'

// Server wall clock at emission (sent_at), fixed: all derivations are exact.
const SENT_AT = new Date('2026-09-26T12:00:00.000Z')
// The provider went silent 45 s before the frame was sent.
const SILENT_SINCE = new Date(SENT_AT.getTime() - 45_000).toISOString()
// The stream's silence limit would end the call 5 minutes after silence began.
const STALL_AT = new Date(SENT_AT.getTime() - 45_000 + 300_000).toISOString()

// Issue #1664 B3/B4 (founder Q1 A): the waiting frame carries attempt and
// max_attempts from the contract wave on, so every render below happens in
// the post-fix frame shape (attempt 1 of 3). The component's props do not
// declare the fields yet, so they are passed shape-compatibly (a Record
// spread) — the attempt clause's own expectations are the RED tests.
const attemptProps = (attempt: number, maxAttempts: number): Record<string, unknown> => ({
  attempt,
  maxAttempts,
})

function renderIndicator(overrides: Record<string, unknown> = {}) {
  const props = {
    silentSince: SILENT_SINCE,
    sentAt: SENT_AT.toISOString(),
    receivedAt: SENT_AT.toISOString(), // live start: receipt == sent_at, elapsed 0
    ...attemptProps(1, 3),
    ...overrides,
  }
  return render(<ProviderWaitingIndicator {...props} />)
}

afterEach(() => {
  vi.useRealTimers()
})

describe('ProviderWaitingIndicator — waiting line (docs/troubleshooting.md, "The model has gone quiet")', () => {
  it('renders "The model hasn\'t responded for 45s — still waiting." from the skew-corrected elapsed', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderIndicator()
    expect(container.textContent).toContain("The model hasn't responded for 45s — still waiting.")
  })

  it('ticks the seconds each second', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderIndicator()
    expect(container.textContent).toContain('45s')
    vi.advanceTimersByTime(1000)
    expect(container.textContent).toContain('46s')
  })

  it('a client clock 30 s fast shows the same elapsed (C-11: receipt anchor cancels the skew)', () => {
    vi.useFakeTimers()
    // True now is sent_at; the fast client reads sent_at + 30 s and its
    // receipt-time reading was also 30 s ahead — elapsed cancels to 45 s.
    vi.setSystemTime(new Date(SENT_AT.getTime() + 30_000))
    const { container } = renderIndicator({
      receivedAt: new Date(SENT_AT.getTime() + 30_000).toISOString(),
    })
    expect(container.textContent).toContain("The model hasn't responded for 45s — still waiting.")
  })

  // Issue #1664 B4 (founder Q2, 2026-11-07): the end-of-call clause names
  // the ATTEMPT and the count — "this attempt ends (attempt 1 of 3)"; the
  // whole-call wording ("the call ends (attempt 3 of 3)") belongs to the
  // last attempt only. Reconciled from the pre-#1664 expectation ("the call
  // ends.") — the founder approved the new wording, so the old oracle would
  // have died on green for the wrong reason.
  it('with a known silence limit it says when THIS attempt ends, with the count (local short time of stall_at)', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderIndicator({ stallAt: STALL_AT })
    const expected = new Intl.DateTimeFormat(undefined, { timeStyle: 'short' }).format(new Date(STALL_AT))
    expect(container.textContent).toContain(
      `If nothing arrives by ${expected}, this attempt ends (attempt 1 of 3).`,
    )
  })

  it('without a known silence limit it does not mention an end time', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderIndicator()
    expect(container.textContent).toContain("The model hasn't responded for 45s — still waiting.")
    expect(container.textContent).not.toContain('the call ends')
  })

  it('announces the full line once in the live region; a 1 s tick does NOT re-announce (MAJ-016)', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderIndicator()
    const live = container.querySelector('[aria-live]')
    expect(live).not.toBeNull()
    expect(live?.textContent).toContain("The model hasn't responded for 45s — still waiting.")
    vi.advanceTimersByTime(1000)
    expect(container.textContent).toContain('46s')
    expect(live?.textContent).toContain('45s')
  })

  it('renders no dismiss button', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderIndicator()
    expect(container.querySelectorAll('button')).toHaveLength(0)
  })
})

describe('waitingElapsedSeconds — C-11 derivation (unit)', () => {
  it('elapsed = serverNow − silent_since, clamped at 0', () => {
    expect(waitingElapsedSeconds(SILENT_SINCE, SENT_AT.toISOString(), SENT_AT.toISOString(), SENT_AT.getTime())).toBe(45)
  })

  it('clamps at 0 when silence has not started', () => {
    expect(
      waitingElapsedSeconds(SENT_AT.toISOString(), SENT_AT.toISOString(), SENT_AT.toISOString(), SENT_AT.getTime()),
    ).toBe(0)
  })

  it('an unparseable timestamp renders 0, never NaN', () => {
    expect(waitingElapsedSeconds('not-a-date', SENT_AT.toISOString(), SENT_AT.toISOString(), SENT_AT.getTime())).toBe(0)
    expect(waitingElapsedSeconds(SILENT_SINCE, 'not-a-date', SENT_AT.toISOString(), SENT_AT.getTime())).toBe(0)
  })
})
