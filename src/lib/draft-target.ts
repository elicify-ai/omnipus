// #1428 step 2: a composer draft keeps the chat it was written in, so Send cannot deliver it to a
// different chat when the attached chat changed in between (for example a helper opened from the
// Activity panel while the draft was still in the box).
//
// The owner travels with the draft itself: when a draft begins (text, files or a staged library file)
// the owning chat is written into the composer quote slot. assistant-ui snapshots that slot at send
// start, per submission, and hands it back as message.metadata.custom.quote, so every way of sending
// (Enter, the Send button, mid-stream, slash commands, deferred sends) carries its own owner and nothing
// is matched by text. The quote slot has no other use in omnipus today; a quote that is not ours is
// never touched.
import { useSessionStore } from '@/store/session'
import { queueTargetFor } from '@/store/chat/queue-target'

export interface DraftQuote { text: string; messageId: string }

const OWNER_PREFIX = 'omnipus-draft-owner:'
let draftStarted = false
let draftChat: string | undefined

/** True for a quote slot value written by this module. */
export function isOwnerQuote(quote: { messageId?: string } | undefined): boolean {
  return !!quote?.messageId?.startsWith(OWNER_PREFIX)
}

/**
 * Called on every change of the composer or of the staged library files. Returns the owner quote the
 * composer should carry now (undefined: none). The chat attached when a draft begins owns it.
 */
export function noteDraftContent(hasContent: boolean): DraftQuote | undefined {
  if (!hasContent) {
    endDraft()
    return undefined
  }
  if (!draftStarted) {
    draftStarted = true
    draftChat = queueTargetFor(useSessionStore.getState().activeSessionId)
  }
  return draftChat ? { text: '', messageId: OWNER_PREFIX + draftChat } : undefined
}

/** The draft is over (nothing is left in the composer), or the composer went away. */
export function endDraft(): void {
  draftStarted = false
  draftChat = undefined
}

/**
 * The chat a submitted message was written in, from its own quote. undefined: no owner (a draft written with no
 * chat attached, or a message that never came from the composer). The runtime treats that as doubt when a chat
 * is attached now.
 */
export function draftOwnerOf(message: { metadata?: { custom?: Record<string, unknown> } }): string | undefined {
  const quote = message.metadata?.custom?.quote as { messageId?: string } | undefined
  return isOwnerQuote(quote) ? quote?.messageId?.slice(OWNER_PREFIX.length) : undefined
}

export function resetDraftTarget(): void {
  endDraft()
}
