// Oracle: founder decision / Chief dispatch, 2026-10-21. Real ChatScreen,
// frame reducer, session buckets and generated Todo validation stay unmocked.
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react'
import type { ReactNode } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import type { ServerFrame, ToolCallStartFrame, ToolCallResultFrame } from '@/lib/api/generated/asyncapi-types'
import { Todo as TodoSchema } from '@/lib/api/generated/schemas'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import { emptySessionState } from '@/store/chat/session'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { fetchSessionMessages } from '@/lib/api'
import type { Message } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { ApiError } from '@/lib/api-error'
import { __resetFinishedTurnIdsForTests } from '@/store/chat/frames'

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
// not-wire-format: independent Q10 fixture expectations, not gateway types.
const q10Fixture: { source: string; cases: { name: string; input: string; expected: string; reject: boolean }[] } = JSON.parse(readFileSync(resolve('tests/fixtures/q10-active-form-normalization.json'), 'utf8'))
let callNumber = 0

function feed(frame: ServerFrame) {
  act(() => useChatStore.getState().handleFrame(frame))
}

function startList(todos: ToolCallStartFrame['params']['todos'], sessionId = SID, tool = 'set_todos') {
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

function setList(todos: ToolCallStartFrame['params']['todos'], sessionId = SID) {
  const callId = startList(todos, sessionId)
  finishList(callId, sessionId)
  expect(useChatStore.getState().sessionsById[sessionId].toolCalls[callId].params.todos).toEqual(todos)
  return callId
}

function activeItem(activeForm: string): ToolCallStartFrame['params']['todos'] {
  return [{ text: 'Prepare the report', status: 'in_progress', active_form: activeForm }]
}

async function renderChat() {
  await act(async () => {
    render(<QueryClientProvider client={queryClient}><ChatScreen /></QueryClientProvider>)
  })
  expect(screen.getByTestId('composer-card')).toBeInTheDocument()
}

function expectLine(text: string) {
  const line = screen.getByTestId('reply-current-todo')
  expect(line.textContent).toBe(text)
  expect(screen.getAllByTestId('reply-current-todo')).toHaveLength(1)
  expect(line.closest('[data-testid="reply-status-indicator"]')).not.toBeNull()
  expect(line).toHaveAttribute('aria-hidden', 'true')
  return line
}

beforeEach(() => {
  callNumber = 0
  vi.spyOn(Math, 'random').mockReturnValue(0.2)
  __resetFinishedTurnIdsForTests()
  queryClient.clear()
  vi.mocked(fetchSessionMessages).mockReset().mockResolvedValue([])
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
  queryClient.clear()
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
})

describe('chat current todo in the existing status indicator', () => {
  it('renders the merged backend active_form through accepted frames, reducer and mounted chat', async () => {
    await renderChat()
    const item = TodoSchema.parse({ text: 'Draft the Q4 summary', status: 'in_progress', active_form: 'Drafting the Q4 summary' })
    const callId = startList([item])
    expect(screen.queryByTestId('reply-current-todo')).not.toBeInTheDocument()
    finishList(callId)
    const line = expectLine('Drafting the Q4 summary')
    expect(line.querySelector('[title]')).toHaveAttribute('title', 'Drafting the Q4 summary')
    expect(line.textContent).not.toContain('Draft the Q4 summary')
    expect(screen.getByTestId('composer-card')).toBeInTheDocument()
  })

  it('shows only the in-progress item inside the indicator, never above the composer', async () => {
    await renderChat()
    setList([
      { text: 'Read the brief', status: 'completed' },
      { text: 'Review the sources', status: 'in_progress', active_form: 'Review the sources' },
      { text: 'Write the report', status: 'pending' },
    ])
    expectLine('Review the sources')
    expect(screen.queryByTestId('chat-working-on')).not.toBeInTheDocument()
    expect(screen.getByTestId('composer-card').parentElement?.querySelector('[data-testid="reply-current-todo"]')).toBeNull()
  })

  it('replaces the visible item when the agent updates its full list', async () => {
    await renderChat()
    setList(activeItem('Review the sources'))
    expectLine('Review the sources')
    setList([{ text: 'Review the sources', status: 'completed' }, { text: 'Write the report', status: 'in_progress', active_form: 'Write the report' }])
    expectLine('Write the report')
  })

  it.each([
    ['pending', [{ text: 'Write the report', status: 'pending' }]],
    ['completed', [{ text: 'Write the report', status: 'completed' }]],
    ['cleared', []],
  ] satisfies [string, ToolCallStartFrame['params']['todos']][])('hides a previously visible item when the replacement is %s', async (_name, todos) => {
    await renderChat()
    setList(activeItem('Review the sources'))
    expectLine('Review the sources')
    setList(todos)
    expect(screen.queryByTestId('reply-current-todo')).not.toBeInTheDocument()
  })

  it('hides on turn completion and does not revive the old list in the next turn', async () => {
    await renderChat()
    setList(activeItem('Review the sources'))
    expectLine('Review the sources')
    feed({ type: 'done', session_id: SID, message_id: 'answer', turn_id: 'turn-one' })
    expect(useChatStore.getState().sessionsById[SID].isStreaming).toBe(false)
    expect(screen.queryByTestId('reply-current-todo')).not.toBeInTheDocument()
    feed({ type: 'token', session_id: SID, content: 'A new answer', message_id: 'next-answer', turn_id: 'turn-two' })
    expect(useChatStore.getState().sessionsById[SID].isStreaming).toBe(true)
    expect(screen.queryByTestId('reply-current-todo')).not.toBeInTheDocument()
  })

  it('never uses the same agent\'s checklist from another conversation', async () => {
    await renderChat()
    setList(activeItem('Private other-chat item'), OTHER)
    expect(useChatStore.getState().sessionsById[OTHER].isStreaming).toBe(true)
    expect(screen.queryByTestId('reply-current-todo')).not.toBeInTheDocument()
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
    expect(screen.queryByTestId('reply-current-todo')).not.toBeInTheDocument()
  })

  it('does not display an unconfirmed or failed checklist write', async () => {
    await renderChat()
    const callId = startList(activeItem('Not saved'))
    expect(screen.queryByTestId('reply-current-todo')).not.toBeInTheDocument()
    finishList(callId, SID, 'error')
    expect(screen.queryByTestId('reply-current-todo')).not.toBeInTheDocument()
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
    expect(screen.queryByTestId('reply-current-todo')).not.toBeInTheDocument()
  })

  it('ignores a different tool even when its arguments contain a todos-shaped value', async () => {
    await renderChat()
    const callId = startList(activeItem('Not a scratchpad'), SID, 'other_tool')
    finishList(callId, SID, 'success', 'other_tool')
    expect(screen.queryByTestId('reply-current-todo')).not.toBeInTheDocument()
  })
})

function savedList(turnId: string, text: string, callId: string): Message {
  return {
    id: `saved-${callId}`, role: 'assistant', content: '', status: 'done',
    timestamp: '2026-10-21T12:00:00Z', turnId,
    tool_calls: [{ id: callId, tool: 'set_todos', status: 'success', params: { outcome: 'Prepare the report', todos: activeItem(text) } }],
  }
}

function announceTurn(turnId: string | null, sessionId = SID) {
  feed({
    type: 'session_state', session_id: sessionId, user_id: 'operator', pending_approvals: [],
    emitted_at: '2026-10-21T12:00:30Z',
    ...(turnId ? { active_turn: { turn_id: turnId, agent_id: 'mia', started_at: '2026-10-21T12:00:00Z' } } : {}),
  })
}

function completeCatchUp(mode: 'incremental' | 'snapshot', sessionId = SID) {
  feed({ type: 'catch_up_complete', session_id: sessionId, mode, seq: 40, boot_id: 'todo-test-boot' })
  const bucket = useChatStore.getState().sessionsById[sessionId]
  expect(bucket.isReplaying).toBe(false)
  expect(bucket.cursor?.seq).toBe(40)
}

function replayList(callId: string, text: string, sessionId = SID) {
  feed({ type: 'tool_call_start', session_id: sessionId, tool: 'set_todos', call_id: callId, params: { outcome: 'Prepare the report', todos: activeItem(text) } })
  finishList(callId, sessionId)
}

function snapshotRestore(turnId: string | null, callId: string, text: string, sessionId = SID, narration = true) {
  act(() => {
    useChatStore.getState().resetSessionForReplay(sessionId)
    useChatStore.getState().setReplaying(true)
  })
  feed({ type: 'session_snapshot', session_id: sessionId, seq: 40, boot_id: 'todo-test-boot', reason: 'unknown_position' })
  announceTurn(turnId, sessionId)
  feed({ type: 'replay_message', session_id: sessionId, role: 'assistant', id: 'old-answer', content: 'Finished earlier work', turn_id: 'old-turn' })
  replayList('old-call', 'Never revive the old item', sessionId)
  feed({ type: 'replay_message', session_id: sessionId, role: 'user', id: 'new-request', content: 'Continue the report' })
  if (narration) {
    feed({ type: 'replay_message', session_id: sessionId, role: 'assistant', id: 'current-narration', content: 'Starting this turn', turn_id: turnId ?? 'completed-turn' })
  }
  replayList(callId, text, sessionId)
  expect(screen.queryByTestId('reply-current-todo')).not.toBeInTheDocument()
  // Actual gateway order: persisted replay terminator, active projection, catch-up complete.
  feed({ type: 'done', session_id: sessionId, stats: { frames_emitted: 7 } })
  // The persisted message/call IDs were already replayed, so the gateway's
  // active projection can legitimately contain no additional text frames.
  completeCatchUp('snapshot', sessionId)
  expect(useChatStore.getState().sessionsById[sessionId].toolCallOrder).toEqual([])
}

describe('current item recovery and indicator design — round 1', () => {
  it('restores the same item after actual disconnect cleanup and incremental reconnect, without another checklist call', async () => {
    await renderChat()
    const callId = setList(activeItem('Resume the same item'))
    expectLine('Resume the same item')
    vi.mocked(fetchSessionMessages).mockResolvedValue([savedList('turn-one', 'Resume the same item', callId)])
    act(() => {
      useConnectionStore.setState({ isConnected: false })
      useChatStore.getState().clearStreamingState()
    })
    const disconnected = useChatStore.getState().sessionsById[SID]
    expect(disconnected.toolCallOrder).toEqual([])
    expect(disconnected.messagesById.answer.tool_calls?.map((call) => call.id)).toEqual([callId])
    act(() => {
      useConnectionStore.setState({ isConnected: true })
      useChatStore.getState().setReplaying(true)
    })
    announceTurn('turn-one')
    feed({ type: 'token', session_id: SID, message_id: 'answer', turn_id: 'turn-one', content: ' after reconnect' })
    completeCatchUp('incremental')
    await waitFor(() => expectLine('Resume the same item'))
    const recovered = useChatStore.getState().sessionsById[SID]
    expect(recovered.activeTurnId).toBe('turn-one')
    expect(recovered.isStreaming).toBe(true)
    expect(recovered.toolCallOrder).toEqual([])
    expect(fetchSessionMessages).toHaveBeenCalledWith(SID)
    setList(activeItem('New live item after recovery'))
    expectLine('New live item after recovery')
  })

  it.each([true, false])('restores the current item after page reload and snapshot replay (narration=%s)', async (narration) => {
    await renderChat()
    setList(activeItem('Before page reload'))
    expectLine('Before page reload')
    cleanup()
    queryClient.clear()
    useChatStore.setState(useChatStore.getInitialState(), true)
    vi.mocked(fetchSessionMessages).mockResolvedValue([
      savedList('old-turn', 'Never revive the old item', 'old-call'),
      savedList('reload-turn', 'Current item after reload', 'reload-call'),
    ])
    await renderChat()
    snapshotRestore('reload-turn', 'reload-call', 'Current item after reload', SID, narration)
    await waitFor(() => expectLine('Current item after reload'))
    expect(useChatStore.getState().sessionsById[SID].activeTurnId).toBe('reload-turn')
    expect(fetchSessionMessages).toHaveBeenCalledWith(SID)
  })

  it('attaches to another running chat and recovers only that chat\'s active-turn item', async () => {
    await renderChat()
    setList(activeItem('Original chat item'))
    expectLine('Original chat item')
    vi.mocked(fetchSessionMessages).mockImplementation(async (sessionId) => sessionId === OTHER
      ? [savedList('old-turn', 'Never revive the old item', 'old-call'), savedList('attached-turn', 'Attached chat item', 'attached-call')]
      : [])
    act(() => useSessionStore.getState().setActiveSession(OTHER, 'mia'))
    snapshotRestore('attached-turn', 'attached-call', 'Attached chat item', OTHER, false)
    await waitFor(() => expectLine('Attached chat item'))
    expect(fetchSessionMessages).toHaveBeenCalledWith(OTHER)
    act(() => useSessionStore.getState().setActiveSession(SID, 'mia'))
    expectLine('Original chat item')
  })

  it('ignores a late history response after the user switches back to another conversation', async () => {
    await renderChat()
    setList(activeItem('Keep the original chat item'))
    expectLine('Keep the original chat item')
    let resolveHistory!: (messages: Message[]) => void
    const pending = new Promise<Message[]>((resolve) => { resolveHistory = resolve })
    vi.mocked(fetchSessionMessages).mockImplementation(async (sessionId) => sessionId === OTHER ? pending : [])
    act(() => useSessionStore.getState().setActiveSession(OTHER, 'mia'))
    snapshotRestore('late-turn', 'late-call', 'Late other-chat item', OTHER, false)
    await waitFor(() => expect(fetchSessionMessages).toHaveBeenCalledWith(OTHER))
    await waitFor(() => expect(queryClient.getQueryState(['current-todo-history', OTHER, 'late-turn'])?.fetchStatus).toBe('fetching'))
    act(() => useSessionStore.getState().setActiveSession(SID, 'mia'))
    expectLine('Keep the original chat item')
    await act(async () => resolveHistory([savedList('late-turn', 'Late other-chat item', 'late-call')]))
    await waitFor(() => expect(queryClient.getQueryState(['current-todo-history', OTHER, 'late-turn'])?.status).toBe('success'))
    expectLine('Keep the original chat item')
  })

  it('never trusts replay-bubble ownership when REST identifies a tool-only call as an older turn', async () => {
    await renderChat()
    const turnId = 'current-turn-without-checklist'
    vi.mocked(fetchSessionMessages).mockResolvedValue([
      savedList('older-tool-only-turn', 'Old tool-only item', 'older-tool-only-call'),
      { id: 'current-text', role: 'assistant', content: 'Current reply', status: 'done', timestamp: '2026-10-21T12:00:30Z', turnId },
    ])
    act(() => {
      useChatStore.getState().resetSessionForReplay(SID)
      useChatStore.getState().setReplaying(true)
    })
    feed({ type: 'session_snapshot', session_id: SID, seq: 40, boot_id: 'todo-test-boot', reason: 'unknown_position' })
    announceTurn(turnId)
    replayList('older-tool-only-call', 'Old tool-only item')
    feed({ type: 'replay_message', session_id: SID, role: 'assistant', id: 'current-text', content: 'Current reply', turn_id: turnId })
    // Positive control: tool frames lack turn_id, so replay's empty-bubble
    // coalescing really did group the old call under a current-turn message.
    const replayed = useChatStore.getState().sessionsById[SID]
    const grouped = replayed.messageOrder.map((id) => replayed.messagesById[id]).find((message) => message.tool_calls?.some((call) => call.id === 'older-tool-only-call'))
    expect(grouped?.turnId).toBe(turnId)
    feed({ type: 'done', session_id: SID, stats: { frames_emitted: 3 } })
    completeCatchUp('snapshot')
    await waitFor(() => {
      expect(queryClient.getQueryState(['current-todo-history', SID, turnId])?.status).toBe('success')
      expect(queryClient.getQueryState(['current-todo-history', SID, turnId])?.fetchStatus).toBe('idle')
    })
    expect(screen.queryByTestId('reply-current-todo')).not.toBeInTheDocument()
  })

  it('does not revive a completed turn\'s saved item in a different running turn or from duplicate old frames', async () => {
    vi.mocked(fetchSessionMessages).mockResolvedValue([savedList('completed-turn', 'Do not revive me', 'completed-call')])
    await renderChat()
    snapshotRestore(null, 'completed-call', 'Do not revive me')
    expect(screen.queryByTestId('reply-current-todo')).not.toBeInTheDocument()
    announceTurn('new-turn-without-todos')
    feed({ type: 'token', session_id: SID, message_id: 'new-answer', turn_id: 'new-turn-without-todos', content: 'New work without a checklist' })
    feed({ type: 'tool_call_start', session_id: SID, seq: 5, call_id: 'completed-call', tool: 'set_todos', params: { todos: activeItem('Do not revive me') } })
    feed({ type: 'tool_call_result', session_id: SID, seq: 6, call_id: 'completed-call', tool: 'set_todos', status: 'success', result: 'saved earlier' })
    completeCatchUp('incremental')
    await waitFor(() => {
      expect(queryClient.getQueryState(['current-todo-history', SID, 'new-turn-without-todos'])?.status).toBe('success')
      expect(queryClient.getQueryState(['current-todo-history', SID, 'new-turn-without-todos'])?.fetchStatus).toBe('idle')
    })
    expect(queryClient.getQueryData(['current-todo-history', SID, 'new-turn-without-todos'])).toEqual([savedList('completed-turn', 'Do not revive me', 'completed-call')])
    expect(useChatStore.getState().sessionsById[SID].activeTurnId).toBe('new-turn-without-todos')
    expect(useChatStore.getState().sessionsById[SID].toolCalls['completed-call']).toBeUndefined()
    expect(screen.queryByTestId('reply-current-todo')).not.toBeInTheDocument()
  })

  it('keeps one chosen fallback through former ticks, item updates and clearing', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    let alternate = false
    const random = vi.spyOn(Math, 'random').mockImplementation(() => { alternate = !alternate; return alternate ? 0.2 : 0.6 })
    await renderChat()
    const phrase = screen.getByTestId('reply-status-fallback').textContent
    expect(phrase).toBeTruthy()
    setList(activeItem('Hold this item steady'))
    expectLine('Hold this item steady')
    random.mockClear()
    act(() => vi.advanceTimersByTime(6000))
    expectLine('Hold this item steady')
    expect(random).not.toHaveBeenCalled()
    setList([])
    expect(screen.getByTestId('reply-status-fallback').textContent).toBe(phrase)
    act(() => vi.advanceTimersByTime(8000))
    expect(screen.getByTestId('reply-status-fallback').textContent).toBe(phrase)
    expect(random).not.toHaveBeenCalled()
  })

  it('keeps full long text in the title, truncates the visual item, and keeps accessible copy stable across updates', async () => {
    await renderChat()
    const text = 'Review a long list of sources '.repeat(15).trim()
    setList(activeItem(text))
    const first = expectLine(text)
    expect(first.querySelector('[title]')).toHaveAttribute('title', text)
    expect(first.querySelector('[title]')).toHaveClass('truncate')
    const indicator = screen.getByTestId('reply-status-indicator')
    expect(within(indicator).getByText('Working on a checklist item')).toHaveClass('sr-only')
    expect(indicator.querySelector('[aria-live]')).toBeNull()
    setList(activeItem('A changed item'))
    expectLine('A changed item')
    expect(within(indicator).getByText('Working on a checklist item')).toHaveClass('sr-only')
    expect(indicator.querySelector('[aria-live]')).toBeNull()
  })

  it.each([300, 500])('keeps a backend-accepted %s-code-point item and logs the UTF-16 schema mismatch once', async (length) => {
    await renderChat()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const text = '𝑥'.repeat(length)
    const callId = setList([{ text, status: 'in_progress', active_form: text }])
    expectLine(text)
    expect(warn).toHaveBeenCalledExactlyOnceWith('chat.current_todo_validation_failed', {
      sessionId: SID, callId, issues: [{ code: 'too_big', path: '0.text' }],
    })
    feed({ type: 'token', session_id: SID, message_id: 'answer', turn_id: 'turn-one', content: ' still working' })
    expectLine(text)
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('never promotes a backend-rejected item beyond the 500-code-point limit', async () => {
    await renderChat()
    setList(activeItem('Previously accepted item'))
    expectLine('Previously accepted item')
    const callId = startList(activeItem('𝑥'.repeat(501)))
    finishList(callId, SID, 'error')
    expectLine('Previously accepted item')
  })

  it('shows the normal fallback and logs a structurally unreadable accepted list', async () => {
    await renderChat()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    feed({ type: 'tool_call_start', session_id: SID, call_id: 'unreadable-call', tool: 'set_todos', params: { todos: [null] } })
    finishList('unreadable-call')
    expect(screen.getByTestId('reply-status-fallback')).toHaveTextContent('Processing your request…')
    expect(screen.getByTestId('reply-status-indicator').textContent).not.toMatch(/Current checklist\s+unavailable/)
    expect(warn).toHaveBeenCalledExactlyOnceWith('chat.current_todo_validation_failed', {
      sessionId: SID, callId: 'unreadable-call', issues: [{ code: 'invalid_type', path: '0' }],
    })
  })

})

describe('checklist recovery error isolation — round 2', () => {
  it('limits a failed recovery read to the indicator without removing the running chat or controls', async () => {
    await loadUsableChat()
    const failure = new Error('Checklist recovery unavailable')
    vi.mocked(fetchSessionMessages).mockRejectedValue(failure)
    act(() => {
      useConnectionStore.setState({ isConnected: false })
      useChatStore.getState().clearStreamingState()
      useConnectionStore.setState({ isConnected: true })
      useChatStore.getState().setReplaying(true)
    })
    announceTurn('turn-one')
    completeCatchUp('incremental')
    await waitForHistoryFailure(failure)
    expectLoadedChatUsable()
    expect(screen.getByTestId('reply-status-fallback')).toHaveTextContent('Processing your request…')
    expect(screen.getByTestId('reply-status-indicator').textContent).not.toMatch(/Current checklist\s+unavailable/)
    expect(screen.queryByTestId('reply-current-todo')).not.toBeInTheDocument()
  })

  it('uses fallback after a recovery refetch fails even when that optional cache retains an older item', async () => {
    await loadUsableChat()
    const history = [savedList('turn-one', 'Previously recovered form', 'previous-recovery')]
    vi.mocked(fetchSessionMessages).mockResolvedValue(history)
    announceTurn('turn-one')
    await waitFor(() => expectLine('Previously recovered form'))
    expect(useChatStore.getState().sessionsById[SID].toolCallOrder).toEqual([])
    const key = ['current-todo-history', SID, 'turn-one']
    const failure = new Error('Optional recovery refetch failed')
    vi.mocked(fetchSessionMessages).mockRejectedValue(failure)
    await act(async () => { await queryClient.refetchQueries({ queryKey: key, exact: true }) })
    await waitForHistoryFailure(failure)
    expect(queryClient.getQueryData(key)).toEqual(history)
    expect(queryClient.getQueryState(key)?.error).toBe(failure)
    expectLoadedChatUsable()
    expect(screen.getByTestId('reply-status-fallback')).toHaveTextContent('Processing your request…')
    expect(screen.queryByTestId('reply-current-todo')).not.toBeInTheDocument()
  })

  it('keeps a confirmed live item and the chat usable when an optional history read fails', async () => {
    await loadUsableChat()
    const callId = setList(activeItem('Keep this accepted live item'))
    expectLine('Keep this accepted live item')
    const failure = new Error('Optional history read failed')
    vi.mocked(fetchSessionMessages).mockRejectedValue(failure)
    announceTurn('turn-one')
    await waitForHistoryFailure(failure)
    expectLoadedChatUsable()
    expectLine('Keep this accepted live item')
    expect(useChatStore.getState().sessionsById[SID].toolCalls[callId].status).toBe('success')
    expect(screen.getByTestId('reply-status-indicator').textContent).not.toMatch(/Current checklist\s+unavailable/)
  })

  it('still presents a genuine initial main-history failure instead of swallowing it as an optional read', async () => {
    const failure = new ApiError(403, 'History read forbidden')
    vi.mocked(fetchSessionMessages).mockRejectedValue(failure)
    await act(async () => {
      render(<QueryClientProvider client={queryClient}><ChatScreen /></QueryClientProvider>)
    })
    expect(await screen.findByText('Could not load messages.')).toBeInTheDocument()
    expect(queryClient.getQueryState(['messages', SID])?.error).toBe(failure)
    expect(screen.getByRole('button', { name: /^Retry$/ })).toBeEnabled()
    expect(screen.queryByTestId('composer-card')).not.toBeInTheDocument()
  })
})

// Founder Q10=A / security F1: real accepted frames → private selector →
// mounted chat indicator. Copy normalization must not erase legitimate RTL or
// joiners, and unusable forms must keep the already-chosen fallback unchanged.
describe('Q10 normalized active-form copy in the rendered chat line', () => {
  it('loads all 38 founder cases, including every listed bidi control', () => {
    expect(q10Fixture.source).toBe('Founder Q10=A, PR #1406 round 2; security F1')
    expect(q10Fixture.cases).toHaveLength(38)
    expect(new Set(q10Fixture.cases.map((item) => item.name)).size).toBe(38)
  })

  it.each(q10Fixture.cases)('$name', async (tc) => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    await renderChat()
    const initialFallback = screen.getByTestId('reply-status-fallback').textContent
    expect(initialFallback).toBe('Processing your request…')
    const todos = activeItem(tc.input)
    const original = structuredClone(todos)
    const callId = setList(todos)
    expect(useChatStore.getState().sessionsById[SID].toolCalls[callId].params.todos, 'display normalization must not mutate recorded raw tool arguments').toEqual(original)
    if (tc.reject || tc.expected === '') {
      expect(screen.queryByTestId('reply-current-todo'), 'unusable active_form must not render a visually blank checklist line').not.toBeInTheDocument()
      const fallback = screen.getByTestId('reply-status-fallback')
      expect(fallback.textContent, 'normalization must preserve the fixed fallback selected before the tool call').toBe(initialFallback)
      expect(fallback).toHaveAttribute('aria-hidden', 'true')
      expect(within(screen.getByTestId('reply-status-indicator')).getByText('Thinking', { exact: true })).toHaveClass('sr-only')
    } else {
      const line = expectLine(tc.expected)
      expect(line.querySelector('[title]')).toHaveAttribute('title', tc.expected)
      expect(screen.queryByTestId('reply-status-fallback')).not.toBeInTheDocument()
      expect(within(screen.getByTestId('reply-status-indicator')).getByText('Working on a checklist item')).toHaveClass('sr-only')
    }
    if (tc.reject) {
      expect(warn).toHaveBeenCalledExactlyOnceWith('chat.current_todo_validation_failed', {
        sessionId: SID, callId, issues: [{ code: 'too_big', path: '0.active_form' }],
      })
    } else {
      expect(warn, 'valid normalized forms must not produce an over-limit/invalid-form warning').not.toHaveBeenCalled()
    }
  })
})

const loadedHistory: Message[] = [{
  id: 'request', role: 'user', content: 'Prepare the report', status: 'done', timestamp: '2026-10-21T12:00:00Z',
}]

async function loadUsableChat() {
  vi.mocked(fetchSessionMessages).mockResolvedValue(loadedHistory)
  await renderChat()
  await waitFor(() => expect(queryClient.getQueryState(['messages', SID])?.status).toBe('success'))
  expect(queryClient.getQueryData(['messages', SID])).toEqual(loadedHistory)
  expect(screen.getByText('Starting the report')).toBeInTheDocument()
  expect(screen.getByTestId('stop-btn')).toBeEnabled()
}

async function waitForHistoryFailure(failure: Error) {
  // Observe a real same-session query rejection, without assuming whether the
  // production fix has isolated that query yet. No fake query/error state.
  await waitFor(() => expect(queryClient.getQueryCache().findAll().some((query) =>
    query.queryKey.includes(SID) && query.state.error === failure,
  )).toBe(true))
}

function expectLoadedChatUsable() {
  expect(screen.getByTestId('composer-card')).toBeInTheDocument()
  expect(screen.getByTestId('composer-input')).toBeEnabled()
  expect(screen.getByTestId('stop-btn')).toBeEnabled()
  expect(screen.getByText('Starting the report')).toBeInTheDocument()
  expect(screen.queryByText('Could not load messages.')).not.toBeInTheDocument()
  expect(queryClient.getQueryState(['messages', SID])?.status).toBe('success')
  expect(queryClient.getQueryData(['messages', SID])).toEqual(loadedHistory)
  expect(useChatStore.getState().sessionsById[SID].isStreaming).toBe(true)
  expect(useChatStore.getState().sessionsById[SID].activeTurnId).toBe('turn-one')
}
