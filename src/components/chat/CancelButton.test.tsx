import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MessageInput } from './MessageInput'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'

// MessageInput uses useQuery (for skills autocomplete). Suppress the network request.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchSkills: vi.fn().mockResolvedValue([]) }
})

function renderInput() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MessageInput />
    </QueryClientProvider>,
  )
}

// test_cancel_button_states (test #36) — MessageInput send/stop button states
// test_cancel_idle_noop (test #39) — Escape when idle is no-op
// Traces to: wave5a-wire-ui-spec.md — Scenario: Cancel during streaming preserves partial response
//             wave5a-wire-ui-spec.md — Scenario: Cancel when idle is a no-op

// Note: CancelButton is not a standalone component — cancel/send behavior lives in MessageInput.
// test_cancel_preserves_partial (#37) is covered in chat.test.ts
// test_cancel_during_tool (#38) is covered in ToolCallBadge.test.tsx

beforeEach(() => {
  act(() => {
    useChatStore.setState({
      isStreaming: false,
      messages: [],
      toolCalls: {},
      pendingApprovals: [],
    })
    useConnectionStore.setState({ connection: null, isConnected: false })
    useSessionStore.setState({ activeSessionId: null, activeAgentId: null })
  })
})

describe('MessageInput — stop button during streaming (test #36)', () => {
  it('renders Stop button while isStreaming', () => {
    // Traces to: wave5a-wire-ui-spec.md — AC1: send button transforms into Stop during streaming
    // FR-21: aria-label is now the current label state ("Stop" initially)
    act(() => {
      useChatStore.setState({ isStreaming: true })
      useConnectionStore.setState({ isConnected: true })
    })
    renderInput()
    // aria-label is "Stop" (the label from stopButtonLabel('stop'))
    expect(screen.getByRole('button', { name: /^stop$/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /send message/i })).toBeNull()
  })

  it('calls cancelStream when Stop button is clicked', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Cancel during streaming (AC1)
    const mockSend = vi.fn()
    act(() => {
      useChatStore.setState({ isStreaming: true })
      useConnectionStore.setState({
        isConnected: true,
        connection: {
          send: mockSend,
          disconnect: vi.fn(),
          connect: vi.fn(),
          isConnected: true,
        } as any,
      })
      useSessionStore.setState({ activeSessionId: 'sess_1' })
    })
    renderInput()
    fireEvent.click(screen.getByRole('button', { name: /^stop$/i }))
    // cancelStream sends cancel frame (or is no-op if we set isStreaming false)
    // It calls connection.send with cancel frame
    expect(useChatStore.getState().isStreaming).toBe(false)
  })
})

describe('MessageInput — send button when idle (test #36)', () => {
  it('renders Send button (aria-label="Send message") when idle', () => {
    // Traces to: wave5a-wire-ui-spec.md — AC4: Stop reverts to Send after cancel/done
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({ isConnected: true })
    })
    renderInput()
    expect(screen.getByRole('button', { name: /send message/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /stop generation/i })).toBeNull()
  })

  it('Send button is disabled when input is empty', () => {
    // Traces to: wave5a-wire-ui-spec.md — Edge case: empty message cannot be submitted
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({ isConnected: true })
    })
    renderInput()
    const sendBtn = screen.getByRole('button', { name: /send message/i })
    expect(sendBtn).toBeDisabled()
  })

  it('Send button is disabled when disconnected', () => {
    // Traces to: wave5a-wire-ui-spec.md — Disconnected: send is blocked
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({ isConnected: false })
    })
    renderInput()
    const sendBtn = screen.getByRole('button', { name: /send message/i })
    expect(sendBtn).toBeDisabled()
  })
})

describe('MessageInput — Escape key no-op when idle (test #39)', () => {
  it('pressing Escape when not streaming does not call cancelStream', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Cancel when idle is a no-op (AC3)
    const mockSend = vi.fn()
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        isConnected: true,
        connection: {
          send: mockSend,
          disconnect: vi.fn(),
          connect: vi.fn(),
          isConnected: true,
        } as any,
      })
    })
    renderInput()
    const textarea = screen.getByRole('textbox')
    fireEvent.keyDown(textarea, { key: 'Escape' })
    // connection.send must NOT be called (no cancel frame sent)
    expect(mockSend).not.toHaveBeenCalled()
  })
})

// B3: stop-button label state machine
// Refs: docs/internal/specs/cancel-cross-channel-spec-review.md B3
describe('MessageInput — stop button label morphing (B3)', () => {
  it('shows "Stopping..." when cancelStage is "graceful"', () => {
    // Traces to: B3 — graceful stage must display "Stopping..." same as optimistic click state.
    act(() => {
      useChatStore.setState({ isStreaming: true, cancelStage: 'graceful' })
      useConnectionStore.setState({ isConnected: true })
    })
    renderInput()
    // FR-21: aria-label is the current label state; in graceful stage it is "Stopping..."
    expect(screen.getByRole('button', { name: /stopping\.\.\./i })).toBeInTheDocument()
    expect(screen.getByText('Stopping...')).toBeInTheDocument()
  })

  it('shows "Force-stopping..." with spinner when cancelStage is "hard"', () => {
    // Traces to: B3 — hard stage must display "Force-stopping..." with spinner.
    act(() => {
      useChatStore.setState({ isStreaming: true, cancelStage: 'hard' })
      useConnectionStore.setState({ isConnected: true })
    })
    renderInput()
    expect(screen.getByText('Force-stopping...')).toBeInTheDocument()
  })

  it('shows "Cancelled" when cancelStage is "detached"', () => {
    // Traces to: B3 — detached stage shown briefly before done frame clears isStreaming.
    act(() => {
      useChatStore.setState({ isStreaming: true, cancelStage: 'detached' })
      useConnectionStore.setState({ isConnected: true })
    })
    renderInput()
    expect(screen.getByText('Cancelled')).toBeInTheDocument()
  })

  it('clicking Stop sets label to "Stopping..." optimistically (before done frame clears isStreaming)', () => {
    // Traces to: B3 — immediate optimistic update on click without waiting for graceful frame.
    // We seed an assistant message so markLastMessageInterrupted (called from cancelStream)
    // updates the message in place WITHOUT flipping isStreaming. The no-message path of
    // markLastMessageInterrupted creates an interrupted placeholder AND sets
    // isStreaming:false — that immediately triggers the useEffect that resets the label
    // to "stop", which would defeat this optimistic-UI assertion.
    act(() => {
      // The bucket needs an assistant message so markLastMessageInterrupted
      // updates it in place (preserving bucket.isStreaming) instead of falling
      // through to the no-message branch that flips isStreaming:false (which
      // would defeat this optimistic-UI assertion).
      const seededMsg = {
        id: 'asst-1',
        role: 'assistant' as const,
        content: 'partial response so far',
        timestamp: new Date().toISOString(),
        status: 'streaming' as const,
        isStreaming: true,
      }
      useSessionStore.setState({ activeSessionId: 'sess_1' })
      useChatStore.setState({
        isStreaming: true,
        cancelStage: null,
        messages: [seededMsg],
        sessionsById: {
          sess_1: {
            ...makeBucketMessages([seededMsg]),
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
            lastUserMessageAt: null,
            lastReceivedEventTime: null,
            spanByParentCallId: {},
          },
        },
      })
      useConnectionStore.setState({
        isConnected: true,
        connection: null,
      })
    })
    renderInput()
    // Before click: no label text shown (just icon); aria-label is "Stop" (FR-21 idle state)
    expect(screen.queryByText('Stopping...')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: /^stop$/i }))
    // After click: optimistic "Stopping..." label appears; isStreaming still true so Stop button stays.
    expect(screen.getByText('Stopping...')).toBeInTheDocument()
  })
})
