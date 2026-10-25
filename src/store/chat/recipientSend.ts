// recipientSend — store-side wiring for the send-time @-recipient derivation
// (founder decision 2026-10-11 Q1 B). The pure parse/resolve lives in
// src/lib/recipientAddress.ts; this module reads the live stores + the
// `['agents']` react-query cache that feed it, and owns the two visible
// blocks the no-session rule needs:
//
//   - the COMPOSER block (ChatScreen's three submit paths): an @-addressed
//     submit in a brand-new chat is refused visibly and the text is kept —
//     the server refuses a recipient without session_id
//     (pkg/gateway/ws_recipient.go::refuseRecipient), and sending the
//     message unaddressed would silently drop the addressing;
//   - the DRAIN block (store.ts::maybeDrainNext, before the pop): a QUEUED
//     @-addressed message holds the queue (with the same warning) instead of
//     draining into an unaddressed first send; the hold releases as soon as
//     a session exists, and the message then drains WITH its recipient.

import { queryClient } from '@/lib/queryClient'
import { logDiagnostic } from '@/lib/telemetry'
import type { Agent } from '@/lib/api'
import type { OutboundRecipient } from '@/lib/recipientAddress'
import { resolveSendRecipient } from '@/lib/recipientAddress'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { OutboundTarget } from './types'

export interface AddressedAgent {
  recipient: OutboundRecipient
  /** The addressed agent's display name, for the visible-block copy. */
  agentName: string
}

/**
 * The session the next frame would ride on — or null. `activeSessionId` is
 * the optimistic `'__pending'` sentinel while a brand-new chat's very first
 * turn is in flight; that string is NOT a session id (a frame carrying it is
 * a protocol violation — see sendMessage's own '__pending' guards), so for
 * the recipient rule it counts as "no session yet".
 */
function currentSessionId(): string | null {
  const sid = useSessionStore.getState().activeSessionId
  return sid === '__pending' ? null : sid
}

/**
 * Derive the recipient for `content` from CURRENT state — always read fresh
 * at send time, never a captured render value. Returns null when the text
 * carries no exact leading "@<Name>" address (the ordinary-text case).
 */
export function resolveAddressedAgent(content: string, target?: OutboundTarget): AddressedAgent | null {
  if (!content.startsWith('@')) return null
  const agents = queryClient.getQueryData<Agent[]>(['agents']) ?? []
  const activeAgentId = target ? target.agentId : useSessionStore.getState().activeAgentId
  const activeWorkspaceId = target ? target.workspaceId : useWorkspacesStore.getState().activeWorkspaceId
  const recipient = resolveSendRecipient({ content, agents, activeAgentId, activeWorkspaceId })
  if (!recipient) return null
  const agentName = agents.find((agent) => agent.id === recipient.agent_id)?.name ?? recipient.agent_id
  return { recipient, agentName }
}

/**
 * The send-side truth about a leading "@" (C8, R3 delta re-review): an
 * agents cache that has NEVER resolved leaves the address unchecked, and
 * sending anyway would silently drop it. Four states:
 *   - `addressed` — a loaded candidate list matched the exact name;
 *   - `plain` — no leading "@" at all, or a leading "@" the loaded list
 *     matches nothing (ordinary text by design: the derivation is not
 *     sticky, an edited prefix travels verbatim);
 *   - `unresolvable` — a leading "@" but the `['agents']` cache holds no
 *     resolved data and the query is still in flight: the list may yet
 *     arrive. Callers must refuse visibly instead of degrading to an
 *     unaddressed send.
 *   - `failed` — the `['agents']` query has ERRORED with no data (S3-08):
 *     waiting will not help, so the copy says the list could not be loaded
 *     and offers a Retry instead of promising "try again in a moment".
 */
export type AddressCheck =
  | { kind: 'addressed'; addressed: AddressedAgent }
  | { kind: 'plain' }
  | { kind: 'unresolvable'; nameGuess: string }
  | { kind: 'failed'; nameGuess: string }

export function checkAddressedAgent(content: string, target?: OutboundTarget): AddressCheck {
  if (!content.startsWith('@')) return { kind: 'plain' }
  if (queryClient.getQueryData<Agent[]>(['agents']) === undefined) {
    // The raw first token after "@" — copy material only, never a
    // resolution; no message content beyond it is logged or shown.
    const nameGuess = content.slice(1).match(/^\S+/)?.[0] ?? ''
    if (queryClient.getQueryState(['agents'])?.status === 'error') {
      return { kind: 'failed', nameGuess }
    }
    return { kind: 'unresolvable', nameGuess }
  }
  const addressed = resolveAddressedAgent(content, target)
  return addressed ? { kind: 'addressed', addressed } : { kind: 'plain' }
}

/** Visible-block copy, shared by the composer block and the drain block so both say exactly the same thing. */
export function recipientAddressBlockMessage(agentName: string): string {
  return `A message addressed to @${agentName} needs an existing conversation — this chat has not started yet. Send an unaddressed first message to open it, then pick @${agentName} again. Your message was kept in the composer.`
}

/**
 * C8 — visible-block copy for the unresolvable-address corner (the agents
 * cache has never resolved AND is still loading). Shared by the composer
 * block, the drain hold and the resend block; truthful in all three (nothing
 * was sent).
 */
export function recipientAddressUnresolvedBlockMessage(nameGuess: string): string {
  const addressed = nameGuess ? `@${nameGuess}` : 'the @ address'
  return `The team list has not loaded yet, so ${addressed} cannot be checked as an addressee. The message was not sent — try again in a moment.`
}

/**
 * C8 follow-up (S3-08) — visible-block copy for the FAILED corner: the
 * agents query has errored with no data, so waiting will not help. Says what
 * happened instead of promising a load; the toast carries a Retry action
 * (retryAgentsRoster) as the offer.
 */
export function recipientAddressFailedBlockMessage(nameGuess: string): string {
  const addressed = nameGuess ? `@${nameGuess}` : 'the @ address'
  return `The team list could not be loaded, so ${addressed} cannot be checked as an addressee. The message was not sent.`
}

/**
 * One recovery attempt on the agents roster (S3-08). Safe to call on every
 * blocked send/drain trigger: react-query dedupes concurrent refetches. The
 * load itself releases a held message through armRosterRelease (R2-S3-02).
 */
export function retryAgentsRoster(): void {
  void queryClient.refetchQueries({ queryKey: ['agents'] })
}

// R2-S3-02: a queued @-message held on a loading/failed roster used to wait
// for some unrelated drain trigger (a turn end, a reconnect) - on an idle chat
// none comes. The hold now arms ONE watch on the roster query; the first
// successful load drops it and re-runs the queue drain. The drain itself is
// registered by the store (this module cannot import the store: store.ts
// imports this one).
let releaseDrain: (() => void) | null = null
let stopWatchingRoster: (() => void) | null = null

export function setRosterReleaseDrain(drain: () => void): void {
  releaseDrain = drain
}

function armRosterRelease(): void {
  if (stopWatchingRoster) return
  stopWatchingRoster = queryClient.getQueryCache().subscribe((event) => {
    if (event.type !== 'updated' || event.action.type !== 'success') return
    if (event.query.queryKey[0] !== 'agents' || event.query.state.data === undefined) return
    stopWatchingRoster?.()
    stopWatchingRoster = null
    releaseDrain?.()
  })
}

/**
 * C8 — visible-block copy for a RETRY whose target bucket has no session
 * ('__pending'): the row stays in the thread with its Retry affordance, not
 * in a composer.
 */
export function recipientAddressResendBlockMessage(agentName: string): string {
  return `A message addressed to @${agentName} needs an existing conversation — this chat has not started yet. The message stays in the thread; retry once the chat has started.`
}

/**
 * The composer's no-session block. Non-null when `content` must NOT go out:
 * an @-addressed message with no session to carry the recipient, or an
 * address that cannot be checked because the agents cache has never
 * resolved (C8 — the caller must not submit, shows this warning, and keeps
 * the composer text exactly as typed). Both unavailable shapes — still
 * loading, or FAILED (S3-08: the failed copy + a Retry action) — return the
 * same 'unresolvable' sentinel; only the toast copy differs. Null when the
 * text is ordinary or safely plain.
 */
export function recipientAddressBlocked(content: string): AddressedAgent | 'unresolvable' | null {
  const check = checkAddressedAgent(content)
  if (check.kind === 'plain') return null
  if (check.kind === 'unresolvable') {
    useUiStore.getState().addToast({
      message: recipientAddressUnresolvedBlockMessage(check.nameGuess),
      variant: 'warning',
    })
    logDiagnostic('chatRecipientSendBlockedAgentsUnavailable', { nameGuess: check.nameGuess })
    return 'unresolvable'
  }
  if (check.kind === 'failed') {
    useUiStore.getState().addToast({
      message: recipientAddressFailedBlockMessage(check.nameGuess),
      variant: 'warning',
      action: { label: 'Retry', onClick: retryAgentsRoster },
    })
    logDiagnostic('chatRecipientSendBlockedAgentsErrored', { nameGuess: check.nameGuess })
    return 'unresolvable'
  }
  if (currentSessionId()) return null
  useUiStore.getState().addToast({
    message: recipientAddressBlockMessage(check.addressed.agentName),
    variant: 'warning',
  })
  // Length-free diagnostic: the agent name, never the message content.
  logDiagnostic('chatRecipientSendBlockedNoSession', { agentName: check.addressed.agentName })
  return check.addressed
}

/**
 * The drain-side twin (store.ts::maybeDrainNext, BEFORE the pop): true when
 * the queued head must not go out — an @-addressed message with no session
 * (draining would send it unaddressed, silently dropping the @), or an
 * address that cannot be checked because the agents cache has never
 * resolved (C8). The queue HOLDS; the warning is deduplicated on its exact
 * message so repeated drain triggers cannot stack identical toasts.
 *
 * The hold is never a silent wait for a load that may never come (S3-08):
 * on a FAILED roster every drain trigger kicks a roster refetch
 * (deduplicated by react-query) and the copy says the list could not be
 * loaded, with a Retry action on the composer path. A held @-message is
 * only ever released by real roster data — releasing it into an
 * unaddressed send would be the silent drop C8 forbids.
 */
export function recipientAddressHoldsQueue(content: string | undefined, target?: OutboundTarget): boolean {
  if (!content) return false
  const check = checkAddressedAgent(content, target)
  if (check.kind === 'plain') return false
  const message = check.kind === 'unresolvable'
    ? recipientAddressUnresolvedBlockMessage(check.nameGuess)
    : check.kind === 'failed'
      ? recipientAddressFailedBlockMessage(check.nameGuess)
      : recipientAddressBlockMessage(check.addressed.agentName)
  if (check.kind === 'addressed' && (target ? target.sessionId && target.sessionId !== '__pending' : currentSessionId())) return false
  const ui = useUiStore.getState()
  const alreadyShown = ui.toasts.some((toast) => toast.variant === 'warning' && toast.message === message)
  if (!alreadyShown) {
    ui.addToast({ message, variant: 'warning', ...(check.kind === 'failed' ? { action: { label: 'Retry', onClick: retryAgentsRoster } } : {}) })
  }
  // Active recovery on the FAILED corner: every drain trigger retries the
  // roster (refetchQueries dedupes), so a transient failure self-heals and
  // the queue drains with its recipient; a permanent one stays loud.
  if (check.kind === 'failed') retryAgentsRoster()
  if (check.kind === 'failed' || check.kind === 'unresolvable') armRosterRelease()
  if (check.kind === 'unresolvable') {
    logDiagnostic('chatRecipientQueueHeldAgentsUnavailable', { nameGuess: check.nameGuess })
  } else if (check.kind === 'failed') {
    logDiagnostic('chatRecipientQueueHeldAgentsErrored', { nameGuess: check.nameGuess })
  } else {
    logDiagnostic('chatRecipientQueueHeldNoSession', { agentName: check.addressed.agentName })
  }
  return true
}
