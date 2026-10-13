// Membership comes from core_team, not member_configs keys (founder bug
// 2026-10-11: a new Main agent — "omnipus" — was added to the workspace team
// but the sidebar showed no entry at all).
//
// The server OMITS a core_team member's member_configs entry when its main
// does not resolve and it has no stored settings (pkg/gateway/
// rest_workspaces.go, the C-MAIN comment: "a member whose main does not
// resolve gets no entry"). Building membership from member_configs keys
// therefore dropped the member SILENTLY: the eligible-mains 'main-id-missing'
// state ("Main chat unavailable" + Retry) could never fire for it.
//
// Oracles (team-lead dispatch, steer round 1 + 2):
//   1. A core_team member with NO member_configs entry → missingMainAgentIds
//      contains it and reason is 'main-id-missing' (the sidebar then shows
//      "Main chat unavailable" with Retry — covered at component level in
//      WorkspaceAgentList.coreTeam.test.tsx).
//   2. Once its member_configs entry with a main id arrives → the row
//      appears (status 'ready').
//   3. Guard: the type filter (core | Main only) applies BEFORE the
//      missing-main check — core_team deliberately contains helper/worker
//      agents (planner, researcher, a subagent_3p worker) that have NO main
//      by design; they must never become "Main chat unavailable" cases.
//   4. Guard: a core_team id absent from the loaded ['agents'] list (not yet
//      loaded / deleted) must not crash, must not produce a row, and must
//      not produce a missing-main note.
//   5. A workspace payload without core_team keeps the member_configs-keys
//      behaviour (legacy/absent-field graceful degradation).
import { describe, expect, it } from 'vitest'
import type { Session, Workspace } from '@/lib/api'
import { makeAgent } from '@/test/factories'
import { workspaceMains } from './workspaceRoster'

const WS = 'ws-1'

function makeWorkspace(overrides: Partial<Workspace> = {}): Workspace {
  return {
    id: WS,
    name: 'Alpha',
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2025-01-01T00:00:00Z',
    updated_at: '2025-01-01T00:00:00Z',
    revision: '0'.repeat(64),
    member_configs: {},
    ...overrides,
  }
}

const SESSIONS: Session[] = []

describe('workspaceMains membership via core_team (founder bug 2026-10-11)', () => {
  it('a core_team Main with no member_configs entry is a missing main, not a silent drop', () => {
    const omnipus = makeAgent({ id: 'omnipus', name: 'Omnipus', type: 'Main' })
    const workspace = makeWorkspace({ core_team: ['omnipus'], member_configs: {} })

    const mains = workspaceMains(workspace, [omnipus], SESSIONS, 'fresh', [])

    expect(mains.result.reason).toBe('main-id-missing')
    expect(mains.result.missingMainAgentIds).toContain('omnipus')
    expect(mains.result.rows).not.toContainEqual(expect.objectContaining({ agentId: 'omnipus' }))
  })

  it('once the member_configs entry with a main id arrives, the row appears (ready)', () => {
    const omnipus = makeAgent({ id: 'omnipus', name: 'Omnipus', type: 'Main' })
    const workspace = makeWorkspace({
      core_team: ['omnipus'],
      member_configs: { omnipus: { main_session_id: 'main-omnipus' } },
    })

    const mains = workspaceMains(workspace, [omnipus], SESSIONS, 'fresh', [])

    expect(mains.result.status).toBe('ready')
    expect(mains.result.missingMainAgentIds).toEqual([])
    expect(mains.result.rows).toEqual([
      { agentId: 'omnipus', workspaceId: WS, name: 'Omnipus', mainSessionId: 'main-omnipus' },
    ])
  })

  it('helpers in core_team (Subagent / subagent_3p, no main by design) never become missing mains — type filter runs before the main check', () => {
    const ava = makeAgent({ id: 'ava', name: 'Ava', type: 'Main' })
    const planner = makeAgent({ id: 'planner', name: 'Planner', type: 'Subagent' })
    const researcher = makeAgent({ id: 'researcher', name: 'Researcher', type: 'Subagent' })
    const extWorker = makeAgent({ id: 'ext-worker', name: 'Ext Worker', type: 'subagent_3p' })
    const newMain = makeAgent({ id: 'omnipus', name: 'Omnipus', type: 'Main' })
    const workspace = makeWorkspace({
      core_team: ['ava', 'planner', 'researcher', 'ext-worker', 'omnipus'],
      // researcher HAS a stored config but no main id — still a helper, still skipped by type.
      member_configs: {
        ava: { main_session_id: 'main-ava' },
        researcher: {},
      },
    })

    const mains = workspaceMains(
      workspace,
      [ava, planner, researcher, extWorker, newMain],
      SESSIONS,
      'fresh',
      [],
    )

    expect(mains.result.rows.map((row) => row.agentId)).toEqual(['ava'])
    expect(mains.result.missingMainAgentIds).toEqual(['omnipus'])
    expect(mains.result.reason).toBe('main-id-missing')
  })

  it('a core_team id absent from the agents list is neither a row nor a missing main, and does not crash', () => {
    const ava = makeAgent({ id: 'ava', name: 'Ava', type: 'Main' })
    const workspace = makeWorkspace({
      core_team: ['ava', 'ghost'],
      member_configs: { ava: { main_session_id: 'main-ava' } },
    })

    const mains = workspaceMains(workspace, [ava], SESSIONS, 'fresh', [])

    expect(mains.result.status).toBe('ready')
    expect(mains.result.missingMainAgentIds).toEqual([])
    expect(mains.result.rows.map((row) => row.agentId)).toEqual(['ava'])
  })

  it('a workspace payload without core_team keeps the member_configs-keys behaviour', () => {
    const mia = makeAgent({ id: 'mia', name: 'Mia', type: 'core' })
    const workspace = makeWorkspace({ member_configs: { mia: { main_session_id: 'main-mia' } } })
    delete (workspace as { core_team?: unknown }).core_team

    const mains = workspaceMains(workspace, [mia], SESSIONS, 'fresh', [])

    expect(mains.result.status).toBe('ready')
    expect(mains.result.rows).toEqual([
      { agentId: 'mia', workspaceId: WS, name: 'Mia', mainSessionId: 'main-mia' },
    ])
  })
})
