// roomModel.ts — pure helpers shared by the group-chat surfaces
// (omnipus-system-agent FR-162, FR-171, FR-201, FR-205, FR-207; §6.11, §6.14).

import type { Agent, Workspace } from '@/lib/api'
import type { RoomDeliveryState } from '@/lib/api/generated/asyncapi-types'
import { isConversationAgent } from '@/lib/omnipus'
import { mainSessionIdOfMember } from '@/lib/nav/sessionCoreSeam'

/**
 * The workspace's conversation agents — the only agents a room can contain
 * (FR-162, FR-207): never workers, hidden engine agents or Omnipus. Membership
 * is `core_team` (the authoritative team list, workspaceRoster.ts); a
 * `member_configs` key is a member too (the server prunes configs to the team
 * on every update, pkg/gateway/rest_workspaces.go — FR-022 GC). Agents not in
 * the loaded roster cannot be classified and are left out.
 */
export function workspaceConversationAgents(workspace: Workspace | undefined, agents: readonly Agent[]): Agent[] {
  if (!workspace) return []
  const ids = [...new Set([...(workspace.core_team ?? []), ...Object.keys(workspace.member_configs ?? {})])]
  const byId = new Map(agents.map((agent) => [agent.id, agent]))
  return ids.flatMap((id) => {
    const agent = byId.get(id)
    return agent && isConversationAgent(agent) ? [agent] : []
  })
}

/** The member's validated main chat in this workspace, or undefined (never a guessed id). */
export function memberMainSessionId(workspace: Workspace | undefined, agentId: string): string | undefined {
  return mainSessionIdOfMember(workspace?.member_configs?.[agentId])
}

/** §6.14: micro-dollars shown rounded to the cent. */
export function formatAllocatedCost(microUsd: number): string {
  return `$${(microUsd / 1_000_000).toFixed(2)}`
}

/** "rate_limit" → "rate limit": a server reason code read as words. */
function readableReason(reason: string): string {
  return reason.replace(/_/g, ' ')
}

/**
 * §6.11's room label for a K27 request state. `answered` is represented by
 * the answer entry itself; its header label is plain "answered".
 */
export function deliveryLabel(delivery: RoomDeliveryState, agentName: string): string {
  switch (delivery.state) {
    case 'recorded': return 'sending…'
    case 'awaiting_approval': return `awaiting your approval — in ${agentName}'s main chat`
    case 'admitted': return 'queued'
    case 'held': return `held — ${agentName} is stopped`
    case 'working': return 'working'
    case 'waiting_on_user': return `waiting for you in ${agentName}'s main chat`
    case 'answered': return 'answered'
    case 'refused': return 'refused'
    case 'failed': return delivery.reason ? `failed: ${readableReason(delivery.reason)}` : 'failed'
    case 'interrupted': return 'interrupted'
    case 'unavailable': return delivery.reason ? `unavailable: ${readableReason(delivery.reason)}` : 'unavailable'
    case 'cancelled': return delivery.reason === 'removed' ? 'cancelled — removed' : 'cancelled'
    case 'unknown': return 'unknown'
  }
}

/** States whose label points at the member's main chat (§6.11 "(link)"). */
export function deliveryNeedsMainChatLink(delivery: RoomDeliveryState): boolean {
  return delivery.state === 'awaiting_approval' || delivery.state === 'waiting_on_user'
}

/** Ask again (#1449 FR-036): the Participants panel asks the open group's box to address a member. */
export const ROOM_ASK_AGAIN_EVENT = 'omnipus:room-ask-again'
export type RoomAskAgainDetail = { roomId: string; agentId: string } // not-wire-format: in-page event payload

/** The six commands a room accepts (FR-205). */
const ROOM_COMMANDS = new Set(['/pause', '/resume', '/mute', '/unmute', '/members', '/help'])

/** True for a slash command a room does not accept (FR-205). */
export function isRefusedRoomCommand(text: string): boolean {
  const command = text.trim().split(/\s+/)[0] ?? ''
  return command.startsWith('/') && !ROOM_COMMANDS.has(command.toLowerCase())
}

/**
 * FR-205: a refused command links to "the main chat of each member (or of the
 * @-named agent)". A member is @-named when the text holds "@<full name>"
 * (case-insensitive, §6.13's exact-name rule) followed by a word boundary.
 */
export function refusalLinkMembers(text: string, members: readonly Agent[]): Agent[] {
  const lower = text.toLowerCase()
  const named = members.filter((member) => {
    const token = `@${member.name.toLowerCase()}`
    const at = lower.indexOf(token)
    if (at < 0) return false
    const next = lower.charAt(at + token.length)
    return next === '' || !/[\p{L}\p{N}_]/u.test(next)
  })
  return named.length > 0 ? named : [...members]
}
