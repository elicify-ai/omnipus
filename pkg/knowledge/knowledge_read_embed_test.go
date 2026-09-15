// Omnipus — tests for knowledge_read's embed/view projection (EMB-020,
// founder decision D-B): the agent surface must mark an embed AS an embed,
// mark an unresolved embed as unresolved with its reason exactly as an
// ordinary unresolved link already does, and describe a saved data view as
// a view rather than as a heading — never marking an ORDINARY link at all.
//
// adr-083-embedded-content-spec.md test 92 (Step 1): "An agent surface that
// marks nothing, 'asserted' by the absence of wrong marks" is the false
// green this file's own name-checks — every positive assertion below is
// paired with a negative one proving the OTHER lines were not also marked.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKnowledgeRead_MarksEmbedsAndViewLabels is test 92: embeds marked, a
// data view rendered as a view (never a heading), ordinary links unmarked.
func TestKnowledgeRead_MarksEmbedsAndViewLabels(t *testing.T) {
	d := ReadData{
		Path:     "Dashboard.md",
		Version:  "v1:deadbeef",
		Included: map[string]bool{ReadIncludeLinks: true},
		Links: []ReadLink{
			// An ORDINARY link — must render exactly as it always has, with
			// no embed marker at all.
			{Form: "[[Notes/Other]]", To: "Notes/Other.md", Alias: "Note", Heading: "Intro",
				Embed: false, Resolved: true, Line: 3},
			// A resolved EMBED of a markdown note's heading — must be
			// marked "(embed)", and the fragment must still print as a
			// heading ("#Q3"), because the target is not a data file.
			{Form: "![[Plan#Q3]]", To: "Plan.md", Heading: "Q3",
				Embed: true, Resolved: true, Line: 7},
			// A resolved EMBED of a data file's saved view — must be
			// marked "(embed)", AND the fragment must print as a VIEW,
			// never as a heading ("#Needs Daniel" would misdescribe it).
			{Form: "![[Tasks.base#Needs Daniel]]", To: "Tasks.base", Heading: "Needs Daniel",
				Embed: true, Resolved: true, Line: 9},
			// An UNRESOLVED embed — must be marked "(unresolved embed)"
			// and still carry its reason, exactly as an ordinary
			// unresolved link already does.
			{Form: "![[Ghost.png]]", Embed: true, Resolved: false,
				Reason: string(ReasonNoMatch), Line: 11},
		},
	}
	out := RenderRead(d)
	require.NotEmpty(t, strings.TrimSpace(out), "RenderRead must not return an empty document")
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

	// (1) the ordinary link is COMPLETELY unmarked: no "(embed)" anywhere
	// on its line, and it still carries its heading and alias as before.
	ordinary := lineFor("Notes/Other.md")
	assert.NotContains(t, ordinary, "embed", "an ORDINARY link must not be marked as an embed: %q", ordinary)
	assert.Contains(t, ordinary, `"Note"`)
	assert.Contains(t, ordinary, "#Intro")

	// (2) the note-heading embed IS marked, and its fragment still reads
	// as a heading — proves the marker and the heading/view distinction
	// are independent, not the same flag doing double duty.
	noteEmbed := lineFor("Plan.md")
	assert.Contains(t, noteEmbed, "embed", "a resolved embed must be marked: %q", noteEmbed)
	assert.Contains(t, noteEmbed, "#Q3", "a NOTE target's fragment must still print as a heading: %q", noteEmbed)
	assert.NotContains(t, noteEmbed, "view ", "a note target must never be described as a view: %q", noteEmbed)

	// (3) the data-view embed IS marked, and its fragment reads as a VIEW
	// — the positive half a marker-only check would miss entirely.
	viewEmbed := lineFor("Tasks.base")
	assert.Contains(t, viewEmbed, "embed", "a resolved embed must be marked: %q", viewEmbed)
	assert.Contains(t, viewEmbed, `view "Needs Daniel"`,
		"a DATA FILE target's fragment must print as a view, not a heading: %q", viewEmbed)
	assert.NotContains(t, viewEmbed, "#Needs Daniel",
		"a saved view must never be printed with the heading marker '#': %q", viewEmbed)

	// (4) the unresolved embed carries BOTH the embed marker and its
	// reason — the same honesty EMB-020 already requires of an ordinary
	// unresolved link, now reachable together.
	broken := lineFor("Ghost.png")
	assert.Contains(t, broken, "unresolved embed", "an unresolved embed must be marked as such: %q", broken)
	assert.Contains(t, broken, string(ReasonNoMatch), "it must still carry its reason: %q", broken)
}

// TestKnowledgeRead_OrdinaryUnresolvedLinkStillUnmarked is the negative
// control test 92's own risk table names: "an agent surface that marks
// nothing" would pass a marker-only check on the unresolved case too, so an
// ORDINARY unresolved link (Embed: false) must render with NO embed marker
// at all — proving the tag is driven by the Embed field, not a constant.
func TestKnowledgeRead_OrdinaryUnresolvedLinkStillUnmarked(t *testing.T) {
	d := ReadData{
		Path:     "A.md",
		Version:  "v1:deadbeef",
		Included: map[string]bool{ReadIncludeLinks: true},
		Links: []ReadLink{
			{Form: "[[Nowhere]]", Resolved: false, Reason: string(ReasonNoMatch), Embed: false, Line: 5},
		},
	}
	out := RenderRead(d)
	assert.Contains(t, out, "(unresolved) [[Nowhere]]")
	assert.NotContains(t, out, "embed", "an ordinary unresolved link must not gain an embed marker: %s", out)
}
