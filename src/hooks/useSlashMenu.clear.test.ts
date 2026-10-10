// useSlashMenu.clear.test.ts — FR-030/031 (U10b): the SPA half of the real
// /clear. Oracles: docs/internal/specs/session-core-spec.md FR-030/FR-031 +
// the WC-1 amendment (2026-10-09) + core's pkg/commands/cmd_clear.go
// (git 67345b1d7, read-only). Expectations are derived from those, never
// from the current SPA code:
//
//   - /clear is a SERVER command (DeliveryAgent): a typed /clear and a
//     palette-selected /clear both reach the server as an ordinary message;
//     the SPA never runs it locally and never starts a session for it.
//   - The palette lists /clear exactly when the server's command list
//     contains it — never from a hardcoded client list (this base's server
//     does not list /clear yet; the palette must then show no /clear).
//   - /new is RETIRED (WC-1): the SPA must not send it to the server, must
//     not offer it, and must not run any client-side new-session path for
//     it. A typed /new is refused visibly (pointing at the agent row's
//     New chat action and at /clear) — the one behavior compatible with all
//     three prohibitions (never offer / never send / never swallow).
//
// These tests isolate the hook, mirroring useSlashMenu.test.ts's harness
// (mocked React Query useQuery, mocked assistant-ui ComposerRuntime).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import type { ComposerRuntime } from '@assistant-ui/react'
import { useSlashMenu } from './useSlashMenu'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'

// The canonical server table after U10b (core's clearCommand(): DeliveryAgent
// → the generated delivery is 'agent'; no /new anywhere).
const CANONICAL_SERVER_COMMANDS = [
  {
    name: 'clear',
    label: '/clear',
    description: "Clear this chat's context; the transcript is kept",
    usage: '/clear',
    delivery: 'agent',
    available_while_streaming: false,
  },
  {
    name: 'help',
    label: '/help',
    description: 'Show available commands',
    delivery: 'client',
    available_while_streaming: false,
  },
  {
    name: 'cancel',
    label: '/cancel',
    description: 'Cancel the current turn',
    delivery: 'client',
    available_while_streaming: true,
  },
]

// This checkout's base server (U10a landed, U10b not yet): /new retired AND
// /clear still absent from the table.
const BASE_SERVER_COMMANDS = CANONICAL_SERVER_COMMANDS.filter((c) => c.name !== 'clear')

let serverCommands: typeof CANONICAL_SERVER_COMMANDS = CANONICAL_SERVER_COMMANDS
let commandsQueryIsLoading = false

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[]; enabled?: boolean }) => {
      if (opts.enabled === false) return { data: [], isError: false, isLoading: false, refetch: vi.fn() }
      const key = opts.queryKey
      if (Array.isArray(key) && key[0] === 'commands') {
        if (commandsQueryIsLoading) return { data: undefined, isError: false, isLoading: true, refetch: vi.fn() }
        return { data: serverCommands, isError: false, isLoading: false, refetch: vi.fn() }
      }
      if (Array.isArray(key) && key[0] === 'skills') return { data: [], isError: false, isLoading: false, refetch: vi.fn() }
      if (Array.isArray(key) && key[0] === 'agents') return { data: [], isError: false, isLoading: false, refetch: vi.fn() }
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
    // Mirrors the real runtime: setText updates the composer core's text, so
    // a later getState() reads back what was set.
    setText: vi.fn((value: string) => { text = value }),
    addAttachment: vi.fn(),
    subscribe: vi.fn(() => vi.fn()),
    send: vi.fn(),
  } as unknown as ComposerRuntime & {
    setText: ReturnType<typeof vi.fn>
    send: ReturnType<typeof vi.fn>
  }
}

function baseParams(overrides: Partial<Parameters<typeof useSlashMenu>[0]> = {}) {
  return {
    isStreaming: false,
    isReplaying: false,
    inputEnabled: true,
    composerRuntime: makeComposerRuntime(),
    appendMessage: vi.fn(),
    cancelIfStreaming: vi.fn(),
    sendRedirectFrame: vi.fn(),
    activateStop: vi.fn(),
    ...overrides,
  }
}

beforeEach(() => {
  serverCommands = CANONICAL_SERVER_COMMANDS
  commandsQueryIsLoading = false
  act(() => {
    useUiStore.setState({ modelSelectorOpen: false, agentSelectorOpen: false, searchModalOpen: false, searchModalMode: 'sessions', searchModalWorkspaceFilter: null })
    useSessionStore.setState({ activeAgentId: null, activeSessionId: null, activeAgentType: null, attachedSessionType: null, attachedTaskTitle: null })
  })
})

describe('useSlashMenu — the palette shows exactly what the server lists (FR-031)', () => {
  it('lists /clear when the server command list contains it', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    const entry = result.current.slashItems.find((i) => i.key === '/clear')
    expect(entry, 'the server lists /clear, so the palette must show it').toBeDefined()
    expect(entry!.section).toBe('commands')
  })

  it('shows NO /clear entry when the server command list does not contain it (this base, pre-U10b)', () => {
    serverCommands = BASE_SERVER_COMMANDS
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    expect(result.current.slashItems.find((i) => i.key === '/clear')).toBeUndefined()
  })

  it('never offers /new, whatever the server lists', () => {
    // Even against a hypothetical stale server that still lists /new, the
    // retired command must not be offered by the SPA's own surface.
    serverCommands = [
      ...BASE_SERVER_COMMANDS,
      { name: 'new', label: '/new', description: 'Start a new conversation', delivery: 'agent', available_while_streaming: false },
    ]
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/new'))
    expect(result.current.slashItems.find((i) => i.key === '/new')).toBeUndefined()
  })
})

describe('useSlashMenu — typed /clear goes to the server (FR-030/031)', () => {
  it.each(['/clear', '/Clear'])('%s is not intercepted — the caller sends it to the server like any server command', (typed) => {
    const composerRuntime = makeComposerRuntime(typed)
    const startNewSession = vi.spyOn(useSessionStore.getState(), 'startNewSession')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))

    let intercepted = false
    act(() => { intercepted = result.current.interceptClientCommand() })

    expect(intercepted).toBe(false)
    expect(composerRuntime.setText).not.toHaveBeenCalled()
    expect(startNewSession).not.toHaveBeenCalled()
    // Nothing ran locally — the send belongs to the caller's normal submit path.
    expect(composerRuntime.send).not.toHaveBeenCalled()
  })

  it('selecting /clear from the palette sends it without starting a session and without ghost text', () => {
    const startNewSession = vi.spyOn(useSessionStore.getState(), 'startNewSession')
    const composerRuntime = makeComposerRuntime()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    act(() => result.current.onInputChange('/'))
    const item = result.current.slashItems.find((i) => i.key === '/clear')!
    act(() => item.onSelect())

    expect(startNewSession).not.toHaveBeenCalled()
    expect(composerRuntime.send).toHaveBeenCalledTimes(1)
    expect((composerRuntime.getState() as { text: string }).text).toBe('/clear')
    expect(result.current.showGhostText).toBe(false)
  })

  it('a /clear submit held by the readiness gate is delivered once the list lands — never dropped, never run locally', () => {
    commandsQueryIsLoading = true
    const composerRuntime = makeComposerRuntime('/clear')
    const startNewSession = vi.spyOn(useSessionStore.getState(), 'startNewSession')
    const { result, rerender } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))

    let intercepted = true
    act(() => { intercepted = result.current.interceptClientCommand() })
    expect(intercepted).toBe(true, 'held while the command list is in flight')
    expect(composerRuntime.send).not.toHaveBeenCalled()

    commandsQueryIsLoading = false
    act(() => { rerender() })

    expect(composerRuntime.send).toHaveBeenCalledTimes(1)
    expect(startNewSession).not.toHaveBeenCalled()
  })
})

describe('useSlashMenu — /new is retired (WC-1): never sent, never a session, refused visibly', () => {
  it.each(['/new', '/NEW', '/New', '/new '])('%s is refused locally: visible reply, nothing sent, no session', (typed) => {
    const composerRuntime = makeComposerRuntime(typed)
    const appendMessage = vi.fn()
    const startNewSession = vi.spyOn(useSessionStore.getState(), 'startNewSession')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime, appendMessage })))

    let intercepted = false
    act(() => { intercepted = result.current.interceptClientCommand() })

    // Taken responsibility for: the caller must NOT dispatch it anywhere.
    expect(intercepted).toBe(true)
    // The refusal is VISIBLE — a system reply naming the replacements.
    expect(appendMessage).toHaveBeenCalledTimes(1)
    const reply = appendMessage.mock.calls[0][0] as { role: string; content: string }
    expect(reply.role).toBe('system')
    expect(reply.content).toContain('/new')
    expect(reply.content).toContain('New chat')
    expect(reply.content).toContain('/clear')
    // The composer text is consumed (the refused command does not linger).
    expect(composerRuntime.setText).toHaveBeenCalledWith('')
    // And nothing left this client: no local session start, no send.
    expect(startNewSession).not.toHaveBeenCalled()
    expect(composerRuntime.send).not.toHaveBeenCalled()
  })

  it('refuses /new immediately even while the command list is still loading', () => {
    commandsQueryIsLoading = true
    const composerRuntime = makeComposerRuntime('/new')
    const appendMessage = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime, appendMessage })))

    let intercepted = false
    act(() => { intercepted = result.current.interceptClientCommand() })

    expect(intercepted).toBe(true)
    expect(appendMessage).toHaveBeenCalledTimes(1)
    expect(composerRuntime.send).not.toHaveBeenCalled()
  })

  it('/help built from a canonical server table never mentions /new', () => {
    const appendMessage = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ appendMessage })))
    act(() => result.current.onInputChange('/'))
    const item = result.current.slashItems.find((i) => i.key === '/help')!
    act(() => item.onSelect())

    expect(appendMessage).toHaveBeenCalledTimes(1)
    const msg = appendMessage.mock.calls[0][0] as { role: string; content: string }
    expect(msg.role).toBe('system')
    expect(msg.content).toContain('/clear')
    expect(msg.content).not.toContain('/new')
  })
})
