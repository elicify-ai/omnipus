/**
 * Omnipus system agent — the SPA's one place for its identity rules
 * (omnipus-system-agent spec FR-001, FR-004, FR-104, FR-106; ADR-20261011
 * OSA-10).
 *
 * Omnipus is an ordinary but hidden member of the default workspace only. It
 * is reached through its fixed sidebar entry, never through a workspace's
 * Team list, sidebar agent rows, workspace-entry selection or the
 * add-existing picker. Its one conversation is its main in the default
 * workspace: `Workspace.member_configs.omnipus.main_session_id` (the server
 * computes it like every member main — pkg/gateway/rest_workspaces.go).
 *
 * The ids below are the server's well-known agent ids
 * (pkg/coreagent/core.go::IDOmnipus, IDJudge, IDPlanSupervisor).
 */
import type { Agent, Workspace } from '@/lib/api'
import { mainSessionIdOfMember } from '@/lib/nav/sessionCoreSeam'
import { isChatEligibleAgent } from '@/lib/agentKind'

export const OMNIPUS_AGENT_ID = 'omnipus'
export const JUDGE_AGENT_ID = 'judge'
export const PLAN_SUPERVISOR_AGENT_ID = 'plansupervisor'

/** Settings → System agents, in the order the spec names them (FR-106). */
export const SYSTEM_AGENT_IDS = [JUDGE_AGENT_ID, PLAN_SUPERVISOR_AGENT_ID, OMNIPUS_AGENT_ID] as const

export function isOmnipusAgentId(id: string | null | undefined): boolean {
  return id === OMNIPUS_AGENT_ID
}

/** Conversation agents: chat colleagues (built-in core or custom Main). */
const CONVERSATION_TYPES = new Set<Agent['type']>(['core', 'Main'])
/** Workers: delegation-only, native or external program. */
const WORKER_TYPES = new Set<Agent['type']>(['Subagent', 'subagent_3p'])

export type TeamKind = 'conversation' | 'worker' // not-wire-format: UI grouping of a Team card, derived from the generated Agent type

/**
 * Where an agent shows up on a workspace's Team list (F:D20, FR-102):
 * conversation agents and workers in separate groups; Omnipus and the system
 * agents never (FR-004, FR-104).
 */
export function teamKindOf(agent: Pick<Agent, 'id' | 'type'>): TeamKind | null {
  if (isOmnipusAgentId(agent.id)) return null
  if (CONVERSATION_TYPES.has(agent.type)) return 'conversation'
  if (WORKER_TYPES.has(agent.type)) return 'worker'
  return null
}

/** A conversation agent other than Omnipus (workspace entry, FR-107). */
export function isConversationAgent(agent: Pick<Agent, 'id' | 'type'>): boolean {
  return teamKindOf(agent) === 'conversation'
}

/**
 * Team-eligible for the add-existing picker (FR-104): conversation agents and
 * workers; never Omnipus, Judge, Plan Supervisor or any other system agent.
 * A `core` agent is a shipped built-in, so it is eligible only while it is a
 * current chat built-in (lib/agentKind.ts::isChatEligibleAgent): a retired
 * built-in that a stale agents cache still carries is never offered (FR-008).
 */
export function isTeamEligible(agent: Pick<Agent, 'id' | 'type'>): boolean {
  if (agent.type === 'core' && !isChatEligibleAgent(agent)) return false
  return teamKindOf(agent) !== null
}

/** The default workspace, or undefined when the list has none (yet). */
export function defaultWorkspaceOf(workspaces: readonly Workspace[]): Workspace | undefined {
  return workspaces.find((workspace) => workspace.is_default === true)
}

/**
 * Omnipus's one conversation: its validated main in the default workspace, or
 * undefined when it does not resolve (never a guessed id).
 */
export function omnipusMainSessionId(workspaces: readonly Workspace[]): string | undefined {
  const home = defaultWorkspaceOf(workspaces)
  if (!home) return undefined
  return mainSessionIdOfMember(home.member_configs?.[OMNIPUS_AGENT_ID])
}
