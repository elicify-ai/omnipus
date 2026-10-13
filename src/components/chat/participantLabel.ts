// participantLabel.ts — U8 participant label logic (session-core F15/F17).
//
// Design of record: coordination/squads/session-core-build-20261008/
// ARCHITECT-ANSWER-PARTICIPANT.md §D4 (render table) + founder ruling F17
// (coordination/FOUNDER-OPEN-ITEMS.md):
//   1. The web user's own messages are right-aligned with NO label.
//   2. Every left-side message carries a label: human "Alice · Telegram"
//      (source map, unknown title-cased); agent = live agent name by
//      agent.agent_id, falling back to display_name, plus the workspace name
//      when the participant's workspace differs from the current one.
//   3. A reply shows "Ava → Alice · Telegram"; the send_message chip that
//      produced it is HIDDEN (paired via reply_to_message_id).
//   4. Two bots on one platform show the platform only (no instance suffix).
//   5. display_name is PLAIN TEXT — never markdown/HTML — ellipsized at ~40
//      characters with the full text in a tooltip.
//
// The renderer contract lives in ParticipantLabel.tsx (the components) —
// MessageItem.tsx and the virtualized rows (ChatScreen.tsx) all call THESE
// helpers so live, plain and virtualized paths cannot drift.

import type { ChatParticipant } from '@/lib/api/generated/asyncapi-types'

/** Structural slice of the catalogued Agent — accepts the generated type as-is. */
export interface ParticipantAgentRef {
  id: string
  name: string
}

/** Structural slice of the catalogued Workspace — accepts the generated type as-is. */
export interface ParticipantWorkspaceRef {
  id: string
  name: string
}

/** The message surface the reply-header helper reads (ChatMessage satisfies this). */
export interface ReplyHeaderMessage {
  agentId?: string
  replyToMessageId?: string
  replyToParticipant?: ChatParticipant
}

/**
 * D4 row 1 — the source label: map the known platforms, title-case the rest.
 * Hyphenated platforms split on word bounds ("google-chat" → "Google Chat");
 * an absent source maps to "" (the caller renders the bare display_name —
 * no guessing).
 */
export function sourceLabel(source: string | undefined): string {
  if (!source) return ''
  return source
    .split(/[\s_-]+/)
    .filter(Boolean)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ')
}

/**
 * D4 rows 1-2 — the label for one participant. Human: "display_name · Source".
 * Agent: the LIVE agent name looked up by agent.agent_id (the catalogued name
 * can be renamed since the entry was stamped), falling back to the stamped
 * display_name; " · workspace" is appended when the participant's workspace
 * differs from the current one. With no current workspace the difference
 * cannot be established — no suffix, no guessing.
 */
export function participantLabel(
  participant: ChatParticipant,
  agents: readonly ParticipantAgentRef[],
  currentWorkspaceId: string | null,
  workspaces: readonly ParticipantWorkspaceRef[],
): string {
  if (participant.kind === 'agent') {
    const live = agents.find((a) => a.id === participant.agent?.agent_id)?.name
    const name = live || participant.display_name
    const workspaceId = participant.agent?.workspace_id
    if (workspaceId && currentWorkspaceId && workspaceId !== currentWorkspaceId) {
      const workspaceName = workspaces.find((w) => w.id === workspaceId)?.name ?? workspaceId
      return `${name} · ${workspaceName}`
    }
    return name
  }
  const source = sourceLabel(participant.source)
  return source ? `${participant.display_name} · ${source}` : participant.display_name
}

/** F17 (5) — the ~40-character ellipsis cap. */
export const PARTICIPANT_LABEL_MAX_CHARS = 40

// C9 (R3 delta re-review): names are user input and can carry emoji or
// combined characters. Counting and slicing UTF-16 code units truncates
// INSIDE a surrogate pair (a broken glyph) and can strand a combining mark
// (a bare base letter), and a lone surrogate makes a blank avatar initial.
// Both helpers therefore work on GRAPHEMES: Intl.Segmenter where the runtime
// has it (ES2022 Intl — the app's compiled target), a code-point spread as
// the fallback elsewhere.
const graphemeSegmenter: Intl.Segmenter | undefined =
  typeof Intl !== 'undefined' && 'Segmenter' in Intl
    ? new Intl.Segmenter(undefined, { granularity: 'grapheme' })
    : undefined

/** The label's graphemes: segmenter clusters, or code points as the fallback. */
function graphemesOf(text: string): string[] {
  if (graphemeSegmenter) return Array.from(graphemeSegmenter.segment(text), (s) => s.segment)
  return [...text]
}

/** One truncated label: the visible text, the full text (tooltip), the flag. */
export interface TruncatedLabel {
  text: string
  full: string
  truncated: boolean
}

/**
 * F17 (5) — ellipsize at the cap; the FULL text always rides along for the
 * tooltip. The cap counts GRAPHEMES (an emoji is one character), and the
 * cut lands on a grapheme boundary. Plain text in, plain text out — never
 * parsed as markup.
 */
export function truncateParticipantLabel(label: string): TruncatedLabel {
  // Cheap UTF-16 pre-check: a string's code-unit length is an UPPER bound on
  // its grapheme count, so a label at or under the cap in units can never
  // exceed it in graphemes — skip segmentation on the common short path.
  if (label.length <= PARTICIPANT_LABEL_MAX_CHARS) {
    return { text: label, full: label, truncated: false }
  }
  const units = graphemesOf(label)
  if (units.length <= PARTICIPANT_LABEL_MAX_CHARS) {
    return { text: label, full: label, truncated: false }
  }
  return {
    text: units.slice(0, PARTICIPANT_LABEL_MAX_CHARS - 1).join('') + '…',
    full: label,
    truncated: true,
  }
}

/**
 * C9 — the avatar's fallback initial: the FIRST GRAPHEME of the display
 * name, upper-cased. An emoji or combined first character comes out whole —
 * never a lone surrogate (blank glyph) or a bare base letter.
 */
export function participantInitial(displayName: string): string {
  if (displayName.length === 0) return ''
  return graphemesOf(displayName)[0]?.toUpperCase() ?? ''
}

/** One reply header: the author part, the recipient part, its truncation. */
export interface ReplyHeader {
  author: string
  target: string
  targetFull: string
  targetTruncated: boolean
}

/**
 * D4 rows 5-6 — the "author → recipient" header of a reply bubble. Null when
 * the message carries no reply fields (a plain assistant bubble keeps today's
 * plain author label). With reply_to_message_id but reply_to_participant
 * absent, the target degrades to the word "reply" — never a guessed name.
 */
export function replyHeaderParts(
  message: ReplyHeaderMessage,
  agents: readonly ParticipantAgentRef[],
  currentWorkspaceId: string | null,
  workspaces: readonly ParticipantWorkspaceRef[],
): ReplyHeader | null {
  if (!message.replyToMessageId && !message.replyToParticipant) return null
  const author = agents.find((a) => a.id === message.agentId)?.name ?? message.agentId ?? ''
  const rawTarget = message.replyToParticipant
    ? participantLabel(message.replyToParticipant, agents, currentWorkspaceId, workspaces)
    : 'reply'
  const { text, full, truncated } = truncateParticipantLabel(rawTarget)
  return { author, target: text, targetFull: full, targetTruncated: truncated }
}

/**
 * F17 (1) — the reply_to ids that have a reply bubble in the thread. A
 * send_message call whose `reply_to` argument names one of these ids has its
 * reply bubble rendering right beside it; the chip itself is hidden.
 */
export function collectPairedReplyToIds(messages: readonly { replyToMessageId?: string }[]): Set<string> {
  const ids = new Set<string>()
  for (const message of messages) {
    if (message.replyToMessageId) ids.add(message.replyToMessageId)
  }
  return ids
}

/**
 * F17 (1) — whether ONE tool call is a send_message chip paired with a reply
 * bubble (its `reply_to` argument matches the bubble's reply_to_message_id).
 * Deliberately exact-match on the tool name and a real string argument — a
 * non-string reply_to or any other tool is never hidden by this rule.
 */
export function isSendChipHiddenByReply(
  tool: string,
  params: Record<string, unknown> | undefined,
  pairedReplyToIds: ReadonlySet<string>,
): boolean {
  if (tool !== 'send_message') return false
  const replyTo = params?.['reply_to']
  return typeof replyTo === 'string' && pairedReplyToIds.has(replyTo)
}
