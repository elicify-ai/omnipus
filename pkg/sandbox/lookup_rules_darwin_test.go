//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// T-106 / §3.4 D8d; FR-056; B-70 (macOS half): the look-up-only rule in
// pkg/sandbox, through this package's real profile cache, renderer and key
// functions (newSeatbeltProfileCache, renderSeatbeltProfile,
// seatbeltPolicyCacheKey) — never through a profile the test builds. The field is SandboxPolicy.LookupOnlyPaths (the one
// pkg/sandbox addition D8d names).
//
// Oracle (§13 T-106, §3.4 D8d, FR-056):
//
//   - macOS rendering: a policy with look-up entries renders EXACTLY one
//     `(allow file-read-metadata (literal "<folder>"))` line per entry,
//     quoted as other paths are (%q), and nothing else for them — no
//     `subpath`, no `file-read*`, no `file-write*`, no `process-exec` for a
//     look-up entry; the folder string appears in no other line;
//   - a relative or uncleaned (non-cleaned) entry aborts the render with an
//     error and no profile (fail closed — the spawn does not happen);
//   - a policy whose ONLY entries are look-ups is refused like any policy
//     with no allows (look-up entries do not satisfy the zero-allows
//     refusal);
//   - the cache: two policies that differ ONLY in their look-up entries
//     render two different profiles, each holding its own folders — render
//     A, then B, then A again, and compare the text each time (B is never
//     rendered first, so a stale entry cannot hide); the key
//     (seatbeltPolicyCacheKey hashes the whole policy value) differs for A
//     and B and is stable for the same policy.
//
// Not a pass if: only the presence of the line is checked, not its exact
// text; the cache case renders B first, so a stale entry never shows; a
// look-up entry satisfies the zero-allows refusal.
func TestSeatbeltLookupRulesRenderExactly(t *testing.T) {
	base := t.TempDir()
	work := filepath.Join(base, "call-work")
	require.NoError(t, os.MkdirAll(work, 0o700), "instrument: the granted folder exists (the renderer resolves rule paths)")
	lookupA := filepath.Join(base, "lookup-a")
	lookupB := filepath.Join(base, "lookup-b")
	require.NoError(t, os.MkdirAll(lookupA, 0o700), "instrument: look-up folder A exists")
	require.NoError(t, os.MkdirAll(lookupB, 0o700), "instrument: look-up folder B exists")

	policyA := SandboxPolicy{
		FilesystemRules: []PathRule{{Path: work, Access: AccessRead | AccessWrite}},
		LookupOnlyPaths: []string{lookupA},
	}
	policyB := SandboxPolicy{
		FilesystemRules: []PathRule{{Path: work, Access: AccessRead | AccessWrite}},
		LookupOnlyPaths: []string{lookupB},
	}

	// A renders: its exact look-up line, exactly once, the folder named in
	// no other line, and the granted folder's own rules unchanged (T-106:
	// the exact text, never presence alone).
	// Capacity two keeps both spec policies resident for the A -> B -> A
	// sequence, using the real renderer rather than a test replacement.
	cache := newSeatbeltProfileCache(2)
	profileA, errA := cache.getOrRender(policyA)
	require.NoError(t, errA, "T-106: a policy with a granted folder and a cleaned look-up entry renders")
	lineA := fmt.Sprintf("(allow file-read-metadata (literal %q))", lookupA)
	require.Contains(t, profileA, lineA,
		"T-106 §3.4 D8d/FR-056: the look-up folder renders exactly (allow file-read-metadata (literal %q))", lookupA)
	require.Equal(t, 1, strings.Count(profileA, lineA),
		"T-106: the look-up line appears exactly once")
	metadataLines := 0
	for _, line := range strings.Split(profileA, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, lookupA) {
			require.Equal(t, lineA, trimmed,
				"T-106 §3.4 D8d/FR-056: the only line naming the look-up folder is its exact metadata line (no subpath, no file-read*, nothing below) — found %s", trimmed)
		}
		if strings.HasPrefix(trimmed, "(allow file-read-metadata") {
			metadataLines++
		}
	}
	require.Equal(t, 1, metadataLines,
		"T-106: the look-up entries are the profile's only file-read-metadata lines")
	resolvedWork, linkErr := filepath.EvalSymlinks(work)
	require.NoError(t, linkErr, "instrument: the granted folder resolves")
	require.Contains(t, profileA, fmt.Sprintf("(allow file-read* (subpath %q))", resolvedWork),
		"T-106: the granted folder's own D8c-era rules still render unchanged")

	// Refusals, fail closed: an error and no profile, for a relative entry,
	// for an uncleaned entry, and for a policy whose only entries are
	// look-ups (they do not satisfy the zero-allows refusal).
	relative := SandboxPolicy{
		FilesystemRules: []PathRule{{Path: work, Access: AccessRead | AccessWrite}},
		LookupOnlyPaths: []string{"relative/lookup"},
	}
	profileRel, relErr := renderSeatbeltProfile(relative)
	require.Error(t, relErr, "T-106 §3.4 D8d: a relative look-up entry aborts the render")
	require.Empty(t, profileRel, "T-106 §3.4 D8d: a refused render produces no profile")
	unclean := SandboxPolicy{
		FilesystemRules: []PathRule{{Path: work, Access: AccessRead | AccessWrite}},
		LookupOnlyPaths: []string{base + "/dup/../lookup-unclean"},
	}
	profileUnclean, uncleanErr := renderSeatbeltProfile(unclean)
	require.Error(t, uncleanErr, "T-106 §3.4 D8d: an uncleaned look-up entry aborts the render")
	require.Empty(t, profileUnclean, "T-106 §3.4 D8d: a refused render produces no profile")
	lookupOnly := SandboxPolicy{LookupOnlyPaths: []string{lookupA}}
	profileOnly, onlyErr := renderSeatbeltProfile(lookupOnly)
	require.Error(t, onlyErr, "T-106 §3.4 D8d/FR-056: look-up entries do not satisfy the zero-allows refusal")
	require.Empty(t, profileOnly, "T-106 §3.4 D8d/FR-056: a refused render produces no profile")

	// Cache: render A, then B, then A again — B never first. Each profile
	// holds its own folder and only its own; the key differs for A and B and
	// is stable for A, so the cache can never serve B's profile for A.
	profileB, errB := cache.getOrRender(policyB)
	require.NoError(t, errB, "T-106: policy B renders")
	require.NotEqual(t, profileA, profileB,
		"T-106 §13: two policies differing only in their look-up entries render two different profiles")
	lineB := fmt.Sprintf("(allow file-read-metadata (literal %q))", lookupB)
	require.NotContains(t, profileA, lineB, "T-106 §13: A's profile does not hold B's folder")
	require.Contains(t, profileB, lineB, "T-106 §13: B's profile holds B's folder")
	require.NotContains(t, profileB, lineA, "T-106 §13: B's profile does not hold A's folder")
	profileAagain, errAgain := cache.getOrRender(policyA)
	require.NoError(t, errAgain, "T-106: policy A renders again")
	require.Equal(t, profileA, profileAagain,
		"T-106 §13: rendering A again returns A's own profile — a stale entry never shows")
	require.NotEqual(t, seatbeltPolicyCacheKey(policyA), seatbeltPolicyCacheKey(policyB),
		"T-106 §3.4 D8d: the profile cache key changes with the look-up entries")
	keyA, keyAagain := seatbeltPolicyCacheKey(policyA), seatbeltPolicyCacheKey(policyA)
	require.Equal(t, keyA, keyAagain,
		"T-106: the cache key is stable for the same policy")
}
