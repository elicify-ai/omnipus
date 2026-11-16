// #1537 FR-008 / chief Q1 A. Nearest second, positive half ties UP:
// 29.5->30 and0.5->1; <0.5 may show Retrying now, never >0.5 early.
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render } from '@testing-library/react'
import { ProviderRetryIndicator, remainingRetrySeconds } from './ProviderRetryIndicator'

const SERVER = Date.parse('2026-11-15T12:00:00Z')
const CLIENT = Date.parse('2036-01-01T00:00:00Z')
afterEach(() => { cleanup(); vi.useRealTimers() })

it('FR-008 nearest-second arithmetic preserves fractions, clamps zero and rounds positive x.5 up', () => {
  // Exact independent specification arithmetic, not Math.round duplicated in
  // the oracle. Both sides of the half-second boundary are explicit.
  for (const [remainingMs, expected] of [[-1, 0], [0, 0], [499, 0], [500, 1], [501, 1],
    [29100, 29], [29499, 29], [29500, 30], [29600, 30]]) {
    expect.soft(remainingRetrySeconds(new Date(SERVER + remainingMs).toISOString(),
      new Date(SERVER).toISOString(), new Date(CLIENT).toISOString(), CLIENT),
    `FR-008 nearest: ${remainingMs}ms remaining yields ${expected}s (half-up tie)`).toBe(expected)
  }
})

function indicator(remainingMs: number) {
  vi.useFakeTimers(); vi.setSystemTime(CLIENT)
  return render(<ProviderRetryIndicator provider="openrouter" model="model-a" retryAt={new Date(SERVER + remainingMs).toISOString()}
    sentAt={new Date(SERVER).toISOString()} receivedAt={new Date(CLIENT).toISOString()} attempt={2} maxAttempts={6} errorCode="rate_limited" />)
}

it('FR-008 visible own retry with29.1 seconds remaining reads0:29, not ceil0:30', () => {
  const { getByTestId } = indicator(29100)
  const row = getByTestId('provider-retry-indicator')
  expect(row, 'FR-00829.1s remains a waiting countdown').toHaveAttribute('data-state', 'waiting')
  expect(row, 'FR-008 visible nearest countdown29.1s ->0:29').toHaveTextContent('Retrying automatically in 0:29 (attempt 2 of 6).')
  expect(row.querySelector('span:not([aria-hidden])'), 'FR-008 visible-only29.1s countdown, never just the hidden announcement').toHaveTextContent('Retrying automatically in 0:29 (attempt 2 of 6).')
  expect(row).not.toHaveTextContent('0:30')
})

it('FR-008 visible29.6 seconds rounds up and only the last half second may say Retrying now', () => {
  const first = indicator(29600)
  expect(first.getByTestId('provider-retry-indicator'), 'FR-008 visible nearest countdown29.6s ->0:30 (not floor)').toHaveTextContent('Retrying automatically in 0:30 (attempt 2 of 6).')
  expect(first.getByTestId('provider-retry-indicator').querySelector('span:not([aria-hidden])'), 'FR-008 visible-only29.6s countdown, never just the hidden announcement').toHaveTextContent('Retrying automatically in 0:30 (attempt 2 of 6).')
  first.unmount()
  const last = indicator(499)
  expect(last.getByTestId('provider-retry-indicator'), 'FR-008499ms -> Retrying now, within the permitted half-second early boundary').toHaveAttribute('data-state', 'retrying-now')
  expect(last.getByTestId('provider-retry-indicator')).toHaveTextContent('Retrying now…')
  expect(last.getByTestId('provider-retry-indicator').querySelector('span:not([aria-hidden])'), 'FR-008 visible-only last-half-second retrying state').toHaveTextContent('Retrying now…')
})
