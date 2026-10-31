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

function renderIndicator(overrides: Partial<Parameters<typeof ProviderWaitingIndicator>[0]> = {}) {
  const props = {
    silentSince: SILENT_SINCE,
    sentAt: SENT_AT.toISOString(),
    receivedAt: SENT_AT.toISOString(), // live start: receipt == sent_at, elapsed 0
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

  it('with a known silence limit it says when the call would end (local short time of stall_at)', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderIndicator({ stallAt: STALL_AT })
    const expected = new Intl.DateTimeFormat(undefined, { timeStyle: 'short' }).format(new Date(STALL_AT))
    expect(container.textContent).toContain(`If nothing arrives by ${expected}, the call ends.`)
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
