// #1428 step 2: remember which chat a composer draft was written in, so Send cannot deliver it to a
// different chat when the attached chat changed in between (for example a helper opened from the
// Activity panel while the draft was still in the box). Module scope: the normal chat has one composer.
import { useSessionStore } from '@/store/session'
import { queueTargetFor } from '@/store/chat/queue-target'

interface Submitted { text: string; chat: string | undefined; at: number }

const HOLD_MS = 5 * 60 * 1000
let draftStarted = false
let draftChat: string | undefined
const submitted: Submitted[] = []

/** Called on every change of the composer text: the chat that is attached when a draft begins owns it. */
export function noteDraftText(text: string): void {
  if (!text.trim()) {
    draftStarted = false
    draftChat = undefined
    return
  }
  if (draftStarted) return
  draftStarted = true
  draftChat = queueTargetFor(useSessionStore.getState().activeSessionId)
}

/** Called at every submit entry point, before the runtime empties the composer and starts its async hand-off. */
export function markDraftSubmit(text: string): void {
  const chat = draftStarted ? draftChat : queueTargetFor(useSessionStore.getState().activeSessionId)
  const now = Date.now()
  for (let i = submitted.length - 1; i >= 0; i--) if (now - submitted[i].at > HOLD_MS) submitted.splice(i, 1)
  submitted.push({ text, chat, at: now })
}

/** Called by the runtime's onNew: the chat the first matching marked submit was written in, if any. */
export function takeDraftTarget(text: string): string | undefined {
  const index = submitted.findIndex((entry) => entry.text === text)
  if (index < 0) return undefined
  return submitted.splice(index, 1)[0].chat
}

export function resetDraftTarget(): void {
  draftStarted = false
  draftChat = undefined
  submitted.length = 0
}
