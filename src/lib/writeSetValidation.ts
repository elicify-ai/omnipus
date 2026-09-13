// Write-set path input validation — the authoring half of the plan-lint
// write_set invariant (ADR-053 §Contract Surface, US-11/G-16).
//
// A plan member's `write_set` is the list of concrete repo-relative paths it
// creates or edits. `pkg/plan/lint.go` reads it at approve time and refuses a
// plan whose PARALLEL members declare overlapping paths. Every rule below
// exists to keep what the author types comparable under that lint's own
// normalisation (`pkg/plan/lint.go::normalizeWriteSetPath`: trim whitespace,
// strip one trailing slash, then POSIX `path.Clean`) — a path the lint cannot
// line up with its sibling is worse than no path at all, because the safety
// check then passes for the wrong reason.
//
// Only THREE things are rejected, each because it would silently defeat that
// comparison or duplicate an existing entry:
//
//   1. An ABSOLUTE path ("/src/a.go"). `path.Clean` preserves the leading
//      slash, so "/src/a.go" and "src/a.go" never compare equal and two
//      members writing the same file would lint clean.
//   2. A BACKSLASH path ("src\\a.go"). The lint splits on "/" only, so a
//      Windows-style separator collapses the whole path into one opaque
//      segment that matches nothing.
//   3. A DUPLICATE of an entry already on the task (compared AFTER
//      normalisation, so "src/a/" and "src/a" count as the same entry).
//
// Everything else is accepted verbatim. Leading "./" and embedded ".." are
// deliberately NOT rejected — `path.Clean` resolves both, so they compare
// correctly. A directory-style entry ("pkg/plan") is legitimate and meaningful:
// the lint treats it as overlapping anything nested beneath it.
//
// Whitespace and one trailing slash are normalised SILENTLY (never an error),
// mirroring `tagValidation.ts`'s case/whitespace handling.

export interface WriteSetValidationResult { // not-wire-format: local UI path-input validation result, computed client-side and never serialised over REST/WS
  /** True when the path may be committed (added to the member's write set). */
  ok: boolean
  /** The normalised value — present even when rejected, for echoing back to the input. */
  value: string
  /** Empty when `ok`; otherwise the exact validation message to render inline. */
  error: string
}

/**
 * Trim surrounding whitespace and strip a single trailing slash.
 *
 * Deliberately a SUBSET of the server's `normalizeWriteSetPath`: it stops
 * short of `path.Clean` so the chip shows the author what they typed rather
 * than a silently rewritten path. The two agree on every value this module
 * accepts, because `path.Clean` is a no-op on an already-clean relative path.
 */
export function normalizeWriteSetPath(raw: string): string {
  const trimmed = raw.trim()
  if (trimmed.length > 1 && trimmed.endsWith('/')) return trimmed.slice(0, -1)
  return trimmed
}

/**
 * Validate + normalise a single write-set path against the entries already
 * declared on this member.
 *
 * - Empty (after trim) → rejected with NO message (a silent no-op, matching
 *   the tag/todo inputs: there is no chip to add, nothing went wrong).
 * - Absolute → rejected, "Use a path relative to the workspace root".
 * - Contains a backslash → rejected, "Use forward slashes in paths".
 * - Already declared → rejected, "Already in the write set".
 * - Otherwise → accepted, normalised value returned.
 */
export function validateWriteSetPath(
  raw: string,
  existing: readonly string[] = [],
): WriteSetValidationResult {
  const value = normalizeWriteSetPath(raw)
  if (!value) return { ok: false, value: '', error: '' }
  if (value.startsWith('/')) {
    return { ok: false, value, error: 'Use a path relative to the workspace root' }
  }
  if (value.includes('\\')) {
    return { ok: false, value, error: 'Use forward slashes in paths' }
  }
  if (existing.some((p) => normalizeWriteSetPath(p) === value)) {
    return { ok: false, value, error: 'Already in the write set' }
  }
  return { ok: true, value, error: '' }
}
