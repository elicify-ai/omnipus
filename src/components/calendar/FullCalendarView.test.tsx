/**
 * Unit tests for FullCalendarView.tsx's pure chip-label helpers.
 *
 * jsdom cannot lay out FullCalendar's own DOM (see the comment atop
 * CalendarScreen.occurrencesDegrade.test.tsx — FullCalendarView is mocked at
 * the module boundary in every screen-level test), so `EventChip`'s render
 * output isn't practically unit-testable end to end here. `occurrenceStatusLabel`
 * and `extTooltip` are exported specifically so the two reviewer-found bugs
 * they fix get direct coverage without needing a real FullCalendar render:
 *
 *  - H3 (accessibility): occurrence chips were announcing as "Inbox" to
 *    screen readers because `statusLabel` (src/lib/statusColors.ts) only
 *    knows the canonical 7-member TaskStatus enum and silently falls back to
 *    STATUS_LABELS.inbox for the ADR-050 synthetic states 'scheduled'/
 *    'no_record' — not real TaskStatus members.
 *  - L3 (tooltip): eventMapping.ts populates `ext.tooltip` (no-record
 *    explanation, bucket worst-wins breakdown, "first at HH:MM") but nothing
 *    read it, so it was invisible (BDD #9).
 */

import { describe, it, expect } from 'vitest'
import { occurrenceStatusLabel, extTooltip } from './FullCalendarView'
import type { CalendarEventExtProps } from './types'

describe('occurrenceStatusLabel (H3 — accessible name)', () => {
  it('maps the two ADR-050 synthetic states to their own human labels', () => {
    expect(occurrenceStatusLabel('scheduled')).toBe('Scheduled')
    expect(occurrenceStatusLabel('no_record')).toBe('No record')
  })

  it('delegates every real TaskStatus value to statusLabel, unchanged', () => {
    expect(occurrenceStatusLabel('done')).toBe('Done')
    expect(occurrenceStatusLabel('in_progress')).toBe('In Progress')
    expect(occurrenceStatusLabel('failed')).toBe('Failed')
    expect(occurrenceStatusLabel('blocked')).toBe('Blocked')
    expect(occurrenceStatusLabel('inbox')).toBe('Inbox')
    expect(occurrenceStatusLabel('next')).toBe('Next')
    expect(occurrenceStatusLabel('planning')).toBe('Planning')
  })

  it('never silently mislabels "scheduled"/"no_record" as "Inbox" (the H3 regression)', () => {
    expect(occurrenceStatusLabel('scheduled')).not.toBe('Inbox')
    expect(occurrenceStatusLabel('no_record')).not.toBe('Inbox')
  })
})

describe('extTooltip (L3 — chip tooltip surfacing)', () => {
  it('reads tooltip off a task-occurrence with one (no-record explanation)', () => {
    const ext: CalendarEventExtProps = {
      kind: 'task-occurrence',
      taskId: 't1',
      status: 'no_record',
      icon: 'Circle',
      occurrenceMs: 1000,
      tooltip: 'Run history unavailable — retention expired or the schedule changed since this ran.',
    }
    expect(extTooltip(ext)).toBe(
      'Run history unavailable — retention expired or the schedule changed since this ran.',
    )
  })

  it('returns undefined for a task-occurrence with no tooltip (e.g. the "scheduled" state)', () => {
    const ext: CalendarEventExtProps = {
      kind: 'task-occurrence',
      taskId: 't1',
      status: 'scheduled',
      icon: 'Clock',
      occurrenceMs: 1000,
    }
    expect(extTooltip(ext)).toBeUndefined()
  })

  it('reads the required tooltip off a task-occurrence-agg bucket', () => {
    const ext: CalendarEventExtProps = {
      kind: 'task-occurrence-agg',
      taskId: 't1',
      status: 'failed',
      icon: 'XCircle',
      tooltip: '12 done · 2 failed · 26 scheduled',
      dayStartMs: 2000,
      dayEndMs: 2000 + 24 * 60 * 60 * 1000,
    }
    expect(extTooltip(ext)).toBe('12 done · 2 failed · 26 scheduled')
  })

  it('reads the tooltip off a task-occurrence-more truncation marker', () => {
    const ext: CalendarEventExtProps = {
      kind: 'task-occurrence-more',
      taskId: 't1',
      status: 'next',
      icon: 'Clock',
      tooltip: 'More occurrences not shown',
    }
    expect(extTooltip(ext)).toBe('More occurrences not shown')
  })

  it('returns undefined for kinds with no tooltip field at all (task-due, task-fire, milestone)', () => {
    const due: CalendarEventExtProps = { kind: 'task-due', taskId: 't1', status: 'next', icon: 'Circle' }
    const fire: CalendarEventExtProps = { kind: 'task-fire', taskId: 't1', status: 'next', icon: 'Clock' }
    const milestone: CalendarEventExtProps = { kind: 'milestone', milestoneId: 'm1', icon: 'Flag' }
    expect(extTooltip(due)).toBeUndefined()
    expect(extTooltip(fire)).toBeUndefined()
    expect(extTooltip(milestone)).toBeUndefined()
  })
})
