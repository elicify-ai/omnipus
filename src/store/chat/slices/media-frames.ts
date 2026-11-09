// media-frames.ts: the `media` frame reducer — screenshots and other inline
// media, plus office-preview page-thumbnail groups (office-preview spec §5.2).
//
// Split out of replay-and-status-frames.ts::handleReplayAndStatusFrame (its
// grandfathered size budget may only shrink). Live frames and replayed frames
// both arrive here, so a reload rebuilds the same groups without re-rendering
// anything on the server (§5.3).
//
// Grouping rules (§5.2): media is grouped by (message, tool_call_id) instead
// of flattened; the dedup key is (tool_call_id, URL) — two previews of the
// same unchanged file produce the same page URLs and must stay two groups
// (B-40). Screenshots carry no tool_call_id, so their key stays the URL alone
// and their behaviour is unchanged.

import { produce } from 'immer'
import { generateId } from '@/lib/constants'
import { logDiagnostic } from '@/lib/telemetry'
import type { MediaFrame, MediaPart } from '@/lib/api/generated/asyncapi-types'
import { findOpenAssistantMessageId } from '../messages'
import type { ChatMessage, MediaAttachment, SessionChatState } from '../types'

type WithBucket = (sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void

/** True for the state marker of an unavailable thumbnail group (no image). */
export function isPreviewUnavailableMarker(item: MediaAttachment): boolean {
  return item.presentation === 'page_thumbnails' && item.unavailableReason !== undefined
}

function dedupKey(item: MediaAttachment): string {
  if (isPreviewUnavailableMarker(item)) return `marker\u0000${item.toolCallId ?? ''}`
  return `${item.toolCallId ?? ''}\u0000${item.url}`
}

/**
 * Merge incoming media into a message's existing list. A thumbnail group's
 * previous unavailable marker is replaced by whatever arrives next for the
 * same tool call (a Retry result, or a newer unavailable outcome), so the
 * group's state is always its latest frame's.
 */
function mergeMedia(existing: MediaAttachment[] | undefined, incoming: MediaAttachment[]): MediaAttachment[] {
  const replacedGroups = new Set(
    incoming.filter((a) => a.presentation === 'page_thumbnails' && a.toolCallId).map((a) => a.toolCallId as string),
  )
  const kept = (existing ?? []).filter(
    (a) => !(isPreviewUnavailableMarker(a) && a.toolCallId !== undefined && replacedGroups.has(a.toolCallId)),
  )
  const seen = new Set(kept.map(dedupKey))
  const fresh = incoming.filter((a) => {
    const key = dedupKey(a)
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
  return [...kept, ...fresh]
}

/** The message that already holds this tool call's thumbnail group, if any. */
function findGroupOwnerId(draft: SessionChatState, toolCallId: string): string | null {
  for (let i = draft.messageOrder.length - 1; i >= 0; i--) {
    const id = draft.messageOrder[i]
    const media = draft.messagesById[id]?.media
    if (media?.some((m) => m.presentation === 'page_thumbnails' && m.toolCallId === toolCallId)) return id
  }
  return null
}

function appendNotice(withBucket: WithBucket, sid: string, notice: string): void {
  withBucket(sid, (b) => {
    return produce(b, (draft) => {
      // ADR-070 §2.1/F2 (code review): a bare scan would append this notice
      // to a closed, closedBySteer bubble if this frame is the first to
      // arrive after a mid-turn steer. Mint a fresh bubble when there is no
      // eligible open one rather than silently dropping a failure notice the
      // user should see.
      const lastMsgId = findOpenAssistantMessageId(draft.messageOrder, draft.messagesById)
      if (lastMsgId) {
        const msg = draft.messagesById[lastMsgId]
        msg.content = (msg.content ?? '') + (msg.content ? '\n\n' : '') + notice
        return
      }
      const newMsg: ChatMessage = {
        id: generateId(),
        role: 'assistant',
        content: notice,
        timestamp: new Date().toISOString(),
      }
      draft.messagesById[newMsg.id] = newMsg
      draft.messageOrder.push(newMsg.id)
    }) as Partial<SessionChatState>
  })
}

function attachMedia(withBucket: WithBucket, sid: string, incoming: MediaAttachment[], groupToolCallId: string | undefined): void {
  withBucket(sid, (b) => {
    return produce(b, (draft) => {
      // §5.2: a later frame for an existing thumbnail group (Retry result,
      // reconnect re-delivery) joins that group's message, wherever it is.
      if (groupToolCallId) {
        const ownerId = findGroupOwnerId(draft, groupToolCallId)
        if (ownerId) {
          const owner = draft.messagesById[ownerId]
          owner.media = mergeMedia(owner.media, incoming)
          return
        }
      }
      // ADR-070 §2.1/F2: resolve the candidate through the eligibility
      // helper so a closed, closedBySteer bubble can never be considered —
      // the downstream `canAttach` refinement (isStreaming OR empty-content)
      // exists for a freshly-created, not-yet-streaming placeholder.
      const lastMsgId = findOpenAssistantMessageId(draft.messageOrder, draft.messagesById)
      if (lastMsgId) {
        const msg = draft.messagesById[lastMsgId]
        const canAttach = msg.isStreaming || (msg.content ?? '') === ''
        if (canAttach) {
          msg.media = mergeMedia(msg.media, incoming)
          return
        }
      }
      const newMsg: ChatMessage = {
        id: generateId(),
        role: 'assistant',
        content: '',
        timestamp: new Date().toISOString(),
        media: mergeMedia(undefined, incoming),
      }
      draft.messagesById[newMsg.id] = newMsg
      draft.messageOrder.push(newMsg.id)
    }) as Partial<SessionChatState>
  })
}

function toAttachment(frame: MediaFrame, part: MediaPart): MediaAttachment {
  return {
    type: part.type,
    url: part.url,
    filename: part.filename,
    contentType: part.content_type,
    caption: part.caption,
    ...(frame.tool_call_id ? { toolCallId: frame.tool_call_id } : {}),
    ...(frame.presentation ? { presentation: frame.presentation } : {}),
    ...(part.page_number !== undefined ? { pageNumber: part.page_number } : {}),
    ...(frame.preview ? { preview: frame.preview } : {}),
  }
}

export function handleMediaFrame(frame: MediaFrame, targetSid: string, withBucket: WithBucket): void {
  const parts = Array.isArray(frame.parts) ? frame.parts : []
  const isThumbnails = frame.presentation === 'page_thumbnails'
  if (isThumbnails && !frame.tool_call_id) {
    // Contract: a page_thumbnails frame always names its tool call. Without
    // one the images still render, but cannot be grouped or retried.
    console.warn('[chat] page_thumbnails media frame without tool_call_id — rendered ungrouped')
    logDiagnostic('chatPreviewFrameMissingToolCallId', { sessionId: targetSid })
  }

  // §5.2/§5.3: an unavailable thumbnail group carries no parts; record its
  // state so the persistent "Page previews unavailable" card renders (live
  // and after reload) — never the generic not-displayed notice.
  if (isThumbnails && parts.length === 0 && frame.preview?.outcome === 'unavailable') {
    const marker: MediaAttachment = {
      type: 'image',
      url: '',
      filename: frame.preview.source_name,
      contentType: '',
      presentation: 'page_thumbnails',
      preview: frame.preview,
      unavailableReason: frame.preview.unavailable_reason ?? null,
      ...(frame.tool_call_id ? { toolCallId: frame.tool_call_id } : {}),
    }
    attachMedia(withBucket, targetSid, [marker], frame.tool_call_id)
    return
  }

  if (parts.length === 0) {
    console.warn('[chat] Received media frame with empty or invalid parts — appending notice')
    logDiagnostic('chatMediaFrameInvalidParts', { sessionId: targetSid })
    appendNotice(withBucket, targetSid, '_1 attachment could not be displayed._')
    return
  }

  const attachments = parts.filter((p) => p.url && p.type).map((p) => toAttachment(frame, p))
  if (attachments.length === 0) {
    appendNotice(
      withBucket,
      targetSid,
      `_${parts.length} attachment${parts.length > 1 ? 's' : ''} could not be displayed._`,
    )
    return
  }
  attachMedia(withBucket, targetSid, attachments, isThumbnails ? frame.tool_call_id : undefined)
}
