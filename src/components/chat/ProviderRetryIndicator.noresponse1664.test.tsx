/**
 * ProviderRetryIndicator.noresponse1664.test.tsx — issue #1664 B5 (founder
 * decisions 2026-11-07, Q2): a header timeout (or a stall with nothing
 * streamed yet) shows its own cause and count —
 *   "OpenRouter did not start answering in time. Retrying automatically in
 *    0:02 (attempt 2 of 3)."
 * ("<model>" in the report's sentence is the display name the line already
 * renders.) Oracle: /workspace/logs/fix-1664/verify-report.md §B5 — never
 * the component. RED today: retryProblemClause has no no_response case, so
 * the generic clause renders instead.
 */
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render } from '@testing-library/react'
import { ProviderRetryIndicator, retryProblemClause } from './ProviderRetryIndicator'

// Server wall clock at emission (sent_at), fixed: all derivations are exact.
const SENT_AT = new Date('2026-11-07T14:00:30.000Z')
// The next attempt fires 2 s after the frame was sent (the backoff's end).
const RETRY_AT = new Date(SENT_AT.getTime() + 2_000).toISOString()

function renderRetry(overrides: Record<string, unknown> = {}) {
  const props = {
    provider: 'OpenRouter',
    model: 'model-a',
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

afterEach(() => {
  vi.useRealTimers()
})

describe('ProviderRetryIndicator — the no_response cause (issue #1664 B5, founder Q2)', () => {
  it('maps error_code no_response to "did not start answering in time"', () => {
    expect(retryProblemClause('no_response')).toBe('did not start answering in time')
  })

  it('renders the full line with the countdown and the attempt count', () => {
    vi.useFakeTimers()
    vi.setSystemTime(SENT_AT)
    const { container } = renderRetry()
    expect(container.textContent).toContain(
      'OpenRouter did not start answering in time. Retrying automatically in 0:02 (attempt 2 of 3).',
    )
  })
})
