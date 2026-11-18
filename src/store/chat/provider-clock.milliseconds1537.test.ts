// #1537 chief Q1 A / FR-008. Only real reducers, ChatScreen and indicator.
// Existing provider-clock.reattach.test.ts absent-field expectations stay intact.
import { beforeEach, afterEach, expect, it, vi } from 'vitest'
import { act } from '@testing-library/react'
import { reset, feed, retry, active, mount, lines } from '../../../tests/helper-model-notices/harness'

const SERVER = Date.parse('2026-11-15T12:00:00Z')
beforeEach(() => { reset(); vi.useFakeTimers(); vi.setSystemTime(new Date('2036-01-01T00:00:00Z')) })
afterEach(() => { reset(); vi.unstubAllGlobals(); vi.useRealTimers() })

it.each([
  { offset: 900, countdown: '0:29', next: '0:28' }, //30-.9=29.1 ->29 (not ceil); whole-second sample would read30.
  { offset: 400, countdown: '0:30', next: '0:29' }, //30-.4=29.6 ->30 (not floor).
])('FR-008 restored own retry prefers emitted_at_ms+$offset and reads$countdown', ({ offset, countdown, next }) => {
  // session_state is received before the older projection frame. A stale
  // retry sent_at must not beat the precise snapshot sample. Clock2036 !=2026.
  feed({ ...active('own-turn', '2026-11-15T12:00:00Z'), emitted_at_ms: SERVER + offset },
    retry('', 'own-turn', { sent_at: '2026-11-15T11:59:55.000Z' }))
  const { container } = mount()
  expect(lines(container, 'Retrying automatically').map(el => el.textContent?.trim()),
    `FR-008 ms exact visible text: S+${offset}ms ->${countdown}; stale sent_at must not win`).toEqual([
    `openrouter is rate-limiting requests right now. Retrying automatically in ${countdown} (attempt 2 of 6).`,
  ])
  expect(lines(container, `Retrying automatically in ${countdown} (attempt 2 of 6).`),
    `FR-008 ms preference: S+${offset}ms ->${countdown}; ignore/truncate must not pass`).toHaveLength(1)
  act(() => vi.advanceTimersByTime(1000))
  expect(lines(container, `Retrying automatically in ${next} (attempt 2 of 6).`),
    'FR-008 precise server sample advances by client elapsed, independent of wall-clock skew').toHaveLength(1)
})
