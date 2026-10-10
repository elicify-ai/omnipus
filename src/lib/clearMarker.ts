// clearMarker.ts — the identity of the /clear marker row and the /clear
// command text (FR-030/031, U10b).
//
// On a successful /clear the SERVER appends exactly ONE chat-view marker
// entry to the session archive — type system, role system, view_membership
// chat, content clearMarkerText — and nothing else about the view changes
// (pkg/agent/clear_session.go::clearConversation, core git 67345b1d7). The
// chat view renders that one row as a quiet divider and keeps every OTHER
// system row in its ordinary treatment; a refusal is a plain reply and no
// marker exists for it.
//
// The REST transcript projection (rawToMessage) carries the marker's role and
// content through verbatim, so role + exact content is the discriminator the
// view can rely on; a user or assistant row that merely contains the sentence
// never matches, because the role check is part of the predicate.

/** The exact marker content the server writes on a successful /clear. */
export const CLEAR_MARKER_TEXT = 'Conversation context cleared'

/** True for the one system entry a successful /clear appends to the chat view. */
export function isClearContextMarker(message: { role?: string; content?: string | null }): boolean {
  return message.role === 'system' && message.content === CLEAR_MARKER_TEXT
}

/**
 * True when an outgoing composer text is the /clear server command (any
 * case, surrounding whitespace ignored) — the text is sent verbatim and the
 * server executes it (DeliveryAgent), so the sender only needs to recognize
 * it to arm the post-turn transcript re-read.
 */
export function isClearCommandText(content: string): boolean {
  return content.trim().toLowerCase() === '/clear'
}
