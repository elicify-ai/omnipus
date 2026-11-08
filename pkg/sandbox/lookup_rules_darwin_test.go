//go:build darwin

package sandbox

import "testing"

// T-106 / §3.4 D8d; FR-056; B-70 (macOS half): the look-up-only rule in
// pkg/sandbox. Blocked until the one pkg/sandbox addition D8d names exists
// (SandboxPolicy's look-up-only folder field, "name is the developer's").
// The checks below are this test's committed oracle; they are written
// against renderSeatbeltProfile and seatbeltPolicyCacheKey in this package,
// both platforms' builds, once the field exists:
//
//   - macOS rendering: a policy with look-up entries renders EXACTLY one
//     `(allow file-read-metadata (literal "<folder>"))` line per entry,
//     quoted as other paths are (strconv.Quote/%q), and nothing else for
//     them — no `subpath`, no `file-read*`, no `file-write*`, no
//     `process-exec` for a look-up entry;
//   - a relative or uncleaned (non-absolute, non-cleaned) entry aborts the
//     render with an error and no profile (fail closed — the spawn does not
//     happen), the same discipline as validateSeatbeltPath;
//   - a policy whose ONLY entries are look-ups is refused like any policy
//     with no allows (look-up entries do not satisfy the zero-allows
//     refusal) — the render returns an error;
//   - the cache: two policies that differ ONLY in their look-up entries
//     render two different profiles, each holding its own folders — render
//     A, then B, then A again, and compare the text each time (B is never
//     rendered first, so a stale entry cannot hide); the key
//     (seatbeltPolicyCacheKey hashes the whole policy value) differs for A
//     and B.
//
// Not a pass if: only the presence of the line is checked, not its exact
// text; the cache case renders B first, so a stale entry never shows; a
// look-up entry satisfies the zero-allows refusal.
func TestSeatbeltLookupRulesRenderExactly(t *testing.T) {
	t.Fatal("BLOCKED: the SandboxPolicy look-up-only folder field does not exist on this code — the one pkg/sandbox addition D8d names (§3.4 D8d; founder Q37 = A) is not implemented here yet. Required by T-106 (§13): the exact rendered macOS line per folder, refusal of relative or untidy entries, the no-allows refusal unaffected, and the profile cache key changing with the new field; the full checks are this test's doc comment")
}
