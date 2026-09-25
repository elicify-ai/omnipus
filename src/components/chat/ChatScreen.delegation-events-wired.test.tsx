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

vi.mock('@assistant-ui/react', () => {
  return {
    useThreadViewportStore: () => ({ getState: () => ({ isAtBottom: true }) }),
    ThreadPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
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
      Messages: () => null,
    },
    MessagePrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Parts: () => null,
    },
    ComposerPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Input: (props: Record<string, unknown>) =>
        React.createElement('textarea', { ...props, 'data-testid': 'composer-input' }),
      Send: ({ children, className, 'data-testid': testId }: { children?: React.ReactNode; className?: string; 'data-testid'?: string }) =>
        React.createElement('button', { type: 'button', className, 'data-testid': testId ?? 'chat-send' }, children),
      AddAttachment: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
        React.createElement('button', { type: 'button', className, 'data-testid': 'add-attachment' }, children),
      Attachments: () => null,
    },
    AttachmentPrimitive: {
      Root: ({ children }: { children?: React.ReactNode }) => React.createElement('div', {}, children),
      Name: () => null,
      Remove: ({ children }: { children?: React.ReactNode }) => React.createElement('button', { type: 'button' }, children),
      Thumb: () => null,
    },
    MessagePartPrimitive: { InProgress: () => null },
    ActionBarPrimitive: {
      Root: ({ children }: { children: React.ReactNode }) => React.createElement('div', {}, children),
      Copy: ({ children }: { children: React.ReactNode }) => React.createElement('span', {}, children),
    },
    AuiIf: () => null,
    useComposerRuntime: () => ({
      getState: () => ({ text: '' }),
      setText: vi.fn(),
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
    }),
    useMessage: () => ({ id: 'msg_streaming', role: 'assistant', status: { type: 'running' }, content: [] }),
    useAttachment: vi.fn(() => ({ id: 'att', name: 'file.txt', contentType: 'text/plain', status: { type: 'complete' }, content: [] })),
    makeAssistantToolUI: () => () => null,
  }
})

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
})
