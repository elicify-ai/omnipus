// #1537 FR-008 / chief Q8 A: nearest-second digits, positive half ties UP,
// at least 0:01 for every positive wait; Retrying now starts at raw zero.
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render } from '@testing-library/react'
import { ProviderRetryIndicator, remainingRetrySeconds } from './ProviderRetryIndicator'

const SERVER = Date.parse('2026-11-15T12:00:00Z')
const CLIENT = Date.parse('2036-01-01T00:00:00Z')
afterEach(() => { cleanup(); vi.useRealTimers() })

it('FR-008 nearest-second arithmetic preserves fractions, clamps zero and rounds positive x.5 up', () => {
  // Exact independent specification arithmetic, not Math.round duplicated in
  // the oracle. Q8 keeps every positive wait at least one displayed second.
  for (const [remainingMs, expected] of [[-1, 0], [0, 0], [1, 1], [499, 1], [500, 1], [501, 1],
    [1400, 1], [1600, 2], [29100, 29], [29499, 29], [29500, 30], [29600, 30]]) {
    expect.soft(remainingRetrySeconds(new Date(SERVER + remainingMs).toISOString(),
      new Date(SERVER).toISOString(), new Date(CLIENT).toISOString(), CLIENT),
    `FR-008 nearest: ${remainingMs}ms remaining yields ${expected}s (Q8 positive floor; half-up tie)`).toBe(expected)
  }
})

function indicator(waitMs: number, errorCode = 'rate_limited', elapsedMs = 0) {
  vi.useFakeTimers(); vi.setSystemTime(CLIENT + elapsedMs)
  return render(<ProviderRetryIndicator provider="openrouter" model="model-a" retryAt={new Date(SERVER + waitMs).toISOString()}
    sentAt={new Date(SERVER).toISOString()} receivedAt={new Date(CLIENT).toISOString()} attempt={2} maxAttempts={6} errorCode={errorCode} />)
}

it('FR-008 visible own retry with29.1 seconds remaining reads0:29, not ceil0:30', () => {
  const { getByTestId } = indicator(29100)
  const row = getByTestId('provider-retry-indicator')
  expect(row, 'FR-00829.1s remains a waiting countdown').toHaveAttribute('data-state', 'waiting')
  expect(row, 'FR-008 visible nearest countdown29.1s ->0:29').toHaveTextContent('Retrying automatically in 0:29 (attempt 2 of 6).')
  expect(row.querySelector('span:not([aria-hidden])'), 'FR-008 visible-only29.1s countdown, never just the hidden announcement').toHaveTextContent('Retrying automatically in 0:29 (attempt 2 of 6).')
  expect(row).not.toHaveTextContent('0:30')
})

it('FR-008 visible29.6 seconds rounds up and499ms retains the cause sentence with0:01', () => {
  const first = indicator(29600)
  expect(first.getByTestId('provider-retry-indicator'), 'FR-008 visible nearest countdown29.6s ->0:30 (not floor)').toHaveTextContent('Retrying automatically in 0:30 (attempt 2 of 6).')
  expect(first.getByTestId('provider-retry-indicator').querySelector('span:not([aria-hidden])'), 'FR-008 visible-only29.6s countdown, never just the hidden announcement').toHaveTextContent('Retrying automatically in 0:30 (attempt 2 of 6).')
  first.unmount()
  const last = indicator(499)
  const row = last.getByTestId('provider-retry-indicator')
  expect.soft(row, 'FR-008 Q8 raw499ms is still waiting, never early retrying-now').toHaveAttribute('data-state', 'waiting')
  expect.soft(row, 'FR-008 Q8 raw499ms retains the cause and0:01').toHaveTextContent('openrouter is rate-limiting requests right now. Retrying automatically in 0:01 (attempt 2 of 6).')
  expect.soft(row.querySelector('span:not([aria-hidden])')?.textContent, 'FR-008 Q8 visible raw499ms cause sentence, not only the announcement').toBe('openrouter is rate-limiting requests right now. Retrying automatically in 0:01 (attempt 2 of 6).')
  expect.soft(row, 'FR-008 Q8 raw499ms must not say Retrying now').not.toHaveTextContent('Retrying now…')
})

function expectTimeoutWaiting(row: HTMLElement, countdown: string, boundary: string) {
  // US1-5/US2-9: the model did not start answering in time. Q8 preserves
  // that cause at every positive remaining value, not just an aria-live copy.
  expect(row.querySelector('span:not([aria-hidden])')?.textContent,
    `FR-008 Q8 ${boundary}: visible timeout cause and${countdown}`).toBe(
    `model-a did not start answering in time. Retrying automatically in ${countdown} (attempt 2 of 6).`)
  expect(row, `FR-008 Q8 ${boundary}: positive raw milliseconds stay waiting`).toHaveAttribute('data-state', 'waiting')
  expect(row, `FR-008 Q8 ${boundary}: never early Retrying now`).not.toHaveTextContent('Retrying now…')
}

it('FR-008 Q8 a500ms timeout wait keeps the cause and0:01 at receipt and with499ms still remaining', () => {
  const initial = indicator(500, 'no_response')
  expectTimeoutWaiting(initial.getByTestId('provider-retry-indicator'), '0:01', '500ms wait, raw500ms')
  initial.unmount()
  // Same deadline/receipt, 1 ms client elapsed. Remount to observe that exact
  // remaining value without imposing a 1 ms production timer-tick interval.
  const afterOneMs = indicator(500, 'no_response', 1)
  expectTimeoutWaiting(afterOneMs.getByTestId('provider-retry-indicator'), '0:01', '500ms wait, raw499ms')
})

it('FR-008 Q8 raw1ms remaining retains the visible timeout cause and0:01', () => {
  const { getByTestId } = indicator(1, 'no_response')
  expectTimeoutWaiting(getByTestId('provider-retry-indicator'), '0:01', 'raw1ms')
})

it('FR-008 Q8 raw0ms at the retry deadline shows Retrying now, not a waiting sentence', () => {
  const { getByTestId } = indicator(0, 'no_response')
  const row = getByTestId('provider-retry-indicator')
  expect(row.querySelector('span:not([aria-hidden])')?.textContent,
    'FR-008 Q8 raw0ms: visible Retrying now starts at the deadline').toBe('Retrying now…')
  expect(row, 'FR-008 Q8 raw0ms: reached deadline is retrying-now').toHaveAttribute('data-state', 'retrying-now')
  expect(row, 'FR-008 Q8 raw0ms: no positive countdown remains').not.toHaveTextContent('Retrying automatically')
})

it.each([[1400, '0:01'], [1600, '0:02'], [29100, '0:29'], [29600, '0:30']] as const)(
  'FR-008 Q8 raw%ims keeps nearest-second digits %s above one second', (remainingMs, countdown) => {
    const { getByTestId } = indicator(remainingMs, 'no_response')
    expectTimeoutWaiting(getByTestId('provider-retry-indicator'), countdown, `raw${remainingMs}ms nearest`)
  },
)
