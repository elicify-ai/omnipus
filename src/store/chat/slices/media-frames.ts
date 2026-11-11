// media-frames.ts — the `media` frame reducer: an agent sent files (send_file,
// screenshots) for the open assistant bubble. Moved out of
// replay-and-status-frames.ts::handleReplayAndStatusFrame (a pinned function
// budget entry, scripts/budgets/functions.txt) by chat-links U8, unchanged
// except for carrying the part's optional `library_ref` onto the attachment.

import { produce } from 'immer'
import { generateId } from '@/lib/constants'
import { logDiagnostic } from '@/lib/telemetry'
import { findOpenAssistantMessageId } from '../messages'
import type { ChatMessage, ChatStore, MediaAttachment, SessionChatState } from '../types'

type MediaFrame = Extract<Parameters<ChatStore['handleFrame']>[0], { type: 'media' }>
type WithBucket = (sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void

/** Attach a `media` frame's parts to the session's open assistant bubble (or a new one). */
export function applyMediaFrame(frame: MediaFrame, targetSid: string | null, withBucket: WithBucket): void {
  if (!targetSid) return
  if (!Array.isArray(frame.parts) || frame.parts.length === 0) {
    console.warn('[chat] Received media frame with empty or invalid parts — appending notice')
    logDiagnostic('chatMediaFrameInvalidParts', { sessionId: targetSid })
    withBucket(targetSid, (b) => {
      return produce(b, (draft) => {
        // ADR-070 §2.1/F2 (code review): a bare scan would append
        // this notice to a closed, closedBySteer bubble if this
        // frame is the first to arrive after a mid-turn steer.
        // Mint a fresh bubble when there is no eligible open one —
        // matching the real-attachment branch below — rather than
        // silently dropping a failure notice the user should see.
        const lastMsgId = findOpenAssistantMessageId(draft.messageOrder, draft.messagesById)
        if (lastMsgId) {
          const msg = draft.messagesById[lastMsgId]
          msg.content = (msg.content ?? '') + (msg.content ? '\n\n' : '') + '_1 attachment could not be displayed._'
        } else {
          const newMsg: ChatMessage = {
            id: generateId(),
            role: 'assistant',
            content: '_1 attachment could not be displayed._',
            timestamp: new Date().toISOString(),
          }
          draft.messagesById[newMsg.id] = newMsg
          draft.messageOrder.push(newMsg.id)
        }
      }) as Partial<SessionChatState>
    })
    return
  }
  const attachments: MediaAttachment[] = frame.parts
    .filter((p) => p.url && p.type)
    .map((p) => ({
      type: p.type,
      url: p.url,
      filename: p.filename,
      contentType: p.content_type,
      caption: p.caption,
      // chat-links FR-021/FR-022: where the sent file lives in the Library.
      // Kept on every media type; only the file card shows the action (A7).
      ...(p.library_ref ? { libraryRef: p.library_ref } : {}),
    }))
  if (attachments.length === 0) {
    withBucket(targetSid, (b) => {
      return produce(b, (draft) => {
        // ADR-070 §2.1/F2 — same reasoning as the invalid-parts
        // branch above.
        const lastMsgId = findOpenAssistantMessageId(draft.messageOrder, draft.messagesById)
        const notice = `_${frame.parts.length} attachment${frame.parts.length > 1 ? 's' : ''} could not be displayed._`
        if (lastMsgId) {
          const msg = draft.messagesById[lastMsgId]
          msg.content = (msg.content ?? '') + (msg.content ? '\n\n' : '') + notice
        } else {
          const newMsg: ChatMessage = {
            id: generateId(),
            role: 'assistant',
            content: notice,
            timestamp: new Date().toISOString(),
          }
          draft.messagesById[newMsg.id] = newMsg
          draft.messageOrder.push(newMsg.id)
        }
      }) as Partial<SessionChatState>
    })
    return
  }
  withBucket(targetSid, (b) => {
    return produce(b, (draft) => {
      // ADR-070 §2.1/F2: resolve the candidate through the
      // eligibility helper so a closed, closedBySteer bubble can
      // never be considered — the downstream `canAttach` refinement
      // (isStreaming OR empty-content) is unrelated to steering (it
      // exists for a freshly-created, not-yet-streaming placeholder)
      // and is kept as-is, now only ever evaluated against a
      // candidate that already passed the "still open" gate.
      const lastMsgId = findOpenAssistantMessageId(draft.messageOrder, draft.messagesById)
      const dedupe = (existing: MediaAttachment[] | undefined, incoming: MediaAttachment[]) => {
        const seen = new Set((existing ?? []).map((a) => a.url))
        const fresh = incoming.filter((a) => !seen.has(a.url))
        return [...(existing ?? []), ...fresh]
      }
      if (lastMsgId) {
        const msg = draft.messagesById[lastMsgId]
        const canAttach = msg.isStreaming || (msg.content ?? '') === ''
        if (canAttach) {
          msg.media = dedupe(msg.media, attachments)
          return
        }
      }
      const newMsg: ChatMessage = {
        id: generateId(),
        role: 'assistant',
        content: '',
        timestamp: new Date().toISOString(),
        media: attachments,
      }
      draft.messagesById[newMsg.id] = newMsg
      draft.messageOrder.push(newMsg.id)
    }) as Partial<SessionChatState>
  })
}
