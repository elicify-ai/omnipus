// Slash Stop and current-chat redirect: preserve client interception,
// exact generated frame shape, streaming availability and no side effects.
//
// Founder 2026-10-06 (permission to replace helper-only refusal assertions):
// "/stop in chat needs to work like one escape or one stop click,
// /stop-redirect in the chat does not redirect a helper it redirects the
// chat itself not their helper". The follow-up explicitly requires a root
// chat's existing RedirectFrame to be sent; backend acceptance is tested by
// a separate backend owner, not mocked into a frontend pass here.
//
// Oracle: founder ruling for /stop activation and any-chat redirect;
// existing D9 client-delivery/available-while-streaming/argument rules;
// generated RedirectFrame {type:'redirect', session_id, instruction} with
// no scope or other extra properties. /cancel remains immediate tree and
// /steer remains absent. Expected values never come from observing output.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import type { ComposerRuntime } from '@assistant-ui/react'
import type { RedirectFrame } from '@/lib/api/generated/asyncapi-types'
import { useSlashMenu } from './useSlashMenu'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'

// Command list as the amended contract serves it: /stop client-delivered and
// streaming-available (§3.1), /stop-redirect client-delivered (RedirectFrame
// interception) and streaming-available (the CORRECTED values — the superseded
// pack pinned 'agent'/false on a transport that cannot execute mid-stream),
// with an instruction hint (D9: "/stop-redirect <instruction>").
const stopPackCommands = [
  { name: 'new', label: '/new', description: 'Start a new conversation', delivery: 'client', available_while_streaming: false },
  { name: 'cancel', label: '/cancel', description: 'Stop all — this session and its helpers', delivery: 'client', available_while_streaming: true },
  { name: 'stop', label: '/stop', description: 'Stop this session\'s current turn', delivery: 'client', available_while_streaming: true },
  { name: 'stop-redirect', label: '/stop-redirect', description: 'Stop this helper and continue with a new instruction', delivery: 'client', available_while_streaming: true, argument_hint: '<instruction>' },
]


vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[]; enabled?: boolean }) => {
      if (opts.enabled === false) return { data: [], isError: false, isLoading: false, refetch: vi.fn() }
      const key = opts.queryKey
      if (Array.isArray(key) && key[0] === 'commands') {
        return { data: stopPackCommands, isError: false, isLoading: false, refetch: vi.fn() }
      }
      // skills / agents / chatAgents — the redirect tests never match these.
      return { data: [], isError: false, isLoading: false, refetch: vi.fn() }
    },
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
  }
})

// Expected guidance is specified here before production changes, not imported
// from the implementation. The founder removed helper-only redirect wording.
const MISSING_SESSION_TEXT =
  '`/stop-redirect` could not resolve the current chat — no active session is attached. Re-open the chat and try again.'
const USAGE_TEXT =
  'Usage: `/stop-redirect <instruction>` — stops this chat\'s current turn and continues it with your new instruction. The instruction text is required; whitespace alone is not an instruction.'

function makeComposerRuntime(text = '') {
  return {
    getState: () => ({ text }),
    setText: vi.fn(),
    addAttachment: vi.fn(),
    subscribe: vi.fn(() => vi.fn()),
    send: vi.fn(),
  } as unknown as ComposerRuntime & {
    setText: ReturnType<typeof vi.fn>
    send: ReturnType<typeof vi.fn>
  }
}

function baseParams(overrides: Partial<Parameters<typeof useSlashMenu>[0]> = {}): Parameters<typeof useSlashMenu>[0] {
  return {
    isStreaming: false,
    isReplaying: false,
    inputEnabled: true,
    composerRuntime: makeComposerRuntime(),
    appendMessage: vi.fn(),
    startNewSession: vi.fn(),
    activateStop: vi.fn(),
    cancelIfStreaming: vi.fn(),
    sendRedirectFrame: vi.fn(),
    ...overrides,
  }
}

beforeEach(() => {
  act(() => {
    useUiStore.setState({
      modelSelectorOpen: false,
      agentSelectorOpen: false,
      searchModalOpen: false,
      searchModalMode: 'sessions',
      searchModalWorkspaceFilter: null,
    })
  })
  act(() => {
    useSessionStore.setState({
      activeAgentId: null,
      activeSessionId: null,
      activeAgentType: null,
      attachedSessionType: null,
      agentSelectionSource: 'auto',
      agentSelectionWorkspaceId: null,
    })
  })
})

// helperSessionStore aims the hook at a helper chat: session_id on the sent
// RedirectFrame must be THIS session (D9: conversation-scoped, #955 — the
// helper's own chat redirects that helper).
function helperSessionStore(sessionId: string) {
  act(() => {
    useSessionStore.setState({ activeSessionId: sessionId, attachedSessionType: 'delegate' })
  })
}

// The root conversation has its own active session, not a child target.
// Redirect uses that same identity without a helper-only parameter.
function attachedRootSessionStore(sessionId: string) {
  act(() => {
    useSessionStore.setState({ activeSessionId: sessionId })
  })
}

// ─── Palette visibility ─────────────────────────────────────────────────────

describe('useSlashMenu — D9 /stop and /stop-redirect palette', () => {
  it('lists /stop while streaming (available_while_streaming)', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams({ isStreaming: true })))
    act(() => result.current.onInputChange('/stop'))
    const labels = result.current.slashItems.filter((i) => i.section === 'commands').map((i) => i.label)
    expect(labels).toContain('/stop')
  })

  it('lists /stop-redirect when not streaming (reachable from the palette)', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/stop-r'))
    const labels = result.current.slashItems.filter((i) => i.section === 'commands').map((i) => i.label)
    expect(labels).toContain('/stop-redirect')
  })

  // Corrected decision (§3.1): /stop-redirect is available WHILE STREAMING —
  // mid-stream is the redirect's primary case. The superseded pack's fixture
  // pinned false here; that value was a transport artifact.
  it('lists /stop-redirect while streaming (available_while_streaming true)', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams({ isStreaming: true })))
    act(() => result.current.onInputChange('/stop-r'))
    const labels = result.current.slashItems.filter((i) => i.section === 'commands').map((i) => i.label)
    expect(labels).toContain('/stop-redirect')
  })

  it('never offers a /steer entry (D9 founder O4: no /steer alias)', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    const labels = result.current.slashItems.map((i) => i.label.toLowerCase())
    expect(labels).not.toContain('/steer')
  })
})

// ─── /stop — single-session stop path ───────────────────────────────────────

describe('useSlashMenu — /stop execution', () => {
  // Founder 2026-10-06: "/stop in chat needs to work like one escape or
  // one stop click". It must not call /cancel's immediate-tree callback.
  it('routes a submitted /stop to exactly one existing Stop activation', () => {
    const composerRuntime = makeComposerRuntime('/stop')
    const activateStop = vi.fn()
    const cancelIfStreaming = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime, activateStop, cancelIfStreaming })))
    let intercepted: boolean | undefined
    act(() => {
      intercepted = result.current.interceptClientCommand()
    })
    expect(activateStop).toHaveBeenCalledTimes(1)
    expect(cancelIfStreaming).not.toHaveBeenCalled()
    expect(intercepted).toBe(true)
    // Nothing is dispatched as a chat message for /stop.
    expect(composerRuntime.send).not.toHaveBeenCalled()
  })

  it('leaves /cancel routed to the same cancel path, unchanged by /stop (D9)', () => {
    const composerRuntime = makeComposerRuntime('/cancel')
    const cancelIfStreaming = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime, cancelIfStreaming })))
    act(() => {
      result.current.interceptClientCommand()
    })
    expect(cancelIfStreaming).toHaveBeenCalledTimes(1)
    expect(composerRuntime.send).not.toHaveBeenCalled()
  })
})

// ─── /stop-redirect — current-chat RedirectFrame interception and usage ────

describe('useSlashMenu — /stop-redirect execution', () => {
  // THE corrected transport test (replaces the superseded "passes through as
  // a message" assertion, which pinned the delivery-agent passthrough that
  // cannot execute mid-stream). RED today: no stop-redirect branch exists, so
  // the text is dispatched as an ordinary chat message and no frame is sent.
  //
  // §3.1 web row: in a HELPER chat the SPA intercepts "/stop-redirect
  // <instruction>" WITHOUT submitting it as chat text and sends the dedicated
  // generated RedirectFrame — exactly one, with the active (helper) session id
  // and the trimmed instruction, and NO scope property (D9 row 2 fixes scope:
  // that helper only, subtree keeps working). It must work MID-STREAM.
  it('in a HELPER chat mid-stream intercepts and sends exactly one RedirectFrame — no chat submission, no scope, no stop', () => {
    helperSessionStore('helper-session-9')
    const composerRuntime = makeComposerRuntime('/stop-redirect focus on the failing tests')
    const appendMessage = vi.fn()
    const cancelIfStreaming = vi.fn()
    const sendRedirectFrame = vi.fn()
    const { result } = renderHook(() =>
      useSlashMenu(baseParams({ composerRuntime, appendMessage, cancelIfStreaming, sendRedirectFrame, isStreaming: true })))
    let intercepted: boolean | undefined
    act(() => {
      intercepted = result.current.interceptClientCommand()
    })
    // Handled client-side via interception — the caller must not dispatch the
    // text as a chat message (mid-stream text would enter the steering queue).
    expect(intercepted).toBe(true)
    expect(sendRedirectFrame).toHaveBeenCalledTimes(1)
    const frame = sendRedirectFrame.mock.calls[0][0] as RedirectFrame
    expect(frame).toEqual({
      type: 'redirect',
      session_id: 'helper-session-9',
      instruction: 'focus on the failing tests',
    })
    // Exact key set — a scope property must never exist on the frame (D9 row
    // 2; the schema's additionalProperties:false).
    expect(Object.keys(frame).sort()).toEqual(['instruction', 'session_id', 'type'])
    // Nothing else happens: no chat-text submission, no local message, no
    // stop-only side effect (redirect is not a bare stop).
    expect(composerRuntime.send).not.toHaveBeenCalled()
    expect(appendMessage).not.toHaveBeenCalled()
    expect(cancelIfStreaming).not.toHaveBeenCalled()
  })

  // The interception path is NOT gated on isStreaming — idle helpers get the
  // same one-frame receipt (D9 row 2 is not conditioned on streaming; the
  // palette is visible in both states).
  it('in a HELPER chat when idle sends the same RedirectFrame (no isStreaming gate on the redirect path)', () => {
    helperSessionStore('helper-session-9')
    const composerRuntime = makeComposerRuntime('/stop-redirect focus on the failing tests')
    const sendRedirectFrame = vi.fn()
    const { result } = renderHook(() =>
      useSlashMenu(baseParams({ composerRuntime, sendRedirectFrame, isStreaming: false })))
    let intercepted: boolean | undefined
    act(() => {
      intercepted = result.current.interceptClientCommand()
    })
    expect(intercepted).toBe(true)
    expect(sendRedirectFrame).toHaveBeenCalledTimes(1)
    expect(sendRedirectFrame.mock.calls[0][0]).toEqual({
      type: 'redirect',
      session_id: 'helper-session-9',
      instruction: 'focus on the failing tests',
    })
    expect(composerRuntime.send).not.toHaveBeenCalled()
  })

  // The instruction is delivered verbatim (D2: the instruction becomes the
  // newest instruction): trimmed at both ends, internal spacing preserved,
  // multibyte content intact.
  it('sends the instruction verbatim: trimmed at both ends, internal spacing and multibyte content preserved', () => {
    helperSessionStore('helper-session-9')
    const composerRuntime = makeComposerRuntime('/stop-redirect \u00A0 先 export the CSV — 报告  ')
    const sendRedirectFrame = vi.fn()
    const { result } = renderHook(() =>
      useSlashMenu(baseParams({ composerRuntime, sendRedirectFrame })))
    act(() => {
      result.current.interceptClientCommand()
    })
    expect(sendRedirectFrame).toHaveBeenCalledTimes(1)
    const frame = sendRedirectFrame.mock.calls[0][0] as RedirectFrame
    expect(frame.instruction).toBe('先 export the CSV — 报告')
  })

  it('without an attached current chat gives exact missing-session guidance and sends nothing', () => {
    const composerRuntime = makeComposerRuntime('/stop-redirect do the other thing')
    const appendMessage = vi.fn()
    const cancelIfStreaming = vi.fn()
    const sendRedirectFrame = vi.fn()
    const { result } = renderHook(() =>
      useSlashMenu(baseParams({ composerRuntime, appendMessage, cancelIfStreaming, sendRedirectFrame })))
    let intercepted: boolean | undefined
    act(() => {
      intercepted = result.current.interceptClientCommand()
    })
    expect(intercepted).toBe(true)
    expect(appendMessage).toHaveBeenCalledTimes(1)
    const guidance = appendMessage.mock.calls[0][0] as { role: string; content: string }
    expect(guidance.role).toBe('system')
    expect(guidance.content).toBe(MISSING_SESSION_TEXT)
    expect(composerRuntime.send).not.toHaveBeenCalled()
    expect(cancelIfStreaming).not.toHaveBeenCalled()
    expect(sendRedirectFrame).not.toHaveBeenCalled()
  })

  // Founder: "/stop-redirect ... redirects the chat itself not their
  // helper". Replace the former root refusal with an exact root-frame oracle.
  it('in a ROOT chat with an attached session sends exactly one current-chat RedirectFrame', () => {
    attachedRootSessionStore('root-session-1')
    const composerRuntime = makeComposerRuntime('/stop-redirect do the other thing')
    const appendMessage = vi.fn()
    const cancelIfStreaming = vi.fn()
    const sendRedirectFrame = vi.fn()
    const { result } = renderHook(() =>
      useSlashMenu(baseParams({ composerRuntime, appendMessage, cancelIfStreaming, sendRedirectFrame, isStreaming: true })))
    let intercepted: boolean | undefined
    act(() => {
      intercepted = result.current.interceptClientCommand()
    })
    expect(intercepted).toBe(true)
    expect(sendRedirectFrame).toHaveBeenCalledTimes(1)
    expect(sendRedirectFrame.mock.calls[0][0]).toEqual({
      type: 'redirect',
      session_id: 'root-session-1',
      instruction: 'do the other thing',
    })
    expect(Object.keys(sendRedirectFrame.mock.calls[0][0]).sort()).toEqual(['instruction', 'session_id', 'type'])
    expect(appendMessage).not.toHaveBeenCalled()
    expect(composerRuntime.send).not.toHaveBeenCalled()
    expect(cancelIfStreaming).not.toHaveBeenCalled()
  })

  it.each([
    { chat: 'root', sessionType: null },
    { chat: 'helper', sessionType: 'delegate' },
  ] as const)('in a $chat chat gives exact usage for bare /stop-redirect and sends nothing', ({ sessionType }) => {
    act(() => { useSessionStore.setState({ attachedSessionType: sessionType }) })
    const composerRuntime = makeComposerRuntime('/stop-redirect')
    const appendMessage = vi.fn()
    const sendRedirectFrame = vi.fn()
    const { result } = renderHook(() =>
      useSlashMenu(baseParams({ composerRuntime, appendMessage, sendRedirectFrame })))
    let intercepted: boolean | undefined
    act(() => {
      intercepted = result.current.interceptClientCommand()
    })
    expect(intercepted).toBe(true)
    expect(appendMessage).toHaveBeenCalledTimes(1)
    const usage = appendMessage.mock.calls[0][0] as { role: string; content: string }
    expect(usage.role).toBe('system')
    expect(usage.content).toBe(USAGE_TEXT)
    expect(composerRuntime.send).not.toHaveBeenCalled()
    expect(sendRedirectFrame).not.toHaveBeenCalled()
  })

  // UNICODE whitespace-only is still an empty instruction (dispatch brief:
  // "empty/Unicode-whitespace instruction returns usage with no control or
  // message side effects"). NBSP U+00A0 + em space U+2003 — an ASCII-only
  // trim would send a whitespace "instruction" as a real redirect.
  it('treats a Unicode-whitespace-only instruction as empty: usage reply, nothing sent', () => {
    helperSessionStore('helper-session-9')
    const composerRuntime = makeComposerRuntime('/stop-redirect \u00A0\u2003 ')
    const appendMessage = vi.fn()
    const sendRedirectFrame = vi.fn()
    const { result } = renderHook(() =>
      useSlashMenu(baseParams({ composerRuntime, appendMessage, sendRedirectFrame })))
    let intercepted: boolean | undefined
    act(() => {
      intercepted = result.current.interceptClientCommand()
    })
    expect(intercepted).toBe(true)
    expect(appendMessage).toHaveBeenCalledTimes(1)
    const usage = appendMessage.mock.calls[0][0] as { role: string; content: string }
    expect(usage.role).toBe('system')
    expect(usage.content).toBe(USAGE_TEXT)
    expect(composerRuntime.send).not.toHaveBeenCalled()
    expect(sendRedirectFrame).not.toHaveBeenCalled()
  })
})
