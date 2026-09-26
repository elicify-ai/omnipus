/**
 * ProviderRetryIndicator.test.tsx — provider-messages spec RED tests:
 * TDD rows 17, 25, 34 (§8 item 2 state table, D3 rows).
 *
 * Oracles come from the SPEC ONLY:
 *   - Waiting line (D3 row 1 / §10 A-2): "OpenRouter is busy. Retrying
 *     automatically in 2:00 (attempt 2 of 3)."
 *   - Same-provider model switch (D3 row 9 / MIN-104 / row 34): "OpenRouter
 *     (model-b) is busy. Retrying automatically in mm:ss (attempt 2 of 3)."
 *     — qualifier only when the chain moves within ONE provider; different
 *     provider → plain provider name, no "(model)".
 *   - "of {max}" from the frame's max_attempts (MIN-104), never hard-coded.
 *   - retrying-now state: "Retrying now…", NO success colour at 0 (the
 *     own-limiter's "cleared" meaning is wrong here) — §8 item 2 state table.
 *   - No dismiss control: the indicator clears itself on the next frame;
 *     Stop is the only early exit (§8 item 2; ADR-082: only explicit stop
 *     ends a turn early). The stopped-by-user/terminal halves of the four
 *     states are PARENT-level (indicator removed / replaced by the terminal
 *     error line) — covered by the loop rows (C-12) and the error-frame path,
 *     not by a component prop.
 *   - Live-region contract (MAJ-016): the aria-live region announces the
 *     full PM-1 line once per attempt start (and once terminal); the
 *     per-second ticking mm:ss sits OUTSIDE the live region — a 1 s tick
 *     must NOT change the live-region text.
 *   - Countdown = retry_at − serverNow clamped ≥ 0 (C-11/MAJ-108), where
 *     serverNow = sent_at + client-elapsed-since-receipt — skew-immune by
 *     construction. Mid-wait attach (row 25 / §10 scenario 8): a tab
 *     attaching 100 s into a 120 s wait shows ~20 s remaining; with the
 *     client clock 30 s fast the shown remaining is still ~20 s (±2 s).
 *     Under vi fake timers + setSystemTime the derivation is exact, so the
 *     ±2 s real-world tolerance collapses to exact strings.
 *
 * COMPILE-RED: src/components/chat/ProviderRetryIndicator.tsx does not exist
 * yet (Wave 3 GREEN). The test imports the spec-named component with a
 * fact-only props contract (every prop is a provider_retry frame fact or the
 * client-clock receipt time the C-11 derivation needs) so GREEN has an
 * unambiguous target. The success-colour token asserted absent is the one
 * RateLimitIndicator uses (--color-success).
 */

import { describe, it, expect, vi, afterEach } from 'vitest'
import { render } from '@testing-library/react'
import { ProviderRetryIndicator } from './ProviderRetryIndicator'

// ── Fixtures ────────────────────────────────────────────────────────────────

// Server wall clock at emission (sent_at), fixed: all derivations are exact.
const SENT_AT = new Date('2026-09-26T12:00:00.000Z')
const RETRY_AT_120 = new Date(SENT_AT.getTime() + 120_000).toISOString()

function renderIndicator(overrides: Partial<Parameters<typeof ProviderRetryIndicator>[0]> = {}) {
  const props = {
    provider: 'OpenRouter',
    model: 'model-b',
    retryAt: RETRY_AT_120,
    sentAt: SENT_AT.toISOString(),
    receivedAt: SENT_AT.toISOString(), // live start: receipt == sent_at, elapsed 0
    attempt: 2,
    maxAttempts: 3,
    ...overrides,
  }
  return render(<ProviderRetryIndicator {...props} />)
}

afterEach(() => {
  vi.useRealTimers()
})

// ── Row 17: waiting state — the exact D3 row-1 line ─────────────────────────

describe('ProviderRetryIndicator — waiting state (row 17, D3 row 1)', () => {
  it('renders the PM-1 line with mm:ss from retry_at − serverNow (clamped ≥ 0), attempt 2 of 3', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT) // client clock == sent_at → serverNow == sent_at → 120 s left
    const { container } = renderIndicator()
    expect(container.textContent).toContain(
      'OpenRouter is busy. Retrying automatically in 2:00 (attempt 2 of 3).',
    )
  })

  it('ticks visually each second (the mm:ss text updates)', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderIndicator()
    expect(container.textContent).toContain('2:00')
    vi.advanceTimersByTime(1000)
    expect(container.textContent).toContain('1:59')
  })

  it('MIN-104: "of {max}" comes from the frame fact, never hard-coded "3"', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderIndicator({ maxAttempts: 5 })
    expect(container.textContent).toContain('(attempt 2 of 5)')
  })

  it('MIN-104/row 34: same-provider model switch qualifies the line with the retry model', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderIndicator({
      previousProvider: 'OpenRouter',
      previousModel: 'model-a',
      model: 'model-b',
    })
    expect(container.textContent).toContain(
      'OpenRouter (model-b) is busy. Retrying automatically in 2:00 (attempt 2 of 3).',
    )
  })

  it('MIN-104/row 34: a different provider renders the plain provider name — no "(model)" qualifier', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderIndicator({
      previousProvider: 'Anthropic',
      previousModel: 'claude-x',
    })
    expect(container.textContent).toContain(
      'OpenRouter is busy. Retrying automatically in 2:00 (attempt 2 of 3).',
    )
    expect(container.textContent).not.toContain('(model-b)')
  })
})

// ── Row 17: retrying-now state — "Retrying now…", no success colour ─────────

describe('ProviderRetryIndicator — retrying-now state (row 17, §8 state table)', () => {
  it('renders "Retrying now…" once the countdown reaches 0', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderIndicator({ retryAt: SENT_AT.toISOString() })
    expect(container.textContent).toContain('Retrying now…')
    expect(container.textContent).not.toContain('Retrying automatically')
  })

  it('has NO success colour at 0 — the own-limiter "cleared" meaning is wrong here', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderIndicator({ retryAt: SENT_AT.toISOString() })
    // The own-limiter indicator paints --color-success when its countdown
    // hits 0; the retry indicator must never do that (§8 state table).
    expect(container.innerHTML).not.toContain('--color-success')
  })
})

// ── Row 17: no dismiss control (§8 item 2; ADR-082) ─────────────────────────

describe('ProviderRetryIndicator — no dismiss (row 17)', () => {
  it('renders no dismiss button — Stop is the only early exit', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderIndicator()
    expect(container.querySelectorAll('button')).toHaveLength(0)
  })
})

// ── Row 17: live-region contract (MAJ-016) ──────────────────────────────────

describe('ProviderRetryIndicator — live region (MAJ-016)', () => {
  it('announces the full PM-1 line in the live region, and a 1 s tick does NOT change it', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderIndicator()

    const live = container.querySelector('[aria-live]')
    expect(live).not.toBeNull()
    expect(live?.textContent).toContain(
      'OpenRouter is busy. Retrying automatically in 2:00 (attempt 2 of 3).',
    )

    // One second passes: the mm:ss ticks visually OUTSIDE the live region.
    vi.advanceTimersByTime(1000)
    expect(container.textContent).toContain('1:59')
    expect(live?.textContent).toContain('2:00') // announcement frozen at attempt start
    expect(live?.textContent).not.toContain('1:59')
  })
})

// ── Row 25: mid-wait attach + clock skew (C-11/MAJ-108) ─────────────────────

describe('ProviderRetryIndicator — mid-wait attach and skew (row 25, C-11/MAJ-108)', () => {
  it('a tab attaching 100 s into a 120 s wait renders ~20 s remaining, not 2:00', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(SENT_AT.getTime() + 100_000)) // attach at sent_at+100 s
    // receivedAt stays at the original receipt: serverNow = sent_at + 100 s.
    const { container } = renderIndicator({ receivedAt: SENT_AT.toISOString() })
    expect(container.textContent).toContain('0:20')
    expect(container.textContent).not.toContain('2:00')
  })

  it('with the client clock 30 s fast the remaining time is still ~20 s (±2 s)', () => {
    vi.useFakeTimers()
    // True elapsed 100 s; the fast client clock reads sent_at + 130 s. The
    // client clock was ALREADY 30 s fast at receipt, so the receipt reading is
    // sent_at + 30 s (the fast clock's own reading), not sent_at — the elapsed
    // derivation (clientNow − receivedAt) then cancels the skew exactly:
    // serverNow = sent_at + (130 s − 30 s) = sent_at + 100 s → 0:20.
    vi.setSystemTime(new Date(SENT_AT.getTime() + 130_000))
    const { container } = renderIndicator({ receivedAt: new Date(SENT_AT.getTime() + 30_000).toISOString() })
    expect(container.textContent).toContain('0:20')
    expect(container.textContent).not.toContain('2:00')
  })
})
