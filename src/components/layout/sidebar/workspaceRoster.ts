import type { Agent, Session, Workspace } from '@/lib/api'
import {
  eligibleMainAgents,
  type EligibleResult,
  type EligibleRosterEntry,
  type EligibleRow,
} from '@/lib/nav/eligibleMains'
import { projectMainAttention, type Signal } from '@/lib/nav/mainAttention'

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
    sessions,
    cachedRows,
  })
  // D5 split: hidden is a DISPLAY flag on the member config.
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
