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
export function resolveAddressedAgent(content: string): AddressedAgent | null {
  if (!content.startsWith('@')) return null
  const agents = queryClient.getQueryData<Agent[]>(['agents']) ?? []
  const { activeAgentId } = useSessionStore.getState()
  const { activeWorkspaceId } = useWorkspacesStore.getState()
  const recipient = resolveSendRecipient({ content, agents, activeAgentId, activeWorkspaceId })
  if (!recipient) return null
  const agentName = agents.find((agent) => agent.id === recipient.agent_id)?.name ?? recipient.agent_id
  return { recipient, agentName }
}

/** Visible-block copy, shared by the composer block and the drain block so both say exactly the same thing. */
export function recipientAddressBlockMessage(agentName: string): string {
  return `A message addressed to @${agentName} needs an existing conversation — this chat has not started yet. Send an unaddressed first message to open it, then pick @${agentName} again. Your message was kept in the composer.`
}

/**
 * The composer's no-session block. True when `content` is @-addressed and
 * the current chat has no session to carry the recipient: the caller must
 * NOT submit (preventDefault / early-return), shows this warning, and keeps
 * the composer text exactly as typed.
 */
export function recipientAddressBlocked(content: string): AddressedAgent | null {
  const addressed = resolveAddressedAgent(content)
  if (!addressed) return null
  if (currentSessionId()) return null
  useUiStore.getState().addToast({
    message: recipientAddressBlockMessage(addressed.agentName),
    variant: 'warning',
  })
  // Length-free diagnostic: the agent name, never the message content.
  logDiagnostic('chatRecipientSendBlockedNoSession', { agentName: addressed.agentName })
  return addressed
}

/**
 * The drain-side twin (store.ts::maybeDrainNext, BEFORE the pop): true when
 * the queued head is @-addressed but no session exists — the queue must HOLD
 * (draining would send the message unaddressed, silently dropping the @).
 * The warning is deduplicated on its exact message so repeated drain
 * triggers cannot stack identical toasts.
 */
export function recipientAddressHoldsQueue(content: string | undefined): boolean {
  if (!content) return false
  const addressed = resolveAddressedAgent(content)
  if (!addressed) return false
  if (currentSessionId()) return false
  const message = recipientAddressBlockMessage(addressed.agentName)
  const ui = useUiStore.getState()
  const alreadyShown = ui.toasts.some((toast) => toast.variant === 'warning' && toast.message === message)
  if (!alreadyShown) {
    ui.addToast({ message, variant: 'warning' })
  }
  logDiagnostic('chatRecipientQueueHeldNoSession', { agentName: addressed.agentName })
  return true
}
