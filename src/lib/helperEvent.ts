import { produce } from 'immer'
import type { HelperEventFrame } from '@/lib/api/generated/asyncapi-types'
import { insertHistoryMessageId } from '@/store/chat/cursor'
import { evictMessageFromBucket, MAX_MESSAGES_PER_SESSION } from '@/store/chat/messages'
import type { SessionChatState } from '@/store/chat/types'

/** Source ids, not helper generations or kinds, identify these immutable rows. */
export function buildHelperEventInsertion(
  bucket: SessionChatState,
  frame: HelperEventFrame,
  inForeground: boolean,
): Partial<SessionChatState> {
  if (bucket.messagesById[frame.message_id]) return {}
  return produce(bucket, (draft) => {
    draft.messagesById[frame.message_id] = {
      id: frame.message_id,
      session_id: frame.session_id,
      role: 'system',
      status: 'done',
      content: frame.helper_event.text,
      timestamp: frame.helper_event.created_at,
      helperEvent: frame.helper_event,
      // Replay/cold/background history must never be announced as a new
      // foreground event. The event payload itself is kept verbatim.
      announceHelperEvent: inForeground && !bucket.isReplaying && !bucket.awaitingCatchUp,
    }
    insertHistoryMessageId(draft.messageOrder, draft.messagesById, frame.message_id)
    if (draft.messageOrder.length > MAX_MESSAGES_PER_SESSION) {
      evictMessageFromBucket(draft, draft.messageOrder[0])
      draft.trimmedCount += 1
    }
  })
}
