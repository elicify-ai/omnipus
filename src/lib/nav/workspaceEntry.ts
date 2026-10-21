/**
 * Workspace entry (omnipus-system-agent FR-107, BDD-12.11, dataset D4;
 * session-core FR-004).
 *
 * A visible remembered chat wins. Otherwise the main of the first
 * conversation agent in the team's order (Omnipus excluded — FR-004), or,
 * when the team has no conversation agent, the empty state with "Set up a
 * team" (send off). A first agent whose main does not resolve is unavailable
 * / Retry — never a silent fallback to the next agent. A transport failure
 * (sessions, workspace or agents not loaded) keeps the committed chat and
 * does not fall back. Entry never acknowledges.
 */
import type { Agent, Session, WorkspaceMemberConfig } from '@/lib/api'
import type { WorkspaceEntryView } from '@/store/session/workspaceEntryFlow'
import { isConversationAgent } from '@/lib/omnipus'
import { mainSessionIdOfMember } from './sessionCoreSeam'

/** A loading or failed target is not a committed send destination (FR-004). */
export function workspaceEntryBlocksSend(state: {
  workspaceEntry: WorkspaceEntryView | null
  resolvingSessionForWorkspace: Record<string, boolean>
}, workspaceId: string | null): boolean {
  const entry = state.workspaceEntry
  return !!(workspaceId && state.resolvingSessionForWorkspace?.[workspaceId])
    || entry?.status === 'failed-attempt'
    || !!(entry && 'sendEnabled' in entry && entry.sendEnabled === false)
}

export type EntryKind = 'workspace-name' | 'cold-reload' | 'login' | 'modal-switch'

export type PointerVerdict =
  | 'visible'
  | 'deleted'
  /** M3: absent from a list the API itself marked incomplete — unknown, not deleted. */
  | 'partial-list'
  | 'hidden'
  | 'forbidden'
  | 'timeout'
  | 'offline'
  | 'server-error'
  | 'absent'

/** The loaded team: membership order, member configs and the agents to classify them. */
export type EntryTeam = { // not-wire-format: the workspace team as loaded by the SPA for the entry decision; never sent to the gateway
  coreTeam: readonly string[]
  memberConfigs: Readonly<Record<string, WorkspaceMemberConfig>>
  agents: readonly Pick<Agent, 'id' | 'type'>[]
}

export type EntryInput = { // not-wire-format: arguments to the in-browser workspace-entry decision, assembled from SPA state; never sent to the gateway
  workspaceId: string
  entryKind: EntryKind
  rememberedSessionId: string | null | undefined
  pointerVerdict: PointerVerdict
  sessions: Session[]
  team: EntryTeam
  committed: { sessionId: string; agentId: string } | null
}

export type ExactEntry = { // not-wire-format: client entry outcome for a visible remembered chat, consumed only by the SPA; not a gateway response
  status: 'exact'
  sessionId: string
  agentId: string
  sendEnabled: true
  acknowledged: false
}

export type WelcomeEntry = { // not-wire-format: client entry outcome that opens the first conversation agent's validated main; drives SPA routing only, never a wire body
  status: 'welcome'
  sessionId: string
  agentId: string
  sendEnabled: true
  acknowledged: false
}

export type EmptyTeamEntry = { // not-wire-format: client entry outcome for a team with no conversation agent; drives the "Set up a team" empty state, never a wire body
  status: 'empty-team'
  sessionId: null
  sendEnabled: false
  acknowledged: false
  setupOffer: true
}

export type UnavailableEntry = { // not-wire-format: client entry outcome when no main id is available; drives the SPA unavailable state and keeps send off
  status: 'unavailable'
  sessionId: null
  sendEnabled: false
  acknowledged: false
  retry: true
  mainSessionError?: WorkspaceMemberConfig['main_session_error']
}

export type FailedAttempt = { // not-wire-format: client entry outcome that keeps the committed chat after a transport failure; SPA-only, never a wire body
  status: 'failed-attempt'
  committed: { sessionId: string; agentId: string } | null
  attempted: { sessionId: string; sendEnabled: false }
  acknowledged: false
  retry: true
  fellBack: false
}

export type EntryResult = ExactEntry | WelcomeEntry | EmptyTeamEntry | UnavailableEntry | FailedAttempt

// 'partial-list' rides here as a fail-safe: an unresolved partial-list verdict
// must never reach the welcome path (that would overwrite a pointer the entry
// could not actually check). workspaceEntryFlow resolves it before this point.
const TRANSPORT_FAILURE = new Set<PointerVerdict>(['timeout', 'offline', 'server-error', 'partial-list'])

function isRealPointer(id: string | null | undefined): id is string {
  return typeof id === 'string' && id !== '__pending' && id.trim() !== ''
}

function validatedMainId(member: WorkspaceMemberConfig): string | undefined {
  const id = mainSessionIdOfMember(member)
  if (typeof id !== 'string') return undefined
  if (id.trim() === '' || id === '__pending') return undefined
  return id
}

/** The first conversation agent in team order, Omnipus and workers excluded (FR-107). */
export function firstConversationAgentId(team: EntryTeam): string | undefined {
  const byId = new Map(team.agents.map((agent) => [agent.id, agent]))
  for (const id of team.coreTeam) {
    const agent = byId.get(id)
    if (agent && isConversationAgent(agent)) return id
  }
  return undefined
}

function teamEntry(team: EntryTeam): WelcomeEntry | EmptyTeamEntry | UnavailableEntry {
  const agentId = firstConversationAgentId(team)
  if (agentId === undefined) {
    return { status: 'empty-team', sessionId: null, sendEnabled: false, acknowledged: false, setupOffer: true }
  }
  const member = team.memberConfigs[agentId] ?? {}
  const sessionId = validatedMainId(member)
  if (sessionId === undefined) {
    return {
      status: 'unavailable',
      sessionId: null,
      sendEnabled: false,
      acknowledged: false,
      retry: true,
      ...(member.main_session_error ? { mainSessionError: member.main_session_error } : {}),
    }
  }
  return {
    status: 'welcome',
    sessionId,
    agentId,
    sendEnabled: true,
    acknowledged: false,
  }
}

export function resolveWorkspaceEntry(input: EntryInput): EntryResult {
  if (TRANSPORT_FAILURE.has(input.pointerVerdict)) {
    const attemptedId = typeof input.rememberedSessionId === 'string' ? input.rememberedSessionId : ''
    return {
      status: 'failed-attempt',
      committed: input.committed,
      attempted: { sessionId: attemptedId, sendEnabled: false },
      acknowledged: false,
      retry: true,
      fellBack: false,
    }
  }

  if (input.pointerVerdict === 'visible' && isRealPointer(input.rememberedSessionId)) {
    const match = input.sessions.find((session) => session.id === input.rememberedSessionId)
    return {
      status: 'exact',
      sessionId: input.rememberedSessionId,
      agentId: match?.agent_id ?? '',
      sendEnabled: true,
      acknowledged: false,
    }
  }

  return teamEntry(input.team)
}
