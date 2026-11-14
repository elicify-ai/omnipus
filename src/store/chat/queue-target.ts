import type { OutboundQueueItem, QueuedOutboundMessage } from './types'

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

type QueueLists = { outboundQueue: OutboundQueueItem[]; pendingDrainQueue: OutboundQueueItem[] }

/**
 * A follow-up typed while a new chat's first message is still unconfirmed has
 * no chat id yet. When that chat gets its id, those items belong to it: bind
 * them, so opening another chat afterwards cannot send them there.
 */
export function bindUnboundQueueItems(
  state: QueueLists,
  sessionId: string,
  firstSendOwner?: string,
): Partial<QueueLists> {
  // A confirmed first send adopts only the follow-ups typed under it. An
  // acknowledgement that belongs to nobody (the legacy path) adopts only
  // items no first send owns, so a foreign acknowledgement cannot take them.
  const unbound = (item: OutboundQueueItem) =>
    typeof item !== 'string' && !item.targetSessionId && item.firstSendOwner === firstSendOwner
  if (!state.outboundQueue.some(unbound) && !state.pendingDrainQueue.some(unbound)) return {}
  const bind = (item: OutboundQueueItem): OutboundQueueItem =>
    unbound(item) ? { ...(item as QueuedOutboundMessage), targetSessionId: sessionId } : item
  return { outboundQueue: state.outboundQueue.map(bind), pendingDrainQueue: state.pendingDrainQueue.map(bind) }
}

/**
 * Order of the next drain batch: items bounced back from the outbound queue,
 * plus items still held for another chat. The acceptance number decides, so a
 * newer message never overtakes an older held one of the same chat, even when
 * the clock gives equal or backward times. Without numbers the queued time
 * decides. A plain legacy string has no number or time; any string keeps the
 * older rule (bounced items first).
 */
export function mergeDrainBatch(
  bounced: readonly OutboundQueueItem[],
  held: readonly OutboundQueueItem[],
): OutboundQueueItem[] {
  const all = [...bounced, ...held]
  if (all.some((item) => typeof item === 'string')) return all
  const items = all as QueuedOutboundMessage[]
  // The acceptance number decides; it does not depend on the clock. Items
  // without one (state from before this field) fall back to the queued time.
  const bySeq = items.every((item) => item.seq !== undefined)
  return items
    .map((item, index) => ({ item, index }))
    .sort((a, b) =>
      (bySeq ? (a.item.seq as number) - (b.item.seq as number) : a.item.timestamp.localeCompare(b.item.timestamp))
      || a.index - b.index)
    .map(({ item }) => item)
}

let lastQueueSeq = 0
/** Next acceptance number: strictly increasing, and never behind the clock after a reload. */
export function nextQueueSeq(): number {
  lastQueueSeq = Math.max(lastQueueSeq + 1, Date.now())
  return lastQueueSeq
}

/** The queued rows the open chat shows: only its own, none already shown as a message. */
export function selectQueuedRows(
  held: readonly OutboundQueueItem[],
  outbound: readonly OutboundQueueItem[],
  shownIds: ReadonlySet<string>,
  activeSessionId: string | null,
): QueuedOutboundMessage[] {
  return [...held, ...outbound]
    .filter((item): item is QueuedOutboundMessage =>
      typeof item !== 'string' && !shownIds.has(item.id) && queuedItemBelongsHere(item, activeSessionId))
    .sort((left, right) => left.timestamp.localeCompare(right.timestamp))
}
