/**
 * Lanes 2 and 3 are actually connected.
 *
 * Renders ChatScreen with store records only. Does not mock
 * useChatDelegationEvents or useDelegationEvents. A line appears only if
 * the screen calls the real derivation.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import * as React from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage, SubagentSpan } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useChatPreferencesStore } from '@/store/chatPreferences'

import { MockButton, MockTextarea } from '@/test/assistantUiMock'
vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock({
  ThreadPrimitive: {
    Viewport: React.forwardRef(
              (
                { children, className, style, 'data-testid': testId }: {
                  children?: React.ReactNode
                  className?: string
                  style?: React.CSSProperties
                  'data-testid'?: string
                },
                ref: React.Ref<HTMLDivElement>,
              ) => React.createElement('div', { ref, className, style, 'data-testid': testId }, children),
            ),
  },
  ComposerPrimitive: {
    Input: (props: Record<string, unknown>) =>
              React.createElement(MockTextarea, { ...props, 'data-testid': 'composer-input'}),
    Send: ({ children, className, 'data-testid': testId }: { children?: React.ReactNode; className?: string; 'data-testid'?: string }) =>
              React.createElement(MockButton, { type: 'button', className, 'data-testid': testId ?? 'chat-send', children: children}),
    AddAttachment: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
              React.createElement(MockButton, { type: 'button', className, 'data-testid': 'add-attachment', children: children}),
  },
  AttachmentPrimitive: {
    Root: ({ children }: { children?: React.ReactNode }) => React.createElement('div', {}, children),
    Remove: ({ children }: { children?: React.ReactNode }) => React.createElement(MockButton, { type: 'button', children: children}),
  },
  useMessage: () => ({ id: 'msg_streaming', role: 'assistant', status: { type: 'running' }, content: [] }),
  useAttachment: vi.fn(() => ({ id: 'att', name: 'file.txt', contentType: 'text/plain', status: { type: 'complete' }, content: [] })),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([{ id: 'ray', name: 'Ray', type: 'Subagent', locked: false, status: 'active' }]),
    fetchSessionMessages: vi.fn().mockResolvedValue([]),
    fetchAboutInfo: vi.fn().mockResolvedValue({ preview_port: 5001 }),
    createSession: vi.fn(),
    uploadFiles: vi.fn(),
    fetchProviders: vi.fn().mockResolvedValue([]),
    isApiError: vi.fn().mockReturnValue(false),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  useNavigate: () => vi.fn(),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('./historical-markdown', () => ({
  HistoricalMessageMarkdown: ({ content }: { content: string }) =>
    React.createElement('div', { 'data-testid': 'historical-markdown' }, content),
}))
vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./tools/BrowserTool', () => ({ isReplayBrowserToolName: () => false, BrowserToolReplayBlock: () => null }))
vi.mock('./tools/WebServeUI', () => ({ WebServeBlock: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => React.createElement('div', {}) }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {},
  getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))

import { ChatScreen } from './ChatScreen'

const SID = 'test-session-delegation-wired'

function wiredMessage(): ChatMessage {
  const span: SubagentSpan = {
    spanId: 'span-wire',
    parentCallId: 'run-wire',
    taskLabel: 'Wire the gate',
    agentId: 'ray',
    childSessionId: 'child-wire',
    status: 'success',
    durationMs: 12,
    lifecycleState: 'completed',
  }
  return {
    id: 'm-wire',
    role: 'assistant',
    content: 'Parent reply',
    timestamp: '2026-09-25T12:00:00.000Z',
    status: 'done',
    tool_calls: [
      {
        id: 'run-wire',
        tool: 'delegate',
        status: 'success',
        params: { action: 'run', agent_id: 'ray', label: 'Wire the gate' },
        result: JSON.stringify({
          session_id: 'child-wire',
          generation: 1,
          is_3p: false,
          state: 'running',
          queue_position: 0,
        }),
      },
    ],
    spans: [span],
  }
}

function seed(message: ChatMessage) {
  const bucket = makeBucketMessages([message])
  act(() => {
    useChatStore.setState((s) => ({
      ...s,
      sessionsById: {
        [SID]: {
          ...((s.sessionsById ?? {})[SID] ?? {}),
          ...bucket,
          isStreaming: false,
          isReplaying: false,
          replayCompletedForSession: SID,
          toolCalls: {},
          toolCallOrder: [],
          textAtToolCallStart: {},
          sessionTokens: 0,
          sessionCost: 0,
          rateLimitEvent: null,
          lastUserMessageAt: null,
          cancelStage: null,
          lastReceivedEventTime: null,
          trimmedCount: 0,
        },
      },
      messages: [message],
      isStreaming: false,
      isReplaying: false,
      replayCompletedForSession: SID,
    }))
  })
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
  ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = undefined
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'ray' })
  act(() => {
    useChatStore.getState().resetSession()
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ChatScreen wired to useDelegationEvents', () => {
  it('renders a line derived from the stored span, with the hook left unmocked', async () => {
    seed(wiredMessage())
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    await act(async () => {
      render(
        <QueryClientProvider client={client}>
          <ChatScreen />
        </QueryClientProvider>,
      )
    })

    expect(screen.getByText('Parent reply')).toBeInTheDocument()
    const line = await screen.findByText('Ray finished · Wire the gate')
    expect(line.closest('[data-testid="delegation-event-line"]')).toHaveAttribute('data-event-id', 'finished:span-wire')
  })

  // Spec AC-7 / D1: the finish line is the fact, never the child's words.
  // The sentinel is the child's own text, planted in the three places a
  // thread could leak it. The store check is the non-vacuity gate: if the
  // sentinel were not actually stored, the absence assertion would pass
  // for the wrong reason.
  it('keeps child-authored text out of the thread (spec AC-7)', async () => {
    const sentinel = 'child-authored-zz9f3c2a'
    seed({
      id: 'm-secret',
      role: 'assistant',
      content: 'Parent reply only',
      timestamp: '2026-09-25T12:00:00.000Z',
      status: 'done',
      tool_calls: [
        {
          id: 'run-secret',
          tool: 'delegate',
          status: 'success',
          params: { action: 'run', agent_id: 'ray', label: 'Quiet task' },
          result: JSON.stringify({
            session_id: 'child-secret',
            generation: 1,
            is_3p: false,
            state: 'running',
            queue_position: 0,
          }),
        },
        {
          id: 'poll-secret',
          tool: 'delegate',
          status: 'success',
          params: { action: 'status', session_id: 'child-secret' },
          result: `status snapshot ${sentinel}`,
        },
      ],
      spans: [
        {
          spanId: 'span-secret',
          parentCallId: 'run-secret',
          taskLabel: 'Quiet task',
          agentId: 'ray',
          childSessionId: 'child-secret',
          status: 'success',
          durationMs: 12,
          lifecycleState: 'completed',
          finalResult: `final ${sentinel}`,
          statusLine: `line ${sentinel}`,
        },
      ],
    })

    const stored = useChatStore.getState().messages.find((message) => message.id === 'm-secret')
    const storedSpan = stored?.spans?.[0]
    if (!storedSpan || storedSpan.status === 'running') {
      throw new Error('the finished span must be in the store before the thread is checked')
    }
    expect(storedSpan.finalResult).toContain(sentinel)
    expect(storedSpan.statusLine).toContain(sentinel)
    const poll = stored?.tool_calls?.find((call) => call.id === 'poll-secret')
    expect(String(poll?.result)).toContain(sentinel)

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    await act(async () => {
      render(
        <QueryClientProvider client={client}>
          <ChatScreen />
        </QueryClientProvider>,
      )
    })

    expect(await screen.findByText('Ray finished · Quiet task')).toBeInTheDocument()
    expect(document.body.textContent ?? '').not.toContain(sentinel)
  })
})

describe('ChatScreen wired finish line under a status poll storm (spec AC-6)', () => {
  function push(frame: Parameters<ReturnType<typeof useChatStore.getState>['handleFrame']>[0]) {
    act(() => {
      useChatStore.getState().handleFrame(frame)
    })
  }

  it('shows exactly one finish line when ten-plus status results surround the transition', async () => {
    push({ type: 'token', content: 'Working. ', session_id: SID })
    push({
      type: 'tool_call_start',
      call_id: 'run-1',
      tool: 'delegate',
      params: { action: 'run', agent_id: 'ray', label: 'Count once' },
      session_id: SID,
    })
    push({
      type: 'tool_call_result',
      call_id: 'run-1',
      tool: 'delegate',
      result: { session_id: 'child-1', generation: 1, state: 'running' },
      status: 'success',
      session_id: SID,
    })
    push({
      type: 'subagent_start',
      session_id: SID,
      span_id: 'span-1',
      parent_call_id: 'run-1',
      task_label: 'Count once',
      agent_id: 'ray',
      child_session_id: 'child-1',
    })
    push({
      type: 'subagent_state',
      session_id: SID,
      span_id: 'span-1',
      child_session_id: 'child-1',
      state: 'running',
      created_at: '2026-09-25T12:00:00.000Z',
    })

    const poll = (id: string) => {
      push({
        type: 'tool_call_start',
        call_id: id,
        tool: 'delegate',
        params: { action: 'status', session_id: 'child-1' },
        session_id: SID,
      })
      push({
        type: 'tool_call_result',
        call_id: id,
        tool: 'delegate',
        result: { state: 'running', note: `look ${id}` },
        status: 'success',
        session_id: SID,
      })
    }

    for (let i = 0; i < 4; i++) poll(`poll-before-${i}`)
    push({
      type: 'subagent_state',
      session_id: SID,
      span_id: 'span-1',
      child_session_id: 'child-1',
      state: 'completed',
      created_at: '2026-09-25T12:00:01.000Z',
    })
    for (let i = 0; i < 4; i++) poll(`poll-between-${i}`)
    push({
      type: 'subagent_end',
      session_id: SID,
      span_id: 'span-1',
      status: 'success',
      duration_ms: 10,
      final_result: 'the child wrote a novel',
      agent_id: 'ray',
      parent_call_id: 'run-1',
    })
    for (let i = 0; i < 4; i++) poll(`poll-after-${i}`)
    push({ type: 'done', session_id: SID })

    const state = useChatStore.getState()
    const calls = [
      ...state.messages.flatMap((message) => message.tool_calls ?? []),
      ...Object.values(state.toolCalls),
    ]
    const statusCalls = calls.filter((call) => call.tool === 'delegate' && call.params?.action === 'status')
    expect(statusCalls).toHaveLength(12)
    const span = state.messages.flatMap((message) => message.spans ?? []).find((item) => item.spanId === 'span-1')
    expect(span?.status).toBe('success')

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    let container!: HTMLElement
    await act(async () => {
      const result = render(
        <QueryClientProvider client={client}>
          <ChatScreen />
        </QueryClientProvider>,
      )
      container = result.container
    })

    // The display name arrives with the agents list. Waiting for it avoids
    // reading the id fallback ("ray") from the first paint.
    const line = await screen.findByText('Ray finished · Count once')
    expect(line.closest('[data-event-kind="finished"]')).not.toBeNull()
    expect(container.querySelectorAll('[data-event-kind="finished"]')).toHaveLength(1)
  })
})

function finishedDelegationMessage(opts: {
  id: string
  content: string
  timestamp: string
  streaming: boolean
  spanId: string
  callId: string
  childId: string
  title: string
}): ChatMessage {
  return {
    id: opts.id,
    role: 'assistant',
    content: opts.content,
    timestamp: opts.timestamp,
    status: opts.streaming ? 'streaming' : 'done',
    isStreaming: opts.streaming ? true : undefined,
    tool_calls: [
      {
        id: opts.callId,
        tool: 'delegate',
        status: 'success',
        params: { action: 'run', agent_id: 'ray', label: opts.title },
        result: JSON.stringify({ session_id: opts.childId, generation: 1, state: 'running' }),
      },
    ],
    spans: [
      {
        spanId: opts.spanId,
        parentCallId: opts.callId,
        taskLabel: opts.title,
        agentId: 'ray',
        childSessionId: opts.childId,
        status: 'success',
        durationMs: 12,
        lifecycleState: 'completed',
      },
    ],
  }
}

describe('ChatScreen virtualized list still shows each delegation line once', () => {
  const height = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
  const width = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth')

  function restoreSize() {
    if (height) Object.defineProperty(HTMLElement.prototype, 'offsetHeight', height)
    if (width) Object.defineProperty(HTMLElement.prototype, 'offsetWidth', width)
  }

  afterEach(() => {
    restoreSize()
  })

  it('renders a historical line inside a virtual row, and a streaming line exactly once', async () => {
    // The list measures offsetHeight, not clientHeight. A zero height draws no rows.
    Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get: () => 800 })
    Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, get: () => 800 })
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
      },
    )

    const messages = [
      finishedDelegationMessage({
        id: 'm-hist',
        content: 'Already done',
        timestamp: '2026-09-25T12:00:00.000Z',
        streaming: false,
        spanId: 'span-hist',
        callId: 'run-hist',
        childId: 'child-hist',
        title: 'Kept row',
      }),
      finishedDelegationMessage({
        id: 'm-live',
        content: 'Still going',
        timestamp: '2026-09-25T12:01:00.000Z',
        streaming: true,
        spanId: 'span-live',
        callId: 'run-live',
        childId: 'child-live',
        title: 'Live row',
      }),
    ]
    const bucket = makeBucketMessages(messages)
    act(() => {
      useChatStore.setState((s) => ({
        ...s,
        sessionsById: {
          [SID]: {
            ...((s.sessionsById ?? {})[SID] ?? {}),
            ...bucket,
            isStreaming: true,
            isReplaying: false,
            replayCompletedForSession: SID,
            toolCalls: {},
            toolCallOrder: [],
            textAtToolCallStart: {},
            sessionTokens: 0,
            sessionCost: 0,
            rateLimitEvent: null,
            lastUserMessageAt: null,
            cancelStage: null,
            lastReceivedEventTime: null,
            trimmedCount: 0,
          },
        },
        messages,
        isStreaming: true,
        isReplaying: false,
        replayCompletedForSession: SID,
      }))
    })

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    let container!: HTMLElement
    await act(async () => {
      const result = render(
        <QueryClientProvider client={client}>
          <ChatScreen />
        </QueryClientProvider>,
      )
      container = result.container
    })

    await screen.findByText('Ray finished · Kept row')
    await screen.findByText('Ray finished · Live row')

    const virtualRow = container.querySelector('[data-index]')
    expect(virtualRow).not.toBeNull()
    const historicalLine = container.querySelector('[data-event-id="finished:span-hist"]')
    expect(historicalLine).not.toBeNull()
    expect(historicalLine?.closest('[data-index]')).not.toBeNull()

    const liveLines = container.querySelectorAll('[data-event-id="finished:span-live"]')
    expect(liveLines).toHaveLength(1)
    expect(liveLines[0]?.closest('[data-index]')).toBeNull()
  })
})
