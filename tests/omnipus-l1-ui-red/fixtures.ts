// WU-10 test fixtures; wire objects use generated contracts, never parallel types.
import type { Agent, Workspace, WorkspaceDelegation } from '@/lib/api/generated/openapi-types'
// fetchSessions returns this existing stats-flattened SPA view, not a wire body.
import type { Session } from '@/lib/api/sessions'
import { makeAgent } from '@/test/factories'

export const REVISION = '2'.repeat(64)
export const SALES_MAIN = 'opaque-sales-mia-main'
export const OMNIPUS_MAIN = 'opaque-default-omnipus-main'

export function agent(id: string, overrides: Partial<Agent> = {}): Agent {
  return makeAgent({
    id,
    name: id === 'plansupervisor' ? 'Plan Supervisor' : id.charAt(0).toUpperCase() + id.slice(1),
    model: 'fixture-model',
    revision: REVISION,
    ...overrides,
  })
}

export const MIA = agent('mia', { type: 'core', locked: true, model: 'sales-model' })
export const JIM = agent('jim', { type: 'core', locked: true })
export const OMNIPUS = agent('omnipus', { type: 'core', locked: true, needs_model: true, model: '' })
export const JUDGE = agent('judge', { type: 'system', locked: true, memory_enabled: false })
export const PLAN_SUPERVISOR = agent('plansupervisor', { type: 'system', locked: true, memory_enabled: false })
export const WORKER = agent('researcher', { name: 'Researcher', type: 'Subagent', executor: { kind: 'native' } })
export const EXTERNAL_WORKER = agent('external', { name: 'External worker', type: 'subagent_3p', executor: { kind: 'external-cli', cli: 'codex' } })

export function workspace(id: string, overrides: Partial<Workspace> = {}): Workspace {
  return {
    id,
    name: id === 'default' ? 'Default' : id.charAt(0).toUpperCase() + id.slice(1),
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2026-10-13T00:00:00Z',
    updated_at: '2026-10-13T00:00:00Z',
    revision: REVISION,
    core_team: [],
    ...overrides,
  }
}

export const SALES = workspace('sales', {
  core_team: ['mia', 'researcher', 'external'],
  member_configs: { mia: { main_session_id: SALES_MAIN } },
})
export const DEFAULT = workspace('default', {
  is_default: true,
  core_team: ['omnipus', 'mia'],
  member_configs: {
    omnipus: { main_session_id: OMNIPUS_MAIN },
    mia: { main_session_id: 'opaque-default-mia-main' },
  },
})
export const RESEARCH = workspace('research', { core_team: ['mia'] })

export function delegation(ws: Workspace): WorkspaceDelegation {
  return { revision: REVISION, workspace_id: ws.id, team: ws.core_team ?? [], edges: [], default_depth: 1 }
}

export function session(id: string, agentId: string, workspaceId = 'sales', overrides: Partial<Session> = {}): Session {
  return {
    id,
    agent_id: agentId,
    workspace_id: workspaceId,
    type: 'chat',
    title: `${agentId} main`,
    created_at: '2026-10-13T00:00:00Z',
    updated_at: '2026-10-13T00:00:00Z',
    message_count: 0,
    ...overrides,
  }
}
