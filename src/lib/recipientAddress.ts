// recipientAddress — send-time @-recipient derivation (founder decision
// 2026-10-11 Q1 B, reversing spec FR-007's "@ unavailable until group
// messaging lands" for addressing only).
//
// The restored @ agent menu (src/hooks/useSlashMenu.ts) inserts the exact
// text `@<Agent name> ` into the composer when the user picks an agent; it
// does NOT switch the active agent or chat (FR-029 — no in-chat handover).
// The selected agent becomes the message's recipient by way of this prefix:
// ON SEND, the leading `@<Name>` of the composer text is matched against the
// same candidate list the menu offered, and a match puts a
// `recipient {workspace_id, agent_id}` pair on the WS MessageFrame — TOGETHER
// WITH the current chat's session_id (contracts/asyncapi.yaml:
// "valid only together with session_id"; the server refuses it otherwise —
// pkg/gateway/ws_recipient.go::refuseRecipient).
//
// The derivation is deliberately NOT sticky state: it re-runs from the text
// at every send, so editing or deleting the prefix before sending simply
// sends no recipient — the @-text itself still travels verbatim inside the
// content.
//
// Pure module: every input is passed in (text, agent list, active ids); the
// store-side wiring that reads the caches/stores lives in
// src/store/chat/recipientSend.ts.

import type { Agent } from '@/lib/api'
import { isWorker } from '@/lib/api'
import { isChatEligibleAgent } from './agentKind'

/** The generated MessageFrame.recipient pair (never hand-written — a narrow pick from the generated type). */
export type OutboundRecipient = NonNullable<import('@/lib/api/generated/asyncapi-types').MessageFrame['recipient']>

/** Minimal shape the parse needs; a full Agent satisfies it. */
export interface NamedCandidate {
  id: string
  name: string
}

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/**
 * Match the leading `@<Name>` of `content` against `candidates`, EXACTLY.
 *
 * Rules (pinned by src/lib/recipientAddress.test.ts):
 *   - the content must START with "@" (a mid-text "@x" is ordinary text);
 *   - the name after "@" must match a candidate name EXACTLY — case
 *     included. The menu inserts the exact display name, and any edit to it
 *     (including a case change) is an edit: the recipient is not sticky;
 *   - the name must end at a word boundary — whitespace or end of content —
 *     so "@Miax" never matches "Mia", while "@Mia\nrest" (a Shift+Enter
 *     multiline draft) still does;
 *   - when one candidate's name is a prefix of another's ("Mia" / "Mia
 *     Smith"), the LONGEST match wins, so both address forms stay
 *     unambiguous.
 *
 * Returns the matched candidate, or null when nothing matches exactly.
 */
export function parseAddressedAgent(content: string, candidates: readonly NamedCandidate[]): NamedCandidate | null {
  if (!content.startsWith('@')) return null
  // Longest name first so the "Mia Smith" vs "Mia" pair resolves to the
  // more specific match.
  const ordered = [...candidates].sort((a, b) => b.name.length - a.name.length)
  for (const candidate of ordered) {
    if (candidate.name.length === 0) continue
    const pattern = new RegExp(`^@${escapeRegExp(candidate.name)}(?:$|\\s)`)
    if (pattern.test(content)) return candidate
  }
  return null
}

/**
 * Full send-side resolution: the exact-name parse of `content` against the
 * picker-eligible agents, narrowed by the addressing rules, mapped to the
 * generated wire pair.
 *
 * Eligibility mirrors the mention menu's own list (the same predicates
 * useChatAgents applies), minus the team scoping — team membership is a
 * picker-display concern, while the server runs the authoritative admission
 * rule for whatever pair arrives (eligible main now, not the chat's own
 * pair, the receiver's send_message policy). Local rules that are NOT the
 * server's to decide:
 *   - the agent that owns the current chat (`activeAgentId`) is excluded —
 *     addressing yourself is meaningless, and the server refuses it;
 *   - workers (Subagent / subagent_3p / legacy worker) are excluded — they
 *     are never addressable (founder F17 (5): delegated workers may only
 *     message their parent);
 *   - a null `activeWorkspaceId` resolves to no recipient (the pair needs a
 *     workspace).
 */
export function resolveSendRecipient(params: {
  content: string
  agents: readonly Agent[]
  activeAgentId: string | null
  activeWorkspaceId: string | null
}): OutboundRecipient | null {
  if (!params.content.startsWith('@')) return null
  if (!params.activeWorkspaceId) return null
  const candidates = params.agents.filter(
    (agent) =>
      agent.id !== params.activeAgentId
      && !isWorker(agent)
      && isChatEligibleAgent(agent),
  )
  const hit = parseAddressedAgent(params.content, candidates)
  if (!hit) return null
  return { workspace_id: params.activeWorkspaceId, agent_id: hit.id }
}
