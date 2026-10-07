/**
 * FR-007 / BDD-07.3, this wave's slice only: the client does not handle
 * /new or its /clear alias, neither is listed in the palette or /help, and
 * a leading "@" does not switch the agent. /help and /model stay.
 * /resume → /sessions is Wave 1 and is not asserted here.
 */
import type { KeyboardEvent } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import type { ComposerRuntime } from '@assistant-ui/react'
import type { Agent } from '@/lib/api'
import { useSlashMenu } from './useSlashMenu'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { makeAgent } from '@/test/factories'

const commands = [
  { name: 'new', label: '/new', description: 'Start a new conversation', delivery: 'client', available_while_streaming: false, aliases: ['clear'] },
  { name: 'help', label: '/help', description: 'Show available commands', delivery: 'client', available_while_streaming: false },
  { name: 'model', label: '/model', description: 'Change the chat model', delivery: 'client', available_while_streaming: false },
  { name: 'cancel', label: '/cancel', description: 'Cancel the current turn', delivery: 'client', available_while_streaming: true },
]

const agents: Agent[] = [
  makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'active' }),
  makeAgent({ id: 'jim', name: 'Jim', type: 'core', status: 'idle' }),
]

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[]; enabled?: boolean }) => {
      if (opts.enabled === false) return { data: [], isError: false, isLoading: false, refetch: vi.fn() }
      const key = opts.queryKey
      if (Array.isArray(key) && key[0] === 'commands') return { data: commands, isError: false, isLoading: false, refetch: vi.fn() }
      if (Array.isArray(key) && key[0] === 'skills') return { data: [], isError: false, isLoading: false, refetch: vi.fn() }
      if (Array.isArray(key) && key[0] === 'agents') return { data: agents, isError: false, isLoading: false, refetch: vi.fn() }
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

function runtime(text: string) {
  return {
    getState: () => ({ text }),
    setText: vi.fn(),
    addAttachment: vi.fn(),
    subscribe: vi.fn(() => vi.fn()),
    send: vi.fn(),
  } as unknown as ComposerRuntime
}

function enter(): KeyboardEvent {
  return {
    key: 'Enter',
    shiftKey: false,
    preventDefault: vi.fn(),
    nativeEvent: { isComposing: false },
  } as unknown as KeyboardEvent
}

beforeEach(() => {
  act(() => {
    useSessionStore.setState({
      activeAgentId: null,
      activeSessionId: null,
      activeAgentType: null,
      agentSelectionSource: 'auto',
      agentSelectionWorkspaceId: null,
    })
    useUiStore.setState({ modelSelectorOpen: false, agentSelectorOpen: false })
  })
})

describe('slash and mention cutover (FR-007, BDD-07.3)', () => {
  it('does not list /new or /clear in the palette, and still lists /help and /model', () => {
    const { result } = renderHook(() => useSlashMenu({
      isStreaming: false,
      isReplaying: false,
      inputEnabled: true,
      composerRuntime: runtime('/'),
      appendMessage: vi.fn(),
      startNewSession: vi.fn(),
      cancelIfStreaming: vi.fn(),
      sendRedirectFrame: vi.fn(),
      activateStop: vi.fn(),
    }))
    act(() => result.current.onInputChange('/'))
    const keys = result.current.slashItems.map((item) => item.key)
    expect(keys).toContain('/help')
    expect(keys).toContain('/model')
    expect(keys).not.toContain('/new')
    expect(keys).not.toContain('/clear')
  })

  it('help text does not teach /new, /clear, or @ agent switching, and still lists /help', () => {
    const appendMessage = vi.fn()
    const { result } = renderHook(() => useSlashMenu({
      isStreaming: false,
      isReplaying: false,
      inputEnabled: true,
      composerRuntime: runtime('/help'),
      appendMessage,
      startNewSession: vi.fn(),
      cancelIfStreaming: vi.fn(),
      sendRedirectFrame: vi.fn(),
      activateStop: vi.fn(),
    }))
    act(() => result.current.onInputChange('/help'))
    const help = result.current.slashItems.find((item) => item.key === '/help')
    expect(help).toBeTruthy()
    act(() => help!.onSelect())
    const content = String(appendMessage.mock.calls[0]?.[0]?.content ?? '')
    expect(content).toContain('/help')
    expect(content).not.toContain('/new')
    expect(content).not.toContain('/clear')
    expect(content).not.toContain('switch agents')
  })

  it.each(['/new', '/clear', '/NEW', '/Clear'])(
    'typing %s does not run the client new-chat command',
    (typed) => {
      const startNewSession = vi.fn()
      const composerRuntime = runtime(typed)
      const { result } = renderHook(() => useSlashMenu({
        isStreaming: false,
        isReplaying: false,
        inputEnabled: true,
        composerRuntime,
        appendMessage: vi.fn(),
        startNewSession,
        cancelIfStreaming: vi.fn(),
        sendRedirectFrame: vi.fn(),
        activateStop: vi.fn(),
      }))
      let handled = false
      act(() => {
        result.current.onInputChange(typed)
        handled = result.current.interceptClientCommand()
      })
      expect(handled).toBe(false)
      expect(startNewSession).not.toHaveBeenCalled()
    },
  )

  it('a leading @ does not switch the agent', () => {
    const { result } = renderHook(() => useSlashMenu({
      isStreaming: false,
      isReplaying: false,
      inputEnabled: true,
      composerRuntime: runtime('@mi'),
      appendMessage: vi.fn(),
      startNewSession: vi.fn(),
      cancelIfStreaming: vi.fn(),
      sendRedirectFrame: vi.fn(),
      activateStop: vi.fn(),
    }))
    act(() => result.current.onInputChange('@mi'))
    expect(result.current.isMentionMode).toBe(false)
    expect(result.current.slashItems.filter((item) => item.section === 'agents')).toEqual([])
    act(() => result.current.handleKeyDown(enter()))
    expect(useSessionStore.getState().activeAgentId).toBeNull()
    expect(result.current.mentionAnnouncement).toBeNull()
  })
})
