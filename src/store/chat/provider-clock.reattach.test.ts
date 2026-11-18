// #1537 order 5b. Clock oracle: spec FR-008, 30s retry reattached at s5
// reads 25s. Client wall clock deliberately bears no relation to server time.
import { beforeEach, afterEach, expect, it, vi } from 'vitest'
import { act } from '@testing-library/react'
import { reset, feed, retry, status, active, mount, lines, named } from '../../../tests/helper-model-notices/harness'

beforeEach(() => { reset(); vi.useFakeTimers(); vi.setSystemTime(new Date('2036-01-01T00:00:00Z')) })
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers() })

it.each(['own', 'helper'])('reattached %s retry uses fresh server time, not old sent_at', whose => {
  feed(active('own-turn', '2026-11-15T12:00:05.000Z'))
  feed(retry(whose === 'own' ? '' : 'child-a', whose === 'own' ? 'own-turn' : 'turn-a'))
  const { container } = mount()
  expect(lines(container, 'Retrying automatically in 0:25 (attempt 2 of 6).'), 'FR-008: s30 minus s5 = 25 seconds').toHaveLength(1)
  act(() => vi.advanceTimersByTime(1000))
  expect(lines(container, 'Retrying automatically in 0:24 (attempt 2 of 6).')).toHaveLength(1)
  feed({ ...active('own-turn', '2026-11-15T12:00:01.000Z') })
  expect(lines(container, 'Retrying automatically in 0:24 (attempt 2 of 6).'), 'older received clock sample cannot go backwards').toHaveLength(1)
})

it.each(['own', 'helper'])('reattached %s silence uses server s35 minus original s0', whose => {
  feed(active('own-turn', '2026-11-15T12:00:35.000Z'))
  feed(status('waiting', whose === 'own' ? '' : 'child-a', 'own-turn', { silent_since: '2026-11-15T12:00:00.000Z', sent_at: '2026-11-15T12:00:30.000Z' }))
  const { container } = mount()
  expect(lines(container, "The model hasn't responded for 35s"), 'FR-008: silence age never restarts on attach').toHaveLength(1)
})

it('expired helper countdown stays at zero until a clear, never negative', () => {
  feed(active('own-turn', '2026-11-15T12:00:31.000Z'), retry())
  const { container } = mount()
  expect(named(container, 'Researcher', 'Retrying now')).toHaveLength(1)
  act(() => vi.advanceTimersByTime(5000))
  expect(named(container, 'Researcher', 'Retrying now')).toHaveLength(1)
  expect(container.textContent).not.toContain('-0:')
})
