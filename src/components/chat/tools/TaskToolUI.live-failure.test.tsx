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
//    stays role="alert". The link is expected where the call's args carry the
//    workspace id (the card builds the link from it); real run_task args carry
//    only task_id, so the realistic-args row asserts the sentence alone.
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
import { render, act, screen, within } from '@testing-library/react'
import type { ReactNode } from 'react'
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

// The "Open tasks" link needs a router the unit mount does not have.
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
  await act(async () => {
    render(
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
  })
}

const NO_REASON_RE = /no reason was given/i
const SUCCESS_HEADING_RE = /^(I (scheduled|created|updated|started|deleted) the task|The task is already running)$/

/** D8: "I couldn't <verb> the task. <reason>" in the alert, the reason unchanged; "Open tasks" kept when the args name a workspace. */
function expectKnownReasonFailure(verb: string, reason: string, opts: { link: boolean }): void {
  const sentence = screen.getByText(`I couldn't ${verb} the task. ${reason}`)
  expect(sentence.closest('[role="alert"]'), 'the sentence sits in the catalogued error region (D7/D8)').not.toBeNull()
  if (opts.link) {
    const open = screen.getByTestId('task-tool-link-open')
    expect((open.textContent ?? '').trim()).toBe('Open tasks')
    expect(open).toHaveAttribute('href', '/workspaces/ws_1/chat?panel=tasks')
  }
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
    expectKnownReasonFailure('start', DENIAL.reason, { link: true })
  })

  it('realistic run_task args (task_id only): the denial reason is shown, framed as D8', async () => {
    sendFrames([
      startFrame('run_task', ARGS_REAL_RUN_TASK),
      resultFrame('run_task', DENIAL, 'error', DENIAL.reason),
    ])
    await renderLiveThread()
    await screen.findByRole('alert')
    expectKnownReasonFailure('start', DENIAL.reason, { link: false })
  })

  it('a plain-text refusal (the gateway sends the text as the result and no error) -> D8 with that text', async () => {
    const reason = 'task "task_1" has no assigned agent; assign one before running it'
    sendFrames([startFrame('run_task', ARGS_WITH_WORKSPACE), resultFrame('run_task', reason, 'error')])
    await renderLiveThread()
    await screen.findByRole('alert')
    expectKnownReasonFailure('start', reason, { link: true })
  })
})

describe('R2-2 controls — the harness tells a known reason from none, and a failure from a success', () => {
  it('control: a failed run_task with an empty result and no error still gets the no-reason sentence (D5)', async () => {
    sendFrames([startFrame('run_task', ARGS_WITH_WORKSPACE), resultFrame('run_task', '', 'error')])
    await renderLiveThread()
    const alert = await screen.findByRole('alert')
    expect((alert.textContent ?? '').trim()).toContain("I couldn't start the task, and no reason was given. Try again.")
    expect(screen.queryByText(/^I couldn't start the task\. /)).toBeNull()
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
