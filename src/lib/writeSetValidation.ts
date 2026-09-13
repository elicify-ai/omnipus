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
// Five things are rejected, each because it would silently defeat that
// comparison or duplicate an existing entry:
//
//   1. An ABSOLUTE path ("/src/a.go"). `path.Clean` preserves the leading
//      slash, so "/src/a.go" and "src/a.go" never compare equal and two
//      members writing the same file would lint clean.
//   2. A BACKSLASH path ("src\\a.go"). The lint splits on "/" only, so a
//      Windows-style separator collapses the whole path into one opaque
//      segment that matches nothing.
//   3. A path that CLIMBS ABOVE the workspace root ("../repo/src/a.go", or
//      anything whose ".." segments resolve to a leading ".."). This is the
//      same defect as (1) wearing different clothes: `path.Clean` leaves the
//      leading ".." in place, so "../repo/src/a.go" can never line up with a
//      sibling's "src/a.go" even when both name the identical file — the
//      overlap check then reports "no conflict" and the plan runs two members
//      at the same file in parallel.
//   4. A path naming the WHOLE workspace (".", "./"). `path.Clean` collapses
//      it to "." — a value `pathsOverlap` treats as a sibling of nothing, so
//      it neither protects the member nor conflicts with anyone.
//   5. A DUPLICATE of an entry already on the task, compared on the SERVER's
//      key (see `writeSetComparisonKey`) rather than on the raw text, so
//      "./src/a.go", "src/x/../a.go", "src/a//" and "src/a.go" are all
//      recognised as the one entry the server will see.
//
// Everything else is accepted verbatim, and what the author typed is what gets
// stored: a leading "./" or an embedded ".." that stays inside the root is
// fine, because `path.Clean` resolves both before the lint compares them. A
// directory-style entry ("pkg/plan") is legitimate and meaningful: the lint
// treats it as overlapping anything nested beneath it.
//
// Whitespace and one trailing slash are normalised SILENTLY (never an error),
// mirroring `tagValidation.ts`'s case/whitespace handling.

// The `// not-wire-format` annotation below is LOAD-BEARING, not decoration:
// `scripts/check-no-handwritten-wire-types.sh` (Constraint #8) walks EVERY
// non-test `.ts`/`.tsx` file under `src/lib/` — its own header comment still
// says "api.ts or ws.ts", but the implementation and its self-test ST-17 were
// widened to the whole directory. Delete the annotation and the script exits
// 1 with `[ts-wire-type] hand-written wire-format type
// 'WriteSetValidationResult'`. Verified by deleting it and running the script.
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
 * The key the SERVER will compare this path by — a faithful port of
 * `pkg/plan/lint.go::normalizeWriteSetPath`: trim whitespace, strip one
 * trailing slash, then POSIX `path.Clean`.
 *
 * This is what makes "./src/a.go", "src/x/../a.go", "src/a//" and "src/a.go"
 * one entry rather than four. It is deliberately NOT what gets stored — see
 * `normalizeWriteSetPath` for why the chip keeps the author's own spelling —
 * it is only ever used to decide whether two spellings are the same path.
 *
 * One documented divergence, on a value this module never accepts anyway: the
 * server's `strings.TrimSuffix(p, "/")` turns a bare "/" into "" and returns
 * early; this returns "/" (the absolute-path rejection catches it first).
 */
export function writeSetComparisonKey(raw: string): string {
  const value = normalizeWriteSetPath(raw)
  if (!value) return ''
  return cleanPosixPath(value)
}

/**
 * POSIX `path.Clean`, ported from Go's `path.Clean` semantics: drop "." and
 * empty segments, resolve ".." against the preceding segment, keep a leading
 * ".." on a relative path (there is nothing above the root to resolve it
 * against) and drop it on a rooted one, and return "." for a path that
 * resolves to nothing.
 */
function cleanPosixPath(p: string): string {
  if (p === '') return '.'
  const rooted = p.startsWith('/')
  const out: string[] = []
  for (const segment of p.split('/')) {
    if (segment === '' || segment === '.') continue
    if (segment === '..') {
      if (out.length > 0 && out[out.length - 1] !== '..') {
        out.pop()
      } else if (!rooted) {
        // Relative path: a leading ".." survives, exactly as Go keeps it.
        out.push('..')
      }
      // Rooted path: ".." above "/" is dropped, exactly as Go drops it.
      continue
    }
    out.push(segment)
  }
  const joined = out.join('/')
  if (rooted) return `/${joined}`
  return joined === '' ? '.' : joined
}

/**
 * Validate + normalise a single write-set path against the entries already
 * declared on this member.
 *
 * - Empty (after trim) → rejected with NO message (a silent no-op, matching
 *   the tag/todo inputs: there is no chip to add, nothing went wrong).
 * - Absolute → rejected, "Use a path relative to the workspace root".
 * - Contains a backslash → rejected, "Use forward slashes in paths".
 * - Climbs above the workspace root → rejected, "Use a path inside the
 *   workspace root — ".." climbs outside it".
 * - Names the whole workspace (".") → rejected, "Name the files or folders
 *   this task writes, not the whole workspace".
 * - Already declared (on the server's comparison key, not the raw text) →
 *   rejected, "Already in the write set".
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
  const key = writeSetComparisonKey(value)
  if (key === '..' || key.startsWith('../')) {
    return {
      ok: false,
      value,
      error: 'Use a path inside the workspace root — ".." climbs outside it',
    }
  }
  if (key === '.') {
    return {
      ok: false,
      value,
      error: 'Name the files or folders this task writes, not the whole workspace',
    }
  }
  if (existing.some((p) => writeSetComparisonKey(p) === key)) {
    return { ok: false, value, error: 'Already in the write set' }
  }
  return { ok: true, value, error: '' }
}
