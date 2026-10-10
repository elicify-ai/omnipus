/**
 * adminMainSession.test.ts — where Admin's main id comes from.
 *
 * Oracle (ARCHITECT-ANSWER-ADMIN-MAIN-BOUND.md, Q1, Status: Decided):
 * "Admin's main id comes ONLY from Workspace.admin_main_session_id on the
 * default workspace; it is never read from member_configs and never built by
 * the SPA; missing ⇒ unavailable + Retry." Admin is not a workspace member;
 * the field is present only on the default workspace and only when the main
 * resolves — never a guessed id.
 *
 * Layers under test: the seam reader (sessionCoreSeam), the roster builder
 * (workspaceRoster), and the eligible-mains row assembly (eligibleMains).
 * All three are real; no network is involved (pure functions over fixtures).
 */
import { describe, expect, it } from 'vitest'
import type { Session, Workspace } from '@/lib/api'
import { adminMainSessionIdOfWorkspace } from './sessionCoreSeam'
import { workspaceMains } from '@/components/layout/sidebar/workspaceRoster'
import { eligibleMainAgents } from './eligibleMains'
import { makeAgent } from '@/test/factories'

/** Full wire-shaped Workspace fixture (no cast-away required fields). */
function makeWorkspace(overrides: Partial<Workspace> = {}): Workspace {
  return {
    id: 'default',
    name: 'Default',
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    revision: '0'.repeat(64),
    is_default: true,
    ...overrides,
  }
}

const ADMIN = makeAgent({ id: 'admin', name: 'Admin', type: 'core' })

describe('adminMainSessionIdOfWorkspace — the seam reader (architect Q1)', () => {
  it('returns the workspace-carried Admin main id verbatim', () => {
    expect(adminMainSessionIdOfWorkspace(makeWorkspace({ admin_main_session_id: 'seam-opaque-admin-main' })))
      .toBe('seam-opaque-admin-main')
  })

  it('rejects an empty or whitespace-only id as unavailable', () => {
    expect(adminMainSessionIdOfWorkspace(makeWorkspace({ admin_main_session_id: '   ' }))).toBeUndefined()
    expect(adminMainSessionIdOfWorkspace(makeWorkspace({ admin_main_session_id: '' }))).toBeUndefined()
  })

  it('returns unavailable when the field is absent (main does not resolve)', () => {
    expect(adminMainSessionIdOfWorkspace(makeWorkspace())).toBeUndefined()
    expect(adminMainSessionIdOfWorkspace(null)).toBeUndefined()
  })

  it('never reads Admin from member_configs', () => {
    // The exact failure mode the ruling removed: an admin-shaped entry in
    // member_configs must not yield an Admin main id.
    const workspace = makeWorkspace({
      member_configs: { admin: { main_session_id: 'main-session-ws+admin-from-membership' } },
    })
    expect(adminMainSessionIdOfWorkspace(workspace)).toBeUndefined()
  })
})

describe('workspaceMains — Admin row on the sidebar roster (architect Q1)', () => {
  it('lists Admin with the workspace-carried main id on the default workspace', () => {
    const { result } = workspaceMains(
      makeWorkspace({ admin_main_session_id: 'seam-opaque-admin-main' }),
      [ADMIN],
      [] as Session[],
      'fresh',
      [],
    )
    expect(result.status).toBe('ready')
    expect(result.retry).toBe(false)
    expect(result.rows).toEqual([
      { agentId: 'admin', workspaceId: 'default', name: 'Admin', mainSessionId: 'seam-opaque-admin-main' },
    ])
    expect(result.missingMainAgentIds).toEqual([])
  })

  it('shows Admin as missing (unavailable + Retry) and builds nothing when the field is absent', () => {
    const { result } = workspaceMains(
      makeWorkspace({ admin_main_session_id: undefined }),
      [ADMIN],
      [] as Session[],
      'fresh',
      [],
    )
    expect(result.status).toBe('unavailable')
    expect(result.retry).toBe(true)
    expect(result.reason).toBe('main-id-missing')
    expect(result.missingMainAgentIds).toEqual(['admin'])
    expect(result.rows).toEqual([])
  })

  it('ignores a member_configs["admin"] entry entirely', () => {
    // A stale membership-shaped admin entry (pre-ruling data) with a valid
    // main_session_id must NOT produce an Admin row while the workspace
    // field is absent: unavailable + Retry, not a membership-sourced row.
    const workspace = makeWorkspace({
      member_configs: { admin: { main_session_id: 'main-session-ws+admin-from-membership' } },
    })
    const { result } = workspaceMains(workspace, [ADMIN], [] as Session[], 'fresh', [])
    expect(result.reason).toBe('main-id-missing')
    expect(result.retry).toBe(true)
    expect(result.rows).toEqual([])
    expect(result.missingMainAgentIds).toEqual(['admin'])
  })

  it('shows no Admin row on a non-default workspace even when the field is present', () => {
    // The server never sends the field off the default workspace; the SPA
    // must not use one even if it saw it. Mia (a member) keeps her row.
    const workspace = makeWorkspace({
      id: 'operations',
      name: 'Operations',
      is_default: false,
      admin_main_session_id: 'seam-opaque-must-not-show',
      member_configs: { mia: { main_session_id: 'seam-opaque-mia-ops' } },
    })
    const mia = makeAgent({ id: 'mia', name: 'Mia', type: 'core' })
    const { result } = workspaceMains(workspace, [mia, ADMIN], [] as Session[], 'fresh', [])
    expect(result.status).toBe('ready')
    expect(result.rows.map((row) => row.agentId)).toEqual(['mia'])
  })

  it('shows no Admin row when the Admin agent itself is not loaded', () => {
    const { result } = workspaceMains(
      makeWorkspace({ admin_main_session_id: 'seam-opaque-admin-main' }),
      [], // Admin agent still loading
      [] as Session[],
      'fresh',
      [],
    )
    expect(result.rows).toEqual([])
    expect(result.reason).toBeNull()
  })
})

describe('eligibleMainAgents — membership can never mint an Admin row (architect Q1)', () => {
  it('ignores an admin roster entry even on the default workspace', () => {
    // Defence in depth below the roster: even if an admin-shaped entry with
    // a validated member main reached the input, only adminDefault (the
    // workspace-carried id) may produce the Admin row.
    const adminMemberEntry = { agent: ADMIN, member: { main_session_id: 'main-session-ws+admin-from-membership' } }
    const actual = eligibleMainAgents({
      workspaceId: 'default',
      isDefaultWorkspace: true,
      rosterState: 'fresh',
      roster: [adminMemberEntry],
      adminDefault: null,
      sessions: [],
      cachedRows: [],
    })
    expect(actual.status).toBe('ready')
    expect(actual.rows).toEqual([])
    expect(actual.missingMainAgentIds).toEqual([])
  })

  it('validates the carried id: blank and pending placeholders are unavailable, never rows', () => {
    for (const bad of ['', '   ', '__pending']) {
      const actual = eligibleMainAgents({
        workspaceId: 'default',
        isDefaultWorkspace: true,
        rosterState: 'fresh',
        roster: [],
        adminDefault: { agent: ADMIN, mainSessionId: bad },
        sessions: [],
        cachedRows: [],
      })
      expect(actual.status, `blank variant "${bad}"`).toBe('unavailable')
      expect(actual.retry).toBe(true)
      expect(actual.reason).toBe('main-id-missing')
      expect(actual.rows).toEqual([])
      expect(actual.missingMainAgentIds).toEqual(['admin'])
    }
  })
})
