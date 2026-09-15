/**
 * ChatScreen.goal-setup-failure-line.test.tsx — operator-reported UX fix,
 * 2026-09-08 (GX-C). Reproduced case: an AskUserQuestion call rejected by
 * argument validation during goal setup left the user staring at nothing
 * but the generic thinking indicator for 17 minutes.
 *
 * Style follows ChatScreen.tool-order.test.tsx exactly: full ChatScreen
 * render, ResizeObserver forced undefined (PlainMessageList fallback, so a
 * finished message renders through VirtualAssistantMessageRow without
 * needing to fake virtualizer geometry), `@assistant-ui/react` mocked down
 * to its stable contract, GenericToolCall mocked to a bare marker
 * (data-testid="tool-call-badge" + data-tool) so assertions can tell "the
 * ordinary path rendered instead" without depending on its internals.
 *
 * BDD:
 *   Given: a goal is active with an empty record, and a tool call FAILED
 *   Then:  a quiet "A clarifying question could not be sent — retrying."
 *          line renders (details/summary), with the raw error on expand
 *   Given: the same failed call, but NO active goal
 *   Then:  general tool-error rendering is unaffected — the ordinary badge renders
 *   Given: the same failed call, active goal but the record is already populated
 *   Then:  general tool-error rendering is unaffected
 *   Given: the same failed call, active goal + empty record, verbose chat ON
 *   Then:  no failure line — verbose chat shows the raw call in full instead
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

vi.mock('@assistant-ui/react', () => {
  return {
    useThreadViewportStore: () => ({ getState: () => ({ isAtBottom: true }) }),
    ThreadPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Viewport: React.forwardRef(
        (
          {
            children,
            className,
            style,
            'data-testid': testId,
          }: { children?: React.ReactNode; className?: string; style?: React.CSSProperties; 'data-testid'?: string },
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
      Input: ({
        disabled,
        placeholder,
        className,
        onChange,
        onKeyDown,
        onBlur,
      }: {
        disabled?: boolean
        placeholder?: string
        className?: string
        onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void
        onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void
        onBlur?: () => void
      }) =>
        React.createElement('textarea', {
          disabled,
          placeholder,
          className,
          onChange,
          onKeyDown,
          onBlur,
          'data-testid': 'composer-input',
        }),
      Send: ({
        disabled,
        children,
        className,
        'data-testid': testId,
      }: {
        disabled?: boolean
        children?: React.ReactNode
        className?: string
        'data-testid'?: string
      }) =>
        React.createElement(
          'button',
          { type: 'button', disabled, className, 'data-testid': testId ?? 'chat-send' },
          children,
        ),
      AddAttachment: ({
        disabled,
        children,
        className,
      }: {
        disabled?: boolean
        children?: React.ReactNode
        className?: string
      }) => React.createElement('button', { type: 'button', disabled, className, 'data-testid': 'add-attachment' }, children),
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
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
// Mocked down to the stable contract — the real component's exact markup
// isn't what this file asserts against, only "did the ORDINARY path render
// instead of the goal-setup failure line".
vi.mock('./tools/GenericToolCall', () => ({
  GenericToolCall: ({ toolName }: { toolName: string }) =>
    React.createElement('div', { 'data-testid': 'tool-call-badge', 'data-tool': toolName }, toolName),
}))
vi.mock('./tools/BrowserTool', () => ({
  isReplayBrowserToolName: () => false,
  BrowserToolReplayBlock: ({ toolName }: { toolName: string }) =>
    React.createElement('div', { 'data-testid': 'tool-call-badge', 'data-tool': toolName }, toolName),
}))
vi.mock('./tools/WebServeUI', () => ({
  WebServeBlock: ({ toolName }: { toolName: string }) =>
    React.createElement('div', { 'data-testid': 'tool-call-badge', 'data-tool': toolName }, toolName),
}))
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

const SID = 'sess_goal_setup_failure_line'

function makeGoalFrame(overrides: Partial<GoalStatusFrame> = {}): GoalStatusFrame {
  return {
    type: 'goal_status',
    session_id: SID,
    goal_id: 'goal_failure_line_test',
    condition: 'ship the release notes',
    round: 0,
    max_rounds: 20,
    latest_reason: '',
    active_loops: 1,
    cap: 16,
    state: 'active',
    ...overrides,
  }
}

/** Seeds a FINISHED (non-streaming) assistant message carrying one FAILED
 * baked tool call, plus an optional goalStatus frame on both the session
 * bucket and the foreground selector. */
function seedFailedToolCallAssistant(goalFrame: GoalStatusFrame | null): void {
  const userMsg: ChatMessage = {
    id: 'u1',
    role: 'user',
    content: '/goal ship the release notes',
    timestamp: new Date().toISOString(),
    status: 'done',
  }
  const assistantMsg: ChatMessage = {
    id: 'a1',
    role: 'assistant',
    content: '',
    timestamp: new Date().toISOString(),
    status: 'done',
    tool_calls: [
      {
        id: 'tc_ask_user_question_1',
        tool: 'ask_user_question',
        params: { questions: [{ text: 'Which environment?' }] },
        result: 'invalid arguments: questions[0].options is required',
        status: 'error',
        error: 'invalid arguments: questions[0].options is required',
      },
    ],
  }
  const messages = [userMsg, assistantMsg]
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
        goalStatus: goalFrame,
      },
    },
    messages,
    isStreaming: false,
    isReplaying: false,
    replayCompletedForSession: SID,
    goalStatus: goalFrame,
  }))
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
  vi.unstubAllGlobals()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ;(globalThis as any).ResizeObserver = undefined

  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  act(() => {
    useChatStore.getState().resetSession()
  })
  useChatPreferencesStore.setState({ verboseChatEnabled: false })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ChatScreen goal-setup failure line (operator UX fix, 2026-09-08)', () => {
  it('renders the quiet failure line, with the error available on expand, while a goal is active and its record is empty', () => {
    seedFailedToolCallAssistant(makeGoalFrame())

    const { container } = render(<ChatScreen />)

    const line = container.querySelector('[data-testid="goal-setup-failure-line"]') as HTMLElement
    expect(line).toBeTruthy()
    expect(line.textContent).toContain('A clarifying question could not be sent — retrying.')
    expect(line.textContent).toContain('invalid arguments: questions[0].options is required')
    expect(container.querySelector('[data-testid="tool-call-badge"]')).toBeNull()
  })

  it('does not change general tool-error rendering when there is no active goal', () => {
    seedFailedToolCallAssistant(null)

    const { container } = render(<ChatScreen />)

    expect(container.querySelector('[data-testid="goal-setup-failure-line"]')).toBeNull()
    const badge = container.querySelector('[data-testid="tool-call-badge"]') as HTMLElement
    expect(badge).toBeTruthy()
    expect(badge.getAttribute('data-tool')).toBe('ask_user_question')
  })

  it('does not change general tool-error rendering once the goal record is populated', () => {
    seedFailedToolCallAssistant(
      makeGoalFrame({
        criteria: [{ id: 'c1', kind: 'prose', text: 'release notes are published', judgment: 'boolean', status: 'pending', author: { kind: 'agent', id: 'tester' } }],
      }),
    )

    const { container } = render(<ChatScreen />)

    expect(container.querySelector('[data-testid="goal-setup-failure-line"]')).toBeNull()
    expect(container.querySelector('[data-testid="tool-call-badge"]')).toBeTruthy()
  })

  it('falls through to the raw call when verbose chat is on, even during goal setup', () => {
    useChatPreferencesStore.setState({ verboseChatEnabled: true })
    seedFailedToolCallAssistant(makeGoalFrame())

    const { container } = render(<ChatScreen />)

    expect(container.querySelector('[data-testid="goal-setup-failure-line"]')).toBeNull()
    expect(container.querySelector('[data-testid="tool-call-badge"]')).toBeTruthy()
  })
})
