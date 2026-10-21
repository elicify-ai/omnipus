// Oracle: founder decision / Chief dispatch, 2026-10-21. Real ChatScreen,
// frame reducer, session buckets and generated Todo validation stay unmocked.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ServerFrame, ToolCallStartFrame, ToolCallResultFrame } from '@/lib/api/generated/asyncapi-types'
import type { Todo } from '@/lib/api/generated/openapi-types'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import { emptySessionState } from '@/store/chat/session'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'

vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock())
vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  useNavigate: () => vi.fn(),
  Link: ({ children }: { children: ReactNode }) => children,
}))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchAgents: vi.fn().mockResolvedValue([]),
  fetchSessionMessages: vi.fn().mockResolvedValue([]),
  fetchAboutInfo: vi.fn().mockResolvedValue({}),
  fetchProviders: vi.fn().mockResolvedValue([]),
  fetchCommands: vi.fn().mockResolvedValue([]),
  fetchSkills: vi.fn().mockResolvedValue([]),
  fetchWorkspaces: vi.fn().mockResolvedValue([]),
}))

import { ChatScreen } from './ChatScreen'

const SID = 'working-on-current'
const OTHER = 'working-on-other'
let client: QueryClient
let callNumber = 0

function feed(frame: ServerFrame) {
  act(() => useChatStore.getState().handleFrame(frame))
}

function startList(todos: Todo[], sessionId = SID, tool = 'set_todos') {
  const callId = `todo-call-${++callNumber}`
  const frame: ToolCallStartFrame = {
    type: 'tool_call_start', session_id: sessionId, agent_id: 'mia',
    tool, call_id: callId, params: { outcome: 'Prepare the report', todos },
  }
  feed(frame)
  return callId
}

function finishList(callId: string, sessionId = SID, status: ToolCallResultFrame['status'] = 'success', tool = 'set_todos') {
  feed({
    type: 'tool_call_result', session_id: sessionId, agent_id: 'mia',
    tool, call_id: callId, status, result: status === 'success' ? 'Checklist saved' : 'Could not save checklist',
  })
  // Positive control: a dropped frame cannot manufacture a passing negative.
  expect(useChatStore.getState().sessionsById[sessionId].toolCalls[callId].status).toBe(status)
}

function setList(todos: Todo[], sessionId = SID) {
  const callId = startList(todos, sessionId)
  finishList(callId, sessionId)
  expect(useChatStore.getState().sessionsById[sessionId].toolCalls[callId].params.todos).toEqual(todos)
}

function activeItem(text: string): Todo[] {
  return [{ text, status: 'in_progress' }]
}

async function renderChat() {
  await act(async () => {
    render(<QueryClientProvider client={client}><ChatScreen /></QueryClientProvider>)
  })
  expect(screen.getByTestId('composer-card')).toBeInTheDocument()
}

function expectLine(text: string) {
  const line = screen.getByTestId('chat-working-on')
  expect(line.textContent).toBe(`Working on: ${text}`)
  expect(screen.getAllByTestId('chat-working-on')).toHaveLength(1)
  expect(line).toHaveAttribute('role', 'status')
  return line
}

beforeEach(() => {
  callNumber = 0
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState({ ...useSessionStore.getInitialState(), activeSessionId: SID, activeAgentId: 'mia' }, true)
  useConnectionStore.setState({ ...useConnectionStore.getInitialState(), isConnected: true, liteMode: false }, true)
  vi.stubGlobal('ResizeObserver', undefined)
  const messages = [{ id: 'request', role: 'user' as const, status: 'done' as const, content: 'Prepare the report', timestamp: '2026-10-21T12:00:00Z' }]
  const bucket = { ...emptySessionState(), ...makeBucketMessages(messages), replayCompletedForSession: SID }
  useChatStore.setState({ sessionsById: { [SID]: bucket }, messages, messagesById: bucket.messagesById, replayCompletedForSession: SID })
  // These are turns in flight, not tool-result stragglers after an idle turn.
  feed({ type: 'token', session_id: SID, content: 'Starting the report', message_id: 'answer', turn_id: 'turn-one' })
  feed({ type: 'token', session_id: OTHER, content: 'Other conversation', message_id: 'other-answer', turn_id: 'other-turn' })
  expect(useChatStore.getState().sessionsById[SID].isStreaming).toBe(true)
  expect(useChatStore.getState().sessionsById[OTHER].isStreaming).toBe(true)
})

afterEach(() => {
  cleanup()
  client.clear()
  vi.unstubAllGlobals()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
})

describe('chat current todo line', () => {
  it('shows only the in-progress item above the composer after set_todos succeeds', async () => {
    await renderChat()
    setList([
      { text: 'Read the brief', status: 'completed' },
      { text: 'Review the sources', status: 'in_progress' },
      { text: 'Write the report', status: 'pending' },
    ])
    const line = expectLine('Review the sources')
    expect(line.compareDocumentPosition(screen.getByTestId('composer-card')) & Node.DOCUMENT_POSITION_FOLLOWING).not.toBe(0)
  })

  it('replaces the visible item when the agent updates its full list', async () => {
    await renderChat()
    setList(activeItem('Review the sources'))
    expectLine('Review the sources')
    setList([{ text: 'Review the sources', status: 'completed' }, { text: 'Write the report', status: 'in_progress' }])
    expectLine('Write the report')
  })

  it.each([
    ['pending', [{ text: 'Write the report', status: 'pending' }]],
    ['completed', [{ text: 'Write the report', status: 'completed' }]],
    ['cleared', []],
  ] satisfies [string, Todo[]][])('hides a previously visible item when the replacement is %s', async (_name, todos) => {
    await renderChat()
    setList(activeItem('Review the sources'))
    expectLine('Review the sources')
    setList(todos)
    expect(screen.queryByTestId('chat-working-on')).not.toBeInTheDocument()
  })

  it('hides on turn completion and does not revive the old list in the next turn', async () => {
    await renderChat()
    setList(activeItem('Review the sources'))
    expectLine('Review the sources')
    feed({ type: 'done', session_id: SID, message_id: 'answer', turn_id: 'turn-one' })
    expect(useChatStore.getState().sessionsById[SID].isStreaming).toBe(false)
    expect(screen.queryByTestId('chat-working-on')).not.toBeInTheDocument()
    feed({ type: 'token', session_id: SID, content: 'A new answer', message_id: 'next-answer', turn_id: 'turn-two' })
    expect(useChatStore.getState().sessionsById[SID].isStreaming).toBe(true)
    expect(screen.queryByTestId('chat-working-on')).not.toBeInTheDocument()
  })

  it('never uses the same agent\'s checklist from another conversation', async () => {
    await renderChat()
    setList(activeItem('Private other-chat item'), OTHER)
    expect(useChatStore.getState().sessionsById[OTHER].isStreaming).toBe(true)
    expect(screen.queryByTestId('chat-working-on')).not.toBeInTheDocument()
    setList(activeItem('Current chat item'))
    expectLine('Current chat item')
    setList(activeItem('Changed other-chat item'), OTHER)
    expectLine('Current chat item')
  })

  it('follows the selected conversation when switching between two live chats of one agent', async () => {
    await renderChat()
    setList(activeItem('Current chat item'))
    setList(activeItem('Other chat item'), OTHER)
    expectLine('Current chat item')
    act(() => useSessionStore.getState().setActiveSession(OTHER, 'mia'))
    expectLine('Other chat item')
    act(() => useSessionStore.getState().setActiveSession(SID, 'mia'))
    expectLine('Current chat item')
  })

  it('stays hidden during a live turn without a checklist', async () => {
    await renderChat()
    feed({ type: 'token', session_id: SID, content: 'Starting the report', turn_id: 'turn-no-list' })
    expect(useChatStore.getState().sessionsById[SID].isStreaming).toBe(true)
    expect(screen.queryByTestId('chat-working-on')).not.toBeInTheDocument()
  })

  it('does not display an unconfirmed or failed checklist write', async () => {
    await renderChat()
    const callId = startList(activeItem('Not saved'))
    expect(screen.queryByTestId('chat-working-on')).not.toBeInTheDocument()
    finishList(callId, SID, 'error')
    expect(screen.queryByTestId('chat-working-on')).not.toBeInTheDocument()
  })

  it('keeps the previous confirmed item when a replacement write fails', async () => {
    await renderChat()
    setList(activeItem('Confirmed item'))
    expectLine('Confirmed item')
    const callId = startList(activeItem('Rejected item'))
    expectLine('Confirmed item')
    finishList(callId, SID, 'error')
    expectLine('Confirmed item')
  })

  it('hides the line while historical frames are being replayed', async () => {
    await renderChat()
    setList(activeItem('Review the sources'))
    expectLine('Review the sources')
    act(() => useChatStore.setState((state) => ({
      sessionsById: { ...state.sessionsById, [SID]: { ...state.sessionsById[SID], isReplaying: true } },
      isReplaying: true,
    })))
    expect(screen.queryByTestId('chat-working-on')).not.toBeInTheDocument()
  })

  it('ignores a different tool even when its arguments contain a todos-shaped value', async () => {
    await renderChat()
    const callId = startList(activeItem('Not a scratchpad'), SID, 'other_tool')
    finishList(callId, SID, 'success', 'other_tool')
    expect(screen.queryByTestId('chat-working-on')).not.toBeInTheDocument()
  })
})
