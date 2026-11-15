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

/** The queues without the one message with this id (#1833 Discard); string items and other ids stay. */
export function withoutQueuedMessage(
  state: { outboundQueue: OutboundQueueItem[]; pendingDrainQueue: OutboundQueueItem[] },
  id: string,
): { outboundQueue: OutboundQueueItem[]; pendingDrainQueue: OutboundQueueItem[] } {
  const keep = (item: OutboundQueueItem) => typeof item === 'string' || item.id !== id
  return { outboundQueue: state.outboundQueue.filter(keep), pendingDrainQueue: state.pendingDrainQueue.filter(keep) }
}

/**
 * Stamp a newly accepted queued message with its acceptance number and the chat it was typed in.
 * With no chat yet, a pending unconfirmed first send becomes its owner instead.
 */
export function stampQueuedMessage(
  message: QueuedOutboundMessage,
  activeSessionId: string | null,
  pendingFirstSendId: string | undefined,
  seq?: number,
): QueuedOutboundMessage {
  const target = queueTargetFor(activeSessionId)
  const firstSendOwner = target ? undefined : pendingFirstSendId
  return {
    ...message,
    seq: seq ?? nextQueueSeq(),
    ...(target ? { targetSessionId: target } : {}),
    ...(firstSendOwner ? { firstSendOwner } : {}),
  }
}

/** A follow-up typed under a first send that has not been confirmed yet: it has no chat, and only that send may give it one. */
function isOwnedAndUnbound(item: OutboundQueueItem): boolean {
  return typeof item !== 'string' && !item.targetSessionId && !!item.firstSendOwner
}

/** True when the item may be sent in the chat that is open now. */
export function queuedItemBelongsHere(item: OutboundQueueItem, activeSessionId: string | null): boolean {
  // Owned follow-ups wait for their own first send to be confirmed (it binds
  // them) and are never sent into whatever chat happens to be open.
  if (isOwnedAndUnbound(item)) return false
  if (typeof item === 'string' || !item.targetSessionId) return true
  return item.targetSessionId === activeSessionId
}

/** True when the item is shown as a queued row in the chat that is open now. */
export function queuedItemShownHere(item: OutboundQueueItem, activeSessionId: string | null): boolean {
  // #1833: a follow-up whose chat was abandoned is shown in every chat until
  // the user discards it: it is never sent, and must not be lost silently.
  if (typeof item !== 'string' && item.abandoned) return true
  // The unconfirmed new chat itself still shows its own waiting follow-ups.
  if (isOwnedAndUnbound(item)) return activeSessionId === null || activeSessionId === '__pending'
  return queuedItemBelongsHere(item, activeSessionId)
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
 * #1833: the unconfirmed new chat was abandoned. Its follow-ups can never
 * get a chat: mark them held. They stay in the queue (visible, discardable),
 * and, still owned and unbound, are never sent.
 */
export function markAbandonedQueueItems(state: QueueLists, firstSendOwner: string): QueueLists {
  const mine = (item: OutboundQueueItem) =>
    typeof item !== 'string' && !item.targetSessionId && item.firstSendOwner === firstSendOwner
  const mark = (item: OutboundQueueItem): OutboundQueueItem =>
    mine(item) ? { ...(item as QueuedOutboundMessage), abandoned: true } : item
  return { outboundQueue: state.outboundQueue.map(mark), pendingDrainQueue: state.pendingDrainQueue.map(mark) }
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
      (bySeq ? (a.item.seq as number) - (b.item.seq as number) : 0)
      || a.item.timestamp.localeCompare(b.item.timestamp)
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
      typeof item !== 'string' && !shownIds.has(item.id) && queuedItemShownHere(item, activeSessionId))
    .sort((left, right) => left.timestamp.localeCompare(right.timestamp))
}

/**
 * #1428 step 2: a draft written in one chat reaches Send while another chat is attached. It is held for the
 * chat it was written in (with its attachments), never sent into the attached chat. Null when there is no
 * mismatch: no recorded chat, a chat that has no id yet, or the attached chat is the one it was written in.
 */
export function holdDraftForItsChat(
  message: QueuedOutboundMessage,
  expectedSessionId: string | undefined,
  activeSessionId: string | null,
): QueuedOutboundMessage | null {
  const target = queueTargetFor(expectedSessionId ?? null)
  if (!target || target === activeSessionId) return null
  return { ...message, seq: nextQueueSeq(), targetSessionId: target }
}
