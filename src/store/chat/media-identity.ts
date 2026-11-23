// media-identity.ts: when two chat media items are the same item (office-preview
// spec §5.2). Shared by the live media reducer (slices/media-frames.ts) and the
// history merge/fold (history-state.ts::unionMedia), so both agree.
import type { MediaAttachment } from './types'

/** True for the state marker of an unavailable thumbnail group (no image). */
export function isPreviewUnavailableMarker(item: MediaAttachment): boolean {
  return item.presentation === 'page_thumbnails' && item.unavailableReason !== undefined
}

/**
 * The dedup key: (tool_call_id, URL), so two previews of the same unchanged
 * file stay two groups (B-40); a group's unavailable marker is one per tool
 * call (it has no URL). Screenshots carry no tool_call_id: URL alone.
 */
export function mediaDedupKey(item: MediaAttachment): string {
  if (isPreviewUnavailableMarker(item)) return `marker\u0000${item.toolCallId ?? ''}`
  return `${item.toolCallId ?? ''}\u0000${item.url}`
}
