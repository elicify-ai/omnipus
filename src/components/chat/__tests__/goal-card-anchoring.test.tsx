/**
 * goal-card-anchoring.test.tsx — T-22 (ADR-082 D9, ui-independent-turns-
 * spec.md S-15/S-16/S-17): the goal record card is anchored at its
 * `set_goal` tool-call's own position, on both the live and replay paths,
 * rendered directly from the call's RESULT (`pkg/tools/set_goal.go`'s
 * success payload — `goal_id`/`definition`/`criteria`/`dod`, ADR-082 D9/
 * FR-016) rather than from the thread-tail `goalPills`-only component this
 * wave deletes (`GoalThreadTailCards`, formerly `src/components/chat/
 * GoalThreadTailCards.tsx`).
 *
 * Covers:
 *   (a) live path — the registered `set_goal` tool UI (SetGoalToolUI)
 *       renders GoalEchoCard from a completed result; a still-running call
 *       (no result yet) renders nothing.
 *   (b) replay path — VirtualAssistantMessageRow (via full ChatScreen
 *       render, PlainMessageList fallback) renders the card interleaved at
 *       the call's own `textOffset` position, same as any other tool part.
 *   (c) GoalThreadTailCards no longer exists on disk.
 *   (d) two `set_goal` calls (register + amend) on one message → two cards,
 *       each with its own record, at their own positions.
 *   (e) live overlay — a matching `goalPills` entry (by `goal_id`) overlays
 *       progress fields (round/max_rounds/cap) and per-criterion `status`
 *       onto the card WITHOUT replacing the result's own definition/
 *       criteria/dod text.
 *
 * The replay-path scaffold ((b)/(d)) mirrors ChatScreen.tool-order.test.tsx
 * exactly (full ChatScreen render, PlainMessageList forced via
 * ResizeObserver=undefined, the same stable-contract mocks for every OTHER
 * tool UI) — `set_goal` is deliberately left UNMOCKED so the real
 * SetGoalCardBlock/GoalEchoCard render and can be asserted against.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import { existsSync } from 'node:fs'
import { join } from 'node:path'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage, PositionedToolCall } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

type CapturedRenderFn = (props: {
  args?: unknown
  result: unknown
  status: { type: string; reason?: string }
  isError?: boolean
}) => React.ReactNode
const capturedToolUIs = vi.hoisted((): Record<string, CapturedRenderFn> => ({}))

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
    // Captures every registered tool UI's render config by toolName —
    // `SetGoalToolUI` (src/components/chat/tools/SetGoalToolUI.tsx) calls
    // this at module load time (triggered transitively the moment
    // ChatScreen.tsx imports `SetGoalCardBlock` from the same file), so
    // `capturedToolUIs['set_goal']` is populated before any test body runs
    // — see (a) below.
    makeAssistantToolUI: (config: { toolName?: string; render?: CapturedRenderFn }) => {
      if (typeof config.toolName === 'string' && config.render) {
        capturedToolUIs[config.toolName] = config.render
      }
      return () => null
    },
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

vi.mock('../historical-markdown', () => ({
  HistoricalMessageMarkdown: ({ content }: { content: string }) =>
    React.createElement('div', { 'data-testid': 'historical-markdown' }, content),
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('../RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('../SubagentBlock', () => ({ SubagentBlock: () => null }))
// Every OTHER tool UI is mocked down to the stable contract (see
// ChatScreen.tool-order.test.tsx) — `set_goal` is deliberately NOT among
// them; ChatScreen.tsx's parts loop routes it through the real
// SetGoalCardBlock (`../tools/SetGoalToolUI`, unmocked below).
vi.mock('../tools/GenericToolCall', () => ({
  GenericToolCall: ({ toolName }: { toolName: string }) =>
    React.createElement('div', { 'data-testid': 'tool-call-badge', 'data-tool': toolName }, toolName),
}))
vi.mock('../tools/BrowserTool', () => ({
  isReplayBrowserToolName: () => false,
  BrowserToolReplayBlock: ({ toolName }: { toolName: string }) =>
    React.createElement('div', { 'data-testid': 'tool-call-badge', 'data-tool': toolName }, toolName),
}))
vi.mock('../tools/WebServeUI', () => ({
  WebServeBlock: ({ toolName }: { toolName: string }) =>
    React.createElement('div', { 'data-testid': 'tool-call-badge', 'data-tool': toolName }, toolName),
}))
vi.mock('../markdown-text', () => ({
  MarkdownText: () => React.createElement('div', {}),
}))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
vi.mock('../composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('../composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('../composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {},
  getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))

import { ChatScreen } from '../ChatScreen'
import {
  SetGoalCardBlock,
  buildFrameFromSetGoalResult,
  parseSetGoalResult,
  classifySetGoalCall,
  type SetGoalResult,
} from '../tools/SetGoalToolUI'
import { useChatPreferencesStore } from '@/store/chatPreferences'

const SID = 'test-session-goal-card-anchoring'

/** The PERSISTED replay shape (review S12): the transcript stores every
 * plain-text tool result as `{ text: "<ForLLM text>" }` (pkg/agent/loop.go's
 * tcRecord) and replay.go forwards that map unchanged — so on the replay
 * path `tc.result` is an envelope whose `text` holds the payload JSON as a
 * STRING. Taken verbatim from a live session's transcript.jsonl. */
function goalResultEnvelope(overrides: Partial<SetGoalResult> & { mode?: string } = {}): { text: string } {
  return { text: goalResultJSON(overrides) }
}

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
}

function goalResultJSON(overrides: Partial<SetGoalResult> & { mode?: string } = {}): string {
  const base = {
    mode: 'register',
    goal_id: 'goal-anchor-1',
    definition: 'Ship a playable browser tetris game.',
    criteria_count: 1,
    dod_count: 0,
    criteria: [
      { kind: 'prose', judgment: 'boolean', text: 'the game is playable end to end', author: { kind: 'agent', id: 'mia' }, status: 'pending' },
    ],
    dod: [],
  }
  return JSON.stringify({ ...base, ...overrides })
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
})

afterEach(() => {
  vi.unstubAllGlobals()
})

// ── (c) GoalThreadTailCards no longer exists ────────────────────────────────

describe('GoalThreadTailCards is deleted (ADR-082 D9)', () => {
  it('the module file no longer exists on disk', () => {
    expect(existsSync(join(__dirname, '..', 'GoalThreadTailCards.tsx'))).toBe(false)
    expect(existsSync(join(__dirname, '..', 'GoalThreadTailCards.test.tsx'))).toBe(false)
  })

  it('ChatScreen renders no goal-echo-card when only a goalPill exists and no set_goal call is present', async () => {
    seedBucket([
      {
        id: 'msg_no_call',
        role: 'assistant',
        content: 'Working on it.',
        timestamp: new Date().toISOString(),
        status: 'done',
      },
    ])
    const pill: GoalStatusFrame = {
      type: 'goal_status',
      session_id: SID,
      goal_id: 'goal-orphan',
      condition: 'ship it',
      definition: 'Ship it.',
      round: 0,
      max_rounds: 10,
      latest_reason: '',
      active_loops: 0,
      cap: 16,
      state: 'active',
      criteria: [
        { kind: 'prose', judgment: 'boolean', text: 'it ships', author: { kind: 'agent', id: 'mia' }, status: 'pending' },
      ],
    }
    useChatStore.setState({ goalPills: { 'goal-orphan': pill } })

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    expect(container.querySelector('[data-testid="goal-echo-card"]')).toBeNull()
  })
})

// ── (a) live path ────────────────────────────────────────────────────────────

describe('SetGoalToolUI — live path', () => {
  it('registers a render function for "set_goal"', () => {
    expect(capturedToolUIs['set_goal']).toBeDefined()
  })

  it('a completed result renders GoalEchoCard with the registered record', () => {
    const renderFn = capturedToolUIs['set_goal']!
    const { container } = render(
      <>{renderFn({ args: {}, result: goalResultJSON(), status: { type: 'complete' } })}</>,
    )
    const card = container.querySelector('[data-testid="goal-echo-card"]')
    expect(card).not.toBeNull()
    expect(container.querySelector('[data-testid="goal-echo-statement"]')?.textContent).toBe(
      'Ship a playable browser tetris game.',
    )
  })

  it('a still-running call (no result yet) renders nothing', () => {
    const renderFn = capturedToolUIs['set_goal']!
    const { container } = render(
      <>{renderFn({ args: {}, result: undefined, status: { type: 'running' } })}</>,
    )
    expect(container.querySelector('[data-testid="goal-echo-card"]')).toBeNull()
    expect(container).toBeEmptyDOMElement()
  })

  it('a completed call with NO result at all renders nothing', () => {
    const renderFn = capturedToolUIs['set_goal']!
    const { container } = render(
      <>{renderFn({ args: {}, result: null, status: { type: 'complete' } })}</>,
    )
    expect(container).toBeEmptyDOMElement()
  })
})

// ── S4: a FAILED set_goal is never invisible ─────────────────────────────────

describe('SetGoalToolUI — failed call (review S4)', () => {
  afterEach(() => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: false })
    })
  })

  it('verbose OFF: a failed call renders a one-line quiet "Goal registration failed" trace with the error on expand', () => {
    const renderFn = capturedToolUIs['set_goal']!
    const { container } = render(
      <>
        {renderFn({
          args: { mode: 'register' },
          result: 'set_goal rejected: definition is required',
          status: { type: 'incomplete', reason: 'error' },
          isError: true,
        })}
      </>,
    )
    expect(container.querySelector('[data-testid="goal-echo-card"]')).toBeNull()
    const failed = container.querySelector('[data-testid="set-goal-failed"]')
    expect(failed).not.toBeNull()
    expect(failed?.textContent).toContain('Goal registration failed')
    expect(container.querySelector('[data-testid="set-goal-failed-detail"]')?.textContent).toContain(
      'definition is required',
    )
    // The raw chip stays hidden — the trace is the dedicated UI's own.
    expect(container.querySelector('[data-testid="tool-call-badge"]')).toBeNull()
  })

  it('verbose ON: a failed call falls through to GenericToolCall (the raw call), not the quiet trace', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    const renderFn = capturedToolUIs['set_goal']!
    const { container } = render(
      <>
        {renderFn({
          args: { mode: 'register' },
          result: 'set_goal rejected: definition is required',
          status: { type: 'incomplete', reason: 'error' },
          isError: true,
        })}
      </>,
    )
    expect(container.querySelector('[data-testid="set-goal-failed"]')).toBeNull()
    // GenericToolCall is mocked in this file to a badge div carrying data-tool.
    expect(container.querySelector('[data-testid="tool-call-badge"][data-tool="set_goal"]')).not.toBeNull()
  })

  it('verbose ON: a SUCCESSFUL call also falls through to GenericToolCall — no card pre-empts the raw view', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    const renderFn = capturedToolUIs['set_goal']!
    const { container } = render(
      <>{renderFn({ args: {}, result: goalResultJSON(), status: { type: 'complete' } })}</>,
    )
    expect(container.querySelector('[data-testid="goal-echo-card"]')).toBeNull()
    expect(container.querySelector('[data-testid="tool-call-badge"][data-tool="set_goal"]')).not.toBeNull()
  })

  it('replay path (verbose OFF): a message whose ONLY part is a failed set_goal shows the trace, not an empty shell', async () => {
    const assistantMsg: ChatMessage = {
      id: 'msg_failed_set_goal',
      role: 'assistant',
      content: '',
      timestamp: new Date().toISOString(),
      status: 'done',
      tool_calls: [
        {
          id: 'tc_failed',
          tool: 'set_goal',
          params: { mode: 'register' },
          result: undefined,
          error: 'set_goal rejected: criteria is required, at least one',
          status: 'error',
          textOffset: 0,
        } as PositionedToolCall,
      ],
    }
    seedBucket([assistantMsg])
    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })
    const failed = container.querySelector('[data-testid="set-goal-failed"]')
    expect(failed).not.toBeNull()
    expect(container.querySelector('[data-testid="set-goal-failed-detail"]')?.textContent).toContain(
      'criteria is required',
    )
  })

  it('classifySetGoalCall (the shared decision table wouldToolCallBeVisible consults) agrees with the renderer', () => {
    const base = { args: {}, isRunning: false, verboseChatEnabled: false }
    expect(classifySetGoalCall({ ...base, result: undefined, isError: false })).toBe('hidden')
    expect(classifySetGoalCall({ ...base, result: undefined, isError: false, isRunning: true })).toBe('hidden')
    expect(classifySetGoalCall({ ...base, result: 'set_goal rejected: x', isError: true })).toBe('failed')
    expect(classifySetGoalCall({ ...base, result: goalResultJSON(), isError: false })).toBe('card')
    expect(classifySetGoalCall({ ...base, result: goalResultEnvelope(), isError: false })).toBe('card')
    expect(classifySetGoalCall({ ...base, result: 'goal record register: 1 criteria, 2 DoD items', isError: false })).toBe('chip')
    expect(classifySetGoalCall({ ...base, result: undefined, isError: false, verboseChatEnabled: true })).toBe('raw')
    expect(classifySetGoalCall({ ...base, result: 'set_goal rejected: x', isError: true, verboseChatEnabled: true })).toBe('raw')
  })
})

// ── S9/S12: result parsing never throws; the persisted envelope is unwrapped ─

describe('parseSetGoalResult — shapes and malformed input (review S9/S12)', () => {
  it('unwraps the persisted replay envelope { text: "<json>" } (S12)', () => {
    const parsed = parseSetGoalResult(goalResultEnvelope({ goal_id: 'goal-env-1' }))
    expect(parsed).not.toBeNull()
    expect(parsed?.goal_id).toBe('goal-env-1')
    expect(parsed?.definition).toBe('Ship a playable browser tetris game.')
    expect(parsed?.criteria).toHaveLength(1)
  })

  it('unwraps the exact persisted transcript.jsonl shape seen in the e2e run (assessment first, goal_id later)', () => {
    const persisted = {
      text: JSON.stringify({
        assessment: { clarity: 'clear', assumptions: ['browser build'] },
        criteria: [
          { kind: 'prose', judgment: 'boolean', text: 'game is playable', author: { kind: 'agent', id: 'mia' }, status: 'pending' },
        ],
        criteria_count: 1,
        definition: 'Ship tetris.',
        dod: [],
        dod_count: 0,
        goal_id: 'goal-e2e-1',
        mode: 'register',
      }),
    }
    const parsed = parseSetGoalResult(persisted)
    expect(parsed?.goal_id).toBe('goal-e2e-1')
    expect(parsed?.definition).toBe('Ship tetris.')
  })

  it('accepts { text: <object> } and an already-parsed object alike', () => {
    const obj = JSON.parse(goalResultJSON({ goal_id: 'goal-obj' }))
    expect(parseSetGoalResult(obj)?.goal_id).toBe('goal-obj')
    expect(parseSetGoalResult({ text: obj })?.goal_id).toBe('goal-obj')
  })

  it('a pre-D9 result WITHOUT goal_id still parses (goal_id "") instead of vanishing', () => {
    const noId = JSON.stringify({ definition: 'Old goal.', criteria: [{ text: 'it works', judgment: 'boolean' }], dod: [] })
    const parsed = parseSetGoalResult(noId)
    expect(parsed).not.toBeNull()
    expect(parsed?.goal_id).toBe('')
    expect(parsed?.criteria).toHaveLength(1)
  })

  it('drops malformed criteria/dod items (no text) instead of throwing', () => {
    const malformed = JSON.stringify({
      goal_id: 'goal-mal',
      definition: 'Def.',
      criteria: [
        { kind: 'prose', judgment: 'boolean', text: 'good one', status: 'pending' },
        { kind: 'prose', judgment: 'boolean' }, // no text
        'not an object',
        null,
        { text: '' }, // empty text
      ],
      dod: [42, { text: 'dod ok', judgment: 'boolean', provenance: 'floor', status: 'weird' }],
    })
    const parsed = parseSetGoalResult(malformed)
    expect(parsed?.criteria.map((c) => c.text)).toEqual(['good one'])
    expect(parsed?.dod).toHaveLength(1)
    expect(parsed?.dod[0].status).toBe('pending') // normalised from a stray value
  })

  it('a malformed item with a MATCHING pill does not throw inside render (was: thread unmount)', () => {
    const pill: GoalStatusFrame = {
      type: 'goal_status',
      session_id: SID,
      goal_id: 'goal-mal-2',
      condition: 'x',
      round: 1,
      max_rounds: 5,
      latest_reason: '',
      active_loops: 0,
      cap: 4,
      state: 'active',
      criteria: [{ kind: 'prose', judgment: 'boolean', text: 'good one', author: { kind: 'agent', id: 'mia' }, status: 'met' }],
    }
    act(() => {
      useChatStore.setState({ goalPills: { 'goal-mal-2': pill } })
    })
    const malformed = JSON.stringify({
      goal_id: 'goal-mal-2',
      definition: 'Def.',
      criteria: [{ judgment: 'boolean' }, { text: 'good one', judgment: 'boolean' }],
      dod: [],
    })
    expect(() =>
      render(<SetGoalCardBlock result={malformed} status={{ type: 'complete' }} />),
    ).not.toThrow()
  })

  it('returns null for a plain error sentence, an empty string, and non-record JSON', () => {
    expect(parseSetGoalResult('set_goal rejected: definition is required')).toBeNull()
    expect(parseSetGoalResult('')).toBeNull()
    expect(parseSetGoalResult('[1,2,3]')).toBeNull()
    expect(parseSetGoalResult({ text: 'not json' })).toBeNull()
    expect(parseSetGoalResult(JSON.stringify({ goal_id: 'g', criteria: [] }))).toBeNull() // no definition
  })

  it('a present-but-unparseable SUCCESS result renders a minimal chip so the registration leaves a trace', () => {
    const { container } = render(
      <SetGoalCardBlock result="goal record register: 1 criteria, 2 DoD items" status={{ type: 'complete' }} />,
    )
    const chip = container.querySelector('[data-testid="set-goal-chip"]')
    expect(chip).not.toBeNull()
    expect(chip?.textContent).toContain('Goal record registered')
  })
})

// ── (b) replay path — interleaved position ──────────────────────────────────

describe('SetGoalCardBlock — replay path (VirtualAssistantMessageRow parts loop)', () => {
  it('renders the card between the text that precedes and follows the call, in true DOM order', async () => {
    const before = 'Registering the goal now. '
    const after = 'Let me know if you want to steer it.'
    const content = before + after
    const assistantMsg: ChatMessage = {
      id: 'msg_set_goal_1',
      role: 'assistant',
      content,
      timestamp: new Date().toISOString(),
      status: 'done',
      tool_calls: [
        {
          id: 'tc_set_goal_1',
          tool: 'set_goal',
          params: { definition: 'Ship a playable browser tetris game.', criteria: [] },
          // The replay path hands the PERSISTED envelope shape (S12) — the
          // raw JSON string is the LIVE shape and never reaches replay.
          result: goalResultEnvelope(),
          status: 'success',
          textOffset: before.length,
        } as PositionedToolCall,
      ],
    }
    seedBucket([assistantMsg])

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const nodes = Array.from(
      container.querySelectorAll('[data-testid="historical-markdown"], [data-testid="goal-echo-card"]'),
    )
    expect(nodes).toHaveLength(3)
    expect(nodes[0]).toHaveAttribute('data-testid', 'historical-markdown')
    expect(nodes[0].textContent).toContain('Registering the goal now.')
    expect(nodes[1]).toHaveAttribute('data-testid', 'goal-echo-card')
    expect(nodes[2]).toHaveAttribute('data-testid', 'historical-markdown')
    expect(nodes[2].textContent).toContain('Let me know if you want to steer it.')

    const position = nodes[0].compareDocumentPosition(nodes[1])
    expect((position & Node.DOCUMENT_POSITION_FOLLOWING) !== 0).toBe(true)
    const position2 = nodes[1].compareDocumentPosition(nodes[2])
    expect((position2 & Node.DOCUMENT_POSITION_FOLLOWING) !== 0).toBe(true)
  })
})

// ── (d) two set_goal calls (register + amend) → two cards, own records ─────

describe('SetGoalCardBlock — register then amend, own record each', () => {
  it('renders two cards, each showing its OWN call\'s definition, at their own positions', async () => {
    const seg1 = 'Registering the goal. '
    const seg2 = 'Amending it with sound effects.'
    const content = seg1 + seg2
    const registerResult = goalResultEnvelope({
      mode: 'register',
      definition: 'Ship a playable browser tetris game.',
    })
    const amendResult = goalResultEnvelope({
      mode: 'update',
      definition: 'Ship a playable browser tetris game with sound.',
      criteria: [
        { kind: 'prose', judgment: 'boolean', text: 'the game is playable end to end', author: { kind: 'agent', id: 'mia' }, status: 'pending' },
        { kind: 'prose', judgment: 'boolean', text: 'sound effects play on line clear', author: { kind: 'agent', id: 'mia' }, status: 'pending' },
      ],
    })
    const assistantMsg: ChatMessage = {
      id: 'msg_set_goal_amend',
      role: 'assistant',
      content,
      timestamp: new Date().toISOString(),
      status: 'done',
      tool_calls: [
        {
          id: 'tc_register',
          tool: 'set_goal',
          params: {},
          result: registerResult,
          status: 'success',
          textOffset: seg1.length,
        } as PositionedToolCall,
        {
          id: 'tc_amend',
          tool: 'set_goal',
          params: {},
          result: amendResult,
          status: 'success',
          textOffset: content.length,
        } as PositionedToolCall,
      ],
    }
    seedBucket([assistantMsg])

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const cards = Array.from(container.querySelectorAll('[data-testid="goal-echo-card"]'))
    expect(cards).toHaveLength(2)
    const statements = cards.map((c) => c.querySelector('[data-testid="goal-echo-statement"]')?.textContent)
    expect(statements).toEqual([
      'Ship a playable browser tetris game.',
      'Ship a playable browser tetris game with sound.',
    ])
    // The register card's own record is untouched by the amendment — still
    // shows the ORIGINAL 1-criterion count, not the amended 2.
    expect(cards[0].querySelector('[data-testid="goal-echo-criteria"]')?.textContent).toContain('1 criterion')
    expect(cards[1].querySelector('[data-testid="goal-echo-criteria"]')?.textContent).toContain('2 criteria')
  })
})

// ── (e) live overlay — progress overlays without moving/replacing the card ─

describe('buildFrameFromSetGoalResult — live overlay by goal_id (FR-018/S-17)', () => {
  const result: SetGoalResult = {
    goal_id: 'goal-overlay-1',
    definition: 'Ship a playable browser tetris game.',
    criteria: [
      { kind: 'prose', judgment: 'boolean', text: 'the game is playable end to end', author: { kind: 'agent', id: 'mia' }, status: 'pending' },
      { kind: 'prose', judgment: 'boolean', text: 'sound effects play on line clear', author: { kind: 'agent', id: 'mia' }, status: 'pending' },
    ],
    dod: [],
  }

  it('with no matching pill, the view carries the record only and reports NO live progress (review S3)', () => {
    const view = buildFrameFromSetGoalResult(result, undefined)
    expect(view.hasLiveProgress).toBe(false)
    expect(view.frame.definition).toBe(result.definition)
    expect(view.frame.criteria?.map((c) => c.status)).toEqual(['pending', 'pending'])
  })

  it('a result WITHOUT goal_id never matches a pill, even one whose goal_id is also empty', () => {
    const emptyIdPill: GoalStatusFrame = {
      type: 'goal_status',
      session_id: SID,
      goal_id: '',
      condition: 'x',
      round: 4,
      max_rounds: 9,
      latest_reason: '',
      active_loops: 0,
      cap: 2,
      state: 'judging',
    }
    const view = buildFrameFromSetGoalResult({ ...result, goal_id: '' }, emptyIdPill)
    expect(view.hasLiveProgress).toBe(false)
  })

  it('a matching pill overlays progress fields and per-criterion status by text, WITHOUT replacing definition/criteria text', () => {
    const pill: GoalStatusFrame = {
      type: 'goal_status',
      session_id: SID,
      goal_id: 'goal-overlay-1',
      condition: 'ship a playable browser tetris game',
      round: 3,
      max_rounds: 20,
      latest_reason: 'first criterion verified',
      active_loops: 1,
      cap: 16,
      state: 'judging',
      criteria: [
        { kind: 'prose', judgment: 'boolean', text: 'the game is playable end to end', author: { kind: 'agent', id: 'mia' }, status: 'met' },
      ],
    }
    const { frame, hasLiveProgress } = buildFrameFromSetGoalResult(result, pill)
    expect(hasLiveProgress).toBe(true)
    // Progress fields overlay from the pill.
    expect(frame.round).toBe(3)
    expect(frame.max_rounds).toBe(20)
    expect(frame.cap).toBe(16)
    expect(frame.state).toBe('judging')
    // The result's OWN definition/criteria TEXT is untouched (never
    // replaced by the pill's condition/criteria set).
    expect(frame.definition).toBe(result.definition)
    expect(frame.criteria).toHaveLength(2)
    // Per-criterion status overlays by matching text; the unmatched second
    // criterion keeps its original (freshly-written) "pending" status.
    expect(frame.criteria?.[0].text).toBe('the game is playable end to end')
    expect(frame.criteria?.[0].status).toBe('met')
    expect(frame.criteria?.[1].status).toBe('pending')
  })

  it('a pill for a DIFFERENT goal_id contributes nothing', () => {
    const otherPill: GoalStatusFrame = {
      type: 'goal_status',
      session_id: SID,
      goal_id: 'goal-unrelated',
      condition: 'a different goal entirely',
      round: 9,
      max_rounds: 40,
      latest_reason: '',
      active_loops: 0,
      cap: 16,
      state: 'done',
    }
    const view = buildFrameFromSetGoalResult(result, otherPill)
    expect(view.hasLiveProgress).toBe(false)
    expect(view.frame.definition).toBe(result.definition)
  })
})

// SetGoalCardBlock is exercised directly (not just via buildFrameFromSetGoalResult)
// to prove the OVERLAY actually reaches the rendered card, real store included.
describe('SetGoalCardBlock — overlay reaches the rendered DOM', () => {
  beforeEach(() => {
    act(() => {
      useChatStore.setState({ goalPills: {} })
    })
  })

  it('round/max_rounds/cap shown in the card reflect the live pill, not the (absent) defaults', () => {
    const pill: GoalStatusFrame = {
      type: 'goal_status',
      session_id: SID,
      goal_id: 'goal-dom-overlay',
      condition: 'ship it',
      round: 5,
      max_rounds: 12,
      latest_reason: '',
      active_loops: 0,
      cap: 8,
      state: 'active',
    }
    act(() => {
      useChatStore.setState({ goalPills: { 'goal-dom-overlay': pill } })
    })
    const result = goalResultJSON({ goal_id: 'goal-dom-overlay' })
    const { container } = render(<SetGoalCardBlock result={result} status={{ type: 'complete' }} />)
    const roundEl = container.querySelector('[data-testid="goal-echo-round"]')
    expect(roundEl?.textContent).toContain('12 rounds')
    expect(roundEl?.textContent).toContain('8 concurrent loops')
    // The card is still anchored — definition text is still the result's.
    expect(container.querySelector('[data-testid="goal-echo-statement"]')?.textContent).toBe(
      'Ship a playable browser tetris game.',
    )
  })

  it('with NO pill the card renders the record without any round/cap line (review S3 — no false progress claim)', () => {
    const result = goalResultJSON({ goal_id: 'goal-no-pill-yet' })
    const { container } = render(<SetGoalCardBlock result={result} status={{ type: 'complete' }} />)
    expect(container.querySelector('[data-testid="goal-echo-card"]')).not.toBeNull()
    expect(container.querySelector('[data-testid="goal-echo-statement"]')?.textContent).toBe(
      'Ship a playable browser tetris game.',
    )
    expect(container.querySelector('[data-testid="goal-echo-round"]')).toBeNull()
    expect(container.textContent).not.toContain('0 rounds')
  })

  it('a pre-D9 result lacking goal_id renders a minimal card (no overlay, no progress) instead of vanishing (review S9)', () => {
    const noId = JSON.stringify({ definition: 'Old goal.', criteria: [{ text: 'it works', judgment: 'boolean' }], dod: [] })
    const { container } = render(<SetGoalCardBlock result={noId} status={{ type: 'complete' }} />)
    expect(container.querySelector('[data-testid="goal-echo-card"]')).not.toBeNull()
    expect(container.querySelector('[data-testid="goal-echo-statement"]')?.textContent).toBe('Old goal.')
    expect(container.querySelector('[data-testid="goal-echo-round"]')).toBeNull()
  })
})
