import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore, getMessages, makeBucketMessages, MAX_MESSAGES_PER_SESSION, clampToolResult, isClientTruncatedResult, evictMessageFromBucket } from './chat'
import type { SessionChatState } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'

// test_chat_store (test #22)
// Traces to: wave5a-wire-ui-spec.md — Scenario: User sends message and receives streaming response
//             wave5a-wire-ui-spec.md — Scenario: Cancel during streaming preserves partial response

const TEST_SESSION_ID = 'test-session-1'

function resetStore() {
  act(() => {
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      isStreaming: false,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      pendingApprovals: [],
      sessionTokens: 0,
      sessionCost: 0,
      isReplaying: false,
      replayCompletedForSession: null,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
    })
    useConnectionStore.setState({
      connection: null,
      isConnected: false,
      connectionError: null,
    })
    useSessionStore.setState({
      activeSessionId: TEST_SESSION_ID,
      activeAgentId: null,
      activeAgentType: null,
    })
  })
}

beforeEach(resetStore)

describe('chat store — initial state', () => {
  it('initializes with empty messages, not streaming; activeSessionId set by beforeEach', () => {
    const chatState = useChatStore.getState()
    const sessionState = useSessionStore.getState()
    expect(chatState.messages).toEqual([])
    expect(chatState.isStreaming).toBe(false)
    // beforeEach sets activeSessionId to TEST_SESSION_ID so per-session actions have a target.
    expect(sessionState.activeSessionId).toBe(TEST_SESSION_ID)
    expect(sessionState.activeAgentId).toBeNull()
  })
})

describe('chat store — session management', () => {
  it('setActiveSession updates activeSessionId and activeAgentId without wiping buckets', () => {
    // Seed a message in the first session bucket.
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'msg_original',
        session_id: TEST_SESSION_ID,
        role: 'user',
        content: 'Original session message',
        timestamp: '2026-03-29T10:00:00Z',
        status: 'done',
      })
    })
    // Switch to a different session — the original bucket must survive.
    act(() => {
      useSessionStore.getState().setActiveSession('sess_abc', 'general-assistant')
    })
    const sessionState = useSessionStore.getState()
    expect(sessionState.activeSessionId).toBe('sess_abc')
    expect(sessionState.activeAgentId).toBe('general-assistant')
    // Original bucket is intact (not wiped by session switch).
    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    const msgs = bucket ? getMessages(bucket) : []
    expect(msgs).toHaveLength(1)
    expect(msgs[0].content).toBe('Original session message')
  })
})

describe('chat store — message handling', () => {
  it('appendMessage adds a user message to the thread', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: User message appears optimistically
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'user_1',
        session_id: 'sess_1',
        role: 'user',
        content: 'Hello, world!',
        timestamp: '2026-03-29T10:00:00Z',
        status: 'done',
      })
    })
    const { messages } = useChatStore.getState()
    expect(messages).toHaveLength(1)
    expect(messages[0].role).toBe('user')
    expect(messages[0].content).toBe('Hello, world!')
  })

  it('setMessages replaces all messages and resets tool calls', () => {
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'old_1',
        session_id: 'sess_1',
        role: 'user',
        content: 'Old message',
        timestamp: '2026-03-29T09:00:00Z',
        status: 'done',
      })
      useChatStore.getState().setMessages([
        {
          id: 'new_1',
          session_id: 'sess_2',
          role: 'user',
          content: 'New message',
          timestamp: '2026-03-29T10:00:00Z',
          status: 'done',
        },
      ])
    })
    const { messages, toolCalls, sessionTokens } = useChatStore.getState()
    expect(messages).toHaveLength(1)
    expect(messages[0].content).toBe('New message')
    expect(Object.keys(toolCalls)).toHaveLength(0)
    expect(sessionTokens).toBe(0)
  })
})

describe('chat store — streaming via handleFrame', () => {
  it('handleFrame(token) appends content to the last assistant message', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Streaming response — tokens append
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst_1',
        session_id: 'sess_1',
        role: 'assistant',
        content: '',
        timestamp: '2026-03-29T10:00:01Z',
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })
      useChatStore.getState().handleFrame({ type: 'token', content: 'Hello', session_id: TEST_SESSION_ID })
      useChatStore.getState().handleFrame({ type: 'token', content: ' world', session_id: TEST_SESSION_ID })
    })
    const { messages } = useChatStore.getState()
    const asst = messages.find((m) => m.id === 'asst_1')
    expect(asst?.content).toBe('Hello world')
    expect(asst?.isStreaming).toBe(true)
  })

  it('handleFrame(done) marks last assistant message as done and sets isStreaming false', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Streaming completes with markdown rendering
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst_2',
        session_id: 'sess_1',
        role: 'assistant',
        content: '# Heading\nParagraph',
        timestamp: '2026-03-29T10:00:01Z',
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })
      useChatStore.getState().handleFrame({ type: 'done', stats: { tokens: 150, cost: 0.02, duration_ms: 0 }, session_id: TEST_SESSION_ID })
    })
    const state = useChatStore.getState()
    expect(state.isStreaming).toBe(false)
    const asst = state.messages.find((m) => m.id === 'asst_2')
    expect(asst?.status).toBe('done')
    expect(asst?.isStreaming).toBe(false)
    expect(state.sessionTokens).toBe(150)
    expect(state.sessionCost).toBeCloseTo(0.02)
  })

  it('handleFrame(error) sets message to error status — message-level error does NOT set connectionError', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: WebSocket connection error during streaming
    // When an assistant message already exists, the error is message-level (e.g. LLM rejected
    // the request). The inline bubble is updated; connectionError is NOT set to avoid falsely
    // showing a connection-down banner when the connection is fine.
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst_3',
        session_id: 'sess_1',
        role: 'assistant',
        content: '',
        timestamp: '2026-03-29T10:00:01Z',
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })
      useConnectionStore.setState({ connectionError: null })
      useChatStore.getState().handleFrame({ type: 'error', message: 'LLM quota exceeded' })
    })
    const chatState = useChatStore.getState()
    expect(chatState.isStreaming).toBe(false)
    const asst = chatState.messages.find((m) => m.id === 'asst_3')
    expect(asst?.status).toBe('error')
    // Message-level error must NOT propagate to the connection error banner
    expect(useConnectionStore.getState().connectionError).toBeNull()
  })

  it('handleFrame(error) with no assistant message sets connectionError banner', () => {
    // When no assistant message exists, the error is connection-level (e.g. the WS frame
    // arrived before the server could even start a reply). Both the error message AND
    // connectionError are set so the banner shows.
    act(() => {
      useChatStore.setState({ isStreaming: true })
      useChatStore.getState().handleFrame({ type: 'error', message: 'Connection lost' })
    })
    const chatState = useChatStore.getState()
    expect(chatState.isStreaming).toBe(false)
    expect(useConnectionStore.getState().connectionError).toBe('Connection lost')
    const errMsg = chatState.messages.find((m) => m.status === 'error')
    expect(errMsg).toBeDefined()
  })
})

describe('chat store — tool calls', () => {
  it('startToolCall registers a running tool call', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Running tool call shows spinner
    act(() => {
      useChatStore.getState().startToolCall('tc_1', 'web_search', { query: 'AWS pricing' })
    })
    const { toolCalls } = useChatStore.getState()
    expect(toolCalls['tc_1']).toMatchObject({
      call_id: 'tc_1',
      tool: 'web_search',
      status: 'running',
    })
  })

  it('resolveToolCall updates status to success with result', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Successful tool call collapses by default
    act(() => {
      useChatStore.getState().startToolCall('tc_2', 'exec', { command: 'ls' })
      useChatStore.getState().resolveToolCall('tc_2', { exit_code: 0 }, 'success', 250)
    })
    const { toolCalls } = useChatStore.getState()
    expect(toolCalls['tc_2'].status).toBe('success')
    expect(toolCalls['tc_2'].duration_ms).toBe(250)
  })

  it('resolveToolCall updates status to error with error message', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Failed tool call shows error with retry
    act(() => {
      useChatStore.getState().startToolCall('tc_3', 'exec', { command: 'ls' })
      useChatStore.getState().resolveToolCall('tc_3', null, 'error', 30000, 'Timeout after 30s')
    })
    const { toolCalls } = useChatStore.getState()
    expect(toolCalls['tc_3'].status).toBe('error')
    expect(toolCalls['tc_3'].error).toBe('Timeout after 30s')
  })

  it('cancelToolCall sets tool status to cancelled', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Cancel during tool execution
    act(() => {
      useChatStore.getState().startToolCall('tc_4', 'web_search', {})
      useChatStore.getState().cancelToolCall('tc_4')
    })
    expect(useChatStore.getState().toolCalls['tc_4'].status).toBe('cancelled')
  })
})

describe('chat store — exec approval', () => {
  it('addApprovalRequest queues a pending approval', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Approval block renders with command details
    act(() => {
      useChatStore.getState().addApprovalRequest({
        type: 'exec_approval_request',
        id: 'appr_1',
        command: 'git pull origin main',
        working_dir: '~/projects/omnipus',
        matched_policy: 'tools.exec.approval=ask',
        session_id: TEST_SESSION_ID,
      })
    })
    const { pendingApprovals } = useChatStore.getState()
    expect(pendingApprovals).toHaveLength(1)
    expect(pendingApprovals[0].command).toBe('git pull origin main')
    expect(pendingApprovals[0].status).toBe('pending')
  })

  it('resolveApproval updates approval status to allowed', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario Outline: User responds to approval prompt
    act(() => {
      useChatStore.getState().addApprovalRequest({
        type: 'exec_approval_request',
        id: 'appr_1',
        command: 'git pull origin main',
        session_id: TEST_SESSION_ID,
      })
      useChatStore.getState().resolveApproval('appr_1', 'allowed')
    })
    const { pendingApprovals } = useChatStore.getState()
    expect(pendingApprovals[0].status).toBe('allowed')
  })
})

describe('chat store — cancel/interrupt (test_cancel_preserves_partial)', () => {
  it('markLastMessageInterrupted sets status to interrupted and clears streaming', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Cancel during streaming preserves partial response (AC1)
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst_cancel',
        session_id: 'sess_1',
        role: 'assistant',
        content: 'Here is the analysis of...',
        timestamp: '2026-03-29T10:00:01Z',
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })
      useChatStore.getState().markLastMessageInterrupted()
    })
    const state = useChatStore.getState()
    expect(state.isStreaming).toBe(false)
    const msg = state.messages.find((m) => m.id === 'asst_cancel')
    expect(msg?.status).toBe('interrupted')
    expect(msg?.content).toBe('Here is the analysis of...')
  })

  it('cancelStream calls connection.send with cancel frame', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Cancel during streaming (AC1 — WebSocket frame sent)
    const mockSend = vi.fn()
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst_5',
        session_id: TEST_SESSION_ID,
        role: 'assistant',
        content: 'Partial...',
        timestamp: '2026-03-29T10:00:01Z',
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      // activeSessionId is already TEST_SESSION_ID from resetStore
      useChatStore.getState().cancelStream()
    })
    expect(mockSend).toHaveBeenCalledWith({ type: 'cancel', session_id: TEST_SESSION_ID })
    expect(useChatStore.getState().isStreaming).toBe(false)
  })

  it('cancelStream is a no-op when not streaming', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Cancel when idle is a no-op (AC3)
    const mockSend = vi.fn()
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useChatStore.getState().cancelStream()
    })
    expect(mockSend).not.toHaveBeenCalled()
  })
})

describe('chat store — sendMessage optimistic render', () => {
  it('sendMessage appends user message immediately and sets isStreaming', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: User message appears optimistically
    // mockSend must return true — sendMessage reverts the optimistic update if send() returns falsy
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useSessionStore.setState({
        activeSessionId: TEST_SESSION_ID,
        activeAgentId: 'general-assistant',
      })
      useChatStore.getState().sendMessage('Hello, world!')
    })
    const state = useChatStore.getState()
    // User message appended immediately
    const userMsg = state.messages.find((m) => m.role === 'user')
    expect(userMsg?.content).toBe('Hello, world!')
    // isStreaming set to true
    expect(state.isStreaming).toBe(true)
    // WS frame sent
    expect(mockSend).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'message', content: 'Hello, world!' })
    )
  })
})

// ── #253: recovery error-bubble tests ────────────────────────────────────────
// Traces to: sprint-258 review finding #253 — failed send must preserve typed
// content as a retriable error bubble, not silently drop the message.

describe('chat store — #253 no-session send failure creates retriable error bubble', () => {
  it('when activeSessionId is null and send() fails, user message gets status:error (not dropped)', () => {
    // BDD:
    //   Given no active session (activeSessionId = null)
    //   And the WS send() returns false (connection dropped mid-send)
    //   When sendMessage('Hello') is called
    //   Then a user message with content 'Hello' and status 'error' is present in the store
    //   And isStreaming is false (no phantom spinner)
    //   And a Retry affordance is reachable (the message is in the store with status:'error')
    const mockSend = vi.fn().mockReturnValue(false) // send fails
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
        connectionError: null,
      })
      // No active session — null triggers the no-session path
      useSessionStore.setState({
        activeSessionId: null,
        activeAgentId: 'general-assistant',
      })
      useChatStore.getState().sendMessage('Hello no-session')
    })

    // After the failed send, the user message must be preserved with status:'error'
    const state = useChatStore.getState()

    // The message must be in SOME bucket (either the pending one or the active one)
    // Check the global messages selector which reflects the active session.
    // The pending bucket is activated via setActiveSession('__pending', ...)
    const userMsg = state.messages.find((m) => m.role === 'user' && m.content === 'Hello no-session')
    expect(userMsg).toBeDefined()
    expect(userMsg?.status).toBe('error')

    // isStreaming must be false — no phantom spinner
    expect(state.isStreaming).toBe(false)

    // The Retry affordance: VirtualUserMessageRow renders UserMessageRetryButton
    // when message.status === 'error'. Assert the store state that drives it:
    // the user message with status:'error' IS reachable in the messages array.
    const errorMessages = state.messages.filter((m) => m.role === 'user' && m.status === 'error')
    expect(errorMessages.length).toBeGreaterThan(0)
  })

  it('when activeSessionId is null and send() succeeds, user message is rendered optimistically', () => {
    // BDD:
    //   Given no active session
    //   And the WS send() returns true (succeeds)
    //   When sendMessage('Hello') is called
    //   Then a user message with content 'Hello' is visible (not status:'error')
    //   And isStreaming is true (awaiting session_started ack)
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useSessionStore.setState({
        activeSessionId: null,
        activeAgentId: 'general-assistant',
      })
      useChatStore.getState().sendMessage('Hello pending')
    })

    const state = useChatStore.getState()
    const userMsg = state.messages.find((m) => m.role === 'user' && m.content === 'Hello pending')
    expect(userMsg).toBeDefined()
    // status should be 'done' (successfully queued, awaiting session_started)
    expect(userMsg?.status).toBe('done')
    // isStreaming should be true — the agent is about to respond
    expect(state.isStreaming).toBe(true)
  })
})

// ── Sprint H: subagent span tests ─────────────────────────────────────────────
// TDD row 11: ChatStore_GroupsFramesBySpan
// Traces to: sprint-h-subagent-block-spec.md Scenarios 2, 4, 5, 8

describe('ChatStore_GroupsFramesBySpan', () => {
  /** Seed an assistant placeholder so spans have a message to attach to. */
  function seedAssistant() {
    act(() => {
      useChatStore.getState().updateLastAssistantMessage('', false)
    })
  }

  it('in-order: subagent_start → tool_call_start → tool_call_result → subagent_end populates span', () => {
    seedAssistant()

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_c1',
        parent_call_id: 'c1',
        task_label: 'audit go files',
        agent_id: 'max',
        session_id: TEST_SESSION_ID,
      })
    })

    let msgs = useChatStore.getState().messages
    let span = msgs[msgs.length - 1].spans?.[0]
    expect(span).toBeDefined()
    expect(span?.spanId).toBe('span_c1')
    expect(span?.taskLabel).toBe('audit go files')
    expect(span?.status).toBe('running')
    expect(span?.steps).toHaveLength(0)

    // tool_call_start with matching parent_call_id
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 't1',
        tool: 'fs.list',
        params: { path: '/tmp' },
        parent_call_id: 'c1',
        session_id: TEST_SESSION_ID,
      })
    })

    msgs = useChatStore.getState().messages
    span = msgs[msgs.length - 1].spans?.[0]
    expect(span?.steps).toHaveLength(1)
    const s0 = span?.steps[0]
    expect(s0?.kind === 'tool' ? s0.tool.tool : undefined).toBe('fs.list')
    expect(s0?.kind === 'tool' ? s0.tool.status : undefined).toBe('running')

    // tool_call_result
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 't1',
        tool: 'fs.list',
        result: 'file.go',
        status: 'success',
        duration_ms: 100,
        parent_call_id: 'c1',
        session_id: TEST_SESSION_ID,
      })
    })

    msgs = useChatStore.getState().messages
    span = msgs[msgs.length - 1].spans?.[0]
    const s0after = span?.steps[0]
    expect(s0after?.kind === 'tool' ? s0after.tool.status : undefined).toBe('success')
    expect(s0after?.kind === 'tool' ? s0after.tool.result : undefined).toBe('file.go')

    // subagent_end
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_end',
        span_id: 'span_c1',
        status: 'success',
        duration_ms: 4210,
        final_result: 'Found 1 Go file',
        session_id: TEST_SESSION_ID,
      })
    })

    msgs = useChatStore.getState().messages
    span = msgs[msgs.length - 1].spans?.[0]
    expect(span?.status).toBe('success')
    // Narrow to terminal span to access durationMs and finalResult.
    const terminalSpan = span?.status !== 'running' ? span : undefined
    expect((terminalSpan as import('@/store/chat').SubagentSpanTerminal | undefined)?.durationMs).toBe(4210)
    expect((terminalSpan as import('@/store/chat').SubagentSpanTerminal | undefined)?.finalResult).toBe('Found 1 Go file')
  })

  it('out-of-order: tool_call_start arrives before subagent_start — buffered then drained', () => {
    seedAssistant()

    // tool_call_start arrives BEFORE subagent_start
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 't2',
        tool: 'shell',
        params: { cmd: 'ls' },
        parent_call_id: 'c2',
        session_id: TEST_SESSION_ID,
      })
    })

    // No span yet — should not appear in flat toolCalls either yet
    let msgs = useChatStore.getState().messages
    expect(msgs[msgs.length - 1].spans ?? []).toHaveLength(0)

    // Now subagent_start arrives — should drain the buffer
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_c2',
        parent_call_id: 'c2',
        task_label: 'list files',
        session_id: TEST_SESSION_ID,
      })
    })

    msgs = useChatStore.getState().messages
    const span = msgs[msgs.length - 1].spans?.[0]
    expect(span).toBeDefined()
    expect(span?.spanId).toBe('span_c2')
    expect(span?.steps).toHaveLength(1)
    const step0 = span?.steps[0]
    expect(step0?.kind).toBe('tool')
    expect(step0?.kind === 'tool' ? step0.tool.tool : undefined).toBe('shell')
  })

  it('step count increments +1 per tool_call_start, not per result (FR-H-010)', () => {
    seedAssistant()

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_c3',
        parent_call_id: 'c3',
        task_label: 'multi-step task',
        session_id: TEST_SESSION_ID,
      })
    })

    for (let i = 1; i <= 3; i++) {
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'tool_call_start',
          call_id: `t_${i}`,
          tool: 'fs.list',
          params: {},
          parent_call_id: 'c3',
          session_id: TEST_SESSION_ID,
        })
      })
      const msgs = useChatStore.getState().messages
      const span = msgs[msgs.length - 1].spans?.[0]
      expect(span?.steps).toHaveLength(i)
    }
  })

  it('two sibling spans accumulate steps independently', () => {
    seedAssistant()

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_s1',
        parent_call_id: 's1',
        task_label: 'first',
        session_id: TEST_SESSION_ID,
      })
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_s2',
        parent_call_id: 's2',
        task_label: 'second',
        session_id: TEST_SESSION_ID,
      })
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'ts1',
        tool: 'exec',
        params: {},
        parent_call_id: 's1',
        session_id: TEST_SESSION_ID,
      })
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'ts2a',
        tool: 'web_search',
        params: {},
        parent_call_id: 's2',
        session_id: TEST_SESSION_ID,
      })
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'ts2b',
        tool: 'file.read',
        params: {},
        parent_call_id: 's2',
        session_id: TEST_SESSION_ID,
      })
    })

    const msgs = useChatStore.getState().messages
    const spans = msgs[msgs.length - 1].spans ?? []
    expect(spans).toHaveLength(2)
    expect(spans[0].steps).toHaveLength(1)
    expect(spans[1].steps).toHaveLength(2)
  })
})

// TDD row 12: ChatStore_OrphanFrame_FallsBackFlat
// Traces to: sprint-h-subagent-block-spec.md Edge (out-of-order), FR-H-009

describe('ChatStore_OrphanFrame_FallsBackFlat', () => {
  it('frame with unknown parent_call_id + no subagent_start within 10s → flat + dev warning', async () => {
    // Use fake timers to simulate the 10s TTL without waiting
    vi.useFakeTimers()
    const warnSpy = vi.spyOn(console, 'warn')

    act(() => {
      useChatStore.getState().updateLastAssistantMessage('', false)
    })

    // tool_call_start with a parent_call_id that has no matching subagent_start
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'orphan_t1',
        tool: 'fs.list',
        params: {},
        parent_call_id: 'orphan_parent',
        session_id: TEST_SESSION_ID,
      })
    })

    // No span yet, not in toolCalls yet (buffered)
    expect(useChatStore.getState().toolCalls['orphan_t1']).toBeUndefined()

    // Advance time past 10s TTL
    await act(async () => {
      vi.advanceTimersByTime(10_001)
    })

    // Now the buffered frame should be released as a flat tool call
    const state = useChatStore.getState()
    expect(state.toolCalls['orphan_t1']).toBeDefined()
    expect(state.toolCalls['orphan_t1'].tool).toBe('fs.list')

    // A dev console warning must have been emitted with the stable prefix.
    expect(warnSpy).toHaveBeenCalledWith(
      expect.stringContaining('[chat] orphan frame'),
    )

    vi.useRealTimers()
    warnSpy.mockRestore()
  })
})

// Regression: TestChatRouter_NonSpawnCall_NoSpan
// flat tool_call_start (no parent_call_id) is NOT grouped into any span

describe('ChatStore regression: flat tool call without parent_call_id', () => {
  it('renders as a flat ToolCallBadge (not attached to any span)', () => {
    act(() => {
      useChatStore.getState().updateLastAssistantMessage('', false)
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'flat_1',
        tool: 'exec',
        params: { cmd: 'pwd' },
        session_id: TEST_SESSION_ID,
        // no parent_call_id
      })
    })

    const state = useChatStore.getState()
    // Tool call appears in the flat toolCalls map
    expect(state.toolCalls['flat_1']).toBeDefined()
    expect(state.toolCalls['flat_1'].tool).toBe('exec')

    // No span was created
    const lastMsg = state.messages[state.messages.length - 1]
    expect(lastMsg.spans ?? []).toHaveLength(0)
  })
})

// ── Replay parity tests ──────────────────────────────────────────────────────

// TDD row 18: ChatStore_ReplaySequence_MatchesLiveSequence
// Traces to: sprint-i-historical-replay-fidelity-spec.md FR-I-010
// Hard Constraint: "one reducer path" — live and replay sequences must produce
// identical ChatMessage shapes (excluding cursor/isStreaming flags).
describe('ChatStore_ReplaySequence_MatchesLiveSequence', () => {
  it('live token sequence and replay_message produce equivalent content, tool-call count, and ordering', () => {
    // Full reset including toolCallOrder (beforeEach only resets a subset of state)
    act(() => { useChatStore.getState().resetSession() })

    // ── Live sequence ──────────────────────────────────────────────────────────
    // Emit token frames producing text "A", then a tool call, then text "B", then done.
    act(() => {
      // Seed an assistant placeholder (sendMessage path does this; replicate here)
      useChatStore.getState().handleFrame({ type: 'token', content: 'A', session_id: TEST_SESSION_ID })
    })
    act(() => {
      // tool_call_start (no parent_call_id — flat call)
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_live_1',
        tool: 'shell',
        params: { cmd: 'echo hi' },
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_live_1',
        tool: 'shell',
        result: { stdout: 'hi\n' },
        status: 'success',
        duration_ms: 42,
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'B', session_id: TEST_SESSION_ID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: TEST_SESSION_ID })
    })

    const liveState = useChatStore.getState()
    // Extract the single assistant message
    const liveAssistant = liveState.messages.find((m) => m.role === 'assistant')
    expect(liveAssistant).toBeDefined()
    const liveContent = liveAssistant!.content           // "AB"
    const liveToolCallOrder = liveState.toolCallOrder    // ['tc_live_1']
    const liveToolCall = liveState.toolCalls['tc_live_1']
    expect(liveContent).toBe('AB')
    expect(liveToolCallOrder).toHaveLength(1)
    expect(liveToolCall.tool).toBe('shell')
    expect(liveToolCall.status).toBe('success')
    // Live sequence: streaming flags settled
    expect(liveAssistant!.isStreaming).toBe(false)

    // ── Reset ─────────────────────────────────────────────────────────────────
    act(() => {
      useChatStore.getState().resetSession()
    })

    // ── Replay sequence ────────────────────────────────────────────────────────
    // replay_message for the completed assistant text, then tool_call_start/result, then done.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'AB',
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_replay_1',
        tool: 'shell',
        params: { cmd: 'echo hi' },
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_replay_1',
        tool: 'shell',
        result: { stdout: 'hi\n' },
        status: 'success',
        duration_ms: 42,
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: TEST_SESSION_ID })
    })

    const replayState = useChatStore.getState()
    const replayAssistant = replayState.messages.find((m) => m.role === 'assistant')
    expect(replayAssistant).toBeDefined()

    // ── Assert shape parity ────────────────────────────────────────────────────
    // Content must match
    expect(replayAssistant!.content).toBe(liveContent)

    // Tool-call count must match
    expect(replayState.toolCallOrder).toHaveLength(liveToolCallOrder.length)

    // Tool-call properties must match
    const replayToolCall = replayState.toolCalls['tc_replay_1']
    expect(replayToolCall.tool).toBe(liveToolCall.tool)
    expect(replayToolCall.status).toBe(liveToolCall.status)

    // Cursor/streaming flags: replay_message arrives as a completed message (no cursor)
    // Live message: also settled after done. Both must be false.
    expect(replayAssistant!.isStreaming).toBe(false)
    // Live and replay both settle identically after done
    expect(replayAssistant!.isStreaming).toBe(liveAssistant!.isStreaming)
  })
})

// TDD row 19: ChatStore_ReplayMessageThenToolCall_InterleavesCorrectly
// Traces to: sprint-i-historical-replay-fidelity-spec.md FR-I-010
// Verifies that textAtToolCallStart is captured correctly when a tool_call_start
// follows a replay_message (completed, non-streaming assistant message).
describe('ChatStore_ReplayMessageThenToolCall_InterleavesCorrectly', () => {
  it('textAtToolCallStart snapshot equals the replay_message content when tool_call_start follows', () => {
    act(() => { useChatStore.getState().resetSession() })
    // Simulate: replay_message with content "Hello from replay" arrives, then tool_call_start
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'Hello from replay',
        session_id: TEST_SESSION_ID,
      })
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_interleave_1',
        tool: 'fs.read',
        params: { path: '/etc/hosts' },
        session_id: TEST_SESSION_ID,
      })
    })

    const state = useChatStore.getState()

    // The tool call must be registered
    expect(state.toolCalls['tc_interleave_1']).toBeDefined()
    expect(state.toolCalls['tc_interleave_1'].tool).toBe('fs.read')

    // textAtToolCallStart must capture the replay_message content at the
    // point the tool call started — this is the visual text position for interleaving.
    const snapshot = state.textAtToolCallStart['tc_interleave_1']
    expect(snapshot).toBe('Hello from replay')
  })

  it('textAtToolCallStart is empty string when tool_call_start arrives before any assistant message', () => {
    act(() => { useChatStore.getState().resetSession() })
    // During replay, tool_call_start may arrive after an entry with no text content.
    // The snapshot should be '' not undefined.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_no_text',
        tool: 'web_search',
        params: { query: 'test' },
        session_id: TEST_SESSION_ID,
      })
    })

    const state = useChatStore.getState()
    const snapshot = state.textAtToolCallStart['tc_no_text']
    expect(snapshot).toBe('')
  })
})

// TDD row 18 supplement: isReplaying flag transitions
describe('ChatStore_isReplaying_flag', () => {
  it('starts false, can be set true via setReplaying, cleared to false on done (with 750ms minimum display window)', async () => {
    // W2-6(a): MIN_REPLAY_DISPLAY_MS raised from 250ms → 750ms (FR-I-014 timing fix).
    // The minimum window is long enough for Playwright's page.click() to return and
    // the test assertion to start polling — without this, the timer fires before the
    // first poll and the textarea is already enabled when Playwright checks.
    // Traces to: temporal-puzzling-melody.md W2-6(a)
    // Initial state
    expect(useChatStore.getState().isReplaying).toBe(false)

    // Simulate attach_session triggering setReplaying(true)
    act(() => {
      useChatStore.getState().setReplaying(true)
    })
    expect(useChatStore.getState().isReplaying).toBe(true)

    // done frame schedules clear — but minimum 750ms display window is enforced
    // so the placeholder doesn't flicker on sub-frame replays and Playwright can
    // observe the disabled state before the window expires.
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: TEST_SESSION_ID })
    })
    // Still true immediately after done (inside the window).
    expect(useChatStore.getState().isReplaying).toBe(true)

    // After >= 750ms the setTimeout fires and flips it.
    await new Promise((r) => setTimeout(r, 800))
    expect(useChatStore.getState().isReplaying).toBe(false)
  })

  it('done frame while not replaying is harmless — isReplaying stays false', () => {
    expect(useChatStore.getState().isReplaying).toBe(false)
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: TEST_SESSION_ID })
    })
    expect(useChatStore.getState().isReplaying).toBe(false)
  })

  it('resetSession clears isReplaying', () => {
    act(() => {
      useChatStore.getState().setReplaying(true)
    })
    expect(useChatStore.getState().isReplaying).toBe(true)
    act(() => {
      useChatStore.getState().resetSession()
    })
    expect(useChatStore.getState().isReplaying).toBe(false)
  })

  // W2-6(b): setReplaying(false) when already false is a no-op.
  it('setReplaying(false) when already false is a no-op — isReplaying stays false', () => {
    // BDD: Given isReplaying is already false
    // BDD: When setReplaying(false) is called
    // BDD: Then isReplaying stays false (no state change)
    // Traces to: temporal-puzzling-melody.md W2-6(b)
    expect(useChatStore.getState().isReplaying).toBe(false)
    act(() => {
      useChatStore.getState().setReplaying(false)
    })
    expect(useChatStore.getState().isReplaying).toBe(false)
  })

  // W2-6(c): setReplaying(true) resets the minimum window AND cancels any pending
  // clear-timer from the previous attach.
  //
  // Updated 2026-05-11: the previous behaviour (no-reset on re-true) made the
  // window observable for as little as ~0ms if a stale `replayingStartedAt`
  // from minutes earlier was still in the map (e.g. a session reopened in the
  // same SPA session). FR-I-014 requires a guaranteed visible window on every
  // attach; the only correct policy is to reset the window each time replay
  // is re-armed and cancel any in-flight false-flip timer that would otherwise
  // fire mid-window and prematurely re-enable the composer.
  it('setReplaying(true) when already true resets the minimum window and cancels stale timers', async () => {
    // BDD: Given setReplaying(true) was called at T=0, starting the 750ms window
    // BDD: When setReplaying(true) is called again at T=700ms
    // BDD: Then the window restarts — isReplaying stays true until at least T=700+750=1450ms

    act(() => {
      useChatStore.getState().setReplaying(true)
    })
    expect(useChatStore.getState().isReplaying).toBe(true)

    // Wait 700ms (still inside the 750ms window from the first call).
    await new Promise((r) => setTimeout(r, 700))

    // Second setReplaying(true) — must reset the window.
    act(() => {
      useChatStore.getState().setReplaying(true)
    })
    expect(useChatStore.getState().isReplaying).toBe(true)

    // Done frame schedules a clear. With the new behaviour, replayingStartedAt
    // was just reset to ~T=700, so elapsed=0, and the clear is scheduled
    // 750ms from now (~T=1450ms from the original T=0).
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: TEST_SESSION_ID })
    })

    // 100ms after the second setReplaying(true) — well inside the fresh 750ms window.
    // With the old (broken) behaviour, isReplaying would already be false because the
    // stale start time put us past 750ms. With the new behaviour, it stays true.
    await new Promise((r) => setTimeout(r, 100))
    expect(useChatStore.getState().isReplaying).toBe(true)

    // After the fresh window expires (~750ms after the second setReplaying), it clears.
    await new Promise((r) => setTimeout(r, 700))
    expect(useChatStore.getState().isReplaying).toBe(false)
  })
})

// ── B1.3(d) — unknown-targetSid done frame handling ───────────────────────────

describe('chat store — done frame for unknown targetSid (B1.3d)', () => {
  // Traces to: B1.3(d) security hardening
  // When a done frame arrives for a targetSid that is not in sessionsById, the
  // store must log a warning and force-clear isStreaming on the active bucket so
  // the spinner does not render indefinitely.

  it('logs console.warn with chat.done_unknown_sid when targetSid is not in sessionsById', () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})

    act(() => {
      // Active session IS in the store (set by beforeEach → resetStore)
      useChatStore.getState().appendMessage({
        id: 'asst_streaming',
        session_id: TEST_SESSION_ID,
        role: 'assistant',
        content: 'streaming…',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })

      // done arrives for a session that is NOT in sessionsById
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: 'unknown-session-xyz',
      })
    })

    expect(warnSpy).toHaveBeenCalledWith(
      'chat.done_unknown_sid',
      expect.objectContaining({ targetSid: 'unknown-session-xyz' })
    )

    warnSpy.mockRestore()
  })

  it('force-clears isStreaming on the active bucket when done arrives for unknown targetSid', () => {
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst_stuck',
        session_id: TEST_SESSION_ID,
        role: 'assistant',
        content: 'partial…',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })
    })

    // Verify we start streaming
    expect(useChatStore.getState().isStreaming).toBe(true)

    act(() => {
      // done for an unknown session — the active bucket must recover
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: 'unknown-session-xyz',
      })
    })

    // isStreaming must be cleared on the active bucket
    expect(useChatStore.getState().isStreaming).toBe(false)
    // The active bucket in sessionsById must also reflect the cleared state
    const activeBucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(activeBucket?.isStreaming).toBe(false)
  })

  it('processes done normally when targetSid is known', () => {
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst_known',
        session_id: TEST_SESSION_ID,
        role: 'assistant',
        content: 'some text',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })

      // done for the known TEST_SESSION_ID — normal path
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: TEST_SESSION_ID,
        stats: { tokens: 42, cost: 0.001, duration_ms: 100 },
      })
    })

    expect(useChatStore.getState().isStreaming).toBe(false)
    const msg = useChatStore.getState().messages.find((m) => m.id === 'asst_known')
    expect(msg?.status).toBe('done')
    expect(useChatStore.getState().sessionTokens).toBe(42)
  })
})

// W2-10: Sibling-spans cross-wire test.
// Two spans A (parentCallId "cA") and B (parentCallId "cB") open.
// Emit 2 tool_call_start frames both with parent_call_id "cA".
// Assert A.steps.length === 2 AND B.steps.length === 0.
// Guards against a routing bug that could increment both spans' counters.
//
// Traces to: temporal-puzzling-melody.md W2-10
describe('ChatStore_sibling_spans_crosswire (W2-10)', () => {
  it('tool_call_start with parent_call_id "cA" routes to span A only, not span B', () => {
    // BDD: Given two open spans A (parentCallId "cA") and B (parentCallId "cB")
    // BDD: When 2 tool_call_start frames arrive with parent_call_id "cA"
    // BDD: Then span A has 2 steps and span B has 0 steps
    // Traces to: temporal-puzzling-melody.md W2-10

    act(() => {
      // Create an assistant message to host the spans
      useChatStore.getState().appendMessage({
        id: 'asst-sibling-1',
        role: 'assistant',
        content: 'Working...',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })

      // Start span A (parentCallId = "cA")
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'spanA',
        parent_call_id: 'cA',
        task_label: 'Span A task',
        agent_id: 'agent-a',
        session_id: TEST_SESSION_ID,
      })

      // Start span B (parentCallId = "cB")
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'spanB',
        parent_call_id: 'cB',
        task_label: 'Span B task',
        agent_id: 'agent-b',
        session_id: TEST_SESSION_ID,
      })
    })

    // Emit 2 tool_call_start frames, both targeting span A (parent_call_id: "cA")
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'step_a_1',
        tool: 'web_search',
        params: { query: 'query 1' },
        parent_call_id: 'cA',
        session_id: TEST_SESSION_ID,
      })
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'step_a_2',
        tool: 'fs.read',
        params: { path: '/tmp/test' },
        parent_call_id: 'cA',
        session_id: TEST_SESSION_ID,
      })
    })

    const state = useChatStore.getState()
    const asstMsg = state.messages.find((m) => m.id === 'asst-sibling-1')
    expect(asstMsg).toBeDefined()
    expect(asstMsg?.spans).toHaveLength(2)

    const spanA = asstMsg!.spans!.find((s) => s.spanId === 'spanA')
    const spanB = asstMsg!.spans!.find((s) => s.spanId === 'spanB')
    expect(spanA).toBeDefined()
    expect(spanB).toBeDefined()

    // Span A must have exactly 2 steps (both tool_call_start frames targeted "cA")
    expect(spanA!.steps).toHaveLength(2)
    const stepA0 = spanA!.steps[0]
    const stepA1 = spanA!.steps[1]
    expect(stepA0.kind === 'tool' ? stepA0.tool.call_id : undefined).toBe('step_a_1')
    expect(stepA1.kind === 'tool' ? stepA1.tool.call_id : undefined).toBe('step_a_2')

    // Span B must have exactly 0 steps (no frames targeted "cB")
    expect(spanB!.steps).toHaveLength(0)
  })
})

// H1-FE: Regression — unknown-sid done must not corrupt an active mid-stream session.
// When a `done` frame arrives for a session_id not in sessionsById (e.g. a deleted
// or replayed session), the handler should NOT force-clear isStreaming on the active
// bucket if the active session sent a user message recently and is still streaming.
describe('chat store — H1-FE: unknown-sid done does not corrupt active stream', () => {
  const ACTIVE_SID = 'active-session'
  const GHOST_SID = 'ghost-session-wiped'

  function seedActiveMidStream(lastUserMessageAt: number) {
    act(() => {
      useSessionStore.setState({ activeSessionId: ACTIVE_SID, activeAgentId: null, activeAgentType: null })
      const seedMsgs = [
        { id: 'u1', session_id: ACTIVE_SID, role: 'user' as const, content: 'hi', timestamp: new Date().toISOString(), status: 'done' as const },
        { id: 'a1', session_id: ACTIVE_SID, role: 'assistant' as const, content: 'thinking…', timestamp: new Date().toISOString(), status: 'streaming' as const, isStreaming: true },
      ]
      useChatStore.setState((state) => ({
        sessionsById: {
          ...state.sessionsById,
          [ACTIVE_SID]: {
            ...makeBucketMessages(seedMsgs),
            toolCalls: {},
            toolCallOrder: [],
            textAtToolCallStart: {},
            pendingApprovals: [],
            isStreaming: true,
            isReplaying: false,
            replayCompletedForSession: null,
            sessionTokens: 0,
            sessionCost: 0,
            rateLimitEvent: null,
            cancelStage: null,
            lastUserMessageAt,
            lastReceivedEventTime: null,
            spanByParentCallId: {},
          },
        },
        // Sync foreground fields
        isStreaming: true,
        messages: seedMsgs,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        pendingApprovals: [],
        sessionTokens: 0,
        sessionCost: 0,
        isReplaying: false,
        replayCompletedForSession: null,
        rateLimitEvent: null,
        lastUserMessageAt,
        lastReceivedEventTime: null,
      }))
    })
  }

  it('leaves active bucket isStreaming=true when unknown-sid done arrives mid-stream (within 10s)', () => {
    // Seed active session as streaming, user sent message 2 seconds ago
    seedActiveMidStream(Date.now() - 2_000)

    // Dispatch a done frame for the ghost session (not in sessionsById)
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: GHOST_SID,
        stats: { tokens: 0, cost: 0, duration_ms: 0 },
      })
    })

    // Active bucket must still be streaming — the unknown-sid done must not touch it
    const activeBucket = useChatStore.getState().sessionsById[ACTIVE_SID]
    expect(activeBucket?.isStreaming).toBe(true)
  })

  it('clears active bucket isStreaming when unknown-sid done arrives after grace period (>10s)', () => {
    // Seed active session as streaming, but user sent message 15 seconds ago
    // (stale spinner from a wiped session — safe to clear)
    seedActiveMidStream(Date.now() - 15_000)

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: GHOST_SID,
        stats: { tokens: 0, cost: 0, duration_ms: 0 },
      })
    })

    // Active bucket spinner is stale — should be cleared
    const activeBucket = useChatStore.getState().sessionsById[ACTIVE_SID]
    expect(activeBucket?.isStreaming).toBe(false)
  })
})

// B3: cancel_stage frame handling
// Refs: docs/internal/specs/cancel-cross-channel-spec-review.md B3, L-1
describe('chat store — cancel_stage frame (B3)', () => {
  it('sets cancelStage to "graceful" when a cancel_stage graceful frame arrives', () => {
    // Seed session as streaming so the frame has a valid target bucket.
    act(() => {
      useChatStore.setState({
        sessionsById: {
          [TEST_SESSION_ID]: {
            ...useChatStore.getState().sessionsById[TEST_SESSION_ID] ?? {
              ...makeBucketMessages([]),
              toolCalls: {},
              toolCallOrder: [],
              textAtToolCallStart: {},
              pendingApprovals: [],
              isReplaying: false,
              replayCompletedForSession: null,
              sessionTokens: 0,
              sessionCost: 0,
              rateLimitEvent: null,
              lastUserMessageAt: null,
              cancelStage: null,
              lastReceivedEventTime: null,
              spanByParentCallId: {},
            },
            isStreaming: true,
          },
        },
      })
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'cancel_stage',
        session_id: TEST_SESSION_ID,
        stage: 'graceful',
      })
    })

    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(bucket?.cancelStage).toBe('graceful')
    // Foreground field is synced too.
    expect(useChatStore.getState().cancelStage).toBe('graceful')
  })

  it('sets cancelStage to "hard" when a cancel_stage hard frame arrives', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'cancel_stage',
        session_id: TEST_SESSION_ID,
        stage: 'hard',
      })
    })

    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(bucket?.cancelStage).toBe('hard')
  })

  it('sets cancelStage to "detached" when a cancel_stage detached frame arrives', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'cancel_stage',
        session_id: TEST_SESSION_ID,
        stage: 'detached',
      })
    })

    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(bucket?.cancelStage).toBe('detached')
  })

  it('clears cancelStage to null when a done frame arrives after cancel', () => {
    // First set a cancel stage.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'cancel_stage',
        session_id: TEST_SESSION_ID,
        stage: 'hard',
      })
    })
    expect(useChatStore.getState().sessionsById[TEST_SESSION_ID]?.cancelStage).toBe('hard')

    // Then receive the done frame.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: TEST_SESSION_ID,
        stats: { tokens: 10, cost: 0.001, duration_ms: 500 },
      })
    })

    expect(useChatStore.getState().sessionsById[TEST_SESSION_ID]?.cancelStage).toBeNull()
    expect(useChatStore.getState().cancelStage).toBeNull()
  })
})

// ── G1: MAX_MESSAGES_PER_SESSION ring-buffer enforcement ──────────────────────

describe('G1: ring buffer — MAX_MESSAGES_PER_SESSION enforcement', () => {
  it('appending 600 messages leaves exactly MAX_MESSAGES_PER_SESSION messages in the bucket', () => {
    // Seed 600 replay_message frames via resetSessionForReplay + handleFrame.
    act(() => {
      useChatStore.getState().resetSessionForReplay(TEST_SESSION_ID)
    })

    for (let i = 0; i < 600; i++) {
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message',
          session_id: TEST_SESSION_ID,
          role: i % 2 === 0 ? 'user' : 'assistant',
          content: `Message ${i}`,
          timestamp: new Date(Date.now() + i * 1000).toISOString(),
        })
      })
    }

    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    const msgs = bucket ? getMessages(bucket) : []
    expect(
      msgs.length,
      `Expected exactly ${MAX_MESSAGES_PER_SESSION} messages after ring-buffer trim, got ${msgs.length}`,
    ).toBe(MAX_MESSAGES_PER_SESSION)

    // Verify the oldest messages were evicted (we should see messages 100–599)
    expect(msgs[0].content).toBe('Message 100')
    expect(msgs[msgs.length - 1].content).toBe('Message 599')

    // trimmedCount must reflect the eviction
    expect(bucket?.trimmedCount).toBeGreaterThan(0)
  })
})

// ── G4: clampToolResult — client-side truncation ──────────────────────────────

describe('G4: clampToolResult — large result is clamped to ClientTruncatedResult', () => {
  it('a 100 KiB string result is replaced with a _truncated_client sentinel', () => {
    // Build a 100 KiB string (100 * 1024 = 102400 chars).
    const largeString = 'x'.repeat(100 * 1024)
    const clamped = clampToolResult(largeString)

    expect(
      isClientTruncatedResult(clamped),
      'clampToolResult must return a ClientTruncatedResult for a 100 KiB input',
    ).toBe(true)

    if (isClientTruncatedResult(clamped)) {
      expect(clamped._truncated_client).toBe(true)
      expect(clamped.original_size_bytes).toBeGreaterThan(0)
      // Preview must be exactly 4 KiB (4096 chars from the original)
      expect(clamped.preview.length).toBe(4096)
      expect(clamped.preview).toBe(largeString.slice(0, 4096))
    }
  })

  it('a small result (under 50 KiB) passes through unchanged', () => {
    const small = { status: 'ok', data: 'hello' }
    const result = clampToolResult(small)
    expect(result).toBe(small)
    expect(isClientTruncatedResult(result)).toBe(false)
  })

  it('an existing _truncated sentinel passes through unchanged (no double-wrapping)', () => {
    const serverTruncated = { _truncated: true as const, original_size_bytes: 500_000, preview: 'first bytes...' }
    const result = clampToolResult(serverTruncated)
    expect(result).toBe(serverTruncated)
    expect(isClientTruncatedResult(result)).toBe(false)
  })
})

// ── G2: span-index O(1) hit before scan-fallback ──────────────────────────────

describe('G2: spanByParentCallId O(1) index is written on subagent_start and cleared on subagent_end', () => {
  it('spanByParentCallId has the parentCallId → {messageId, spanIdx} entry after subagent_start', () => {
    // Arrange: an active streaming assistant message (so the span attaches to it).
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_started',
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token',
        session_id: TEST_SESSION_ID,
        content: 'Planning...',
      })
    })

    // Inject subagent_start with a known parentCallId.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        session_id: TEST_SESSION_ID,
        span_id: 'span-g2-test',
        parent_call_id: 'tc-g2-parent',
        task_label: 'Run subtask',
      })
    })

    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(bucket).toBeDefined()

    const index = bucket!.spanByParentCallId
    expect(
      'tc-g2-parent' in index,
      'spanByParentCallId must contain the parentCallId after subagent_start',
    ).toBe(true)
    expect(index['tc-g2-parent'].messageId).toBeTruthy()
    expect(typeof index['tc-g2-parent'].spanIdx).toBe('number')
  })

  it('spanByParentCallId entry is removed after subagent_end', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_started',
        session_id: TEST_SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'token',
        session_id: TEST_SESSION_ID,
        content: 'Planning...',
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        session_id: TEST_SESSION_ID,
        span_id: 'span-g2-end-test',
        parent_call_id: 'tc-g2-end',
        task_label: 'Run subtask',
      })
    })

    // Verify the index was written.
    const before = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect('tc-g2-end' in (before?.spanByParentCallId ?? {})).toBe(true)

    // subagent_end must clear the index entry.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'subagent_end',
        session_id: TEST_SESSION_ID,
        span_id: 'span-g2-end-test',
        status: 'success',
        duration_ms: 500,
        final_result: 'done',
      })
    })

    const after = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(
      'tc-g2-end' in (after?.spanByParentCallId ?? {}),
      'spanByParentCallId must NOT contain the parentCallId after subagent_end',
    ).toBe(false)
  })
})

// ── G5: lastReceivedEventTime advances monotonically ─────────────────────────

describe('G5: lastReceivedEventTime advances on each replay_message with a newer timestamp', () => {
  it('lastReceivedEventTime is null initially, then advances with each timestamped replay frame', () => {
    act(() => {
      useChatStore.getState().resetSessionForReplay(TEST_SESSION_ID)
    })

    const t0 = '2026-01-01T00:00:00.000Z'
    const t1 = '2026-01-01T00:00:01.000Z'
    const t2 = '2026-01-01T00:00:02.000Z'

    // Initial state: no lastReceivedEventTime.
    const init = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(init?.lastReceivedEventTime).toBeNull()

    // First frame at t0.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        session_id: TEST_SESSION_ID,
        role: 'user',
        content: 'hello',
        timestamp: t0,
      })
    })
    const after0 = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(after0?.lastReceivedEventTime).toBe(t0)

    // Second frame at t1 (newer) — must advance.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        session_id: TEST_SESSION_ID,
        role: 'assistant',
        content: 'world',
        timestamp: t1,
      })
    })
    const after1 = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(after1?.lastReceivedEventTime).toBe(t1)

    // Third frame at t2 (newer) — must advance.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        session_id: TEST_SESSION_ID,
        role: 'user',
        content: 'again',
        timestamp: t2,
      })
    })
    const after2 = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(after2?.lastReceivedEventTime).toBe(t2)

    // An older frame must NOT rewind the cursor.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        session_id: TEST_SESSION_ID,
        role: 'user',
        content: 'old',
        timestamp: t0, // older than t2
      })
    })
    const afterOld = useChatStore.getState().sessionsById[TEST_SESSION_ID]
    expect(
      afterOld?.lastReceivedEventTime,
      'lastReceivedEventTime must not rewind when an older-timestamped frame arrives',
    ).toBe(t2)
  })
})

// ── evictMessageFromBucket unit tests ─────────────────────────────────────────

describe('evictMessageFromBucket — full sweep of dependent maps', () => {
  it('removes the message from messagesById and messageOrder', () => {
    const bucket: SessionChatState = {
      messagesById: { 'm1': { id: 'm1', role: 'user', content: 'hi', timestamp: '', status: 'done' } },
      messageOrder: ['m1'],
      trimmedCount: 0,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      spanByParentCallId: {},
      pendingApprovals: [],
      isStreaming: false,
      isReplaying: false,
      replayCompletedForSession: null,
      sessionTokens: 0,
      sessionCost: 0,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
    }
    evictMessageFromBucket(bucket, 'm1')
    expect(bucket.messageOrder).toHaveLength(0)
    expect(bucket.messagesById['m1']).toBeUndefined()
  })

  it('removes tool calls owned by the evicted message (via tool_calls array)', () => {
    const bucket: SessionChatState = {
      messagesById: {
        'm1': {
          id: 'm1', role: 'assistant', content: '', timestamp: '', status: 'done',
          tool_calls: [{ id: 'tc1', tool: 'exec', params: {}, status: 'success' }],
        },
      },
      messageOrder: ['m1'],
      trimmedCount: 0,
      toolCalls: { tc1: { id: 'tc1', call_id: 'tc1', tool: 'exec', params: {}, status: 'success' } },
      toolCallOrder: ['tc1'],
      textAtToolCallStart: { tc1: 'some text' },
      spanByParentCallId: {},
      pendingApprovals: [],
      isStreaming: false,
      isReplaying: false,
      replayCompletedForSession: null,
      sessionTokens: 0,
      sessionCost: 0,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
    }
    evictMessageFromBucket(bucket, 'm1')
    expect(bucket.toolCalls['tc1']).toBeUndefined()
    expect(bucket.toolCallOrder).toHaveLength(0)
    expect(bucket.textAtToolCallStart['tc1']).toBeUndefined()
  })

  it('removes spanByParentCallId entries pointing at the evicted message', () => {
    const bucket: SessionChatState = {
      messagesById: {
        'm1': { id: 'm1', role: 'assistant', content: '', timestamp: '', status: 'done' },
      },
      messageOrder: ['m1'],
      trimmedCount: 0,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      spanByParentCallId: {
        'pc1': { messageId: 'm1', spanIdx: 0 },
        'pc2': { messageId: 'm2', spanIdx: 0 }, // belongs to a different message — must survive
      },
      pendingApprovals: [],
      isStreaming: false,
      isReplaying: false,
      replayCompletedForSession: null,
      sessionTokens: 0,
      sessionCost: 0,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
    }
    evictMessageFromBucket(bucket, 'm1')
    expect(bucket.spanByParentCallId['pc1']).toBeUndefined()
    // pc2 belongs to a different message and must not be touched.
    expect(bucket.spanByParentCallId['pc2']).toBeDefined()
  })
})

// ── Eviction-leak regression: applyMessageArray path ─────────────────────────
// Seeds 600 messages where every assistant message has 3 tool calls and 1 span.
// Trims via appendMessage (applyMessageArray path) and asserts dependent maps
// evict the correct entries.

describe('eviction-leak regression — applyMessageArray evicts spanByParentCallId and textAtToolCallStart', () => {
  it('after trimming 100 messages, spanByParentCallId and textAtToolCallStart track the surviving set', () => {
    act(() => {
      useChatStore.getState().resetSession()
    })

    // Seed 600 assistant messages, each with 3 tool calls (in tool_calls array).
    // Also manually populate spanByParentCallId and textAtToolCallStart to simulate
    // in-flight spans and tool call snapshots.
    const TOTAL = 600

    // Build the bucket directly to avoid the test going through 600 handleFrame cycles.
    const msgs: import('./chat').ChatMessage[] = []
    const toolCalls: SessionChatState['toolCalls'] = {}
    const toolCallOrder: string[] = []
    const textAtToolCallStart: SessionChatState['textAtToolCallStart'] = {}
    const spanByParentCallId: SessionChatState['spanByParentCallId'] = {}

    for (let i = 0; i < TOTAL; i++) {
      const msgId = `msg_${i}`
      const tcIds = [`tc_${i}_0`, `tc_${i}_1`, `tc_${i}_2`]
      const parentCallId = `pc_${i}`

      msgs.push({
        id: msgId,
        role: 'assistant' as const,
        content: `Message ${i}`,
        timestamp: new Date(Date.now() + i * 1000).toISOString(),
        status: 'done' as const,
        tool_calls: tcIds.map((tcId) => ({ id: tcId, tool: 'exec', params: {}, status: 'success' as const })),
      })

      for (const tcId of tcIds) {
        toolCalls[tcId] = { id: tcId, call_id: tcId, tool: 'exec', params: {}, status: 'success' }
        toolCallOrder.push(tcId)
        textAtToolCallStart[tcId] = `snapshot for ${tcId}`
      }

      // Each message also has a span entry in the index.
      spanByParentCallId[parentCallId] = { messageId: msgId, spanIdx: 0 }
    }

    act(() => {
      useChatStore.setState((s) => ({
        sessionsById: {
          ...s.sessionsById,
          [TEST_SESSION_ID]: {
            ...makeBucketMessages(msgs),
            toolCalls,
            toolCallOrder,
            textAtToolCallStart,
            spanByParentCallId,
            pendingApprovals: [],
            isStreaming: false,
            isReplaying: false,
            replayCompletedForSession: null,
            sessionTokens: 0,
            sessionCost: 0,
            rateLimitEvent: null,
            lastUserMessageAt: null,
            cancelStage: null,
            lastReceivedEventTime: null,
          },
        },
      }))
    })

    // Now trigger appendMessage which will go through applyMessageArray and trim.
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'trigger_msg',
        role: 'user',
        content: 'trigger',
        timestamp: new Date().toISOString(),
        status: 'done',
      })
    })

    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]!
    const survivingMsgIds = new Set(bucket.messageOrder)

    // There should be exactly MAX_MESSAGES_PER_SESSION messages after trim.
    expect(bucket.messageOrder.length).toBe(MAX_MESSAGES_PER_SESSION)

    // All spanByParentCallId entries must point to surviving messages.
    for (const [parentCallId, entry] of Object.entries(bucket.spanByParentCallId)) {
      expect(
        survivingMsgIds.has(entry.messageId),
        `spanByParentCallId["${parentCallId}"] points to evicted message "${entry.messageId}"`,
      ).toBe(true)
    }

    // Count how many span entries we expect: one per surviving assistant message.
    const survivingAssistantMsgIds = new Set(
      bucket.messageOrder.filter((id) => bucket.messagesById[id]?.role === 'assistant')
    )
    expect(Object.keys(bucket.spanByParentCallId).length).toBe(survivingAssistantMsgIds.size)

    // All textAtToolCallStart entries must have call_ids present in toolCallOrder.
    const liveCallIdSet = new Set(bucket.toolCallOrder)
    for (const callId of Object.keys(bucket.textAtToolCallStart)) {
      expect(
        liveCallIdSet.has(callId),
        `textAtToolCallStart has entry for evicted call_id "${callId}"`,
      ).toBe(true)
    }
  })
})

// ── Eviction-leak regression: replay_message path ────────────────────────────
// Sends 600+ replay_message frames so the inline replay-path eviction fires.
// Asserts spanByParentCallId and textAtToolCallStart are clean after replay.

describe('eviction-leak regression — replay_message path evicts dependent maps', () => {
  it('after replaying 600 messages with spans, spanByParentCallId has no dangling entries', () => {
    act(() => {
      useChatStore.getState().resetSessionForReplay(TEST_SESSION_ID)
    })

    const TOTAL = 600

    // Pre-populate spanByParentCallId with entries for messages 0..599,
    // then send replay_message frames for all 600 messages so the ring buffer
    // evicts the oldest 100 via the inline replay-path.
    const spanByParentCallId: SessionChatState['spanByParentCallId'] = {}
    for (let i = 0; i < TOTAL; i++) {
      spanByParentCallId[`pc_${i}`] = { messageId: `replay_msg_${i}`, spanIdx: 0 }
    }

    act(() => {
      useChatStore.setState((s) => ({
        sessionsById: {
          ...s.sessionsById,
          [TEST_SESSION_ID]: {
            ...s.sessionsById[TEST_SESSION_ID]!,
            spanByParentCallId,
          },
        },
      }))
    })

    // Replay 600 messages — the ring buffer evicts old ones via the replay path.
    for (let i = 0; i < TOTAL; i++) {
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'replay_message',
          session_id: TEST_SESSION_ID,
          id: `replay_msg_${i}`,
          role: i % 2 === 0 ? 'user' : 'assistant',
          content: `Replay ${i}`,
          timestamp: new Date(Date.now() + i * 1000).toISOString(),
        })
      })
    }

    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]!
    const survivingMsgIds = new Set(bucket.messageOrder)

    expect(bucket.messageOrder.length).toBe(MAX_MESSAGES_PER_SESSION)

    // After replay, all spanByParentCallId entries must point to surviving messages only.
    for (const [parentCallId, entry] of Object.entries(bucket.spanByParentCallId)) {
      expect(
        survivingMsgIds.has(entry.messageId),
        `spanByParentCallId["${parentCallId}"] points to evicted message "${entry.messageId}" after replay eviction`,
      ).toBe(true)
    }
  })
})

// ── Eviction-leak regression: tool_call_result burst path ────────────────────
// Verifies the ring buffer stays clean when a burst of tool_call_result frames
// (not just replay_message) causes eviction.

describe('eviction-leak regression — tool_call_result burst triggers eviction with clean maps', () => {
  it('after 501 appendMessage calls with tool calls, textAtToolCallStart contains no evicted entries', () => {
    act(() => {
      useChatStore.getState().resetSession()
    })

    // Append MAX+1 messages where each is an assistant message with a tool call
    // snapshot recorded via startToolCall (which writes textAtToolCallStart).
    // We drive this through handleFrame to exercise the full pipeline.
    for (let i = 0; i < MAX_MESSAGES_PER_SESSION + 1; i++) {
      act(() => {
        // A tool_call_start frame adds a textAtToolCallStart entry.
        useChatStore.getState().handleFrame({
          type: 'tool_call_start',
          call_id: `burst_tc_${i}`,
          tool: 'exec',
          params: { cmd: `cmd_${i}` },
          session_id: TEST_SESSION_ID,
        })
      })
      act(() => {
        // A done frame bakes the tool calls into the message and closes the turn.
        // Each done frame appends a completed assistant message + resets toolCalls.
        useChatStore.getState().handleFrame({
          type: 'tool_call_result',
          call_id: `burst_tc_${i}`,
          tool: 'exec',
          result: { exit_code: 0 },
          status: 'success',
          duration_ms: 10,
          session_id: TEST_SESSION_ID,
        })
      })
    }

    const bucket = useChatStore.getState().sessionsById[TEST_SESSION_ID]!

    // All textAtToolCallStart entries must have a corresponding live toolCallOrder entry.
    const liveCallIdSet = new Set(bucket.toolCallOrder)
    for (const callId of Object.keys(bucket.textAtToolCallStart)) {
      expect(
        liveCallIdSet.has(callId),
        `textAtToolCallStart has a dangling entry for "${callId}" not in toolCallOrder`,
      ).toBe(true)
    }
  })
})
