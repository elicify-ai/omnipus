// @ mention menu — Admin is offered only in the default workspace
// (combined-review R2-S2-DV-07, team-lead decision: FIX in code).
//
// The gateway refuses an @ request to Admin from any workspace but the
// default: pkg/gateway/main_session.go::mainAgentEligible returns ws.IsDefault
// for Admin (Admin owns a main only there and sits on no team). Until now the
// picker still offered a ready Admin everywhere (useChatAgents' ADR-090
// standalone-operator exemption is workspace-blind), so a user in a
// non-default workspace was offered a choice that could only fail with
// "that agent cannot be reached from here right now".
//
// Harness copied from useSlashMenu.navCutover.test.tsx (useQuery mocked by
// query key; the workspaces list is a mutable fixture so each test picks the
// active workspace's is_default).

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import type { ComposerRuntime } from '@assistant-ui/react'
import type { Agent, Workspace } from '@/lib/api'
import { useSlashMenu } from './useSlashMenu'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { makeAgent } from '@/test/factories'

const fixture = vi.hoisted(() => ({
  agents: [] as Agent[],
  workspaces: [] as Workspace[],
}))

const workspace = (overrides: { is_default?: boolean; core_team?: string[] }): Workspace =>
  ({
    id: 'ws-1',
    name: 'Test Workspace',
    is_default: overrides.is_default ?? false,
    core_team: overrides.core_team ?? ['mia'],
  }) as Workspace

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[]; enabled?: boolean }) => {
      if (opts.enabled === false) return { data: [], isError: false, isLoading: false, refetch: vi.fn() }
      const key = opts.queryKey
      if (Array.isArray(key) && key[0] === 'commands') return { data: [], isError: false, isLoading: false, refetch: vi.fn() }
      if (Array.isArray(key) && key[0] === 'skills') return { data: [], isError: false, isLoading: false, refetch: vi.fn() }
      if (Array.isArray(key) && key[0] === 'agents') return { data: fixture.agents, isError: false, isLoading: false, refetch: vi.fn() }
      if (Array.isArray(key) && key[0] === 'workspaces') return { data: fixture.workspaces, isError: false, isLoading: false, refetch: vi.fn() }
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

function renderMenu(text: string) {
  return renderHook(() => useSlashMenu({
    isStreaming: false,
    isReplaying: false,
    inputEnabled: true,
    composerRuntime: runtime(text) as unknown as ComposerRuntime,
    appendMessage: vi.fn(),
    cancelIfStreaming: vi.fn(),
    sendRedirectFrame: vi.fn(),
    activateStop: vi.fn(),
  }))
}

beforeEach(() => {
  fixture.agents = [
    makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'active' }),
    makeAgent({ id: 'admin', name: 'Admin', type: 'core', status: 'active' }),
  ]
  fixture.workspaces = [workspace({})]
  act(() => {
    useSessionStore.setState({ activeAgentId: null, activeSessionId: null })
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
  })
})

describe('@ mention menu — Admin only in the default workspace (R2-S2-DV-07)', () => {
  it('hides Admin from the picker in a NON-default workspace', () => {
    fixture.workspaces = [workspace({ is_default: false })]
    const { result } = renderMenu('@')
    act(() => result.current.onInputChange('@'))
    expect(result.current.isMentionMode).toBe(true)
    expect(result.current.slashItems.map((item) => item.key)).toEqual(['mia'])
    // Even typing Admin's name matches nothing.
    act(() => result.current.onInputChange('@admin'))
    expect(result.current.slashItems).toEqual([])
  })

  it('still lists Admin in the DEFAULT workspace (ADR-090 standalone operator, unchanged there)', () => {
    fixture.workspaces = [workspace({ is_default: true })]
    const { result } = renderMenu('@')
    act(() => result.current.onInputChange('@'))
    // Alphabetical: Admin before Mia.
    expect(result.current.slashItems.map((item) => item.key)).toEqual(['admin', 'mia'])
  })

  it('teammates are unaffected by the workspace kind — Mia lists in both', () => {
    fixture.workspaces = [workspace({ is_default: false, core_team: ['mia'] })]
    const { result } = renderMenu('@')
    act(() => result.current.onInputChange('@mi'))
    expect(result.current.slashItems.map((item) => item.key)).toEqual(['mia'])
  })
})
