// useChatAgents.adminStandalone.test.ts — ADR-090 FR-001 / §2.2.
//
// Admin is the STANDALONE OPERATOR: a chat-able core agent ("Yes / no team
// membership") that deliberately belongs to NO workspace team — every
// membership write path (REST validator, sysagent team tools, boot seed)
// refuses an Admin membership. That exclusion must not cost Admin its
// chat-ability: `chatAgents` is the ONE list both chat surfaces consume
// (the AgentPicker dropdown and useSlashMenu's "@" mention menu), so if
// core_team scoping drops Admin, the operator becomes unreachable in chat
// while the engine-side turn path (rooted at Admin's own agent home) works.
//
// The spec oracle is ADR-090's roster table, not any code-derived list: the
// six ordinary team roles (mia/jim/ava/planner/researcher/worker-tier
// chat-eligible members) stay strictly workspace-scoped; Admin alone is
// exempt from TEAM scoping; and the exemption is not a readiness bypass —
// a non-ready Admin (draft) stays excluded, hidden System agents stay
// excluded, and a non-core agent carrying the id cannot ride it.
//
// Wait contract: the workspace query is a SECOND request (`enabled` only
// when `activeWorkspaceId` is set). Waiting on `agents.length` alone can
// fire while `teamIds` is still unset, which makes the team filter a no-op
// and would let "Admin is listed" pass vacuously before scoping applies.
// Every team-scoped case therefore waits until an off-team eligible
// sentinel (Jim) has left `chatAgents` — that can only happen after the
// team query has resolved and been applied.
//
// Companion to useChatAgents.systemExcluded.test.ts (ADR-049's hidden-role
// exclusion) and to the core_team-scoping describe in useChatAgents.test.ts
// — kept a separate file per the same convention.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import type { Agent, Workspace } from '@/lib/api'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { makeAgent } from '@/test/factories'
import type { UseChatAgentsResult } from './useChatAgents'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn(),
    fetchWorkspaces: vi.fn(),
  }
})

import { fetchAgents, fetchWorkspaces } from '@/lib/api'
import { useChatAgents } from './useChatAgents'

function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return React.createElement(QueryClientProvider, { client }, children)
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

// The ADR-090 default-team shape: a workspace whose core_team names ONLY
// ordinary roles. Post-ADR-090 no legitimate writer can put admin on a
// core_team, so this is the steady state every workspace will be in.
function teamWorkspace(): Workspace {
  return {
    id: 'ws-adr090',
    name: 'Ordinary Team WS',
    status: 'active',
    core_team: ['mia'],
  } as unknown as Workspace
}

const MIA = makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'active' })
const JIM = makeAgent({ id: 'jim', name: 'Jim', type: 'core', status: 'idle' })
const ADMIN = makeAgent({
  id: 'admin',
  name: 'Admin',
  type: 'core',
  locked: true,
  status: 'idle',
  description: 'Standalone operator',
})
const OPS_HELPER = makeAgent({ id: 'ops-helper', name: 'Ops Helper', type: 'Main', status: 'active' })
const JUDGE = makeAgent({ id: 'judge', name: 'Judge', type: 'system', locked: true, status: 'active' })

function idsOf(agents: Agent[]): string[] {
  return agents.map((a) => a.id)
}

/** Agents have loaded AND team scoping has applied (Jim is on the roster, off the team). */
async function waitUntilTeamScopeApplied(
  result: { current: UseChatAgentsResult },
  rosterLength: number,
): Promise<void> {
  await waitFor(() => {
    expect(result.current.agents).toHaveLength(rosterLength)
    expect(idsOf(result.current.chatAgents)).not.toContain('jim')
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  act(() => { useWorkspacesStore.setState({ activeWorkspaceId: null }) })
})

describe('useChatAgents — Admin standalone operator stays chat-selectable (ADR-090 FR-001)', () => {
  it('keeps Admin in chatAgents while the active workspace\'s core_team scopes ordinary agents out', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([MIA, JIM, ADMIN, OPS_HELPER, JUDGE])
    vi.mocked(fetchWorkspaces).mockResolvedValue([teamWorkspace()])
    act(() => { useWorkspacesStore.setState({ activeWorkspaceId: 'ws-adr090' }) })

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitUntilTeamScopeApplied(result, 5)
    // Exact list, in the agents array's own order: Mia (on the team) and
    // Admin (team-independent) — nothing else. Jim's absence is the proof
    // the team query applied; Admin's presence is the exemption.
    expect(idsOf(result.current.chatAgents)).toEqual(['mia', 'admin'])
  })

  it('ordinary core agents and custom Main agents stay strictly team-scoped — the exemption is Admin\'s alone', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([MIA, JIM, ADMIN, OPS_HELPER, JUDGE])
    vi.mocked(fetchWorkspaces).mockResolvedValue([teamWorkspace()])
    act(() => { useWorkspacesStore.setState({ activeWorkspaceId: 'ws-adr090' }) })

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitUntilTeamScopeApplied(result, 5)
    const ids = idsOf(result.current.chatAgents)
    expect(ids).toContain('admin')
    expect(ids).not.toContain('jim')
    expect(ids).not.toContain('ops-helper')
  })

  it('a non-ready Admin (draft) is still excluded — team independence is not a readiness bypass', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      MIA,
      JIM,
      makeAgent({ id: 'admin', name: 'Admin', type: 'core', locked: true, status: 'draft' }),
    ])
    vi.mocked(fetchWorkspaces).mockResolvedValue([teamWorkspace()])
    act(() => { useWorkspacesStore.setState({ activeWorkspaceId: 'ws-adr090' }) })

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitUntilTeamScopeApplied(result, 3)
    expect(idsOf(result.current.chatAgents)).toEqual(['mia'])
  })

  it('hidden System agents on no team stay excluded even though Admin, equally teamless, is kept', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([MIA, JIM, ADMIN, JUDGE])
    vi.mocked(fetchWorkspaces).mockResolvedValue([teamWorkspace()])
    act(() => { useWorkspacesStore.setState({ activeWorkspaceId: 'ws-adr090' }) })

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitUntilTeamScopeApplied(result, 4)
    expect(idsOf(result.current.chatAgents)).toEqual(['mia', 'admin'])
  })

  it('a non-core agent carrying the id "admin" does not ride the exemption', async () => {
    // Boundary for the predicate's type gate: the exemption is the locked
    // core roster entry's, not the bare string's. A custom Main agent
    // (ids are unique, so this coexists with the roster only in a fixture)
    // off the team stays hidden exactly like any other custom agent.
    vi.mocked(fetchAgents).mockResolvedValue([
      MIA,
      JIM,
      makeAgent({ id: 'admin', name: 'Admin', type: 'Main', status: 'active' }),
    ])
    vi.mocked(fetchWorkspaces).mockResolvedValue([teamWorkspace()])
    act(() => { useWorkspacesStore.setState({ activeWorkspaceId: 'ws-adr090' }) })

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitUntilTeamScopeApplied(result, 3)
    expect(idsOf(result.current.chatAgents)).toEqual(['mia'])
  })

  it('with no Admin in the roster at all, core_team scoping is unchanged (no accidental widening)', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([MIA, JIM, OPS_HELPER])
    vi.mocked(fetchWorkspaces).mockResolvedValue([teamWorkspace()])
    act(() => { useWorkspacesStore.setState({ activeWorkspaceId: 'ws-adr090' }) })

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitUntilTeamScopeApplied(result, 3)
    expect(idsOf(result.current.chatAgents)).toEqual(['mia'])
  })

  it('no active workspace: no team filter applies — Admin and ordinary agents alike (pre-existing pin)', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([MIA, JIM, ADMIN])
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    // activeWorkspaceId stays null (top-level beforeEach default) — the
    // workspaces query is disabled, so there is no second-request race.

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitFor(() => {
      expect(result.current.agents).toHaveLength(3)
      expect(idsOf(result.current.chatAgents)).toEqual(['mia', 'jim', 'admin'])
    })
    expect(vi.mocked(fetchWorkspaces)).not.toHaveBeenCalled()
  })
})
