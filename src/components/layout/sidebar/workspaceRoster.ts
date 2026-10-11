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

/** Built-in Admin. Rendered only from the default workspace's admin_main_session_id, never via membership. */
export const ADMIN_AGENT_ID = 'admin'

export type RosterState = 'fresh' | 'failed-no-cache' | 'failed-stale-cache'

export type WorkspaceMains = {
  result: EligibleResult
  /**
   * D5: the rows the sidebar lists — every projected row whose member is NOT
   * hidden_from_sidebar. `result.rows` still carries ALL rows.
   */
  visible: EligibleRow[]
  /**
   * D5: rows hidden from the list. They stay full members and their attention
   * still counts toward `signals`/`onCount` (ADR §SPA read path).
   */
  hidden: EligibleRow[]
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
  // D5 split: hidden is a DISPLAY flag on the member config. Admin's row is
  // not a member entry, so it can never land in `hidden`.
  const hiddenIds = new Set(
    Object.entries(workspace.member_configs ?? {})
      .filter(([, member]) => member.hidden_from_sidebar === true)
      .map(([id]) => id),
  )
  const visible: EligibleRow[] = []
  const hidden: EligibleRow[] = []
  for (const row of result.rows) {
    if (hiddenIds.has(row.agentId)) hidden.push(row)
    else visible.push(row)
  }
  // Global projection: a main id is whatever the seam validated, looked up
  // on the session list. Missing from the projection is unknown, never off.
  // Computed over ALL rows — hidden included — so hiding a row never mutes
  // the "a main chat needs you" signal on the collapsed workspace row.
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
  return { result, visible, hidden, signals, onCount, unknown }
}
