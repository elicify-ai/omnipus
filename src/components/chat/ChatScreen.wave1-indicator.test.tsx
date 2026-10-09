// FR-022 / FR-023. BDD-07.1, BDD-07.2, BDD-E06, BDD-E07.
// Oracles are the spec indicator table and the phrases already published in
// ThinkingIndicator — not a new phase vocabulary.
// Live path: real AssistantRuntimeProvider (same harness as
// ChatScreen.thinking-indicator-context.test.tsx).
// Plain / replay path: ResizeObserver removed so PlainMessageList renders
// VirtualAssistantMessageRow, including a streaming row.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, within, waitFor } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, useMessagePartText } from '@assistant-ui/react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { useToolApprovalStore } from '@/store/toolApproval'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}

vi.stubGlobal('ResizeObserver', ResizeObserverStub)
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollTo) {
  Element.prototype.scrollTo = function () {}
}

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      { id: 'agent-1', name: 'Mia', color: '#3B82F6', icon: 'lightbulb', figure: 'Omnipus', role: 'general' },
      { id: 'agent-jim', name: 'Jim', color: '#22D3EE', icon: 'graph', figure: 'Man', role: 'general' },
      { id: 'agent-research', name: 'Research Assistant', color: '#3B82F6', figure: 'Monogram', role: 'researcher' },
    ]),
    fetchSessionMessages: vi.fn().mockResolvedValue([]),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
    uploadFiles: vi.fn(),
    fetchProviders: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./ActivityBar', () => ({ ActivityBar: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('./markdown-text', () => ({
  MarkdownText: () => {
    const { text } = useMessagePartText()
    return React.createElement('span', { 'data-testid': 'assistant-text' }, text)
  },
}))

import { ChatScreen } from './ChatScreen'

function Providers({ children }: { children: React.ReactNode }) {
  const queryClientRef = React.useRef<QueryClient | null>(null)
  if (!queryClientRef.current) {
    queryClientRef.current = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  }
  const runtime = useOmnipusRuntime()
  return (
    <QueryClientProvider client={queryClientRef.current}>
      <AssistantRuntimeProvider runtime={runtime}>{children}</AssistantRuntimeProvider>
    </QueryClientProvider>
  )
}

function seedMessages(sid: string, messages: ChatMessage[], flags: { streaming: boolean; replaying: boolean }) {
  const bucket = makeBucketMessages(messages)
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [sid]: {
        ...((s.sessionsById ?? {})[sid] ?? {}),
        ...bucket,
        isStreaming: flags.streaming,
        isReplaying: flags.replaying,
        replayCompletedForSession: sid,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        toolCallOwnerMessageId: {},
        goalStatus: null,
        goalPills: {},
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
    isStreaming: flags.streaming,
    isReplaying: flags.replaying,
    replayCompletedForSession: sid,
    toolCalls: {},
    toolCallOrder: [],
    textAtToolCallStart: {},
    goalStatus: null,
    goalPills: {},
    pendingAsk: null,
  }))
  useSessionStore.setState({ activeSessionId: sid, activeAgentId: 'agent-1' })
  useConnectionStore.setState({
    connection: null,
    isConnected: true,
    connectionError: null,
    reconnectPhase: null,
    reconnectAttempt: 0,
  })
}

function streamingPair(sid: string, agentId: string): [
  Extract<ChatMessage, { role: 'user' }>,
  Extract<ChatMessage, { role: 'assistant' }>,
] {
  return [
    {
      id: `${sid}_user`,
      role: 'user',
      content: 'hi',
      timestamp: new Date().toISOString(),
      status: 'done',
    },
    {
      id: `${sid}_assistant`,
      role: 'assistant',
      content: '',
      timestamp: new Date().toISOString(),
      status: 'streaming',
      isStreaming: true,
      agentId,
      turnId: `${sid}_turn`,
    },
  ]
}

async function mount(): Promise<HTMLElement> {
  let container!: HTMLElement
  await act(async () => {
    const result = render(
      <Providers>
        <ChatScreen />
      </Providers>,
    )
    container = result.container
  })
  return container
}

function assistantBubble(container: HTMLElement): HTMLElement {
  const bubble = container.querySelector('[data-testid="assistant-message"]')
  expect(bubble).toBeTruthy()
  return bubble as HTMLElement
}

function expectNameOnly(bubble: HTMLElement, name: string) {
  const label = within(bubble).getByTestId('agent-label')
  expect(label.textContent).toBe(name)
  // FR-022 semantic boundary, not a blacklist of a retired avatar's class.
  expect(label.querySelectorAll('svg, img, picture, canvas, [role="img"], [data-art], [data-ink], [data-figure], [data-avatar]')).toHaveLength(0)
  expect(label.childElementCount).toBe(0)
  expect(['', 'none']).toContain(getComputedStyle(label).backgroundImage)
}

function expectMarkMotion(bubble: HTMLElement, motion: 'thinking' | 'working' | 'waiting' | 'none') {
  const mark = bubble.querySelector('[data-testid="agent-icon"]')
  expect(mark, `real ${motion} phase mark`).not.toBeNull()
  expect(mark).toHaveAttribute('data-motion', motion)
  if (motion === 'none') expect(mark?.querySelector('[data-glow]')).toBeNull()
  else expect(mark?.querySelector('[data-glow]')).not.toBeNull()
}

// I1 / AC-16: the full reply name and painted initial must agree through the
// real screen → indicator → AgentIcon path, in both ink and glow. The guest's
// R deliberately differs from the active owner's M.
function expectMonogramReply(bubble: HTMLElement) {
  expectNameOnly(bubble, 'Research Assistant')
  const mark = within(bubble).getByTestId('agent-icon')
  expect(mark).toHaveAttribute('data-figure', 'Monogram')
  for (const layer of ['data-ink', 'data-glow']) {
    const letters = mark.querySelectorAll(`[${layer}] [data-initial]`)
    expect(letters, `${layer} has one guest initial`).toHaveLength(1)
    expect(letters[0].tagName.toLowerCase()).toBe('text')
    expect(letters[0]).toHaveAttribute('data-initial', 'R')
    expect(letters[0].textContent, `${layer} paints the guest's R`).toBe('R')
  }
  expect(within(bubble).getByText('Thinking…')).toBeInTheDocument()
  expect(useSessionStore.getState().activeAgentId).toBe('agent-1')
}

describe('live inline indicator', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', ResizeObserverStub)
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
    useChatPreferencesStore.getState().setVerboseChatEnabled(false)
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    vi.stubGlobal('ResizeObserver', ResizeObserverStub)
  })

  it('replaces the bouncing dots with the agent mark and keeps the existing Thinking phrase', async () => {
    seedMessages('live-1', streamingPair('live-1', 'agent-1'), { streaming: true, replaying: false })
    const bubble = assistantBubble(await mount())
    expect(await within(bubble).findByText('Mia')).toBeInTheDocument()
    expect(within(bubble).getByText('Thinking…')).toBeInTheDocument()
    expect(bubble.querySelector('.animate-bounce')).toBeNull()
    expect(bubble.querySelector('.w-7')).toBeNull()
    const mark = bubble.querySelector('[data-art]')
    expect(mark?.getAttribute('data-art')).toBe('octopus')
    expect(mark?.querySelector('svg')?.getAttribute('width')).toBe('48')
    expect(mark?.querySelector('[data-ink]')?.getAttribute('style') ?? '').not.toMatch(/opacity:\s*0/)
    expectNameOnly(bubble, 'Mia')
    expectMarkMotion(bubble, 'thinking')
  })

  it('names a guest reply as that guest and does not switch the chat owner', async () => {
    seedMessages('guest-1', streamingPair('guest-1', 'agent-jim'), { streaming: true, replaying: false })
    const bubble = assistantBubble(await mount())
    expect(await within(bubble).findByText('Jim')).toBeInTheDocument()
    expect(within(bubble).queryByText('Mia')).not.toBeInTheDocument()
    expect(useSessionStore.getState().activeAgentId).toBe('agent-1')
    expect(bubble.querySelector('.w-7')).toBeNull()
    expect(bubble.querySelector('[data-art]')?.getAttribute('data-art')).toBe('man')
    expectNameOnly(bubble, 'Jim')
    expectMarkMotion(bubble, 'thinking')
  })

  it('passes the guest name into the live Monogram mark through ResolvedAgentMark (I1)', async () => {
    const sid = 'live-monogram-name'
    seedMessages(sid, streamingPair(sid, 'agent-research'), { streaming: true, replaying: false })
    const bubble = assistantBubble(await mount())
    await within(bubble).findByText('Research Assistant')
    // AssistantUI's running root proves this is the live path, not the
    // plain/virtual row, which marks its root complete even while streaming.
    expect(bubble).toHaveAttribute('data-status', 'running')
    expectMonogramReply(bubble)
  })

  it('keeps the existing hidden-command label while the phase is Working', async () => {
    const sid = 'work-1'
    const messages = streamingPair(sid, 'agent-1')
    seedMessages(sid, messages, { streaming: true, replaying: false })
    const toolCallId = 'tc_bash'
    const description = 'Running the test suite'
    const liveToolCalls = {
      [toolCallId]: {
        id: toolCallId,
        call_id: toolCallId,
        tool: 'bash',
        params: { action: 'run', run_in_background: true, description, command: 'go test ./pkg/gateway' },
        status: 'running' as const,
      },
    }
    useChatStore.setState((s) => ({
      ...s,
      toolCalls: liveToolCalls,
      toolCallOrder: [toolCallId],
    }))
    const bubble = assistantBubble(await mount())
    // Existing label oracle: deriveBashThinkingLabel returns the description
    // when it is already within 48 characters. The raw command must not appear.
    expect(within(bubble).getByText(description)).toBeInTheDocument()
    expect(bubble.textContent).not.toContain('go test')
    expect(bubble.querySelector('.animate-bounce')).toBeNull()
    expectMarkMotion(bubble, 'working')
    expectNameOnly(bubble, 'Mia')
  })

  it('derives Working from the real active-session tool reducer, with decision and disconnect precedence', async () => {
    const sid = 'correlated-tools'
    seedMessages(sid, streamingPair(sid, 'agent-jim'), { streaming: true, replaying: false })
    const description = 'Checking the release notes'
    act(() => useChatStore.getState().startToolCall('tc-correlated', 'bash', {
      action: 'run', run_in_background: true, description, command: 'git status',
    }))
    expect(useChatStore.getState().sessionsById[sid]?.toolCalls['tc-correlated'].status).toBe('running')
    const bubble = assistantBubble(await mount())
    expect(await within(bubble).findByText(description)).toBeInTheDocument()
    expectMarkMotion(bubble, 'working')
    expectNameOnly(bubble, 'Jim')
    act(() => useToolApprovalStore.setState({ queue: [{
      approvalId: 'correlated-approval', toolCallId: 'tc-correlated', toolName: 'bash',
      args: { command: 'git status' }, agentId: 'agent-jim', sessionId: sid,
      turnId: `${sid}_turn`, expiresAt: Date.now() + 60_000,
    }], resolvedIds: [] }))
    expect(within(bubble).getByText('Waiting for your approval — bash')).toBeInTheDocument()
    expectMarkMotion(bubble, 'waiting')
    act(() => useConnectionStore.setState({ isConnected: false, reconnectPhase: 'reconnecting' }))
    expect(within(bubble).getByText('Unavailable/reconnecting')).toBeInTheDocument()
    expectMarkMotion(bubble, 'none')
    act(() => {
      useConnectionStore.setState({ isConnected: true, reconnectPhase: null })
      useToolApprovalStore.setState({ queue: [], resolvedIds: ['correlated-approval'] })
      useChatStore.getState().resolveToolCall('tc-correlated', 'clean', 'success')
    })
    expect(within(bubble).getByText('Thinking…')).toBeInTheDocument()
    expectMarkMotion(bubble, 'thinking')
    expect(within(bubble).queryByText(description)).not.toBeInTheDocument()
    expect(useSessionStore.getState().activeAgentId).toBe('agent-1')
  })

  it.each([
    { name: 'visible foreground bash', tool: 'bash', params: { action: 'run', command: 'git status' } },
    { name: 'hidden unlabelled ToolSearch', tool: 'ToolSearch', params: { query: 'select:Read' } },
    { name: 'hidden unlabelled delegate', tool: 'delegate', params: { action: 'status', call_id: 'earlier-dispatch' } },
  ])('shows Working for a correlated $name call regardless of card visibility', async ({ tool, params }) => {
    const sid = `visibility-${tool}`
    seedMessages(sid, streamingPair(sid, 'agent-jim'), { streaming: true, replaying: false })
    act(() => useChatStore.getState().startToolCall('tc-phase', tool, params))
    expect(useChatStore.getState().sessionsById[sid]?.toolCalls['tc-phase'].status).toBe('running')
    const bubble = assistantBubble(await mount())
    // W1-7 / Indicator input mapping: a running tool, not a hidden label,
    // is the Working discriminator. Use the existing working phrase, stable.
    expect(within(bubble).getByText('Working on it…')).toBeInTheDocument()
    expect(within(bubble).queryByText('Thinking…')).not.toBeInTheDocument()
    expectMarkMotion(bubble, 'working')
    expectNameOnly(bubble, 'Jim')
    act(() => useChatStore.getState().resolveToolCall('tc-phase', 'complete', 'success'))
    expect(within(bubble).getByText('Thinking…')).toBeInTheDocument()
    expect(within(bubble).queryByText('Working on it…')).not.toBeInTheDocument()
    expectMarkMotion(bubble, 'thinking')
    expect(useSessionStore.getState().activeAgentId).toBe('agent-1')
  })

  it('keeps Working when verbose chat reveals the running tool card', async () => {
    const sid = 'verbosity-phase'
    const description = 'Checking the release notes'
    seedMessages(sid, streamingPair(sid, 'agent-jim'), { streaming: true, replaying: false })
    act(() => useChatStore.getState().startToolCall('tc-verbosity', 'bash', {
      action: 'run', run_in_background: true, description, command: 'git status',
    }))
    const bubble = assistantBubble(await mount())
    expect(within(bubble).getByText(description)).toBeInTheDocument()
    expectMarkMotion(bubble, 'working')
    act(() => useChatPreferencesStore.getState().setVerboseChatEnabled(true))
    expect(within(bubble).getByText('Working on it…')).toBeInTheDocument()
    expect(within(bubble).queryByText(description)).not.toBeInTheDocument()
    expect(within(bubble).queryByText('Thinking…')).not.toBeInTheDocument()
    expectMarkMotion(bubble, 'working')
    act(() => useChatPreferencesStore.getState().setVerboseChatEnabled(false))
    expect(within(bubble).getByText(description)).toBeInTheDocument()
    expectMarkMotion(bubble, 'working')
  })

  it('preserves the goal phrase without letting it override a real Working phase', async () => {
    const sid = 'goal-tool-phase'
    seedMessages(sid, streamingPair(sid, 'agent-jim'), { streaming: true, replaying: false })
    const goalStatus: GoalStatusFrame = {
      type: 'goal_status', session_id: sid, goal_id: 'goal-tool-phase',
      condition: 'Ship the release notes', round: 0, max_rounds: 20,
      latest_reason: '', active_loops: 1, cap: 16, state: 'active',
    }
    act(() => useChatStore.setState((s) => ({
      goalStatus,
      sessionsById: { ...s.sessionsById, [sid]: { ...s.sessionsById[sid], goalStatus } },
    })))
    const bubble = assistantBubble(await mount())
    expect(within(bubble).getByText('Framing your goal')).toBeInTheDocument()
    expectMarkMotion(bubble, 'thinking')
    act(() => useChatStore.getState().startToolCall('tc-goal-tool', 'set_goal', { mode: 'register' }))
    expect(within(bubble).getByText('Setting acceptance criteria')).toBeInTheDocument()
    expectMarkMotion(bubble, 'working')
    act(() => useChatStore.getState().resolveToolCall('tc-goal-tool', 'registered', 'success'))
    expect(within(bubble).getByText('Framing your goal')).toBeInTheDocument()
    expectMarkMotion(bubble, 'thinking')
  })

  it('does not borrow Working from a tool owned by another reply', async () => {
    const sid = 'other-reply-tool'
    seedMessages(sid, streamingPair(sid, 'agent-jim'), { streaming: true, replaying: false })
    act(() => {
      useChatStore.getState().startToolCall('tc-other-reply', 'bash', {
        action: 'run', run_in_background: true, description: 'Other reply work', command: 'git status',
      })
      useChatStore.setState((s) => ({ sessionsById: {
        ...s.sessionsById,
        [sid]: { ...s.sessionsById[sid], toolCallOwnerMessageId: { 'tc-other-reply': 'older-assistant' } },
      } }))
    })
    expect(useChatStore.getState().sessionsById[sid]?.toolCalls['tc-other-reply'].status).toBe('running')
    const bubble = assistantBubble(await mount())
    expect(within(bubble).getByText('Thinking…')).toBeInTheDocument()
    expect(within(bubble).queryByText('Other reply work')).not.toBeInTheDocument()
    expectMarkMotion(bubble, 'thinking')
  })

  it('shows Waiting from a pending approval, not the thinking dots', async () => {
    const sid = 'wait-1'
    seedMessages(sid, streamingPair(sid, 'agent-1'), { streaming: true, replaying: false })
    useToolApprovalStore.setState({
      queue: [{
        approvalId: 'ap-1',
        toolCallId: 'tc-wait',
        toolName: 'bash',
        args: { command: 'git status' },
        agentId: 'agent-1',
        sessionId: sid,
        turnId: 'turn-1',
        expiresAt: Date.now() + 60_000,
      }],
      resolvedIds: [],
    })
    const bubble = assistantBubble(await mount())
    expect(within(bubble).getByText(/waiting for your approval/i)).toBeInTheDocument()
    expect(bubble.textContent).toContain('bash')
    expect(bubble.querySelector('.animate-bounce')).toBeNull()
    expectMarkMotion(bubble, 'waiting')
  })

  it('shows a static Unavailable/reconnecting phrase when the gateway is reconnecting', async () => {
    seedMessages('disc-1', streamingPair('disc-1', 'agent-1'), { streaming: true, replaying: false })
    useConnectionStore.setState({ isConnected: false, reconnectPhase: 'reconnecting', reconnectAttempt: 1, connectionError: null, connection: null })
    const bubble = assistantBubble(await mount())
    expect(within(bubble).getByText('Unavailable/reconnecting')).toBeInTheDocument()
    expect(bubble.querySelector('.animate-bounce')).toBeNull()
    expectMarkMotion(bubble, 'none')
  })

  it('draws no animation loop when reduced motion is on, and keeps the Thinking phrase', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    // Force a different phrase if a forbidden rotation interval runs.
    let randomBeat = 0
    vi.spyOn(Math, 'random').mockImplementation(() => (++randomBeat % 2 ? 0.1 : 0.3))
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: query.includes('prefers-reduced-motion'),
      media: query,
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent() { return false },
    }))
    seedMessages('still-1', streamingPair('still-1', 'agent-1'), { streaming: true, replaying: false })
    const bubble = assistantBubble(await mount())
    expect(within(bubble).getByText('Thinking…')).toBeInTheDocument()
    expect(bubble.querySelector('.animate-bounce')).toBeNull()
    expect(bubble.querySelector('[data-art]')?.getAttribute('data-art')).toBe('octopus')
    expectMarkMotion(bubble, 'none')
    for (let beat = 0; beat < 4; beat++) {
      await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
      expect(within(bubble).getByText('Thinking…')).toBeInTheDocument()
      expect(within(bubble).queryByText('Working on it…')).not.toBeInTheDocument()
      expect(within(bubble).queryByText('Analyzing…')).not.toBeInTheDocument()
      expectMarkMotion(bubble, 'none')
    }
  })

  it('actually advances the normal-motion phrase clock while the reduced-motion clock stays still', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    let randomBeat = 0
    vi.spyOn(Math, 'random').mockImplementation(() => (++randomBeat % 2 ? 0.1 : 0.3))
    vi.stubGlobal('matchMedia', (query: string) => ({ matches: false, media: query, addEventListener() {}, removeEventListener() {} }))
    seedMessages('clock-control', streamingPair('clock-control', 'agent-1'), { streaming: true, replaying: false })
    const bubble = assistantBubble(await mount())
    expect(within(bubble).getByText('Thinking…')).toBeInTheDocument()
    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
    expect(within(bubble).getByText('Working on it…')).toBeInTheDocument()
    expect(within(bubble).queryByText('Thinking…')).not.toBeInTheDocument()
  })
})

describe('plain, replay, and idle bubbles', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
    // Feature-detect is at render time. Undefined selects PlainMessageList.
    ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = undefined
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
    useChatPreferencesStore.getState().setVerboseChatEnabled(false)
  })

  afterEach(() => {
    vi.stubGlobal('ResizeObserver', ResizeObserverStub)
  })

  it.each(['success', 'error', 'cancelled'] as const)(
    'uses the plain reply’s real hidden tool status and returns to Thinking after %s',
    async (settledStatus) => {
      const sid = `plain-tool-${settledStatus}`
      const messages = streamingPair(sid, 'agent-jim')
      const call = {
        id: 'plain-tool', call_id: 'plain-tool', tool: 'delegate',
        params: { action: 'status', call_id: 'earlier-dispatch' }, status: 'running' as const,
      }
      messages[1].tool_calls = [call]
      seedMessages(sid, messages, { streaming: true, replaying: false })
      const bubble = assistantBubble(await mount())
      expect(await within(bubble).findByText('Jim')).toBeInTheDocument()
      expect(within(bubble).getByText('Working on it…')).toBeInTheDocument()
      expectMarkMotion(bubble, 'working')
      expectNameOnly(bubble, 'Jim')
      act(() => seedMessages(sid, [messages[0], {
        ...messages[1], tool_calls: [{ ...call, status: settledStatus, result: 'complete' }],
      }], { streaming: true, replaying: false }))
      expect(within(bubble).getByText('Thinking…')).toBeInTheDocument()
      expectMarkMotion(bubble, 'thinking')
      expect(within(bubble).queryByText('Working on it…')).not.toBeInTheDocument()
    },
  )

  it('passes the guest name into the plain Monogram mark through VirtualAssistantMessageRow (I1)', async () => {
    const sid = 'plain-monogram-name'
    seedMessages(sid, streamingPair(sid, 'agent-research'), { streaming: true, replaying: false })
    const bubble = assistantBubble(await mount())
    await within(bubble).findByText('Research Assistant')
    expect(globalThis.ResizeObserver).toBeUndefined()
    expect(bubble).toHaveAttribute('data-status', 'complete')
    expect(bubble.closest('[data-index]')).toBeNull()
    expectMonogramReply(bubble)
  })

  it('shows a finished plain-path reply as the guest name only', async () => {
    const sid = 'plain-1'
    seedMessages(sid, [{
      id: `${sid}_assistant`,
      role: 'assistant',
      content: 'Launch notes',
      timestamp: new Date().toISOString(),
      status: 'done',
      agentId: 'agent-jim',
    }], { streaming: false, replaying: false })
    const bubble = assistantBubble(await mount())
    expect(await within(bubble).findByText('Jim')).toBeInTheDocument()
    expect(bubble.textContent).toContain('Launch notes')
    expect(bubble.querySelector('.w-7')).toBeNull()
    expectNameOnly(bubble, 'Jim')
    expect(useSessionStore.getState().activeAgentId).toBe('agent-1')
  })

  it('shows a replayed helper reply as the guest name only', async () => {
    const sid = 'replay-1'
    seedMessages(sid, [{
      id: `${sid}_assistant`,
      role: 'assistant',
      content: 'Helper result',
      timestamp: new Date().toISOString(),
      status: 'done',
      agentId: 'agent-jim',
    }], { streaming: false, replaying: true })
    const bubble = assistantBubble(await mount())
    expect(await within(bubble).findByText('Jim')).toBeInTheDocument()
    expect(bubble.textContent).toContain('Helper result')
    expect(bubble.querySelector('.w-7')).toBeNull()
    expectNameOnly(bubble, 'Jim')
  })

  it('shows Idle for a resolved chat and does not keep a thinking indicator on the reply', async () => {
    const sid = 'idle-1'
    seedMessages(sid, [{
      id: `${sid}_assistant`,
      role: 'assistant',
      content: 'The answer',
      timestamp: new Date().toISOString(),
      status: 'done',
      agentId: 'agent-1',
    }], { streaming: false, replaying: false })
    const bubble = assistantBubble(await mount())
    expect(await within(bubble).findByText('Mia')).toBeInTheDocument()
    expect(bubble.textContent).toContain('The answer')
    expect(bubble.querySelector('.animate-bounce')).toBeNull()
    expect(within(bubble).getByText('Idle')).toBeInTheDocument()
    expectNameOnly(bubble, 'Mia')
  })

  it('keeps virtualized historical and separate live guest labels name-only with real viewport measurement', async () => {
    const height = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
    const width = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth')
    Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get: () => 800 })
    Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, get: () => 800 })
    vi.stubGlobal('ResizeObserver', ResizeObserverStub)
    const sid = 'virtual-names'
    const messages: ChatMessage[] = [
      { id: 'virtual-history', role: 'assistant', content: 'Historical guest answer', timestamp: '2026-10-08T09:00:00Z', status: 'done', agentId: 'agent-jim' },
      ...streamingPair(sid, 'agent-1'),
    ]
    seedMessages(sid, messages, { streaming: true, replaying: false })
    try {
      const container = await mount()
      await waitFor(() => expect(container.querySelector('[data-message-id="virtual-history"]')).not.toBeNull())
      const historical = container.querySelector('[data-message-id="virtual-history"]') as HTMLElement
      expect(historical.closest('[data-index]')).not.toBeNull()
      expectNameOnly(historical, 'Jim')
      const live = container.querySelector(`[data-message-id="${sid}_assistant"]`) as HTMLElement
      expectNameOnly(live, 'Mia')
      expectMarkMotion(live, 'thinking')
      expect(useSessionStore.getState().activeAgentId).toBe('agent-1')
    } finally {
      if (height) Object.defineProperty(HTMLElement.prototype, 'offsetHeight', height)
      if (width) Object.defineProperty(HTMLElement.prototype, 'offsetWidth', width)
    }
  })
})
