/**
 * Event lines in the chat thread.
 * AC-8: [open] on a line in the thread navigates to the child session.
 * AC-9: verbose chat still renders a delegate call as a badge.
 * AC-10 render half: refusal reason, no [open].
 * AC-5 render half: non-verbose, a status call renders no badge and no line.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, within, act, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import type { DelegationEvent } from '@/lib/delegationEvents.types'

const mockNavigate = vi.fn()
const eventBox = vi.hoisted(() => ({ current: [] as DelegationEvent[] }))

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

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: () => ({ data: [], isError: false, refetch: vi.fn() }),
    useMutation: () => ({ mutate: vi.fn(), isPending: false }),
    useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
  }
})

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  useNavigate: () => mockNavigate,
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionMessages: vi.fn().mockResolvedValue([]),
    fetchAboutInfo: vi.fn().mockResolvedValue({ preview_port: 5001 }),
    createSession: vi.fn(),
    uploadFiles: vi.fn(),
    fetchProviders: vi.fn().mockResolvedValue([]),
    isApiError: vi.fn().mockReturnValue(false),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
  }
})

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

vi.mock('./useChatDelegationEvents', () => ({
  useChatDelegationEvents: () => eventBox.current,
}))

import { ChatScreen } from './ChatScreen'

const SID = 'test-session-delegation-lines'

function message(id: string, content: string, tool?: ChatMessage['tool_calls']): ChatMessage {
  return {
    id,
    role: 'assistant',
    content,
    timestamp: '2026-09-25T00:00:00.000Z',
    status: 'done',
    tool_calls: tool,
  }
}

function seed(messages: ChatMessage[]) {
  const bucket = makeBucketMessages(messages)
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
      messages,
      isStreaming: false,
      isReplaying: false,
      replayCompletedForSession: SID,
    }))
  })
}

beforeEach(() => {
  mockNavigate.mockClear()
  eventBox.current = []
  useConnectionStore.setState({
    isConnected: true,
    liteMode: false,
    reconnectPhase: null,
    reconnectAttempt: 0,
    connectionError: null,
    connection: null,
  })
  vi.unstubAllGlobals()
  // PlainMessageList, so a finished message is in the DOM without virtualizer geometry.
  ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = undefined
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  act(() => {
    useChatStore.getState().resetSession()
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

async function renderScreen() {
  await act(async () => {
    render(<ChatScreen />)
  })
}

describe('ChatScreen delegation event lines', () => {
  it('places lines after their anchor message, in time order, and unanchored lines at the end', async () => {
    seed([message('m1', 'First answer'), message('m2', 'Second answer')])
    eventBox.current = [
      { id: 'late', kind: 'finished', sessionId: SID, at: 20, anchorMessageId: 'm1', agentName: 'Mia', title: 'Later', childSessionId: 'c-late' },
      { id: 'early', kind: 'delegated', sessionId: SID, at: 10, anchorMessageId: 'm1', agentName: 'Mia', title: 'Earlier', childSessionId: 'c-early' },
      { id: 'tail', kind: 'bash_launched', sessionId: SID, at: 5, command: 'npm test' },
    ]
    await renderScreen()

    const first = screen.getByText('First answer')
    const early = screen.getByText('Delegated to Mia · Earlier')
    const late = screen.getByText('Mia finished · Later')
    const second = screen.getByText('Second answer')
    const tail = screen.getByText('Running in background · npm test')
    const following = Node.DOCUMENT_POSITION_FOLLOWING
    expect(first.compareDocumentPosition(early) & following).toBeTruthy()
    expect(early.compareDocumentPosition(late) & following).toBeTruthy()
    expect(late.compareDocumentPosition(second) & following).toBeTruthy()
    expect(second.compareDocumentPosition(tail) & following).toBeTruthy()
  })

  it('AC-8: [open] on a thread line navigates to that child session', async () => {
    const user = userEvent.setup()
    seed([message('m1', 'Done')])
    eventBox.current = [
      { id: 'fin', kind: 'finished', sessionId: SID, at: 1, anchorMessageId: 'm1', agentName: 'Ray', title: 'Ship it', childSessionId: 'child-ray' },
    ]
    await renderScreen()
    await user.click(screen.getByRole('button', { name: '[open]' }))
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/sessions/$sessionId',
      params: { sessionId: 'child-ray' },
    })
  })

  it('AC-10: a refusal line in the thread shows its reason and no [open]', async () => {
    seed([message('m1', 'I tried')])
    eventBox.current = [
      { id: 'openable', kind: 'delegated', sessionId: SID, at: 1, anchorMessageId: 'm1', agentName: 'Mia', title: 'Try', childSessionId: 'child-mia' },
      { id: 'nope', kind: 'refused', sessionId: SID, at: 2, reason: 'unknown skill' },
    ]
    await renderScreen()
    // Non-vacuity: this thread can render [open], so the refusal's lack of one is real.
    expect(screen.getAllByRole('button', { name: '[open]' })).toHaveLength(1)
    const refusal = screen.getByText('Delegation refused · unknown skill').closest('[data-testid="delegation-event-line"]')
    expect(refusal).not.toBeNull()
    expect(within(refusal as HTMLElement).queryByRole('button', { name: '[open]' })).toBeNull()
  })

  it('AC-9: verbose chat still renders a delegate run as a badge', async () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    seed([
      message('m1', 'Delegating.', [
        {
          id: 'tc-run',
          tool: 'delegate',
          params: { action: 'run', task: 'do X' },
          status: 'success',
          result: { ok: true },
        },
      ]),
    ])
    await renderScreen()
    expect(document.querySelector('[data-testid="tool-call-badge"][data-tool="delegate"]')).not.toBeNull()
  })

  it('AC-5 render half: non-verbose, a status call renders no badge and no line', async () => {
    const statusCall = {
      id: 'tc-status',
      tool: 'delegate',
      params: { action: 'status' },
      status: 'success' as const,
      result: { ok: true },
    }
    // Non-vacuity for the badge: the same call renders a badge once verbose is on.
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    seed([message('m-verbose', 'Polling.', [statusCall])])
    await renderScreen()
    expect(document.querySelector('[data-testid="tool-call-badge"][data-tool="delegate"]')).not.toBeNull()
    cleanup()

    // Non-vacuity for the line: this screen does render a line when an event exists.
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: false })
    })
    eventBox.current = [
      { id: 'shown', kind: 'delegated', sessionId: SID, at: 1, anchorMessageId: 'm-line', agentName: 'Mia', title: 'Real', childSessionId: 'c1' },
    ]
    seed([message('m-line', 'A real event.')])
    await renderScreen()
    expect(screen.getByTestId('delegation-event-line')).toBeInTheDocument()
    cleanup()

    eventBox.current = []
    seed([message('m-status', 'Just a poll.', [statusCall])])
    await renderScreen()
    expect(document.querySelector('[data-testid="tool-call-badge"][data-tool="delegate"]')).toBeNull()
    expect(screen.queryByTestId('delegation-event-line')).toBeNull()
  })

  it('places the delegated line between the text before the call and the text after it, with finished directly after', async () => {
    const before = "I'll hand this to the worker."
    const after = 'The worker has finished. All done.'
    const call = {
      id: 'call-wire',
      tool: 'delegate',
      params: { action: 'run', label: 'Wire the gate' },
      status: 'success' as const,
      result: { ok: true },
      textOffset: before.length,
    }
    const msg = message('m-inline', before + after, [call] as NonNullable<ChatMessage['tool_calls']>)
    msg.spans = [
      {
        spanId: 'span-wire',
        parentCallId: 'call-wire',
        taskLabel: 'Wire the gate',
        status: 'success',
        durationMs: 10,
        childSessionId: 'child-gp',
      },
    ]
    seed([msg])
    eventBox.current = [
      {
        id: 'finished:span-wire',
        kind: 'finished',
        sessionId: SID,
        at: 2,
        anchorMessageId: 'm-inline',
        agentName: 'General Purpose',
        title: 'Wire the gate',
        childSessionId: 'child-gp',
      },
      {
        id: 'delegated:span-wire',
        kind: 'delegated',
        sessionId: SID,
        at: 1,
        anchorMessageId: 'm-inline',
        agentName: 'General Purpose',
        title: 'Wire the gate',
        childSessionId: 'child-gp',
      },
    ]
    await renderScreen()

    const beforeEl = screen.getByText(before)
    const delegated = screen.getByText('Delegated to General Purpose · Wire the gate')
    const finished = screen.getByText('General Purpose finished · Wire the gate')
    const afterEl = screen.getByText(after)
    const following = Node.DOCUMENT_POSITION_FOLLOWING
    expect(beforeEl.compareDocumentPosition(delegated) & following).toBeTruthy()
    expect(delegated.compareDocumentPosition(finished) & following).toBeTruthy()
    expect(finished.compareDocumentPosition(afterEl) & following).toBeTruthy()
  })
})
