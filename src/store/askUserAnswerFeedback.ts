import type { AskUserAnswerFrame } from '@/lib/api/generated/asyncapi-types'

// UI-only routing, not a new wire shape. Both identifiers come from the
// generated answer/card types; no foreground-session fallback is permitted.
type CardIdentity = Pick<AskUserAnswerFrame, 'session_id' | 'card_id'>
type RejectionListener = (message: string) => void
const listeners = new Map<string, Set<RejectionListener>>()
const pendingReconciliationListeners = new Map<string, Set<() => void>>()

export function askUserCardKey(card: CardIdentity): string {
  return JSON.stringify([card.session_id, card.card_id])
}

export function subscribeAskUserAnswerRejection(card: CardIdentity, listener: RejectionListener): () => void {
  const key = askUserCardKey(card)
  const subscribers = listeners.get(key) ?? new Set<RejectionListener>()
  subscribers.add(listener)
  listeners.set(key, subscribers)
  return () => {
    subscribers.delete(listener)
    if (subscribers.size === 0) listeners.delete(key)
  }
}

/** True only when a mounted pending card received the rejection inline. */
export function reportAskUserAnswerRejected(card: CardIdentity, message: string): boolean {
  const subscribers = listeners.get(askUserCardKey(card))
  if (!subscribers) return false
  for (const listener of subscribers) listener(message)
  return true
}

export function subscribeAskUserPendingReconciliation(card: CardIdentity, listener: () => void): () => void {
  const key = askUserCardKey(card)
  const subscribers = pendingReconciliationListeners.get(key) ?? new Set<() => void>()
  subscribers.add(listener)
  pendingReconciliationListeners.set(key, subscribers)
  return () => {
    subscribers.delete(listener)
    if (subscribers.size === 0) pendingReconciliationListeners.delete(key)
  }
}

/** Emitted only after a session snapshot restores this exact card as pending. */
export function reportAskUserPendingReconciled(card: CardIdentity): void {
  for (const listener of pendingReconciliationListeners.get(askUserCardKey(card)) ?? []) listener()
}
