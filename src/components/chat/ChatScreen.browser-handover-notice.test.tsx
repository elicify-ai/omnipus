/**
 * ChatScreen.browser-handover-notice.test.tsx — ADR-085 BROWSER-FR-042
 * (render half), wave B8, C-90.
 *
 * `VirtualSystemMessageRow` (the historical/PlainMessageList render path —
 * jsdom has no ResizeObserver, so ChatScreen always uses this path in
 * tests, matching every other ChatScreen.*.test.tsx in this directory that
 * fully mocks '@assistant-ui/react') must render a `ChatMessage` carrying
 * `browserHandoverNoticeId` with `data-testid="browser-handover-notice"` —
 * the discriminator the e2e spec (tests/e2e/browser-control-handover.
 * spec.ts) names as its positive observable — and with the notice's own
 * text as its visible content. This mirrors the shipped `isGoalAck` /
 * `data-testid="goal-ack-line"` two-site pattern exactly (C-90): both
 * `SystemMessage()` (the live AssistantUI render path — unreachable for
 * this synthetic, never-streaming message the same way it is for the
 * goal-ack line, since neither sets `isStreaming`) and
 * `VirtualSystemMessageRow` (the reachable, historical path, exercised
 * here) carry the identical `isBrowserHandoverNotice` discriminator.
 *
 * Also asserts the two discriminators do not cross-contaminate: a
 * goal-ack message and a browser-handover notice present in the SAME
 * thread each get their OWN, distinct testid, and an ordinary system
 * banner (neither field set) gets neither.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'

vi.mock('@assistant-ui/react', () => {
  return {
    useThreadViewportStore: () => ({ getState: () => ({ isAtBottom: true }) }),
    ThreadPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Viewport: React.forwardRef(
        (
          { children, className, style, 'data-testid': testId }: {
            children?: React.ReactNode; className?: string; style?: React.CSSProperties; 'data-testid'?: string
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
      Input: ({ disabled, placeholder, className, onChange, onKeyDown, onBlur }: {
        disabled?: boolean; placeholder?: string; className?: string;
        onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
        onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
        onBlur?: () => void;
      }) =>
        React.createElement('textarea', {
          disabled, placeholder, className, onChange, onKeyDown, onBlur,
          'data-testid': 'composer-input',
        }),
      Send: ({ disabled, children, className, 'data-testid': testId }: {
        disabled?: boolean; children?: React.ReactNode; className?: string; 'data-testid'?: string
      }) =>
        React.createElement('button', { type: 'button', disabled, className, 'data-testid': testId ?? 'chat-send' }, children),
      AddAttachment: ({ disabled, children, className }: { disabled?: boolean; children?: React.ReactNode; className?: string }) =>
        React.createElement('button', { type: 'button', disabled, className, 'data-testid': 'add-attachment' }, children),
      Attachments: () => null,
    },
    AttachmentPrimitive: {
      Root: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Name: () => null,
      Remove: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
        React.createElement('button', { type: 'button', className }, children),
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
    useMessage: () => ({
      id: 'msg_streaming',
      role: 'assistant',
      status: { type: 'running' },
      content: [],
    }),
    useAttachment: vi.fn(() => ({
      id: 'att-default',
      name: 'file.txt',
      contentType: 'text/plain',
      file: undefined,
      status: { type: 'complete' },
      content: [],
    })),
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
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

// importOriginal: useSlashMenu calls useChatAgents unconditionally (the "@"
// mention menu), which needs real fetchWorkspaces/workspacesQueryKeys/isWorker
// even though this file doesn't exercise mentions — see
// ChatScreen.plain-list-parity.test.tsx's identical note.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionMessages: vi.fn().mockResolvedValue([]),
    fetchAboutInfo: vi.fn().mockResolvedValue({}),
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
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('./markdown-text', () => ({
  MarkdownText: () => React.createElement('div', {}),
}))
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

const SID = 'test-session-browser-handover-notice'
const NOTICE_TEXT = "A person has taken control of the browser. Send a message to get it back."

function seedBucket(messages: ChatMessage[]): void {
  const bucket = makeBucketMessages(messages)
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
        spanByParentCallId: {},
        trimmedCount: 0,
      },
    },
    messages,
    isStreaming: false,
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
}

describe('VirtualSystemMessageRow — browser-handover notice (ADR-085 BROWSER-FR-042)', () => {
  beforeEach(() => {
    useConnectionStore.setState({
      isConnected: true,
      liteMode: false,
      reconnectPhase: null,
      reconnectAttempt: 0,
      connectionError: null,
      connection: null,
    })
    // Force the PlainMessageList fallback (no ResizeObserver in jsdom) so
    // every message — including the last — renders through
    // VirtualSystemMessageRow, mirroring ChatScreen.plain-list-parity.test.tsx.
    vi.unstubAllGlobals()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(globalThis as any).ResizeObserver = undefined
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders the notice with the discriminator testid and the frame text', async () => {
    const noticeMsg: ChatMessage = {
      id: 'browser-handover-01J3ZQK8N2H8VXNRP5T7C9M4WE',
      role: 'system',
      status: 'done',
      content: NOTICE_TEXT,
      timestamp: new Date().toISOString(),
      browserHandoverNoticeId: 'browser-handover-01J3ZQK8N2H8VXNRP5T7C9M4WE',
    }
    seedBucket([noticeMsg])

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const notice = container.querySelector('[data-testid="browser-handover-notice"]')
    expect(notice).toBeTruthy()
    expect(notice!.textContent).toBe(NOTICE_TEXT)
  })

  it('does not stamp the discriminator on an ordinary system banner (help text, /new, etc.)', async () => {
    const banner: ChatMessage = {
      id: 'sys-banner-1',
      role: 'system',
      status: 'done',
      content: 'Started a new chat.',
      timestamp: new Date().toISOString(),
    }
    seedBucket([banner])

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    expect(container.querySelector('[data-testid="browser-handover-notice"]')).toBeNull()
    expect(container.querySelector('[data-testid="goal-ack-line"]')).toBeNull()
    expect(container.textContent).toContain('Started a new chat.')
  })

  it('gives a goal-ack line and a browser-handover notice their own, distinct testids in the same thread', async () => {
    const goalAck: ChatMessage = {
      id: 'goal-ack-goal_1',
      role: 'system',
      status: 'done',
      content: 'Goal set. Working out what done looks like.',
      timestamp: new Date().toISOString(),
      goalAckGoalId: 'goal_1',
    }
    const handoverNotice: ChatMessage = {
      id: 'browser-handover-abc',
      role: 'system',
      status: 'done',
      content: NOTICE_TEXT,
      timestamp: new Date(Date.now() + 1000).toISOString(),
      browserHandoverNoticeId: 'browser-handover-abc',
    }
    seedBucket([goalAck, handoverNotice])

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const goalAckEl = container.querySelector('[data-testid="goal-ack-line"]')
    const noticeEl = container.querySelector('[data-testid="browser-handover-notice"]')
    expect(goalAckEl).toBeTruthy()
    expect(noticeEl).toBeTruthy()
    expect(goalAckEl).not.toBe(noticeEl)
    expect(goalAckEl!.textContent).toBe('Goal set. Working out what done looks like.')
    expect(noticeEl!.textContent).toBe(NOTICE_TEXT)
  })
})
