// #1537 Q12 A / S2: FR-008 never retries early; browser setTimeout has a
// signed32-bit maximum delay. Real indicator, fake process-edge clock only.
import { afterEach, expect, it, vi } from 'vitest'
import { act, cleanup, render } from '@testing-library/react'
import { ProviderRetryIndicator } from './ProviderRetryIndicator'

const TIMER_MAX = 2147483647 // MDN Window.setTimeout maximum; Q12 A.
const SERVER = Date.parse('2026-11-15T12:00:00.000Z')
const CLIENT = Date.parse('2036-01-01T00:00:00.000Z')
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers() })

it.each([
  ['maximum minus one', TIMER_MAX - 1, '35791:24', '35791:23', '35791:13'],
  ['maximum exactly', TIMER_MAX, '35791:24', '35791:23', '35791:13'],
  ['maximum plus one', TIMER_MAX + 1, '35791:24', '35791:23', '35791:13'],
  ['30 days', 30 * 24 * 60 * 60 * 1000, '43200:00', '43199:59', '43199:49'],
] as const)('S2 %s wait never requests an overflowing timer or retries during the first eleven seconds', (_label, waitMs, initial, afterOne, afterEleven) => {
  // Independent arithmetic: MAX/1000 rounds to2147484s =35791min24s;
  // thirty days =43200min. Remove1s/11s for the later exact visible digits.
  vi.useFakeTimers(); vi.setSystemTime(CLIENT)
  const timeouts = vi.spyOn(globalThis, 'setTimeout') // Delegates; does not replace production logic.
  const probe = setTimeout(() => {}, TIMER_MAX + 1)
  expect(timeouts.mock.calls.map(([, delay]) => delay), 'S2 instrument: the spy can see a raw overflow request before any normalization').toEqual([TIMER_MAX + 1])
  clearTimeout(probe); timeouts.mockClear() // Independent instrument probe is never fired.
  const { getByTestId } = render(<ProviderRetryIndicator provider="openrouter" model="model-a"
    retryAt={new Date(SERVER + waitMs).toISOString()} sentAt="2026-11-15T12:00:00.000Z"
    receivedAt="2036-01-01T00:00:00.000Z" attempt={2} maxAttempts={6} errorCode="rate_limited" />)
  const row = getByTestId('provider-retry-indicator')
  const assertSafeTimeouts = (phase: string) => {
    // Zero deadline timeouts is permitted for a deferred long wait; the
    // periodic clock can reevaluate it. Never require a particular fix strategy.
    for (const [, delay] of timeouts.mock.calls) expect(delay, `S2 ${phase}: requested delay must not exceed the browser maximum2147483647ms`).toBeLessThanOrEqual(TIMER_MAX)
  }
  const assertWaiting = (countdown: string, phase: string) => {
    expect(row, `S2 ${phase}: still waiting, never early Retrying now`).toHaveAttribute('data-state', 'waiting')
    expect(row.querySelector('span:not([aria-hidden])')?.textContent, `S2 ${phase}: exact visible long-wait countdown`).toBe(
      `openrouter is rate-limiting requests right now. Retrying automatically in ${countdown} (attempt 2 of 6).`)
    expect(row, `S2 ${phase}: no early retry announcement either`).not.toHaveTextContent('Retrying now…')
  }
  assertWaiting(initial, 'mount')
  // Stop unsafe-code RED before advancing: do not drive a known overflow
  // rearm loop. The elapsed checks run on safe controls and after GREEN.
  assertSafeTimeouts('mount')
  act(() => vi.advanceTimersByTime(1000))
  assertWaiting(afterOne, '+1s'); assertSafeTimeouts('+1s')
  act(() => vi.advanceTimersByTime(10000))
  assertWaiting(afterEleven, '+11s'); assertSafeTimeouts('+11s')
})
