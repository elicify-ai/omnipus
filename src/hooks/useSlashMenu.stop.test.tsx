// useSlashMenu.stop.test.tsx — RED wave, corrected transport (qa-lead,
// test/a-redirect-transport-red): the D9 chat commands /stop and
// /stop-redirect in the composer's slash menu, re-based on the landed
// generated RedirectFrame.
//
// This pack CORRECTS the superseded QA2 pack (feat/stopall-qa2 @ 75aa893a5),
// which pinned the guessed delivery-'agent' passthrough transport. Per the
// architect's corrected ruling (stream-a-seams-assessment.md §3.1/§4):
//   - /stop-redirect is delivery 'client' and available_while_streaming TRUE —
//     redirect stops first, then gives the new instruction (the command name
//     encodes stop-first); mid-stream is the primary case, not an edge.
//   - In a HELPER chat the SPA INTERCEPTS the typed command WITHOUT submitting
//     it as chat text (the same interception /cancel uses) and sends the
//     dedicated generated RedirectFrame {type:'redirect', session_id,
//     instruction} — which never touches message intake, so it executes
//     mid-stream. The superseded "passes through as a message" test pinned the
//     unworkable transport and is REPLACED (the only replaced assertion).
//   - NO scope property exists on the frame (D9 row 2 fixes scope: that helper
//     only, subtree keeps working) — pinned at exact-shape level.
//   - In the ROOT chat the command is refused CLIENT-SIDE with the helper
//     guidance and sends nothing (§3.2: fail-closed on helper identity).
//
// SPEC SOURCES (expected values derive from these, never from the
// implementation):
//   - ADR D9: /stop (root or helper) = single-session stop, same server
//     behaviour as one Stop-button press; /stop-redirect in a HELPER's chat =
//     D2 redirect on that helper; /stop-redirect in the ROOT's chat = refuse
//     with guidance to target a helper (nothing is sent); no /steer alias
//     (founder O4); /cancel is unaffected (scope tree, FR-5 intact).
//   - cancel-cross-channel-spec FR-3a (as amended by D9): entries tagged
//     available_while_streaming stay visible mid-stream — /cancel, /stop and
//     /stop-redirect all carry the tag.
//   - Generated contract: src/lib/api/generated/asyncapi-types.ts
//     RedirectFrame {type:'redirect', session_id, instruction};
//     contracts/components/schemas/RedirectFrame.yaml additionalProperties:
//     false.
//
// REPORTED INTERFACE SHAPE for frontend-lead: UseSlashMenuParams gains
//   isHelperSession: boolean                          // false ⇒ root chat
//   sendRedirectFrame: (frame: RedirectFrame) => void // client interception
// Both pass through the cast (STOP_PARAM_CAST below) so this RED pack
// typecheck-clean under `npm run typecheck` before GREEN adds the params; the
// behaviour assertions still fail today.

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

// STOP_PARAM_CAST: `isHelperSession` and `sendRedirectFrame` are the reported
// D9 seams frontend-lead must add to UseSlashMenuParams (see the file header).
// The cast keeps the RED pack typecheck-clean before the params exist; once
// GREEN adds them, the cast is dead weight a CHECK pass may drop.
type StopPackParams = Parameters<typeof useSlashMenu>[0] & {
  isHelperSession: boolean
  sendRedirectFrame: (frame: RedirectFrame) => void
}

function baseParams(overrides: Partial<StopPackParams> = {}) {
  const params = {
    isStreaming: false,
    isReplaying: false,
    inputEnabled: true,
    composerRuntime: makeComposerRuntime(),
    appendMessage: vi.fn(),
    startNewSession: vi.fn(),
    cancelIfStreaming: vi.fn(),
    sendRedirectFrame: vi.fn(),
    isHelperSession: false,
    ...overrides,
  }
  return params as unknown as Parameters<typeof useSlashMenu>[0]
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
  // RED today: /stop resolves as a client command but runClientCommand has no
  // 'stop' branch, so the submit clears the composer and silently does
  // nothing — cancelIfStreaming (the Stop button's single-press path) is
  // never called.
  it('routes a submitted /stop to the single-session stop path (cancelIfStreaming), same as one Stop-button press', () => {
    const composerRuntime = makeComposerRuntime('/stop')
    const cancelIfStreaming = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime, cancelIfStreaming })))
    let intercepted: boolean | undefined
    act(() => {
      intercepted = result.current.interceptClientCommand()
    })
    expect(cancelIfStreaming).toHaveBeenCalledTimes(1)
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

// ─── /stop-redirect — RedirectFrame interception, root refusal, usage ───────

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
      useSlashMenu(baseParams({ composerRuntime, appendMessage, cancelIfStreaming, sendRedirectFrame, isHelperSession: true, isStreaming: true })))
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
      useSlashMenu(baseParams({ composerRuntime, sendRedirectFrame, isHelperSession: true, isStreaming: false })))
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
      useSlashMenu(baseParams({ composerRuntime, sendRedirectFrame, isHelperSession: true })))
    act(() => {
      result.current.interceptClientCommand()
    })
    expect(sendRedirectFrame).toHaveBeenCalledTimes(1)
    const frame = sendRedirectFrame.mock.calls[0][0] as RedirectFrame
    expect(frame.instruction).toBe('先 export the CSV — 报告')
  })

  // RED today: "/stop-redirect do X" in the ROOT chat is dispatched as an
  // ordinary chat message — no guidance is shown and nothing holds the send.
  it('in the ROOT chat refuses with guidance to target a helper and sends nothing', () => {
    const composerRuntime = makeComposerRuntime('/stop-redirect do the other thing')
    const appendMessage = vi.fn()
    const cancelIfStreaming = vi.fn()
    const sendRedirectFrame = vi.fn()
    const { result } = renderHook(() =>
      useSlashMenu(baseParams({ composerRuntime, appendMessage, cancelIfStreaming, sendRedirectFrame, isHelperSession: false })))
    let intercepted: boolean | undefined
    act(() => {
      intercepted = result.current.interceptClientCommand()
    })
    // Handled client-side: the caller must NOT dispatch anything.
    expect(intercepted).toBe(true)
    // Guidance names the helper target (D9's own word).
    expect(appendMessage).toHaveBeenCalledTimes(1)
    const guidance = appendMessage.mock.calls[0][0] as { role: string; content: string }
    expect(guidance.role).toBe('system')
    expect(guidance.content.toLowerCase()).toContain('helper')
    // And nothing leaves the composer: no message send, no stop, no frame.
    expect(composerRuntime.send).not.toHaveBeenCalled()
    expect(cancelIfStreaming).not.toHaveBeenCalled()
    expect(sendRedirectFrame).not.toHaveBeenCalled()
  })

  // RED today: a bare /stop-redirect in a helper chat is dispatched to the
  // server as a chat message instead of getting local usage guidance.
  it('in a HELPER chat shows usage for a bare /stop-redirect and sends nothing', () => {
    const composerRuntime = makeComposerRuntime('/stop-redirect')
    const appendMessage = vi.fn()
    const sendRedirectFrame = vi.fn()
    const { result } = renderHook(() =>
      useSlashMenu(baseParams({ composerRuntime, appendMessage, sendRedirectFrame, isHelperSession: true })))
    let intercepted: boolean | undefined
    act(() => {
      intercepted = result.current.interceptClientCommand()
    })
    expect(intercepted).toBe(true)
    expect(appendMessage).toHaveBeenCalledTimes(1)
    const usage = appendMessage.mock.calls[0][0] as { role: string; content: string }
    expect(usage.role).toBe('system')
    expect(usage.content).toContain('/stop-redirect')
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
      useSlashMenu(baseParams({ composerRuntime, appendMessage, sendRedirectFrame, isHelperSession: true })))
    let intercepted: boolean | undefined
    act(() => {
      intercepted = result.current.interceptClientCommand()
    })
    expect(intercepted).toBe(true)
    expect(appendMessage).toHaveBeenCalledTimes(1)
    const usage = appendMessage.mock.calls[0][0] as { role: string; content: string }
    expect(usage.role).toBe('system')
    expect(usage.content).toContain('/stop-redirect')
    expect(composerRuntime.send).not.toHaveBeenCalled()
    expect(sendRedirectFrame).not.toHaveBeenCalled()
  })
})
