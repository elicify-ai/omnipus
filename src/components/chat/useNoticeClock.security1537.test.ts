// #1537 Q12 A / S1: an unusable estimate must not throw from chat rendering.
// Pure real clockAnchor; no Date/function mock and no implementation mutation.
import { expect, it } from 'vitest'
import { clockAnchor } from './useNoticeClock'

const DATE_MAX = 8640000000000000 // Inclusive JavaScript Date limit, Q12 A.
const SERVER = Date.parse('2026-11-15T12:00:05.000Z')
const CLIENT = Date.parse('2036-01-01T00:00:00.000Z')
const own = { sentAt: '2026-11-15T12:00:00.000Z', receivedAt: '2036-01-01T00:00:00.000Z' }

it.each([
  ['server', { serverMs: DATE_MAX + 1, clientMs: CLIENT }],
  ['client', { serverMs: SERVER, clientMs: DATE_MAX + 1 }],
] as const)('S1 clockAnchor with an invalid %s Date returns the usable own pair without throwing', (_field, clock) => {
  expect(() => clockAnchor(clock, own), 'S1 clockAnchor: invalid Date must not escape as RangeError').not.toThrow()
  expect(clockAnchor(clock, own), 'S1 clockAnchor: unusable estimate falls back to the existing own anchor, never an invalid/mixed pair').toEqual(own)
})

it('S1 clockAnchor retains a valid precise server sample instead of always falling back', () => {
  expect(clockAnchor({ serverMs: SERVER, clientMs: CLIENT }, own), 'S1 positive control: valid s5 sample differs from own s0').toEqual({
    sentAt: '2026-11-15T12:00:05.000Z', receivedAt: '2036-01-01T00:00:00.000Z',
  })
})
