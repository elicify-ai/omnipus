import type { StoreApi } from 'zustand'
import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { sanitizeLegacyErrorMessage } from '@/lib/llm-error'
import { reportAskUserAnswerRejected } from '@/store/askUserAnswerFeedback'
import { useConnectionStore } from '@/store/connection'
import type { ChatStore, SessionChatState } from '../types'
import { appendErrorNotice } from './error-notice-frames'

type WithBucket = (sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void

/** Decision A (#1343): an action error is card-scoped, never a turn ending. */
export function handleAskUserCardErrorFrame(
  frame: ServerFrame,
  get: StoreApi<ChatStore>['getState'],
  withBucket: WithBucket,
): boolean {
  if (frame.type !== 'error' || frame.ask_user_card_id === undefined) return false
  const message = sanitizeLegacyErrorMessage(frame.message)
  // The wire validator requires session_id when a card id is present, but
  // an empty/unknown session must still never fall back to the foreground.
  const bucket = frame.session_id ? get().sessionsById[frame.session_id] : undefined
  if (!bucket) {
    useConnectionStore.getState().setConnectionError(message)
    return true
  }
  const card = bucket.pendingAsk
  if (card?.status === 'pending' && card.session_id === frame.session_id && card.card_id === frame.ask_user_card_id) {
    if (reportAskUserAnswerRejected(card, message)) return true
  }
  // Includes background cards (no mounted listener), stale/promoted cards and
  // accepted-but-resume-failed terminal cards. The notification never reopens
  // a card, replaces its draft or changes any running/replaying turn state.
  withBucket(frame.session_id ?? null, (current) => appendErrorNotice(current, message, undefined, undefined, undefined))
  return true
}
