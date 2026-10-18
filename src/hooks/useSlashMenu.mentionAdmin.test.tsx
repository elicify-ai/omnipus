// @ mention menu — the retired Admin agent is offered NOWHERE
// (R2-S2-DV-07 origin; superseded by WU-11 GREEN, omnipus-system-agent
// FR-008: Ava/Admin UI removed, founder 2026-10-18 Train 3 keeps rooms not
// Admin). The gateway-side workspace rule this file pinned is moot: the
// @ menu offers no Admin in ANY workspace, default included, even on a stale
// cache that still carries the record.
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
  // Per-key query state (PTA-04): the loading/failed cases flip these instead
  // of the data fixtures, so the mock mirrors React Query's shape — loading
  // carries NO data, a failure carries whatever was retained ([] = nothing).
  agentsLoading: false,
  agentsError: false,
  workspacesLoading: false,
  workspacesError: false,
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
      if (Array.isArray(key) && key[0] === 'agents') {
        return {
          data: fixture.agentsLoading ? undefined : fixture.agents,
          isError: fixture.agentsError,
          isLoading: fixture.agentsLoading,
          refetch: vi.fn(),
        }
      }
      if (Array.isArray(key) && key[0] === 'workspaces') {
        return {
          data: fixture.workspacesLoading ? undefined : fixture.workspaces,
          isError: fixture.workspacesError,
          isLoading: fixture.workspacesLoading,
          refetch: vi.fn(),
        }
      }
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
  fixture.agentsLoading = false
  fixture.agentsError = false
  fixture.workspacesLoading = false
  fixture.workspacesError = false
  act(() => {
    useSessionStore.setState({ activeAgentId: null, activeSessionId: null })
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
  })
})

describe('@ mention menu — no Admin anywhere (FR-008; origin R2-S2-DV-07)', () => {
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

  // WU-11 GREEN retarget (FR-008): the once-unchanged default-workspace offer
  // flips with the retired agent — the default workspace offers no Admin
  // either.
  it('lists no Admin in the DEFAULT workspace either (FR-008)', () => {
    fixture.workspaces = [workspace({ is_default: true })]
    const { result } = renderMenu('@')
    act(() => result.current.onInputChange('@'))
    expect(result.current.slashItems.map((item) => item.key)).toEqual(['mia'])
    // Even typing Admin's name matches nothing here too.
    act(() => result.current.onInputChange('@admin'))
    expect(result.current.slashItems).toEqual([])
  })

  it('teammates are unaffected by the workspace kind — Mia lists in both', () => {
    fixture.workspaces = [workspace({ is_default: false, core_team: ['mia'] })]
    const { result } = renderMenu('@')
    act(() => result.current.onInputChange('@mi'))
    expect(result.current.slashItems.map((item) => item.key)).toEqual(['mia'])
  })
})

// PTA-04 (combined review round 3): the Admin rule while the data behind it
// is not there yet. activeWorkspaceIsDefault reads FALSE whenever the
// workspaces list carries no active workspace — loading or failed — so Admin
// drops out of @ until the real answer lands (useChatAgents: "Unknown reads
// as false — the safe side"): the menu never offers a pick the gateway would
// refuse, at the cost of a late-arriving Admin. A failed AGENTS roster with
// no cached data empties the candidates entirely — a name-less menu, never a
// fabricated one.
describe('@ mention menu — Admin rule while the roster or workspace list is loading/failed (PTA-04)', () => {
  it('while the workspaces list is LOADING, Admin is hidden even in the default workspace; teammates stay', () => {
    fixture.workspaces = [workspace({ is_default: true })]
    fixture.workspacesLoading = true
    const { result } = renderMenu('@')
    act(() => result.current.onInputChange('@'))
    expect(result.current.slashItems.map((item) => item.key)).toEqual(['mia'])
    // Typing Admin's name matches nothing — the silence is the safe side.
    act(() => result.current.onInputChange('@admin'))
    expect(result.current.slashItems).toEqual([])
  })

  it('when the workspaces list has FAILED with nothing retained, Admin stays hidden; teammates stay', () => {
    fixture.workspaces = [workspace({ is_default: true })]
    fixture.workspacesError = true
    fixture.workspaces = []
    const { result } = renderMenu('@')
    act(() => result.current.onInputChange('@'))
    expect(result.current.slashItems.map((item) => item.key)).toEqual(['mia'])
    act(() => result.current.onInputChange('@admin'))
    expect(result.current.slashItems).toEqual([])
  })

  it('when the AGENTS roster has FAILED with nothing cached, the menu offers no agents at all', () => {
    fixture.workspaces = [workspace({ is_default: true })]
    fixture.agentsError = true
    fixture.agents = []
    const { result } = renderMenu('@')
    act(() => result.current.onInputChange('@'))
    expect(result.current.slashItems).toEqual([])
  })
})
