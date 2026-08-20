import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { ChatThread } from '@/components/chat/ChatThread'

// test_chat_streaming_integration (test #24)
// test_cancel_integration (test #40)
// Traces to: wave5a-wire-ui-spec.md — Scenario: User sends message and receives streaming response
//             wave5a-wire-ui-spec.md — Scenario: Cancel during streaming preserves partial response

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchSessionMessages: vi.fn().mockResolvedValue([]) }
})

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function wrapper({ children }: { children: React.ReactNode }) {
  return <QueryClientProvider client={makeClient()}>{children}</QueryClientProvider>
}

beforeEach(() => {
  act(() => {
    // Clear sessionsById so per-session buckets don't leak across tests.
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      isStreaming: false,
      toolCalls: {},
    })
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    useSessionStore.setState({ activeSessionId: null, activeAgentId: null })
  })
})

describe('chat streaming integration (test #24) — token rendering', () => {
  it('renders tokens incrementally as handleFrame is called', async () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: User sends message and receives streaming response
    render(<ChatThread />, { wrapper })

    act(() => {
      // appendMessage routes to the active session; handleFrame routes by
      // frame.session_id. The test wires both to sess_test so they meet in
      // the same bucket, otherwise tokens land in a different bucket from
      // the assistant placeholder and the rendered DOM stays empty.
      useSessionStore.setState({ activeSessionId: 'sess_test' })
      useChatStore.getState().appendMessage({
        id: 'user_1',
        session_id: 'sess_test',
        role: 'user',
        content: 'Hello, world!',
        timestamp: new Date().toISOString(),
        status: 'done',
      })
      useChatStore.getState().appendMessage({
        id: 'asst_1',
        session_id: 'sess_test',
        role: 'assistant',
        content: '',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })
      useChatStore.getState().handleFrame({ type: 'token', session_id: 'sess_test', content: 'Hello' })
      useChatStore.getState().handleFrame({ type: 'token', session_id: 'sess_test', content: ' world' })
    })

    await waitFor(() => {
      expect(screen.getByText(/Hello world/i)).toBeInTheDocument()
    })
  })

  it('clears streaming state and renders final content on done frame', async () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Streaming response completes with markdown
    render(<ChatThread />, { wrapper })

    act(() => {
      useSessionStore.setState({ activeSessionId: 'sess_test' })
      useChatStore.getState().appendMessage({
        id: 'asst_2',
        session_id: 'sess_test',
        role: 'assistant',
        content: '# Heading',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })
      useChatStore.getState().handleFrame({ type: 'done', session_id: 'sess_test', stats: { tokens: 150, cost: 0.02, duration_ms: 0 } })
    })

    await waitFor(() => {
      expect(useChatStore.getState().isStreaming).toBe(false)
    })
    const msg = useChatStore.getState().messages.find((m) => m.id === 'asst_2')
    expect(msg?.status).toBe('done')
  })

  it('records connectionError on error frame when no assistant message exists', async () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: WebSocket connection error during streaming
    // When NO assistant message exists, error frames are connection-level and set connectionError.
    render(<ChatThread />, { wrapper })

    act(() => {
      // No assistant message appended — error is connection-level
      useChatStore.setState({ isStreaming: true })
      useChatStore.getState().handleFrame({ type: 'error', message: 'Connection lost' })
    })

    await waitFor(() => {
      expect(useChatStore.getState().isStreaming).toBe(false)
      expect(useConnectionStore.getState().connectionError).toBe('Connection lost')
    })
  })

  it('does NOT set connectionError on error frame when assistant message exists', async () => {
    // Message-level errors update the message inline without setting the global banner
    render(<ChatThread />, { wrapper })

    act(() => {
      useChatStore.getState().appendMessage({
        id: 'asst_3',
        session_id: 'sess_test',
        role: 'assistant',
        content: '',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })
      useChatStore.setState({ isStreaming: true })
      useChatStore.getState().handleFrame({ type: 'error', message: 'Connection lost' })
    })

    await waitFor(() => {
      expect(useChatStore.getState().isStreaming).toBe(false)
      // Message-level error does NOT set connectionError
      expect(useConnectionStore.getState().connectionError).toBeNull()
      // Instead, the message itself has error status
      const msg = useChatStore.getState().messages.find((m) => m.id === 'asst_3')
      expect(msg?.status).toBe('error')
    })
  })
})

describe('cancel integration (test #40)', () => {
  it('sends cancel frame and marks partial response as interrupted', async () => {
    // Traces to: wave5a-wire-ui-spec.md — test_cancel_integration (test #40)
    const mockSend = vi.fn()
    render(<ChatThread />, { wrapper })

    act(() => {
      // Seed the session bucket directly so the message lands in the right session.
      useSessionStore.setState({ activeSessionId: 'sess_cancel' })
      const cancelMsg = {
        id: 'asst_cancel',
        session_id: 'sess_cancel',
        role: 'assistant' as const,
        content: 'Here is the analysis of...',
        timestamp: new Date().toISOString(),
        status: 'streaming' as const,
        isStreaming: true,
      }
      useChatStore.setState({
        sessionsById: {
          'sess_cancel': {
            ...makeBucketMessages([cancelMsg]),
            toolCalls: {},
            toolCallOrder: [],
            textAtToolCallStart: {},
            isStreaming: true,
            isReplaying: false,
            replayCompletedForSession: null,
            sessionTokens: 0,
            sessionCost: 0,
            rateLimitEvent: null,
            cancelStage: null,
            lastUserMessageAt: null,
            lastReceivedEventTime: null,
            spanByParentCallId: {},
          },
        },
        isStreaming: true,
        messages: [cancelMsg],
      })
      useConnectionStore.setState({
        connection: {
          send: mockSend,
          disconnect: vi.fn(),
          connect: vi.fn(),
          isConnected: true,
        } as unknown as WsConnection,
        isConnected: true,
      })
      useChatStore.getState().cancelStream()
    })

    // Cancel frame sent
    expect(mockSend).toHaveBeenCalledWith({ type: 'cancel', session_id: 'sess_cancel' })

    // Partial response preserved with interrupted status
    await waitFor(() => {
      const msg = useChatStore.getState().messages.find((m) => m.id === 'asst_cancel')
      expect(msg?.status).toBe('interrupted')
      expect(msg?.content).toBe('Here is the analysis of...')
    })
  })
})
