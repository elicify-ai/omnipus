/**
 * ChatScreen.goal-outcome.test.tsx — the goal outcome line in the rendered
 * thread (founder decision 2026-09-14).
 *
 * BDD:
 *   Given a reloaded thread whose history holds a goal outcome entry
 *   When  the chat renders with Verbose chat OFF
 *   Then  the outcome line is visible, in place, with its own wording
 *
 *   Given a live `goal_outcome` push, then the replay of the same ending
 *   Then  exactly one outcome line renders
 *
 *   Given an ordinary system banner (no outcome)
 *   Then  no outcome line renders
 *
 * Harness mirrors ChatScreen.browser-handover-notice.test.tsx: jsdom has no
 * ResizeObserver, so ChatScreen uses the PlainMessageList path and every
 * system message renders through VirtualSystemMessageRow.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import type { GoalOutcomeFrame } from '@/lib/api/generated/asyncapi-types'

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

const SID = 'test-session-goal-outcome'
const GOAL_TEXT = 'write e8-impossible.txt at the workspace root'
const UNMET_REASON = 'The file is 5 bytes; the criterion also requires exactly 500 bytes.'

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

function userMessage(id: string, content: string): ChatMessage {
  return { id, role: 'user', content, timestamp: '2026-09-14T06:20:00Z', status: 'done' }
}

/** A system message exactly as a cold REST load produces it (rawToMessage). */
function coldLoadedOutcome(id: string, outcome: NonNullable<ChatMessage['goalOutcome']>): ChatMessage {
  return {
    id,
    role: 'system',
    status: 'done',
    content: 'persisted entry content',
    timestamp: outcome.ended_at,
    goalOutcome: outcome,
  }
}

async function renderScreen(): Promise<HTMLElement> {
  let container!: HTMLElement
  await act(async () => {
    container = render(<ChatScreen />).container
  })
  return container
}

describe('ChatScreen — goal outcome line (Verbose chat OFF)', () => {
  beforeEach(() => {
    useConnectionStore.setState({
      isConnected: true,
      liteMode: false,
      reconnectPhase: null,
      reconnectAttempt: 0,
      connectionError: null,
      connection: null,
    })
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
    vi.unstubAllGlobals()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(globalThis as any).ResizeObserver = undefined
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders a reloaded "not met after N tries" outcome from history, in place, with the Judge reason', async () => {
    seedBucket([
      userMessage('u1', `/goal ${GOAL_TEXT}`),
      coldLoadedOutcome('goal-outcome-goal_R-1', {
        goal_id: 'goal_R',
        goal_text: GOAL_TEXT,
        ending: 'rounds_exhausted',
        rounds_used: 5,
        max_rounds: 5,
        judge_reason: UNMET_REASON,
        ended_at: '2026-09-14T06:29:31Z',
      }),
      userMessage('u2', 'ok, what now?'),
    ])
    expect(useChatPreferencesStore.getState().verboseChatEnabled).toBe(false)

    const container = await renderScreen()

    const rows = container.querySelectorAll('[data-testid="goal-outcome-line"]')
    expect(rows).toHaveLength(1)
    const row = rows[0]
    expect(row.querySelector('[data-testid="goal-outcome-headline"]')!.textContent).toBe(
      `Goal not met after 5 tries — ${GOAL_TEXT}`,
    )
    expect(row.querySelector('[data-testid="goal-outcome-summary"]')!.textContent).toBe(`Judge: ${UNMET_REASON}`)

    // In place: between the goal command and the later message.
    const wrapper = row.closest('[data-message-id]')!
    const ids = Array.from(container.querySelectorAll('[data-message-id]')).map((el) => el.getAttribute('data-message-id'))
    expect(wrapper.getAttribute('data-message-id')).toBe('goal-outcome-goal_R-1')
    expect(ids.indexOf('u1')).toBeLessThan(ids.indexOf('goal-outcome-goal_R-1'))
    expect(ids.indexOf('goal-outcome-goal_R-1')).toBeLessThan(ids.indexOf('u2'))
  })

  it('renders met and stopped-by-you outcomes with their own wording', async () => {
    seedBucket([
      coldLoadedOutcome('goal-outcome-goal_M-1', {
        goal_id: 'goal_M',
        goal_text: 'write pill-test.txt',
        ending: 'met',
        rounds_used: 1,
        max_rounds: 20,
        criteria_total: 4,
        ended_at: '2026-09-14T05:30:34Z',
      }),
      coldLoadedOutcome('goal-outcome-goal_S-1', {
        goal_id: 'goal_S',
        goal_text: 'ship the release notes',
        ending: 'stopped_by_user',
        rounds_used: 2,
        max_rounds: 20,
        ended_at: '2026-09-14T05:40:00Z',
      }),
    ])

    const container = await renderScreen()

    const headlines = Array.from(container.querySelectorAll('[data-testid="goal-outcome-headline"]')).map((el) => el.textContent)
    expect(headlines).toEqual(['Goal met — write pill-test.txt', 'Goal stopped by you — ship the release notes'])
    const tones = Array.from(container.querySelectorAll('[data-testid="goal-outcome-line"]')).map((el) =>
      el.getAttribute('data-goal-tone'),
    )
    expect(tones).toEqual(['met', 'stopped'])
  })

  it('renders exactly one line when the live push and the replay of the same ending both arrive', async () => {
    seedBucket([userMessage('u1', `/goal ${GOAL_TEXT}`)])
    const frame: GoalOutcomeFrame = {
      type: 'goal_outcome',
      session_id: SID,
      message_id: 'goal-outcome-goal_L-1',
      outcome: {
        goal_id: 'goal_L',
        goal_text: GOAL_TEXT,
        ending: 'rounds_exhausted',
        rounds_used: 5,
        max_rounds: 5,
        judge_reason: UNMET_REASON,
        ended_at: '2026-09-14T06:29:31Z',
      },
    }

    act(() => {
      useChatStore.getState().handleFrame(frame) // live
      useChatStore.getState().handleFrame(frame) // replay after reconnect
    })
    const container = await renderScreen()

    const rows = container.querySelectorAll('[data-testid="goal-outcome-line"]')
    expect(rows).toHaveLength(1)
    expect(rows[0].querySelector('[data-testid="goal-outcome-headline"]')!.textContent).toBe(
      `Goal not met after 5 tries — ${GOAL_TEXT}`,
    )
  })

  it('does not render an outcome line for an ordinary system banner', async () => {
    seedBucket([
      { id: 'sys-banner-1', role: 'system', status: 'done', content: 'Started a new chat.', timestamp: '2026-09-14T06:00:00Z' },
    ])

    const container = await renderScreen()

    expect(container.querySelector('[data-testid="goal-outcome-line"]')).toBeNull()
    expect(container.textContent).toContain('Started a new chat.')
  })
})
