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

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[]; enabled?: boolean }) => {
      if (opts.enabled === false) return { data: [], isError: false, refetch: vi.fn() }
      const key = opts.queryKey
      if (Array.isArray(key) && key[0] === 'commands') {
        return commandsQueryIsError
          ? { data: undefined, isError: true, refetch: vi.fn() }
          : { data: mockCommands, isError: false, refetch: vi.fn() }
      }
      if (Array.isArray(key) && key[0] === 'skills') return { data: skillsOverride ?? mockSkills, isError: false, refetch: vi.fn() }
      if (Array.isArray(key) && key[0] === 'agents') return { data: mentionAgentsOverride ?? mockAgents, isError: false, refetch: vi.fn() }
      return { data: [], isError: false, refetch: vi.fn() }
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

function makeComposerRuntime(text = '') {
  return {
    getState: () => ({ text }),
    setText: vi.fn(),
    addAttachment: vi.fn(),
    subscribe: vi.fn(() => vi.fn()),
  } as unknown as ComposerRuntime & { setText: ReturnType<typeof vi.fn> }
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
    })
  })
  commandsQueryIsError = false
  mentionAgentsOverride = null
  skillsOverride = null
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
      '/resume', '/workspace', '/new', '/help', '/model', '/agents', '/skills', '/cancel', '/handoff', 'code-review', 'web-research',
    ])
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
    act(() => result.current.onInputChange('/ne'))
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['/new'])
  })

  it('shows no items for an unmatched prefix (Issue 3 — menu just disappears)', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/zzz'))
    expect(result.current.slashItems).toHaveLength(0)
    expect(result.current.shouldShowSlash).toBe(false)
  })

  it('does not surface the "clear" alias as its own palette entry — prefix filtering matches canonical names only', () => {
    // /new's hidden alias is "clear" (pkg/commands/cmd_clear.go). The
    // palette list must never grow a separate "/clear" entry — only the
    // canonical "/new" label is filterable/visible.
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/cl'))
    expect(result.current.slashItems).toHaveLength(0)
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
    const startNewSession = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ startNewSession })))
    act(() => result.current.onInputChange('/'))
    // Move highlight off "/resume" (index 0, whose onSelect touches the
    // global ui store) past "/workspace" (index 1, session-search
    // enhancement's second synthetic client-only entry — also touches the
    // global ui store) onto "/new" (index 2, whose onSelect calls the
    // locally-mocked startNewSession) so this test's assertions stay
    // self-contained.
    act(() => result.current.handleKeyDown({ key: 'ArrowDown', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    act(() => result.current.handleKeyDown({ key: 'ArrowDown', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(result.current.slashItems[result.current.slashHighlight].key).toBe('/new')

    const shiftEnter = { key: 'Enter', shiftKey: true, preventDefault: vi.fn() } as unknown as React.KeyboardEvent
    act(() => result.current.handleKeyDown(shiftEnter))

    // Nothing was selected — preventDefault was not called, the menu is
    // still open, and no command handler ran.
    expect(shiftEnter.preventDefault).not.toHaveBeenCalled()
    expect(result.current.slashOpen).toBe(true)
    expect(startNewSession).not.toHaveBeenCalled()

    // Plain Enter (no Shift) on the SAME highlighted row still selects
    // normally, proving the guard is Shift-specific, not a general Enter
    // regression.
    const plainEnter = { key: 'Enter', shiftKey: false, preventDefault: vi.fn() } as unknown as React.KeyboardEvent
    act(() => result.current.handleKeyDown(plainEnter))
    expect(plainEnter.preventDefault).toHaveBeenCalled()
    expect(startNewSession).toHaveBeenCalledTimes(1)
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
  it('/new calls startNewSession', () => {
    const startNewSession = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ startNewSession })))
    act(() => result.current.onInputChange('/'))
    const item = result.current.slashItems.find((i) => i.key === '/new')!
    act(() => item.onSelect())
    expect(startNewSession).toHaveBeenCalledTimes(1)
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
    expect(msg.content).toContain('/new')
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

describe('useSlashMenu — interceptClientCommand (send-path)', () => {
  it('intercepts an exact client-delivery command (canonical "/new") and runs it locally', () => {
    const composerRuntime = makeComposerRuntime('/new')
    const startNewSession = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime, startNewSession })))

    let intercepted = false
    act(() => { intercepted = result.current.interceptClientCommand() })

    expect(intercepted).toBe(true)
    expect(composerRuntime.setText).toHaveBeenCalledWith('')
    expect(startNewSession).toHaveBeenCalledTimes(1)
  })

  it('intercepts the legacy "/clear" alias and runs the same client command as "/new"', () => {
    // Backend renamed /clear -> /new with Aliases: ["clear"] (pkg/commands/cmd_clear.go).
    // Typing the old alias must still resolve — not fall through to the LLM as chat text.
    const composerRuntime = makeComposerRuntime('/clear')
    const startNewSession = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime, startNewSession })))

    let intercepted = false
    act(() => { intercepted = result.current.interceptClientCommand() })

    expect(intercepted).toBe(true)
    expect(composerRuntime.setText).toHaveBeenCalledWith('')
    expect(startNewSession).toHaveBeenCalledTimes(1)
  })

  // LOW S7/C3: "/Clear" or "/NEW" + Enter was silently sent to the LLM as
  // chat text — the palette filter itself is case-insensitive (menuFilter
  // lowercases), but interceptClientCommand's label/alias comparison wasn't.
  it('intercepts an uppercase "/CLEAR" alias case-insensitively', () => {
    const composerRuntime = makeComposerRuntime('/CLEAR')
    const startNewSession = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime, startNewSession })))

    let intercepted = false
    act(() => { intercepted = result.current.interceptClientCommand() })

    expect(intercepted).toBe(true)
    expect(composerRuntime.setText).toHaveBeenCalledWith('')
    expect(startNewSession).toHaveBeenCalledTimes(1)
  })

  it('intercepts a mixed-case "/New" canonical label case-insensitively', () => {
    const composerRuntime = makeComposerRuntime('/New')
    const startNewSession = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime, startNewSession })))

    let intercepted = false
    act(() => { intercepted = result.current.interceptClientCommand() })

    expect(intercepted).toBe(true)
    expect(composerRuntime.setText).toHaveBeenCalledWith('')
    expect(startNewSession).toHaveBeenCalledTimes(1)
  })

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
    const startNewSession = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ startNewSession })))
    act(() => result.current.onInputChange('/'))

    act(() => result.current.onInputBlur())
    // Item's mousedown handler fires mid-delay (e.g. 50ms in) and wins the race.
    act(() => { vi.advanceTimersByTime(50) })
    const item = result.current.slashItems.find((i) => i.key === '/new')!
    act(() => item.onSelect())

    expect(startNewSession).toHaveBeenCalledTimes(1)
    expect(result.current.slashOpen).toBe(false)

    // The original blur timer (now stale) still fires at the 150ms mark —
    // it must be a harmless no-op, not resurrect/alter menu state.
    act(() => { vi.advanceTimersByTime(100) })
    expect(result.current.slashOpen).toBe(false)
    expect(startNewSession).toHaveBeenCalledTimes(1)
  })
})

describe('useSlashMenu — onHoverItem', () => {
  it('moves the keyboard highlight to the hovered index, and Enter selects that hovered item', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    expect(result.current.slashHighlight).toBe(0)

    // Index 5 in the unified list is "/agents" (resume, workspace, new, help,
    // model, agents, ...) — shifted by one from "/workspace" (session-search
    // enhancement's second synthetic client-only entry, inserted right after
    // "/resume").
    act(() => result.current.onHoverItem(5))
    expect(result.current.slashHighlight).toBe(5)
    expect(result.current.slashItems[5].key).toBe('/agents')

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

describe('useSlashMenu — "@" agent-mention menu', () => {
  it('shows nothing for "@" mid-text — only a leading "@" triggers (same rule as "/")', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('hello @x'))
    expect(result.current.shouldShowSlash).toBe(false)
    expect(result.current.isMentionMode).toBe(false)
    expect(result.current.slashItems).toHaveLength(0)
  })

  it('a bare "@" opens the menu listing every scoped chat agent (worker excluded) with section "agents"', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('@'))
    expect(result.current.isMentionMode).toBe(true)
    expect(result.current.shouldShowSlash).toBe(true)
    // Deferred item 3: alphabetical by name — "Jim" < "Mars" < "Mia" —
    // rather than API/array order (mockAgents declares mia, jim, mars).
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['jim', 'mars', 'mia'])
    expect(result.current.slashItems.every((i) => i.section === 'agents')).toBe(true)
  })

  it('the SECOND typed character (the first after "@") already filters the list', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('@'))
    expect(result.current.slashItems).toHaveLength(3)
    act(() => result.current.onInputChange('@m'))
    // Deferred item 4: "Mars"/"Mia" prefix-match "m" (alphabetical within
    // the prefix rank: "Mars" < "Mia"); "Jim" now ALSO matches via the
    // substring rank ("jim" contains "m", just not at the start), ranked
    // after both prefix matches.
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['mars', 'mia', 'jim'])
  })

  it('filtering is case-insensitive by NAME prefix — "@M" matches identically to "@m"', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('@M'))
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['mars', 'mia', 'jim'])
  })

  // Fix 7 (bugfixes3 review): inverted from "matches by agent id prefix
  // too" — user-created agent ids are UUIDs/arbitrary strings unrelated to
  // the visible label, so an id-prefix clause surfaced agents into "@a"
  // whose id happened to start with "a" but whose NAME had nothing to do
  // with what was typed. Filtering is NAME-prefix only now. A fixture whose
  // id and name diverge proves both directions: the divergent id must NOT
  // leak a match, and the real name must still match normally.
  it('matches by agent NAME (prefix or substring) only — a divergent id does not leak a match', () => {
    mentionAgentsOverride = [
      ...mockAgents,
      makeAgent({ id: 'ops-7', name: 'Marcus', type: 'core', status: 'active', description: 'Support' }),
    ]
    const { result } = renderHook(() => useSlashMenu(baseParams()))

    // Matches by NAME prefix ("Marcus" starts with "marc").
    act(() => result.current.onInputChange('@marc'))
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['ops-7'])

    // Does NOT match by the divergent ID prefix ("ops-7" starts with "op",
    // but the name "Marcus" does not) — even under substring matching:
    // "marcus" does not contain "op" either.
    act(() => result.current.onInputChange('@op'))
    expect(result.current.slashItems).toHaveLength(0)

    // Deferred item 4: extends (does not weaken) the above — "arc" is a
    // SUBSTRING of "Marcus" (not a prefix: "marcus" does not start with
    // "arc"), so it must now ALSO find Marcus via the substring-match rank,
    // while the divergent id is still never consulted.
    act(() => result.current.onInputChange('@arc'))
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['ops-7'])
  })

  it('hides the menu entirely when no agent matches the filter', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('@zzz'))
    expect(result.current.slashItems).toHaveLength(0)
    expect(result.current.shouldShowSlash).toBe(false)
  })

  it('a commands-fetch error does NOT force the "@" menu open (LOW S8 fallback is "/"-only)', () => {
    commandsQueryIsError = true
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('@zzz'))
    expect(result.current.shouldShowSlash).toBe(false)
  })

  it('deleting back to empty input closes the menu', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('@m'))
    expect(result.current.slashOpen).toBe(true)
    act(() => result.current.onInputChange(''))
    expect(result.current.slashOpen).toBe(false)
    expect(result.current.shouldShowSlash).toBe(false)
  })

  it('ArrowDown/ArrowUp cycle the highlight across agent rows, same as the "/" menu', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('@'))
    expect(result.current.slashHighlight).toBe(0)

    const down = { key: 'ArrowDown', preventDefault: vi.fn() } as unknown as React.KeyboardEvent
    act(() => result.current.handleKeyDown(down))
    expect(result.current.slashHighlight).toBe(1)

    const up = { key: 'ArrowUp', preventDefault: vi.fn() } as unknown as React.KeyboardEvent
    act(() => result.current.handleKeyDown(up))
    act(() => result.current.handleKeyDown(up))
    expect(result.current.slashHighlight).toBe(result.current.slashItems.length - 1)
  })

  it('Escape closes the "@" menu', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('@'))
    expect(result.current.slashOpen).toBe(true)
    act(() => result.current.handleKeyDown({ key: 'Escape', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(result.current.slashOpen).toBe(false)
  })

  it('Enter on the highlighted row selects that agent: sets it active (preserving activeSessionId), clears the composer, closes the menu', () => {
    act(() => {
      useSessionStore.setState({ activeAgentId: 'jim', activeSessionId: 'sess_123' })
    })
    const composerRuntime = makeComposerRuntime('@m')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    act(() => result.current.onInputChange('@m'))
    // Deferred item 3: '@m' matches ['mars', 'mia'] in alphabetical order —
    // highlight 0 -> 'mars' (was 'mia' under the old array-order matching).
    act(() => result.current.handleKeyDown({ key: 'Enter', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))

    expect(useSessionStore.getState().activeAgentId).toBe('mars')
    // activeSessionId must be PRESERVED, not detached (same SC-005 contract as AgentPicker).
    expect(useSessionStore.getState().activeSessionId).toBe('sess_123')
    expect(composerRuntime.setText).toHaveBeenLastCalledWith('')
    expect(result.current.inputValue).toBe('')
    expect(result.current.slashOpen).toBe(false)
  })

  it('clicking (onSelect) a row does the same as Enter — verifies the mouse path independent of keyboard nav', () => {
    act(() => {
      useSessionStore.setState({ activeAgentId: null, activeSessionId: 'sess_click' })
    })
    const composerRuntime = makeComposerRuntime()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    act(() => result.current.onInputChange('@ji'))
    const item = result.current.slashItems.find((i) => i.key === 'jim')!
    act(() => item.onSelect())

    expect(useSessionStore.getState().activeAgentId).toBe('jim')
    expect(useSessionStore.getState().activeSessionId).toBe('sess_click')
    expect(composerRuntime.setText).toHaveBeenLastCalledWith('')
    expect(result.current.slashOpen).toBe(false)
  })

  it('passes the agent type through to setActiveSession (not hardcoded/null)', () => {
    const composerRuntime = makeComposerRuntime()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    act(() => result.current.onInputChange('@ma'))
    const item = result.current.slashItems.find((i) => i.key === 'mars')!
    act(() => item.onSelect())
    expect(useSessionStore.getState().activeAgentType).toBe('Main')
  })

  it('marks the currently active agent row with isActiveAgent, and no other row', () => {
    act(() => {
      useSessionStore.setState({ activeAgentId: 'jim' })
    })
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('@'))
    const flags = result.current.slashItems.map((i) => [i.key, i.isActiveAgent])
    // Deferred item 3: alphabetical order — jim, mars, mia.
    expect(flags).toEqual([['jim', true], ['mars', false], ['mia', false]])
  })

  it('agent rows carry color/icon/description through for the render layer', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('@mia'))
    const item = result.current.slashItems[0]
    expect(item.label).toBe('@Mia')
    expect(item.description).toBe('Assistant')
    expect(item.agentColor).toBe('#111111')
  })

  it('"/" and "@" triggers never mix — "/" still shows commands+skills, unaffected by agent data', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/'))
    expect(result.current.isMentionMode).toBe(false)
    expect(result.current.slashItems.every((i) => i.section === 'commands' || i.section === 'skills')).toBe(true)
  })

  // Gap 1 (test-quality review): keep a handle on the keydown event object
  // in the Enter-select path and assert preventDefault was called — guards
  // the select+send double-fire (a textarea's native Enter-submits-a-form
  // behavior must never fire alongside the menu's own selection).
  it('Enter-select calls preventDefault on the keydown event (guards the select+send double-fire)', () => {
    const composerRuntime = makeComposerRuntime('@m')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    act(() => result.current.onInputChange('@m'))

    const enterEvent = { key: 'Enter', preventDefault: vi.fn() } as unknown as React.KeyboardEvent
    act(() => result.current.handleKeyDown(enterEvent))

    expect(enterEvent.preventDefault).toHaveBeenCalledTimes(1)
    // Prove the selection itself actually ran too — preventDefault firing
    // in isolation (with no real selection) would be a hollow assertion.
    // Deferred item 3: '@m' matches ['mars', 'mia'] alphabetically — highlight
    // 0 is 'mars'.
    expect(useSessionStore.getState().activeAgentId).toBe('mars')
  })

  // Gap 2: highlight resets to 0 whenever the visible list narrows, so a
  // stale out-of-range highlight index can never make Enter select nothing
  // (a no-op) or the wrong row.
  it('highlight resets to 0 when narrowing the filter shrinks the list (guards the out-of-range no-op)', () => {
    const composerRuntime = makeComposerRuntime('@j')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    act(() => result.current.onInputChange('@'))
    act(() => result.current.handleKeyDown({ key: 'ArrowDown', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    act(() => result.current.handleKeyDown({ key: 'ArrowDown', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    // Deferred item 3: bare "@" order is alphabetical (jim, mars, mia) — the
    // third row is now "mia", not "mars".
    expect(result.current.slashHighlight).toBe(2)
    expect(result.current.slashItems[2].key).toBe('mia')

    act(() => result.current.onInputChange('@j'))
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['jim'])
    expect(result.current.slashHighlight).toBe(0)

    // Enter on the reset highlight selects the one remaining match, "jim" —
    // not a no-op and not the stale "mars" position.
    act(() => result.current.handleKeyDown({ key: 'Enter', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(useSessionStore.getState().activeAgentId).toBe('jim')
  })

  // Gap 3: pins the reopen-on-next-keystroke design — Escape only closes
  // slashOpen, it does not clear inputValue, so the very next keystroke
  // (which re-runs onInputChange's startsWith("@") check) reopens the menu.
  it('Escape closes the menu, then typing the next character reopens it', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('@'))
    expect(result.current.slashOpen).toBe(true)

    act(() => result.current.handleKeyDown({ key: 'Escape', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(result.current.slashOpen).toBe(false)

    act(() => result.current.onInputChange('@m'))
    expect(result.current.slashOpen).toBe(true)
    // Deferred items 3/4: alphabetical within the prefix rank, plus "jim"
    // now matching via the substring rank (see the sibling test above).
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['mars', 'mia', 'jim'])
  })

  // Gap 4: streaming interaction — the mention menu has NO
  // available_while_streaming-style filtering (unlike the "/" commands
  // section); this is intentional mid-stream parity with AgentPicker, which
  // is never disabled while streaming either. Selecting an agent mid-stream
  // still just switches the active agent — it never sends anything.
  describe('streaming interaction', () => {
    it('while streaming, "@" still lists every scoped agent — no filtering applied', () => {
      const { result } = renderHook(() => useSlashMenu(baseParams({ isStreaming: true })))
      act(() => result.current.onInputChange('@'))
      // Deferred item 3: alphabetical order — jim, mars, mia.
      expect(result.current.slashItems.map((i) => i.key)).toEqual(['jim', 'mars', 'mia'])
      expect(result.current.slashItems.every((i) => i.section === 'agents')).toBe(true)
    })

    it('while streaming, Enter-select still calls setActiveSession, and never touches appendMessage/startNewSession (no send)', () => {
      const composerRuntime = makeComposerRuntime('@m')
      const appendMessage = vi.fn()
      const startNewSession = vi.fn()
      const { result } = renderHook(() =>
        useSlashMenu(baseParams({ isStreaming: true, composerRuntime, appendMessage, startNewSession })),
      )
      act(() => result.current.onInputChange('@m'))
      act(() => result.current.handleKeyDown({ key: 'Enter', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))

      // Deferred item 3: '@m' matches ['mars', 'mia'] alphabetically — highlight 0 is 'mars'.
      expect(useSessionStore.getState().activeAgentId).toBe('mars')
      expect(composerRuntime.setText).toHaveBeenLastCalledWith('')
      // No message-producing side effect fired — selection is a pure
      // agent-switch, not a send.
      expect(appendMessage).not.toHaveBeenCalled()
      expect(startNewSession).not.toHaveBeenCalled()
    })
  })

  // Gap 5: the mention list caps at 8 rows (matches the skills section's own
  // cap), and the cap is re-applied AFTER filtering — narrowing the query
  // can surface a match that was pushed past the cap in the unfiltered
  // (bare "@") view.
  // Deferred items 3/4 (formerly "Fix 6" — cap-only, array order): the
  // mention list still caps at 8 rows, but WHICH 8 survive is now
  // deterministic — agents are sorted alphabetically by name BEFORE the cap
  // (useSlashMenu.ts's `sortedAgents`) — and the overflow is exposed via
  // `agentsHiddenCount` instead of silently vanishing.
  describe('mention-list cap + hidden count (deferred items 3/4)', () => {
    // Agent01..Agent10 (zero-padded so lexicographic order matches numeric
    // order) — a clean fixture for testing the alphabetical-then-cap
    // behavior without needing a name like "Zed" to reason about sort
    // position.
    function tenAgentsFixture() {
      return Array.from({ length: 10 }, (_, i) =>
        makeAgent({ id: `ag${String(i + 1).padStart(2, '0')}`, name: `Agent${String(i + 1).padStart(2, '0')}`, type: 'Main', status: 'active' }))
    }

    it('10 scoped agents show exactly 8 alphabetically-ordered rows for a bare "@", plus agentsHiddenCount=2', () => {
      mentionAgentsOverride = tenAgentsFixture()
      const { result } = renderHook(() => useSlashMenu(baseParams()))

      act(() => result.current.onInputChange('@'))
      expect(result.current.slashItems).toHaveLength(8)
      // Alphabetical order (Agent01..Agent08) — not array/API order.
      expect(result.current.slashItems.map((i) => i.key)).toEqual([
        'ag01', 'ag02', 'ag03', 'ag04', 'ag05', 'ag06', 'ag07', 'ag08',
      ])
      // Agent09/Agent10 are the two matches pushed past the cap.
      expect(result.current.agentsHiddenCount).toBe(2)
    })

    it('narrowing to a filter with 9 remaining matches still hides 1 (agentsHiddenCount=1)', () => {
      mentionAgentsOverride = tenAgentsFixture()
      const { result } = renderHook(() => useSlashMenu(baseParams()))

      // "agent0" prefix-matches Agent01..Agent09 (9 of the 10) — Agent10 is
      // excluded: "agent10" starts with "agent1", not "agent0", and does not
      // contain "agent0" as a substring either.
      act(() => result.current.onInputChange('@agent0'))
      expect(result.current.slashItems).toHaveLength(8)
      expect(result.current.slashItems.map((i) => i.key)).not.toContain('ag10')
      expect(result.current.agentsHiddenCount).toBe(1)
    })

    it('narrowing further to ≤8 matches drops agentsHiddenCount back to 0 (the footer disappears)', () => {
      mentionAgentsOverride = tenAgentsFixture()
      const { result } = renderHook(() => useSlashMenu(baseParams()))

      // "agent1" prefix-matches only Agent10 — none of Agent01..Agent09
      // start with or contain "agent1" (they all have "agent0" at that
      // position).
      act(() => result.current.onInputChange('@agent1'))
      expect(result.current.slashItems.map((i) => i.key)).toEqual(['ag10'])
      expect(result.current.agentsHiddenCount).toBe(0)
    })

    it('cap-hidden agents never appear in slashItems — ArrowDown wrap-around only cycles the 8 visible rows, never reaching a hidden one', () => {
      mentionAgentsOverride = tenAgentsFixture()
      const { result } = renderHook(() => useSlashMenu(baseParams()))
      act(() => result.current.onInputChange('@'))
      expect(result.current.slashItems).toHaveLength(8)

      // Cycle ArrowDown exactly 8 times — wraps back to index 0 (modulo 8,
      // not modulo 10), so the highlight can never land on the two
      // cap-hidden agents (Agent09/Agent10), which don't exist in
      // slashItems at all.
      for (let i = 0; i < 8; i++) {
        act(() => result.current.handleKeyDown({ key: 'ArrowDown', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
      }
      expect(result.current.slashHighlight).toBe(0)
      expect(result.current.slashItems[result.current.slashHighlight].key).toBe('ag01')
    })
  })

  // Gap 6 (Fix 4 regression): the composer's onSubmit resyncs the text
  // mirror via `slashMenu.onInputChange('')` immediately after a real send
  // — see useSlashMenu.ts's file header and ChatScreen.tsx's
  // ComposerPrimitive.Root onSubmit doc comment. Before that fix, a stale
  // "@..." mirror survived a send and a subsequent ArrowDown reopened the
  // full agent menu with nothing visible in the (now-empty) textarea.
  it('Fix 4 regression: after the submit path resyncs the mirror (onInputChange("")), ArrowDown does not reopen the stale "@" menu', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    // Simulate typing an "@"-prefixed message...
    act(() => result.current.onInputChange('@x'))
    expect(result.current.slashOpen).toBe(true)
    // ...then simulate the composer's onSubmit resync that runs immediately
    // after a successful send.
    act(() => result.current.onInputChange(''))
    expect(result.current.slashOpen).toBe(false)
    expect(result.current.isMentionMode).toBe(false)

    // The textarea is now visually empty. Before Fix 4 this ArrowDown would
    // read the stale "@x" mirror and reopen the agent menu.
    act(() => result.current.handleKeyDown({ key: 'ArrowDown', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(result.current.slashOpen).toBe(false)
    expect(result.current.shouldShowSlash).toBe(false)
    expect(result.current.slashItems).toHaveLength(0)
  })

  // Gap 8: backspacing away a narrowing filter must re-show every agent
  // that was hidden by the filter, not just stop shrinking.
  it('backspacing from a narrowed filter back to bare "@" re-shows the full agent list', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('@mi'))
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['mia'])

    act(() => result.current.onInputChange('@'))
    // Deferred item 3: alphabetical order — jim, mars, mia.
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['jim', 'mars', 'mia'])
  })

  // Gap 9: effectiveActiveAgentId's fallback chain — `activeAgentId ||
  // chatAgents[0]?.id` — must mark exactly the right row (or none) in both
  // directions of the fallback.
  describe('effectiveActiveAgentId fallback', () => {
    it('activeAgentId null: the FIRST chat agent (in useChatAgents\' own order) is marked active (fallback to chatAgents[0])', () => {
      act(() => { useSessionStore.setState({ activeAgentId: null }) })
      const { result } = renderHook(() => useSlashMenu(baseParams()))
      act(() => result.current.onInputChange('@'))
      const flags = result.current.slashItems.map((i) => [i.key, i.isActiveAgent])
      // Deferred item 3 sorts the RENDERED rows alphabetically (jim, mars,
      // mia) but deliberately does NOT change effectiveActiveAgentId's
      // fallback source — it still reads `chatAgents[0]` (useChatAgents'
      // own, unsorted order — "mia" first in this fixture), matching
      // AgentPicker.tsx's own identical `effectiveAgentId = activeAgentId ||
      // chatAgents[0]?.id` fallback (composer/AgentPicker.tsx) and its
      // auto-select-first-ready-agent effect, which also keys off
      // `chatAgents[0]` unsorted. Diverging this hook's fallback from
      // AgentPicker's would mark the WRONG row "active" here relative to
      // whichever agent AgentPicker itself just auto-selected.
      expect(flags).toEqual([['jim', false], ['mars', false], ['mia', true]])
    })

    it('activeAgentId set to an id NOT present in chatAgents: no row is marked active', () => {
      act(() => { useSessionStore.setState({ activeAgentId: 'nonexistent-agent' }) })
      const { result } = renderHook(() => useSlashMenu(baseParams()))
      act(() => result.current.onInputChange('@'))
      const flags = result.current.slashItems.map((i) => [i.key, i.isActiveAgent])
      expect(flags).toEqual([['jim', false], ['mars', false], ['mia', false]])
    })
  })

  // Gap 10: interception is exclusively a "/" concern — an "@"-prefixed
  // message (menu closed or not) must always fall through to the normal
  // send path, never get swallowed as if it were a client command.
  it('interceptClientCommand returns false for an "@"-prefixed message — never intercepted', () => {
    const composerRuntime = makeComposerRuntime('@mia')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))

    let intercepted = false
    act(() => { intercepted = result.current.interceptClientCommand() })

    expect(intercepted).toBe(false)
    expect(composerRuntime.setText).not.toHaveBeenCalled()
  })
})

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

  it('a runtime-driven text clear that bypasses onChange (e.g. mouse-click Send) still resyncs the mirror, so a stale "@" mirror cannot reopen the menu', () => {
    const { runtime, setExternalText, notifySubscribers } = makeSubscribableComposerRuntime()
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime: runtime })))

    // Simulate typing "@x" through the normal onChange path — a real
    // keystroke updates both this hook's mirror AND the runtime's own text
    // (see AssistantUI's ComposerInput.js onChange, which calls BOTH the
    // caller's onChange and `aui.composer().setText(...)`).
    setExternalText('@x')
    act(() => result.current.onInputChange('@x'))
    expect(result.current.slashOpen).toBe(true)
    expect(result.current.isMentionMode).toBe(true)

    // Simulate the runtime clearing its own text (click-Send) and notifying
    // subscribers — WITHOUT ever going through onInputChange or onSubmit.
    setExternalText('')
    act(() => { notifySubscribers() })

    expect(result.current.inputValue).toBe('')
    expect(result.current.slashOpen).toBe(false)
    expect(result.current.isMentionMode).toBe(false)

    // The textarea is now visually empty. Before Fix A, this ArrowDown
    // would still read the stale "@x" mirror and reopen the full agent menu.
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

describe('useSlashMenu — mentionAnnouncement reconciliation (Fix B, bugfixes3 sign-off)', () => {
  it('clears mentionAnnouncement when activeAgentId changes via a path OTHER than "@" (e.g. AgentPicker)', () => {
    // "@mi" isolates a single match ("mia") — deferred item 3/4 made "@m"
    // match both "mars" and "mia" (alphabetically, "mars" first), so a
    // narrower filter keeps this test's single-match intent unambiguous.
    const composerRuntime = makeComposerRuntime('@mi')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    act(() => result.current.onInputChange('@mi'))
    act(() => result.current.handleKeyDown({ key: 'Enter', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(result.current.mentionAnnouncement).toBe('Mia')

    // Simulate an external switch — e.g. AgentPicker's handleAgentSelect —
    // writing directly to the session store, NOT through selectMentionAgent.
    act(() => { useSessionStore.setState({ activeAgentId: 'jim' }) })

    expect(result.current.mentionAnnouncement).toBeNull()
  })

  it('re-announces the SAME agent selected via "@" a second time, after an intervening external switch (A -> picker -> A)', () => {
    const composerRuntime = makeComposerRuntime('@mi')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))

    // First "@" selection of Mia.
    act(() => result.current.onInputChange('@mi'))
    act(() => result.current.handleKeyDown({ key: 'Enter', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(result.current.mentionAnnouncement).toBe('Mia')

    // Switch to Jim via a DIFFERENT surface (AgentPicker), not through "@".
    act(() => { useSessionStore.setState({ activeAgentId: 'jim' }) })
    expect(result.current.mentionAnnouncement).toBeNull()

    // Re-select Mia via "@" again. Before Fix B this was silent: mentionAnnouncement
    // still held 'Mia' from the first selection, so setMentionAnnouncement('Mia')
    // was a same-value no-op (no transition, no re-announce). It must now be
    // a real `null -> 'Mia'` transition.
    act(() => result.current.onInputChange('@mi'))
    act(() => result.current.handleKeyDown({ key: 'Enter', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(result.current.mentionAnnouncement).toBe('Mia')
  })
})

describe('useSlashMenu — IME composition guard (Fix C, bugfixes3 sign-off)', () => {
  it('Enter during an IME composition does not select from the menu or wipe the draft', () => {
    // "@mi" isolates a single match ("mia") — see the reconciliation
    // describe block above for why "@m" alone is now ambiguous.
    const composerRuntime = makeComposerRuntime('@mi')
    const { result } = renderHook(() => useSlashMenu(baseParams({ composerRuntime })))
    act(() => result.current.onInputChange('@mi'))

    const imeEnter = { key: 'Enter', preventDefault: vi.fn(), nativeEvent: { isComposing: true } } as unknown as React.KeyboardEvent
    act(() => result.current.handleKeyDown(imeEnter))

    // Nothing was selected — preventDefault was not called, the menu is
    // still open, and no agent switch happened.
    expect(imeEnter.preventDefault).not.toHaveBeenCalled()
    expect(result.current.slashOpen).toBe(true)
    expect(useSessionStore.getState().activeAgentId).toBeNull()

    // A plain (non-composing) Enter on the SAME highlighted row still
    // selects normally — proves the guard is IME-specific, not a general
    // Enter regression.
    const plainEnter = { key: 'Enter', preventDefault: vi.fn() } as unknown as React.KeyboardEvent
    act(() => result.current.handleKeyDown(plainEnter))
    expect(plainEnter.preventDefault).toHaveBeenCalled()
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
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
  it('"@code" ranks the prefix match ("Code Assistant") above the substring-only match ("Assist Code")', () => {
    mentionAgentsOverride = [
      makeAgent({ id: 'assist-code', name: 'Assist Code', type: 'core', status: 'active' }),
      makeAgent({ id: 'code-asst', name: 'Code Assistant', type: 'core', status: 'active' }),
    ]
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('@code'))

    // "Code Assistant" starts with "code" (prefix rank); "Assist Code"
    // contains "code" but doesn't start with it (substring rank). Without
    // rank-first ordering, plain alphabetical sort would put "Assist Code"
    // FIRST ("Assist" < "Code") — this proves rank wins over alphabetical.
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['code-asst', 'assist-code'])
  })

  it('"@assist" finds "Code Assistant" via the substring rank (not just the literal prefix match "Assist Code")', () => {
    mentionAgentsOverride = [
      makeAgent({ id: 'assist-code', name: 'Assist Code', type: 'core', status: 'active' }),
      makeAgent({ id: 'code-asst', name: 'Code Assistant', type: 'core', status: 'active' }),
    ]
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('@assist'))

    // "Assist Code" starts with "assist" (prefix rank, ranked first);
    // "Code Assistant" contains "assist" further in ("Code ASSISTant" —
    // substring rank, ranked second) — this is the exact "@assist finds
    // Code Assistant" case: prefix-only matching would have missed it
    // entirely (slashItems would have been just ['assist-code']).
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['assist-code', 'code-asst'])
  })

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
