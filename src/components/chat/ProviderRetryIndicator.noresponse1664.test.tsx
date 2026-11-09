/**
 * ProviderRetryIndicator.noresponse1664.test.tsx — issue #1664 B5 (founder
 * decisions 2026-11-07, Q2) and Q4 A (founder decision 2026-11-08).
 *
 * B5: a header timeout (or a stall with nothing streamed yet) shows its own
 * cause and count — "<model> did not start answering in time. Retrying
 * automatically in 0:02 (attempt 2 of 3)." The subject is the MODEL, as the
 * approved sentence is written (CHECK F3: the provider-for-model
 * substitution was never approved — round 2 expected the provider; that
 * expectation is the one authorized replacement this round).
 *
 * Q4 A: the empty_reply retry line reads exactly "{model} sent an empty
 * reply. Retrying automatically in 0:02 (attempt 2 of 3)." — live (the
 * aria-live announcement) and on screen (the visible span).
 *
 * Oracle: /workspace/logs/fix-1664/verify-report.md §B5 + the Q4 A decision
 * — never the component. Current defects: the line names the provider
 * instead of the model, and empty_reply has no cause-specific clause.
 */
import { describe, it, expect, vi, afterEach } from 'vitest'
import { act, render } from '@testing-library/react'
import type { ProviderRetryFrame } from '@/lib/api/generated/asyncapi-types'
import { ProviderRetryIndicator, retryProblemClause } from './ProviderRetryIndicator'

// Server wall clock at emission (sent_at), fixed: all derivations are exact.
const SENT_AT = new Date('2026-11-07T14:00:30.000Z')
// The next attempt fires 2 s after the frame was sent (the backoff's end).
const RETRY_AT = new Date(SENT_AT.getTime() + 2_000).toISOString()

// Distinct values on purpose (CHECK F3): the approved sentences name the
// MODEL; a test whose provider and model agree cannot tell the difference.
const PROVIDER = 'OpenRouter'
const MODEL = 'model-a'

function renderRetry(overrides: Record<string, unknown> = {}) {
  const props = {
    provider: PROVIDER,
    model: MODEL,
    retryAt: RETRY_AT,
    sentAt: SENT_AT.toISOString(),
    receivedAt: SENT_AT.toISOString(), // live start: 2 s remain
    attempt: 2,
    maxAttempts: 3,
    errorCode: 'no_response',
    ...overrides,
  }
  return render(<ProviderRetryIndicator {...props} />)
}

/** The line's two delivery surfaces, queried apart (CHECK F1's lesson). */
function retrySurfaces(container: HTMLElement) {
  return {
    live: container.querySelector('[data-testid="provider-retry-indicator"] [role="status"]'),
    visible: container.querySelector(
      '[data-testid="provider-retry-indicator"] > span:not([aria-hidden])',
    ),
  }
}

afterEach(() => {
  vi.useRealTimers()
})

describe('ProviderRetryIndicator — the no_response cause (issue #1664 B5, founder Q2)', () => {
  it('maps error_code no_response to "did not start answering in time"', () => {
    expect(retryProblemClause('no_response')).toBe('did not start answering in time')
  })

  it('renders the full line with the countdown and the attempt count — subject is the model', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderRetry()
    const expected = `${MODEL} did not start answering in time. Retrying automatically in 0:02 (attempt 2 of 3).`
    const { live, visible } = retrySurfaces(container)
    expect(visible?.textContent).toBe(expected)
    expect(live?.textContent).toBe(expected)
  })
})

describe('ProviderRetryIndicator — the empty_reply cause (issue #1664 Q4 A, founder 2026-11-08)', () => {
  it('maps error_code empty_reply to "sent an empty reply"', () => {
    expect(retryProblemClause('empty_reply')).toBe('sent an empty reply')
  })

  it('reads exactly the Q4 A sentence — live in the announcement and on the screen', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderRetry({ errorCode: 'empty_reply' })
    const expected = `${MODEL} sent an empty reply. Retrying automatically in 0:02 (attempt 2 of 3).`
    const { live, visible } = retrySurfaces(container)
    expect(visible, 'the visible retry line must exist').not.toBeNull()
    expect(visible?.textContent).toBe(expected)
    expect(live?.textContent).toBe(expected)
  })

  it('takes the countdown and next-attempt numbers from the frame, not the approved example literals', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const frame: ProviderRetryFrame = {
      type: 'provider_retry',
      session_id: 'session-1664-empty-retry',
      turn_id: 'turn-1664-empty-retry',
      provider: PROVIDER,
      model: MODEL,
      sent_at: SENT_AT.toISOString(),
      retry_at: new Date(SENT_AT.getTime() + 7_000).toISOString(),
      retry_after_seconds: 7,
      attempt: 3,
      max_attempts: 3,
      error_code: 'empty_reply',
    }
    const { container } = renderRetry({
      provider: frame.provider,
      model: frame.model,
      sentAt: frame.sent_at,
      retryAt: frame.retry_at,
      attempt: frame.attempt,
      maxAttempts: frame.max_attempts,
      errorCode: frame.error_code,
    })
    const { live, visible } = retrySurfaces(container)
    const initial = 'model-a sent an empty reply. Retrying automatically in 0:07 (attempt 3 of 3).'
    expect(visible?.textContent).toBe(initial)
    expect(live?.textContent).toBe(initial)
    act(() => {
      vi.advanceTimersByTime(1_000)
    })
    expect(visible?.textContent).toBe(
      'model-a sent an empty reply. Retrying automatically in 0:06 (attempt 3 of 3).',
    )
  })
})
