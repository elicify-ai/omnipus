// #1428 step 2: remember which chat a composer draft was written in, so Send cannot deliver it to a
// different chat when the attached chat changed in between (for example a helper opened from the
// Activity panel while the draft was still in the box). Module scope: the normal chat has one composer.
//
// A submit is matched to its owner by what the composer snapshots at send time: the text plus the ids of
// the attached files. Marks of the same key are taken in the order they were made (a text-only send
// reaches onNew in submit order); a send with files can finish later, but its key differs.
import { useSessionStore } from '@/store/session'
import { queueTargetFor } from '@/store/chat/queue-target'

interface Submitted { key: string; chat: string | undefined }

/** Marks that were never taken (a failed hand-off) are dropped oldest first. */
const MAX_MARKS = 50
let draftStarted = false
let draftChat: string | undefined
const submitted: Submitted[] = []

function submitKey(text: string, attachmentIds: readonly string[]): string {
  return JSON.stringify([text, attachmentIds])
}

/** Called on every change of the composer: the chat attached when a draft begins owns it. A draft is text or files. */
export function noteDraftContent(hasContent: boolean): void {
  if (!hasContent) {
    endDraft()
    return
  }
  if (draftStarted) return
  draftStarted = true
  draftChat = queueTargetFor(useSessionStore.getState().activeSessionId)
}

/** The draft is over (the composer is empty, or the composer went away). Marks already made stay. */
export function endDraft(): void {
  draftStarted = false
  draftChat = undefined
}

/**
 * Called at every submit entry point, before the runtime empties the composer and starts its async
 * hand-off. A submit the composer will refuse (nothing to send) leaves no mark behind.
 */
export function markComposerSubmit(state: { text?: string; attachments?: readonly { id: string }[] }): void {
  const text = state.text ?? ''
  const ids = (state.attachments ?? []).map((a) => a.id)
  if (!text.trim() && ids.length === 0) return
  const chat = draftStarted ? draftChat : queueTargetFor(useSessionStore.getState().activeSessionId)
  submitted.push({ key: submitKey(text, ids), chat })
  if (submitted.length > MAX_MARKS) submitted.shift()
}

/** Called first thing in the runtime's onNew, whatever happens next: the chat this submit was written in, if marked. */
export function takeDraftTarget(text: string, attachmentIds: readonly string[]): string | undefined {
  const key = submitKey(text, attachmentIds)
  const index = submitted.findIndex((entry) => entry.key === key)
  if (index < 0) return undefined
  return submitted.splice(index, 1)[0].chat
}

export function resetDraftTarget(): void {
  endDraft()
  submitted.length = 0
}
