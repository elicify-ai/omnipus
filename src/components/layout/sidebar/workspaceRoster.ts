import type { Agent, Session, Workspace } from '@/lib/api'
import {
  eligibleMainAgents,
  type EligibleResult,
  type EligibleRosterEntry,
  type EligibleRow,
} from '@/lib/nav/eligibleMains'
import { projectMainAttention, type Signal } from '@/lib/nav/mainAttention'

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
  for (const [id, member] of Object.entries(configs)) {
    const agent = byId.get(id)
    if (agent) roster.push({ agent, member })
  }
  return roster
}

function adminDefault(workspace: Workspace, agents: Agent[]): EligibleRosterEntry | null {
  if (!workspace.is_default) return null
  const agent = agents.find((candidate) => candidate.id === ADMIN_AGENT_ID)
  if (!agent) return null
  return { agent, member: workspace.member_configs?.[ADMIN_AGENT_ID] ?? {} }
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
