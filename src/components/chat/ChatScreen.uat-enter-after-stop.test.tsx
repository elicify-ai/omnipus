import { AssistantRuntimeProvider } from '@assistant-ui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ErrorFrame } from '@/lib/api/generated/asyncapi-types'
import { codeToDisplay } from '@/lib/llm-error'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import type { WsConnection } from '@/lib/ws'
import { getMessages, useChatStore } from '@/store/chat'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { useConnectionStore } from '@/store/connection'
import { useJudgeActivityStore } from '@/store/judgeActivity'
import { useSessionStore } from '@/store/session'
import { useToolApprovalStore } from '@/store/toolApproval'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { ChatScreen } from './ChatScreen'

// Oracle: release-UAT dispatch D3, validator/report-v.md V5/V5b / D5.
// Enter after Stop must SEND "continue and say what you did" in the selected
// parent chat, not open a helper transcript or leave the text unsent (2/2 UAT).
// Source: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/evidence/2026-10-06-release.
// REAL: ChatScreen, composer, AssistantUI, runtime adapter, all stores/actions,
// frame routing, delegation/activity rendering, query provider and router.
// FAKE: socket/fetch process edges and jsdom's unavailable scrolling.
// PlainMessageList is the real no-ResizeObserver fallback: browser layout,
// virtualization, full app route loaders and backend admission remain gaps.
// A PASS on the base must be reported, never bent into RED. GREEN certification
// and the skill's Proof-of-failability/mutation checklist belong to CHECK.

const PARENT = 'session-uat-d3-enter-parent'
const CHILD = 'session-uat-d3-enter-helper'
const WORKSPACE = 'uat-d3-enter-workspace'
const INITIAL_TEXT = 'Delegate one helper and report progress.'
const INITIAL_ID = 'uat-d3-enter-first-user'
const PARTIAL_TEXT = 'Checking helper progress.'
const CHILD_TEXT = 'Helper transcript: drafting the long document.'
const CONTINUATION = 'continue and say what you did'
const STOP_COPY = codeToDisplay.turn_canceled
// Let the existing useCancelState minimum display duration finish. This is
// scenario setup, not an elapsed-time assertion or an expanded test deadline.
const STOP_LABEL_DISPLAY_MS = 1000

let client: QueryClient
const sender = { send: vi.fn<WsConnection['send']>() }
let originalScrollIntoView: PropertyDescriptor | undefined

function resetStores() {
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useChatPreferencesStore.setState(useChatPreferencesStore.getInitialState(), true)
    useJudgeActivityStore.setState(useJudgeActivityStore.getInitialState(), true)
    useToolApprovalStore.setState(useToolApprovalStore.getInitialState(), true)
    useUiStore.setState(useUiStore.getInitialState(), true)
    localStorage.clear()
    sessionStorage.clear()
  })
}

beforeEach(() => {
  resetStores()
  sender.send.mockReset().mockReturnValue(true)
  vi.stubGlobal('ResizeObserver', undefined)
  originalScrollIntoView = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollIntoView')
  Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
    configurable: true, writable: true, value: vi.fn(),
  })
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  })
  for (const key of [['commands', 'web'], ['agents'], ['skills'], ['providers'], ['workspaces'], ['messages', PARENT]]) {
    client.setQueryData(key, [])
  }
  // Optional picker HTTP requests are outside D3; fail only at fetch so their
  // real query/error handling stays intact. No API module/hook is mocked.
  vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockRejectedValue(new Error('D3 fixture: optional HTTP is offline.')))
  act(() => {
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
    useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE })
  })
})

afterEach(() => {
  cleanup()
  client.clear()
  resetStores()
  vi.unstubAllGlobals()
  if (originalScrollIntoView) Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', originalScrollIntoView)
  else Reflect.deleteProperty(HTMLElement.prototype, 'scrollIntoView')
})

function seedParentAndHelper() {
  act(() => {
    // A helper already visited by this browser has its own real bucket. Its
    // scoped frames must never become the foreground parent's transcript.
    useSessionStore.getState().setActiveSession(CHILD, 'worker')
    useChatStore.getState().handleFrame({ type: 'token', session_id: CHILD, agent_id: 'worker', content: CHILD_TEXT })
    useSessionStore.getState().setActiveSession(PARENT, 'jim')
    useChatStore.getState().sendMessage(INITIAL_TEXT, { clientMessageId: INITIAL_ID })
    useChatStore.getState().handleFrame({ type: 'token', session_id: PARENT, agent_id: 'jim', content: PARTIAL_TEXT })
    useChatStore.getState().handleFrame({
      type: 'tool_call_start', session_id: PARENT, call_id: 'uat-d3-delegate-call', tool: 'delegate', params: {}, agent_id: 'jim',
    })
    useChatStore.getState().handleFrame({
      type: 'subagent_start', session_id: PARENT, child_session_id: CHILD,
      span_id: 'uat-d3-helper-span', parent_call_id: 'uat-d3-delegate-call', task_label: 'Write the long document', agent_id: 'worker',
    })
    useChatStore.getState().handleFrame({
      type: 'subagent_state', session_id: PARENT, child_session_id: CHILD, span_id: 'uat-d3-helper-span', state: 'running', created_at: new Date().toISOString(),
    })
  })
  expect(useSessionStore.getState().activeSessionId, 'fixture: selected chat is the parent, not the previously visited helper').toBe(PARENT)
  expect(getMessages(useChatStore.getState().sessionsById[CHILD]).map((message) => message.content),
    'fixture: distinguishable helper transcript exists before Enter').toStrictEqual([CHILD_TEXT])
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: initial prompt went through the real send path').toStrictEqual([
    {
      type: 'message', session_id: PARENT, content: INITIAL_TEXT, client_message_id: INITIAL_ID,
      agent_id: 'jim', metadata: { workspace_id: WORKSPACE },
    },
  ])
  sender.send.mockClear()
}

function RealScreen() {
  const runtime = useOmnipusRuntime()
  return <QueryClientProvider client={client}><AssistantRuntimeProvider runtime={runtime}><ChatScreen /></AssistantRuntimeProvider></QueryClientProvider>
}

async function renderRealScreen() {
  const root = createRootRoute({ component: RealScreen })
  const router = createRouter({ routeTree: root, history: createMemoryHistory({ initialEntries: ['/'] }) })
  await act(async () => {
    await router.load()
    render(<RouterProvider router={router} />)
  })
  expect(await screen.findByText(PARTIAL_TEXT, { exact: true }), 'instrument: real parent transcript was rendered').toBeVisible()
  expect(screen.queryByText(CHILD_TEXT, { exact: true }), 'instrument: helper transcript is not selected').not.toBeInTheDocument()
  expect(screen.getByTestId('activity-bar'), 'instrument: real helper activity control is present').toBeEnabled()
  expect(screen.getByRole('combobox', { name: 'Message input' }), 'instrument: real composer is interactive').toBeEnabled()
  return router
}

function acknowledgeStop(tree: boolean) {
  const stopped: ErrorFrame = {
    type: 'error', session_id: PARENT, message: STOP_COPY,
    payload: { llm_error: { code: 'turn_canceled', message: STOP_COPY, retryable: true } },
  }
  act(() => {
    useChatStore.getState().handleFrame(stopped)
    useChatStore.getState().handleFrame({ type: 'done', session_id: PARENT })
    if (tree) {
      useChatStore.getState().handleFrame({ ...stopped, session_id: CHILD })
      useChatStore.getState().handleFrame({ type: 'done', session_id: CHILD })
      useChatStore.getState().handleFrame({
        type: 'subagent_state', session_id: PARENT, child_session_id: CHILD, span_id: 'uat-d3-helper-span', state: 'stopped', created_at: new Date().toISOString(),
      })
      useChatStore.getState().handleFrame({
        type: 'subagent_end', session_id: PARENT, span_id: 'uat-d3-helper-span', status: 'interrupted', reason: 'parent_cancelled',
      })
    }
  })
  expect(useChatStore.getState().isStreaming, 'fixture: Stop landed, so Enter is an ordinary new-message send').toBe(false)
  expect(useChatStore.getState().messages.filter((message) => message.content === PARTIAL_TEXT)
    .map((message) => message.status), 'fixture: the stopped parent answer is retained as interrupted').toStrictEqual(['interrupted'])
}

describe('release UAT D3 — Enter after Stop sends in the selected parent chat', () => {
  it.each([
    { id: 'D3-E1', stopName: 'Stop generation', tree: false },
    { id: 'D3-E2', stopName: '/cancel (Stop all)', tree: true },
  ])('$id: Enter after $stopName sends the continuation in this chat instead of opening a helper', async ({ stopName, tree }) => {
    seedParentAndHelper()
    const router = await renderRealScreen()
    const user = userEvent.setup()

    // Founder 2026-10-06: "the button needs to go". Use the remaining
    // immediate-tree /cancel command; every frame and continuation oracle
    // below is unchanged from the removed-button scenario.
    if (tree) {
      act(() => {
        client.setQueryData(['commands', 'web'], [{
          name: 'cancel', label: '/cancel', description: 'Stop this chat and its helpers',
          delivery: 'client', available_while_streaming: true,
        }])
      })
      await user.type(screen.getByRole('combobox', { name: 'Message input' }), '/cancel')
      await user.keyboard('{Enter}')
    } else {
      await user.click(screen.getByRole('button', { name: stopName }))
    }
    expect(sender.send.mock.calls.map(([frame]) => frame), 'instrument: the actual Stop control emitted the intended parent cancel').toStrictEqual([
      tree ? { type: 'cancel', session_id: PARENT, scope: 'tree' } : { type: 'cancel', session_id: PARENT },
    ])
    acknowledgeStop(tree)
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, STOP_LABEL_DISPLAY_MS)) })
    const childBeforeEnter = structuredClone(useChatStore.getState().sessionsById[CHILD])
    sender.send.mockClear()
    const input = screen.getByRole('combobox', { name: 'Message input' })
    expect(input, 'D3: stopped chat must accept another message').toBeEnabled()

    await user.type(input, CONTINUATION)
    await user.keyboard('{Enter}')

    expect(sender.send.mock.calls.map(([frame]) => frame),
      'D3: Enter must send exactly this continuation to Jim in the parent chat; it must not leave it unsent or target the helper').toStrictEqual([
      {
        type: 'message', session_id: PARENT, content: CONTINUATION,
        client_message_id: expect.any(String), agent_id: 'jim', metadata: { workspace_id: WORKSPACE },
      },
    ])
    expect(useSessionStore.getState().activeSessionId, 'D3: Enter is Send, never select-helper').toBe(PARENT)
    expect(router.state.location.pathname, 'D3: Enter must not navigate to the helper transcript').toBe('/')
    expect(useChatStore.getState().messages.filter((message) => message.role === 'user')
      .map(({ content, session_id }) => ({ content, session_id })),
    'D3: continuation appears exactly once after the original user message, in this chat').toStrictEqual([
      { content: INITIAL_TEXT, session_id: PARENT },
      { content: CONTINUATION, session_id: PARENT },
    ])
    expect(screen.getByText(CONTINUATION, { exact: true }), 'D3: real chat thread visibly contains the sent continuation').toBeVisible()
    expect(input, 'D3: Enter must clear the submitted text rather than leave it in the composer').toHaveValue('')
    expect(useChatStore.getState().sessionsById[CHILD], 'D3: Enter in the parent must not modify the helper transcript/state').toStrictEqual(childBeforeEnter)
    expect(screen.queryByText(CHILD_TEXT, { exact: true }), 'D3: helper transcript must remain out of the main pane').not.toBeInTheDocument()
  })

  // Founder Q16 (2026-10-06): after the first Stop the same Stop button holds
  // the Send position for the whole 3 s window even though the turn already
  // ended, and Enter still sends — as a message, never as a second Stop.
  it('Q16: stream ended inside the window, Stop is still shown and Enter still sends (no tree frame)', async () => {
    seedParentAndHelper()
    await renderRealScreen()
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: 'Stop generation' }))
    acknowledgeStop(false)
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, STOP_LABEL_DISPLAY_MS)) })
    expect(screen.getByTestId('stop-btn'), 'Q16: the same Stop button is still present inside the 3 s window').toBeInTheDocument()
    sender.send.mockClear()

    await user.type(screen.getByRole('combobox', { name: 'Message input' }), CONTINUATION)
    await user.keyboard('{Enter}')

    expect(sender.send.mock.calls.map(([frame]) => frame), 'Q16: Enter sends the message; no cancel/tree frame').toStrictEqual([
      {
        type: 'message', session_id: PARENT, content: CONTINUATION,
        client_message_id: expect.any(String), agent_id: 'jim', metadata: { workspace_id: WORKSPACE },
      },
    ])
  })
})
