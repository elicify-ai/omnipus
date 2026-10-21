/**
 * PR #1311 G5, architect R2-3: a conclusion-handling notice is NOT a turn exit.
 * Real frame reducer, display catalogue, chat rows, composer and Stop action.
 * Only network APIs and the AssistantUI runtime adapter are stand-ins.
 * Exact-copy oracle: the independently agreed acceptance fixture, not observed
 * implementation output. GREEN and mutation proof are deferred to CHECK.
 */
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { act } from 'react'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { readFileSync } from 'node:fs'
import { useChatStore, getMessages, makeBucketMessages, type ChatMessage } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { useChatPreferencesStore } from './chatPreferences'
import { codeToAttribution, codeToMessage } from '@/lib/llm-error'
import { ChatScreen } from '@/components/chat/ChatScreen'

vi.mock('@assistant-ui/react', async () =>
  (await import('@/test/assistantUiMock')).createAssistantUiMock())
vi.mock('@tanstack/react-query', async (importOriginal) => ({
  ...await importOriginal<typeof import('@tanstack/react-query')>(),
  useQuery: (opts: { queryKey?: readonly unknown[] }) => ({ data: queryDataWithEmptyHistory(opts?.queryKey), isError: false, refetch: vi.fn() }),
  useMutation: () => ({ mutate: vi.fn(), isPending: false }),
  useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
}))
vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }), useSearch: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))
import { emptyHistoryPage, queryDataWithEmptyHistory, type HistoryReadPort } from '../../tests/chat-history-paging/legacy-fixtures'

vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchAgents: vi.fn().mockResolvedValue([]),
  fetchSessionMessages: vi.fn<HistoryReadPort>(async (sessionId) => emptyHistoryPage(sessionId)),
  fetchCommands: vi.fn().mockResolvedValue([]),
  fetchSkills: vi.fn().mockResolvedValue([]),
  fetchProviders: vi.fn().mockResolvedValue([]),
  uploadFiles: vi.fn(),
}))
vi.mock('@/components/chat/composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('@/components/chat/composer/TokenCounter', () => ({ TokenCounter: () => null }))

// jsdom has no measured viewport. Exercise ChatScreen's REAL no-observer
// fallback renderer rather than enable virtualization with a no-op observer.
vi.stubGlobal('ResizeObserver', undefined)
if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = function () {}

let caseNumber = 0
let SID: string
let TURN: string
let MESSAGE: string
const CODE = 'judge_conclusion_failed'
const COPY = readFileSync('tests/fixtures/no-time-limits-r3/judge-conclusion-failed-copy.txt', 'utf8').trimEnd()
const NARRATION = 'The newer response is still working.'
let send: ReturnType<typeof vi.fn>

beforeEach(() => {
  // Finished-turn/cursor maps deliberately outlive buckets. Independent input
  // identities keep tests isolated without mocking or resetting that mechanism.
  caseNumber += 1
  SID = `r3-conclusion-notice-chat-${caseNumber}`
  TURN = `r3-newer-turn-${caseNumber}`
  MESSAGE = `r3-newer-reply-${caseNumber}`
  send = vi.fn().mockReturnValue(true)
  act(() => {
    useChatStore.setState({
      sessionsById: {}, ...makeBucketMessages([]), isStreaming: false,
      toolCalls: {}, toolCallOrder: [], textAtToolCallStart: {},
      sessionTokens: 0, sessionCost: 0, isReplaying: false,
      replayCompletedForSession: null, rateLimitEvent: null, providerRetryEvent: null,
      outboundQueue: [], pendingDrainQueue: [],
      lastUserMessageAt: null, cancelStage: null, loopStatus: null,
    })
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'mia', activeAgentType: null, attachedSessionType: null })
    useConnectionStore.setState({
      connection: { send } as unknown as ReturnType<typeof useConnectionStore.getState>['connection'],
      isConnected: true, connectionError: null, liteMode: true,
    })
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
  })
})

afterEach(() => {
  cleanup()
  act(() => {
    useChatStore.getState().clearStreamingState()
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    useSessionStore.setState({ activeSessionId: null })
  })
})

function startRealFrameSequence(): void {
  const placeholder: ChatMessage = {
    id: MESSAGE, role: 'assistant', content: '', timestamp: new Date().toISOString(),
    status: 'streaming', isStreaming: true, agentId: 'mia',
  }
  act(() => {
    useChatStore.setState({
      sessionsById: { [SID]: {
        ...makeBucketMessages([placeholder]), toolCalls: {}, toolCallOrder: [],
        textAtToolCallStart: {}, isStreaming: true, isReplaying: false,
        replayCompletedForSession: null, sessionTokens: 0, sessionCost: 0,
        rateLimitEvent: null, cancelStage: null, lastUserMessageAt: null, lastReceivedEventTime: null,
      } },
      ...makeBucketMessages([placeholder]), isStreaming: true,
    })
    useChatStore.getState().handleFrame({
      type: 'session_state', session_id: SID, user_id: 'tester',
      emitted_at: new Date().toISOString(), pending_approvals: [], pending_asks: [],
      active_turn: { turn_id: TURN, agent_id: 'mia', started_at: new Date().toISOString() },
    })
    useChatStore.getState().handleFrame({
      type: 'token', session_id: SID, message_id: MESSAGE, turn_id: TURN, agent_id: 'mia', content: NARRATION,
    })
  })
  const b = useChatStore.getState().sessionsById[SID]
  expect(b.activeTurnId, 'instrument: a REAL announcement established the active turn').toBe(TURN)
  expect(b.messagesById[MESSAGE].content, 'instrument: the REAL token reducer streamed the newer reply').toBe(NARRATION)
  expect(b.isStreaming).toBe(true)
}

function emitConclusionNotice(): void {
  act(() => {
    // Deliberately session-scoped: no fake client_message_id refusal field,
    // no provider_message trust bypass, and no turn_id binding to the new turn.
    useChatStore.getState().handleFrame({
      type: 'error', session_id: SID, message: 'disk full: raw backend diagnostic must not be ordinary copy',
      payload: { llm_error: {
        code: CODE, message: 'disk full: raw backend diagnostic must not be ordinary copy',
        retryable: false, detail: 'could not be saved: disk full',
      } },
    })
  })
}

describe('G5 — judge_conclusion_failed is a visible non-terminal notice', () => {
  it('renders the exact catalogue copy with Verbose off, separate from the newer response', () => {
    startRealFrameSequence()
    render(<ChatScreen />)
    emitConclusionNotice()
    expect(codeToMessage(CODE), 'G5 missing or wrong catalogue copy').toBe(COPY)
    expect(codeToAttribution[CODE], 'G5 catalogue attribution').toBe('ambiguous')
    const messages = getMessages(useChatStore.getState().sessionsById[SID])
    const notice = messages.find((m) => m.errorCode === CODE)
    expect(notice?.content, 'G5 real reducer must use curated copy, not unknown/raw text').toBe(COPY)
    expect(notice?.id, 'G5 notice must not replace the ongoing reply').not.toBe(MESSAGE)
    expect(notice?.errorDetail, 'G5 raw detail is Verbose-only').toBeUndefined()
    expect(screen.getByText(COPY), 'G5 ordinary chat must visibly render the notice').toBeInTheDocument()
  })

  it('preserves streaming, active-turn identity, continued tokens and the real Stop control', () => {
    startRealFrameSequence()
    render(<ChatScreen />)
    const stopBefore = screen.getByTestId('stop-btn')
    emitConclusionNotice()
    const state = useChatStore.getState()
    const bucket = state.sessionsById[SID]
    expect.soft(bucket.isStreaming, 'G5 operational notice must not end the running turn').toBe(true)
    expect.soft(state.isStreaming, 'G5 Stop must remain available in the foreground').toBe(true)
    expect.soft(bucket.activeTurnId, 'G5 operational notice must not clear the active turn identity').toBe(TURN)
    expect.soft(bucket.activeTurnAgentId).toBe('mia')
    expect.soft(bucket.messagesById[MESSAGE].content, 'G5 newer narration must survive').toBe(NARRATION)
    expect.soft(bucket.messagesById[MESSAGE].status).toBe('streaming')
    expect.soft(screen.queryByTestId('stop-btn'), 'G5 real Stop button disappeared').toBe(stopBefore)
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', session_id: SID, content: ' Still making progress.' })
    })
    expect.soft(useChatStore.getState().sessionsById[SID].messagesById[MESSAGE].content,
      'G5 a notice cannot poison the continuing token path').toBe(NARRATION + ' Still making progress.')
    act(() => { fireEvent.click(stopBefore) })
    expect(send.mock.calls.map(([frame]) => frame), 'G5 real Stop must still reach the WebSocket edge')
      .toContainEqual({ type: 'cancel', session_id: SID })
  })

  it('control: an ordinary terminal error still closes the streaming turn and removes Stop', () => {
    startRealFrameSequence()
    render(<ChatScreen />)
    act(() => {
      useChatStore.getState().handleFrame({ type: 'error', session_id: SID, message: 'provider timeout',
        payload: { llm_error: { code: 'turn_timed_out', message: 'provider timeout', retryable: false } } })
    })
    const bucket = useChatStore.getState().sessionsById[SID]
    expect(bucket.isStreaming).toBe(false)
    expect(useChatStore.getState().isStreaming).toBe(false)
    expect(bucket.activeTurnId).toBeNull()
    expect(bucket.activeTurnAgentId).toBeNull()
    expect(bucket.messagesById[MESSAGE].status).toBe('error')
    expect(screen.queryByTestId('stop-btn')).not.toBeInTheDocument()
  })
})
