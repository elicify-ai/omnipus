/**
 * TaskToolUI.replay.test.tsx — issue #1683 / Team B WP-1 (RED), replay half.
 *
 * Founder decision 2026-11-07 (T1 A): the readable task-tool card works LIVE
 * AND ON HISTORY REPLAY. The replay surface is ToolCallBadge (the historical
 * list renderer — its ONE caller is MessageItem's live/historical list), and
 * a dedicated per-tool card is delivered there the same way set_goal's is:
 * the component file exports the presentational block, ToolCallBadge routes
 * the tool to it before the generic rendering (ToolCallBadge.tsx::set_goal
 * early-return precedent).
 *
 * RED route chosen: the REGISTERED-SURFACE route. ToolCallBadge exists TODAY
 * and renders a replayed task call as the generic raw-JSON badge — exactly
 * what the founder decision removes — so every test here fails on
 * ASSERTIONS (card missing AND raw badge present), naming the defect, not on
 * an import. (The live half, TaskToolUI.live-wiring.test.tsx, covers the
 * four live registrations via the import route.)
 *
 * ORACLES: founder decision (issue #1683) — same card on replay; plan §4.2
 * WP-1 (f) "Replay renders the same card as live" and (h); the result-shape
 * table in SetGoalToolUI.tsx's file doc comment (live = JSON string, replay
 * = `{ text: "<json>" }` envelope, some paths already-parsed object) — the
 * card must render from all three, or a card that renders live vanishes
 * after a reload (the pre-S12 regression). Once-trigger zone rule — C1 B,
 * founder decision 2026-11-07 11:16Z: a ONCE trigger shows the VIEWER's own
 * local time (task detail panel's existing display,
 * src/components/workspaces/taskFormFields.ts:135, used at
 * TaskDetailPanel.tsx:1034), never the trigger-zone time; recurring keeps
 * the founder example "every Monday at 09:00 (Berlin time)" (trigger zone).
 * The viewer zone is deterministically UTC via the repo's test pin
 * (vite.config.ts:264: TZ 'UTC', LC_ALL 'en-US').
 *
 * TEST-ID contract: see TaskToolUI.live-wiring.test.tsx's header — both
 * paths render the SAME test ids, which is how "same card" is asserted here.
 *
 * ROUND-4 additions (team-lead decisions on the CHECK FAIL report; oracles
 * are the decisions and the voice rules, never the card's output): D3's
 * exact create heading is pinned on the replay path too; D4's warning text
 * is pinned EXACTLY here as well as live; D6's link copy is pinned beside
 * the unchanged href assertions; D7's role="alert" failure semantics hold
 * on replay like live. The REAL saved-history dispatcher pin (D1) lives in
 * TaskToolUI.historical.test.tsx — this file stays on ToolCallBadge.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import type { ReactNode } from 'react'
import type { ToolCall } from '@/lib/api'

// Same Link shim as the live-wiring file: assert navigation TARGETS without
// a full router (case (g) parity on replay).
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
      const searchEntries = Object.entries(props.search ?? {}).map(
        ([k, v]) => [k, String(v)] as [string, string],
      )
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

import { ToolCallBadge } from '../ToolCallBadge'

// ── fixtures (same instant as the live half: Mon 2026-11-09 09:00 Berlin =
// 08:00 UTC; vitest pins TZ=UTC + en-US) ─────────────────────────────────────
const MON_0900_BERLIN_MS = Date.UTC(2026, 10, 9, 8, 0, 0)
const RECURRING_TRIGGER = {
  type: 'recurring',
  config: { rrule: 'FREQ=WEEKLY;BYDAY=MO', dtstart_ms: MON_0900_BERLIN_MS, tz: 'Europe/Berlin' },
}
// once: the tz stays so the trigger's zone (Europe/Berlin) DIFFERS from the
// pinned viewer zone (UTC, vite.config.ts:264) — under C1 B (founder
// 2026-11-07 11:16Z) the card must format the instant in the VIEWER's zone
// (08:00); an implementation that formats in the trigger's zone (09:00)
// fails the once test below.
const ONCE_TRIGGER = { type: 'once', config: { at_ms: MON_0900_BERLIN_MS, tz: 'Europe/Berlin' } }
const BASE_ARGS = {
  title: 'Weekly report',
  prompt: 'Write the weekly report',
  workspace_id: 'ws_1',
  agent_id: 'agent_mia',
}
const ARGS_CREATE = { ...BASE_ARGS, trigger: RECURRING_TRIGGER }
const ARGS_UPDATE = { task_id: 'task_1', ...BASE_ARGS, trigger: RECURRING_TRIGGER }

const RESULT_CREATE = JSON.stringify({ task_id: 'task_1', status: 'inbox' })
const RESULT_UPDATE_WARN = JSON.stringify({
  task_id: 'task_1',
  status: 'inbox',
  updated_fields: ['trigger'],
  schedule_sync_warning:
    'the trigger was saved on the task, but its schedule could not be re-armed (scheduler unreachable); the saved trigger stays authoritative — re-save the schedule or restart the gateway',
})

// r17 §7.10 foreign-trigger refusal tail.
const FOREIGN_REFUSAL =
  'Tasks for agents in another workspace start only when their team picks them up there: create it without a trigger, or ask the assignee (or a person) to add the schedule after pickup. Nothing was created.'

type ToolCallWithId = ToolCall & { call_id: string }

function makeTaskCall(overrides: Partial<ToolCallWithId>): ToolCallWithId {
  return {
    id: 'tc_1',
    call_id: 'tc_1',
    tool: 'create_task',
    params: ARGS_CREATE,
    status: 'success',
    duration_ms: 12,
    ...overrides,
  }
}

// ── (f) replay renders the same card as live ────────────────────────────────
describe('case (f) — history replay renders the same readable card', () => {
  it('a replayed create_task renders the card — not the raw-JSON generic badge', () => {
    // The persisted shape: every plain-text tool result is stored under the
    // `{ text: "<payload json>" }` envelope and replay forwards it unchanged.
    render(<ToolCallBadge toolCall={makeTaskCall({ result: { text: RESULT_CREATE } })} />)
    const card = screen.getByTestId('task-tool-card')
    const schedule = within(card).getByTestId('task-tool-schedule')
    expect(schedule.textContent ?? '').toMatch(/0?9[:.]00/)
    expect(card.textContent ?? '').toMatch(/berlin/i)
    expect((card.textContent ?? '')).toContain('Weekly report')
    // The raw-JSON generic badge must be GONE for a task tool on replay.
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it.each([
    ['envelope shape', { text: RESULT_CREATE }],
    ['live-style JSON string shape', RESULT_CREATE],
    ['already-parsed object shape', JSON.parse(RESULT_CREATE)],
  ])('renders the card for the %s result shape (same card as live)', (_name, result) => {
    render(<ToolCallBadge toolCall={makeTaskCall({ result })} />)
    const card = screen.getByTestId('task-tool-card')
    expect(within(card).getByTestId('task-tool-schedule')).toBeTruthy()
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it('replay card links carry the same route-truth targets as live', () => {
    render(<ToolCallBadge toolCall={makeTaskCall({ result: { text: RESULT_CREATE } })} />)
    const card = screen.getByTestId('task-tool-card')
    expect(within(card).getByTestId('task-tool-link-open')).toHaveAttribute(
      'href',
      '/workspaces/ws_1/chat?panel=tasks',
    )
    expect(within(card).getByTestId('task-tool-link-calendar')).toHaveAttribute(
      'href',
      '/workspaces/ws_1/calendar',
    )
    // Round 4 / D6 parity: the copy names the destination, live and replay.
    // RED today: "Open task" / "Show in Calendar".
    expect((within(card).getByTestId('task-tool-link-open').textContent ?? '').trim()).toBe('Open tasks')
    expect((within(card).getByTestId('task-tool-link-calendar').textContent ?? '').trim()).toBe('Open calendar')
  })

  it('a replayed once trigger shows the VIEWER-local time, not the trigger-zone time (C1 B)', () => {
    // C1 B (founder 2026-11-07 11:16Z) on the replay path too: the detail
    // panel's display (taskFormFields.ts:135) formats in the viewer's zone;
    // with the pinned viewer UTC the fixture instant (09:00 Europe/Berlin)
    // must read 08:00, and 09:00 must not appear. Use the process-start pin
    // (src/lib/calendar/eventMapping.test.ts:289–297; vite.config.ts:264),
    // not a runtime TZ override.
    expect(Intl.DateTimeFormat().resolvedOptions().timeZone).toBe('UTC')
    render(
      <ToolCallBadge
        toolCall={makeTaskCall({
          params: { ...BASE_ARGS, trigger: ONCE_TRIGGER },
          result: { text: RESULT_CREATE },
        })}
      />,
    )
    const card = screen.getByTestId('task-tool-card')
    const schedule = within(card).getByTestId('task-tool-schedule')
    // Same viewer-local date as the detail panel: 9 November 2026. Accept
    // date-part ordering as in src/components/ui/date-picker.test.tsx:27–39.
    expect(schedule.textContent ?? '').toMatch(/2026/)
    expect(schedule.textContent ?? '').toMatch(/\b(?:Nov(?:ember)?\s+0?9|0?9\s+Nov(?:ember)?|11\/0?9\/2026|2026-11-09)\b/i)
    expect(schedule.textContent ?? '').toMatch(/(^|\D)0?8[:.]00/)
    expect(schedule.textContent ?? '').not.toMatch(/(^|\D)0?9[:.]00/)
  })

  it('a replayed refused create shows the refusal — never a success card ((h) parity)', () => {
    render(
      <ToolCallBadge
        toolCall={makeTaskCall({ status: 'error', error: FOREIGN_REFUSAL, result: undefined })}
      />,
    )
    expect(screen.getByText(/Nothing was created/)).toBeTruthy()
    // Round 4 / D7 parity: the failure carries the catalogued error
    // semantics on replay too. RED today: no role="alert" exists.
    expect(screen.getByRole('alert').textContent ?? '').toContain('Nothing was created')
    expect(screen.queryByTestId('task-tool-schedule')).toBeNull()
    expect(screen.queryByTestId('task-tool-link-open')).toBeNull()
    expect(screen.queryByTestId('task-tool-verb')).toBeNull()
  })

  it('a replayed update keeps the saved-but-not-active warning ((d) parity)', () => {
    render(
      <ToolCallBadge
        toolCall={makeTaskCall({
          tool: 'update_task',
          params: ARGS_UPDATE,
          result: { text: RESULT_UPDATE_WARN },
        })}
      />,
    )
    const card = screen.getByTestId('task-tool-card')
    // Round 4 / D4 parity: the warning text is pinned EXACTLY on replay,
    // matching the live case (d) — an opposite-meaning text fails here too.
    // RED today: the current copy differs.
    expect((within(card).getByTestId('task-tool-schedule-warning').textContent ?? '').trim()).toBe(
      "I saved the task, but its schedule isn't active. The saved schedule is still the one to use. Open Calendar and save it again.",
    )
    expect(within(card).getByTestId('task-tool-verb')).toBeTruthy()
  })

  it("a replayed create's heading is exactly the scheduled-create heading (D3 parity)", () => {
    // Round 4 / D3 on the replay path: same exact heading as live for a
    // create with a schedule. RED today: "Task scheduled".
    render(<ToolCallBadge toolCall={makeTaskCall({ result: { text: RESULT_CREATE } })} />)
    const card = screen.getByTestId('task-tool-card')
    expect((within(card).getByTestId('task-tool-verb').textContent ?? '').trim()).toBe('I scheduled the task')
  })
})
