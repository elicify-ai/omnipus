// clear-refetch.ts — FR-030/031 (U10b) SPA half, transcript re-read after a
// /clear (R3).
//
// A successful /clear moves the session's context/display window server-side
// and appends ONE chat-view marker entry ("Conversation context cleared",
// type system, role system — pkg/agent/clear_session.go, core git 67345b1d7).
// Nothing pushes that marker live, so the only way the view can reflect the
// server is to RE-READ the transcript query. This module owns that re-read:
// invalidate the session's ['messages'] query, and once the fresh projection
// has landed, replace the bucket's messages with it. Nothing here clears
// anything locally — a refusal re-read shows the unchanged transcript, and an
// empty projection renders whatever the server actually returned.
//
// Kept out of frames.ts's grandfathered handleFrame: the done-case call site
// is one line; this module is new code under the normal function budget.

import { queryClient } from '@/lib/queryClient'
import type { Message } from '@/lib/api'
import type { ChatMessage, SessionChatState } from './types'

type WithBucket = (
  sid: string | null,
  updater: (bucket: SessionChatState) => Partial<SessionChatState>,
) => void

/**
 * Re-reads the session's transcript after a /clear and applies the fresh
 * server projection to the bucket. Idle buckets only: a bucket mid-turn or
 * mid-replay keeps its live state — the re-read itself still happened, and a
 * replay of the same archive delivers the marker its own way.
 */
export function refreshTranscriptAfterClear(sid: string, withBucket: WithBucket): void {
  void queryClient
    .invalidateQueries({ queryKey: ['messages', sid] })
    .then(() => {
      const fresh = queryClient.getQueryData<Message[]>(['messages', sid])
      if (!fresh) return
      withBucket(sid, (b) => {
        if (b.isStreaming || b.isReplaying) return {}
        const messagesById: Record<string, ChatMessage> = {}
        const messageOrder: string[] = []
        // Same admission rule as ChatScreen's REST-fallback path: only
        // role-bearing entries are view messages (roleless tool_call rows
        // are not renderable transcript rows).
        for (const m of fresh) {
          if (m.role !== 'user' && m.role !== 'assistant' && m.role !== 'system') continue
          messagesById[m.id] = m as ChatMessage
          messageOrder.push(m.id)
        }
        return { messagesById, messageOrder }
      })
    })
}
