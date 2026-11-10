// Session row words (FR-025/FR-026/FR-036). Restores the coverage the deleted
// Sidebar.lifecycle-labels tests gave: the exact lifecycle words, the stop
// cause wording, and "coarse status never drives the label".

import { describe, it, expect } from 'vitest'
import type { Session } from '@/lib/api'
import { sessionStatusLabel, sessionKindLabel } from './sessionLabels'

function session(overrides: Partial<Session>): Session {
  return {
    id: 's1', type: 'chat', agent_id: 'ava', title: 'T', status: 'active',
    created_at: '2026-10-10T00:00:00Z', updated_at: '2026-10-10T00:00:00Z',
    channel: 'webchat', partitions: [], stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
    ...overrides,
  } as Session
}

describe('sessionStatusLabel — lifecycle words', () => {
  it.each([
    ['working', 'Working', 'working'],
    ['waiting_for_answer', 'Waiting', 'waiting'],
    ['done', 'Done', 'done'],
    ['failed', 'Failed', 'failed'],
    ['interrupted', 'Interrupted', 'interrupted'],
  ] as const)('%s reads %s', (lifecycle_state, text, tone) => {
    expect(sessionStatusLabel(session({ lifecycle_state }))).toEqual({ text, tone })
  })

  it('Interrupted is its own tone, never the Failed one', () => {
    expect(sessionStatusLabel(session({ lifecycle_state: 'interrupted' })).tone).not.toBe('failed')
  })

  it('queued execution wins over a working lifecycle', () => {
    expect(sessionStatusLabel(session({ lifecycle_state: 'working', execution: 'queued' }))).toEqual({ text: 'Queued', tone: 'queued' })
  })

  it('coarse status never produces Working: no lifecycle means Unavailable, except archived which is Done', () => {
    expect(sessionStatusLabel(session({ status: 'active' }))).toEqual({ text: 'Unavailable', tone: 'unavailable' })
    expect(sessionStatusLabel(session({ status: 'archived' }))).toEqual({ text: 'Done', tone: 'done' })
  })
})

describe('sessionStatusLabel — stop cause wording', () => {
  const NOTE = { at: '2026-10-10T00:00:00Z', by: 'human:u1', seq: 1 }
  it('a stopped session without a note is just Stopped', () => {
    expect(sessionStatusLabel(session({ lifecycle_state: 'stopped' }))).toEqual({ text: 'Stopped', tone: 'stopped' })
  })
  it.each([
    ['stop', 'Stopped · stop'],
    ['redirect_pause', 'Stopped · redirect pause'],
    ['cascade', 'Stopped · cascade'],
    ['restart', 'Stopped · restart'],
    ['timeout', 'Stopped · timeout'],
  ] as const)('cause %s reads "%s"', (cause, text) => {
    expect(sessionStatusLabel(session({ lifecycle_state: 'stopped', stop_note: { ...NOTE, cause } }))).toEqual({ text, tone: 'stopped' })
  })
  it('a stop note on a session that is not stopped adds nothing', () => {
    expect(sessionStatusLabel(session({ lifecycle_state: 'working', stop_note: { ...NOTE, cause: 'cascade' } })).text).toBe('Working')
  })
})

describe('sessionKindLabel', () => {
  it('names the real kind, never the old HB abbreviation', () => {
    expect(sessionKindLabel('delegate')).toBe('Helper')
    expect(sessionKindLabel('main')).toBe('Main chat')
    expect(sessionKindLabel('scheduled')).toBe('Scheduled')
  })
})
