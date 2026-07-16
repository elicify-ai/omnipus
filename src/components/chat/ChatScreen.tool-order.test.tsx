/**
 * ChatScreen.tool-order.test.tsx — DOM ordering regression net for the
 * "tool calls sink to the bottom when a stream finishes" bug.
 *
 * Root cause (see commit 5d6c1e0e for the store-side fix and
 * src/lib/messageParts.ts for the renderer-side fix): once a stream
 * finished (or a session replayed), VirtualAssistantMessageRow rendered
 * `message.content` as ONE text block, then ALL of `message.tool_calls`
 * after it — collapsing whatever interleaved order the assistant actually
 * streamed in. The store now stamps every baked tool call with `textOffset`
 * (PositionedToolCall, src/store/chat.ts); the renderer now threads that
 * through splitMessageParts (src/lib/messageParts.ts) to rebuild the true
 * order. This suite drives the REAL store via `handleFrame` (the same
 * sequence a live stream produces) and asserts the FINISHED message's DOM
 * still shows text/tool-call/text in the order they happened — this must
 * never regress silently.
 *
 * Style follows ChatScreen.plain-list-parity.test.tsx: full ChatScreen
 * render, ResizeObserver forced undefined (PlainMessageList fallback, which
 * — unlike the virtualized path — renders every message including a
 * still-streaming one, so no extra state juggling is needed), historical-
 * markdown and GenericToolCall mocked down to their STABLE contracts only
 * (data-testid="historical-markdown" / data-testid="tool-call-badge" +
 * data-tool — the two attributes the task brief guarantees are preserved
 * across the sibling agent's concurrent GenericToolCall/ToolCallBadge/
 * toolStatusConfig restyle). No assertion in this file touches any of
 * those three files' internal markup or classes.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage, PositionedToolCall } from '@/store/chat'
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

// Mocked down to the STABLE contract only: renders the raw content string
// (no markdown parsing) inside data-testid="historical-markdown", so
// assertions can find each interleaved text slice by its literal text.
vi.mock('./historical-markdown', () => ({
  HistoricalMessageMarkdown: ({ content }: { content: string }) =>
    React.createElement('div', { 'data-testid': 'historical-markdown' }, content),
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
// Mocked down to the STABLE contract the task brief guarantees:
// data-testid="tool-call-badge" + data-tool — GenericToolCall's actual
// markup/classes are being restyled concurrently by a sibling agent and are
// deliberately NOT asserted against here.
vi.mock('./tools/GenericToolCall', () => ({
  GenericToolCall: ({ toolName }: { toolName: string }) =>
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

const SID = 'test-session-tool-order'

// ── DOM-order assertion helpers ──────────────────────────────────────────────

/** Asserts `a` precedes `b` in document order, via Node.compareDocumentPosition. */
function expectBefore(a: Element, b: Element): void {
  const position = a.compareDocumentPosition(b)
  expect(
    (position & Node.DOCUMENT_POSITION_FOLLOWING) !== 0,
    `expected ${a.outerHTML} to precede ${b.outerHTML} in document order`,
  ).toBe(true)
}

/** All interleaved text/tool-call parts for the rendered message, in DOM order. */
function orderedPartNodes(container: HTMLElement): Element[] {
  return Array.from(
    container.querySelectorAll('[data-testid="historical-markdown"], [data-testid="tool-call-badge"]'),
  )
}

// ── Bucket-seeding helper (for the offset-less fallback case, which needs a
// hand-built PositionedToolCall with no `textOffset` — not reachable via a
// normal handleFrame sequence) ────────────────────────────────────────────────

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

beforeEach(() => {
  useConnectionStore.setState({
    isConnected: true,
    liteMode: false,
    reconnectPhase: null,
    reconnectAttempt: 0,
    connectionError: null,
    connection: null,
  })
  // Force the PlainMessageList fallback (as plain-list-parity does) so a
  // still-mid-merge or already-finished message renders through
  // VirtualAssistantMessageRow without needing to fake element geometry for
  // the virtualizer.
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

describe('ChatScreen — tool-call/text DOM ordering (interleaving regression net)', () => {
  it('interleaves a single tool call between two streamed text segments (the core regression case)', async () => {
    // Exact sequence a live stream produces: token -> tool_call_start ->
    // tool_call_result -> token -> done. Before the fix, the finished
    // bubble would show "Let me check. The answer is 42." as ONE block
    // with the badge stranded after it.
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'Let me check. ', session_id: SID })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc1',
        tool: 'web_search',
        params: { query: 'the answer' },
        session_id: SID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc1',
        tool: 'web_search',
        result: { hits: [] },
        status: 'success',
        session_id: SID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'token', content: 'The answer is 42.', session_id: SID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: SID })
    })

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const nodes = orderedPartNodes(container)
    expect(nodes).toHaveLength(3)
    expect(nodes[0]).toHaveAttribute('data-testid', 'historical-markdown')
    expect(nodes[0].textContent).toContain('Let me check.')
    expect(nodes[1]).toHaveAttribute('data-testid', 'tool-call-badge')
    expect(nodes[1]).toHaveAttribute('data-tool', 'web_search')
    expect(nodes[2]).toHaveAttribute('data-testid', 'historical-markdown')
    expect(nodes[2].textContent).toContain('The answer is 42.')

    // Explicit compareDocumentPosition check per the load-bearing assertion
    // this suite exists to pin.
    expectBefore(nodes[0], nodes[1])
    expectBefore(nodes[1], nodes[2])
  })

  it('interleaves two tool calls with three distinct text segments, in true stream order', async () => {
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'A', session_id: SID }) })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'tool_call_start', call_id: 'tc1', tool: 'tool_a', params: {}, session_id: SID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'tool_call_result', call_id: 'tc1', tool: 'tool_a', result: 1, status: 'success', session_id: SID })
    })
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'B', session_id: SID }) })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'tool_call_start', call_id: 'tc2', tool: 'tool_b', params: {}, session_id: SID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'tool_call_result', call_id: 'tc2', tool: 'tool_b', result: 2, status: 'success', session_id: SID })
    })
    act(() => { useChatStore.getState().handleFrame({ type: 'token', content: 'C', session_id: SID }) })
    act(() => { useChatStore.getState().handleFrame({ type: 'done', session_id: SID }) })

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const nodes = orderedPartNodes(container)
    expect(nodes).toHaveLength(5)
    expect(nodes[0].textContent).toBe('A')
    expect(nodes[1]).toHaveAttribute('data-tool', 'tool_a')
    expect(nodes[2].textContent).toContain('B')
    expect(nodes[3]).toHaveAttribute('data-tool', 'tool_b')
    expect(nodes[4].textContent).toContain('C')

    for (let i = 0; i < nodes.length - 1; i++) expectBefore(nodes[i], nodes[i + 1])
  })

  it('a tool call with no recorded textOffset (reconnect edge) falls back to rendering after all text — pins the legacy fallback', async () => {
    // Not reachable via a normal handleFrame sequence (every live bake path
    // now stamps an offset) — mirrors the store's own reconnect-guard
    // fixture (chat.tool-call-offset.test.ts) by hand-building a
    // PositionedToolCall with no `textOffset` key at all.
    const assistantMsg: ChatMessage = {
      id: 'msg_reconnect',
      role: 'assistant',
      content: 'reconnected text',
      timestamp: new Date().toISOString(),
      status: 'done',
      tool_calls: [
        { id: 'tc_orphan', tool: 'shell', params: {}, status: 'success' } as PositionedToolCall,
      ],
    }
    seedBucket([assistantMsg])

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const nodes = orderedPartNodes(container)
    expect(nodes).toHaveLength(2)
    expect(nodes[0]).toHaveAttribute('data-testid', 'historical-markdown')
    expect(nodes[0].textContent).toContain('reconnected text')
    expect(nodes[1]).toHaveAttribute('data-testid', 'tool-call-badge')
    expect(nodes[1]).toHaveAttribute('data-tool', 'shell')
    expectBefore(nodes[0], nodes[1])
  })

  it('a WS-replay same-turn merge (segment A -> tool call -> segment B) renders A, tool, B in that DOM order', async () => {
    // Mirrors chat.tool-call-offset.test.ts's WS-replay same-turn merge
    // fixture: two replay_message entries sharing turn_id/agent_id coalesce
    // into one bubble, with the tool call started in between.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'A',
        id: 'entry-1',
        agent_id: 'agent-ray',
        turn_id: 'turn-interleaved-1',
        session_id: SID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_shell',
        tool: 'shell',
        params: { cmd: 'echo hi' },
        agent_id: 'agent-ray',
        session_id: SID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_shell',
        tool: 'shell',
        result: { stdout: 'hi\n' },
        status: 'success',
        duration_ms: 42,
        session_id: SID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'B',
        id: 'entry-2',
        agent_id: 'agent-ray',
        turn_id: 'turn-interleaved-1',
        session_id: SID,
      })
    })

    const assistantMsgs = useChatStore.getState().messages.filter((m) => m.role === 'assistant')
    expect(assistantMsgs).toHaveLength(1)
    expect(assistantMsgs[0].content).toBe('A\n\nB')

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const nodes = orderedPartNodes(container)
    expect(nodes).toHaveLength(3)
    expect(nodes[0].textContent).toBe('A')
    expect(nodes[1]).toHaveAttribute('data-tool', 'shell')
    expect(nodes[2].textContent).toContain('B')
    expectBefore(nodes[0], nodes[1])
    expectBefore(nodes[1], nodes[2])
  })
})
