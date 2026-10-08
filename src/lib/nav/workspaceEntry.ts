/**
 * Workspace entry (FR-004, BDD-02).
 *
 * A visible remembered chat wins. Anything else opens Ava's validated main,
 * or unavailable / Retry with send off. A transport failure keeps the
 * committed chat and does not fall back. Entry never acknowledges.
 */
import type { Session, WorkspaceMemberConfig } from '@/lib/api'
import { mainSessionIdOfMember } from './sessionCoreSeam'

export type EntryKind = 'workspace-name' | 'cold-reload' | 'login' | 'modal-switch'

export type PointerVerdict =
  | 'visible'
  | 'deleted'
  | 'hidden'
  | 'forbidden'
  | 'timeout'
  | 'offline'
  | 'server-error'
  | 'absent'

export type EntryInput = { // not-wire-format: arguments to the in-browser workspace-entry decision, assembled from SPA state; never sent to the gateway
  workspaceId: string
  entryKind: EntryKind
  rememberedSessionId: string | null | undefined
  pointerVerdict: PointerVerdict
  sessions: Session[]
  avaMember: WorkspaceMemberConfig
  committed: { sessionId: string; agentId: string } | null
}

export type ExactEntry = { // not-wire-format: client entry outcome for a visible remembered chat, consumed only by the SPA; not a gateway response
  status: 'exact'
  sessionId: string
  agentId: string
  sendEnabled: true
  acknowledged: false
}

export type WelcomeEntry = { // not-wire-format: client entry outcome that opens Ava's validated main chat; drives SPA routing only, never a wire body
  status: 'welcome'
  sessionId: string
  agentId: 'ava'
  sendEnabled: true
  acknowledged: false
}

export type UnavailableEntry = { // not-wire-format: client entry outcome when no main id is available; drives the SPA unavailable state and keeps send off
  status: 'unavailable'
  sessionId: null
  sendEnabled: false
  acknowledged: false
  retry: true
}

export type FailedAttempt = { // not-wire-format: client entry outcome that keeps the committed chat after a transport failure; SPA-only, never a wire body
  status: 'failed-attempt'
  committed: { sessionId: string; agentId: string } | null
  attempted: { sessionId: string; sendEnabled: false }
  acknowledged: false
  retry: true
  fellBack: false
}

export type EntryResult = ExactEntry | WelcomeEntry | UnavailableEntry | FailedAttempt

const TRANSPORT_FAILURE = new Set<PointerVerdict>(['timeout', 'offline', 'server-error'])

function isRealPointer(id: string | null | undefined): id is string {
  return typeof id === 'string' && id !== '__pending' && id.trim() !== ''
}

function validatedMainId(member: WorkspaceMemberConfig): string | undefined {
  const id = mainSessionIdOfMember(member)
  if (typeof id !== 'string') return undefined
  if (id.trim() === '' || id === '__pending') return undefined
  return id
}

function welcomeOrUnavailable(avaMember: WorkspaceMemberConfig): WelcomeEntry | UnavailableEntry {
  const sessionId = validatedMainId(avaMember)
  if (sessionId === undefined) {
    return {
      status: 'unavailable',
      sessionId: null,
      sendEnabled: false,
      acknowledged: false,
      retry: true,
    }
  }
  return {
    status: 'welcome',
    sessionId,
    agentId: 'ava',
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

  return welcomeOrUnavailable(input.avaMember)
}
