import type { AskUserAnswerFrame } from '@/lib/api/generated/asyncapi-types'

// UI-only routing, not a new wire shape. Both identifiers come from the
// generated answer/card types; no foreground-session fallback is permitted.
type CardIdentity = Pick<AskUserAnswerFrame, 'session_id' | 'card_id'>
type RejectionListener = (message: string) => void
const listeners = new Map<string, Set<RejectionListener>>()

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

/** Notify only this mounted pending card; terminal/unmounted cards have no listener. */
export function reportAskUserAnswerRejected(card: CardIdentity, message: string): void {
  // TODO(wire-1343): route the generated ErrorFrame.ask_user_card_id plus
  // required session_id here, before generic turn-ending error handling.
  // The authoritative pending/terminal card, not the message text, decides
  // whether retry is allowed: an accepted-but-resume-failed card stays terminal.
  for (const listener of listeners.get(askUserCardKey(card)) ?? []) listener(message)
}
