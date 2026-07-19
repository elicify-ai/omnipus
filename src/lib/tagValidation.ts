// Tag input validation (ADR-049 D1, SD-C8) — the milestone replacement.
//
// Tags are workspace-scoped, free-form strings: lowercase, trimmed,
// deduplicated, at most 16 per task, each at most 64 characters.
// `prefix:value` (e.g. `milestone:q3`) is convention only, not schema — never
// rejected, never re-prefixed or uniquified client-side (that is backend's
// job during migration, D1). Spaces are permitted verbatim (SD-C8): a tag
// like "Q3 Release" normalises to "q3 release", it is NOT rejected for
// containing a space.
//
// Case + whitespace are normalised SILENTLY (no error) on commit; length and
// count are the only two hard rejections, each with the exact user-facing
// message the BDD dataset specifies.

export const TAG_MAX_LENGTH = 64
export const TAG_MAX_COUNT = 16

export interface TagValidationResult {
  /** True when the tag may be committed (added to the task's tag set). */
  ok: boolean
  /** The normalised (lowercased + trimmed) value — present even when rejected, for echoing back to the input. */
  value: string
  /** Empty when `ok`; otherwise the exact validation message to render inline. */
  error: string
}

/** Lowercase + trim a raw tag input. Pure, silent normalisation (SD-C8) — never rejects on case/whitespace alone. */
export function normalizeTag(raw: string): string {
  return raw.trim().toLowerCase()
}

/**
 * Grapheme-safe length (mirrors `truncateLabel`, `SubagentBlock.tsx:29`) —
 * counts Unicode code points via `Array.from` rather than UTF-16 code units
 * (`.length`), so combining marks / surrogate pairs (e.g. "café", emoji)
 * aren't silently double-counted against the 64-char cap.
 */
export function graphemeLength(s: string): number {
  return Array.from(s).length
}

/**
 * Validate + normalise a single tag against the task's existing tag set.
 *
 * - Empty (after trim) → rejected with no message (a silent no-op, mirroring
 *   the todo input's `!text` guard — there is no chip to add, nothing to
 *   tell the user went wrong).
 * - Over 64 (grapheme) chars → rejected, "Max 64 characters".
 * - Would exceed 16 distinct tags on the task → rejected, "Max 16 tags per task"
 *   (re-adding an already-present tag is a no-op, not a count violation).
 * - Otherwise → accepted, normalised value returned.
 */
export function validateTag(raw: string, existing: readonly string[] = []): TagValidationResult {
  const value = normalizeTag(raw)
  if (!value) return { ok: false, value: '', error: '' }
  if (graphemeLength(value) > TAG_MAX_LENGTH) {
    return { ok: false, value, error: 'Max 64 characters' }
  }
  if (!existing.includes(value) && existing.length >= TAG_MAX_COUNT) {
    return { ok: false, value, error: 'Max 16 tags per task' }
  }
  return { ok: true, value, error: '' }
}
