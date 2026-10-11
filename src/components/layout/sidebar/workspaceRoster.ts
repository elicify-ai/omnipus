import type { Agent, Session, Workspace } from '@/lib/api'
import {
  eligibleMainAgents,
  type EligibleAdminEntry,
  type EligibleResult,
  type EligibleRosterEntry,
  type EligibleRow,
} from '@/lib/nav/eligibleMains'
import { projectMainAttention, type Signal } from '@/lib/nav/mainAttention'
import { adminMainSessionIdOfWorkspace } from '@/lib/nav/sessionCoreSeam'

const ADMIN_AGENT_ID = 'admin'

export type RosterState = 'fresh' | 'failed-no-cache' | 'failed-stale-cache'

export type WorkspaceMains = {
  result: EligibleResult
  signals: Record<string, Signal>
  /** Mains whose attention is on. Meaningful only when `unknown` is false. */
  onCount: number
  /** Any shown main has unknown attention. A number would be a lie. */
  unknown: boolean
}

function memberRoster(workspace: Workspace, agents: Agent[]): EligibleRosterEntry[] {
  const byId = new Map(agents.map((agent) => [agent.id, agent]))
  const configs = workspace.member_configs ?? {}
  const roster: EligibleRosterEntry[] = []
  // Membership is core_team — the authoritative team list. The server OMITS a
  // member's member_configs entry when its main does not resolve and it has
  // no stored settings (pkg/gateway/rest_workspaces.go, C-MAIN), so deriving
  // membership from member_configs keys dropped that member SILENTLY and the
  // eligible-mains 'main-id-missing' state ("Main chat unavailable") could
  // never fire for it (founder bug 2026-10-11, agent "omnipus").
  // member_configs is only the per-member config lookup here; a member whose
  // config is absent carries `undefined` and lands in missingMainAgentIds via
  // eligibleMains's own type-filter-first eligibility rules.
  // Workspaces without a core_team field (optional on the wire) keep the
  // member_configs-keys behaviour.
  const memberIds = workspace.core_team ?? Object.keys(configs)
  for (const id of memberIds) {
    const agent = byId.get(id)
    // An id absent from the loaded agents list (not yet loaded / deleted) is
    // neither a row nor a missing main — it cannot be classified at all.
    if (agent) roster.push({ agent, member: configs[id] })
  }
  return roster
}

function adminDefault(workspace: Workspace, agents: Agent[]): EligibleAdminEntry | null {
  if (!workspace.is_default) return null
  const agent = agents.find((candidate) => candidate.id === ADMIN_AGENT_ID)
  if (!agent) return null
  // Admin is not a member: its main id rides on the default workspace itself.
  return { agent, mainSessionId: adminMainSessionIdOfWorkspace(workspace) }
}

export function workspaceMains(
  workspace: Workspace,
  agents: Agent[],
  sessions: Session[],
  rosterState: RosterState,
  cachedRows: EligibleRow[],
): WorkspaceMains {
  const result = eligibleMainAgents({
    workspaceId: workspace.id,
    isDefaultWorkspace: workspace.is_default === true,
    rosterState,
    roster: memberRoster(workspace, agents),
    adminDefault: adminDefault(workspace, agents),
    sessions,
    cachedRows,
  })
  // Global projection: a main id is whatever the seam validated, looked up
  // on the session list. Missing from the projection is unknown, never off.
  const projection = projectMainAttention(sessions)
  const signals: Record<string, Signal> = {}
  let onCount = 0
  let unknown = false
  for (const row of result.rows) {
    const signal = projection.byMainId[row.mainSessionId] ?? 'unknown'
    signals[row.mainSessionId] = signal
    if (signal === 'unknown') unknown = true
    else if (signal === 'on') onCount += 1
  }
  return { result, signals, onCount, unknown }
}
