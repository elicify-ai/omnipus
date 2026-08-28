// Tests for fielded indexing — ADR-068 D21.2 (fields) and D16.5 (the freshness
// stored field).
//
// Oracles come from the ADR, never from the implementation:
//
//	D21.2  title, name, headings, property keys, property values and body are
//	       DISTINCT fields; frontmatter is stripped from the body rather than
//	       flowing into it as prose. Exit criterion: "a field query on a
//	       property key is possible at all, which it is not today."
//	D16.5  the freshness token is the note's content SHA-256, stored on the
//	       bleve document and returned WITH the hit, so the comparison reads two
//	       values that both arrive with the hit. Empty for an attachment, which
//	       is unknown freshness and must be flagged rather than assumed fresh.
//	G1/G2  a mapping change must force a rebuild. bleve.OpenUsing takes no
//	       mapping argument, so an index built before this change would answer
//	       every field query with zero hits and NO ERROR.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/blevesearch/bleve/v2"
	bleveMapping "github.com/blevesearch/bleve/v2/mapping"
)

// d21Note is a note in the shape D21.2 is about: frontmatter carrying property
// keys and values, a title, headings, and prose that shares NO vocabulary with
// any of them. The disjointness is the point — every assertion below can name
// which field it came from.
const d21Note = `---
title: The Kessel Account
status: prospect
owner: analyst-04
tags:
  - quarterly
  - escalated
---

# The Kessel Account

The narrative body mentions parsecs and nothing else of interest.

## Renewal history

A second section, about smugglers.

` + "```" + `sh
# not-a-heading inside a fence
` + "```" + `
`

// d21Collection writes one fielded note plus a plain one and returns home/root.
func d21Collection(t *testing.T) (home, root string) {
	t.Helper()
	home, root = t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "kessel.md", d21Note)
	b2WriteFile(t, root, "plain.md", "# Plain\n\nA note with no frontmatter at all, mentioning parsecs.\n")
	return home, root
}

// d21Indexed opens, syncs and returns a ready index over d21Collection.
func d21Indexed(t *testing.T) *Index {
	t.Helper()
	home, root := d21Collection(t)
	ix := b2Open(t, home, root)
	b2Sync(t, ix)
	return ix
}

// d21FieldPaths runs a field query and returns the matched paths, sorted.
func d21FieldPaths(t *testing.T, ix *Index, field, term string) []string {
	t.Helper()
	hits, err := ix.SearchField(field, term, 10)
	if err != nil {
		t.Fatalf("SearchField(%q, %q): %v", field, term, err)
	}
	out := b2HitPaths(hits)
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// D21.2's exit criterion
// ---------------------------------------------------------------------------

// TestFieldQueryOnAPropertyKeyIsPossibleAtAll is the criterion stated in W2's
// exit row, word for word: "a field query on a property key is possible at all,
// which it is not today."
//
// The oracle is not "the search returns something". It is that asking for the
// KEY and asking for the WORD are different questions with different answers.
// Before fielded indexing they were the same question — `status` was a body
// token — so a test that only asserted "searching for status finds the note"
// would have passed against the code this change replaces.
func TestFieldQueryOnAPropertyKeyIsPossibleAtAll(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "declares.md", "---\nstatus: prospect\n---\n\nA note about parsecs.\n")
	// The decoy declares NO status. It merely talks about one, using the exact
	// word, in its prose. A body-token search cannot tell these two apart.
	b2WriteFile(t, root, "mentions.md", "# Mentions\n\nThe status of this account is a prospect, we think.\n")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	got := d21FieldPaths(t, ix, fieldPropKey, "status")
	want := []string{"declares.md"}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("field query on the property key `status` = %v, want %v — "+
			"a note that merely uses the word must not answer a question about declared properties", got, want)
	}

	// The control, which is what makes the assertion above mean anything: a
	// plain full-text search DOES return both, because both contain the word.
	// If this stopped being true the test above would be passing for the wrong
	// reason (an index that returns nothing at all).
	free, err := ix.Search("status", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(free) != 2 {
		t.Fatalf("free-text search for \"status\" returned %d notes, want 2 — "+
			"the field query's selectivity is only meaningful against a search that finds both", len(free))
	}
}

// TestFieldQueryOnAPropertyPair distinguishes notes by property VALUE, which is
// the other half of "possible at all": knowing a note has a status is less
// useful than knowing which one.
func TestFieldQueryOnAPropertyPair(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "a.md", "---\nstatus: prospect\n---\n\nfirst\n")
	b2WriteFile(t, root, "b.md", "---\nstatus: churned\n---\n\nsecond\n")
	b2WriteFile(t, root, "c.md", "---\nstatus: prospect\n---\n\nthird\n")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	got := d21FieldPaths(t, ix, fieldProp, "status=prospect")
	want := []string{"a.md", "c.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("field query prop=status=prospect = %v, want %v", got, want)
	}

	// Case folding is applied at BOTH ends by the same function, so a query
	// spelled differently from the file still lands.
	if got := d21FieldPaths(t, ix, fieldProp, "STATUS=Prospect"); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("case-folded field query = %v, want %v", got, want)
	}
}

// TestSearchFieldRejectsAnUnknownFieldRatherThanReturningNothing pins the
// difference between "no note matches" and "you asked for a field that does not
// exist". bleve answers both with zero hits.
func TestSearchFieldRejectsAnUnknownFieldRatherThanReturningNothing(t *testing.T) {
	ix := d21Indexed(t)

	if _, err := ix.SearchField("stauts", "prospect", 10); err == nil {
		t.Fatal("a query against a misspelled field returned no error — " +
			"an empty result is indistinguishable from `no note matches`")
	}
	// source_hash is stored and not indexed: querying it can never match, so it
	// must be refused rather than silently answered with nothing.
	if _, err := ix.SearchField(fieldSourceHash, "abc", 10); err == nil {
		t.Error("a query against the stored-only source_hash field returned no error")
	}
	if _, err := ix.SearchField(fieldPropKey, "   ", 10); err == nil {
		t.Error("a field query with an empty term returned no error")
	}
}

// ---------------------------------------------------------------------------
// Frontmatter no longer flows into the body
// ---------------------------------------------------------------------------

// TestFrontmatterIsNotIndexedAsBodyProse is D21.2's stated defect, asserted
// directly. `owner: analyst-04` used to put the loose tokens "owner" and
// "analyst-04" in the body text.
func TestFrontmatterIsNotIndexedAsBodyProse(t *testing.T) {
	ix := d21Indexed(t)

	for _, term := range []string{"owner", "quarterly", "escalated", "prospect"} {
		if got := d21FieldPaths(t, ix, fieldBody, term); len(got) != 0 {
			t.Errorf("body field contains the frontmatter term %q (matched %v) — "+
				"frontmatter must not be analysed as prose", term, got)
		}
	}

	// The control: the note's actual prose IS in the body field. Without this,
	// the assertions above would pass against an index with no body field at
	// all, or against a note that failed to index.
	if got := d21FieldPaths(t, ix, fieldBody, "parsecs"); len(got) == 0 {
		t.Fatal("body field does not contain the note's own prose — the test above proves nothing")
	}
}

// TestFrontmatterTermsAreStillFindable is the other side of stripping, and it
// is what stops the strip from being a data loss. The terms left the body; they
// did not leave the index.
func TestFrontmatterTermsAreStillFindable(t *testing.T) {
	ix := d21Indexed(t)

	if got := d21FieldPaths(t, ix, fieldPropValue, "quarterly"); len(got) != 1 {
		t.Errorf("prop_value field query for a sequence element = %v, want kessel.md", got)
	}
	if got := d21FieldPaths(t, ix, fieldPropKey, "owner"); len(got) != 1 {
		t.Errorf("prop_key field query for `owner` = %v, want kessel.md", got)
	}
	// And through the front door, which is what a person actually types.
	hits, err := ix.Search("escalated", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "kessel.md" {
		t.Errorf("free-text search for a frontmatter value = %v, want kessel.md", b2HitPaths(hits))
	}
}

// TestStrippedFrontmatterMovesTheOffset is FR-050a's half of the strip. The
// excerpt re-read starts at IndexHit.Offset, so an offset that still points at
// byte 0 would render the note's YAML as its excerpt.
func TestStrippedFrontmatterMovesTheOffset(t *testing.T) {
	home, root := d21Collection(t)
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	hits, err := ix.Search("parsecs", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	var hit IndexHit
	for _, h := range hits {
		if h.Path == "kessel.md" {
			hit = h
		}
	}
	if hit.Path == "" {
		t.Fatalf("kessel.md not found among %v", b2HitPaths(hits))
	}

	data, err := os.ReadFile(filepath.Join(root, "kessel.md"))
	if err != nil {
		t.Fatal(err)
	}
	if hit.Offset <= 0 {
		t.Fatalf("offset = %d; a note whose frontmatter was stripped must start after it", hit.Offset)
	}
	if int(hit.Offset) > len(data) {
		t.Fatalf("offset %d is past the end of the %d-byte file", hit.Offset, len(data))
	}
	// The oracle is the file's own bytes: the excerpt must begin at the first
	// byte of prose. Deriving the expected offset from the parser would be
	// reading the answer off the implementation.
	rest := string(data[hit.Offset:])
	if !strings.HasPrefix(strings.TrimLeft(rest, "\r\n"), "# The Kessel Account") {
		t.Errorf("re-reading at offset %d gives %.40q, want the note's first line of prose",
			hit.Offset, rest)
	}
	if strings.Contains(rest, "status: prospect") {
		t.Error("the excerpt re-read still lands inside the frontmatter block")
	}
}

// ---------------------------------------------------------------------------
// Title and headings
// ---------------------------------------------------------------------------

// TestTitleFieldPrefersFrontmatterThenHeadingThenStem pins the ORDER, which is
// the part a reimplementation gets wrong.
func TestTitleFieldPrefersFrontmatterThenHeadingThenStem(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "fm.md", "---\ntitle: Declared Title\n---\n\n# Heading Title\n\nbody\n")
	b2WriteFile(t, root, "h1.md", "# Heading Title\n\nbody\n")
	b2WriteFile(t, root, "stem-title.md", "no frontmatter, no heading\n")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	cases := []struct {
		term string
		want string
	}{
		{"declared", "fm.md"},
		{"heading", "h1.md"},
		{"stem", "stem-title.md"},
	}
	for _, tc := range cases {
		got := d21FieldPaths(t, ix, fieldTitle, tc.term)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("title field query %q = %v, want [%s]", tc.term, got, tc.want)
		}
	}

	// fm.md declares a title AND has an H1. The frontmatter wins, so the H1's
	// words must NOT be in its title field — otherwise the order is unpinned
	// and both sources are being concatenated.
	if got := d21FieldPaths(t, ix, fieldTitle, "heading"); len(got) == 1 && got[0] == "fm.md" {
		t.Error("the frontmatter title did not take precedence over the H1")
	}
}

// TestHeadingsFieldExcludesFencedCodeComments proves the heading extraction is
// the package's own scanner and not a second `#`-prefix rule: a "# comment"
// inside a shell fence is not a heading.
func TestHeadingsFieldExcludesFencedCodeComments(t *testing.T) {
	ix := d21Indexed(t)

	if got := d21FieldPaths(t, ix, fieldHeadings, "renewal"); len(got) != 1 {
		t.Errorf("headings field query for a real section heading = %v, want kessel.md", got)
	}
	if got := d21FieldPaths(t, ix, fieldHeadings, "fence"); len(got) != 0 {
		t.Errorf("a `#` comment inside a code fence was indexed as a heading (matched %v)", got)
	}
}

// ---------------------------------------------------------------------------
// D16.5 — the freshness stored field
// ---------------------------------------------------------------------------

// TestSourceHashRidesOnTheHitAndIsTheContentHash is D16.5's mechanism. The
// oracle is the manifest's own recorded hash, which the ADR names as the token:
// "the note's content SHA-256 — ManifestEntry.Hash".
func TestSourceHashRidesOnTheHitAndIsTheContentHash(t *testing.T) {
	home, root := d21Collection(t)
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	hits, err := ix.Search("parsecs", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}

	manifest, err := LoadManifest(ix.ManifestPath(), ix.Root())
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	for _, h := range hits {
		rec, ok := manifest.Get(h.Path)
		if !ok {
			t.Fatalf("%s has no manifest entry", h.Path)
		}
		if h.SourceHash == "" {
			t.Errorf("%s: the hit carries no source hash — D16.5's comparison has nothing to compare", h.Path)
			continue
		}
		if h.SourceHash != rec.Hash {
			t.Errorf("%s: hit source hash %q, manifest hash %q — the two indexes would be reported as "+
				"disagreeing about a note nobody touched", h.Path, h.SourceHash, rec.Hash)
		}
	}
}

// TestSourceHashChangesWithTheNoteAndNotWithTheIndex is what makes the token a
// FRESHNESS token rather than a document id: it must move when the bytes move,
// and stay put when they do not.
func TestSourceHashChangesWithTheNoteAndNotWithTheIndex(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "n.md", "---\nstatus: prospect\n---\n\nparsecs before\n")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	first := d21OneHash(t, ix, "parsecs")

	// A second reconcile that changes nothing must not change the token.
	b2Sync(t, ix)
	if again := d21OneHash(t, ix, "parsecs"); again != first {
		t.Errorf("source hash changed across a no-op reconcile: %q then %q", first, again)
	}

	b2WriteFile(t, root, "n.md", "---\nstatus: churned\n---\n\nparsecs after\n")
	if _, err := ix.SyncWith(context.Background(), SyncOptions{Deep: true}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if after := d21OneHash(t, ix, "parsecs"); after == first {
		t.Errorf("source hash %q survived a content change — a stale record would report itself fresh", after)
	}
}

// d21OneHash searches for term and returns the single hit's source hash.
func d21OneHash(t *testing.T, ix *Index, term string) string {
	t.Helper()
	hits, err := ix.Search(term, 10)
	if err != nil {
		t.Fatalf("Search(%q): %v", term, err)
	}
	if len(hits) != 1 {
		t.Fatalf("Search(%q) returned %d hits, want 1", term, len(hits))
	}
	return hits[0].SourceHash
}

// TestAttachmentSourceHashIsEmptyBecauseItIsNeverRead pins D16.5's attachment
// rule against the tidy-minded fix. FR-039a forbids opening an attachment, and
// hashing is opening, so unknown freshness is the CORRECT answer here.
func TestAttachmentSourceHashIsEmptyBecauseItIsNeverRead(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "img/diagram-v3.png", "\x89PNG not really")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	hits, err := ix.Search("diagram-v3", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("attachment not findable by name: %v", b2HitPaths(hits))
	}
	if hits[0].Kind != ScanKindAttachment {
		t.Fatalf("kind = %q, want attachment", hits[0].Kind)
	}
	if hits[0].SourceHash != "" {
		t.Errorf("attachment carries source hash %q — hashing it would mean opening it, which FR-039a forbids",
			hits[0].SourceHash)
	}
}

// ---------------------------------------------------------------------------
// The rebuild guard, verified against THIS change
// ---------------------------------------------------------------------------

// preFieldedMapping is the mapping this package shipped BEFORE D21.2 —
// path, name, kind, offset, body and nothing else.
//
// It is written out in full rather than derived from buildIndexMapping by
// deletion, because a derived version would track future edits to the current
// mapping and stop being the historical one. This is what is actually on disk
// in an installation that indexed before the change.
func preFieldedMapping() *bleveMapping.IndexMappingImpl {
	m := bleve.NewIndexMapping()

	body := bleve.NewTextFieldMapping()
	body.Analyzer = "en"
	body.Store, body.IncludeTermVectors, body.IncludeInAll, body.DocValues = false, false, false, false

	name := bleve.NewTextFieldMapping()
	name.Analyzer = "en"
	name.Store, name.IncludeTermVectors, name.IncludeInAll, name.DocValues = false, false, false, false

	pathField := bleve.NewTextFieldMapping()
	pathField.Analyzer = "keyword"
	pathField.Store = true
	pathField.IncludeTermVectors, pathField.IncludeInAll, pathField.DocValues = false, false, false

	kind := bleve.NewTextFieldMapping()
	kind.Analyzer = "keyword"
	kind.Store = true
	kind.IncludeTermVectors, kind.IncludeInAll, kind.DocValues = false, false, false

	offset := bleve.NewNumericFieldMapping()
	offset.Store, offset.Index, offset.IncludeInAll, offset.DocValues = true, false, false, false

	doc := bleve.NewDocumentMapping()
	doc.AddFieldMappingsAt(fieldPath, pathField)
	doc.AddFieldMappingsAt(fieldName, name)
	doc.AddFieldMappingsAt(fieldKind, kind)
	doc.AddFieldMappingsAt(fieldOffset, offset)
	doc.AddFieldMappingsAt(fieldBody, body)
	doc.Dynamic = false

	m.DefaultMapping = doc
	m.IndexDynamic = false
	m.StoreDynamic = false
	return m
}

// TestPreFieldedMappingIsDetectedAsDrift is the check the coordinator asked for
// by name, and it is checked rather than assumed for a specific reason: agent
// 1 of this stage found that mappingDrift does NOT compare every setting a
// mapping carries, so their own change would have been silently inert on every
// existing index.
//
// If a new FIELD were likewise uncompared, the failure would be the worst shape
// in ADR-068 §1.3: the index opens, the code issues a query against `prop_key`,
// the persisted mapping has no such field, and bleve answers with zero hits and
// no error.
func TestPreFieldedMappingIsDetectedAsDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bleve")
	w0BuildIndexWithMapping(t, path, preFieldedMapping())

	idx, err := bleve.OpenUsing(path, map[string]any{"bolt_timeout": boltOpenTimeout})
	if err != nil {
		t.Fatalf("OpenUsing: %v", err)
	}
	defer func() {
		if cErr := idx.Close(); cErr != nil {
			t.Errorf("Close: %v", cErr)
		}
	}()

	drift := mappingDrift(idx.Mapping())
	if drift == "" {
		t.Fatal("mappingDrift found nothing wrong with the pre-D21.2 five-field mapping — " +
			"an index built before fielding would be opened as-is, and every field query against it " +
			"would return zero hits and no error")
	}
	// Naming the field is what makes the report actionable, and it also proves
	// the drift was found for the right reason rather than incidentally.
	named := false
	for _, f := range []string{fieldTitle, fieldHeadings, fieldPropKey, fieldPropValue, fieldProp, fieldSourceHash} {
		if strings.Contains(drift, `"`+f+`"`) {
			named = true
		}
	}
	if !named {
		t.Errorf("drift report %q names none of the new fields", drift)
	}
}

// TestPreFieldedIndexIsRebuiltNotOpened is the end-to-end half: the guard being
// able to SEE the drift is worth nothing unless OpenIndex acts on it.
//
// It stamps the CURRENT format version beside the old index on purpose, which
// takes guard G1 out of the picture entirely. G1 depends on a human remembering
// to bump indexFormatVersion; this asserts the change is caught even by the
// guard that depends on nobody remembering anything.
func TestPreFieldedIndexIsRebuiltNotOpened(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "kessel.md", d21Note)

	dir, err := IndexDirFor(home, root)
	if err != nil {
		t.Fatalf("IndexDirFor: %v", err)
	}
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		t.Fatal(mkErr)
	}
	w0BuildIndexWithMapping(t, filepath.Join(dir, indexBleveSubdir), preFieldedMapping())
	w0WriteFormat(t, dir, indexFormatVersion)

	ix := b2Open(t, home, root)
	if reason := ix.RebuildReason(); reason == "" {
		t.Fatal("an index built with the pre-D21.2 mapping was opened as it stood — " +
			"field queries against it return zero hits and no error")
	} else if !strings.Contains(reason, "different document mapping") {
		t.Errorf("rebuild reason = %q, want it to name the mapping", reason)
	}

	// And the rebuilt index answers the field query the old one could not.
	b2Sync(t, ix)
	if got := d21FieldPaths(t, ix, fieldPropKey, "status"); len(got) != 1 {
		t.Errorf("after the rebuild, a field query on a property key = %v, want [kessel.md]", got)
	}
}

// ---------------------------------------------------------------------------
// Segmentation: which fields are per-note and which are per-segment
// ---------------------------------------------------------------------------

// TestNoteLevelFieldsAreOnEverySegment. Search collapses a note's segments to
// its BEST one, so a title that lived only on segment 0 would be invisible to a
// query matching segment 3 — and the collapsed hit would be ranked as though
// the note had no title.
func TestNoteLevelFieldsAreOnEverySegment(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()

	// A note comfortably over IndexSegmentSize, whose rare term lives only in
	// the LAST segment. Lines are short so the segment cuts land on boundaries.
	var b strings.Builder
	b.WriteString("---\ntitle: The Long Ledger\nstatus: prospect\n---\n\n# The Long Ledger\n\n")
	for b.Len() < IndexSegmentSize*2 {
		b.WriteString("filler prose about ordinary matters and nothing else at all\n")
	}
	b.WriteString("\nthe rare terminal marker klaatu appears only here\n")
	b2WriteFile(t, root, "long.md", b.String())

	ix := b2Open(t, home, root)
	stats := b2Sync(t, ix)
	if stats.Segments < 3 {
		t.Fatalf("the note produced %d segments, want at least 3 — the test needs a late segment", stats.Segments)
	}

	hits, err := ix.Search("klaatu", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("Search(klaatu) = %v, want one hit", b2HitPaths(hits))
	}
	if hits[0].Segment == 0 {
		t.Fatalf("the marker matched segment 0; the test cannot distinguish per-note from per-segment")
	}
	if hits[0].SourceHash == "" {
		t.Error("a hit on a late segment carries no source hash — D16.5 would report unknown freshness " +
			"for a note that is perfectly fresh")
	}

	// The note-level fields answer for the whole note however it was segmented.
	if got := d21FieldPaths(t, ix, fieldTitle, "ledger"); len(got) != 1 {
		t.Errorf("title field query over a segmented note = %v, want [long.md]", got)
	}
	if got := d21FieldPaths(t, ix, fieldPropKey, "status"); len(got) != 1 {
		t.Errorf("prop_key field query over a segmented note = %v, want [long.md]", got)
	}
}

// TestEmptyNoteStillCarriesTitleAndHash. An empty note is still a note; it must
// be addressable, and its freshness must be KNOWN rather than unknown.
func TestEmptyNoteStillCarriesTitleAndHash(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "hollow-vessel.md", "")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	hits, err := ix.Search("hollow-vessel", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("an empty note is not findable by name: %v", b2HitPaths(hits))
	}
	if hits[0].SourceHash == "" {
		t.Error("an empty note carries no source hash; its freshness is knowable and must be known")
	}
	if got := d21FieldPaths(t, ix, fieldTitle, "hollow-vessel"); len(got) != 1 {
		t.Errorf("title field query on an empty note = %v, want [hollow-vessel.md]", got)
	}
}

// ---------------------------------------------------------------------------
// Malformed input never removes a note from the index
// ---------------------------------------------------------------------------

// TestUnparseableFrontmatterStillIndexesTheNote. Refusing to index a note
// because its YAML is malformed would remove it from search entirely, which is
// a far worse answer than indexing its text and reporting no properties.
func TestUnparseableFrontmatterStillIndexesTheNote(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "broken.md", "---\n\tthis: [is not, valid: yaml\n---\n\n# Broken\n\nparsecs\n")
	b2WriteFile(t, root, "unterminated.md", "---\ntitle: Never Closed\nstatus: prospect\n\n# Body\n\nparsecs\n")
	ix := b2Open(t, home, root)
	stats := b2Sync(t, ix)

	if stats.Indexed != 2 {
		t.Fatalf("indexed %d notes, want 2 — malformed frontmatter must not remove a note from the index", stats.Indexed)
	}
	hits, err := ix.Search("parsecs", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Errorf("prose search over malformed notes = %v, want both", b2HitPaths(hits))
	}
	// The unterminated block has no closing fence, so nothing is stripped and
	// no property is claimed. The literal `title:` line is still recovered,
	// which is what the query-time title derivation has always done.
	if got := d21FieldPaths(t, ix, fieldTitle, "closed"); len(got) != 1 {
		t.Errorf("title of a note with an unterminated frontmatter block = %v, want [unterminated.md]", got)
	}
	if got := d21FieldPaths(t, ix, fieldPropKey, "status"); len(got) != 0 {
		t.Errorf("a property was claimed from an unterminated frontmatter block (matched %v) — "+
			"the block's extent is unknown, so its contents are not properties", got)
	}
}

// TestEmptyPropertyValueIsNotTheTextNull. `status:` with no value is not the
// value "null"; indexing it as one would make a search for "null" match every
// note with an empty property.
func TestEmptyPropertyValueIsNotTheTextNull(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "n.md", "---\nstatus:\nowner: null\n---\n\nparsecs\n")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	if got := d21FieldPaths(t, ix, fieldPropValue, "null"); len(got) != 0 {
		t.Errorf("an empty property value was indexed as the text \"null\" (matched %v)", got)
	}
	// The KEY is still declared, which is the distinction FR-007/R-3 keeps: a
	// key with no value is present and empty, not absent.
	if got := d21FieldPaths(t, ix, fieldPropKey, "status"); len(got) != 1 {
		t.Errorf("an empty property's key was not indexed (matched %v)", got)
	}
}
