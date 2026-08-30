// D — "An empty assistant bubble renders before every reply."
//
// The chat store's optimistic assistant placeholder (store/chat.ts sendMessage)
// starts as content:'' / status:'streaming' the instant a message is sent, and
// that placeholder is the LAST message in the store, so VirtualizedMessageListInner
// routes it to the LIVE AssistantMessage() render (via real ThreadPrimitive.Messages),
// not the historical VirtualAssistantMessageRow path. Every other test file in this
// directory that touches ChatScreen fully mocks '@assistant-ui/react' (Messages: () =>
// null), which means the live AssistantMessage() render — where this bug actually
// lives — has never been exercised by a test. This file deliberately leaves
// '@assistant-ui/react' UNMOCKED and mounts ChatScreen inside a real
// AssistantRuntimeProvider (via the real useOmnipusRuntime hook, skipping
// OmnipusRuntimeProvider's WsLifecycle so no real WebSocket is attempted) so the
// assertions exercise the actual AssistantMessage() component, not a stand-in.
//
// BDD:
//   Given: the last assistant message is streaming (isStreaming:true) with
//          empty content, no tool calls, no media, no subagent spans
//   Then:  the bubble shows the thinking indicator and NOT a Copy button
//          (nothing has streamed in yet — there is nothing to copy)
//   Given: the same message now has streamed-in text
//   Then:  the bubble shows that text AND the Copy button

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, useMessagePartText } from '@assistant-ui/react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', ResizeObserverStub)
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}
// jsdom has no layout engine and doesn't implement scrollTo — the real (unmocked)
// ThreadPrimitive.Viewport's autoscroll effect calls it on a rAF callback that can
// fire after the test body returns, otherwise surfacing as an unhandled rejection.
if (typeof Element !== 'undefined' && !Element.prototype.scrollTo) {
  Element.prototype.scrollTo = function () {}
}

// '@assistant-ui/react' is intentionally NOT mocked in this file — see header.

// importOriginal: useSlashMenu now calls useChatAgents unconditionally (the
// "@" mention menu — src/hooks/useChatAgents.ts), which needs real
// `fetchWorkspaces`/`workspacesQueryKeys`/`isWorker` even though this file
// doesn't exercise mentions. The workspaces query stays disabled (no
// activeWorkspaceId set here), so `fetchWorkspaces` is never actually
// invoked — this just needs to exist so the module loads.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      { id: 'agent-1', name: 'Mia', color: '#123456', icon: null },
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
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./ActivityBar', () => ({ ActivityBar: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
// Composer Redesign (variant A1): this file targets the live AssistantMessage()
// bubble render, not the composer's picker/model/token sub-components — stub
// them to null so their workspaces/providers query plumbing doesn't need
// mocking here (this file uses a real, unmocked QueryClient).
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
// Real MarkdownText pulls in Shiki/Mermaid/KaTeX — far more than this test needs.
// Replace it with a minimal renderer built on the REAL useMessagePartText hook
// (unmocked '@assistant-ui/react') so the DOM still reflects the actual streamed
// text, just without the heavy markdown pipeline.
vi.mock('./markdown-text', () => ({
  MarkdownText: () => {
    const { text } = useMessagePartText()
    return React.createElement('span', { 'data-testid': 'assistant-text' }, text)
  },
}))

import { ChatScreen } from './ChatScreen'

const SID = 'sess_empty_bubble_test'

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

/** Seeds a user message followed by a streaming assistant placeholder. */
function seedStreamingAssistant(assistantContent: string): void {
  const userMsg: ChatMessage = {
    id: 'msg_user',
    role: 'user',
    content: 'hi',
    timestamp: new Date().toISOString(),
    status: 'done',
  }
  const streamingMsg: ChatMessage = {
    id: 'msg_assistant',
    role: 'assistant',
    content: assistantContent,
    timestamp: new Date().toISOString(),
    status: 'streaming',
    isStreaming: true,
  }
  const allMessages = [userMsg, streamingMsg]
  const bucket = makeBucketMessages(allMessages)
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
        spanByParentCallId: {},
        trimmedCount: 0,
      },
    },
    messages: allMessages,
    isStreaming: true,
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  useConnectionStore.setState({ connection: null, isConnected: true, connectionError: null })
}

/**
 * Seeds a user message followed by a TERMINAL (isStreaming:false,
 * status:'done') assistant message with empty content — the uncovered
 * variant of the D-fix bug (live UAT, 2026-08-26): the provider returned a
 * response the engine judged empty (pkg/agent/loop.go's `defaultResponse`
 * sentinel path), and the message finalized (`done` arrived) before that
 * fallback text was merged onto it. Unlike seedStreamingAssistant('') above
 * — where isStreaming:true and the message is still IN PROGRESS — this
 * message is DONE: not streaming, not interrupted, not a typed error, and
 * still holding nothing. The bug: showEmptyPlaceholder (pre-fix) required
 * isRunning, so a terminal-empty message fell through to the normal render
 * branch and showed an avatar + name + a bare "Copy message" button with no
 * content paragraph above it — a Copy affordance for literally nothing to
 * copy, and (unlike the streaming case) no thinking indicator either, since
 * nothing is actually in progress.
 *
 * Render path note: because this message is NOT streaming,
 * VirtualizedMessageListInner's `hasStreamingMessage` gate (ChatScreen.tsx)
 * is false, so — unlike seedStreamingAssistant's placeholder — it is never
 * routed through the live `ThreadPrimitive.Messages`/AssistantMessage()
 * path; it renders through the historical VirtualAssistantMessageRow
 * instead, exactly as it would in the real app once the store flips
 * isStreaming to false. The companion describe block below force-disables
 * `ResizeObserver` so ChatScreen falls back to PlainMessageList (see
 * VirtualizedMessageList's own feature-detection comment) rather than the
 * virtualizer, which computes zero visible rows under jsdom's absent
 * layout engine — PlainMessageList renders every message unconditionally,
 * through the same VirtualAssistantMessageRow component a real browser
 * would use once virtualization is active.
 */
function seedTerminalEmptyAssistant(): void {
  const userMsg: ChatMessage = {
    id: 'msg_user',
    role: 'user',
    content: 'hi',
    timestamp: new Date().toISOString(),
    status: 'done',
  }
  const terminalEmptyMsg: ChatMessage = {
    id: 'msg_assistant',
    role: 'assistant',
    content: '',
    timestamp: new Date().toISOString(),
    status: 'done',
    isStreaming: false,
  }
  const allMessages = [userMsg, terminalEmptyMsg]
  const bucket = makeBucketMessages(allMessages)
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
    messages: allMessages,
    isStreaming: false,
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  useConnectionStore.setState({ connection: null, isConnected: true, connectionError: null })
}

/**
 * Seeds a user message followed by a streaming assistant placeholder whose
 * ONLY content is a live (in-progress or just-finished) `delegate` tool
 * call — Fix 3 (2026-07-16): a `delegate` 'run' dispatch is hidden from the
 * thread by default (toolVisibility.ts), so this exercises the exact
 * "hidden delegation is the message's only content" ghost-bubble scenario
 * on the LIVE AssistantMessage() render path (item 8b/8c coverage —
 * SubagentSpansRenderer's sibling gate for tool-call parts, previously
 * untested since every OTHER ChatScreen suite mocks ThreadPrimitive.Messages
 * to null; this file is the one exception, see its header).
 */
function seedStreamingAssistantWithHiddenToolCall(toolCallStatus: 'running' | 'success'): void {
  const userMsg: ChatMessage = {
    id: 'msg_user',
    role: 'user',
    content: 'hi',
    timestamp: new Date().toISOString(),
    status: 'done',
  }
  const streamingMsg: ChatMessage = {
    id: 'msg_assistant',
    role: 'assistant',
    content: '',
    timestamp: new Date().toISOString(),
    status: 'streaming',
    isStreaming: true,
  }
  const allMessages = [userMsg, streamingMsg]
  const bucket = makeBucketMessages(allMessages)
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [SID]: {
        ...((s.sessionsById ?? {})[SID] ?? {}),
        ...bucket,
        isStreaming: true,
        isReplaying: false,
        replayCompletedForSession: SID,
        toolCalls: {
          tc_hidden_delegate: {
            id: 'tc_hidden_delegate',
            call_id: 'tc_hidden_delegate',
            tool: 'delegate',
            params: { action: 'run', async: true, target_agent_id: 'ray', task: 'do X' },
            status: toolCallStatus,
            ...(toolCallStatus === 'success' ? { result: { ok: true } } : {}),
          },
        },
        toolCallOrder: ['tc_hidden_delegate'],
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
    messages: allMessages,
    isStreaming: true,
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  useConnectionStore.setState({ connection: null, isConnected: true, connectionError: null })
}

describe('Fix 3 (2026-07-16): ghost bubble when the only live content is a hidden delegation', () => {
  it('a STILL-RUNNING hidden delegate call shows the thinking placeholder, not a bare Copy bubble', async () => {
    seedStreamingAssistantWithHiddenToolCall('running')

    await act(async () => {
      render(
        <Providers>
          <ChatScreen />
        </Providers>,
      )
    })

    const bubble = screen.getByTestId('assistant-message')
    expect(within(bubble).queryByLabelText('Copy message')).toBeNull()
    expect(
      within(bubble).getByText(
        /Thinking…|Composing response…|Processing your request…|Analyzing…|Generating…/,
      ),
    ).toBeInTheDocument()
  })

  it('a FINISHED (success) hidden delegate call, with nothing else in the message yet, still shows the thinking placeholder', async () => {
    seedStreamingAssistantWithHiddenToolCall('success')

    await act(async () => {
      render(
        <Providers>
          <ChatScreen />
        </Providers>,
      )
    })

    const bubble = screen.getByTestId('assistant-message')
    expect(within(bubble).queryByLabelText('Copy message')).toBeNull()
    expect(
      within(bubble).getByText(
        /Thinking…|Composing response…|Processing your request…|Analyzing…|Generating…/,
      ),
    ).toBeInTheDocument()
  })
})

describe('D: empty streaming assistant bubble (live AssistantMessage render)', () => {
  it('renders the thinking indicator, not an empty Copy-only bubble, while content is empty', async () => {
    seedStreamingAssistant('')

    await act(async () => {
      render(
        <Providers>
          <ChatScreen />
        </Providers>,
      )
    })

    const bubble = screen.getByTestId('assistant-message')
    // No Copy affordance while there's nothing to copy.
    expect(within(bubble).queryByText('Copy')).toBeNull()
    expect(within(bubble).queryByLabelText('Copy message')).toBeNull()
    // The thinking indicator (rotating status text) is shown instead.
    expect(
      within(bubble).getByText(
        /Thinking…|Composing response…|Processing your request…|Analyzing…|Generating…/,
      ),
    ).toBeInTheDocument()
  })

  it('renders the streamed text and the Copy button once content has arrived', async () => {
    seedStreamingAssistant('Hello there')

    await act(async () => {
      render(
        <Providers>
          <ChatScreen />
        </Providers>,
      )
    })

    const bubble = screen.getByTestId('assistant-message')
    expect(within(bubble).getByTestId('assistant-text').textContent).toBe('Hello there')
    expect(within(bubble).getByText('Copy')).toBeInTheDocument()
  })
})

describe('D2 (2026-08-26 live UAT): terminal-empty assistant bubble — the uncovered variant', () => {
  // BDD:
  //   Given: the last assistant message is DONE (isStreaming:false,
  //          status:'done') — not interrupted, not a typed error — with
  //          empty content, no tool calls, no media, no subagent spans
  //   Then:  the bubble shows neither the thinking indicator (nothing is
  //          in progress) NOR a Copy button (there is nothing to copy) —
  //          just the avatar and agent name, matching a bubble with
  //          nothing to show rather than offering a broken affordance
  //
  // This message is terminal (not streaming), so ChatScreen routes it
  // through VirtualAssistantMessageRow, not the live AssistantMessage()
  // path (see seedTerminalEmptyAssistant's doc comment). @tanstack/
  // react-virtual — the module-scope ResizeObserverStub's real consumer —
  // computes zero visible rows under jsdom (no layout engine), so this
  // block disables ResizeObserver for its own tests to force ChatScreen's
  // documented PlainMessageList fallback, which renders every message
  // unconditionally through the same row component a real, laid-out browser
  // would virtualize into view.
  const originalResizeObserver = globalThis.ResizeObserver
  beforeEach(() => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any -- test-only global deletion to trigger ChatScreen's documented feature-detection fallback
    delete (globalThis as any).ResizeObserver
  })
  afterEach(() => {
    globalThis.ResizeObserver = originalResizeObserver
  })

  it('renders no Copy button and no thinking indicator once a response has terminated empty', async () => {
    seedTerminalEmptyAssistant()

    await act(async () => {
      render(
        <Providers>
          <ChatScreen />
        </Providers>,
      )
    })

    const bubble = screen.getByTestId('assistant-message')
    // The defect: an empty bubble offering a Copy button that would copy
    // nothing.
    expect(within(bubble).queryByText('Copy')).toBeNull()
    expect(within(bubble).queryByLabelText('Copy message')).toBeNull()
    // Nothing is in progress — the thinking indicator must not show either
    // (that would misrepresent a finished turn as still running).
    expect(
      within(bubble).queryByText(
        /Thinking…|Composing response…|Processing your request…|Analyzing…|Generating…/,
      ),
    ).toBeNull()
    // The bubble still identifies its speaker (avatar + name) — this is a
    // real, finished assistant turn, not a message that failed to mount.
    expect(within(bubble).getByTestId('agent-label')).toBeInTheDocument()
  })
})
