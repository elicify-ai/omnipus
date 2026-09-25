/**
 * Where a delegation line sits in the thread.
 * After the message named by anchorMessageId, earliest first.
 * No anchor, or an anchor that is not a message on screen, goes at the end.
 */
import type { DelegationEvent } from './delegationEvents.types'

function byTime(a: DelegationEvent, b: DelegationEvent): number {
  // `at` is anchor-message time plus sequence, not wall-clock event time — order only, never render it.
  if (a.at !== b.at) return a.at - b.at
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
}

export function delegationEventsAfterMessage(
  events: readonly DelegationEvent[],
  messageId: string,
): DelegationEvent[] {
  return events.filter((event) => event.anchorMessageId === messageId).sort(byTime)
}

export function delegationEventsAtEnd(
  events: readonly DelegationEvent[],
  messageIds: ReadonlySet<string>,
): DelegationEvent[] {
  return events
    .filter((event) => !event.anchorMessageId || !messageIds.has(event.anchorMessageId))
    .sort(byTime)
}
