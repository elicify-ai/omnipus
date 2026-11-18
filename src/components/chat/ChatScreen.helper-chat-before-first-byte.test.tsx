// #1537 order 6b / FR-004: active helper chat before any token, raw own copy.
import { beforeEach, afterEach, expect, it, vi } from 'vitest'
import { reset, feed, retry, status, active, mount, lines, bucket, AT } from '../../../tests/helper-model-notices/harness'

beforeEach(reset)
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers() })

it.each(['retry', 'waiting'])('helper active, no streamed byte: own %s notice is visible', kind => {
  feed(active('helper-turn'))
  feed(kind === 'retry' ? retry('', 'helper-turn') : status('waiting', '', 'helper-turn'))
  expect(bucket().messageOrder, 'positive control: nothing has streamed').toEqual([])
  expect(bucket().activeTurnId).toBe('helper-turn')
  const { container } = mount()
  const text = kind === 'retry'
    ? 'openrouter is rate-limiting requests right now. Retrying automatically in 0:30 (attempt 2 of 6).'
    : "The model hasn't responded for 30s — still waiting."
  const visible = lines(container, text)
  expect(visible, 'FR-004: waiting belongs in the helper chat before first byte').toHaveLength(1)
  expect(visible[0]).toBeVisible()
  expect(container.textContent).not.toContain('Researcher:')
})

it('own notices preserve the existing idle gate (FR-003)', () => {
  vi.useFakeTimers(); vi.setSystemTime(new Date(AT))
  feed(retry('', 'own-turn'))
  const { container } = mount()
  expect(lines(container, 'Retrying automatically')).toHaveLength(0)
})
