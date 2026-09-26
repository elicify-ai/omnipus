/**
 * ChatScreen.judge-verdict-thread-card.test.tsx — the Judge verdict card in
 * the rendered thread (ADR-049 D2/D4/US-13/SD-C10).
 *
 * BDD:
 *   Given a reloaded thread (REST cold-load) whose history holds a
 *     judge_verdict entry
 *   When  the chat renders with Verbose chat ON
 *   Then  exactly one JudgeVerdictThreadCard renders, in place
 *
 *   Given the same reloaded thread
 *   When  the chat renders with Verbose chat OFF
 *   Then  no card renders (panel-only by default, SD-C10)
 *
 *   Given an ordinary system banner (no verdict)
 *   Then  no card renders
 *
 *   Given a LIVE `judge_verdict` WS frame carrying `session_id` (scope=task
 *     or scope=goal, live-thread-card fix, 2026-09-14)
 *   When  handleFrame processes it with Verbose chat ON
 *   Then  exactly one JudgeVerdictThreadCard renders, with no reload needed
 *
 * Harness mirrors ChatScreen.goal-outcome.test.tsx: jsdom has no
 * ResizeObserver, so ChatScreen uses the PlainMessageList path and every
 * system message renders through VirtualSystemMessageRow or, for a
 * judge_verdict entry, JudgeVerdictThreadCard.
 *
 * Scope note: the original version of this file covered only the REST
 * cold-load carrier, because the live/replay `JudgeVerdictFrame` WS push
 * used to carry NO `session_id` at all (a deliberately GLOBAL frame, fed
 * only into `useJudgeActivityStore`). The frame now OPTIONALLY carries
 * `session_id` for scope=task/scope=goal
 * (contracts/components/schemas/JudgeVerdictFrame.yaml) — this file now
 * also covers the live path; `chat.judge-verdict-frame.test.ts` covers the
 * store-level frame→card/panel routing and live-vs-replay de-dup in detail.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
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

const SID = 'test-session-judge-verdict'
const GOAL_TEXT = 'write e8-impossible.txt at the workspace root'

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

function userMessage(id: string, content: string): ChatMessage {
  return { id, role: 'user', content, timestamp: '2026-09-14T06:20:00Z', status: 'done' }
}

/** A judge_verdict system entry exactly as a cold REST load produces it (rawToMessage). */
function coldLoadedVerdict(id: string, verdict: NonNullable<ChatMessage['verdict']>): ChatMessage {
  return {
    id,
    role: 'system',
    status: 'done',
    content: JSON.stringify(verdict),
    timestamp: verdict.judged_at,
    type: 'judge_verdict',
    verdict,
  }
}

const SAMPLE_VERDICT: NonNullable<ChatMessage['verdict']> = {
  id: 'verdict-goal-1',
  scope: 'goal',
  round: 1,
  met: true,
  per_criterion: [{ criterion_id: 'crit-1', met: true, reason: 'confirmed by evidence' }],
  model: 'z-ai/glm-5.3',
  judged_at: '2026-09-14T06:29:31Z',
  judge_agent_id: 'judge',
}

async function renderScreen(): Promise<HTMLElement> {
  let container!: HTMLElement
  await act(async () => {
    container = render(<ChatScreen />).container
  })
  return container
}

describe('ChatScreen — judge verdict thread card (ADR-049 D2/D4/SD-C10)', () => {
  beforeEach(() => {
    useConnectionStore.setState({
      isConnected: true,
      liteMode: false,
      reconnectPhase: null,
      reconnectAttempt: 0,
      connectionError: null,
      connection: null,
    })
    vi.unstubAllGlobals()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(globalThis as any).ResizeObserver = undefined
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders exactly one JudgeVerdictThreadCard from a REST cold-loaded entry when Verbose chat is ON', async () => {
    useChatPreferencesStore.setState({ verboseChatEnabled: true })
    seedBucket([
      userMessage('u1', `/goal ${GOAL_TEXT}`),
      coldLoadedVerdict('goal-verdict-1-judge-1', SAMPLE_VERDICT),
      userMessage('u2', 'thanks!'),
    ])

    const container = await renderScreen()

    const cards = container.querySelectorAll('[data-testid="judge-verdict-thread-card"]')
    expect(cards).toHaveLength(1)
    // toolui-analysis item 5 (founder-approved 2026-09-26): collapsed header
    // renders "Judge verdict · <scope> round N · met/unmet" — middot, not the
    // pre-restyle em-dash (JudgeVerdictThreadCard.tsx header comment).
    expect(cards[0].textContent).toContain('Judge verdict · goal round 1')
    expect(cards[0].textContent).toContain('met')

    // In place: between the goal command and the later message.
    const ids = Array.from(container.querySelectorAll('[data-message-id]')).map((el) => el.getAttribute('data-message-id'))
    expect(ids.indexOf('u1')).toBeLessThan(ids.indexOf('u2'))
  })

  it('renders no card when Verbose chat is OFF (panel-only by default, SD-C10)', async () => {
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
    seedBucket([
      userMessage('u1', `/goal ${GOAL_TEXT}`),
      coldLoadedVerdict('goal-verdict-2-judge-1', SAMPLE_VERDICT),
    ])

    const container = await renderScreen()

    expect(container.querySelector('[data-testid="judge-verdict-thread-card"]')).toBeNull()
  })

  it('renders exactly one JudgeVerdictThreadCard from a LIVE session-scoped frame, no reload needed', async () => {
    useChatPreferencesStore.setState({ verboseChatEnabled: true })
    // Reset the bucket to a known state (no leftover judge_verdict entries
    // from an earlier test in this file) before dispatching the live frame
    // — handleFrame PATCHES the bucket (withBucket merges onto whatever is
    // already there), unlike seedBucket's full messagesById/messageOrder
    // overwrite.
    seedBucket([userMessage('u1', `/goal ${GOAL_TEXT}`)])

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'judge_verdict',
        id: 'verdict-live-goal-1',
        scope: 'goal',
        session_id: SID,
        round: 1,
        met: true,
        per_criterion: [{ criterion_id: 'crit-1', met: true, reason: 'confirmed by evidence' }],
        model: 'z-ai/glm-5.3',
        judged_at: '2026-09-14T06:29:31Z',
        judge_agent_id: 'judge',
      })
    })

    const container = await renderScreen()

    const cards = container.querySelectorAll('[data-testid="judge-verdict-thread-card"]')
    expect(cards).toHaveLength(1)
    // toolui-analysis item 5 (founder-approved 2026-09-26): middot separator,
    // not the pre-restyle em-dash (JudgeVerdictThreadCard.tsx header comment).
    expect(cards[0].textContent).toContain('Judge verdict · goal round 1')
    expect(cards[0].textContent).toContain('met')
  })

  it('does not render a card for an ordinary system banner (no verdict)', async () => {
    useChatPreferencesStore.setState({ verboseChatEnabled: true })
    seedBucket([
      { id: 'sys-banner-1', role: 'system', status: 'done', content: 'Started a new chat.', timestamp: '2026-09-14T06:00:00Z' },
    ])

    const container = await renderScreen()

    expect(container.querySelector('[data-testid="judge-verdict-thread-card"]')).toBeNull()
    expect(container.textContent).toContain('Started a new chat.')
  })
})
