// Body-replace tests (ADR-068 D14.1, D15.3; spec Draft 9 FR-047, AC-E3).
//
// The oracle for every expected string here is HAND-CONSTRUCTED — either a
// literal string written out in the test, or lines joined with strings.Join
// — never derived by calling the code under test and asserting it against
// itself. A test that re-derives its expectation from the implementation
// cannot fail when the implementation is wrong; it can only fail when the
// implementation is INCONSISTENT with itself.
//
// Line numbers in every fixture below are hand-counted in the comment above
// the fixture, against FR-047's own rule: 1-based, counted over the WHOLE
// FILE (frontmatter included).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/ -run ReplaceBody
package knowledge

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// The central requirement (Deliverable 2): ambiguous anchor is refused,
// naming BOTH matches. Spec test name per the traceability table (§7 test 34,
// §12 FR-047 row): TestReplaceBody_AmbiguousAnchorIsRefused.
// ---------------------------------------------------------------------------

// Fixture, lines hand-counted:
//
//	1  ---
//	2  title: Deal
//	3  ---
//	4  (blank)
//	5  ## Pricing        <- first anchor occurrence
//	6  (blank)
//	7  Old.
//	8  (blank)
//	9  ## Terms
//	10 (blank)
//	11 X.
//	12 (blank)
//	13 ## Pricing        <- second anchor occurrence
//	14 (blank)
//	15 Dup.
const rbTwoMatchesFixture = "---\ntitle: Deal\n---\n\n## Pricing\n\nOld.\n\n## Terms\n\nX.\n\n## Pricing\n\nDup.\n"

func TestReplaceBody_AmbiguousAnchorIsRefused(t *testing.T) {
	edit := ReplaceBodyByAnchor("Deals/Acme.md", "## Pricing", "## Pricing\n\nNew.\n")
	out, err := edit([]byte(rbTwoMatchesFixture))

	require.Error(t, err)
	assert.Nil(t, out, "a refused edit must not return a partial replacement")

	var ambig *AmbiguousAnchorError
	require.ErrorAs(t, err, &ambig, "the refusal must be the typed AmbiguousAnchorError, not a generic error")
	assert.ErrorIs(t, err, ErrAmbiguousAnchor)

	require.Len(t, ambig.Matches, 2, "both matches must be named, not just a count")
	assert.Equal(t, 5, ambig.Matches[0].Line, "first occurrence is on line 5")
	assert.Equal(t, 13, ambig.Matches[1].Line, "second occurrence is on line 13")

	msg := err.Error()
	assert.Contains(t, msg, `"## Pricing"`)
	assert.Contains(t, msg, "Deals/Acme.md")
	assert.Contains(t, msg, "5")
	assert.Contains(t, msg, "13")
	assert.Contains(t, msg, "give a unique anchor or a line_range",
		"the remedy clause FR-047 requires must be present")
}

// TestReplaceBody_AmbiguousAnchor_ThreeMatchesNamesAll proves the refusal
// does not silently degenerate into "first two" once there are more than
// two — FR-047 says "naming every match".
func TestReplaceBody_AmbiguousAnchor_ThreeMatchesNamesAll(t *testing.T) {
	// Lines: 1 heading A, 2 blank, 3 heading A, 4 blank, 5 heading A.
	src := "## Repeat\n\n## Repeat\n\n## Repeat\n"
	edit := ReplaceBodyByAnchor("notes/x.md", "## Repeat", "## Repeat\n")
	_, err := edit([]byte(src))
	require.Error(t, err)

	var ambig *AmbiguousAnchorError
	require.ErrorAs(t, err, &ambig)
	require.Len(t, ambig.Matches, 3)
	assert.Equal(t, []int{1, 3, 5}, []int{ambig.Matches[0].Line, ambig.Matches[1].Line, ambig.Matches[2].Line})
	assert.Contains(t, err.Error(), "3 times")
	assert.Contains(t, err.Error(), "1, 3, 5")
}

// ---------------------------------------------------------------------------
// Deliverable 3: the NOT-FOUND refusal is a DISTINCT type from ambiguity,
// with its own remedy.
// ---------------------------------------------------------------------------

func TestReplaceBody_AnchorNotFound_IsDistinctFromAmbiguous(t *testing.T) {
	src := "---\ntitle: Deal\n---\n\n## Pricing\n\nOld.\n"
	edit := ReplaceBodyByAnchor("Deals/Acme.md", "## Discount", "## Discount\n\nNew.\n")
	out, err := edit([]byte(src))

	require.Error(t, err)
	assert.Nil(t, out)

	var nf *AnchorNotFoundError
	require.ErrorAs(t, err, &nf, "not-found must be its own type, not AmbiguousAnchorError with zero matches")
	assert.ErrorIs(t, err, ErrAnchorNotFound)
	assert.False(t, errors.Is(err, ErrAmbiguousAnchor), "not-found must never satisfy the ambiguity sentinel")

	msg := err.Error()
	assert.Contains(t, msg, `"## Discount"`)
	assert.Contains(t, msg, "Deals/Acme.md")
	assert.Contains(t, msg, "knowledge_read", "the remedy must tell the caller how to get a correct anchor")
}

// ---------------------------------------------------------------------------
// Deliverable 1 (anchor addressing) — scoped to the BODY only, and matched
// byte-exact with no normalisation.
// ---------------------------------------------------------------------------

// Fixture: the literal anchor text also appears inside a frontmatter scalar
// value, and once (only) in the body. Only the body occurrence may count.
//
//	1  ---
//	2  title: Deal
//	3  note: "## Pricing"
//	4  ---
//	5  (blank)
//	6  Body text.
//	7  (blank)
//	8  ## Pricing     <- the only match that counts
//	9  (blank)
//	10 Only occurrence in body.
const rbFrontmatterDecoyFixture = "---\ntitle: Deal\nnote: \"## Pricing\"\n---\n\nBody text.\n\n## Pricing\n\nOnly occurrence in body.\n"

func TestReplaceBody_AnchorScopedToBody_FrontmatterOccurrenceIgnored(t *testing.T) {
	edit := ReplaceBodyByAnchor("x.md", "## Pricing", "## Pricing\n\nNew.\n")
	out, err := edit([]byte(rbFrontmatterDecoyFixture))
	require.NoError(t, err, "the frontmatter occurrence must not count, so there is exactly one real match")

	// The expected value is built from the fixture's OWN bytes around its
	// LAST (body) occurrence — a literal substring splice around whichever
	// occurrence is NOT the frontmatter one — rather than hand-typed, so a
	// miscounted blank line in the oracle cannot masquerade as a bug in the
	// matcher.
	idx := strings.LastIndex(rbFrontmatterDecoyFixture, "## Pricing")
	prefix := rbFrontmatterDecoyFixture[:idx]
	suffix := rbFrontmatterDecoyFixture[idx+len("## Pricing"):]
	require.Contains(t, prefix, `note: "## Pricing"`,
		"fixture sanity: the frontmatter occurrence must be BEFORE the split point, proving "+
			"LastIndex found the body one and not the frontmatter one")
	want := prefix + "## Pricing\n\nNew.\n" + suffix
	assert.Equal(t, want, string(out))
}

// TestReplaceBody_AnchorMatch_WhitespaceIsExactNotNormalised is the guard
// against the standing lesson: no whitespace-blind comparison anywhere in
// the matcher. A trailing space the file does not have must NOT match.
func TestReplaceBody_AnchorMatch_WhitespaceIsExactNotNormalised(t *testing.T) {
	src := "---\ntitle: Deal\n---\n\n## Pricing\n\nOld.\n"

	t.Run("anchor with an extra trailing space is not found", func(t *testing.T) {
		edit := ReplaceBodyByAnchor("x.md", "## Pricing ", "New.")
		_, err := edit([]byte(src))
		require.Error(t, err)
		var nf *AnchorNotFoundError
		require.ErrorAs(t, err, &nf, "a trimmed/normalised matcher would wrongly find this")
	})

	t.Run("the exact byte sequence matches", func(t *testing.T) {
		edit := ReplaceBodyByAnchor("x.md", "## Pricing", "## Discount")
		out, err := edit([]byte(src))
		require.NoError(t, err)
		assert.Equal(t, "---\ntitle: Deal\n---\n\n## Discount\n\nOld.\n", string(out))
	})
}

// TestReplaceBody_AnchorMatch_LineEndingsAreExact proves CRLF and LF are
// never treated as equivalent by the matcher, in either direction.
func TestReplaceBody_AnchorMatch_LineEndingsAreExact(t *testing.T) {
	crlf := "---\r\ntitle: Deal\r\n---\r\n\r\n## Pricing\r\nDetails here.\r\n\r\n## Terms\r\n"

	t.Run("an LF anchor does not match a CRLF file", func(t *testing.T) {
		edit := ReplaceBodyByAnchor("x.md", "## Pricing\nDetails here.", "replaced")
		_, err := edit([]byte(crlf))
		require.Error(t, err)
		var nf *AnchorNotFoundError
		require.ErrorAs(t, err, &nf)
	})

	t.Run("the exact CRLF anchor matches", func(t *testing.T) {
		edit := ReplaceBodyByAnchor("x.md", "## Pricing\r\nDetails here.", "## Pricing\r\nReplaced.")
		out, err := edit([]byte(crlf))
		require.NoError(t, err)
		assert.Equal(t, "---\r\ntitle: Deal\r\n---\r\n\r\n## Pricing\r\nReplaced.\r\n\r\n## Terms\r\n", string(out))
	})
}

// ---------------------------------------------------------------------------
// Deliverable 4: byte preservation everywhere outside the replaced span.
// ---------------------------------------------------------------------------

func TestReplaceBody_ByteIdentical_OutsideTheReplacedSpan(t *testing.T) {
	src := "---\ntitle: Deal\nnote: has \"quotes\" and\ttabs\n---\n\n## Summary\n\nPrefix paragraph, untouched.\n\n" +
		"## Pricing\n\nOld pricing block.\nSecond line of it.\n\n## Terms and Conditions\n\n" +
		"Suffix paragraph, also untouched.\n// a comment-shaped line\n"
	edit := ReplaceBodyByAnchor("x.md", "Old pricing block.\nSecond line of it.", "Flat $99/mo, billed annually.")
	out, err := edit([]byte(src))
	require.NoError(t, err)

	prefix := "---\ntitle: Deal\nnote: has \"quotes\" and\ttabs\n---\n\n## Summary\n\nPrefix paragraph, untouched.\n\n## Pricing\n\n"
	suffix := "\n\n## Terms and Conditions\n\nSuffix paragraph, also untouched.\n// a comment-shaped line\n"
	want := prefix + "Flat $99/mo, billed annually." + suffix

	require.True(t, strings.HasPrefix(src, prefix), "fixture sanity: prefix must actually be the file's own prefix")
	require.True(t, strings.HasSuffix(src, suffix), "fixture sanity: suffix must actually be the file's own suffix")
	assert.Equal(t, want, string(out))
	assert.Equal(t, prefix, string(out[:len(prefix)]), "every byte before the match is untouched")
	assert.Equal(t, suffix, string(out[len(out)-len(suffix):]), "every byte after the match is untouched")
}

// ---------------------------------------------------------------------------
// Empty / malformed anchor requests.
// ---------------------------------------------------------------------------

func TestReplaceBody_EmptyAnchorIsRefused(t *testing.T) {
	edit := ReplaceBodyByAnchor("x.md", "", "New.")
	_, err := edit([]byte("---\ntitle: Deal\n---\n\nBody.\n"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrReplaceBodySpec)
}

// ---------------------------------------------------------------------------
// Line-range addressing.
// ---------------------------------------------------------------------------

// Fixture, lines hand-counted:
//
//	1 ---
//	2 title: Deal
//	3 ---
//	4 (blank)
//	5 Line A
//	6 Line B
//	7 Line C
const rbLineRangeFixture = "---\ntitle: Deal\n---\n\nLine A\nLine B\nLine C\n"

func TestReplaceBody_LineRange_SingleLine(t *testing.T) {
	edit := ReplaceBodyByLineRange("x.md", 6, 6, "Replaced B\n")
	out, err := edit([]byte(rbLineRangeFixture))
	require.NoError(t, err)

	want := strings.Join([]string{"---", "title: Deal", "---", "", "Line A", "Replaced B", "Line C"}, "\n") + "\n"
	assert.Equal(t, want, string(out))
}

func TestReplaceBody_LineRange_MultiLineSpan(t *testing.T) {
	edit := ReplaceBodyByLineRange("x.md", 5, 6, "X\nY\nZ\n")
	out, err := edit([]byte(rbLineRangeFixture))
	require.NoError(t, err)

	want := strings.Join([]string{"---", "title: Deal", "---", "", "X", "Y", "Z", "Line C"}, "\n") + "\n"
	assert.Equal(t, want, string(out))
}

func TestReplaceBody_LineRange_OutsideFileIsRefused(t *testing.T) {
	edit := ReplaceBodyByLineRange("x.md", 6, 10, "New\n")
	_, err := edit([]byte(rbLineRangeFixture))
	require.Error(t, err)

	var lre *LineRangeError
	require.ErrorAs(t, err, &lre)
	assert.ErrorIs(t, err, ErrLineRangeInvalid)
	assert.Equal(t, "outside", lre.Reason)
	assert.Equal(t, 7, lre.TotalLines)
}

func TestReplaceBody_LineRange_FrontmatterOverlapIsRefused(t *testing.T) {
	edit := ReplaceBodyByLineRange("x.md", 2, 5, "New\n")
	_, err := edit([]byte(rbLineRangeFixture))
	require.Error(t, err)

	var lre *LineRangeError
	require.ErrorAs(t, err, &lre)
	assert.Equal(t, "frontmatter", lre.Reason)
	assert.Equal(t, 3, lre.FrontmatterLines)

	assert.Contains(t, err.Error(), "frontmatter")
	assert.Contains(t, err.Error(), "set_property")
}

func TestReplaceBody_LineRange_InvalidBoundsRefused(t *testing.T) {
	cases := []struct {
		name       string
		start, end int
	}{
		{"start below 1", 0, 3},
		{"end before start", 5, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edit := ReplaceBodyByLineRange("x.md", tc.start, tc.end, "New\n")
			_, err := edit([]byte(rbLineRangeFixture))
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrLineRangeInvalid)
		})
	}
}

// TestReplaceBody_LineRange_TerminatorConsumption documents Deliverable 5's
// stated rule precisely: replacing a non-last line consumes THAT line's own
// terminator, so a body with no trailing newline glues onto whatever follows.
// This is asserted, not merely described, so a change to the rule fails a
// test rather than only a comment.
func TestReplaceBody_LineRange_TerminatorConsumption(t *testing.T) {
	edit := ReplaceBodyByLineRange("x.md", 5, 5, "NoTrailingNewline")
	out, err := edit([]byte(rbLineRangeFixture))
	require.NoError(t, err)
	assert.Equal(t, "---\ntitle: Deal\n---\n\nNoTrailingNewlineLine B\nLine C\n", string(out),
		"the tool adds no newline on the caller's behalf")
}

// TestReplaceBody_LineRange_LastLineNoTrailingNewline covers the file-ends-
// without-a-newline case: nothing is glued because there is nothing after.
func TestReplaceBody_LineRange_LastLineNoTrailingNewline(t *testing.T) {
	src := "---\ntitle: Deal\n---\n\nLine A\nLine B"
	edit := ReplaceBodyByLineRange("x.md", 6, 6, "Replaced tail")
	out, err := edit([]byte(src))
	require.NoError(t, err)
	assert.Equal(t, "---\ntitle: Deal\n---\n\nLine A\nReplaced tail", string(out))
}

// ---------------------------------------------------------------------------
// ReplaceBody: the dispatcher FR-047's tool description actually calls.
// ---------------------------------------------------------------------------

func TestReplaceBody_Dispatcher_RequiresExactlyOneAddressingMode(t *testing.T) {
	t.Run("neither given", func(t *testing.T) {
		edit := ReplaceBody("x.md", "", nil, "New")
		_, err := edit([]byte(rbLineRangeFixture))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrReplaceBodySpec)
	})
	t.Run("both given", func(t *testing.T) {
		edit := ReplaceBody("x.md", "Line A", &LineRange{Start: 5, End: 5}, "New")
		_, err := edit([]byte(rbLineRangeFixture))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrReplaceBodySpec)
		assert.Contains(t, err.Error(), "not both")
	})
	t.Run("anchor alone delegates", func(t *testing.T) {
		edit := ReplaceBody("x.md", "Line A", nil, "Replaced A")
		out, err := edit([]byte(rbLineRangeFixture))
		require.NoError(t, err)
		assert.Contains(t, string(out), "Replaced A")
	})
	t.Run("line_range alone delegates", func(t *testing.T) {
		edit := ReplaceBody("x.md", "", &LineRange{Start: 5, End: 5}, "Replaced A\n")
		out, err := edit([]byte(rbLineRangeFixture))
		require.NoError(t, err)
		assert.Contains(t, string(out), "Replaced A")
	})
}

// ---------------------------------------------------------------------------
// findAnchorMatches: the non-overlapping-match convention, probed
// adversarially (Standing rule 2) rather than assumed.
// ---------------------------------------------------------------------------

func TestFindAnchorMatches_NonOverlappingConvention(t *testing.T) {
	got := findAnchorMatches([]byte("aaaa"), 0, []byte("aa"))
	assert.Equal(t, []int{0, 2}, got, "matches resume AFTER the previous match, matching strings.Count's convention")
}

// ---------------------------------------------------------------------------
// Full write-path integration: EditNote (real lock, real version check, real
// atomic write) — proves the primitive is wired to production concurrency
// control, not just correct in isolation.
// ---------------------------------------------------------------------------

// TestReplaceBody_EditNote_AmbiguousAnchorLeavesFileByteIdentical is AC-E3,
// through the actual write path a knowledge_edit tool would call.
func TestReplaceBody_EditNote_AmbiguousAnchorLeavesFileByteIdentical(t *testing.T) {
	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)
	abs := a1Note(t, root, "Deals/Acme.md", rbTwoMatchesFixture, 0o600)

	rec := &a1Recorder{}
	_, err := EditNote(OSLinkFS(), c, EditNoteRequest{
		RelPath:       "Deals/Acme.md",
		Edits:         []NoteEdit{ReplaceBodyByAnchor("Deals/Acme.md", "## Pricing", "## Pricing\n\nNew.\n")},
		ExpectVersion: NoteContentVersion([]byte(rbTwoMatchesFixture)),
		Audit:         rec,
		Actor:         AuthorActor{AgentID: "ava", WorkspaceID: "ws-1"},
	})
	require.Error(t, err)

	var ambig *AmbiguousAnchorError
	require.ErrorAs(t, err, &ambig)
	assert.Equal(t, []AnchorMatch{{Line: 5}, {Line: 13}}, ambig.Matches)

	onDisk, rerr := os.ReadFile(abs)
	require.NoError(t, rerr)
	assert.Equal(t, rbTwoMatchesFixture, string(onDisk),
		"AC-E3: an ambiguous replace_body anchor leaves the file byte-identical")

	require.Len(t, rec.records, 1, "the refusal must be audited")
	assert.Equal(t, AuthorOutcomeRefused, rec.last().Outcome)
}

// TestReplaceBody_EditNote_AnchorNotFoundLeavesFileUnchanged is the not-found
// twin of the test above, through the same real write path.
func TestReplaceBody_EditNote_AnchorNotFoundLeavesFileUnchanged(t *testing.T) {
	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)
	const src = "---\ntitle: Deal\n---\n\n## Pricing\n\nOld.\n"
	abs := a1Note(t, root, "Deals/Acme.md", src, 0o600)

	_, err := EditNote(OSLinkFS(), c, EditNoteRequest{
		RelPath:       "Deals/Acme.md",
		Edits:         []NoteEdit{ReplaceBodyByAnchor("Deals/Acme.md", "## Discount", "## Discount\n\nNew.\n")},
		ExpectVersion: NoteContentVersion([]byte(src)),
	})
	require.Error(t, err)
	var nf *AnchorNotFoundError
	require.ErrorAs(t, err, &nf)

	onDisk, rerr := os.ReadFile(abs)
	require.NoError(t, rerr)
	assert.Equal(t, src, string(onDisk))
}

// TestReplaceBody_EditNote_SingleMatchAppliesAndVersionRoundTrips is the
// positive control: without it, an implementation that refused every
// replace_body call (e.g. by treating every match as ambiguous) would pass
// every test above it.
func TestReplaceBody_EditNote_SingleMatchAppliesAndVersionRoundTrips(t *testing.T) {
	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)
	const src = "---\ntitle: Deal\n---\n\n## Pricing\n\nOld pricing.\n\n## Terms\n\nX.\n"
	abs := a1Note(t, root, "Deals/Acme.md", src, 0o600)

	res, err := EditNote(OSLinkFS(), c, EditNoteRequest{
		RelPath:       "Deals/Acme.md",
		Edits:         []NoteEdit{ReplaceBodyByAnchor("Deals/Acme.md", "Old pricing.", "New pricing: $500/mo.")},
		ExpectVersion: NoteContentVersion([]byte(src)),
	})
	require.NoError(t, err)
	assert.True(t, res.Changed)

	const want = "---\ntitle: Deal\n---\n\n## Pricing\n\nNew pricing: $500/mo.\n\n## Terms\n\nX.\n"
	onDisk, rerr := os.ReadFile(abs)
	require.NoError(t, rerr)
	assert.Equal(t, want, string(onDisk))
	assert.Equal(t, NoteContentVersion([]byte(want)), res.Version,
		"the returned token must describe what is on disk, matching the knowledge_read -> knowledge_edit "+
			"round trip AC-R1/AC-R2 promise")
}

// TestReplaceBody_EditNote_StaleVersionRefused proves replace_body composes
// with the SAME version-token concurrency control every other knowledge_edit
// operation uses (FR-043) — not a second, invented mechanism.
func TestReplaceBody_EditNote_StaleVersionRefused(t *testing.T) {
	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)
	const src = "---\ntitle: Deal\n---\n\n## Pricing\n\nOld.\n"
	abs := a1Note(t, root, "Deals/Acme.md", src, 0o600)
	staleToken := NoteContentVersion([]byte(src))

	const theirs = "---\ntitle: Deal\n---\n\n## Pricing\n\nWritten by another program.\n"
	require.NoError(t, os.WriteFile(abs, []byte(theirs), 0o600))

	_, err := EditNote(OSLinkFS(), c, EditNoteRequest{
		RelPath:       "Deals/Acme.md",
		Edits:         []NoteEdit{ReplaceBodyByAnchor("Deals/Acme.md", "## Pricing", "## Discount")},
		ExpectVersion: staleToken,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoteVersionStale)

	onDisk, rerr := os.ReadFile(abs)
	require.NoError(t, rerr)
	assert.Equal(t, theirs, string(onDisk), "a stale-token refusal must leave the file exactly as it was")
}
