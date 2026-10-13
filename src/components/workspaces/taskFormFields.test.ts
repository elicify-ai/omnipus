import { describe, it, expect } from 'vitest'
import {
  buildTrigger,
  toDatetimeLocalValue,
  datetimeLocalToMs,
  datetimeLocalToIso,
  triggerSummary,
} from './taskFormFields'

describe('buildTrigger', () => {
  it('builds a manual trigger with an empty config', () => {
    expect(buildTrigger('manual', {})).toEqual({ type: 'manual', config: {} })
  })

  it('builds a once trigger carrying at_ms', () => {
    expect(buildTrigger('once', { at_ms: 1_700_000_000_000 })).toEqual({
      type: 'once',
      config: { at_ms: 1_700_000_000_000 },
    })
  })

  it('keeps at_ms === 0 (epoch) since only null/undefined are dropped', () => {
    expect(buildTrigger('once', { at_ms: 0 })).toEqual({ type: 'once', config: { at_ms: 0 } })
  })

  it('drops undefined / null / empty-string config keys', () => {
    expect(buildTrigger('once', { at_ms: undefined })).toEqual({ type: 'once', config: {} })
    expect(buildTrigger('recurring', {})).toEqual({ type: 'recurring', config: {} })
  })
})

describe('toDatetimeLocalValue', () => {
  it('returns "" for null / undefined / empty', () => {
    expect(toDatetimeLocalValue(undefined)).toBe('')
    expect(toDatetimeLocalValue(null)).toBe('')
    expect(toDatetimeLocalValue('')).toBe('')
  })

  it('returns "" for an invalid / NaN input', () => {
    expect(toDatetimeLocalValue(Number.NaN)).toBe('')
    expect(toDatetimeLocalValue('not-a-date')).toBe('')
  })

  it('formats an epoch-ms instant as a local YYYY-MM-DDTHH:mm value', () => {
    // Pick a fixed instant and confirm the round-trip back to ms matches.
    const ms = new Date('2026-08-15T17:00:00Z').getTime()
    const local = toDatetimeLocalValue(ms)
    expect(local).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/)
    // datetimeLocalToMs parses the produced local value back to the same instant.
    expect(datetimeLocalToMs(local)).toBe(ms)
  })

  it('formats an RFC3339 string identically to the equivalent epoch-ms', () => {
    const iso = '2026-08-15T17:00:00Z'
    const ms = new Date(iso).getTime()
    expect(toDatetimeLocalValue(iso)).toBe(toDatetimeLocalValue(ms))
  })
})

describe('datetimeLocalToMs', () => {
  it('returns null for empty input', () => {
    expect(datetimeLocalToMs('')).toBeNull()
  })

  it('returns null for an invalid value', () => {
    expect(datetimeLocalToMs('garbage')).toBeNull()
  })

  it('round-trips through toDatetimeLocalValue', () => {
    const ms = new Date('2026-01-02T03:04:00Z').getTime()
    const local = toDatetimeLocalValue(ms)
    expect(datetimeLocalToMs(local)).toBe(ms)
  })
})

describe('datetimeLocalToIso', () => {
  it('returns null for empty input', () => {
    expect(datetimeLocalToIso('')).toBeNull()
  })

  it('returns null for an invalid value', () => {
    expect(datetimeLocalToIso('garbage')).toBeNull()
  })

  it('round-trips local → iso → ms against datetimeLocalToMs', () => {
    const local = toDatetimeLocalValue(new Date('2026-03-04T05:06:00Z').getTime())
    const iso = datetimeLocalToIso(local)
    expect(iso).not.toBeNull()
    expect(new Date(iso as string).getTime()).toBe(datetimeLocalToMs(local))
  })
})

describe('triggerSummary', () => {
  it('describes a missing / null trigger as manual', () => {
    expect(triggerSummary(undefined)).toBe('Manual (drag to run)')
    expect(triggerSummary(null)).toBe('Manual (drag to run)')
    expect(triggerSummary({ type: 'manual', config: {} })).toBe('Manual (drag to run)')
  })

  it('describes a once trigger with its time, and the unset case', () => {
    const at = new Date('2026-08-15T17:00:00Z').getTime()
    expect(triggerSummary({ type: 'once', config: { at_ms: at } })).toBe(
      `Once — ${new Date(at).toLocaleString()}`,
    )
    expect(triggerSummary({ type: 'once', config: {} })).toBe('Once — (time unset)')
  })

  it('describes a recurring trigger without surfacing any rule string', () => {
    expect(triggerSummary({ type: 'recurring', config: { rrule: 'FREQ=WEEKLY;BYDAY=MO', dtstart_ms: 1, tz: 'UTC' } })).toBe('Recurring')
    expect(triggerSummary({ type: 'recurring', config: {} })).toBe('Recurring')
  })
})
