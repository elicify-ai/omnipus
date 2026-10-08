/**
 * FR-007 / BDD-07.3 and founder X3. The server command table decides.
 * A server list that contains `clear` shows /clear; a list without it does
 * not. /new is the same: shown only when the server returns it. There is no
 * client-local handler for either — selecting one sends it as a normal
 * command, and startNewSession is never called. A leading "@" does not
 * switch the agent. /resume and /workspace stay the existing web-only
 * entries; this file does not remove them.
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

type ServerCommand = {
  name: string
  label: string
  description: string
  delivery: 'client' | 'agent'
  available_while_streaming: boolean
}

const server = vi.hoisted(() => {
  const clearCommand = (): ServerCommand => ({
    name: 'clear',
    label: '/clear',
    description: 'Reset this chat to a safe point',
    delivery: 'client',
    available_while_streaming: false,
  })
  const newCommand = (): ServerCommand => ({
    name: 'new',
    label: '/new',
    description: 'Start a new conversation',
    delivery: 'client',
    available_while_streaming: false,
  })
  const rest = (): ServerCommand[] => [
    { name: 'help', label: '/help', description: 'Show available commands', delivery: 'client', available_while_streaming: false },
    { name: 'model', label: '/model', description: 'Change the chat model', delivery: 'client', available_while_streaming: false },
    { name: 'cancel', label: '/cancel', description: 'Cancel the current turn', delivery: 'client', available_while_streaming: true },
  ]
  return {
    commands: [clearCommand(), ...rest()] as ServerCommand[],
    withClear: () => [clearCommand(), ...rest()],
    withNew: () => [newCommand(), ...rest()],
    withoutNewOrClear: () => rest(),
  }
})

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
      if (Array.isArray(key) && key[0] === 'commands') return { data: server.commands, isError: false, isLoading: false, refetch: vi.fn() }
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

function runtime(text = '') {
  let current = text
  const setText = vi.fn((value: string) => {
    current = value
  })
  const send = vi.fn()
  return {
    getState: () => ({ text: current }),
    setText,
    addAttachment: vi.fn(),
    subscribe: vi.fn(() => vi.fn()),
    send,
    current: () => current,
  }
}

function enter(): KeyboardEvent {
  return {
    key: 'Enter',
    shiftKey: false,
    preventDefault: vi.fn(),
    nativeEvent: { isComposing: false },
  } as unknown as KeyboardEvent
}

function commandKeys(items: { key: string; section: string }[]) {
  return items.filter((item) => item.section === 'commands').map((item) => item.key)
}

beforeEach(() => {
  server.commands = server.withClear()
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

describe('slash and mention cutover (FR-007, BDD-07.3, founder X3)', () => {
  it('lists /clear when the server returns it, and does not invent /new', () => {
    const composerRuntime = runtime('/')
    const { result } = renderHook(() => useSlashMenu({
      isStreaming: false,
      isReplaying: false,
      inputEnabled: true,
      composerRuntime: composerRuntime as unknown as ComposerRuntime,
      appendMessage: vi.fn(),
      startNewSession: vi.fn(),
      cancelIfStreaming: vi.fn(),
      sendRedirectFrame: vi.fn(),
      activateStop: vi.fn(),
    }))
    act(() => result.current.onInputChange('/'))
    expect(commandKeys(result.current.slashItems)).toEqual([
      '/resume', '/workspace', '/clear', '/help', '/model', '/cancel',
    ])
  })

  it('does not list /clear or /new when the server returns neither', () => {
    server.commands = server.withoutNewOrClear()
    const { result } = renderHook(() => useSlashMenu({
      isStreaming: false,
      isReplaying: false,
      inputEnabled: true,
      composerRuntime: runtime('/') as unknown as ComposerRuntime,
      appendMessage: vi.fn(),
      startNewSession: vi.fn(),
      cancelIfStreaming: vi.fn(),
      sendRedirectFrame: vi.fn(),
      activateStop: vi.fn(),
    }))
    act(() => result.current.onInputChange('/'))
    expect(commandKeys(result.current.slashItems)).toEqual([
      '/resume', '/workspace', '/help', '/model', '/cancel',
    ])
  })

  it('help text lists the server /clear command and does not teach /new or @ switching', () => {
    const appendMessage = vi.fn()
    const { result } = renderHook(() => useSlashMenu({
      isStreaming: false,
      isReplaying: false,
      inputEnabled: true,
      composerRuntime: runtime('/help') as unknown as ComposerRuntime,
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
    expect(content).toContain('/clear')
    expect(content).not.toContain('/new')
    expect(content).not.toContain('switch agents')
  })

  it.each(['/clear', '/new'] as const)(
    'selecting %s sends that server command and does not start a session',
    (label) => {
      server.commands = label === '/clear' ? server.withClear() : server.withNew()
      const composerRuntime = runtime('')
      const startNewSession = vi.fn()
      const { result } = renderHook(() => useSlashMenu({
        isStreaming: false,
        isReplaying: false,
        inputEnabled: true,
        composerRuntime: composerRuntime as unknown as ComposerRuntime,
        appendMessage: vi.fn(),
        startNewSession,
        cancelIfStreaming: vi.fn(),
        sendRedirectFrame: vi.fn(),
        activateStop: vi.fn(),
      }))
      act(() => result.current.onInputChange('/'))
      const item = result.current.slashItems.find((entry) => entry.key === label)
      expect(item, `the server returned ${label}`).toBeDefined()
      act(() => item!.onSelect())
      expect(startNewSession).not.toHaveBeenCalled()
      expect(composerRuntime.send).toHaveBeenCalledTimes(1)
      expect(composerRuntime.current().trim()).toBe(label)
    },
  )

  it.each(['/new', '/clear', '/NEW', '/Clear'])(
    'typing %s is not a client command and does not start a session',
    (typed) => {
      server.commands = [...server.withClear(), ...server.withNew().filter((command) => command.name === 'new')]
      const composerRuntime = runtime(typed)
      const startNewSession = vi.fn()
      const { result } = renderHook(() => useSlashMenu({
        isStreaming: false,
        isReplaying: false,
        inputEnabled: true,
        composerRuntime: composerRuntime as unknown as ComposerRuntime,
        appendMessage: vi.fn(),
        startNewSession,
        cancelIfStreaming: vi.fn(),
        sendRedirectFrame: vi.fn(),
        activateStop: vi.fn(),
      }))
      let handled = true
      act(() => {
        result.current.onInputChange(typed)
        handled = result.current.interceptClientCommand()
      })
      expect(handled).toBe(false)
      expect(startNewSession).not.toHaveBeenCalled()
      expect(composerRuntime.setText).not.toHaveBeenCalled()
    },
  )

  it('a leading @ does not switch the agent', () => {
    const { result } = renderHook(() => useSlashMenu({
      isStreaming: false,
      isReplaying: false,
      inputEnabled: true,
      composerRuntime: runtime('@mi') as unknown as ComposerRuntime,
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
