// #1452 D2 (chat-history-paging-spec, "Amendment 2026-10-27 (founder, #1452
// D2)", A2.2/A2.4): the browser side of the history preview marker. The
// generated ToolCallResultFrame keeps `result: z.unknown()`, so any object
// carrying `_history_preview` is parsed here with the generated
// HistoryResultPreview schema plus the byte rule before it is treated as a
// marker; a failing object is a malformed item, never a marker or a plain
// result.
import { HistoryResultPreview as HistoryResultPreviewSchema } from '@/lib/api/generated/ws-schemas'
import type { HistoryResultPreview } from '@/lib/api/generated/asyncapi-types'

/** A2.1: a preview is at most 4,096 UTF-8 bytes of the full result's encoding. */
export const HISTORY_PREVIEW_MAX_BYTES = 4_096

const utf8 = new TextEncoder()

/** True when the value is an object that claims to be a history preview marker (valid or not). */
export function carriesHistoryPreviewKey(value: unknown): boolean {
  return typeof value === 'object' && value !== null && !Array.isArray(value) && Object.hasOwn(value, '_history_preview')
}

/** A valid history preview marker: the generated schema and the byte rule both hold. */
export function isHistoryResultPreview(value: unknown): value is HistoryResultPreview {
  if (!carriesHistoryPreviewKey(value)) return false
  const parsed = HistoryResultPreviewSchema.safeParse(value)
  return parsed.success && utf8.encode(parsed.data.preview).length <= HISTORY_PREVIEW_MAX_BYTES
}

/** An object carrying the reserved `_history_preview` key that is not a valid marker. */
export function isMalformedHistoryPreview(value: unknown): boolean {
  return carriesHistoryPreviewKey(value) && !isHistoryResultPreview(value)
}

/** Human size of a true byte count, binary units (matches the existing tool-result size labels). */
export function humanByteSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`
}
