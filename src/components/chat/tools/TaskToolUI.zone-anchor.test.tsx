// Zone-anchor pin — issue #1683 round 3. A RECURRING trigger's schedule line
// reads its calendar words (weekday / day-of-month / month) and its clock time
// in the TRIGGER's own time zone, never the viewer's (round-3 brief; founder
// example "every Monday at 09:00 (Berlin time)" — a Berlin-Monday instant is
// only self-consistent when the words come from the trigger zone).
//
// Oracles (never the card's current output):
//  - The zone-anchor rule and the founder recurring example above.
//  - The task detail panel's recurring display FORM — plain words, the zone
//    named, never the raw rule string: taskFormFields.ts::scheduledTriggerSummary
//    (recurring branch, rendered in TaskDetailPanel.tsx::TaskDetailPanel);
//    the card assembles the same shape in TaskToolUI.tsx::plainSchedule.
//    Honest note: the panel's recurring branch anchors `new Date(dtstart_ms)`
//    in the VIEWER's zone — the trigger-zone anchor itself rests on the rule,
//    not on the panel.
//  - Which words the line is made of: recurrence.ts::summarizeRecurrence
//    (yearly month+day from the anchor Date it is handed), recurrence.ts::
//    parseRruleString (monthly day + defaults from the anchor instant it is
//    handed), weekly weekday from the rule's BYDAY token.
//  - Deterministic viewer zone: the repo-wide vitest pin (vite.config.ts::
//    test.env — TZ 'UTC', LC_ALL 'en-US'; a runtime TZ override is unreliable,
//    see the TZ note in src/lib/calendar/eventMapping.test.ts).
//
// Discriminating fixture (arithmetic, checked by the instrument test below):
// DTSTART = 2026-11-09T20:00:00Z. The pinned UTC viewer reads
// Monday 9 November 20:00; Pacific/Auckland (NZDT, UTC+13 in Nov 2026) reads
// Tuesday 10 November 09:00. Weekday, date and clock all differ, so a
// viewer-anchored implementation cannot pass.
//
// Scope: TaskToolCardBlock only — the one card both paths render (live/replay
// parity is pinned in TaskToolUI.live-wiring.test.tsx / -replay.test.tsx).
// No mocks: the args carry no workspace_id, so no router Link renders, and
// the trigger parse and Intl run for real. Assertion granularity is
// part-level regexes, as in the round-1/2 pack — display copy is not pinned.

import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { TaskToolCardBlock } from './TaskToolUI'

const DTSTART_AUCKLAND_MS = Date.UTC(2026, 10, 9, 20, 0, 0)
const TRIGGER_ZONE = 'Pacific/Auckland'
const RAW_RULE_RE = /RRULE|FREQ=|BYDAY|BYMONTHDAY|DTSTART|COUNT=|UNTIL=|INTERVAL=|cron/i

/** Renders the card for a recurring create and returns the card and its schedule line. */
function renderRecurringCard(rrule: string): { card: HTMLElement; schedule: HTMLElement } {
  render(
    <TaskToolCardBlock
      toolName="create_task"
      args={{
        title: 'Zone anchor',
        trigger: { type: 'recurring', config: { rrule, dtstart_ms: DTSTART_AUCKLAND_MS, tz: TRIGGER_ZONE } },
      }}
      result={JSON.stringify({ task_id: 'task_1', status: 'inbox' })}
      status={{ type: 'complete' }}
    />,
  )
  const card = screen.getByTestId('task-tool-card')
  return { card, schedule: screen.getByTestId('task-tool-schedule') }
}

describe('zone anchor — a repeating schedule reads its calendar words in the trigger zone', () => {
  it('fixture instrument: the pinned UTC viewer and the Pacific/Auckland trigger disagree on weekday, date and clock', () => {
    // Process-start pin, not a runtime TZ override (see file header).
    expect(Intl.DateTimeFormat().resolvedOptions().timeZone).toBe('UTC')
    // Viewer-zone reading of the fixture instant: Monday 9 November, 20:00.
    const viewer = new Date(DTSTART_AUCKLAND_MS)
    expect(viewer.getDay()).toBe(1) // Monday
    expect(viewer.getDate()).toBe(9)
    expect(viewer.getMonth()).toBe(10) // November
    expect(viewer.getHours()).toBe(20)
    // Trigger-zone reading of the SAME instant: Tuesday 10 November, 09:00.
    const inTriggerZone = new Intl.DateTimeFormat('en-US', {
      timeZone: TRIGGER_ZONE,
      weekday: 'long',
      year: 'numeric',
      month: 'numeric',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    }).formatToParts(new Date(DTSTART_AUCKLAND_MS))
    const pick = (type: Intl.DateTimeFormatPartTypes): string =>
      inTriggerZone.find((part) => part.type === type)?.value ?? ''
    expect(pick('weekday')).toBe('Tuesday')
    expect(pick('month')).toBe('11')
    expect(pick('day')).toBe('10')
    expect(pick('hour')).toBe('09')
    expect(pick('minute')).toBe('00')
  })

  it('a yearly rule names the trigger-zone month and day, with the trigger-zone clock', () => {
    const { card, schedule } = renderRecurringCard('FREQ=YEARLY')
    const text = schedule.textContent ?? ''
    // Trigger-zone month word + day of month (9 November in the viewer's zone
    // must not appear): the yearly phrase takes both from the anchor Date.
    expect(text).toMatch(/(?:\bNov(?:ember)?\s+0?10\b|\b0?10\s+Nov(?:ember)?\b|2026-11-10)/)
    expect(text).not.toMatch(/(?:\bNov(?:ember)?\s+0?9\b|\b0?9\s+Nov(?:ember)?\b|2026-11-09)/)
    // Trigger-zone clock time; the pinned viewer's 20:00 must not appear.
    expect(text).toMatch(/(^|\D)0?9[:.]00/)
    expect(text).not.toMatch(/(^|\D)20[:.]00/)
    // The trigger zone is named (founder example form) and the rule stays raw.
    expect(card.textContent ?? '').toMatch(/auckland/i)
    expect(card.textContent ?? '').not.toMatch(RAW_RULE_RE)
  })

  it('a monthly rule names the trigger-zone day of month, with the trigger-zone clock', () => {
    // No BYMONTHDAY/BYDAY/BYSETPOS: the day comes from the anchor instant
    // parseRruleString is given — day 10 in the trigger zone, day 9 in the
    // viewer's zone.
    const { card, schedule } = renderRecurringCard('FREQ=MONTHLY')
    const text = schedule.textContent ?? ''
    expect(text).toMatch(/\bday\s+0?10\b/i)
    expect(text).not.toMatch(/\bday\s+0?9\b/i)
    expect(text).toMatch(/(^|\D)0?9[:.]00/)
    expect(text).not.toMatch(/(^|\D)20[:.]00/)
    expect(card.textContent ?? '').toMatch(/auckland/i)
    expect(card.textContent ?? '').not.toMatch(RAW_RULE_RE)
  })

  it('a weekly rule names the weekday the trigger-zone calendar gives, with the trigger-zone clock', () => {
    // The weekday word comes from the rule's BYDAY token; the fixture makes it
    // equal to the trigger-zone weekday of DTSTART (Tuesday in Auckland). The
    // viewer-zone weekday of the same instant (Monday) must not appear.
    const { card, schedule } = renderRecurringCard('FREQ=WEEKLY;BYDAY=TU')
    const text = schedule.textContent ?? ''
    expect(text).toMatch(/\btuesday\b/i)
    expect(text).not.toMatch(/\bmonday\b/i)
    expect(text).toMatch(/(^|\D)0?9[:.]00/)
    expect(text).not.toMatch(/(^|\D)20[:.]00/)
    expect(card.textContent ?? '').toMatch(/auckland/i)
    expect(card.textContent ?? '').not.toMatch(RAW_RULE_RE)
  })
})
