// useSlashMenu.stop.test.tsx — RED wave 2 (qa-lead, feat/stopall-qa2): the
// D9 chat commands /stop and /stop-redirect in the composer's slash menu.
//
// SPEC SOURCES (expected values derive from these, never from the
// implementation):
//   - ADR D9 input table: /stop (root or helper) = single-session stop, same
//     server behaviour as one Stop-button press; /stop-redirect in a HELPER's
//     chat = D2 redirect on that helper; /stop-redirect in the ROOT's chat =
//     refuse with guidance to target a helper (nothing is sent); no /steer
//     alias (founder O4); /cancel is unaffected (scope tree, FR-5 intact).
//   - cancel-cross-channel-spec FR-3a analogue: entries tagged
//     available_while_streaming stay visible mid-stream.
//   - Wire path decision (reported, contract-backed): /stop is delivery
//     'client' (the SPA routes it to the same cancelIfStreaming path as the
//     Stop button; the store's cancelStream sends {type:'cancel',
//     session_id} with NO scope field, and the contract's CancelFrame.scope
//     defaults to 'session' — the single-session stop). /stop-redirect is
//     delivery 'agent' (insert-as-text → UserMessageFrame → the server's
//     existing command dispatch, pkg/agent/loop_slash.go::handleCommand) —
//     no new wire frame is invented.
//
// REPORTED INTERFACE SHAPE for frontend-lead: UseSlashMenuParams gains
//   isHelperSession: boolean   // false ⇒ root chat (root refusal path)
// The tests pass it through a cast (STOP_PARAM_CAST below) so this RED pack
// compiles under `npm run typecheck` before GREEN adds the param; the
// behaviour assertions still fail today.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import type { ComposerRuntime } from '@assistant-ui/react'
import type { Agent } from '@/lib/api'
import { useSlashMenu } from './useSlashMenu'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { makeAgent } from '@/test/factories'

// Command list as the amended contract will serve it: /stop client-delivered
// and streaming-available (dispatch brief), /stop-redirect agent-delivered
// with an instruction hint (D9: "/stop-redirect <instruction>").
const stopPackCommands = [
  { name: 'new', label: '/new', description: 'Start a new conversation', delivery: 'client', available_while_streaming: false },
  { name: 'cancel', label: '/cancel', description: 'Stop all — this session and its helpers', delivery: 'client', available_while_streaming: true },
  { name: 'stop', label: '/stop', description: 'Stop this session\'s current turn', delivery: 'client', available_while_streaming: true },
  { name: 'stop-redirect', label: '/stop-redirect', description: 'Stop this helper and continue with a new instruction', delivery: 'agent', available_while_streaming: false, argument_hint: '<instruction>' },
]

const mockAgents: Agent[] = [
  makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'active', color: '#111111', description: 'Assistant' }),
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

// STOP_PARAM_CAST: `isHelperSession` is the reported D9 seam frontend-lead
// must add to UseSlashMenuParams (see the file header). The cast keeps the
// RED pack typecheck-clean before the param exists; once GREEN adds it, the
// cast is dead weight a CHECK pass may drop.
type StopPackParams = Parameters<typeof useSlashMenu>[0] & { isHelperSession: boolean }

function baseParams(overrides: Partial<StopPackParams> = {}) {
  const params = {
    isStreaming: false,
    isReplaying: false,
    inputEnabled: true,
    composerRuntime: makeComposerRuntime(),
    appendMessage: vi.fn(),
    startNewSession: vi.fn(),
    cancelIfStreaming: vi.fn(),
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

// ─── /stop-redirect — helper passthrough, root refusal, usage ───────────────

describe('useSlashMenu — /stop-redirect execution', () => {
  // RED today: "/stop-redirect do X" resolves to nothing client-side and is
  // dispatched as an ordinary chat message even in the ROOT's chat — no
  // guidance is shown and nothing holds the send.
  it('in the ROOT chat refuses with guidance to target a helper and sends nothing', () => {
    const composerRuntime = makeComposerRuntime('/stop-redirect do the other thing')
    const appendMessage = vi.fn()
    const cancelIfStreaming = vi.fn()
    const { result } = renderHook(() =>
      useSlashMenu(baseParams({ composerRuntime, appendMessage, cancelIfStreaming, isHelperSession: false })))
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
    // And nothing leaves the composer: no message send, no stop.
    expect(composerRuntime.send).not.toHaveBeenCalled()
    expect(cancelIfStreaming).not.toHaveBeenCalled()
  })

  // RED today: a bare /stop-redirect in a helper chat is dispatched to the
  // server as a chat message instead of getting local usage guidance.
  it('in a HELPER chat shows usage for a bare /stop-redirect and sends nothing', () => {
    const composerRuntime = makeComposerRuntime('/stop-redirect')
    const appendMessage = vi.fn()
    const { result } = renderHook(() =>
      useSlashMenu(baseParams({ composerRuntime, appendMessage, isHelperSession: true })))
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
  })

  // Contract pin (its discriminating power arrives with the two RED tests
  // above): with an instruction, a helper chat must NOT intercept — the text
  // goes out as an ordinary message, which is the server path the D2 redirect
  // rides (UserMessageFrame → loop_slash command dispatch). No new frame is
  // invented.
  it('in a HELPER chat passes "/stop-redirect <instruction>" through as a message (the server redirect path)', () => {
    const composerRuntime = makeComposerRuntime('/stop-redirect focus on the failing tests')
    const appendMessage = vi.fn()
    const { result } = renderHook(() =>
      useSlashMenu(baseParams({ composerRuntime, appendMessage, isHelperSession: true })))
    let intercepted: boolean | undefined
    act(() => {
      intercepted = result.current.interceptClientCommand()
    })
    expect(intercepted).toBe(false)
    expect(appendMessage).not.toHaveBeenCalled()
  })
})
