import type { OutboundQueueItem } from './types'

/**
 * #1428 step 1: a queued message goes only to the chat it was written in.
 *
 * The chat a message was typed in is recorded when it is queued, but only for
 * a real chat id. A first message of a brand new chat has no id yet (null or
 * the '__pending' placeholder); it keeps the existing rule and goes to the
 * chat the server creates for it.
 */
export function queueTargetFor(activeSessionId: string | null): string | undefined {
  return activeSessionId && activeSessionId !== '__pending' ? activeSessionId : undefined
}

/** True when the item may be shown or sent in the chat that is open now. */
export function queuedItemBelongsHere(item: OutboundQueueItem, activeSessionId: string | null): boolean {
  if (typeof item === 'string' || !item.targetSessionId) return true
  return item.targetSessionId === activeSessionId
}

/**
 * Index of the first item that may be sent now, or -1. An item waiting for a
 * chat that is not open is skipped, so it never blocks messages of the open
 * chat, and order inside each chat stays first in, first out.
 */
export function firstDrainableIndex(queue: readonly OutboundQueueItem[], activeSessionId: string | null): number {
  return queue.findIndex((item) => queuedItemBelongsHere(item, activeSessionId))
}
