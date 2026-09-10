// Omnipus — tests for EMB-021a (ADR-083 spec revision 3; founder decision
// D-B): knowledge_read's own honesty cross-check against the walk's skip
// list.
//
// EMB-021 already corrected the READER surface (KnowledgeNoteView.tsx's
// resolveEmbedAgainstGraph / findSkipForTarget): a link whose target sits
// under a directory the walk could not list (the dominant walk-level shape
// — WalkContained records an unreadable directory under ITS OWN path, so
// every file beneath it never enters the index at all) must be reported as
// "could not be checked", never as an ordinary "no_match" — because
// "no_match" is a claim that the collection was actually searched, and it
// was not.
//
// Revision 2 shipped that fix on the reader only. The AGENT surface —
// knowledge_read, via toReadLinks — never consulted the skip list at all:
// ReadLink had ten fields and no place for a skip-derived reason, and
// renderReadLinks printed "(unresolved) … no_match" for a walk-skipped
// target exactly as it did for a genuinely absent one. An agent summarising
// a note therefore told the operator a file was missing when the walk had
// simply never been able to look. This file is the agent-surface half of
// the SAME guarantee (spec EMB-021a).
//
// adr-083-embedded-content-spec.md test 120's own pairing requirement is
// honoured directly: every positive assertion below (the skipped target
// reads as could-not-be-checked) is paired with a negative one in the SAME
// response (an ordinary miss elsewhere in the note still reads as absent) —
// proving the fix flags the ONE link the skip explains, not every
// unresolved link in the answer.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run 'ReadLink|KnowledgeRead|Skip' -p 1 ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Unit level — findSkipForLinkTarget's three clauses, mirroring
// KnowledgeNoteView.test.tsx's `findSkipForTarget (unit, ADR-083 EMB-021)`
// describe block so the two implementations are provably testing the same
// rule.
// ---------------------------------------------------------------------------

func TestFindSkipForLinkTarget_PathEquality(t *testing.T) {
	skipped := []SkippedEntry{{RelPath: "notes/private/plan.md", Reason: SkipUnreadable}}
	found, ok := findSkipForLinkTarget(skipped, "notes/private/plan.md")
	require.True(t, ok)
	assert.Equal(t, SkipUnreadable, found.Reason)
}

func TestFindSkipForLinkTarget_BasenameEqualityWithAndWithoutMarkdownExt(t *testing.T) {
	_, ok := findSkipForLinkTarget([]SkippedEntry{{RelPath: "notes/private/plan.md"}}, "plan")
	assert.True(t, ok, "a bare basename must match a skip's own file basename")
	_, ok = findSkipForLinkTarget([]SkippedEntry{{RelPath: "notes/private/plan"}}, "plan.md")
	assert.True(t, ok, "the markdown extension must not block the match in either direction")
}

func TestFindSkipForLinkTarget_AncestorPrefixIsTheDominantWalkLevelShape(t *testing.T) {
	// WalkContained records an unreadable DIRECTORY under its own path, and
	// every file beneath it never enters walk.Files at all — clauses 1 and
	// 2 both miss ("notes/private" != "notes/private/plan.md"; "private" !=
	// "plan.md"), so only the ancestor-prefix clause can catch this, the
	// shape EMB-021's own rationale names as dominant.
	skipped := []SkippedEntry{{RelPath: "notes/private", Reason: SkipUnreadable}}
	found, ok := findSkipForLinkTarget(skipped, "notes/private/plan.md")
	require.True(t, ok)
	assert.Equal(t, SkipUnreadable, found.Reason)
}

func TestFindSkipForLinkTarget_NearMissPrefixDoesNotSuppress(t *testing.T) {
	// "notes/priv" is a STRING prefix of "notes/private/plan.md" but not a
	// path-SEGMENT prefix — the boundary is required.
	_, ok := findSkipForLinkTarget([]SkippedEntry{{RelPath: "notes/priv"}}, "notes/private/plan.md")
	assert.False(t, ok)
	// A skip naming a sibling directory must not suppress an unrelated one.
	_, ok = findSkipForLinkTarget([]SkippedEntry{{RelPath: "notes/public"}}, "notes/private/plan.md")
	assert.False(t, ok)
}

func TestFindSkipForLinkTarget_BasenameIsEqualityNotPrefix(t *testing.T) {
	// A skip named "plan.md" must not wrongly suppress "plan-extended.md" —
	// clause 2 is an equality check, not a substring/prefix check.
	_, ok := findSkipForLinkTarget([]SkippedEntry{{RelPath: "notes/plan.md"}}, "notes/plan-extended.md")
	assert.False(t, ok)
}

func TestFindSkipForLinkTarget_NoSkipsIsNoMatch(t *testing.T) {
	_, ok := findSkipForLinkTarget(nil, "notes/plan.md")
	assert.False(t, ok)
	_, ok = findSkipForLinkTarget([]SkippedEntry{{RelPath: "unrelated/dir"}}, "notes/plan.md")
	assert.False(t, ok)
}

// ---------------------------------------------------------------------------
// RenderRead level — the ReadLink struct in hand, no filesystem: proves the
// RENDERING half (renderReadLinks) reads SkipReason ahead of the ordinary
// unresolved branch, independent of whatever produced the struct.
// ---------------------------------------------------------------------------

func TestRenderRead_SkipReasonRendersAsCouldNotBeCheckedNeverAsUnresolved(t *testing.T) {
	d := ReadData{
		Path:     "notes/reader.md",
		Version:  "v1:deadbeef",
		Included: map[string]bool{ReadIncludeLinks: true},
		Links: []ReadLink{
			// A skip-explained target — must read as could-not-be-checked,
			// carry the skip's own reason, and NEVER the ordinary
			// UnresolvedReason ("no_match") also set on the same struct.
			{Form: "![[notes/private/plan.md]]", Embed: true, Resolved: false,
				Reason: string(ReasonNoMatch), SkipReason: "a file or folder Omnipus could not read (permission denied)", Line: 3},
			// PAIRED NEGATIVE: an ordinary unresolved link with no
			// SkipReason must render exactly as it always has.
			{Form: "[[Nowhere]]", Resolved: false, Reason: string(ReasonNoMatch), Line: 9},
		},
	}
	out := RenderRead(d)
	lines := strings.Split(out, "\n")
	lineFor := func(needle string) string {
		for _, l := range lines {
			if strings.Contains(l, needle) {
				return l
			}
		}
		t.Fatalf("no rendered line contains %q; full output:\n%s", needle, out)
		return ""
	}

	skipLine := lineFor("notes/private/plan.md")
	assert.Contains(t, skipLine, "could not be checked embed")
	assert.Contains(t, skipLine, "a file or folder Omnipus could not read (permission denied)")
	assert.NotContains(t, skipLine, "no_match", "the skip-derived reason must win over the raw UnresolvedReason: %q", skipLine)
	assert.NotContains(t, skipLine, "(unresolved", "must not ALSO render the ordinary unresolved tag: %q", skipLine)

	ordinaryLine := lineFor("[[Nowhere]]")
	assert.Contains(t, ordinaryLine, "(unresolved) [[Nowhere]]")
	assert.Contains(t, ordinaryLine, string(ReasonNoMatch))
	assert.NotContains(t, ordinaryLine, "could not be checked", "a link with no SkipReason must never render the skip tag: %q", ordinaryLine)
}

// ---------------------------------------------------------------------------
// Full integration — through ReadTool.Execute's real rendered output, a
// real vault on disk, a real unreadable directory. Not the struct: the
// whole read path (BuildLinkGraph → toReadLinks → RenderRead) end to end.
// ---------------------------------------------------------------------------

// TestReadTool_LinkIntoUnreadableDirectoryIsCouldNotBeCheckedNotNoMatch is
// EMB-021a's own scenario: `notes/private/` cannot be listed, so
// `notes/private/plan.md` never enters the walk, yet a link naming it
// full-path still produces a matching UNRESOLVED edge (EMB-021's own
// rationale). The rendered agent-surface response must say the target could
// not be checked, with the walk's own reason — never that nothing carries
// that name.
func TestReadTool_LinkIntoUnreadableDirectoryIsCouldNotBeCheckedNotNoMatch(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("runs as root: 0000 dir perms do not block reads, so the walk would list the directory successfully")
	}
	root := t.TempDir()
	b5Note(t, root, "notes/private/plan.md", "# Plan\n\nSecret plan body.\n")
	b5Note(t, root, "notes/reader.md",
		"See ![[notes/private/plan.md]] for the plan, and [[notes/Ghost.md]] for nothing at all.\n")

	privateDir := filepath.Join(root, "notes", "private")
	require.NoError(t, os.Chmod(privateDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(privateDir, 0o700) }) // let TempDir cleanup remove it

	ctx, deps := readCtxAndDeps(t, root)
	res := NewReadTool(deps).Execute(ctx, map[string]any{"path": "notes/reader.md"})
	require.NotNil(t, res)
	require.False(t, res.IsError, "unexpected error: %s", resultText(res))
	out := resultText(res)
	t.Logf("rendered knowledge_read response:\n%s", out)

	lines := strings.Split(out, "\n")
	// Scoped to the LINKS/BACKLINKS section's own rendered lines (the "  ->
	// "/"  <- " prefix renderReadLinks emits) — the note's BODY is echoed
	// verbatim just above and contains the identical wikilink substrings,
	// so an unscoped Contains(out, …) would find the raw markdown source
	// rather than the rendered link line it is meant to check.
	lineFor := func(needle string) string {
		for _, l := range lines {
			if !strings.HasPrefix(l, "  ->") && !strings.HasPrefix(l, "  <-") {
				continue
			}
			if strings.Contains(l, needle) {
				return l
			}
		}
		t.Fatalf("no rendered LINKS/BACKLINKS line contains %q; full output:\n%s", needle, out)
		return ""
	}

	// The link under the unlistable directory: EMB-021a's fix.
	skippedLine := lineFor("notes/private/plan.md")
	assert.Contains(t, skippedLine, "could not be checked",
		"a target under an unreadable directory must read as unverified, not absent: %q", skippedLine)
	assert.Contains(t, skippedLine, "embed", "the embed marker (EMB-020) must still be present: %q", skippedLine)
	assert.NotContains(t, skippedLine, "no_match",
		"must never assert the walk's own no_match reason for a target it never actually looked at: %q", skippedLine)
	assert.NotContains(t, skippedLine, "(unresolved", "must not render as plainly unresolved: %q", skippedLine)

	// PAIRED NEGATIVE (test 120's own pairing requirement, and this file's
	// no-over-fire proof): an ORDINARY unresolved link elsewhere in the
	// SAME note, under no skip at all, must still read as absent — proving
	// the fix flags only the ONE link the skip actually explains.
	ghostLine := lineFor("notes/Ghost.md")
	assert.Contains(t, ghostLine, "(unresolved)", "an ordinary miss must still read as unresolved: %q", ghostLine)
	assert.Contains(t, ghostLine, "no_match", "an ordinary miss must still carry its real reason: %q", ghostLine)
	assert.NotContains(t, ghostLine, "could not be checked",
		"an ordinary miss must not be mistaken for a skip-explained one: %q", ghostLine)
}

// TestReadTool_AbsentLinkWithNoSkipsStillReportsNoMatch is the DoD's own
// control: a genuinely absent file, in a vault with NO unreadable
// directories or symlinks anywhere, must still report ordinary absence —
// proving the EMB-021a cross-check does not fire just because SOME
// unrelated skip might exist; here none exists at all.
func TestReadTool_AbsentLinkWithNoSkipsStillReportsNoMatch(t *testing.T) {
	root := t.TempDir()
	b5Note(t, root, "notes/reader.md", "[[notes/Ghost.md]] does not exist anywhere in this collection.\n")

	ctx, deps := readCtxAndDeps(t, root)
	res := NewReadTool(deps).Execute(ctx, map[string]any{"path": "notes/reader.md"})
	require.False(t, res.IsError, "unexpected error: %s", resultText(res))
	out := resultText(res)

	assert.Contains(t, out, "(unresolved) [[notes/Ghost.md]]")
	assert.Contains(t, out, string(ReasonNoMatch))
	assert.NotContains(t, out, "could not be checked",
		"a plain miss with no skip anywhere in the collection must never render the skip-explained marker")
}
