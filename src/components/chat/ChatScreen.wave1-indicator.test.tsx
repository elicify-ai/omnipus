// FR-022 / FR-023. BDD-07.1, BDD-07.2, BDD-E06, BDD-E07.
// Oracles are the spec indicator table and the phrases already published in
// ThinkingIndicator — not a new phase vocabulary.
// Live path: real AssistantRuntimeProvider (same harness as
// ChatScreen.thinking-indicator-context.test.tsx).
// Plain / replay path: ResizeObserver removed so PlainMessageList renders
// VirtualAssistantMessageRow, including a streaming row.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, within } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, useMessagePartText } from '@assistant-ui/react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { useToolApprovalStore } from '@/store/toolApproval'

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

function streamingPair(sid: string, agentId: string): ChatMessage[] {
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

describe('live inline indicator', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', ResizeObserverStub)
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  })

  afterEach(() => {
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
  })

  it('names a guest reply as that guest and does not switch the chat owner', async () => {
    seedMessages('guest-1', streamingPair('guest-1', 'agent-jim'), { streaming: true, replaying: false })
    const bubble = assistantBubble(await mount())
    expect(await within(bubble).findByText('Jim')).toBeInTheDocument()
    expect(within(bubble).queryByText('Mia')).not.toBeInTheDocument()
    expect(useSessionStore.getState().activeAgentId).toBe('agent-1')
    expect(bubble.querySelector('.w-7')).toBeNull()
    expect(bubble.querySelector('[data-art]')?.getAttribute('data-art')).toBe('man')
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
  })

  it('shows a static Unavailable/reconnecting phrase when the gateway is reconnecting', async () => {
    seedMessages('disc-1', streamingPair('disc-1', 'agent-1'), { streaming: true, replaying: false })
    useConnectionStore.setState({ isConnected: false, reconnectPhase: 'reconnecting', reconnectAttempt: 1, connectionError: null, connection: null })
    const bubble = assistantBubble(await mount())
    expect(within(bubble).getByText('Unavailable/reconnecting')).toBeInTheDocument()
    expect(bubble.querySelector('.animate-bounce')).toBeNull()
  })

  it('draws no animation loop when reduced motion is on, and keeps the Thinking phrase', async () => {
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
  })
})

describe('plain, replay, and idle bubbles', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
    // Feature-detect is at render time. Undefined selects PlainMessageList.
    ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = undefined
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  })

  afterEach(() => {
    vi.stubGlobal('ResizeObserver', ResizeObserverStub)
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
  })
})
