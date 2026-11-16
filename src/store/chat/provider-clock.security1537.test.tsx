// #1537 Q12 A / S1: FR-008, and emitted_at_ms is the SAME instant as
// emitted_at. Reject unusable samples; keep the real store and chat rendering.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act } from '@testing-library/react'
import { active, bucket, feed, mount, named, reset, retry } from '../../../tests/helper-model-notices/harness'

const DATE_MAX = 8640000000000000 // Inclusive JavaScript Date limit; Q12 A boundary.
const SERVER = Date.parse('2026-11-15T12:00:00.000Z')
const CLIENT = Date.parse('2036-01-01T00:00:00.000Z') // Deliberately unrelated wall clock.
beforeEach(() => { reset(); vi.useFakeTimers(); vi.setSystemTime(CLIENT) })
afterEach(() => { reset(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers() })

it('S1 Date-limit-plus-one sample falls back to textual server time and the helper chat still renders', () => {
  feed({ ...active('own-turn', '2026-11-15T12:00:05.000Z'), emitted_at_ms: DATE_MAX + 1 }, retry())
  expect.soft(bucket().providerServerClock, 'S1: an unusable Date cannot be retained; textual s5 beats old retry sent_at s0').toEqual({ serverMs: SERVER + 5000, clientMs: CLIENT })
  let container!: HTMLElement
  expect(() => { ({ container } = mount()) }, 'S1: rendering the real chat after a bad clock sample must not throw Invalid time value').not.toThrow()
  const rows = named(container, 'Researcher', 'Retrying automatically')
  expect(rows.map(row => row.textContent), 'FR-008 S1: s30 deadline minus textual s5 sample is exactly 0:25').toEqual([
    'Researcher: openrouter is rate-limiting requests right now. Retrying automatically in 0:25 (attempt 2 of 6).',
  ])
  expect(rows[0], 'S1: the named retry is actually visible, not an announcement-only fallback').toBeVisible()
  act(() => vi.advanceTimersByTime(1000))
  expect(named(container, 'Researcher', 'Retrying automatically').map(row => row.textContent), 'FR-008 S1: fallback server time advances to s6, not the client2036 clock').toEqual([
    'Researcher: openrouter is rate-limiting requests right now. Retrying automatically in 0:24 (attempt 2 of 6).',
  ])
})

it('S1 largest valid Date is accepted as a store clock sample with matching textual time', () => {
  // A matching extended-year text avoids confusing Date validity with the
  // independent cross-field consistency rule. No year275760 UI is mounted.
  feed({ ...active('own-turn', '+275760-09-13T00:00:00.000Z'), emitted_at_ms: DATE_MAX })
  expect(bucket().providerServerClock, 'S1 inclusive boundary: reject >DateMax, not >=DateMax').toEqual({ serverMs: DATE_MAX, clientMs: CLIENT })
})

it('S1 ten-year inconsistent sample cannot poison a later normal sample or its helper countdown', () => {
  // No exact consistency tolerance is specified. Q12-S1-CONSISTENCY in the
  // plan recommends <=1s; ten YEARS is deliberately far outside any such bound.
  const future = Date.parse('2036-11-15T12:00:05.000Z')
  expect(new Date(future).getTime(), 'S1 instrument: the mismatched sample is Date-valid, not the max+1 case again').toBe(future)
  feed({ ...active('own-turn', '2026-11-15T12:00:05.000Z'), emitted_at_ms: future })
  act(() => vi.advanceTimersByTime(1000))
  feed({ ...active('own-turn', '2026-11-15T12:00:06.000Z'), emitted_at_ms: SERVER + 6000 }, retry())
  // FR-008 fixes the estimate, not which equivalent sample pair wins a tie.
  const clock = bucket().providerServerClock
  const offset = clock ? clock.serverMs - clock.clientMs : undefined
  expect.soft(offset, 'S1: the server-clock estimate equals the normal sample offset, regardless of equal-offset sample retention').toBe((SERVER + 6000) - (CLIENT + 1000))
  expect.soft(offset, 'S1: the ten-year sample must not survive as the server-clock estimate').not.toBe(future - CLIENT)
  const { container } = mount()
  expect(named(container, 'Researcher', '').map(row => row.textContent), 'FR-008 S1: normal server s6 leaves 0:24; a poisoned clock must not force Retrying now').toEqual([
    'Researcher: openrouter is rate-limiting requests right now. Retrying automatically in 0:24 (attempt 2 of 6).',
  ])
})
