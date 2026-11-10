// TaskToolUI.rest-history.test.tsx — issue #1683, round 5 / R2-1 and 5a.
//
// CHECK round 2 (R2-1, a blocker): a failed task call reloaded through the
// REST history page loses the server's reason on the way to the card.
// src/lib/api/sessions.ts::rawToToolCall sets `error: undefined`, so the saved
// card cannot tell "the server gave a reason" from "no reason was given" and
// prints "I couldn't update the task, and no reason was given. Try again."
// for a call whose stored error is "task not found: task_99".
//
// This file drives the REAL path end to end; only the network edge is faked:
//   global fetch (a REST page built from the GENERATED wire types)
//     -> sessions.ts::fetchSessionMessages -> parseWireMessageList
//     -> rawToMessage -> rawToToolCall
//     -> HistoricalToolCallBlock.tsx::renderHistoricalToolCall (real) -> card.
//
// ORACLES (team-lead decisions, never the card's current output):
//  - D5 (reaffirmed): the no-reason sentence "I couldn't <verb> the task, and
//    no reason was given. Try again." appears ONLY when no reason exists
//    anywhere. A reason counts when it is in the call's error field, a string
//    result, a {text} result, or the `reason` field of a generated structured
//    tool failure (delegation_denied).
//  - D8: with a known reason the card shows "I couldn't <verb> the task.
//    <reason>" and keeps the "Open tasks" link visible. <verb> is create /
//    update / start / delete; <reason> is the received text UNCHANGED. The
//    alert stays role="alert".
//  - The contract: contracts/components/schemas/ToolCall.yaml::error — "Human-
//    readable failure reason when status is 'error'. Mirrors the equivalent
//    field on ToolCallResultFrame so a reloaded transcript carries the same
//    explanation the live WS frame did."
//  - A REST tool result is an OBJECT (ToolCall.result is `{[key]: unknown}`),
//    so a plain-text result arrives as the persisted `{ text }` envelope.
//
// The fixtures are typed with the generated ToolCall / DelegationFailure
// types (openapi-types.ts, asyncapi-types.ts) and validated by
// expectValidPage, so an illegal wire shape cannot slip into the suite.
//
// ROUND 5b (team-lead decisions Q1 A and Q2 C, 2026-11-09 — the link rule): a
// failed call keeps the "Open tasks" link with or without a reason (Q1 A); the
// link goes to /tasks when the call's parameters carry no workspace_id and to
// /workspaces/<id>/chat?panel=tasks when they name one, and "Open calendar"
// appears only when workspace_id is present (Q2 C). Every row above the last
// describe carries workspace_id (TASK_ARGS) and keeps its workspace target.

import { render, screen, within } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DelegationFailure } from '@/lib/api/generated/asyncapi-types'
import type { ToolCall as WireToolCall } from '@/lib/api/generated/openapi-types'
import { SID, entry, expectValidPage, page, pos } from '../../../../tests/chat-history-paging/fixtures'
import { renderHistoricalToolCall } from './HistoricalToolCallBlock'

// Same Link shim as the live-wiring file: the "Open tasks" link needs a router
// the unit mount does not have, so it renders as an <a> whose href is the
// route with its params and search folded in.
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const original = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...original,
    Link: ({ to, params, search, children, ...rest }: {
      to?: unknown
      params?: Record<string, unknown>
      search?: Record<string, unknown>
      children?: ReactNode
    } & Record<string, unknown>) => {
      let href = typeof to === 'string' ? to : ''
      for (const [k, v] of Object.entries(params ?? {})) href = href.replace(`$${k}`, String(v))
      const searchEntries = Object.entries(search ?? {}).map(([k, v]) => [k, String(v)] as [string, string])
      const qs = searchEntries.length > 0 ? '?' + new URLSearchParams(searchEntries).toString() : ''
      return (
        <a href={href + qs} {...rest}>
          {children}
        </a>
      )
    },
  }
})

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn())
  Object.defineProperty(document, 'cookie', { configurable: true, get: () => '__Host-csrf=test-csrf-token' })
})
afterEach(() => {
  vi.unstubAllGlobals()
  sessionStorage.clear()
  Reflect.deleteProperty(document, 'cookie')
  vi.resetModules()
})

type SavedTc = Parameters<typeof renderHistoricalToolCall>[0]['tc']

const TASK_ARGS = { task_id: 'task_99', title: 'Weekly report', workspace_id: 'ws_1' }
// Round 5b / Q2 C: the link targets the decisions name, as literals.
const WORKSPACE_TASKS_HREF = '/workspaces/ws_1/chat?panel=tasks'
const TASKS_INDEX_HREF = '/tasks'

/** A wire tool call in the generated ToolCall shape; a failed call by default. */
function wireCall(tool: string, overrides: Partial<WireToolCall> = {}): WireToolCall {
  return { id: `call_${tool}`, tool, status: 'error', parameters: TASK_ARGS, content_state: 'full', ...overrides }
}

/**
 * Puts one wire call on a REST history page, validates the page against the
 * generated schema, and reads it back through the REAL fetchSessionMessages.
 */
async function loadThroughRest(call: WireToolCall): Promise<SavedTc> {
  const fixture = page(
    [entry(1, { id: call.id, type: 'tool_call', role: 'assistant', content: '', agent_id: 'mia', turn_id: 'mia-turn-1', tool_calls: [call] })],
    { sessionId: SID },
  )
  expectValidPage(fixture, [pos(1)], 1)
  vi.mocked(fetch).mockResolvedValueOnce(
    new Response(JSON.stringify(fixture), { status: 200, headers: { 'Content-Type': 'application/json' } }),
  )
  const { fetchSessionMessages } = await import('@/lib/api/sessions')
  const result = await fetchSessionMessages(SID)
  const saved = result.entries.flatMap((row) => row.tool_calls ?? [])
  expect(saved.map((tc) => tc.id), 'the REST page keeps exactly the one saved task call').toEqual([call.id])
  return saved[0] as SavedTc
}

/** Renders a saved call through the real history dispatcher, as ChatScreen's saved row does. */
function renderSaved(tc: SavedTc) {
  const node = renderHistoricalToolCall({
    tc,
    callId: tc.id,
    inlineEvents: undefined,
    activeSessionId: null,
    verboseChatEnabled: false,
    goalRecordEmpty: false,
    liteMode: false,
  })
  return render(<>{node}</>)
}

const NO_REASON_RE = /no reason was given/i
const SUCCESS_HEADING_RE = /^(I (scheduled|created|updated|started|deleted) the task|The task is already running)$/

/** D8: "I couldn't <verb> the task. <reason>" in the alert, the reason unchanged, "Open tasks" kept. */
function expectKnownReasonFailure(verb: string, reason: string): void {
  const sentence = screen.getByText(`I couldn't ${verb} the task. ${reason}`)
  expect(sentence.closest('[role="alert"]'), 'the sentence sits in the catalogued error region (D7/D8)').not.toBeNull()
  const open = screen.getByTestId('task-tool-link-open')
  expect((open.textContent ?? '').trim()).toBe('Open tasks')
  expect(open).toHaveAttribute('href', WORKSPACE_TASKS_HREF)
  // D5: a call that has a reason never claims it has none.
  expect(screen.queryByText(NO_REASON_RE)).toBeNull()
  // Never the generic chip, never a success heading.
  expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  expect(screen.queryByText(SUCCESS_HEADING_RE)).toBeNull()
}

describe('R2-1 — a saved failed task call loaded through the REST adapter keeps the server reason', () => {
  it('update_task: wire status "error" + error "task not found: task_99" + no result -> D8 sentence and the Open tasks link', async () => {
    const tc = await loadThroughRest(wireCall('update_task', { error: 'task not found: task_99' }))
    renderSaved(tc)
    expectKnownReasonFailure('update', 'task not found: task_99')
  })

  it('control: the same call with NO error and NO result still gets the no-reason sentence (D5)', async () => {
    const tc = await loadThroughRest(wireCall('update_task'))
    renderSaved(tc)
    const sentence = screen.getByText("I couldn't update the task, and no reason was given. Try again.")
    expect(sentence.closest('[role="alert"]')).not.toBeNull()
    // The instrument tells the two outcomes apart: the D8 framing is absent.
    expect(screen.queryByText(/^I couldn't update the task\. /)).toBeNull()
    // Round 5b / Q1 A: the no-reason failure keeps the "Open tasks" link too
    // (TASK_ARGS names workspace_id, so the workspace tasks panel).
    const open = screen.getByTestId('task-tool-link-open')
    expect((open.textContent ?? '').trim()).toBe('Open tasks')
    expect(open).toHaveAttribute('href', WORKSPACE_TASKS_HREF)
  })

  it('control: a SUCCESSFUL saved call over the same path renders the card, so the harness can reach it', async () => {
    const tc = await loadThroughRest(
      wireCall('update_task', {
        status: 'success',
        result: { text: JSON.stringify({ task_id: 'task_99', status: 'inbox', updated_fields: ['title'] }) },
      }),
    )
    renderSaved(tc)
    const card = screen.getByTestId('task-tool-card')
    expect((within(card).getByTestId('task-tool-verb').textContent ?? '').trim()).toBe('I updated the task')
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  // The reason text is the server's, so each row uses a different, realistic
  // text: lower-case start, an internal id, quotes, a trailing full stop, a
  // semicolon. A card that capitalises, trims, hides ids or rewrites fails.
  it.each([
    ['create_task', 'create', 'agent "agent_zz" is not a member of this workspace.'],
    ['update_task', 'update', 'task not found: task_99'],
    [
      'run_task',
      'start',
      'task "task_99" is blocked on an unmet dependency; it becomes runnable automatically once its blockers complete',
    ],
    ['delete_task', 'delete', 'Task task_99 was already deleted'],
  ])('%s: a stored error is shown after "I couldn\'t %s the task." with the reason unchanged', async (tool, verb, reason) => {
    const tc = await loadThroughRest(wireCall(tool, { error: reason }))
    renderSaved(tc)
    expectKnownReasonFailure(verb, reason)
  })
})

describe('5a — a reason carried only in a {text} result is still a reason (D5/D8), on the saved path', () => {
  it('update_task: wire status "error", no error field, result { text: "task not found: task_99" } -> D8', async () => {
    const tc = await loadThroughRest(wireCall('update_task', { result: { text: 'task not found: task_99' } }))
    renderSaved(tc)
    expectKnownReasonFailure('update', 'task not found: task_99')
  })

  it.each([
    ['create_task', 'create', 'workspace "ws_7" does not exist'],
    ['run_task', 'start', 'task "task_99" has no assigned agent; assign one before running it'],
    ['delete_task', 'delete', 'Task task_99 is running and cannot be deleted'],
  ])('%s: a {text}-only reason is framed for the %s action', async (tool, verb, reason) => {
    const tc = await loadThroughRest(wireCall(tool, { result: { text: reason } }))
    renderSaved(tc)
    expectKnownReasonFailure(verb, reason)
  })
})

describe('D5 — the reason field of a structured delegation_denied failure is a reason, on the saved path', () => {
  // Built from the GENERATED DelegationFailure type (asyncapi-types.ts), not
  // from anything the renderer reads.
  const DENIAL: DelegationFailure = {
    error: 'delegation_denied',
    reason: 'Scout is not in your trust set, so you cannot start its task.',
    policy: 'trust_set',
    tool: 'run_task',
    target_agent_id: 'scout',
  }

  it('run_task: status "error", no error field, result = the structured failure -> D8 with the denial reason', async () => {
    const tc = await loadThroughRest(wireCall('run_task', { result: { ...DENIAL } }))
    renderSaved(tc)
    expectKnownReasonFailure('start', DENIAL.reason)
  })
})

describe('Round 5b / Q2 C — a saved task call that names no workspace, on the REST path', () => {
  // Parameters in the shape the real tools take, with NO workspace_id: run_task and
  // delete_task take only task_id (pkg/tools/run_task.go::TaskRunTool.Parameters,
  // pkg/tools/task.go::TaskDeleteTool.Parameters); create_task and update_task take
  // workspace_id as optional (absent or null = the agent's own workspace). The
  // failure rows pin the alert text and the link, not the D8 framing (the describes
  // above own that). TODAY the REST adapter drops a stored error
  // (src/lib/api/sessions.ts::rawToToolCall sets `error: undefined`), so a stored-error
  // card shows the D5 sentence and no link: the stored-error rows therefore assert the
  // link BEFORE the reason, so they go red at the missing link first (the link is the
  // rule under test) and are held to the reason only after that (the R2-1 rows above
  // own the adapter gap). The no-reason and success rows go red for the link alone.
  const NO_WORKSPACE_ROWS = [
    {
      tool: 'create_task',
      parameters: { title: 'Weekly report', prompt: 'Write the weekly report', agent_id: 'agent_mia' },
      reason: 'agent "agent_zz" is not a member of this workspace.',
      noReason: "I couldn't create the task, and no reason was given. Try again.",
      payload: { task_id: 'task_99', status: 'inbox' },
    },
    {
      tool: 'update_task',
      parameters: { task_id: 'task_99', title: 'Weekly report' },
      reason: 'task not found: task_99',
      noReason: "I couldn't update the task, and no reason was given. Try again.",
      payload: { task_id: 'task_99', status: 'inbox', updated_fields: ['title'] },
    },
    {
      tool: 'run_task',
      parameters: { task_id: 'task_99' },
      reason: 'task "task_99" is blocked on an unmet dependency; it becomes runnable automatically once its blockers complete',
      noReason: "I couldn't start the task, and no reason was given. Try again.",
      payload: { task_id: 'task_99', status: 'running', session_id: 'sess_1' },
    },
    {
      tool: 'delete_task',
      parameters: { task_id: 'task_99' },
      reason: 'Task task_99 was already deleted',
      noReason: "I couldn't delete the task, and no reason was given. Try again.",
      payload: { deleted: 'task_99' },
    },
  ]

  function expectTasksIndexLinkAndNoCalendar(): void {
    const open = screen.getByTestId('task-tool-link-open')
    expect((open.textContent ?? '').trim()).toBe('Open tasks')
    expect(open).toHaveAttribute('href', TASKS_INDEX_HREF)
    expect(screen.queryByTestId('task-tool-link-calendar')).toBeNull()
  }

  it.each(NO_WORKSPACE_ROWS)(
    '$tool: a stored error and no workspace_id -> the alert carries the reason, "Open tasks" links to /tasks, no calendar link',
    async ({ tool, parameters, reason }) => {
      const tc = await loadThroughRest(wireCall(tool, { parameters, error: reason }))
      renderSaved(tc)
      expectTasksIndexLinkAndNoCalendar()
      expect(screen.getByRole('alert').textContent ?? '').toContain(reason)
      expect(screen.queryByTestId('tool-call-badge')).toBeNull()
    },
  )

  it.each(NO_WORKSPACE_ROWS)(
    '$tool: no error, no result and no workspace_id -> the D5 sentence, "Open tasks" links to /tasks, no calendar link',
    async ({ tool, parameters, noReason }) => {
      const tc = await loadThroughRest(wireCall(tool, { parameters }))
      renderSaved(tc)
      expect((screen.getByRole('alert').textContent ?? '').trim()).toBe(noReason)
      expect(screen.queryByTestId('tool-call-badge')).toBeNull()
      expectTasksIndexLinkAndNoCalendar()
    },
  )

  // TODAY a successful card shows its links only when the call's parameters carry
  // workspace_id, so each success row is red for the missing /tasks link.
  it.each(NO_WORKSPACE_ROWS)(
    '$tool: a successful saved call with no workspace_id -> the card, "Open tasks" links to /tasks, no calendar link',
    async ({ tool, parameters, payload }) => {
      const tc = await loadThroughRest(
        wireCall(tool, { parameters, status: 'success', result: { text: JSON.stringify(payload) } }),
      )
      renderSaved(tc)
      const card = screen.getByTestId('task-tool-card')
      const open = within(card).getByTestId('task-tool-link-open')
      expect((open.textContent ?? '').trim()).toBe('Open tasks')
      expect(open).toHaveAttribute('href', TASKS_INDEX_HREF)
      expect(within(card).queryByTestId('task-tool-link-calendar')).toBeNull()
    },
  )
})

// ── Round 6 / R3-2 — a saved failed call that names a workspace offers "Open tasks" only, on the REST path ──
//
// CHECK3 (R3-2, mutant m08): switching the failure view's calendar link on left every test green,
// because the calendar-absence checks above sit on calls that name NO workspace, where the card's own
// workspace guard hides the link whatever the failure view asks for. The accepted failure view is the
// alert followed by ONE action — dev2-report.md, "Two choices, accepted by the coordinator" (12:59Z):
// "A failed call shows only 'Open tasks', with no 'Open calendar', even when it names a workspace" —
// reaffirmed in the CHECK3 dispatch and the round-6 brief. Every call here carries TASK_ARGS, which
// names workspace_id (create_task and update_task take it as an optional parameter). The rows fail for
// ANY second action, not just a link with one test id, and they run through the same real path as the
// R2-1 rows (REST page -> fetchSessionMessages -> rawToToolCall -> renderHistoricalToolCall).
describe('Round 6 / R3-2 — a saved failed task call that names a workspace offers "Open tasks" only, on the REST path', () => {
  /** The one action a failed call offers: "Open tasks" at `href`, alone in its group, no calendar link or text anywhere. */
  function expectOnlyOpenTasksAction(href: string): void {
    const open = screen.getByTestId('task-tool-link-open')
    expect((open.textContent ?? '').trim()).toBe('Open tasks')
    expect(open).toHaveAttribute('href', href)
    const actions = Array.from(open.parentElement?.children ?? []).map((el) => (el.textContent ?? '').trim())
    expect(actions, 'R3-2: "Open tasks" is the only action a failed call offers').toEqual(['Open tasks'])
    expect(screen.queryByTestId('task-tool-link-calendar'), 'R3-2: a failed call has no calendar link').toBeNull()
    expect(screen.queryByText(/open calendar/i), 'R3-2: a failed call says nothing about opening the calendar').toBeNull()
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  }

  const NAMED_WORKSPACE_ROWS: Array<{ tool: string; verb: string; reason: string }> = [
    { tool: 'create_task', verb: 'create', reason: 'agent "agent_zz" is not a member of this workspace.' },
    { tool: 'update_task', verb: 'update', reason: 'task not found: task_99' },
  ]

  // The two places a REST page keeps a reason: the call's error field and a {text} result.
  const REASON_CARRIERS: Array<{ carrier: string; shape: (reason: string) => Partial<WireToolCall> }> = [
    { carrier: 'the stored error field', shape: (reason) => ({ error: reason }) },
    { carrier: 'a {text} result', shape: (reason) => ({ result: { text: reason } }) },
  ]
  const WITH_REASON_ROWS = NAMED_WORKSPACE_ROWS.flatMap((row) =>
    REASON_CARRIERS.map((carrier) => ({ ...row, carrier: carrier.carrier, overrides: carrier.shape(row.reason) })),
  )

  it.each(WITH_REASON_ROWS)(
    '$tool, reason in $carrier: the D8 sentence, then "Open tasks" alone',
    async ({ tool, verb, reason, overrides }) => {
      const tc = await loadThroughRest(wireCall(tool, overrides))
      renderSaved(tc)
      expect((screen.getByRole('alert').textContent ?? '').trim()).toBe(`I couldn't ${verb} the task. ${reason}`)
      expectOnlyOpenTasksAction(WORKSPACE_TASKS_HREF)
    },
  )

  it.each(NAMED_WORKSPACE_ROWS)('$tool with no reason: the D5 sentence, then "Open tasks" alone', async ({ tool, verb }) => {
    const tc = await loadThroughRest(wireCall(tool))
    renderSaved(tc)
    expect((screen.getByRole('alert').textContent ?? '').trim()).toBe(
      `I couldn't ${verb} the task, and no reason was given. Try again.`,
    )
    expectOnlyOpenTasksAction(WORKSPACE_TASKS_HREF)
  })

  it('a structured delegation_denied failure that names a workspace offers "Open tasks" alone', async () => {
    // Built from the GENERATED DelegationFailure type (asyncapi-types.ts), as in the D5 describe above.
    const denial: DelegationFailure = {
      error: 'delegation_denied',
      reason: 'Scout is not in your trust set, so you cannot start its task.',
      policy: 'trust_set',
      tool: 'run_task',
      target_agent_id: 'scout',
    }
    const tc = await loadThroughRest(wireCall('run_task', { result: { ...denial } }))
    renderSaved(tc)
    expect((screen.getByRole('alert').textContent ?? '').trim()).toBe(`I couldn't start the task. ${denial.reason}`)
    expectOnlyOpenTasksAction(WORKSPACE_TASKS_HREF)
  })

  it('control: the SAME create call, once it succeeded, does offer "Open calendar" — the rows above can see the link', async () => {
    const tc = await loadThroughRest(
      wireCall('create_task', {
        status: 'success',
        result: { text: JSON.stringify({ task_id: 'task_99', status: 'inbox' }) },
      }),
    )
    renderSaved(tc)
    const card = screen.getByTestId('task-tool-card')
    expect(within(card).getByTestId('task-tool-link-calendar')).toHaveAttribute('href', '/workspaces/ws_1/calendar')
    expect((within(card).getByTestId('task-tool-link-calendar').textContent ?? '').trim()).toBe('Open calendar')
  })
})
