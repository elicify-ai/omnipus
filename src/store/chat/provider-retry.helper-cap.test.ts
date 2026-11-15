// #1537 Q1 A, corrected fake-model dataset3: exactly120s starts at2:00,
// then recovered1. The Go real-producer test supplies the transport/policy
// evidence; this real store/ChatScreen test owns formatting and replacement.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { cleanup } from '@testing-library/react'
import { AT, active, bucket, feed, lines, mount, named, reset, retry, status } from '../../../tests/helper-model-notices/harness'

beforeEach(() => {
  reset()
  vi.useFakeTimers()
  vi.setSystemTime(new Date(AT))
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

it.each(['helper own', 'idle main named'] as const)('%s at-cap countdown starts at 2:00 and recovers once', whose => {
  const own = whose === 'helper own'
  const child = own ? '' : 'child-a'
  const turn = 'cap-helper-turn'
  if (own) feed(active(turn))
  // Row3 fixes120 at the policy cap. AT is s0, so deadline is s120.
  feed(retry(child, turn, { retry_after_seconds: 120, retry_at: '2026-11-15T12:02:00.000Z' }))
  if (own) {
    expect(bucket().messageOrder, 'own helper is active before the first streamed byte').toEqual([])
    expect(bucket().activeTurnId).toBe(turn)
  }
  const { container } = mount()
  const sentence = 'openrouter is rate-limiting requests right now. Retrying automatically in 2:00 (attempt 2 of 6).'
  const rows = own ? lines(container, sentence) : named(container, 'Researcher', sentence)
  expect(rows, `dataset3: ${whose} displays the exact120s countdown at2:00`).toHaveLength(1)
  expect(rows[0]).toBeVisible()
  expect(rows[0].textContent?.trim(), 'dataset3: unchanged sentence, named prefix only on the top copy')
    .toBe(own ? sentence : `Researcher: ${sentence}`)

  feed(status('recovered', child, turn, { retries: 1 }))
  const recovered = 'Connection came back — the model answered after 1 retry.'
  const recoveryRows = own ? lines(container, recovered) : named(container, 'Researcher', recovered)
  expect(recoveryRows, `dataset3: ${whose} shows exactly one recovery after the120s retry`).toHaveLength(1)
  expect(recoveryRows[0]).toBeVisible()
  expect(recoveryRows[0].textContent?.trim()).toBe(own ? recovered : `Researcher: ${recovered}`)
  expect(lines(container, 'Retrying automatically'), 'dataset3: recovery replaces, never stacks with, the countdown').toHaveLength(0)
})
