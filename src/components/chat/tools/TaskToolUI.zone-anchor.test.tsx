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
// Round 5b / Q2 C: only the router's Link is stubbed. The args carry no
// workspace_id, and a card whose call names no workspace now links "Open tasks"
// to /tasks (team-lead decision Q2 C, 2026-11-09), so a Link renders here and
// needs a router the unit mount does not have; the same <a> shim as the other
// TaskToolUI tests stands in for it (this file asserts nothing about the link —
// its schedule assertions are unchanged). The trigger parse and Intl run for
// real. Assertion granularity is part-level regexes, as in the round-1/2 pack —
// display copy is not pinned.

import { render, screen } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const original = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...original,
    Link: (props: {
      to?: unknown
      params?: Record<string, unknown>
      search?: Record<string, unknown>
      children?: ReactNode
    } & Record<string, unknown>) => {
      let href = typeof props.to === 'string' ? props.to : ''
      for (const [k, v] of Object.entries(props.params ?? {})) href = href.replace(`$${k}`, String(v))
      const searchEntries = Object.entries(props.search ?? {}).map(([k, v]) => [k, String(v)] as [string, string])
      const qs = searchEntries.length > 0 ? '?' + new URLSearchParams(searchEntries).toString() : ''
      const { to: _to, params: _params, search: _search, children, ...rest } = props
      return (
        <a href={href + qs} {...rest}>
          {children}
        </a>
      )
    },
  }
})

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

// ── Round 4 / D2 — the END date is a rule-zone calendar word too ────────────
//
// FR-006a (docs/internal/specs/calendar-recurrence-redesign-spec.md):
// "Ends on date D" means D 23:59:59 IN THE RULE'S ZONE, converted to UTC.
// Team-lead decision D2 (round 4): the card must show the rule-zone calendar
// date, never the viewer's — in EITHER direction, hence the west (Los
// Angeles) RED case and the east (Auckland) control.
//
// RED today: recurrence.ts::endConditionClause prints the parsed UNTIL
// instant through formatMonthDayYear, whose local getters read the VIEWER's
// zone (parseUntilToken builds the instant in UTC), so a UTC viewer of a Los
// Angeles schedule ending 10 November reads "ends November 11, 2026" (CHECK
// F2, reproduced by its diagnostics).
describe('end date — a repeating schedule names its end date in the trigger zone (FR-006a, D2)', () => {
  // LA November 2026 is PST (UTC−8); Auckland November 2026 is NZDT (UTC+13).
  const LA_ZONE = 'America/Los_Angeles'
  const LA_DTSTART = Date.UTC(2026, 10, 9, 17, 0, 0) // 9 Nov 09:00 in Los Angeles
  const AK_DTSTART = Date.UTC(2026, 10, 10, 20, 0, 0) // 11 Nov 09:00 in Auckland

  function zonePartsOf(epochMs: number, timeZone: string): Map<string, string> {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone,
      year: 'numeric',
      month: 'numeric',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hourCycle: 'h23',
    }).formatToParts(new Date(epochMs))
    return new Map(parts.map((part) => [part.type, part.value]))
  }

  function renderWithZone(rrule: string, dtstartMs: number, timeZone: string) {
    render(
      <TaskToolCardBlock
        toolName="create_task"
        args={{
          title: 'Daily report',
          trigger: { type: 'recurring', config: { rrule, dtstart_ms: dtstartMs, tz: timeZone } },
        }}
        result={JSON.stringify({ task_id: 'task_1', status: 'inbox' })}
        status={{ type: 'complete' }}
      />,
    )
    const card = screen.getByTestId('task-tool-card')
    return { card, schedule: screen.getByTestId('task-tool-schedule') }
  }

  it('fixture instrument: both UNTIL instants are their rule-zone end-of-day boundary', () => {
    expect(Intl.DateTimeFormat().resolvedOptions().timeZone).toBe('UTC')
    // UNTIL=20261111T075959Z is 10 Nov 23:59:59 in Los Angeles — the
    // inclusive end of 10 November there.
    const la = zonePartsOf(Date.UTC(2026, 10, 11, 7, 59, 59), LA_ZONE)
    expect(la.get('month')).toBe('11')
    expect(la.get('day')).toBe('10')
    expect(la.get('hour')).toBe('23')
    expect(la.get('minute')).toBe('59')
    expect(la.get('second')).toBe('59')
    // UNTIL=20261111T105959Z is 11 Nov 23:59:59 in Auckland — the inclusive
    // end of 11 November there.
    const ak = zonePartsOf(Date.UTC(2026, 10, 11, 10, 59, 59), TRIGGER_ZONE)
    expect(ak.get('month')).toBe('11')
    expect(ak.get('day')).toBe('11')
    expect(ak.get('hour')).toBe('23')
    expect(ak.get('minute')).toBe('59')
    expect(ak.get('second')).toBe('59')
  })

  it('a Los Angeles daily schedule ending 10 November reads "ends November 10, 2026" from the UTC viewer', () => {
    const { card, schedule } = renderWithZone('FREQ=DAILY;UNTIL=20261111T075959Z', LA_DTSTART, LA_ZONE)
    const text = schedule.textContent ?? ''
    // The rule-zone end date (FR-006a / D2) — not the viewer-UTC instant's
    // date (11 November), which is the defect this pins against.
    expect(text).toMatch(/ends November 10, 2026/)
    expect(text).not.toMatch(/ends November 11/)
    // Start-word control (same rule as the round-3 pins): trigger-zone clock.
    expect(text).toMatch(/(^|\D)0?9[:.]00/)
    expect(text).not.toMatch(/(^|\D)17[:.]00/)
    expect(card.textContent ?? '').toMatch(/los angeles/i)
    // The wire rule never leaks onto the card (T1 A).
    expect(card.textContent ?? '').not.toMatch(RAW_RULE_RE)
  })

  it('control, zone east of UTC: an Auckland daily schedule ending 11 November reads "ends November 11, 2026"', () => {
    // The opposite direction: for a rule zone EAST of the viewer, the
    // rule-zone end date and the viewer-UTC instant date coincide, so the
    // correct answer is 11 November — an implementation that "fixed" the west
    // case by always subtracting a day (or anchoring in some fixed zone) dies
    // here.
    const { card, schedule } = renderWithZone('FREQ=DAILY;UNTIL=20261111T105959Z', AK_DTSTART, TRIGGER_ZONE)
    const text = schedule.textContent ?? ''
    expect(text).toMatch(/ends November 11, 2026/)
    expect(text).not.toMatch(/ends November 10/)
    expect(text).toMatch(/(^|\D)0?9[:.]00/)
    expect(card.textContent ?? '').toMatch(/auckland/i)
    expect(card.textContent ?? '').not.toMatch(RAW_RULE_RE)
  })
})

// ── Round 5 / D9 — one sentence: the run time and zone, then the end date ───
//
// CHECK round 2, developer note 3: the line is built by appending the run's
// clock time after the recurrence helper's end-date clause, so a schedule
// that ends on 10 November reads "…, ends November 10, 2026 at 09:00 (Los
// Angeles time)". That reads as a 09:00 cut-off, but FR-006a (see the D2
// block above) means the rule ends at 23:59:59 on the end date; and on a
// weekly rule the last run may not even fall on that date. Team-lead decision
// D9 (2026-11-08): the run time comes BEFORE the zone, the end date carries NO
// clock time, and the line is the sentence
//   "Repeats every day at 09:00 (Los Angeles time), ends November 10, 2026."
// The same fixture values as the D2 rows are used (Los Angeles DTSTART 9 Nov
// 09:00 PST, UNTIL 10 Nov 23:59:59 PST; Auckland DTSTART 11 Nov 09:00 NZDT,
// UNTIL 11 Nov 23:59:59 NZDT); both are daily rules, so the frequency words
// are the same in both lines and only the zone name and end date change.
// The whole line is pinned with toBe (trailing full stop included, as quoted
// in the decision): the loose D2 regexes above cannot tell the two orders
// apart — "ends November 10, 2026" is a substring of both.
describe('D9 — a repeating schedule with an end date reads run time and zone first, the end date last', () => {
  const LA = { zone: 'America/Los_Angeles', dtstart: Date.UTC(2026, 10, 9, 17, 0, 0), until: '20261111T075959Z' }
  const AUCKLAND = { zone: 'Pacific/Auckland', dtstart: Date.UTC(2026, 10, 10, 20, 0, 0), until: '20261111T105959Z' }

  function scheduleLineFor(fixture: { zone: string; dtstart: number; until: string }): string {
    render(
      <TaskToolCardBlock
        toolName="create_task"
        args={{
          title: 'Daily report',
          trigger: {
            type: 'recurring',
            config: { rrule: `FREQ=DAILY;UNTIL=${fixture.until}`, dtstart_ms: fixture.dtstart, tz: fixture.zone },
          },
        }}
        result={JSON.stringify({ task_id: 'task_1', status: 'inbox' })}
        status={{ type: 'complete' }}
      />,
    )
    return (screen.getByTestId('task-tool-schedule').textContent ?? '').trim()
  }

  it('a Los Angeles daily schedule ending 10 November reads exactly "Repeats every day at 09:00 (Los Angeles time), ends November 10, 2026."', () => {
    expect(Intl.DateTimeFormat().resolvedOptions().timeZone).toBe('UTC')
    expect(scheduleLineFor(LA)).toBe('Repeats every day at 09:00 (Los Angeles time), ends November 10, 2026.')
  })

  it('control, zone east of UTC: an Auckland daily schedule ending 11 November reads exactly "Repeats every day at 09:00 (Auckland time), ends November 11, 2026."', () => {
    expect(scheduleLineFor(AUCKLAND)).toBe('Repeats every day at 09:00 (Auckland time), ends November 11, 2026.')
  })

  it('the end date carries no clock time: no "at HH:MM" follows "ends"', () => {
    const line = scheduleLineFor(LA)
    // The run time appears exactly once, before the zone; nothing after the
    // end date but the closing full stop.
    expect(line.match(/\d{1,2}:\d{2}/g) ?? []).toEqual(['09:00'])
    expect(line).not.toMatch(/ends[^.]*\bat\b/)
    expect(line).toMatch(/ends November 10, 2026\.$/)
  })
})
