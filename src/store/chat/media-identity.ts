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

function thumbnailGroupOf(item: MediaAttachment): string | undefined {
  return item.presentation === 'page_thumbnails' ? item.toolCallId : undefined
}

/** The items (pages and marker) of one tool call's thumbnail group, in order. */
export function thumbnailGroupItems(media: readonly MediaAttachment[], toolCallId: string): MediaAttachment[] {
  return media.filter((item) => thumbnailGroupOf(item) === toolCallId)
}

/**
 * Replace, in place, every thumbnail group of `existing` that `incoming` also
 * carries (§5.2: a group's state is its latest frame's, and it keeps its
 * place among the other cards). Returns the merged list and the incoming
 * items that were not placed (other media, and groups `existing` lacks).
 */
export function replaceThumbnailGroups(
  existing: readonly MediaAttachment[],
  incoming: readonly MediaAttachment[],
): { merged: MediaAttachment[]; rest: MediaAttachment[] } {
  const incomingGroups = new Set(incoming.flatMap((item) => thumbnailGroupOf(item) ?? []))
  const placed = new Set<string>()
  const merged: MediaAttachment[] = []
  for (const item of existing) {
    const group = thumbnailGroupOf(item)
    if (group === undefined || !incomingGroups.has(group)) {
      merged.push(item)
      continue
    }
    if (!placed.has(group)) {
      placed.add(group)
      merged.push(...incoming.filter((next) => thumbnailGroupOf(next) === group))
    }
  }
  const rest = incoming.filter((item) => {
    const group = thumbnailGroupOf(item)
    return group === undefined || !placed.has(group)
  })
  return { merged, rest }
}
