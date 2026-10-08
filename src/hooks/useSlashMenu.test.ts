// useSlashMenu.test.ts — partitioned slash-command + skill palette
// (FR-005/FR-006/FR-009/FR-014/D9/R3) plus the ghost-text hint. Extracted out
// of OmnipusComposer's own inline state; ChatScreen's existing integration
// tests (partitioned-menu, unknown-slash, ghost-text, agents-command, etc.)
// exercise this through the rendered composer's real DOM events. These tests
// isolate the hook's own filtering/dispatch logic so a regression here fails
// fast and close to the cause, independent of AssistantUI's mocked
// primitives.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import type { ComposerRuntime } from '@assistant-ui/react'
import type { Agent } from '@/lib/api'
import { useSlashMenu } from './useSlashMenu'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { makeAgent } from '@/test/factories'

const mockCommands = [
  { name: 'new', label: '/new', description: 'Start a new conversation', delivery: 'client', available_while_streaming: false, aliases: ['clear'] },
  { name: 'help', label: '/help', description: 'Show available commands', delivery: 'client', available_while_streaming: false },
  { name: 'model', label: '/model', description: 'Change the chat model', delivery: 'client', available_while_streaming: false },
  { name: 'agents', label: '/agents', description: 'Open agent selector', delivery: 'client', available_while_streaming: false },
  { name: 'skills', label: '/skills', description: 'List installed skills', delivery: 'client', available_while_streaming: false },
  { name: 'cancel', label: '/cancel', description: 'Cancel the current turn', delivery: 'client', available_while_streaming: true },
  { name: 'unknown-agent-cmd', label: '/handoff', description: 'Agent-delivered command', delivery: 'agent', available_while_streaming: false },
]
const mockSkills = [
  { id: 'web-research', name: 'Web Research', version: '1.0', description: 'Web search and extraction', verified: true, status: 'active', argument_hint: '<query>' },
  { id: 'code-review', name: 'Code Review', version: '1.0', description: 'Reviews code quality', verified: true, status: 'active' },
]
// "@" mention menu source data — includes a worker (id "max") to prove
// useChatAgents' isWorker exclusion carries through to the mention menu,
// mirroring AgentPicker.test.tsx's own worker-exclusion fixtures. The
// worker's id/name/type ("max" / "Max Worker" / "Subagent") deliberately
// matches ChatScreen.agent-mention.test.tsx's own "max" fixture exactly
// (gap 12 — cross-file fixture alignment): a fixture id shared across test
// files should mean the same thing everywhere, so "max" is a worker in both
// files, never a chat-eligible agent in either.
const mockAgents: Agent[] = [
  makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'active', color: '#111111', description: 'Assistant' }),
  makeAgent({ id: 'jim', name: 'Jim', type: 'core', status: 'idle', description: 'Orchestrator' }),
  makeAgent({ id: 'mars', name: 'Mars', type: 'Main', status: 'active', description: 'Ops lead' }),
  makeAgent({ id: 'max', name: 'Max Worker', type: 'Subagent', status: 'active', description: 'Labour agent' }),
]

// Fix 7 (bugfixes3 review): mutable per-test override for the ['agents']
// query, mirroring the `commandsQueryIsError` pattern above. Only the
// "NAME prefix only" test needs a fixture whose id and name diverge (a
// UUID-like id with an unrelated display name) — overriding here keeps
// every other test's agent list (and its exact expected row counts/order)
// untouched.
let mentionAgentsOverride: typeof mockAgents | null = null

// Deferred item 3: mutable per-test override for the ['skills'] query,
// mirroring `mentionAgentsOverride` — only the cap/hidden-count test needs a
// skills list bigger than the module-level `mockSkills` fixture (2 entries,
// well under the 8-row cap).
let skillsOverride: typeof mockSkills | null = null

// LOW S8: mutable flag so individual tests can simulate fetchCommands('web')
// erroring — read lazily inside useQuery's closure (only touched when a test
// actually invokes the hook), so the module-load-order TDZ concern that
// applies to top-level `const` reads inside a hoisted vi.mock factory
// doesn't apply here either.
let commandsQueryIsError = false

// Readiness-gate flag: simulates `fetchCommands('web')` still being in flight
// (React Query `isLoading` — first fetch, no data yet). This is the window in
// which a typed "/new" used to resolve against a list containing only the two
// synthetic client-only entries and escape to the backend as chat text.
let commandsQueryIsLoading = false

// Round-1 review addition: mutable per-test override for the ['commands']
// query, mirroring `mentionAgentsOverride`/`skillsOverride` above. Only the
// argument_hint-ghost-for-agent-delivery-commands tests need commands beyond
// the shared `mockCommands` fixture (whose one agent-delivery entry,
// "/handoff", deliberately carries no argument_hint) — overriding here keeps
// every other test's exact command-list/order assertions untouched.
let commandsOverride: typeof mockCommands | null = null

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[]; enabled?: boolean }) => {
      if (opts.enabled === false) return { data: [], isError: false, isLoading: false, refetch: vi.fn() }
      const key = opts.queryKey
      if (Array.isArray(key) && key[0] === 'commands') {
        if (commandsQueryIsLoading) return { data: undefined, isError: false, isLoading: true, refetch: vi.fn() }
        return commandsQueryIsError
          ? { data: undefined, isError: true, isLoading: false, refetch: vi.fn() }
          : { data: commandsOverride ?? mockCommands, isError: false, isLoading: false, refetch: vi.fn() }
      }
      if (Array.isArray(key) && key[0] === 'skills') return { data: skillsOverride ?? mockSkills, isError: false, isLoading: false, refetch: vi.fn() }
      if (Array.isArray(key) && key[0] === 'agents') return { data: mentionAgentsOverride ?? mockAgents, isError: false, isLoading: false, refetch: vi.fn() }
      return { data: [], isError: false, isLoading: false, refetch: vi.fn() }
    },
  }
})

// importOriginal for `isWorker`/`workspacesQueryKeys` — useChatAgents (used
// internally for the "@" mention menu) calls these as real functions, not
// through the mocked useQuery above. fetchAgents/fetchWorkspaces are never
// actually invoked (useQuery is fully mocked and never calls queryFn), so
// stubbing them is only to satisfy the module's static import.
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

// `send` is the real runtime method every submit path converges on
// (ComposerPrimitive.Root's onSubmit, ComposerPrimitive.Send's onClick, and
// ChatScreen's mid-stream path) — spying on it is how these tests observe
// "this text was dispatched to the backend".
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

function baseParams(overrides: Partial<Parameters<typeof useSlashMenu>[0]> = {}) {
  return {
    isStreaming: false,
    isReplaying: false,
    inputEnabled: true,
    composerRuntime: makeComposerRuntime(),
    appendMessage: vi.fn(),
    startNewSession: vi.fn(),
    cancelIfStreaming: vi.fn(),
    sendRedirectFrame: vi.fn(),
    activateStop: vi.fn(),
    ...overrides,
  }
}

beforeEach(() => {
  act(() => {
    useUiStore.setState({
      modelSelectorOpen: false,
      agentSelectorOpen: false,
      // Session-search TWO MODES: reset closed/sessions-mode/no-filter
      // between tests so a /workspace dispatch in one test can't leak
      // 'workspaces' mode (or a stale open=true) into the next.
      searchModalOpen: false,
      searchModalMode: 'sessions',
      searchModalWorkspaceFilter: null,
    })
  })
  // useSessionStore is a real (unmocked) singleton store — selectMentionAgent
  // writes to it via setActiveSession, so it must be reset between tests the
  // same way useUiStore is, or a mention-selection test would leak its
  // activeAgentId/activeSessionId into the next test.
  act(() => {
    useSessionStore.setState({
      activeAgentId: null,
      activeSessionId: null,
      activeAgentType: null,
      attachedSessionType: null,
      attachedTaskTitle: null,
      // selectMentionAgent now records an EXPLICIT selection (see the AGENT
      // PRECEDENCE RULE in src/store/session.ts) — reset it too, or one
      // mention-selection test would leave a pin that changes how the next
      // test's session-derived writes behave.
      agentSelectionSource: 'auto',
      agentSelectionWorkspaceId: null,
    })
  })
  commandsQueryIsError = false
  commandsQueryIsLoading = false
  mentionAgentsOverride = null
  skillsOverride = null
  commandsOverride = null
})

afterEach(() => {
  // Safety net for tests that opt into fake timers (onInputBlur delay) —
  // matches useCancelState.test.ts's convention so a thrown assertion can't
  // leak fake timers into a later test.
  vi.useRealTimers()
})

describe('useSlashMenu — gating', () => {
  it('shows nothing when inputValue does not start with "/"', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('hello'))
    expect(result.current.shouldShowSlash).toBe(false)
    expect(result.current.slashItems).toHaveLength(0)
  })

  it('shows nothing when isReplaying is true', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams({ isReplaying: true })))
    act(() => result.current.onInputChange('/'))
    expect(result.current.shouldShowSlash).toBe(false)
  })

  it('shows nothing when inputEnabled is false', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams({ inputEnabled: false })))
    act(() => result.current.onInputChange('/'))
    expect(result.current.shouldShowSlash).toBe(false)
  })

  it('shows the full partitioned list (commands then skills) for a bare "/"', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    expect(result.current.shouldShowSlash).toBe(true)
    // Deferred item 3: skills are sorted alphabetically by name before the
    // cap — "Code Review" < "Web Research" — so code-review now precedes
    // web-research (previously API/array order). A bare "/" is an EMPTY
    // filter, so rankByFilter's early-return still preserves this
    // (pre-sorted) order rather than re-ranking by prefix/substring.
    // Session-search enhancement: "/workspace" is a second synthetic
    // client-only entry, inserted immediately after "/resume" in
    // useSlashMenu's allCommands (both web-client-only synthetic commands
    // sit together ahead of every backend-served command).
    expect(result.current.slashItems.map((i) => i.key)).toEqual([
      '/resume', '/workspace', '/help', '/model', '/agents', '/skills', '/cancel', '/handoff', 'code-review', 'web-research',
    ])
    expect(result.current.slashItems.map((i) => i.key)).not.toContain('/new')
    expect(result.current.slashItems.map((i) => i.key)).not.toContain('/clear')
  })
})

describe('useSlashMenu — streaming filter', () => {
  it('while streaming, only available_while_streaming commands remain (skills unaffected)', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams({ isStreaming: true })))
    act(() => result.current.onInputChange('/'))
    const commandKeys = result.current.slashItems.filter((i) => i.section === 'commands').map((i) => i.key)
    // "/workspace" is declared with available_while_streaming: true (same as
    // "/resume" and "/cancel") — session search must stay reachable mid-turn.
    expect(commandKeys).toEqual(['/resume', '/workspace', '/cancel'])
    const skillKeys = result.current.slashItems.filter((i) => i.section === 'skills').map((i) => i.key)
    // Deferred item 3: alphabetical by name (see comment on the "gating" test above).
    expect(skillKeys).toEqual(['code-review', 'web-research'])
  })
})

describe('useSlashMenu — D9 "/skills" filter', () => {
  it('hides the commands section entirely and shows every skill', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/skills'))
    expect(result.current.slashItems.every((i) => i.section === 'skills')).toBe(true)
    // Deferred item 3: alphabetical by name.
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['code-review', 'web-research'])
  })
})

// Deferred item 3: skills share the same cap + hidden-count mechanism as
// agents (SECTION_CAP, sort-before-cap, skillsHiddenCount) — this file's
// module-level `mockSkills` only has 2 entries, so this describe block
// swaps in a larger skills list (via `skillsOverride`) to actually exercise
// the cap.
describe('useSlashMenu — skills cap + hidden count (deferred item 3)', () => {
  it('11 skills show exactly 8 alphabetically-ordered rows for a bare "/", plus skillsHiddenCount=3', () => {
    skillsOverride = Array.from({ length: 11 }, (_, i) => ({
      id: `sk${String(i + 1).padStart(2, '0')}`,
      name: `Skill${String(i + 1).padStart(2, '0')}`,
      version: '1.0',
      description: '',
      verified: true,
      status: 'active',
    }))
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))

    const skillKeys = result.current.slashItems.filter((i) => i.section === 'skills').map((i) => i.key)
    expect(skillKeys).toHaveLength(8)
    // Alphabetical by name: Skill01..Skill08 (zero-padded so lexicographic
    // order matches numeric order).
    expect(skillKeys).toEqual(['sk01', 'sk02', 'sk03', 'sk04', 'sk05', 'sk06', 'sk07', 'sk08'])
    // Skill09/Skill10/Skill11 are the three matches pushed past the cap.
    expect(result.current.skillsHiddenCount).toBe(3)
  })

  it('narrowing to ≤8 matches drops skillsHiddenCount back to 0', () => {
    skillsOverride = Array.from({ length: 11 }, (_, i) => ({
      id: `sk${String(i + 1).padStart(2, '0')}`,
      name: `Skill${String(i + 1).padStart(2, '0')}`,
      version: '1.0',
      description: '',
      verified: true,
      status: 'active',
    }))
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    // "sk1" prefix-matches only "Skill10" and "Skill11" (2 matches — well
    // under the cap); "Skill01".."Skill09" all have "sk0" at that position.
    act(() => result.current.onInputChange('/sk1'))

    const skillKeys = result.current.slashItems.filter((i) => i.section === 'skills').map((i) => i.key)
    expect(skillKeys).toEqual(['sk10', 'sk11'])
    expect(result.current.skillsHiddenCount).toBe(0)
  })
})

describe('useSlashMenu — prefix filtering', () => {
  it('filters commands and skills by the text after "/"', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/hel'))
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['/help'])
  })

  it('shows no items for an unmatched prefix (Issue 3 — menu just disappears)', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/zzz'))
    expect(result.current.slashItems).toHaveLength(0)
    expect(result.current.shouldShowSlash).toBe(false)
  })

  it('does not list /new or its /clear alias', () => {
    // FR-007: the SPA no longer lists /new or the backend alias /clear.
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/cl'))
    expect(result.current.slashItems).toHaveLength(0)
    act(() => result.current.onInputChange('/new'))
    expect(result.current.slashItems.map((i) => i.key)).not.toContain('/new')
    expect(result.current.slashItems.map((i) => i.key)).not.toContain('/clear')
  })
})

describe('useSlashMenu — keyboard navigation', () => {
  it('ArrowDown/ArrowUp cycle the highlight and open the menu', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    expect(result.current.slashHighlight).toBe(0)

    const down = { key: 'ArrowDown', preventDefault: vi.fn() } as unknown as React.KeyboardEvent
    act(() => result.current.handleKeyDown(down))
    expect(result.current.slashHighlight).toBe(1)
    expect(result.current.slashOpen).toBe(true)

    const up = { key: 'ArrowUp', preventDefault: vi.fn() } as unknown as React.KeyboardEvent
    act(() => result.current.handleKeyDown(up))
    act(() => result.current.handleKeyDown(up))
    // wraps around from index 0
    expect(result.current.slashHighlight).toBe(result.current.slashItems.length - 1)
  })

  it('Escape closes the menu', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    act(() => result.current.handleKeyDown({ key: 'ArrowDown', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(result.current.slashOpen).toBe(true)

    act(() => result.current.handleKeyDown({ key: 'Escape', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(result.current.slashOpen).toBe(false)
  })

  // Fix 8 (bugfixes3 review): Shift+Enter is universally "insert a
  // newline" — selecting from the menu on Shift+Enter silently ate the
  // newline and could mutate a multiline draft that happened to start with
  // "/" or "@". Applies identically to "/" and "@" mode; exercised here via
  // "/" (the two modes share this exact handleKeyDown code path).
  it('Shift+Enter does NOT select from the menu — falls through so the caller can insert a newline', () => {
    const appendMessage = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ appendMessage })))
    act(() => result.current.onInputChange('/'))
    // Move highlight off "/resume" and "/workspace" (both touch the ui store)
    // onto "/help", whose handler appends a local message.
    act(() => result.current.handleKeyDown({ key: 'ArrowDown', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    act(() => result.current.handleKeyDown({ key: 'ArrowDown', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(result.current.slashItems[result.current.slashHighlight].key).toBe('/help')

    const shiftEnter = { key: 'Enter', shiftKey: true, preventDefault: vi.fn() } as unknown as React.KeyboardEvent
    act(() => result.current.handleKeyDown(shiftEnter))

    // Nothing was selected — preventDefault was not called, the menu is
    // still open, and no command handler ran.
    expect(shiftEnter.preventDefault).not.toHaveBeenCalled()
    expect(result.current.slashOpen).toBe(true)
    expect(appendMessage).not.toHaveBeenCalled()

    // Plain Enter (no Shift) on the SAME highlighted row still selects
    // normally, proving the guard is Shift-specific, not a general Enter
    // regression.
    const plainEnter = { key: 'Enter', shiftKey: false, preventDefault: vi.fn() } as unknown as React.KeyboardEvent
    act(() => result.current.handleKeyDown(plainEnter))
    expect(plainEnter.preventDefault).toHaveBeenCalled()
    expect(appendMessage).toHaveBeenCalledTimes(1)
  })

  it('is a no-op when the menu should not be showing', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    // No "/" typed — shouldShowSlash is false.
    act(() => result.current.handleKeyDown({ key: 'ArrowDown', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(result.current.slashHighlight).toBe(0)
    expect(result.current.slashOpen).toBe(false)
  })
})

describe('useSlashMenu — client command dispatch', () => {
  it('/new is not listed and does not start a session', () => {
    const startNewSession = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ startNewSession })))
    act(() => result.current.onInputChange('/'))
    expect(result.current.slashItems.find((i) => i.key === '/new')).toBeUndefined()
    expect(result.current.slashItems.find((i) => i.key === '/clear')).toBeUndefined()
    expect(startNewSession).not.toHaveBeenCalled()
  })

  it('/help appends a system message built from the command list', () => {
    const appendMessage = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ appendMessage })))
    act(() => result.current.onInputChange('/'))
    const item = result.current.slashItems.find((i) => i.key === '/help')!
    act(() => item.onSelect())
    expect(appendMessage).toHaveBeenCalledTimes(1)
    const msg = appendMessage.mock.calls[0][0]
    expect(msg.role).toBe('system')
    expect(msg.content).toContain('/help')
    expect(msg.content).not.toContain('/new')
    expect(msg.content).not.toContain('/clear')
    expect(msg.content).not.toContain('switch agents')
  })

  it('/model opens the model selector via the ui store', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    const item = result.current.slashItems.find((i) => i.key === '/model')!
    act(() => item.onSelect())
    expect(useUiStore.getState().modelSelectorOpen).toBe(true)
  })

  it('/agents opens the agent selector via the ui store', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    const item = result.current.slashItems.find((i) => i.key === '/agents')!
    act(() => item.onSelect())
    expect(useUiStore.getState().agentSelectorOpen).toBe(true)
  })

  it('/cancel delegates to cancelIfStreaming, not a local cancel', () => {
    const cancelIfStreaming = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ isStreaming: true, cancelIfStreaming })))
    act(() => result.current.onInputChange('/'))
    const item = result.current.slashItems.find((i) => i.key === '/cancel')!
    act(() => item.onSelect())
    expect(cancelIfStreaming).toHaveBeenCalledTimes(1)
  })

  // Session-search TWO MODES: /resume and /workspace open the SAME
  // SearchModal instance (searchModalOpen) but in DIFFERENT modes —
  // /resume must leave the panel in its original 'sessions' behavior
  // (unchanged per spec); /workspace must switch it into 'workspaces' mode
  // (openWorkspaceSwitcher, ui store) rather than reusing openSearchModal.
  it('/resume opens the search modal in sessions mode (unchanged)', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    const item = result.current.slashItems.find((i) => i.key === '/resume')!
    act(() => item.onSelect())
    expect(useUiStore.getState().searchModalOpen).toBe(true)
    expect(useUiStore.getState().searchModalMode).toBe('sessions')
    expect(useUiStore.getState().searchModalWorkspaceFilter).toBeNull()
  })

  it('/workspace opens the search modal in workspaces mode (openWorkspaceSwitcher, not openSearchModal)', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    const item = result.current.slashItems.find((i) => i.key === '/workspace')!
    act(() => item.onSelect())
    expect(useUiStore.getState().searchModalOpen).toBe(true)
    expect(useUiStore.getState().searchModalMode).toBe('workspaces')
    expect(useUiStore.getState().searchModalWorkspaceFilter).toBeNull()
  })

  it('/skills sets the input to "/skills" and reopens the menu filtered to skills only (D9)', () => {
    // Traces to: pkg/commands/cmd_skills.go (Name: "skills", Delivery: client)
    // and pkg/commands/surface_test.go's clientCmds list — "skills" is a real
    // client-delivery command, not just the isSkillsFilter text-match tested
    // elsewhere in this file. Exercise it via actual dispatch: selecting the
    // palette entry.
    const composerRuntime = makeComposerRuntime()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    act(() => result.current.onInputChange('/'))
    const item = result.current.slashItems.find((i) => i.key === '/skills')!
    act(() => item.onSelect())

    // executeSlashCommand clears the composer first, then runClientCommand's
    // "skills" branch re-sets it to "/skills" — the final call is what matters.
    expect(composerRuntime.setText).toHaveBeenLastCalledWith('/skills')
    expect(result.current.inputValue).toBe('/skills')
    expect(result.current.slashOpen).toBe(true)
    // D9: the re-opened menu is filtered to skills-only, proving this ran
    // the real command handler and not a no-op.
    expect(result.current.slashItems.every((i) => i.section === 'skills')).toBe(true)
    // Deferred item 3: alphabetical by name.
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['code-review', 'web-research'])
  })

  it('typing "/skills" + Enter (send-path interception) dispatches the same client command', () => {
    // Traces to: pkg/commands/surface_test.go clientCmds — confirms typing
    // "/skills"+Enter converges with palette selection, per interceptClientCommand's
    // own doc comment ("makes typing '/new'+Enter behave identically to palette selection").
    const composerRuntime = makeComposerRuntime('/skills')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))

    let intercepted = false
    act(() => { intercepted = result.current.interceptClientCommand() })

    expect(intercepted).toBe(true)
    expect(composerRuntime.setText).toHaveBeenLastCalledWith('/skills')
    expect(result.current.inputValue).toBe('/skills')
    expect(result.current.slashOpen).toBe(true)
  })

  it('an agent-delivery command inserts "<label> " as text instead of running locally', () => {
    const composerRuntime = makeComposerRuntime()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    act(() => result.current.onInputChange('/'))
    const item = result.current.slashItems.find((i) => i.key === '/handoff')!
    act(() => item.onSelect())
    expect(composerRuntime.setText).toHaveBeenCalledWith('/handoff ')
  })
})

describe('useSlashMenu — skill selection and ghost text', () => {
  it('selecting a skill with an argument_hint sets the input and shows that hint as ghost text', () => {
    const composerRuntime = makeComposerRuntime()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    act(() => result.current.onInputChange('/web'))
    const item = result.current.slashItems.find((i) => i.key === 'web-research')!
    act(() => item.onSelect())
    expect(composerRuntime.setText).toHaveBeenCalledWith('/web-research ')

    // Selecting a skill sets the input text internally too — simulate the
    // corresponding textarea onChange the real composer would fire.
    act(() => result.current.onInputChange('/web-research '))
    expect(result.current.showGhostText).toBe(true)
    expect(result.current.ghostText).toBe('<query>')
  })

  it('selecting a skill without an argument_hint falls back to the generic placeholder', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/code'))
    const item = result.current.slashItems.find((i) => i.key === 'code-review')!
    act(() => item.onSelect())
    act(() => result.current.onInputChange('/code-review '))
    expect(result.current.showGhostText).toBe(true)
    expect(result.current.ghostText).toBe('<message>')
  })

  it('ghost text disappears once the user types past the exact "/<id> " value', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/code'))
    const item = result.current.slashItems.find((i) => i.key === 'code-review')!
    act(() => item.onSelect())
    act(() => result.current.onInputChange('/code-review '))
    expect(result.current.showGhostText).toBe(true)

    act(() => result.current.onInputChange('/code-review do something'))
    expect(result.current.showGhostText).toBe(false)
  })
})

// Round-1 review addition (SD-C7/R3): the SAME argument_hint ghost-text
// mechanism the skill-selection tests above exercise also applies to
// agent-delivery COMMANDS (executeSlashCommand's `def.delivery === 'agent'`
// branch, useSlashMenu.ts:699-716) — e.g. `/goal` -> `<condition>`, `/loop`
// -> a loop-config hint. The shared `mockCommands` fixture's one
// agent-delivery entry ("/handoff") deliberately carries no argument_hint
// (see the existing "inserts as text instead of running locally" test
// above), so this uses `commandsOverride` to add agent-delivery commands
// that DO declare one.
describe('useSlashMenu — agent-delivery command argument_hint ghost text (SD-C7/R3)', () => {
  const goalCommand = {
    name: 'goal',
    label: '/goal',
    description: 'Run a goal-loop session',
    delivery: 'agent',
    available_while_streaming: false,
    argument_hint: '<condition>',
  }
  const loopCommand = {
    name: 'loop',
    label: '/loop',
    description: 'Run a bounded loop session',
    delivery: 'agent',
    available_while_streaming: false,
    argument_hint: '<task description>',
  }

  it('selecting "/goal" (agent-delivery) shows its argument_hint as ghost text, same as a skill', () => {
    commandsOverride = [...mockCommands, goalCommand, loopCommand]
    const composerRuntime = makeComposerRuntime()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    act(() => result.current.onInputChange('/goal'))
    const item = result.current.slashItems.find((i) => i.key === '/goal')!
    act(() => item.onSelect())

    // executeSlashCommand's agent branch inserts "/goal " as text (does not
    // send/clear) and arms the ghost — simulate the composer's own onChange
    // firing with that inserted value, matching the skill-ghost tests' shape.
    expect(composerRuntime.setText).toHaveBeenCalledWith('/goal ')
    act(() => result.current.onInputChange('/goal '))
    expect(result.current.showGhostText).toBe(true)
    expect(result.current.ghostText).toBe('<condition>')
  })

  it('selecting "/loop" (agent-delivery) shows ITS OWN argument_hint, not /goal\'s (proves the hint is per-command, not a hardcoded string)', () => {
    commandsOverride = [...mockCommands, goalCommand, loopCommand]
    const composerRuntime = makeComposerRuntime()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    act(() => result.current.onInputChange('/loop'))
    const item = result.current.slashItems.find((i) => i.key === '/loop')!
    act(() => item.onSelect())

    expect(composerRuntime.setText).toHaveBeenCalledWith('/loop ')
    act(() => result.current.onInputChange('/loop '))
    expect(result.current.showGhostText).toBe(true)
    expect(result.current.ghostText).toBe('<task description>')
  })

  it('an agent-delivery command with NO argument_hint ("/handoff") shows no ghost at all (unlike a skill, which falls back to the generic placeholder)', () => {
    // executeSlashCommand's agent branch (useSlashMenu.ts:699-716) only arms
    // ghostCommandLabel/ghostCommandArgumentHint when `def.argument_hint` is
    // truthy; with no hint it explicitly nulls both rather than falling back
    // to GHOST_TEXT_PLACEHOLDER the way completeSkillName does for skills —
    // an intentional asymmetry between the two ghost pairs.
    const composerRuntime = makeComposerRuntime()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    act(() => result.current.onInputChange('/'))
    const item = result.current.slashItems.find((i) => i.key === '/handoff')!
    act(() => item.onSelect())

    act(() => result.current.onInputChange('/handoff '))
    expect(result.current.showGhostText).toBe(false)
    expect(result.current.ghostText).toBe('')
  })

  it('the command ghost disappears once the user types past the exact "/<label> " value, same as the skill ghost', () => {
    commandsOverride = [...mockCommands, goalCommand]
    const composerRuntime = makeComposerRuntime()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    act(() => result.current.onInputChange('/goal'))
    const item = result.current.slashItems.find((i) => i.key === '/goal')!
    act(() => item.onSelect())
    act(() => result.current.onInputChange('/goal '))
    expect(result.current.showGhostText).toBe(true)

    act(() => result.current.onInputChange('/goal errors > 0'))
    expect(result.current.showGhostText).toBe(false)
  })

  it('selecting a skill ghost then a command ghost switches to the NEW pair (SD-C7 mutual exclusivity)', () => {
    commandsOverride = [...mockCommands, goalCommand]
    const composerRuntime = makeComposerRuntime()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))

    // Arm the skill ghost first.
    act(() => result.current.onInputChange('/web'))
    const skillItem = result.current.slashItems.find((i) => i.key === 'web-research')!
    act(() => skillItem.onSelect())
    act(() => result.current.onInputChange('/web-research '))
    expect(result.current.showGhostText).toBe(true)
    expect(result.current.ghostText).toBe('<query>')

    // Now select the agent-delivery command. `setInputValue` runs
    // synchronously inside `onSelect` (executeSlashCommand), so the new
    // command-ghost pair is already showing immediately — no separate
    // onInputChange needed. Critically, the text is `<condition>` (the
    // NEW pair), not a stale `<query>` left over from the skill selection.
    act(() => result.current.onInputChange('/goal'))
    const cmdItem = result.current.slashItems.find((i) => i.key === '/goal')!
    act(() => cmdItem.onSelect())
    expect(result.current.showGhostText).toBe(true)
    expect(result.current.ghostText).toBe('<condition>')
  })
})

describe('useSlashMenu — interceptClientCommand (send-path)', () => {
  it.each(['/new', '/clear', '/NEW', '/Clear'])(
    'does not intercept %s or start a session',
    (typed) => {
      const composerRuntime = makeComposerRuntime(typed)
      const startNewSession = vi.fn()
      const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime, startNewSession })))

      let intercepted = false
      act(() => { intercepted = result.current.interceptClientCommand() })

      expect(intercepted).toBe(false)
      expect(composerRuntime.setText).not.toHaveBeenCalled()
      expect(startNewSession).not.toHaveBeenCalled()
    },
  )

  it('does not intercept an unknown slash token — caller must dispatch it as a normal message', () => {
    const composerRuntime = makeComposerRuntime('/zzz hi')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))

    let intercepted = false
    act(() => { intercepted = result.current.interceptClientCommand() })

    expect(intercepted).toBe(false)
    expect(composerRuntime.setText).not.toHaveBeenCalled()
  })

  it('does not intercept an agent-delivery command — it must reach the backend', () => {
    const composerRuntime = makeComposerRuntime('/handoff do the thing')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))

    let intercepted = false
    act(() => { intercepted = result.current.interceptClientCommand() })

    expect(intercepted).toBe(false)
    expect(composerRuntime.setText).not.toHaveBeenCalled()
  })

  it('does not intercept plain text', () => {
    const composerRuntime = makeComposerRuntime('hello there')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))

    let intercepted = false
    act(() => { intercepted = result.current.interceptClientCommand() })

    expect(intercepted).toBe(false)
  })
})

// Readiness gate — a slash command submitted before GET /api/v1/commands has
// resolved.
//
// Observed for real (CI trace): the fetch was issued at t=3424ms and "/new"
// was submitted at t=3660ms, 236ms later. `allCommands` still held only the
// two synthetic client-only entries, the lookup missed,
// interceptClientCommand returned false, and the literal "/new" was
// dispatched to the backend AS A CHAT MESSAGE — where a server-side command
// handler answered it ("Chat history cleared!") and it was persisted into the
// transcript as something the user "said".
//
// The outcome these tests lock: a "/"-prefixed submit in that window is NEVER
// dispatched as chat, and nothing the user typed is dropped either.
describe('useSlashMenu — interceptClientCommand readiness gate (commands still loading)', () => {
  it.each(['/new', '/clear'])(
    'a held %s is delivered as an ordinary message once the list lands, and never starts a session',
    (typed) => {
      commandsQueryIsLoading = true
      const composerRuntime = makeComposerRuntime(typed)
      const startNewSession = vi.fn()
      const { result, rerender } = renderHook(() => useSlashMenu(baseParams({ composerRuntime, startNewSession })))

      let intercepted = false
      act(() => { intercepted = result.current.interceptClientCommand() })

      // Held while the list is in flight — nothing is dispatched yet.
      expect(intercepted).toBe(true)
      expect(composerRuntime.send).not.toHaveBeenCalled()
      expect(startNewSession).not.toHaveBeenCalled()

      commandsQueryIsLoading = false
      act(() => { rerender() })

      // /new and /clear are not client commands. The held text is sent.
      expect(startNewSession).not.toHaveBeenCalled()
      expect(composerRuntime.send).toHaveBeenCalledTimes(1)
      expect(composerRuntime.setText).not.toHaveBeenCalledWith('')
    },
  )

  it('a held submit that turns out NOT to be a client command is delivered, not dropped', () => {
    // The gate must not eat legitimate input: "/handoff …" is an
    // agent-delivery command and belongs on the wire.
    commandsQueryIsLoading = true
    const composerRuntime = makeComposerRuntime('/handoff do the thing')
    const startNewSession = vi.fn()
    const { result, rerender } = renderHook(() => useSlashMenu(baseParams({ composerRuntime, startNewSession })))

    act(() => { result.current.interceptClientCommand() })
    expect(composerRuntime.send).not.toHaveBeenCalled()

    commandsQueryIsLoading = false
    act(() => { rerender() })

    expect(composerRuntime.send).toHaveBeenCalledTimes(1)
    expect(startNewSession).not.toHaveBeenCalled()
    // Not cleared out from under the user before the send.
    expect(composerRuntime.setText).not.toHaveBeenCalledWith('')
  })

  it('an unknown "/zzz hi" is likewise held and then delivered verbatim', () => {
    commandsQueryIsLoading = true
    const composerRuntime = makeComposerRuntime('/zzz hi')
    const { result, rerender } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))

    act(() => { result.current.interceptClientCommand() })
    commandsQueryIsLoading = false
    act(() => { rerender() })

    expect(composerRuntime.send).toHaveBeenCalledTimes(1)
  })

  it('plain text is not held at all while the list loads — only "/"-prefixed input is', () => {
    commandsQueryIsLoading = true
    const composerRuntime = makeComposerRuntime('hello there')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))

    let intercepted = false
    act(() => { intercepted = result.current.interceptClientCommand() })

    expect(intercepted).toBe(false)
    expect(composerRuntime.send).not.toHaveBeenCalled()
  })

  it('a client command that IS already resolvable (the synthetic "/resume") runs immediately, without waiting for the fetch', () => {
    commandsQueryIsLoading = true
    const composerRuntime = makeComposerRuntime('/resume')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))

    let intercepted = false
    act(() => { intercepted = result.current.interceptClientCommand() })

    expect(intercepted).toBe(true)
    expect(useUiStore.getState().searchModalOpen).toBe(true)
    expect(composerRuntime.send).not.toHaveBeenCalled()
  })

  it('nothing is flushed when no submit was held (a plain list load must not send the composer on its own)', () => {
    commandsQueryIsLoading = true
    const composerRuntime = makeComposerRuntime('/new')
    const startNewSession = vi.fn()
    const { rerender } = renderHook(() => useSlashMenu(baseParams({ composerRuntime, startNewSession })))

    commandsQueryIsLoading = false
    act(() => { rerender() })

    expect(composerRuntime.send).not.toHaveBeenCalled()
    expect(startNewSession).not.toHaveBeenCalled()
  })

  it('a held submit flushes exactly once, not again on every later render', () => {
    commandsQueryIsLoading = true
    const composerRuntime = makeComposerRuntime('/zzz hi')
    const { result, rerender } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))

    act(() => { result.current.interceptClientCommand() })
    commandsQueryIsLoading = false
    act(() => { rerender() })
    act(() => { rerender() })
    act(() => { result.current.onInputChange('/zzz hi') })

    expect(composerRuntime.send).toHaveBeenCalledTimes(1)
  })
})

describe('useSlashMenu — onInputBlur (delayed close)', () => {
  it('does not close the menu synchronously, then closes after the 150ms delay if nothing else happened', () => {
    vi.useFakeTimers()
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    expect(result.current.slashOpen).toBe(true)

    act(() => result.current.onInputBlur())
    // Synchronously still open — the delay exists precisely so a menu-item's
    // mousedown can win the race before the blur-triggered close happens.
    expect(result.current.slashOpen).toBe(true)

    // Not yet elapsed.
    act(() => { vi.advanceTimersByTime(149) })
    expect(result.current.slashOpen).toBe(true)

    // Delay elapsed with nothing else happening — menu closes.
    act(() => { vi.advanceTimersByTime(1) })
    expect(result.current.slashOpen).toBe(false)
  })

  it('a mousedown-select within the delay window completes before the stale blur timer fires, and does not get undone by it', () => {
    vi.useFakeTimers()
    const appendMessage = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ appendMessage })))
    act(() => result.current.onInputChange('/'))

    act(() => result.current.onInputBlur())
    // Item's mousedown handler fires mid-delay (e.g. 50ms in) and wins the race.
    act(() => { vi.advanceTimersByTime(50) })
    const item = result.current.slashItems.find((i) => i.key === '/help')!
    act(() => item.onSelect())

    expect(appendMessage).toHaveBeenCalledTimes(1)
    expect(result.current.slashOpen).toBe(false)

    // The original blur timer (now stale) still fires at the 150ms mark —
    // it must be a harmless no-op, not resurrect/alter menu state.
    act(() => { vi.advanceTimersByTime(100) })
    expect(result.current.slashOpen).toBe(false)
    expect(appendMessage).toHaveBeenCalledTimes(1)
  })
})

// #472 — trace-evidenced slash-menu self-close race. A Playwright trace on
// cancel-cross-channel.spec.ts T24a caught the palette opening on "/new"
// (passed a toBeVisible check), then the ENTIRE listbox vanishing ~15ms
// later with the composer's text UNCHANGED the whole time (still exactly
// "/new") and nothing afterward re-running onInputChange to reopen it — the
// next .click() hung until timeout.
//
// Root cause: onInputBlur schedules a 150ms-delayed closeSlash so a
// mousedown-select can beat it (see the describe block above). That timer is
// fire-and-forget: if the input regains focus and the user types a fresh
// "/"-prefixed value before the 150ms elapse — exactly
// `input.click(); input.pressSequentially('/new')`, matching the real T24a
// steps (an agent-picker interaction blurs the composer immediately before
// this block) — the timer has no idea a brand-new menu opened in the
// meantime, fires anyway, and closes it even though nothing about the
// CURRENT state warrants a close.
//
// Two other candidates named in the #472 investigation comment — the
// `activeAgentId` reconciliation effect and the `commandsFirstLoadPending`
// flush effect — were audited and ruled out; see their own inline notes in
// useSlashMenu.ts and the "ruled-out suspects" block below.
describe('useSlashMenu — #472 stale blur-close timer race (trace-evidenced)', () => {
  it('typing a fresh command after refocus cancels the pending blur-close timer — the fresh menu survives past the original 150ms mark', () => {
    vi.useFakeTimers()
    const { result } = renderHook(() => useSlashMenu(baseParams()))

    // Open the menu, then blur — e.g. the agent-picker interaction that
    // precedes "/new" in the real T24a flow. Schedules closeSlash at T+150.
    act(() => result.current.onInputChange('/'))
    act(() => result.current.onInputBlur())

    // Refocus and type a fresh command WELL inside the 150ms window —
    // mirrors `input.click(); input.pressSequentially('/new')`.
    act(() => { vi.advanceTimersByTime(100) })
    act(() => result.current.onInputChange('/help'))
    expect(result.current.slashOpen).toBe(true)

    // Advance PAST the ORIGINAL blur's 150ms mark (50 more ms = T+150 from
    // the blur). Before the fix, the stale timer fires here and closes the
    // freshly-typed "/new" menu even though the text never stopped being
    // "/new", no new blur happened, and nothing was selected.
    act(() => { vi.advanceTimersByTime(50) })

    expect(result.current.slashOpen).toBe(true)
    expect(result.current.slashItems.map((i) => i.key)).toContain('/help')
  })

  it('keyboard navigation after refocus (no retyping) also cancels the pending blur-close timer', () => {
    vi.useFakeTimers()
    const { result } = renderHook(() => useSlashMenu(baseParams()))

    act(() => result.current.onInputChange('/'))
    act(() => result.current.onInputBlur())
    act(() => { vi.advanceTimersByTime(100) })

    // Refocus without retyping — e.g. clicking back into the composer and
    // navigating the still-open palette with the keyboard.
    act(() => result.current.handleKeyDown({ key: 'ArrowDown', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(result.current.slashOpen).toBe(true)

    act(() => { vi.advanceTimersByTime(50) })
    expect(result.current.slashOpen).toBe(true)
  })

  it('a SECOND blur before the first timer elapses replaces it rather than stacking two independent closes', () => {
    vi.useFakeTimers()
    const { result } = renderHook(() => useSlashMenu(baseParams()))

    act(() => result.current.onInputChange('/'))
    act(() => result.current.onInputBlur())
    act(() => { vi.advanceTimersByTime(100) })
    // Refocus + reblur without typing in between.
    act(() => result.current.onInputBlur())

    // 100ms after the SECOND blur (only 50ms past the first blur's own
    // 150ms mark) — the second timer has not reached its own 150ms yet.
    act(() => { vi.advanceTimersByTime(100) })
    expect(result.current.slashOpen).toBe(true)

    // The second timer's own 150ms mark (50ms further) closes it.
    act(() => { vi.advanceTimersByTime(50) })
    expect(result.current.slashOpen).toBe(false)
  })

  // Positive controls — the fix must not turn closeSlash into a no-op.
  it('control: a blur with no subsequent re-engagement still closes after 150ms', () => {
    vi.useFakeTimers()
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    act(() => result.current.onInputBlur())
    act(() => { vi.advanceTimersByTime(150) })
    expect(result.current.slashOpen).toBe(false)
  })

  it('control: clearing the input still closes the menu immediately (onInputChange\'s own explicit-reason close is untouched)', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/new'))
    expect(result.current.slashOpen).toBe(true)
    act(() => result.current.onInputChange(''))
    expect(result.current.slashOpen).toBe(false)
  })

  it('control: an explicit selection still closes the menu immediately', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    const item = result.current.slashItems.find((i) => i.key === '/help')!
    act(() => item.onSelect())
    expect(result.current.slashOpen).toBe(false)
  })

  it('control: an explicit Escape still closes the menu immediately', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/help'))
    act(() => result.current.handleKeyDown({ key: 'Escape', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(result.current.slashOpen).toBe(false)
  })
})

// #472 investigation — the two OTHER candidates named in the trace-evidenced
// lead comment. Both audited in useSlashMenu.ts (see their own inline
// notes): neither touches slashOpen/closeSlash under the "just typing, never
// submitted" shape this race actually has. These pin that finding as a
// regression guard, not a race reproduction — they already pass without the
// stale-timer fix above.
describe('useSlashMenu — #472 investigation: ruled-out suspects', () => {
  it('activeAgentId resolving (e.g. an auto-select settle after mount) does not touch an open "/" menu', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/help'))
    expect(result.current.slashOpen).toBe(true)

    // Simulate the auto-select-first-ready-agent settle (AgentPicker-style)
    // resolving activeAgentId from null to a real id, with NO mention
    // selection involved — exactly the reconciliation effect's own trigger.
    act(() => { useSessionStore.setState({ activeAgentId: 'mia' }) })

    expect(result.current.slashOpen).toBe(true)
    expect(result.current.slashItems.map((i) => i.key)).toContain('/help')
  })

  it('the commandsFirstLoadPending transition (list finishing its first load) does not touch an open "/" menu when nothing was deferred', () => {
    commandsQueryIsLoading = true
    const { result, rerender } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    expect(result.current.slashOpen).toBe(true)

    // The list lands — the exact transition the #472 comment named as a
    // candidate — with no Enter ever pressed (nothing deferred).
    commandsQueryIsLoading = false
    act(() => { rerender() })

    expect(result.current.slashOpen).toBe(true)
  })
})

describe('useSlashMenu — onHoverItem', () => {
  it('moves the keyboard highlight to the hovered index, and Enter selects that hovered item', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    expect(result.current.slashHighlight).toBe(0)

    // Index 4 in the unified list is "/agents" (resume, workspace, help,
    // model, agents). /new is no longer listed, so the old index 5 is /skills.
    act(() => result.current.onHoverItem(4))
    expect(result.current.slashHighlight).toBe(4)
    expect(result.current.slashItems[4].key).toBe('/agents')

    // Prove the hover-set index is the SAME one keyboard selection acts on —
    // not just a display-only value that Enter ignores.
    act(() => result.current.handleKeyDown({ key: 'Enter', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(useUiStore.getState().agentSelectorOpen).toBe(true)
  })

  it('a second hover moves the highlight again (differentiation — not stuck on the first value)', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))

    act(() => result.current.onHoverItem(2))
    expect(result.current.slashHighlight).toBe(2)

    act(() => result.current.onHoverItem(5))
    expect(result.current.slashHighlight).toBe(5)
  })
})

describe('useSlashMenu — cancelIfStreaming staleness across re-render', () => {
  // Traces to: useSlashMenu.ts's file-header comment on why
  // interceptClientCommand/runClientCommand are deliberately NOT wrapped in
  // useCallback — they close over cancelIfStreaming, whose identity changes
  // whenever isStreaming toggles (see useCancelState). Memoizing would risk
  // calling a stale cancelIfStreaming after such a toggle. This test proves
  // the current (unmemoized) implementation actually avoids that risk,
  // rather than merely asserting SOME cancelIfStreaming mock was called.
  it('after a re-render swaps cancelIfStreaming to a new identity, /cancel calls the NEW one, not the stale one', () => {
    const oldCancel = vi.fn()
    const newCancel = vi.fn()
    const composerRuntime = makeComposerRuntime('/cancel')
    const { result, rerender } = renderHook(
      (props: Parameters<typeof useSlashMenu>[0]) => useSlashMenu(props),
      { initialProps: baseParams({ composerRuntime, cancelIfStreaming: oldCancel }) },
    )

    // Simulate the parent's isStreaming toggling: useCancelState hands back
    // a brand-new cancelIfStreaming closure, and the parent re-renders this
    // hook with it — exactly the scenario the file-header comment reasons about.
    rerender(baseParams({ composerRuntime, cancelIfStreaming: newCancel }))

    act(() => { result.current.interceptClientCommand() })

    expect(newCancel).toHaveBeenCalledTimes(1)
    expect(oldCancel).not.toHaveBeenCalled()
  })

  it('same scenario via palette selection (executeSlashCommand path), not just interceptClientCommand', () => {
    const oldCancel = vi.fn()
    const newCancel = vi.fn()
    const composerRuntime = makeComposerRuntime()
    const { result, rerender } = renderHook(
      (props: Parameters<typeof useSlashMenu>[0]) => useSlashMenu(props),
      { initialProps: baseParams({ composerRuntime, isStreaming: true, cancelIfStreaming: oldCancel }) },
    )
    act(() => result.current.onInputChange('/'))

    rerender(baseParams({ composerRuntime, isStreaming: true, cancelIfStreaming: newCancel }))

    const item = result.current.slashItems.find((i) => i.key === '/cancel')!
    act(() => item.onSelect())

    expect(newCancel).toHaveBeenCalledTimes(1)
    expect(oldCancel).not.toHaveBeenCalled()
  })
})

describe('useSlashMenu — commandsError (LOW S8)', () => {
  it('exposes commandsError=true when fetchCommands(\'web\') errors, so the caller can render a fallback row', () => {
    commandsQueryIsError = true
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    expect(result.current.commandsError).toBe(true)
  })

  it('commandsError is false on the happy path', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    expect(result.current.commandsError).toBe(false)
  })

  it('keeps the menu open (shouldShowSlash=true) even when the error leaves zero matching items', () => {
    // With the commands query errored, allCommands is just the two synthetic
    // client-only entries ("/resume", "/workspace") — a prefix that matches
    // neither of those nor any skill leaves slashItems empty. shouldShowSlash
    // must still be true so the caller's "Commands unavailable" row has
    // somewhere to render.
    commandsQueryIsError = true
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/zzz'))
    expect(result.current.slashItems).toHaveLength(0)
    expect(result.current.shouldShowSlash).toBe(true)
  })

  it('does not force the menu open when nothing has been typed', () => {
    commandsQueryIsError = true
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    expect(result.current.shouldShowSlash).toBe(false)
  })

  // Documented per the task, not a new behavior: with the commands list
  // unavailable, only the two synthetic client-only commands ("/resume",
  // "/workspace") still resolve locally. Any other slash text — even the
  // name of a real backend command — can no longer be matched, so
  // interceptClientCommand correctly returns false and the caller's normal
  // send path takes over (no silent swallow, no crash; the degradation is
  // the visible "Commands unavailable" row, not a broken send).
  it('interceptClientCommand returns false for a command name that can no longer resolve locally', () => {
    commandsQueryIsError = true
    const composerRuntime = makeComposerRuntime('/help')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    let intercepted = false
    act(() => { intercepted = result.current.interceptClientCommand() })
    expect(intercepted).toBe(false)
  })

  it('the synthetic client-only "/resume" command still intercepts even when the backend commands query errors', () => {
    commandsQueryIsError = true
    const composerRuntime = makeComposerRuntime('/resume')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    let intercepted = false
    act(() => { intercepted = result.current.interceptClientCommand() })
    expect(intercepted).toBe(true)
    expect(useUiStore.getState().searchModalOpen).toBe(true)
    // Two-modes contract: /resume must land in 'sessions' mode, unchanged.
    expect(useUiStore.getState().searchModalMode).toBe('sessions')
  })

  // Mirrors the "/resume" case directly above: "/workspace" (session-search
  // enhancement) is the second synthetic client-only command and must
  // survive the same backend-outage degradation, opening the SAME search
  // modal instance but in its 'workspaces' mode (no workspace preselected).
  it('the synthetic client-only "/workspace" command still intercepts even when the backend commands query errors', () => {
    commandsQueryIsError = true
    const composerRuntime = makeComposerRuntime('/workspace')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    let intercepted = false
    act(() => { intercepted = result.current.interceptClientCommand() })
    expect(intercepted).toBe(true)
    expect(useUiStore.getState().searchModalOpen).toBe(true)
    // Two-modes contract: /workspace must land in 'workspaces' mode, not
    // 'sessions' — this is the whole point of the openWorkspaceSwitcher split.
    expect(useUiStore.getState().searchModalMode).toBe('workspaces')
  })
})

// Root-cause regression (cancel-cross-channel T24a investigation,
// sendfile-fix): ChatScreen.tsx's `inputEnabled` gate is
// `!agentRemoved && !isReplaying && !(reconnectPhase === 'gave_up') &&
// isConnected` — it depends ONLY on the WS being connected, never on this
// hook's own `['commands','web']` query having resolved. So the composer
// accepts (and can submit) input the instant the socket connects, which can
// easily be BEFORE the separate REST fetch for the commands list lands —
// e.g. a fast typed "/new"+Enter (or Playwright's `input.fill('/new');
// input.press('Enter')`, which has no reason to wait on it either) right
// after page load.
//
// Before the fix: `allCommands` was built from `[resume, workspace,
// ...commands]` where `commands` defaults to `[]` until the query resolves
// — during that ordinary, non-error loading window, "/new" was not found in
// `allCommands`, `interceptClientCommand()` returned false, and the
// caller's `onSubmit`/Send handlers only call `e.preventDefault()` when it
// returns true — so the literal text "/new" fell through and was sent to
// the backend as an ordinary chat message. Because this happens before the
// user has (necessarily) picked a specific agent, that phantom message
// mints/continues a session bound to whatever agent is currently active
// (typically the default), and the session_started ack for it can arrive
// AFTER a later, correct agent switch and silently revert the picker/active
// agent back to the default — the exact "picker showed Jim, then the turn
// ran as Mia" symptom from the T24a investigation.
//
// Fixed via the readiness gate + deferred flush covered by the
// "interceptClientCommand readiness gate (commands still loading)" describe
// block above — a held submit is never dispatched to the backend and is
// replayed for real once the list resolves. This is DISTINCT from the
// "commandsError" describe block above that one: that block models a
// CONFIRMED, permanent query failure, where the team deliberately decided
// most client commands should degrade (see its own doc comment); the
// readiness-gate block models the ordinary, transient "hasn't resolved YET"
// window, which is not a confirmed failure and was never a deliberate
// degradation — it is the actual bug.

// bugfixes3 fix round — 14-reviewer sign-off punch list (items A/B/C/D).
describe('useSlashMenu — composerRuntime subscription resync (Fix A, bugfixes3 sign-off)', () => {
  // Verified against node_modules/@assistant-ui/react/dist/utils/
  // createActionButton.js + primitives/composer/ComposerSend.js:
  // ComposerPrimitive.Send is a plain `type="button"` whose onClick calls
  // `composer.send()` directly — it does NOT go through
  // ComposerPrimitive.Root's onSubmit (`form.requestSubmit()`), so the
  // Fix-4 mirror resync living in ChatScreen.tsx's onSubmit never runs for
  // a click-Send. This composerRuntime exposes a capturable/invocable
  // `subscribe` callback so the test can simulate the runtime clearing its
  // own text out-of-band, exactly the way a click-Send does.
  function makeSubscribableComposerRuntime(initialText = '') {
    let currentText = initialText
    let notify: (() => void) | undefined
    const runtime = {
      getState: () => ({ text: currentText }),
      setText: vi.fn((t: string) => { currentText = t }),
      addAttachment: vi.fn(),
      subscribe: vi.fn((cb: () => void) => {
        notify = cb
        return vi.fn()
      }),
    } as unknown as ComposerRuntime & { setText: ReturnType<typeof vi.fn>; subscribe: ReturnType<typeof vi.fn> }
    return {
      runtime,
      setExternalText: (t: string) => { currentText = t },
      notifySubscribers: () => notify?.(),
    }
  }

  it('a runtime-driven text clear that bypasses onChange (e.g. mouse-click Send) still resyncs the mirror, so a stale slash mirror cannot reopen the menu', () => {
    const { runtime, setExternalText, notifySubscribers } = makeSubscribableComposerRuntime()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime: runtime })))

    setExternalText('/help')
    act(() => result.current.onInputChange('/help'))
    expect(result.current.slashOpen).toBe(true)
    expect(result.current.isMentionMode).toBe(false)

    // A leading "@" is not a menu. The mirror may hold it; the menu stays shut.
    setExternalText('@x')
    act(() => result.current.onInputChange('@x'))
    expect(result.current.isMentionMode).toBe(false)
    expect(result.current.shouldShowSlash).toBe(false)

    setExternalText('')
    act(() => { notifySubscribers() })

    expect(result.current.inputValue).toBe('')
    expect(result.current.slashOpen).toBe(false)

    act(() => result.current.handleKeyDown({ key: 'ArrowDown', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(result.current.slashOpen).toBe(false)
    expect(result.current.shouldShowSlash).toBe(false)
    expect(result.current.slashItems).toHaveLength(0)
  })

  it('subscribes to the composerRuntime on mount and unsubscribes on unmount', () => {
    const composerRuntime = makeComposerRuntime()
    const subscribeMock = composerRuntime.subscribe as unknown as ReturnType<typeof vi.fn>
    const { unmount } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    expect(subscribeMock).toHaveBeenCalledTimes(1)
    const unsubscribe = subscribeMock.mock.results[0]!.value
    unmount()
    expect(unsubscribe).toHaveBeenCalledTimes(1)
  })
})

describe('useSlashMenu — IME composition guard (Fix C, bugfixes3 sign-off)', () => {
  it('Enter during an IME composition does not select from the menu or wipe the draft', () => {
    const appendMessage = vi.fn()
    const composerRuntime = makeComposerRuntime('/help')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime, appendMessage })))
    act(() => result.current.onInputChange('/help'))

    const imeEnter = { key: 'Enter', preventDefault: vi.fn(), nativeEvent: { isComposing: true } } as unknown as React.KeyboardEvent
    act(() => result.current.handleKeyDown(imeEnter))

    // Nothing was selected — preventDefault was not called, the menu is
    // still open, and /help did not run.
    expect(imeEnter.preventDefault).not.toHaveBeenCalled()
    expect(result.current.slashOpen).toBe(true)
    expect(appendMessage).not.toHaveBeenCalled()

    // A plain (non-composing) Enter on the SAME highlighted row still
    // selects normally — proves the guard is IME-specific, not a general
    // Enter regression.
    const plainEnter = { key: 'Enter', preventDefault: vi.fn() } as unknown as React.KeyboardEvent
    act(() => result.current.handleKeyDown(plainEnter))
    expect(plainEnter.preventDefault).toHaveBeenCalled()
    expect(appendMessage).toHaveBeenCalledTimes(1)
  })
})

describe('useSlashMenu — empty-items keyboard handling (Fix D, bugfixes3 sign-off)', () => {
  it('commandsError + "/nomatch" + Enter: does not block submit; ArrowDown leaves the highlight at 0 (no NaN)', () => {
    commandsQueryIsError = true
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/nomatch'))
    expect(result.current.slashItems).toHaveLength(0)
    expect(result.current.shouldShowSlash).toBe(true)

    const enterEvent = { key: 'Enter', preventDefault: vi.fn() } as unknown as React.KeyboardEvent
    act(() => result.current.handleKeyDown(enterEvent))
    // Enter falls through to the caller's normal submit path — not swallowed.
    expect(enterEvent.preventDefault).not.toHaveBeenCalled()

    const downEvent = { key: 'ArrowDown', preventDefault: vi.fn() } as unknown as React.KeyboardEvent
    act(() => result.current.handleKeyDown(downEvent))
    expect(result.current.slashHighlight).toBe(0)
    expect(downEvent.preventDefault).not.toHaveBeenCalled()

    // Escape still closes the (contentless) menu.
    act(() => result.current.handleKeyDown({ key: 'Escape', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(result.current.slashOpen).toBe(false)
  })
})

// Deferred item 4: prefix-then-substring matching, consistently across all
// three sections. Before this, matching was prefix-only everywhere
// (commands: label prefix; skills: id/name prefix; agents: name prefix) —
// e.g. "@assist" could not find an agent named "Code Assistant" because
// "assist" is not a PREFIX of "code assistant", only a substring of it.
describe('useSlashMenu — prefix-then-substring matching (deferred item 4)', () => {
  it('"/ancel" finds "/cancel" via the substring rank for commands (not just a label prefix)', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/ancel'))
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['/cancel'])
  })

  it('"/eview" finds "code-review" via the substring rank for skills (not just an id/name prefix)', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/eview'))
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['code-review'])
  })

  it('commands: prefix matching still works unchanged through the same rankByFilter path substring matching now shares', () => {
    // Sanity check that routing commands through the shared rankByFilter
    // helper (used for the prefix-vs-substring ranking proven above via
    // agents) didn't regress plain prefix matching for commands.
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/ag'))
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['/agents'])
  })
})
