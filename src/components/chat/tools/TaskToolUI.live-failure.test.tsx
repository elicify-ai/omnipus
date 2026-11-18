// TaskToolUI.live-failure.test.tsx — issue #1683, round 5 / R2-2.
//
// CHECK round 2 (R2-2, a blocker): a LIVE task call that the server refuses
// with an explained failure loses its reason on the way to the card. The
// gateway sends a refusal such as run_task's delegation_denied as a
// tool_call_result frame with `result` = the structured failure object and
// `error` = that object's `reason` (pkg/gateway/websocket_forward_hub.go::
// hubToolExecEnd, contracts/asyncapi.yaml DelegationFailure). The store keeps
// both (store/chat/slices/frames.ts::applyToolCallResultFrame), but the native
// live adapter (lib/omnipus-runtime.ts::buildContentParts) hands the registered
// task UI only `result` + `isError`, and the card does not read the structured
// object's `reason` — so it announces "no reason was given" for an explained
// refusal. The existing live-wiring tests call a CAPTURED render function with
// a string, which cannot see that gap.
//
// This file drives the real chain, mocking only the network edge:
//   store.handleFrame(tool_call_start / tool_call_result, built from the
//   GENERATED frame + DelegationFailure types)
//     -> useOmnipusRuntime (the real adapter) -> real OmnipusRuntimeProvider
//     -> real AssistantUI tool registry -> RunTaskToolUI -> the card.
//
// ORACLES (team-lead decisions; never the card's output):
//  - D5 (reaffirmed): the no-reason sentence appears ONLY when no reason
//    exists anywhere; a reason counts when it is in the call's error field, a
//    string result, a {text} result, or the `reason` of a generated structured
//    failure.
//  - D8: with a known reason the card shows "I couldn't <verb> the task.
//    <reason>" (reason unchanged) and keeps the "Open tasks" link. The alert
//    stays role="alert".
//  - Round 5b, Q1 A + Q2 C (team-lead decisions, 2026-11-09 — the link rule):
//    a failed call keeps "Open tasks" with or without a reason; the link goes
//    to /workspaces/<id>/chat?panel=tasks when the call's args name a
//    workspace and to /tasks when they do not (real run_task args carry only
//    task_id); "Open calendar" appears only when workspace_id is present.
//    Round 5b REPLACES the round-5 `link: boolean` flag (false = "the row
//    asserts the sentence alone") with an explicit expected href, so the
//    realistic-args row now pins the /tasks link too (strengthened, nothing removed).
//  - Frame shapes: contracts/asyncapi.yaml (ToolCallStartFrame,
//    ToolCallResultFrame, DelegationFailure); pkg/tools/result.go::
//    DelegationDeniedResult is the producer of the structured object.
//
// Harness: ChatScreen.tool-ui-live-registration.test.tsx (real provider, real
// registry, ws transport stubbed). Registry population is asynchronous, so the
// failure region is awaited with findBy*.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ThreadPrimitive, MessagePrimitive } from '@assistant-ui/react'
import type { ToolCallMessagePartComponent } from '@assistant-ui/react'
import { act, screen, within } from '@testing-library/react'
import { renderWithTaskRouter } from '../../../../tests/task-tool-card/router'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import type { DelegationFailure, ToolCallResultFrame, ToolCallStartFrame } from '@/lib/api/generated/asyncapi-types'
import { GenericToolCall } from './GenericToolCall'
import { OmnipusRuntimeProvider } from '../OmnipusRuntimeProvider'

// WsLifecycle constructs a real WsConnection on mount — stub the transport.
vi.mock('@/lib/ws', () => ({
  WsConnection: class {
    connect() {}
    disconnect() {}
    send() {
      return false
    }
  },
}))

vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {},
  getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))

const SID = 'sess_live_task_failure'
const CALL_ID = 'call_live_task_1'

// jsdom has no ResizeObserver / Element.scrollTo; AssistantUI needs both (same
// stubs as the registration test, and a class — not undefined).
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
;(globalThis as unknown as { ResizeObserver?: unknown }).ResizeObserver = ResizeObserverStub
if (!Element.prototype.scrollTo) {
  Element.prototype.scrollTo = () => {}
  Element.prototype.scrollBy = () => {}
}

beforeEach(() => {
  useConnectionStore.setState({
    isConnected: true,
    liteMode: false,
    reconnectPhase: null,
    reconnectAttempt: 0,
    connectionError: null,
    connection: null,
  })
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  act(() => {
    useChatStore.getState().resetSession()
  })
})

afterEach(() => {
  vi.restoreAllMocks()
})

// ── fixtures ────────────────────────────────────────────────────────────────
// The structured refusal pkg/tools/result.go::DelegationDeniedResult builds,
// typed by the generated contract.
const DENIAL: DelegationFailure = {
  error: 'delegation_denied',
  reason: 'Scout is not in your trust set, so you cannot start its task.',
  policy: 'trust_set',
  tool: 'run_task',
  target_agent_id: 'scout',
}
const ARGS_WITH_WORKSPACE = { task_id: 'task_1', workspace_id: 'ws_1' }
const ARGS_REAL_RUN_TASK = { task_id: 'task_1' } // run_task's real schema: task_id only
// Round 5b / Q2 C: the link targets the decisions name, as literals.
const WORKSPACE_TASKS_HREF = '/workspaces/ws_1/chat?panel=tasks'
const TASKS_INDEX_HREF = '/tasks'

function startFrame(tool: string, params: Record<string, unknown>): ToolCallStartFrame {
  return { type: 'tool_call_start', session_id: SID, tool, call_id: CALL_ID, params }
}

function resultFrame(
  tool: string,
  result: unknown,
  status: ToolCallResultFrame['status'],
  error?: string,
): ToolCallResultFrame {
  return {
    type: 'tool_call_result',
    session_id: SID,
    tool,
    call_id: CALL_ID,
    result,
    status,
    ...(error !== undefined ? { error } : {}),
  }
}

function sendFrames(frames: Array<ToolCallStartFrame | ToolCallResultFrame>): void {
  act(() => {
    for (const frame of frames) useChatStore.getState().handleFrame(frame)
  })
}

// The live bubble mirrors ChatScreen's AssistantMessage parts config: the
// Fallback is GenericToolCall, so an unregistered tool would render the
// generic chip while a registered one pre-empts it.
async function renderLiveThread(): Promise<void> {
  await renderWithTaskRouter(
    <OmnipusRuntimeProvider>
      <ThreadPrimitive.Root>
        <ThreadPrimitive.Viewport>
          <ThreadPrimitive.Messages>
            {({ message }: { message: { role: string } }) =>
              message.role === 'assistant' ? (
                <MessagePrimitive.Root>
                  <MessagePrimitive.Parts
                    components={{ tools: { Fallback: GenericToolCall as unknown as ToolCallMessagePartComponent } }}
                  />
                </MessagePrimitive.Root>
              ) : null
            }
          </ThreadPrimitive.Messages>
        </ThreadPrimitive.Viewport>
      </ThreadPrimitive.Root>
    </OmnipusRuntimeProvider>,
  )
}

const NO_REASON_RE = /no reason was given/i
const SUCCESS_HEADING_RE = /^(I (scheduled|created|updated|started|deleted) the task|The task is already running)$/

/** D8: "I couldn't <verb> the task. <reason>" in the alert, the reason unchanged; "Open tasks" kept, pointing at `href`. */
function expectKnownReasonFailure(verb: string, reason: string, href: string): void {
  const sentence = screen.getByText(`I couldn't ${verb} the task. ${reason}`)
  expect(sentence.closest('[role="alert"]'), 'the sentence sits in the catalogued error region (D7/D8)').not.toBeNull()
  const open = screen.getByTestId('task-tool-link-open')
  expect((open.textContent ?? '').trim()).toBe('Open tasks')
  expect(open).toHaveAttribute('href', href)
  expect(screen.queryByText(NO_REASON_RE)).toBeNull()
  expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  expect(screen.queryByText(SUCCESS_HEADING_RE)).toBeNull()
}

describe('R2-2 — a live run_task refused by delegation policy keeps its reason (D5/D8)', () => {
  it('Result = the structured failure, Error = its reason (as the gateway sends it) -> D8 with the denial reason and the Open tasks link', async () => {
    sendFrames([
      startFrame('run_task', ARGS_WITH_WORKSPACE),
      resultFrame('run_task', DENIAL, 'error', DENIAL.reason),
    ])
    // Instrument: the store holds the call exactly as the frame carried it —
    // the structured result AND the error — so what the card shows can only
    // depend on how the live adapter and the registered UI use them.
    expect(useChatStore.getState().toolCalls[CALL_ID]).toMatchObject({
      status: 'error',
      result: DENIAL,
      error: DENIAL.reason,
    })

    await renderLiveThread()
    // The failure region is mounted by the REGISTERED task UI (a generic chip
    // would have no alert), so the sentence checks below are about the card.
    await screen.findByRole('alert')
    expectKnownReasonFailure('start', DENIAL.reason, WORKSPACE_TASKS_HREF)
  })

  it('realistic run_task args (task_id only): the denial reason is shown, framed as D8, with the /tasks link (Round 5b / Q2 C)', async () => {
    sendFrames([
      startFrame('run_task', ARGS_REAL_RUN_TASK),
      resultFrame('run_task', DENIAL, 'error', DENIAL.reason),
    ])
    await renderLiveThread()
    await screen.findByRole('alert')
    expectKnownReasonFailure('start', DENIAL.reason, TASKS_INDEX_HREF)
    // Q2 C: the call names no workspace, so there is no calendar link.
    expect(screen.queryByTestId('task-tool-link-calendar')).toBeNull()
  })

  it('a plain-text refusal (the gateway sends the text as the result and no error) -> D8 with that text', async () => {
    const reason = 'task "task_1" has no assigned agent; assign one before running it'
    sendFrames([startFrame('run_task', ARGS_WITH_WORKSPACE), resultFrame('run_task', reason, 'error')])
    await renderLiveThread()
    await screen.findByRole('alert')
    expectKnownReasonFailure('start', reason, WORKSPACE_TASKS_HREF)
  })
})

describe('R2-2 controls — the harness tells a known reason from none, and a failure from a success', () => {
  it('control: a failed run_task with an empty result and no error still gets the no-reason sentence (D5)', async () => {
    sendFrames([startFrame('run_task', ARGS_WITH_WORKSPACE), resultFrame('run_task', '', 'error')])
    await renderLiveThread()
    const alert = await screen.findByRole('alert')
    expect((alert.textContent ?? '').trim()).toContain("I couldn't start the task, and no reason was given. Try again.")
    expect(screen.queryByText(/^I couldn't start the task\. /)).toBeNull()
    // Round 5b / Q1 A: the no-reason failure keeps the "Open tasks" link too
    // (ARGS_WITH_WORKSPACE names workspace_id, so the workspace tasks panel).
    const open = screen.getByTestId('task-tool-link-open')
    expect((open.textContent ?? '').trim()).toBe('Open tasks')
    expect(open).toHaveAttribute('href', WORKSPACE_TASKS_HREF)
  })

  it('control: a successful live create_task renders the card through the same chain, not the generic chip', async () => {
    sendFrames([
      startFrame('create_task', {
        title: 'Weekly report',
        workspace_id: 'ws_1',
        trigger: { type: 'manual', config: {} },
      }),
      resultFrame('create_task', JSON.stringify({ task_id: 'task_1', status: 'inbox' }), 'success'),
    ])
    await renderLiveThread()
    const card = await screen.findByTestId('task-tool-card')
    expect((within(card).getByTestId('task-tool-verb').textContent ?? '').trim()).toBe('I created the task')
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })
})

// Round 5b / Q2 C (team-lead decisions Q1 A and Q2 C, 2026-11-09), through the real
// live chain: a call whose args name no workspace links "Open tasks" to /tasks and
// shows no calendar link. These rows go red for the missing link alone — the
// sentence assertions below already hold today.
describe('Round 5b / Q2 C — a live task call that names no workspace', () => {
  it('a failed run_task (real args: task_id only) with no reason -> the D5 sentence, "Open tasks" links to /tasks, no calendar link', async () => {
    sendFrames([startFrame('run_task', ARGS_REAL_RUN_TASK), resultFrame('run_task', '', 'error')])
    await renderLiveThread()
    const alert = await screen.findByRole('alert')
    expect((alert.textContent ?? '').trim()).toContain("I couldn't start the task, and no reason was given. Try again.")
    const open = screen.getByTestId('task-tool-link-open')
    expect((open.textContent ?? '').trim()).toBe('Open tasks')
    expect(open).toHaveAttribute('href', TASKS_INDEX_HREF)
    expect(screen.queryByTestId('task-tool-link-calendar')).toBeNull()
  })

  it('a successful create_task whose args name no workspace -> the card, "Open tasks" links to /tasks, no calendar link', async () => {
    sendFrames([
      startFrame('create_task', { title: 'Weekly report', trigger: { type: 'manual', config: {} } }),
      resultFrame('create_task', JSON.stringify({ task_id: 'task_1', status: 'inbox' }), 'success'),
    ])
    await renderLiveThread()
    const card = await screen.findByTestId('task-tool-card')
    const open = within(card).getByTestId('task-tool-link-open')
    expect((open.textContent ?? '').trim()).toBe('Open tasks')
    expect(open).toHaveAttribute('href', TASKS_INDEX_HREF)
    expect(within(card).queryByTestId('task-tool-link-calendar')).toBeNull()
  })

  it('a successful run_task (real args: task_id only) -> the card, "Open tasks" links to /tasks, no calendar link', async () => {
    sendFrames([
      startFrame('run_task', ARGS_REAL_RUN_TASK),
      resultFrame('run_task', JSON.stringify({ task_id: 'task_1', status: 'running', session_id: 'sess_1' }), 'success'),
    ])
    await renderLiveThread()
    const card = await screen.findByTestId('task-tool-card')
    expect((within(card).getByTestId('task-tool-verb').textContent ?? '').trim()).toBe('I started the task')
    const open = within(card).getByTestId('task-tool-link-open')
    expect((open.textContent ?? '').trim()).toBe('Open tasks')
    expect(open).toHaveAttribute('href', TASKS_INDEX_HREF)
    expect(within(card).queryByTestId('task-tool-link-calendar')).toBeNull()
  })
})

// ── Round 6 / R3-1 — the call's error field is the ONLY reason (result null) ─────────────────
//
// CHECK3 (R3-1): every failing row above supplies the reason in `result` as well as, or instead
// of, the call's `error` — the gateway's delegation_denied frame carries it in BOTH (result.reason
// and error), the plain-text row in `result` only — so removing the live adapter's hand-over of
// the call's recorded error (lib/omnipus-runtime.ts::buildContentParts, the `omnipusToolError`
// artifact the registered task UI reads) left every row green. A frame can carry the reason in
// `error` ALONE: the contract allows `result: null` on an error result
// (contracts/components/schemas/ToolCallResultFrame.yaml) and pkg/gateway/replay.go::
// buildResultFrame / applyPersistedFailureReason keep a plain persisted reason in Error without
// populating Result. D5 names the call's error field as a reason on its own, REST or live. The
// rows below send exactly that frame through the same real chain and pin the D8 sentence, the
// alert and the task link; the duplicated-reason rows above stay as the realistic controls.
describe('Round 6 / R3-1 — a live failure whose only reason is the call\'s error field (result null)', () => {
  const ERROR_ONLY_ROWS: Array<{ tool: string; verb: string; args: Record<string, unknown>; reason: string; href: string }> = [
    {
      tool: 'create_task',
      verb: 'create',
      args: { title: 'Weekly report', workspace_id: 'ws_1' },
      reason: 'agent "agent_zz" is not a member of this workspace.',
      href: WORKSPACE_TASKS_HREF,
    },
    {
      tool: 'update_task',
      verb: 'update',
      args: { task_id: 'task_99', title: 'Weekly report', workspace_id: 'ws_1' },
      reason: 'task not found: task_99',
      href: WORKSPACE_TASKS_HREF,
    },
    {
      tool: 'run_task',
      verb: 'start',
      args: ARGS_REAL_RUN_TASK,
      reason: 'task "task_99" is blocked on an unmet dependency; it becomes runnable automatically once its blockers complete',
      href: TASKS_INDEX_HREF,
    },
    { tool: 'delete_task', verb: 'delete', args: { task_id: 'task_99' }, reason: 'Task task_99 was already deleted', href: TASKS_INDEX_HREF },
  ]

  it.each(ERROR_ONLY_ROWS)(
    '$tool: result null + error = the reason -> the D8 sentence in the alert and the Open tasks link',
    async ({ tool, verb, args, reason, href }) => {
      sendFrames([startFrame(tool, args), resultFrame(tool, null, 'error', reason)])
      // Instrument: the store holds the call exactly as the frame carried it — result null (not
      // undefined, not a string) and the reason in `error` alone — so what the card shows can
      // depend on nothing but how the live adapter hands the call's error over.
      expect(useChatStore.getState().sessionsById[SID]?.toolCalls[CALL_ID]).toMatchObject({ status: 'error', result: null, error: reason })

      await renderLiveThread()
      const alert = await screen.findByRole('alert')
      expect(
        (alert.textContent ?? '').trim(),
        'R3-1: with result null, the call\'s error field is the reason the card shows (D5/D8)',
      ).toBe(`I couldn't ${verb} the task. ${reason}`)
      expectKnownReasonFailure(verb, reason, href)
    },
  )

  it('control: the same frame with no error either still gets the no-reason sentence (D5) — the rows above can tell the two apart', async () => {
    sendFrames([
      startFrame('update_task', { task_id: 'task_99', title: 'Weekly report', workspace_id: 'ws_1' }),
      resultFrame('update_task', null, 'error'),
    ])
    expect(useChatStore.getState().sessionsById[SID]?.toolCalls[CALL_ID]).toMatchObject({ status: 'error', result: null })
    await renderLiveThread()
    const alert = await screen.findByRole('alert')
    expect((alert.textContent ?? '').trim()).toBe("I couldn't update the task, and no reason was given. Try again.")
    expect(screen.queryByText(/^I couldn't update the task\. /)).toBeNull()
  })
})

// ── Round 6 / R3-2 — a live failed call that names a workspace offers "Open tasks" only ──────
//
// CHECK3 (R3-2): the accepted failure view is the alert followed by ONE action, "Open tasks"
// (dev2-report.md, "Two choices, accepted by the coordinator" 12:59Z: "A failed call shows only
// 'Open tasks', with no 'Open calendar', even when it names a workspace"; reaffirmed in the
// CHECK3 dispatch). Every calendar-absence check in this file above is on a call that names NO
// workspace, where the card's workspace guard hides the link on its own, so switching the failure
// view's calendar link on passed unnoticed (CHECK3 mutant m08). These rows use calls that DO
// name a workspace (create_task / update_task take workspace_id as an optional parameter), with
// a reason and without one.
describe('Round 6 / R3-2 — a live failed call that names a workspace offers "Open tasks" only', () => {
  /** The one action a failed call offers: "Open tasks" at `href`, alone in its group, with no calendar link or text anywhere. */
  function expectOnlyOpenTasksAction(href: string): void {
    const open = screen.getByTestId('task-tool-link-open')
    expect((open.textContent ?? '').trim()).toBe('Open tasks')
    expect(open).toHaveAttribute('href', href)
    const actions = Array.from(open.parentElement?.children ?? []).map((el) => (el.textContent ?? '').trim())
    expect(actions, 'R3-2: "Open tasks" is the only action a failed call offers').toEqual(['Open tasks'])
    expect(screen.queryByTestId('task-tool-link-calendar'), 'R3-2: a failed call has no calendar link').toBeNull()
    expect(screen.queryByText(/open calendar/i), 'R3-2: a failed call says nothing about opening the calendar').toBeNull()
  }

  const NAMED_WORKSPACE_ROWS: Array<{ tool: string; verb: string; args: Record<string, unknown>; reason: string }> = [
    {
      tool: 'create_task',
      verb: 'create',
      args: { title: 'Weekly report', workspace_id: 'ws_1' },
      reason: 'agent "agent_zz" is not a member of this workspace.',
    },
    {
      tool: 'update_task',
      verb: 'update',
      args: { task_id: 'task_99', title: 'Weekly report', workspace_id: 'ws_1' },
      reason: 'task not found: task_99',
    },
  ]

  it.each(NAMED_WORKSPACE_ROWS)(
    '$tool with a reason: the D8 sentence, then "Open tasks" alone',
    async ({ tool, verb, args, reason }) => {
      sendFrames([startFrame(tool, args), resultFrame(tool, reason, 'error')])
      await renderLiveThread()
      const alert = await screen.findByRole('alert')
      expect((alert.textContent ?? '').trim()).toBe(`I couldn't ${verb} the task. ${reason}`)
      expectOnlyOpenTasksAction(WORKSPACE_TASKS_HREF)
    },
  )

  it.each(NAMED_WORKSPACE_ROWS)(
    '$tool with no reason: the D5 sentence, then "Open tasks" alone',
    async ({ tool, verb, args }) => {
      sendFrames([startFrame(tool, args), resultFrame(tool, '', 'error')])
      await renderLiveThread()
      const alert = await screen.findByRole('alert')
      expect((alert.textContent ?? '').trim()).toBe(`I couldn't ${verb} the task, and no reason was given. Try again.`)
      expectOnlyOpenTasksAction(WORKSPACE_TASKS_HREF)
    },
  )
})
