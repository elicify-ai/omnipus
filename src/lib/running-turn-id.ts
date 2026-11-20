// #1428 step 2 (spec C r9 4.7, record `turnId`; UAT-15): the turn a typed
// command or Stop is meant to stop. `activeTurnId` is set only by a
// session_state that announces the turn (the attach snapshot). A turn that
// started after the attach names itself only in its token frames, which the
// reducer stamps on the streaming answer (`ChatMessage.turnId`). Without that
// fallback the record captured `null`, and after a reconnect an applied
// redirect's NEW turn matched "null = still running" and showed the notice.

import { getMessages } from '@/store/chat/messages'
import type { SessionChatState } from '@/store/chat/types'

/** The bucket's running turn: the announced one, else the streaming answer's own turn id. */
export function runningTurnId(bucket: SessionChatState | undefined): string | null {
  if (!bucket) return null
  if (bucket.activeTurnId) return bucket.activeTurnId
  if (!bucket.isStreaming) return null
  const messages = getMessages(bucket)
  for (let i = messages.length - 1; i >= 0; i--) {
    const message = messages[i]
    if (message.role === 'assistant' && message.isStreaming && message.turnId) return message.turnId
  }
  return null
}
