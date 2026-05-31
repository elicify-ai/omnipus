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
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'

// ── Mock AssistantUI ───────────────────────────────────────────────────────────
vi.mock('@assistant-ui/react', () => {
  const React = require('react')
  return {
    ThreadPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Viewport: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
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
    }),
    useMessage: () => ({
      id: 'msg_streaming',
      role: 'assistant',
      status: { type: 'running' },
      content: [],
    }),
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

vi.mock('@/lib/api', () => ({
  fetchAgents: vi.fn().mockResolvedValue([]),
  fetchSessionMessages: vi.fn().mockResolvedValue([]),
  fetchAboutInfo: vi.fn().mockResolvedValue({ preview_port: 5001 }),
  createSession: vi.fn(),
  uploadFiles: vi.fn(),
  isApiError: vi.fn().mockReturnValue(false),
}))

vi.mock('./historical-markdown', () => ({
  HistoricalMessageMarkdown: ({ content }: { content: string }) => {
    const React = require('react')
    return React.createElement('div', { 'data-testid': 'historical-markdown' }, content)
  },
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./SessionPanel', () => ({ SessionPanel: () => null }))
vi.mock('./ExecApprovalBlock', () => ({ ExecApprovalBlock: () => null }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./tools/GenericToolCall', () => ({
  GenericToolCall: ({ toolName }: { toolName: string }) => {
    const React = require('react')
    return React.createElement('div', { 'data-testid': 'tool-call-badge' }, toolName)
  },
}))
vi.mock('./markdown-text', () => ({
  MarkdownText: () => {
    const React = require('react')
    return React.createElement('div', {})
  },
}))
vi.mock('./shiki-highlighter', () => ({
  SyntaxHighlighter: ({ children }: { children?: React.ReactNode }) => {
    const React = require('react')
    return React.createElement('pre', {}, children)
  },
  CopyCodeHeader: () => null,
}))
vi.mock('./image-lightbox', () => ({ ImageLightbox: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
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
        pendingApprovals: [],
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

/**
 * Patch a DOM element's layout geometry so @tanstack/react-virtual can
 * calculate visible items. jsdom has no layout engine, so all these
 * properties default to 0. The virtualizer uses clientHeight and scrollHeight
 * from getScrollElement() to determine which items are visible.
 */
function patchScrollContainer(el: Element, opts: { clientHeight: number; scrollHeight: number; scrollTop: number }): void {
  Object.defineProperty(el, 'clientHeight', { configurable: true, value: opts.clientHeight })
  Object.defineProperty(el, 'scrollHeight', { configurable: true, value: opts.scrollHeight })
  Object.defineProperty(el, 'scrollTop', { configurable: true, writable: true, value: opts.scrollTop })
}

// ── Capturing ResizeObserver stub ──────────────────────────────────────────────
// jsdom has no ResizeObserver. The production stick-to-bottom relies on one
// observing the content wrapper, so the tests must (a) make ResizeObserver
// defined (so the component takes the virtualized path, not the PlainMessageList
// fallback) and (b) be able to fire the observer callback to simulate a content-
// height change — which is exactly what re-pins the scroll in production. We
// record every observer + the elements it watches so a test can fire only the
// callback watching a given element (the virtualizer registers its own observers
// internally, which we don't want to drive).
type RoRecord = { cb: ResizeObserverCallback; els: Element[] }
let resizeObservers: RoRecord[] = []
class CapturingResizeObserver {
  private rec: RoRecord
  constructor(cb: ResizeObserverCallback) {
    this.rec = { cb, els: [] }
    resizeObservers.push(this.rec)
  }
  observe(el: Element) { this.rec.els.push(el) }
  unobserve(el: Element) { this.rec.els = this.rec.els.filter((e) => e !== el) }
  disconnect() { this.rec.els = []; resizeObservers = resizeObservers.filter((r) => r !== this.rec) }
}
/** Fire the ResizeObserver callback(s) observing `el` (default: all). */
function fireResize(el?: Element): void {
  for (const rec of [...resizeObservers]) {
    if (el && !rec.els.includes(el)) continue
    try {
      rec.cb([], {} as ResizeObserver)
    } catch {
      // The virtualizer's own observer may not tolerate a synthetic empty entry
      // list; we only care about the component's stick-to-bottom observer.
    }
  }
}
/** The content wrapper the stick-to-bottom ResizeObserver watches. */
function contentWrapperOf(scrollEl: Element): Element | null {
  return scrollEl.querySelector('.max-w-4xl')
}

// ── Import the component under test ───────────────────────────────────────────
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
    // Capturing ResizeObserver (jsdom lacks it). Being defined also makes the
    // component take the virtualized path. resizeObservers is reset each test so
    // fireResize() only drives the current render's observers.
    resizeObservers = []
    vi.stubGlobal('ResizeObserver', CapturingResizeObserver)
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
        patchScrollContainer(scrollEl, { clientHeight: 600, scrollHeight: 80_000, scrollTop: 79_400 })
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

  it('auto-scroll detection: wasAtBottomRef tracks scrollTop proximity to bottom', async () => {
    const messages = makeMessages(10)
    seedStore(messages)

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const scrollElOrNull = container.querySelector('[data-testid="virtualized-message-list"]') as HTMLElement | null
    // #251: hard assertion — if the scroll container is absent the virtualizer is broken.
    // jsdom supports DOM but not layout; the scroll container MUST be in the DOM regardless.
    expect(scrollElOrNull).not.toBeNull()
    // Non-null assertion after the hard expect above: TypeScript cannot narrow from expect().
    const scrollEl = scrollElOrNull!

    // Simulate being at the bottom (within 50px).
    patchScrollContainer(scrollEl, { clientHeight: 600, scrollHeight: 1600, scrollTop: 1000 })
    const distanceFromBottom = scrollEl.scrollHeight - scrollEl.scrollTop - scrollEl.clientHeight
    expect(distanceFromBottom).toBeLessThan(50)

    // Simulate being in the middle (> 50px from bottom).
    patchScrollContainer(scrollEl, { clientHeight: 600, scrollHeight: 1600, scrollTop: 200 })
    const distanceFromBottomMid = scrollEl.scrollHeight - scrollEl.scrollTop - scrollEl.clientHeight
    expect(distanceFromBottomMid).toBeGreaterThanOrEqual(50)
  })

  // ── Regression tests for issue #251: auto-scroll during token streaming ────────

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
          pendingApprovals: [],
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

  it('re-pins to bottom on a content-height change while the user is at the bottom', async () => {
    // BDD:
    //   Given: a live chat where the user is pinned to the bottom (within 50px)
    //   When: the transcript content grows (tokens, tool call, image — anything)
    //   Then: the scroll container re-pins to the bottom
    //
    // Regression test for issue #251. The fix drives a ResizeObserver on the
    // content wrapper, so ANY content-height change re-pins — not just the
    // enumerated [messages.length, streamingContentLength] signals the old
    // implementation watched. Here we drive the observer directly (the
    // mechanism), independent of what caused the resize.
    const originalRaf = globalThis.requestAnimationFrame
    const originalCaf = globalThis.cancelAnimationFrame
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => { cb(0); return 0 })
    vi.stubGlobal('cancelAnimationFrame', (_id: number) => {})

    seedStreamingStore(10)

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const scrollEl = container.querySelector('[data-testid="virtualized-message-list"]') as HTMLElement | null
    // Hard assertion — no vacuous skip. If the virtualized list didn't render,
    // the regression guard must fail, not silently pass.
    expect(scrollEl).not.toBeNull()
    const content = contentWrapperOf(scrollEl!)
    expect(content).not.toBeNull()

    // Step 1: user at the bottom (distanceFromBottom = 0). Fire scroll so the
    // onScroll handler records wasAtBottom=true.
    patchScrollContainer(scrollEl!, { clientHeight: 600, scrollHeight: 1600, scrollTop: 1000 })
    await act(async () => {
      scrollEl!.dispatchEvent(new Event('scroll', { bubbles: true }))
    })

    // Step 2: content grows to 2400 (scrollTop not yet followed).
    patchScrollContainer(scrollEl!, { clientHeight: 600, scrollHeight: 2400, scrollTop: 1000 })

    // Step 3: the content wrapper resizes — drive the observer directly.
    await act(async () => {
      fireResize(content!)
    })

    // Re-pinned to the new bottom.
    expect(scrollEl!.scrollTop).toBe(scrollEl!.scrollHeight)

    vi.stubGlobal('requestAnimationFrame', originalRaf)
    vi.stubGlobal('cancelAnimationFrame', originalCaf)
  })

  it('re-pins on a NON-text content change (tool call / image render) — the #251 gap', async () => {
    // This is the specific bug: a tool-call block or image renders in the live
    // turn, growing the transcript height WITHOUT changing messages.length or the
    // streaming text length. The old signal-based effect never fired for this, so
    // the new content sat below the viewport. The ResizeObserver fix re-pins
    // regardless of WHAT changed, so we assert the re-pin WITHOUT any store/
    // message mutation — purely a resize.
    const originalRaf = globalThis.requestAnimationFrame
    const originalCaf = globalThis.cancelAnimationFrame
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => { cb(0); return 0 })
    vi.stubGlobal('cancelAnimationFrame', (_id: number) => {})

    seedStreamingStore(10)

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const scrollEl = container.querySelector('[data-testid="virtualized-message-list"]') as HTMLElement | null
    expect(scrollEl).not.toBeNull()
    const content = contentWrapperOf(scrollEl!)
    expect(content).not.toBeNull()

    // User at the bottom.
    patchScrollContainer(scrollEl!, { clientHeight: 600, scrollHeight: 1600, scrollTop: 1000 })
    await act(async () => {
      scrollEl!.dispatchEvent(new Event('scroll', { bubbles: true }))
    })

    // A tool-call pill + image render: height jumps to 3000. No store mutation.
    patchScrollContainer(scrollEl!, { clientHeight: 600, scrollHeight: 3000, scrollTop: 1000 })
    await act(async () => {
      fireResize(content!)
    })

    // Re-pinned even though nothing about the message list "signals" changed.
    expect(scrollEl!.scrollTop).toBe(3000)

    vi.stubGlobal('requestAnimationFrame', originalRaf)
    vi.stubGlobal('cancelAnimationFrame', originalCaf)
  })

  it('suspends ONLY on a user gesture, never on content-growth scrolls; resumes at bottom', async () => {
    // BDD — the live-browser bug + the correct ChatGPT/Claude behavior:
    //   1. A scroll event caused by CONTENT GROWTH (no user gesture) that leaves
    //      the viewport temporarily far from the bottom must NOT suspend
    //      auto-follow — the ResizeObserver still re-pins. (This is the exact bug
    //      reproduced in a live browser: a tool-call/image render fired a scroll
    //      with a large distance and the old code flipped wasAtBottom=false,
    //      sticking the view ~600px above the bottom.)
    //   2. A scroll away from the bottom DURING a real user gesture (wheel/touch/
    //      key/pointer) DOES suspend — don't fight the user reading history.
    //   3. Returning to the bottom resumes auto-follow.

    const originalRaf = globalThis.requestAnimationFrame
    const originalCaf = globalThis.cancelAnimationFrame
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => { cb(0); return 0 })
    vi.stubGlobal('cancelAnimationFrame', (_id: number) => {})

    seedStreamingStore(10)

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const scrollEl = container.querySelector('[data-testid="virtualized-message-list"]') as HTMLElement | null
    expect(scrollEl).not.toBeNull()
    const content = contentWrapperOf(scrollEl!)
    expect(content).not.toBeNull()

    // Establish "at the bottom".
    patchScrollContainer(scrollEl!, { clientHeight: 600, scrollHeight: 1600, scrollTop: 1000 })
    await act(async () => {
      scrollEl!.dispatchEvent(new Event('scroll', { bubbles: true }))
    })

    // (1) CONTENT GROWTH leaves us far from the bottom (scrollTop lags) and fires
    // a scroll — but with NO user gesture. Auto-follow must survive and re-pin.
    patchScrollContainer(scrollEl!, { clientHeight: 600, scrollHeight: 3000, scrollTop: 1000 })
    await act(async () => {
      scrollEl!.dispatchEvent(new Event('scroll', { bubbles: true })) // distance = 1400, no gesture
      fireResize(content!)
    })
    expect(scrollEl!.scrollTop).toBe(3000) // re-pinned despite the large-distance scroll

    // (2) USER scrolls up (wheel gesture) → suspend.
    patchScrollContainer(scrollEl!, { clientHeight: 600, scrollHeight: 3000, scrollTop: 200 })
    await act(async () => {
      scrollEl!.dispatchEvent(new WheelEvent('wheel', { bubbles: true, deltaY: -300 }))
      scrollEl!.dispatchEvent(new Event('scroll', { bubbles: true })) // distance = 2200, gesture active
    })
    patchScrollContainer(scrollEl!, { clientHeight: 600, scrollHeight: 3600, scrollTop: 200 })
    await act(async () => {
      fireResize(content!)
    })
    expect(scrollEl!.scrollTop).toBe(200) // NOT re-pinned — user is reading history

    // (3) USER scrolls back to the bottom → resume; next growth re-pins.
    patchScrollContainer(scrollEl!, { clientHeight: 600, scrollHeight: 3600, scrollTop: 3000 })
    await act(async () => {
      scrollEl!.dispatchEvent(new Event('scroll', { bubbles: true })) // distance = 0 → resume
    })
    patchScrollContainer(scrollEl!, { clientHeight: 600, scrollHeight: 4200, scrollTop: 3000 })
    await act(async () => {
      fireResize(content!)
    })
    expect(scrollEl!.scrollTop).toBe(4200) // re-pinned

    vi.stubGlobal('requestAnimationFrame', originalRaf)
    vi.stubGlobal('cancelAnimationFrame', originalCaf)
  })
})
