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
 *
 * ROUND-5b additions (team-lead decisions Q1 A and Q2 C, 2026-11-09 — the link
 * rule): a failed call keeps the "Open tasks" link with or without a reason
 * (Q1 A); the link goes to /tasks when the call carries no workspace_id and to
 * /workspaces/<id>/chat?panel=tasks when it names one, and "Open calendar"
 * appears only when workspace_id is present (Q2 C).
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
// Round 5b / Q2 C: the same create / update calls with NO workspace_id (the real
// tools take it as optional — absent or null = the agent's own workspace,
// pkg/tools/task.go::resolveTargetWorkspace — and run_task / delete_task take only
// task_id), plus the link targets the decisions name, as literals.
const NO_WORKSPACE_BASE = { title: 'Weekly report', prompt: 'Write the weekly report', agent_id: 'agent_mia' }
const ARGS_CREATE_NO_WS = { ...NO_WORKSPACE_BASE, trigger: RECURRING_TRIGGER }
const ARGS_UPDATE_NO_WS = { task_id: 'task_1', ...NO_WORKSPACE_BASE, trigger: RECURRING_TRIGGER }
const WORKSPACE_TASKS_HREF = '/workspaces/ws_1/chat?panel=tasks'
const TASKS_INDEX_HREF = '/tasks'

const RESULT_CREATE = JSON.stringify({ task_id: 'task_1', status: 'inbox' })
const RESULT_UPDATE = JSON.stringify({ task_id: 'task_1', status: 'inbox', updated_fields: ['trigger'] })
const RESULT_RUN = JSON.stringify({ task_id: 'task_1', status: 'running', session_id: 'sess_1' })
const RESULT_DELETE = JSON.stringify({ deleted: 'task_1' })
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
    // Round 5 / D8 REPLACES the round-4 assertion that stood here
    // (`queryByTestId('task-tool-link-open')` is null): team-lead decision D8
    // (2026-11-08) says a call that came back with a KNOWN reason — this
    // refusal text, carried in the call's error field, is one — keeps the
    // "Open tasks" link as the way forward. The link is expected because
    // ARGS_CREATE carries workspace_id. (Round 5b / Q1 A: the no-reason
    // failure keeps the link too — the D5 control below.)
    expect(screen.getByTestId('task-tool-link-open')).toHaveAttribute('href', WORKSPACE_TASKS_HREF)
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

// ── Round 5 / D8 — a replayed failed call with a KNOWN reason ───────────────
//
// Team-lead decisions D5 (reaffirmed) and D8 (2026-11-08) are the oracle:
//  - D5: the no-reason sentence appears ONLY when no reason exists anywhere. A
//    reason counts when it is the call's error field, a string result, a
//    {text} result, or the `reason` of a generated structured tool failure
//    (delegation_denied).
//  - D8: with a known reason the card shows "I couldn't <verb> the task.
//    <reason>" (verb create / update / start / delete; reason UNCHANGED), keeps
//    the "Open tasks" link, and the alert stays role="alert".
// This file stays on ToolCallBadge (the replay badge). The saved-history
// dispatcher and the REST adapter are pinned in TaskToolUI.historical.test.tsx
// and TaskToolUI.rest-history.test.tsx. The link's TARGET follows Round 5b / Q2
// C: params that carry workspace_id link to that workspace's tasks panel (the
// create / update rows); real run_task and delete_task params carry only
// task_id, so those rows link to /tasks. Round 5b / Q2 C REPLACES the round-5
// `link: boolean` flag (false = "the row asserts the sentence alone") with an
// explicit expected href on every row — the two task_id-only rows now pin the
// /tasks link too (strengthened, nothing removed).
describe('Round 5 / D8 — a replayed failed call with a known reason', () => {
  const NO_REASON_RE = /no reason was given/i
  const SUCCESS_HEADING_RE = /^(I (scheduled|created|updated|started|deleted) the task|The task is already running)$/

  const ROWS: Array<{ tool: string; verb: string; params: Record<string, unknown>; reason: string; href: string }> = [
    {
      tool: 'create_task',
      verb: 'create',
      params: ARGS_CREATE,
      reason: 'agent "agent_zz" is not a member of this workspace.',
      href: WORKSPACE_TASKS_HREF,
    },
    { tool: 'update_task', verb: 'update', params: ARGS_UPDATE, reason: 'task not found: task_99', href: WORKSPACE_TASKS_HREF },
    {
      tool: 'run_task',
      verb: 'start',
      params: { task_id: 'task_1' },
      reason: 'task "task_1" is blocked on an unmet dependency; it becomes runnable automatically once its blockers complete',
      href: TASKS_INDEX_HREF,
    },
    {
      tool: 'delete_task',
      verb: 'delete',
      params: { task_id: 'task_1' },
      reason: 'Task task_1 was already deleted',
      href: TASKS_INDEX_HREF,
    },
  ]

  function expectKnownReasonFailure(verb: string, reason: string, href: string): void {
    const sentence = screen.getByText(`I couldn't ${verb} the task. ${reason}`)
    expect(sentence.closest('[role="alert"]'), 'the sentence sits in the catalogued error region (D7/D8)').not.toBeNull()
    const open = screen.getByTestId('task-tool-link-open')
    expect((open.textContent ?? '').trim()).toBe('Open tasks')
    expect(open).toHaveAttribute('href', href)
    // D5: a call that has a reason never claims it has none.
    expect(screen.queryByText(NO_REASON_RE)).toBeNull()
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
    expect(screen.queryByTestId('task-tool-schedule')).toBeNull()
    expect(screen.queryByText(SUCCESS_HEADING_RE)).toBeNull()
  }

  it.each(ROWS)('$tool: a stored error field is shown after "I couldn\'t $verb the task."', ({ tool, verb, params, reason, href }) => {
    render(<ToolCallBadge toolCall={makeTaskCall({ tool, params, status: 'error', error: reason, result: undefined })} />)
    expectKnownReasonFailure(verb, reason, href)
  })

  it.each(ROWS)('5a, $tool: a reason carried only in a {text} result gets the same framing', ({ tool, verb, params, reason, href }) => {
    render(<ToolCallBadge toolCall={makeTaskCall({ tool, params, status: 'error', result: { text: reason } })} />)
    expectKnownReasonFailure(verb, reason, href)
  })

  it.each(ROWS)('$tool: a reason carried as a string result gets the same framing', ({ tool, verb, params, reason, href }) => {
    render(<ToolCallBadge toolCall={makeTaskCall({ tool, params, status: 'error', result: reason })} />)
    expectKnownReasonFailure(verb, reason, href)
  })

  it('D5: the reason field of a structured delegation_denied failure counts as a reason', () => {
    // Shaped by the generated DelegationFailure contract (asyncapi.yaml) and
    // pkg/tools/result.go::DelegationDeniedResult — not by the card's code.
    const denial = {
      error: 'delegation_denied',
      reason: 'Scout is not in your trust set, so you cannot start its task.',
      policy: 'trust_set',
      tool: 'run_task',
      target_agent_id: 'scout',
    }
    render(
      <ToolCallBadge
        toolCall={makeTaskCall({
          tool: 'run_task',
          params: { task_id: 'task_1', workspace_id: 'ws_1' },
          status: 'error',
          result: denial,
        })}
      />,
    )
    expectKnownReasonFailure('start', denial.reason, WORKSPACE_TASKS_HREF)
  })

  // Round 5b / Q1 A: the D5 control ALSO asserts the "Open tasks" link (added in
  // place; the sentence assertions are unchanged). ARGS_UPDATE names workspace_id.
  it('D5 control: with no error and no result the no-reason sentence is still shown (the checks above can tell the two apart)', () => {
    render(<ToolCallBadge toolCall={makeTaskCall({ tool: 'update_task', params: ARGS_UPDATE, status: 'error', result: undefined })} />)
    expect((screen.getByRole('alert').textContent ?? '').trim()).toBe(
      "I couldn't update the task, and no reason was given. Try again.",
    )
    expect(screen.queryByText(/^I couldn't update the task\. /)).toBeNull()
    const open = screen.getByTestId('task-tool-link-open')
    expect((open.textContent ?? '').trim()).toBe('Open tasks')
    expect(open).toHaveAttribute('href', WORKSPACE_TASKS_HREF)
  })
})

// ── Round 5b / Q2 C — a replayed call that names no workspace ───────────────
//
// Team-lead decisions Q1 A and Q2 C (2026-11-09) are the oracle: a failed call
// keeps "Open tasks" with a reason (D8) or without one (D5); with no workspace_id
// in the call's params the link goes to /tasks, and "Open calendar" is absent
// (it appears only when workspace_id is present — never the route's current
// workspace). The failure rows pin the alert and the reason it carries, not the
// D8 framing (the round-5 describe above owns that), so each row goes red for
// the missing link alone. TODAY a successful card shows its links only when the
// params carry workspace_id, so the success rows are red for the missing /tasks link.
describe('Round 5b / Q2 C — a replayed call that names no workspace', () => {
  const NO_WORKSPACE_ROWS: Array<{
    tool: string
    params: Record<string, unknown>
    reason: string
    noReason: string
    result: string
  }> = [
    {
      tool: 'create_task',
      params: ARGS_CREATE_NO_WS,
      reason: 'agent "agent_zz" is not a member of this workspace.',
      noReason: "I couldn't create the task, and no reason was given. Try again.",
      result: RESULT_CREATE,
    },
    {
      tool: 'update_task',
      params: ARGS_UPDATE_NO_WS,
      reason: 'task not found: task_99',
      noReason: "I couldn't update the task, and no reason was given. Try again.",
      result: RESULT_UPDATE,
    },
    {
      tool: 'run_task',
      params: { task_id: 'task_1' },
      reason: 'task "task_1" is blocked on an unmet dependency; it becomes runnable automatically once its blockers complete',
      noReason: "I couldn't start the task, and no reason was given. Try again.",
      result: RESULT_RUN,
    },
    {
      tool: 'delete_task',
      params: { task_id: 'task_1' },
      reason: 'Task task_1 was already deleted',
      noReason: "I couldn't delete the task, and no reason was given. Try again.",
      result: RESULT_DELETE,
    },
  ]

  function expectTasksIndexLinkAndNoCalendar(): void {
    const open = screen.getByTestId('task-tool-link-open')
    expect((open.textContent ?? '').trim()).toBe('Open tasks')
    expect(open).toHaveAttribute('href', TASKS_INDEX_HREF)
    expect(screen.queryByTestId('task-tool-link-calendar')).toBeNull()
  }

  it.each(NO_WORKSPACE_ROWS)(
    '$tool: a failed call with a stored reason and no workspace_id links "Open tasks" to /tasks and has no calendar link',
    ({ tool, params, reason }) => {
      render(<ToolCallBadge toolCall={makeTaskCall({ tool, params, status: 'error', error: reason, result: undefined })} />)
      expect(screen.getByRole('alert').textContent ?? '').toContain(reason)
      expect(screen.queryByTestId('tool-call-badge')).toBeNull()
      expectTasksIndexLinkAndNoCalendar()
    },
  )

  it.each(NO_WORKSPACE_ROWS)(
    '$tool: a failed call with no reason and no workspace_id keeps the D5 sentence, links "Open tasks" to /tasks and has no calendar link',
    ({ tool, params, noReason }) => {
      render(<ToolCallBadge toolCall={makeTaskCall({ tool, params, status: 'error', result: undefined })} />)
      expect((screen.getByRole('alert').textContent ?? '').trim()).toBe(noReason)
      expect(screen.queryByTestId('tool-call-badge')).toBeNull()
      expectTasksIndexLinkAndNoCalendar()
    },
  )

  it.each(NO_WORKSPACE_ROWS)(
    '$tool: a successful call with no workspace_id links "Open tasks" to /tasks and has no calendar link',
    ({ tool, params, result }) => {
      render(<ToolCallBadge toolCall={makeTaskCall({ tool, params, result: { text: result } })} />)
      const card = screen.getByTestId('task-tool-card')
      const open = within(card).getByTestId('task-tool-link-open')
      expect((open.textContent ?? '').trim()).toBe('Open tasks')
      expect(open).toHaveAttribute('href', TASKS_INDEX_HREF)
      expect(within(card).queryByTestId('task-tool-link-calendar')).toBeNull()
    },
  )
})
