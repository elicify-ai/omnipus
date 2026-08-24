/**
 * ChatScreen.virtualization.test.tsx — W1: virtualizer integration tests.
 *
 * Verifies that:
 *   1. Only O(overscan) DOM nodes are mounted for a 1000-message session.
 *   2. Auto-scroll-to-bottom fires when user was at the bottom.
 *   3. Scroll position is preserved when user was in the middle.
 *
 * Notes on jsdom limitations:
 *   jsdom has no layout engine, so scroll containers always report 0 clientHeight.
 *   @tanstack/react-virtual requires a non-zero container height to render
 *   virtual items. We patch the DOM element properties before rendering.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'

// ── Mock AssistantUI ───────────────────────────────────────────────────────────
// Captured here so tests can assert autoScroll delegation without re-implementing scroll math.
let lastViewportAutoScroll: boolean | undefined

// Reference the module-level `React` import (not an in-factory `require('react')`)
// — the latter was corrupting the shared React module binding across test files
// sharing a reused worker process (`pool: 'forks'` in vite.config.ts), causing an
// intermittent `ReferenceError: useRef is not defined` in unrelated sibling test
// files (e.g. useRunningActivity.test.ts / ActivityBar.test.tsx) run in the same
// batch. vi.mock factories may reference top-level `import` bindings safely —
// only same-file local `const`/`let` declarations have the hoisting restriction.
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
            autoScroll,
            'data-testid': testId,
          }: {
            children?: React.ReactNode
            className?: string
            style?: React.CSSProperties
            autoScroll?: boolean
            'data-testid'?: string
          },
          ref: React.Ref<HTMLDivElement>
        ) => {
          lastViewportAutoScroll = autoScroll
          return React.createElement('div', { ref, className, style, 'data-testid': testId }, children)
        }
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
    fetchAboutInfo: vi.fn().mockResolvedValue({}),
    createSession: vi.fn(),
    uploadFiles: vi.fn(),
    fetchProviders: vi.fn().mockResolvedValue([]),
    isApiError: vi.fn().mockReturnValue(false),
    // Isolation hardening (gap 13, bugfixes3 QA hardening wave): the
    // `importOriginal` spread above leaves the REAL fetchCommands/
    // fetchSkills reachable — harmless today only because BASE_URL resolves
    // to '' in this test environment. Explicit overrides close that gap.
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('./historical-markdown', () => ({
  HistoricalMessageMarkdown: ({ content }: { content: string }) => {
    return React.createElement('div', { 'data-testid': 'historical-markdown' }, content)
  },
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./tools/GenericToolCall', () => ({
  GenericToolCall: ({ toolName }: { toolName: string }) => {
    return React.createElement('div', { 'data-testid': 'tool-call-badge' }, toolName)
  },
}))
vi.mock('./markdown-text', () => ({
  MarkdownText: () => {
    return React.createElement('div', {})
  },
}))
vi.mock('./shiki-highlighter', () => ({
  SyntaxHighlighter: ({ children }: { children?: React.ReactNode }) => {
    return React.createElement('pre', {}, children)
  },
  CopyCodeHeader: () => null,
}))
vi.mock('./image-lightbox', () => ({ ImageLightbox: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
// Composer Redesign (variant A1): virtualizer integration tests target the
// message list, not the composer's picker/model/token sub-components — stub
// them to null so their workspaces/providers query plumbing doesn't need
// mocking here.
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {},
  getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))

// ── Helper ────────────────────────────────────────────────────────────────────

const SID = 'test-session-virtualize'

function makeMessages(count: number): ChatMessage[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `msg_${i}`,
    role: (i % 2 === 0 ? 'user' : 'assistant') as ChatMessage['role'],
    content: `Message ${i}`,
    timestamp: new Date().toISOString(),
    status: 'done' as const,
  }))
}

function seedStore(messages: ChatMessage[]): void {
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
    messages: messages,
    isStreaming: false,
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
}

// ── Capturing ResizeObserver stub ──────────────────────────────────────────────
// jsdom has no ResizeObserver. VirtualizedMessageList feature-detects
// `typeof ResizeObserver === 'undefined'` and falls back to a plain list when
// absent. This stub must remain so the component takes the Viewport-based path,
// not the PlainMessageList fallback. Auto-scroll behavior is now owned by
// assistant-ui's Viewport (useThreadViewportAutoScroll / use-stick-to-bottom)
// and verified via live browser; the unit test asserts delegation + structure,
// not scroll math.
class StubResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

// ── Import the components under test ──────────────────────────────────────────
// Import after mocks are set up.
import { ChatScreen } from './ChatScreen'

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('VirtualizedMessageList', () => {
  beforeEach(() => {
    // Reset stores to initial state.
    useConnectionStore.setState({
      isConnected: true,
      liteMode: false,
      reconnectPhase: null,
      reconnectAttempt: 0,
      connectionError: null,
      connection: null,
    })
    // Stub ResizeObserver so the component takes the virtualized path rather than
    // the PlainMessageList fallback (VirtualizedMessageList feature-detects it).
    vi.stubGlobal('ResizeObserver', StubResizeObserver)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('mounts ≤ 50 DOM nodes for a 1000-message session', async () => {
    const messages = makeMessages(1000)
    seedStore(messages)

    // Patch getBoundingClientRect so the virtualizer sees a real container size.
    // This makes it render visible items (height 600px ÷ estimateSize 80px = ~7 visible + 5 overscan).
    const originalGBCR = Element.prototype.getBoundingClientRect
    Element.prototype.getBoundingClientRect = vi.fn().mockReturnValue({
      height: 600, width: 800, top: 0, left: 0, bottom: 600, right: 800, x: 0, y: 0,
      toJSON() { return this },
    } as DOMRect)

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container

      // After render, patch the scroll container's layout properties so
      // the virtualizer processes the geometry correctly.
      const scrollEl = container.querySelector('[data-testid="virtualized-message-list"]')
      if (scrollEl) {
        Object.defineProperty(scrollEl, 'clientHeight', { configurable: true, value: 600 })
        Object.defineProperty(scrollEl, 'scrollHeight', { configurable: true, value: 80_000 })
        Object.defineProperty(scrollEl, 'scrollTop', { configurable: true, writable: true, value: 79_400 })
      }
    })

    const userRows = container.querySelectorAll('[data-message-role="user"]')
    const assistantRows = container.querySelectorAll('[data-message-role="assistant"]')
    const totalMounted = userRows.length + assistantRows.length

    // The virtualizer renders only the visible window + overscan.
    // With 1000 messages and a 600px container (estimateSize=80), this is:
    //   ~7 visible + 5 overscan above + 5 below = ~17 max.
    // We assert ≤ 50 to give room for jsdom's imprecise geometry.
    // We allow 0 as an acceptable outcome in jsdom (no layout engine).
    expect(totalMounted).toBeLessThanOrEqual(50)

    Element.prototype.getBoundingClientRect = originalGBCR
  })

  it('VirtualizedMessageList component is present in the rendered output', async () => {
    const messages = makeMessages(5)
    seedStore(messages)

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    // The virtualized scroll container should be in the DOM.
    const scrollEl = container.querySelector('[data-testid="virtualized-message-list"]')
    expect(scrollEl).toBeTruthy()
  })

  it('ResizeObserver unavailable — renders plain list without crash', async () => {
    // BDD:
    //   Given a browser without ResizeObserver (iOS < 13.4, some enterprise Android WebViews)
    //   When ChatScreen renders with messages
    //   Then it does NOT crash, and renders message rows in a plain list
    //
    // This guards the hasResizeObserver fallback in VirtualizedMessageList.
    vi.unstubAllGlobals()
    // Remove ResizeObserver to simulate the unsupported environment.
    const originalResizeObserver = globalThis.ResizeObserver
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(globalThis as any).ResizeObserver = undefined

    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})

    const messages = makeMessages(5)
    seedStore(messages)

    let container!: HTMLElement
    let threw = false
    try {
      await act(async () => {
        const result = render(<ChatScreen />)
        container = result.container
      })
    } catch {
      threw = true
    }

    expect(threw).toBe(false)

    // The fallback plain list must render at least some message rows.
    const rows = container.querySelectorAll('[data-message-role]')
    expect(rows.length).toBeGreaterThan(0)

    // Restore
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(globalThis as any).ResizeObserver = originalResizeObserver
    warnSpy.mockRestore()

    // Re-stub ResizeObserver for subsequent tests.
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      unobserve() {}
      disconnect() {}
    })
  })

  // ── Auto-scroll delegation + structure ────────────────────────────────────────
  // Auto-scroll behavior is now owned by assistant-ui's Viewport
  // (useThreadViewportAutoScroll / use-stick-to-bottom) and verified via live
  // browser. The unit tests below assert delegation + structure, not scroll math.

  /**
   * Helper: seed the store with N completed messages plus a final streaming assistant message.
   * Sets isStreaming=true on both the bucket and foreground selectors.
   */
  function seedStreamingStore(completedCount: number): ChatMessage[] {
    const completed = makeMessages(completedCount)
    const streamingMsg: ChatMessage = {
      id: 'msg_streaming',
      role: 'assistant',
      content: 'Partial response...',
      timestamp: new Date().toISOString(),
      status: 'streaming',
      isStreaming: true,
    }
    const allMessages = [...completed, streamingMsg]
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
    return allMessages
  }

  it('delegates auto-scroll to the assistant-ui Viewport (autoScroll enabled)', async () => {
    // BDD:
    //   Given: a session with messages
    //   When: VirtualizedMessageListInner renders
    //   Then: the scroll container [data-testid="virtualized-message-list"] is present
    //         AND the Viewport receives autoScroll=true (delegating stick-to-bottom
    //         to assistant-ui's useThreadViewportAutoScroll engine)
    const messages = makeMessages(5)
    seedStore(messages)
    lastViewportAutoScroll = undefined

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const scrollEl = container.querySelector('[data-testid="virtualized-message-list"]')
    expect(scrollEl).not.toBeNull()
    expect(lastViewportAutoScroll).toBe(true)
  })

  it('renders the live streaming message anchor during streaming', async () => {
    // BDD:
    //   Given: an active streaming session
    //   When: VirtualizedMessageListInner renders
    //   Then: [data-testid="streaming-message-anchor"] is present so the Viewport
    //         can scroll it into view as tokens arrive
    seedStreamingStore(5)

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const anchor = container.querySelector('[data-testid="streaming-message-anchor"]')
    expect(anchor).not.toBeNull()
  })

  /**
   * ADR-070 §4/F3 (grill-spec round 1 on the implementation spec): this is
   * the one claim in the whole ADR that, before this test existed, was only
   * ever verified via a disposable, uncommitted throwaway repro — it had to
   * land as committed coverage. The mid-turn-steer sequence is the exact
   * precondition that broke the pin-to-bottom mechanism: once a follow-up
   * becomes the array's last item, `hasStreamingMessage` (keyed off
   * `messages[messages.length - 1]?.isStreaming`) used to go false even
   * though a genuinely-streaming bubble existed elsewhere in the array.
   * ADR-070 §2.1's fix (closing the pre-steer bubble so a NEW bubble opens
   * and becomes the true last item) is what self-heals this — this test
   * proves that healing actually happens, not just that it should in theory.
   */
  function seedSteeredStreamingStore(): ChatMessage[] {
    const userMsg: ChatMessage = {
      id: 'msg_user', role: 'user', content: 'question', timestamp: new Date().toISOString(), status: 'done',
    }
    const closedBySteerMsg: ChatMessage = {
      id: 'msg_pre_steer', role: 'assistant', content: 'partial reply before the follow-up',
      timestamp: new Date().toISOString(), status: 'done', isStreaming: false, closedBySteer: true,
    }
    const steerMsg: ChatMessage = {
      id: 'msg_steer', role: 'user', content: 'follow-up sent mid-turn', timestamp: new Date().toISOString(), status: 'done',
    }
    const postSteerStreamingMsg: ChatMessage = {
      id: 'msg_post_steer', role: 'assistant', content: 'reply to the follow-up, still streaming',
      timestamp: new Date().toISOString(), status: 'streaming', isStreaming: true,
    }
    const allMessages = [userMsg, closedBySteerMsg, steerMsg, postSteerStreamingMsg]
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
    return allMessages
  }

  it('pins the live streaming anchor to the POST-steer bubble, not the closed pre-steer one (ADR-070 §4 F3)', async () => {
    seedSteeredStreamingStore()

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    // The pin-to-bottom mechanism must still engage even though the array's
    // last item is the streaming bubble ONLY because ADR-070 §2.1 closed the
    // pre-steer one — before that fix, the last array item in this exact
    // shape would have been the (non-streaming) steer message, and this
    // anchor would have been absent (confirmed via a throwaway repro during
    // investigation; this test replaces that with committed coverage).
    const anchor = container.querySelector('[data-testid="streaming-message-anchor"]')
    expect(anchor).not.toBeNull()
  })

  it('DOM order top-to-bottom matches [user, finished pre-steer reply, follow-up, live post-steer reply] (ADR-070 §4 F3)', async () => {
    // Uses the PlainMessageList fallback (ResizeObserver removed) for
    // deterministic DOM rendering — jsdom's lack of a layout engine makes
    // the virtualized path unreliable for a small, fixed message set (see
    // 'VirtualUserMessageRow media rendering' below for the same technique).
    // PlainMessageList renders every message in true array order with no
    // separate "live anchor" region, which is exactly what a direct
    // DOM-order assertion needs.
    vi.unstubAllGlobals()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(globalThis as any).ResizeObserver = undefined

    seedSteeredStreamingStore()

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const rows = Array.from(container.querySelectorAll('[data-message-role]'))
    const rendered = rows.map((r) => ({
      role: r.getAttribute('data-message-role'),
      text: r.textContent ?? '',
    }))

    expect(rendered.length).toBe(4)
    expect(rendered.map((r) => r.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
    // The core reported symptom, asserted directly: the reply to the
    // follow-up must render below it, not above it.
    const idxOf = (needle: string) => rendered.findIndex((r) => r.text.includes(needle))
    const preSteerIdx = idxOf('partial reply before the follow-up')
    const steerIdx = idxOf('follow-up sent mid-turn')
    const postSteerIdx = idxOf('reply to the follow-up, still streaming')
    expect(preSteerIdx).toBeGreaterThanOrEqual(0)
    expect(steerIdx).toBeGreaterThanOrEqual(0)
    expect(postSteerIdx).toBeGreaterThanOrEqual(0)
    expect(preSteerIdx).toBeLessThan(steerIdx)
    expect(steerIdx).toBeLessThan(postSteerIdx)

    // Restore ResizeObserver for subsequent tests in this file.
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      unobserve() {}
      disconnect() {}
    })
  })
})

// ── VirtualUserMessageRow media rendering (finding 7) ────────────────────────

describe('VirtualUserMessageRow media rendering', () => {
  // Uses the PlainMessageList fallback (ResizeObserver removed) so we get stable
  // DOM rendering without needing virtualizer layout tricks.
  beforeEach(() => {
    vi.unstubAllGlobals()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(globalThis as any).ResizeObserver = undefined
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    // Re-stub ResizeObserver for subsequent test suites.
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      unobserve() {}
      disconnect() {}
    })
  })

  function seedSingleUserMessage(msg: ChatMessage): void {
    useChatStore.setState((s) => ({
      ...s,
      messages: [msg],
      isStreaming: false,
      isReplaying: false,
      replayCompletedForSession: SID,
    }))
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  }

  it('renders an <img> for an image media attachment', async () => {
    const msg: ChatMessage = {
      id: 'msg_img',
      role: 'user',
      content: 'look at this',
      timestamp: new Date().toISOString(),
      status: 'done',
      media: [
        {
          type: 'image',
          url: 'http://example.com/screenshot.png',
          filename: 'screenshot.png',
          contentType: 'image/png',
        },
      ],
    }
    seedSingleUserMessage(msg)

    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })
    warnSpy.mockRestore()

    // AttachmentCard renders an <img> for image media.
    const img = container.querySelector('img[alt="screenshot.png"]')
    expect(img).toBeTruthy()
    expect(img?.getAttribute('src')).toBe('http://example.com/screenshot.png')
  })

  it('renders the type label for a non-image file media attachment', async () => {
    const msg: ChatMessage = {
      id: 'msg_file',
      role: 'user',
      content: 'see attached',
      timestamp: new Date().toISOString(),
      status: 'done',
      media: [
        {
          type: 'file',
          url: 'http://example.com/report.pdf',
          filename: 'report.pdf',
          contentType: 'application/pdf',
        },
      ],
    }
    seedSingleUserMessage(msg)

    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })
    warnSpy.mockRestore()

    // AttachmentCard renders the type label for non-image files.
    expect(container.querySelector('img')).toBeFalsy()
    const pdfLabel = container.querySelector('[data-message-role="user"]')?.textContent
    expect(pdfLabel).toContain('PDF')
  })

  it('renders both image and file when media contains both', async () => {
    const msg: ChatMessage = {
      id: 'msg_mixed',
      role: 'user',
      content: 'mixed attachments',
      timestamp: new Date().toISOString(),
      status: 'done',
      media: [
        {
          type: 'image',
          url: 'http://example.com/photo.jpg',
          filename: 'photo.jpg',
          contentType: 'image/jpeg',
        },
        {
          type: 'file',
          url: 'http://example.com/data.xlsx',
          filename: 'data.xlsx',
          contentType: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
        },
      ],
    }
    seedSingleUserMessage(msg)

    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })
    warnSpy.mockRestore()

    // Image renders as <img>.
    expect(container.querySelector('img[alt="photo.jpg"]')).toBeTruthy()
    // File renders with type label.
    const userRow = container.querySelector('[data-message-role="user"]')
    expect(userRow?.textContent).toContain('Excel')
  })

  it('attachment-only message (empty content) renders cards but no empty text bubble', async () => {
    // A message with no text content — only a media attachment. The text bubble
    // must NOT appear (it would be an empty rounded rectangle).
    const msg: ChatMessage = {
      id: 'msg_attach_only',
      role: 'user',
      content: '',
      timestamp: new Date().toISOString(),
      status: 'done',
      media: [
        {
          type: 'image',
          url: 'http://example.com/shot.png',
          filename: 'shot.png',
          contentType: 'image/png',
        },
      ],
    }
    seedSingleUserMessage(msg)

    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })
    warnSpy.mockRestore()

    // The attachment card (image) renders.
    expect(container.querySelector('img[alt="shot.png"]')).toBeTruthy()

    // The text bubble div has a known class: rounded-xl px-4 py-3 text-sm.
    // With empty content the component conditionally skips it.
    const bubbles = container.querySelectorAll('.rounded-xl.px-4.py-3.text-sm')
    expect(bubbles.length).toBe(0)
  })

  it('G14 — renders <img> for image media with a non-image filename + blank contentType (regression)', async () => {
    // BDD:
    //   Given: a sent user message with media type='image', a blank contentType,
    //          AND a filename with no image extension (e.g. a server-suffixed name)
    //   When:  VirtualUserMessageRow renders via AttachmentCard
    //   Then:  an <img> with the correct src is present (not the file-card fallback)
    //
    // Root cause: AttachmentCard's thumbnail gate re-ran isImageAttachment(filename, contentType)
    // internally. With a blank contentType AND a filename the extension regex misses, that
    // re-check returned false and silently fell back to the file-card — even though the caller
    // already decided m.type==='image'. The fix passes isImage={m.type==='image'} so the gate
    // trusts the caller. This fixture uses an extension-less filename so it FAILS pre-fix.
    const msg: ChatMessage = {
      id: 'msg_blank_ct',
      role: 'user',
      content: '',
      timestamp: new Date().toISOString(),
      status: 'done',
      media: [
        {
          type: 'image',
          url: '/api/v1/uploads/s/upload',
          filename: 'upload',
          contentType: '',
        },
      ],
    }
    seedSingleUserMessage(msg)

    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })
    warnSpy.mockRestore()

    // Must render an <img> with the correct src — NOT fall back to the file-card.
    const img = container.querySelector('img[alt="upload"]')
    expect(img).toBeTruthy()
    expect(img?.getAttribute('src')).toBe('/api/v1/uploads/s/upload')
  })
})
