// previewGroups.ts — pure helpers for office-preview thumbnail groups
// (office-preview spec §5.2–§5.4): partition a message's media into inline
// items and per-tool-call page groups, derive each group's display state, and
// return focus when the enlarged page viewer closes.

import type { MediaAttachment } from '@/store/chat'
import type { MediaPreview } from '@/lib/api/generated/asyncapi-types'

/** DOM hooks used to return focus after the viewer closes (virtualized rows remount). */
export const PREVIEW_GROUP_ATTR = 'data-preview-group'
export const PREVIEW_THUMB_ATTR = 'data-preview-thumb'

export interface PreviewGroup {
  /** Group key: the tool call id (or a per-position fallback when absent). */
  key: string
  toolCallId?: string
  /** Stored page thumbnails, in page order. */
  pages: MediaAttachment[]
  /** The latest preview record delivered for this group. */
  preview?: MediaPreview
  /**
   * Present when the latest outcome is unavailable. `marker` is the store's
   * state-marker item itself: a new object means a new frame arrived (e.g. a
   * Retry that failed again), which resets the card's Retry state.
   */
  unavailable?: { reason: MediaPreview['unavailable_reason'] | null; marker: MediaAttachment }
}

export type MediaBlock =
  | { kind: 'item'; key: string; item: MediaAttachment }
  | { kind: 'group'; key: string; group: PreviewGroup }

/**
 * Inline media keeps its order; every page_thumbnails item joins its tool
 * call's group, which is placed where that group's first item appeared.
 */
export function partitionMedia(media: readonly MediaAttachment[]): MediaBlock[] {
  const blocks: MediaBlock[] = []
  const groups = new Map<string, PreviewGroup>()
  media.forEach((item, index) => {
    if (item.presentation !== 'page_thumbnails') {
      blocks.push({ kind: 'item', key: `${item.url}-${index}`, item })
      return
    }
    const key = item.toolCallId ?? `ungrouped-${index}`
    let group = groups.get(key)
    if (!group) {
      group = { key, ...(item.toolCallId ? { toolCallId: item.toolCallId } : {}), pages: [] }
      groups.set(key, group)
      blocks.push({ kind: 'group', key: `group-${key}`, group })
    }
    if (item.preview) group.preview = item.preview
    if (item.unavailableReason !== undefined) {
      group.unavailable = { reason: item.unavailableReason, marker: item }
    } else {
      group.pages.push(item)
      // A later stored page means a later frame for this tool call carried
      // images; the reducer already dropped the older marker, this keeps the
      // derived state consistent with the latest record either way.
      if (item.preview && item.preview.outcome !== 'unavailable') delete group.unavailable
    }
  })
  for (const group of groups.values()) {
    group.pages.sort((a, b) => (a.pageNumber ?? 0) - (b.pageNumber ?? 0))
  }
  return blocks
}

/** "report.docx · page 3 of 12" — the server caption, else built from the record. */
export function pageCaption(page: MediaAttachment, preview: MediaPreview | undefined): string {
  if (page.caption) return page.caption
  const name = preview?.source_name ?? page.filename
  if (page.pageNumber !== undefined && preview) return `${name} · page ${page.pageNumber} of ${preview.page_count}`
  if (page.pageNumber !== undefined) return `${name} · page ${page.pageNumber}`
  return name
}

/** The number of pages this batch asked for (first_page..last_page). */
export function requestedPageCount(preview: MediaPreview): number {
  return Math.max(0, preview.last_page - preview.first_page + 1)
}

const REASON_TEXT: Record<NonNullable<MediaPreview['unavailable_reason']>, string> = {
  renderer_missing: 'the PDF renderer is not installed',
  renderer_damaged: 'the PDF renderer is damaged',
  renderer_failed: 'rendering the pages failed',
  storage_failed: 'the page images could not be stored',
}

export function unavailableReasonText(reason: MediaPreview['unavailable_reason'] | null): string | null {
  return reason ? REASON_TEXT[reason] : null
}

function findByAttr(attr: string, value: string): HTMLElement | null {
  for (const el of document.querySelectorAll<HTMLElement>(`[${attr}]`)) {
    if (el.getAttribute(attr) === value && el.isConnected) return el
  }
  return null
}

export function thumbAttrValue(groupKey: string, pageNumber: number): string {
  return `${groupKey}#${pageNumber}`
}

/**
 * §5.4: Escape or Close returns focus to the opening thumbnail, else to its
 * group container, else to the chat input (the row may have been
 * virtualized away while the viewer was open). Returns the element focused,
 * or null when none of the three exists.
 */
export function restorePreviewFocus(groupKey: string, openerPage: number): HTMLElement | null {
  const target =
    findByAttr(PREVIEW_THUMB_ATTR, thumbAttrValue(groupKey, openerPage)) ??
    findByAttr(PREVIEW_GROUP_ATTR, groupKey) ??
    document.querySelector<HTMLElement>('[data-testid="chat-input"]')
  target?.focus()
  return target
}
