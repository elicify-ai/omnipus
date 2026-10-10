/**
 * Eligible workspace mains (FR-001, FR-003, BDD-01).
 *
 * A row's main id comes only from the session-core seam. This module never
 * builds `main-session-…`, never picks the newest session, and never treats
 * a failed roster as an empty team.
 */
import type { Agent, Session, WorkspaceMemberConfig } from '@/lib/api'
import { mainSessionIdOfMember } from './sessionCoreSeam'

export type EligibleRow = { // not-wire-format: browser-built roster row for one eligible workspace main; assembled from seam ids, never a gateway payload
  agentId: string
  workspaceId: string
  name: string
  mainSessionId: string
}

export type EligibleResult = { // not-wire-format: in-browser result of the eligible-mains computation, including retry and stale flags; never crosses the gateway
  status: 'ready' | 'unavailable' | 'stale'
  retry: boolean
  reason: 'main-id-missing' | 'roster-failed' | null
  rows: EligibleRow[]
  missingMainAgentIds: string[]
}

export type EligibleRosterEntry = { // not-wire-format: local pairing of a generated Agent with its workspace member, used only inside eligible-mains logic
  agent: Agent
  member: WorkspaceMemberConfig
}

export type EligibleAdminEntry = { // not-wire-format: the loaded Admin agent plus the seam-read Admin main id; never a membership entry
  agent: Agent
  mainSessionId: string | undefined
}

export type EligibleInput = { // not-wire-format: function arguments assembled from already-loaded SPA state for eligible mains; not a request or response body
  workspaceId: string
  isDefaultWorkspace: boolean
  rosterState: 'fresh' | 'failed-no-cache' | 'failed-stale-cache'
  roster: EligibleRosterEntry[]
  /** Default-workspace Admin, even though Admin is not a member: the agent plus
   *  the id the seam read from Workspace.admin_main_session_id (or undefined). */
  adminDefault: EligibleAdminEntry | null
  sessions: Session[]
  cachedRows: EligibleRow[]
}

/** Chat colleagues. Workers and hidden engines are not mains. */
const COLLEAGUE_TYPES = new Set<Agent['type']>(['core', 'Main'])

/** Built-in Admin. Shown only as the validated default-workspace main, never via membership. */
const ADMIN_AGENT_ID = 'admin'

function isEligibleColleague(agent: Agent): boolean {
  return agent.id !== ADMIN_AGENT_ID && COLLEAGUE_TYPES.has(agent.type)
}

function validatedId(id: string | undefined): string | undefined {
  if (typeof id !== 'string') return undefined
  if (id.trim() === '' || id === '__pending') return undefined
  return id
}

function validatedMainId(member: WorkspaceMemberConfig): string | undefined {
  return validatedId(mainSessionIdOfMember(member))
}

function rowFor(workspaceId: string, agent: Agent, mainSessionId: string): EligibleRow {
  return {
    agentId: agent.id,
    workspaceId,
    name: agent.name,
    mainSessionId,
  }
}

function cachedRowStillEligible(row: EligibleRow, input: EligibleInput): boolean {
  if (row.workspaceId !== input.workspaceId) return false
  if (row.agentId === ADMIN_AGENT_ID) return input.isDefaultWorkspace
  const known = input.roster.find((entry) => entry.agent.id === row.agentId)
  if (!known) return true
  return isEligibleColleague(known.agent)
}

export function eligibleMainAgents(input: EligibleInput): EligibleResult {
  if (input.rosterState === 'failed-no-cache') {
    return {
      status: 'unavailable',
      retry: true,
      reason: 'roster-failed',
      rows: [],
      missingMainAgentIds: [],
    }
  }

  if (input.rosterState === 'failed-stale-cache') {
    return {
      status: 'stale',
      retry: true,
      reason: null,
      rows: input.cachedRows.filter((row) => cachedRowStillEligible(row, input)),
      missingMainAgentIds: [],
    }
  }

  const rows: EligibleRow[] = []
  const missingMainAgentIds: string[] = []

  for (const entry of input.roster) {
    if (!isEligibleColleague(entry.agent)) continue
    const mainSessionId = validatedMainId(entry.member)
    if (mainSessionId === undefined) {
      missingMainAgentIds.push(entry.agent.id)
      continue
    }
    rows.push(rowFor(input.workspaceId, entry.agent, mainSessionId))
  }

  if (input.isDefaultWorkspace && input.adminDefault) {
    const mainSessionId = validatedId(input.adminDefault.mainSessionId)
    if (mainSessionId === undefined) {
      missingMainAgentIds.push(input.adminDefault.agent.id)
    } else {
      rows.push(rowFor(input.workspaceId, input.adminDefault.agent, mainSessionId))
    }
  }

  if (missingMainAgentIds.length > 0) {
    return {
      status: 'unavailable',
      retry: true,
      reason: 'main-id-missing',
      rows,
      missingMainAgentIds,
    }
  }

  return {
    status: 'ready',
    retry: false,
    reason: null,
    rows,
    missingMainAgentIds,
  }
}
