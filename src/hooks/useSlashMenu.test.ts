// useSlashMenu.test.ts — partitioned slash-command + skill palette
// (FR-005/FR-006/FR-009/FR-014/D9/R3) plus the ghost-text hint. Extracted out
// of OmnipusComposer's own inline state; ChatScreen's existing integration
// tests (partitioned-menu, unknown-slash, ghost-text, agents-command, etc.)
// exercise this through the rendered composer's real DOM events. These tests
// isolate the hook's own filtering/dispatch logic so a regression here fails
// fast and close to the cause, independent of AssistantUI's mocked
// primitives.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import type { ComposerRuntime } from '@assistant-ui/react'
import { useSlashMenu } from './useSlashMenu'
import { useUiStore } from '@/store/ui'

const mockCommands = [
  { name: 'clear', label: '/clear', description: 'Start a new conversation', delivery: 'client', available_while_streaming: false },
  { name: 'help', label: '/help', description: 'Show available commands', delivery: 'client', available_while_streaming: false },
  { name: 'model', label: '/model', description: 'Change the chat model', delivery: 'client', available_while_streaming: false },
  { name: 'agents', label: '/agents', description: 'Open agent selector', delivery: 'client', available_while_streaming: false },
  { name: 'cancel', label: '/cancel', description: 'Cancel the current turn', delivery: 'client', available_while_streaming: true },
  { name: 'unknown-agent-cmd', label: '/handoff', description: 'Agent-delivered command', delivery: 'agent', available_while_streaming: false },
]
const mockSkills = [
  { id: 'web-research', name: 'Web Research', version: '1.0', description: 'Web search and extraction', verified: true, status: 'active', argument_hint: '<query>' },
  { id: 'code-review', name: 'Code Review', version: '1.0', description: 'Reviews code quality', verified: true, status: 'active' },
]

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[]; enabled?: boolean }) => {
      if (opts.enabled === false) return { data: [], isError: false, refetch: vi.fn() }
      const key = opts.queryKey
      if (Array.isArray(key) && key[0] === 'commands') return { data: mockCommands, isError: false, refetch: vi.fn() }
      if (Array.isArray(key) && key[0] === 'skills') return { data: mockSkills, isError: false, refetch: vi.fn() }
      return { data: [], isError: false, refetch: vi.fn() }
    },
  }
})

vi.mock('@/lib/api', () => ({
  fetchCommands: vi.fn().mockResolvedValue([]),
  fetchSkills: vi.fn().mockResolvedValue([]),
}))

function makeComposerRuntime(text = '') {
  return {
    getState: () => ({ text }),
    setText: vi.fn(),
    addAttachment: vi.fn(),
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
  act(() => { useUiStore.setState({ modelSelectorOpen: false, agentSelectorOpen: false }) })
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
    expect(result.current.slashItems.map((i) => i.key)).toEqual([
      '/clear', '/help', '/model', '/agents', '/cancel', '/handoff', 'web-research', 'code-review',
    ])
  })
})

describe('useSlashMenu — streaming filter', () => {
  it('while streaming, only available_while_streaming commands remain (skills unaffected)', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams({ isStreaming: true })))
    act(() => result.current.onInputChange('/'))
    const commandKeys = result.current.slashItems.filter((i) => i.section === 'commands').map((i) => i.key)
    expect(commandKeys).toEqual(['/cancel'])
    const skillKeys = result.current.slashItems.filter((i) => i.section === 'skills').map((i) => i.key)
    expect(skillKeys).toEqual(['web-research', 'code-review'])
  })
})

describe('useSlashMenu — D9 "/skills" filter', () => {
  it('hides the commands section entirely and shows every skill', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/skills'))
    expect(result.current.slashItems.every((i) => i.section === 'skills')).toBe(true)
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['web-research', 'code-review'])
  })
})

describe('useSlashMenu — prefix filtering', () => {
  it('filters commands and skills by the text after "/"', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/cl'))
    expect(result.current.slashItems.map((i) => i.key)).toEqual(['/clear'])
  })

  it('shows no items for an unmatched prefix (Issue 3 — menu just disappears)', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    act(() => result.current.onInputChange('/zzz'))
    expect(result.current.slashItems).toHaveLength(0)
    expect(result.current.shouldShowSlash).toBe(false)
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

  it('is a no-op when the menu should not be showing', () => {
    const { result } = renderHook(() => useSlashMenu(baseParams()))
    // No "/" typed — shouldShowSlash is false.
    act(() => result.current.handleKeyDown({ key: 'ArrowDown', preventDefault: vi.fn() } as unknown as React.KeyboardEvent))
    expect(result.current.slashHighlight).toBe(0)
    expect(result.current.slashOpen).toBe(false)
  })
})

describe('useSlashMenu — client command dispatch', () => {
  it('/clear calls startNewSession', () => {
    const startNewSession = vi.fn()
    const { result } = renderHook(() => useSlashMenu(baseParams({ startNewSession })))
    act(() => result.current.onInputChange('/'))
    const item = result.current.slashItems.find((i) => i.key === '/clear')!
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
    expect(msg.content).toContain('/clear')
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
  it('intercepts an exact client-delivery command and runs it locally', () => {
    const composerRuntime = makeComposerRuntime('/clear')
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
