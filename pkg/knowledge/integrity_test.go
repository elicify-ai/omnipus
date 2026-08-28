// Omnipus — tests for the bounded integrity sweep (FR-075, FR-075a, AC-D1..D6).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// Fixtures
//
// R-F: every record type, property and value below is a fixture THIS TEST
// declares. The product ships none of them, and what is asserted is the SHAPE
// and the remedy clause, never these particular words.
// ---------------------------------------------------------------------------

// fakePropertyIndex is a knowledge.PropertyIndexReader over in-memory rows.
//
// The sweep is tested against this rather than against SQLite ON PURPOSE: the
// questions the sweep asks are two, they are stated in the interface, and a
// database in the way would make every bound test a database test. The
// adapter over the real store is covered in pkg/vaultprops.
type fakePropertyIndex struct {
	records   []IndexedRecord
	relations []IndexedRelation
	// recordsErr and relationsErr let a test drive the failure paths.
	recordsErr   error
	relationsErr error
}

func (f *fakePropertyIndex) ScanRecords(_ context.Context, recordType string, visit func(IndexedRecord) error) error {
	if f.recordsErr != nil {
		return f.recordsErr
	}
	for _, r := range f.records {
		if recordType != "" && r.RecordType != recordType {
			continue
		}
		if err := visit(r); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakePropertyIndex) ScanRelations(_ context.Context, recordType string, visit func(IndexedRelation) error) error {
	if f.relationsErr != nil {
		return f.relationsErr
	}
	byPath := map[string]string{}
	for _, r := range f.records {
		byPath[r.Path] = r.RecordType
	}
	for _, e := range f.relations {
		if recordType != "" && byPath[e.Path] != recordType {
			continue
		}
		if err := visit(e); err != nil {
			return err
		}
	}
	return nil
}

// writeNote writes one note into a collection and returns the collection root.
func writeNote(t *testing.T, root, rel, body string) string {
	t.Helper()
	if root == "" {
		root = t.TempDir()
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return root
}

func mustCollectionRoot(t *testing.T, dir string) CollectionRoot {
	t.Helper()
	root, err := NewCollectionRoot(OSLinkFS(), dir)
	if err != nil {
		t.Fatalf("NewCollectionRoot(%s): %v", dir, err)
	}
	return root
}

// integrityFixtureSchemas declares one record type with a relation.
func integrityFixtureSchemas(t *testing.T, root string) *records.SchemaSet {
	t.Helper()
	dir := records.SchemaDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir schema dir: %v", err)
	}
	files := map[string]string{
		"widget.yaml": `
schema_version: 1
type: widget
identity:
  prefix: WI
properties:
  name:  { type: text }
  maker: { type: relation, to: foundry }
`,
		"foundry.yaml": `
schema_version: 1
type: foundry
properties:
  name: { type: text }
`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	set, report, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("fixture schemas did not load: %v", report.Rejections)
	}
	return set
}

// findingDetails flattens one category's rendered findings.
func findingDetails(r *IntegrityReport, cat IntegrityCategory) []string {
	c := r.Category(cat)
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Findings))
	for _, f := range c.Findings {
		out = append(out, f.Detail)
	}
	return out
}

// ---------------------------------------------------------------------------
// The bounds, pinned at their production values
// ---------------------------------------------------------------------------

// TestIntegrity_BoundsAreTheSpecifiedNumbers pins FR-075a's two bounds.
//
// Every other bound test in this file drives an OVERRIDE so it can build a
// corpus that fits in a test. This is what stops the override from being the
// only thing that was ever exercised: a bound reachable only through a test
// parameter is a bound that could be anything in production.
func TestIntegrity_BoundsAreTheSpecifiedNumbers(t *testing.T) {
	if IntegritySweepLimit != 100_000 {
		t.Errorf("FR-075a bounds the sweep at 100,000 notes; the constant is %d", IntegritySweepLimit)
	}
	if IntegrityFindingsPerCategory != 500 {
		t.Errorf("FR-075a clamps each category at 500 findings; the constant is %d", IntegrityFindingsPerCategory)
	}
}

// TestIntegrity_SweepLimitRefusesAtItsRealBoundary exercises the production
// constant at the boundary without building a 100,001-note corpus.
//
// checkSweepLimit is a pure function of a count precisely so this is possible:
// the alternative is a bound nobody ever tests at its real value, which is how
// a 100,000 becomes a 1,000 in a refactor and nothing notices.
func TestIntegrity_SweepLimitRefusesAtItsRealBoundary(t *testing.T) {
	if err := checkSweepLimit("archive", IntegritySweepLimit, IntegritySweepLimit); err != nil {
		t.Fatalf("exactly at the limit must be swept, not refused: %v", err)
	}
	err := checkSweepLimit("archive", IntegritySweepLimit+1, IntegritySweepLimit)
	if err == nil {
		t.Fatalf("one note above the limit must be refused")
	}
	msg := err.Error()
	for _, want := range []string{"archive", "100001", "100000", "record_type=", "narrower collection"} {
		// The numbers are rendered with separators in the response; the error
		// itself carries them plain. Accept either spelling of the digits.
		if strings.Contains(msg, want) || strings.Contains(msg, group(atoiOrZero(want))) {
			continue
		}
		t.Errorf("FR-075a's refusal must name the collection, the counts and the SCOPED remedy; "+
			"%q is missing from %q", want, msg)
	}
}

func atoiOrZero(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// TestIntegrity_SweepLimitIsCheckedBeforeTheGraphIsBuilt — AC-D4.
//
// A partial sweep must never be presented as whole, and the bound must fire
// BEFORE the expensive work rather than after it. The corpus here is tiny and
// the limit is lowered to meet it; the boundary against the production
// constant is the test above.
func TestIntegrity_SweepLimitIsCheckedBeforeTheGraphIsBuilt(t *testing.T) {
	root := ""
	for i := 0; i < 5; i++ {
		root = writeNote(t, root, fmt.Sprintf("n%d.md", i), "hello\n")
	}
	_, err := CheckIntegrity(context.Background(), IntegrityOptions{
		FS:             OSLinkFS(),
		Root:           mustCollectionRoot(t, root),
		CollectionName: "archive",
		SweepLimit:     4,
	})
	if err == nil {
		t.Fatalf("a collection above the sweep limit must be REFUSED, never partly swept")
	}
	var tooLarge *SweepTooLargeError
	if !asSweepTooLarge(err, &tooLarge) {
		t.Fatalf("expected a SweepTooLargeError, got %T: %v", err, err)
	}
	if tooLarge.Notes != 5 || tooLarge.Limit != 4 {
		t.Errorf("the refusal must carry the real counts, got notes=%d limit=%d", tooLarge.Notes, tooLarge.Limit)
	}
	if !strings.Contains(err.Error(), "archive") {
		t.Errorf("the refusal must name the collection: %q", err.Error())
	}
}

func asSweepTooLarge(err error, out **SweepTooLargeError) bool {
	if e, ok := err.(*SweepTooLargeError); ok {
		*out = e
		return true
	}
	return false
}

// TestIntegrity_PerCategoryClampReportsWhatItHid — FR-075a, over a corpus
// large enough to EXCEED the bound rather than a stubbed counter.
//
// 620 notes, each linking to a target that does not exist, and each linked to
// by nothing. That is 620 broken links and 620 orphans: both categories clamp,
// and both must say what they hid.
func TestIntegrity_PerCategoryClampReportsWhatItHid(t *testing.T) {
	const notes = 620
	const clamp = 500

	root := t.TempDir()
	for i := 0; i < notes; i++ {
		writeNote(t, root, fmt.Sprintf("n%04d.md", i), fmt.Sprintf("see [[missing-%04d]]\n", i))
	}

	report, err := CheckIntegrity(context.Background(), IntegrityOptions{
		FS:                  OSLinkFS(),
		Root:                mustCollectionRoot(t, root),
		CollectionName:      "big",
		FindingsPerCategory: clamp,
	})
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	if report.NotesSwept != notes {
		t.Fatalf("expected %d notes swept, got %d", notes, report.NotesSwept)
	}

	for _, cat := range []IntegrityCategory{CategoryBrokenLink, CategoryOrphan} {
		c := report.Category(cat)
		if c == nil {
			t.Fatalf("category %q is missing from the report entirely", cat)
		}
		if c.Total != notes {
			t.Errorf("%s: Total must be the PRE-clamp count so the clamp line can quote it; got %d, want %d",
				cat, c.Total, notes)
		}
		if len(c.Findings) != clamp {
			t.Errorf("%s: expected exactly %d findings retained, got %d", cat, clamp, len(c.Findings))
		}
		if !c.Clamped() {
			t.Errorf("%s: %d of %d retained and Clamped() says no", cat, len(c.Findings), c.Total)
		}
	}

	// And the clamp must be VISIBLE in the text a model reads. A bound that
	// clamps silently is a truncation.
	text := RenderDescribe(DescribeData{
		Collection: "big",
		Schemas:    records.NewSchemaSet(),
		Views:      records.NewViewSet(),
		Integrity:  report,
		Sections:   map[string]bool{},
	})
	for _, want := range []string{
		"broken link: showing 500 of 620",
		"orphan: showing 500 of 620",
		"narrow with collection=",
		"record_type=",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the clamp line must state what it hid and how to narrow; %q missing from:\n%s", want, text)
		}
	}
}

// ---------------------------------------------------------------------------
// The findings themselves
// ---------------------------------------------------------------------------

// TestIntegrity_DuplicateIdentifierNamesBothPaths — AC-D1, FR-039.
func TestIntegrity_DuplicateIdentifierNamesBothPaths(t *testing.T) {
	root := writeNote(t, "", "Widgets/Acme.md", "a\n")
	root = writeNote(t, root, "Widgets/Acme (old).md", "b\n")
	schemas := integrityFixtureSchemas(t, root)

	report, err := CheckIntegrity(context.Background(), IntegrityOptions{
		FS:      OSLinkFS(),
		Root:    mustCollectionRoot(t, root),
		Schemas: schemas,
		Store: &fakePropertyIndex{records: []IndexedRecord{
			{Path: "Widgets/Acme.md", RecordType: "widget", RecordID: "WI-0142"},
			{Path: "Widgets/Acme (old).md", RecordType: "widget", RecordID: "WI-0142"},
		}},
	})
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	details := findingDetails(report, CategoryDuplicateID)
	if len(details) != 1 {
		t.Fatalf("expected one duplicate-identifier finding, got %v", details)
	}
	for _, want := range []string{"WI-0142", "Widgets/Acme.md", "Widgets/Acme (old).md", "neither is preferred"} {
		if !strings.Contains(details[0], want) {
			t.Errorf("FR-039 requires BOTH paths and the statement that neither is preferred; "+
				"%q missing from %q", want, details[0])
		}
	}
}

// TestIntegrity_IdentifiersAreComparedByteExact — R-8. CO-0142 and co-0142 are
// two records, so a case difference is NOT a duplicate.
//
// MUTATION: fold the identifier into the grouping key in TypedIntegrity and
// this test fails.
func TestIntegrity_IdentifiersAreComparedByteExact(t *testing.T) {
	root := writeNote(t, "", "a.md", "a\n")
	root = writeNote(t, root, "b.md", "b\n")
	schemas := integrityFixtureSchemas(t, root)

	report, err := CheckIntegrity(context.Background(), IntegrityOptions{
		FS:      OSLinkFS(),
		Root:    mustCollectionRoot(t, root),
		Schemas: schemas,
		Store: &fakePropertyIndex{records: []IndexedRecord{
			{Path: "a.md", RecordType: "widget", RecordID: "WI-0142"},
			{Path: "b.md", RecordType: "widget", RecordID: "wi-0142"},
		}},
	})
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	if got := findingDetails(report, CategoryDuplicateID); len(got) != 0 {
		t.Fatalf("R-8 makes WI-0142 and wi-0142 two records; reporting them as duplicates "+
			"would refuse a rename the operator is entitled to make. Got: %v", got)
	}
}

// TestIntegrity_RelationFindings covers FR-033 (unresolved) and FR-034 (wrong
// type) — and the case-only "nearest" suggestion, which is exact rather than
// fuzzy on purpose.
func TestIntegrity_RelationFindings(t *testing.T) {
	root := writeNote(t, "", "Widgets/Gear.md", "g\n")
	root = writeNote(t, root, "Foundries/Acme Ltd.md", "f\n")
	root = writeNote(t, root, "Meetings/Q3 planning.md", "m\n")
	schemas := integrityFixtureSchemas(t, root)

	store := &fakePropertyIndex{
		records: []IndexedRecord{
			{Path: "Widgets/Gear.md", RecordType: "widget", RecordID: "WI-0091"},
			{Path: "Foundries/Acme Ltd.md", RecordType: "foundry", RecordID: "FO-0001"},
			{Path: "Meetings/Q3 planning.md", RecordType: "", RecordID: ""},
		},
		relations: []IndexedRelation{
			{Path: "Widgets/Gear.md", RecordID: "WI-0091", Property: "maker", Target: "Acme Corp."},
			{Path: "Widgets/Gear.md", RecordID: "WI-0091", Property: "maker", Target: "Q3 planning"},
			{Path: "Widgets/Gear.md", RecordID: "WI-0091", Property: "maker", Target: "acme ltd"},
		},
	}
	report, err := CheckIntegrity(context.Background(), IntegrityOptions{
		FS: OSLinkFS(), Root: mustCollectionRoot(t, root), Schemas: schemas, Store: store,
	})
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}

	unresolved := findingDetails(report, CategoryUnresolvedRelation)
	if len(unresolved) != 2 {
		t.Fatalf("expected two unresolved relations, got %v", unresolved)
	}
	for _, want := range []string{"WI-0091", "maker", "Acme Corp.", "no note resolves"} {
		if !strings.Contains(unresolved[0], want) {
			t.Errorf("%q missing from the unresolved-relation finding %q", want, unresolved[0])
		}
	}

	wrong := findingDetails(report, CategoryWrongType)
	if len(wrong) != 1 {
		t.Fatalf("expected one wrong-type relation, got %v", wrong)
	}
	for _, want := range []string{"WI-0091", "maker", "Q3 planning", "expected foundry"} {
		if !strings.Contains(wrong[0], want) {
			t.Errorf("FR-034 requires the finding to name what it FOUND and what was EXPECTED; "+
				"%q missing from %q", want, wrong[0])
		}
	}
	// The third edge differs from a real note ONLY IN CASE, and this package's
	// wikilink resolution is case-SENSITIVE (NoteIndex.byBase is keyed on the
	// exact basename), so it genuinely does not resolve. That is precisely the
	// case the "nearest" suggestion exists for: the answer is a fact — the
	// file is right there and the link is one keystroke from working — not a
	// fuzzy guess.
	caseOnly := ""
	for _, d := range unresolved {
		if strings.Contains(d, "acme ltd") {
			caseOnly = d
		}
	}
	if caseOnly == "" {
		t.Fatalf("the case-only relation must be reported; got %v", unresolved)
	}
	if !strings.Contains(caseOnly, "nearest: Foundries/Acme Ltd.md") {
		t.Errorf("a case-only mismatch must name the note it almost matched, so the operator "+
			"is not left hunting for a file that is already there; got %q", caseOnly)
	}
}

// TestIntegrity_NearestSuggestionIsExactNotFuzzy — a wrong suggestion in an
// integrity report is worse than none, because the operator acts on it.
func TestIntegrity_NearestSuggestionIsExactNotFuzzy(t *testing.T) {
	root := writeNote(t, "", "Widgets/Gear.md", "g\n")
	root = writeNote(t, root, "Foundries/Acme Ltd.md", "f\n")
	schemas := integrityFixtureSchemas(t, root)

	report, err := CheckIntegrity(context.Background(), IntegrityOptions{
		FS: OSLinkFS(), Root: mustCollectionRoot(t, root), Schemas: schemas,
		Store: &fakePropertyIndex{
			records: []IndexedRecord{{Path: "Widgets/Gear.md", RecordType: "widget", RecordID: "WI-1"}},
			relations: []IndexedRelation{
				{Path: "Widgets/Gear.md", RecordID: "WI-1", Property: "maker", Target: "Acme Limited"},
			},
		},
	})
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	got := findingDetails(report, CategoryUnresolvedRelation)
	if len(got) != 1 {
		t.Fatalf("expected one finding, got %v", got)
	}
	if strings.Contains(got[0], "nearest") {
		t.Errorf("'Acme Limited' and 'Acme Ltd' differ by more than case; a fuzzy suggestion here "+
			"would send the operator to the wrong note. Got %q", got[0])
	}
}

// TestIntegrity_OrphanRowNamesTheVanishedNote — FR-020c, D16.5.
func TestIntegrity_OrphanRowNamesTheVanishedNote(t *testing.T) {
	root := writeNote(t, "", "Widgets/Live.md", "x\n")
	schemas := integrityFixtureSchemas(t, root)

	report, err := CheckIntegrity(context.Background(), IntegrityOptions{
		FS: OSLinkFS(), Root: mustCollectionRoot(t, root), Schemas: schemas,
		Store: &fakePropertyIndex{records: []IndexedRecord{
			{Path: "Widgets/Live.md", RecordType: "widget", RecordID: "WI-1"},
			{Path: "Widgets/Gone.md", RecordType: "widget", RecordID: "WI-0221"},
		}},
	})
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	got := findingDetails(report, CategoryOrphanRow)
	if len(got) != 1 {
		t.Fatalf("expected one orphan row, got %v", got)
	}
	for _, want := range []string{"WI-0221", "Widgets/Gone.md", "no note exists"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("%q missing from the orphan-row finding %q", want, got[0])
		}
	}
}

// TestIntegrity_BrokenWikilinkInAVaultWithNoRecords — AC-D5.
//
// This is the capability knowledge_graph's retirement would otherwise have
// lost: MOST NOTES IN A VAULT ARE NOT RECORDS, and a vault-wide broken-link
// report would have had no home in the new surface at all.
func TestIntegrity_BrokenWikilinkInAVaultWithNoRecords(t *testing.T) {
	root := writeNote(t, "", "Notes/2026-08-14.md", "see [[Q2 retro]]\n")
	root = writeNote(t, root, "Notes/index.md", "[[2026-08-14]]\n")

	report, err := CheckIntegrity(context.Background(), IntegrityOptions{
		FS: OSLinkFS(), Root: mustCollectionRoot(t, root),
		// No schemas, no store: a vault with no record types at all.
	})
	if err != nil {
		t.Fatalf("a vault with no record types must still sweep: %v", err)
	}
	got := findingDetails(report, CategoryBrokenLink)
	if len(got) != 1 {
		t.Fatalf("expected exactly one broken ordinary wikilink, got %v", got)
	}
	for _, want := range []string{"Notes/2026-08-14.md", "Q2 retro", "ordinary wikilink, not a relation"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("%q missing from the broken-link finding %q", want, got[0])
		}
	}
}

// TestIntegrity_UnresolvedReasonsAreNotCollapsed — FR-042 records three
// distinct reasons and they are not the same problem.
func TestIntegrity_UnresolvedReasonsAreNotCollapsed(t *testing.T) {
	root := writeNote(t, "", "a.md", "[[../outside]] and [[]] and [[nowhere]]\n")
	report, err := CheckIntegrity(context.Background(), IntegrityOptions{
		FS: OSLinkFS(), Root: mustCollectionRoot(t, root),
	})
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	joined := strings.Join(findingDetails(report, CategoryBrokenLink), "\n")
	for _, want := range []string{"leaves the collection", "names nothing", "no note resolves"} {
		if !strings.Contains(joined, want) {
			t.Errorf("collapsing FR-042's reasons into one message loses the distinction; "+
				"%q missing from:\n%s", want, joined)
		}
	}
}

// ---------------------------------------------------------------------------
// The "not checked" posture — AC-D6
// ---------------------------------------------------------------------------

// TestIntegrity_WithoutAnIndexTypedCategoriesAreNotCheckedNotZero — AC-D6, and
// it is the single most important assertion in this file.
//
// "0 duplicate identifiers" and "duplicate identifiers were not checked" are
// OPPOSITE VERDICTS. A report that renders them the same tells an operator
// their vault is clean when the truth is that nothing looked.
//
// MUTATION: in CheckIntegrity, drop the `sink.notRun(...)` loop so a failed
// typed half leaves the categories at zero. This test fails.
func TestIntegrity_WithoutAnIndexTypedCategoriesAreNotCheckedNotZero(t *testing.T) {
	root := writeNote(t, "", "a.md", "[[gone]]\n")
	schemas := integrityFixtureSchemas(t, root)

	report, err := CheckIntegrity(context.Background(), IntegrityOptions{
		FS: OSLinkFS(), Root: mustCollectionRoot(t, root), Schemas: schemas,
		Store: nil, // no properties index at all
	})
	if err != nil {
		t.Fatalf("the wikilink and orphan checks still run without an index: %v", err)
	}
	for cat := range typedCategories {
		c := report.Category(cat)
		if c == nil {
			t.Fatalf("category %q vanished from the report", cat)
		}
		if c.NotRun == "" {
			t.Errorf("%s ran without a properties index and reported %d findings; "+
				"it must report WHY it could not run instead of a count", cat, c.Total)
		}
	}
	if len(findingDetails(report, CategoryBrokenLink)) != 1 {
		t.Errorf("the wikilink half must still run — FR-020h says so in the same sentence")
	}

	// And the text a model reads must name the blocked categories.
	text := RenderDescribe(DescribeData{
		Collection: "c", Schemas: schemas, Views: records.NewViewSet(),
		Integrity: report, Sections: map[string]bool{},
	})
	if !strings.Contains(text, "NOT CHECKED") {
		t.Fatalf("the response must say the categories were NOT CHECKED:\n%s", text)
	}
	for cat := range typedCategories {
		if !strings.Contains(text, string(cat)) {
			t.Errorf("AC-D6 requires the unrunnable categories to be named BY NAME; %q missing from:\n%s", cat, text)
		}
	}
}

// TestIntegrity_RecordTypeScopeIsRefusedWithoutAnIndex.
//
// A record_type scope needs the index to know WHICH notes carry that type.
// Without it the wikilink half would silently widen to the whole vault while
// the report still claimed to be scoped — a wrong answer wearing a correct
// label. So the call is refused instead.
func TestIntegrity_RecordTypeScopeIsRefusedWithoutAnIndex(t *testing.T) {
	root := writeNote(t, "", "a.md", "[[gone]]\n")
	schemas := integrityFixtureSchemas(t, root)

	_, err := CheckIntegrity(context.Background(), IntegrityOptions{
		FS: OSLinkFS(), Root: mustCollectionRoot(t, root), Schemas: schemas,
		RecordType: "widget", Store: nil,
	})
	if err == nil {
		t.Fatalf("a scoped sweep with no index must be refused, not silently widened")
	}
	if !strings.Contains(err.Error(), "widget") || !strings.Contains(err.Error(), "unscoped") {
		t.Errorf("the refusal must name the scope it could not honour and the remedy; got %q", err.Error())
	}
}

// TestIntegrity_UnknownRecordTypeIsRefusedWithTheDeclaredOnesListed — FR-024's
// pattern applied to the scope argument. It must never answer with an empty
// sweep, because an empty sweep and a typo are indistinguishable.
func TestIntegrity_UnknownRecordTypeIsRefusedWithTheDeclaredOnesListed(t *testing.T) {
	root := writeNote(t, "", "a.md", "x\n")
	schemas := integrityFixtureSchemas(t, root)

	_, err := CheckIntegrity(context.Background(), IntegrityOptions{
		FS: OSLinkFS(), Root: mustCollectionRoot(t, root), Schemas: schemas,
		RecordType: "widgt",
	})
	if err == nil {
		t.Fatalf("an undeclared record type must be refused, not swept as nothing")
	}
	for _, want := range []string{"widgt", "widget", "foundry"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("FR-024 requires the valid names to be listed; %q missing from %q", want, err.Error())
		}
	}
}

// TestIntegrity_RecordTypeScopeNarrowsTheWikilinkHalfToo.
//
// A scoped sweep that reported broken links from every note in the vault would
// be reporting something other than what it said it was reporting.
func TestIntegrity_RecordTypeScopeNarrowsTheWikilinkHalfToo(t *testing.T) {
	root := writeNote(t, "", "Widgets/Gear.md", "[[gone-a]]\n")
	root = writeNote(t, root, "Notes/loose.md", "[[gone-b]]\n")
	schemas := integrityFixtureSchemas(t, root)

	report, err := CheckIntegrity(context.Background(), IntegrityOptions{
		FS: OSLinkFS(), Root: mustCollectionRoot(t, root), Schemas: schemas,
		RecordType: "widget",
		Store: &fakePropertyIndex{records: []IndexedRecord{
			{Path: "Widgets/Gear.md", RecordType: "widget", RecordID: "WI-1"},
		}},
	})
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	got := findingDetails(report, CategoryBrokenLink)
	if len(got) != 1 || !strings.Contains(got[0], "Widgets/Gear.md") {
		t.Fatalf("a record_type-scoped sweep must report only that type's notes, got %v", got)
	}
	if strings.Contains(strings.Join(got, "\n"), "Notes/loose.md") {
		t.Errorf("a note outside the scope leaked into a scoped report")
	}
	if report.ScopeLabel == "whole vault" {
		t.Errorf("a scoped report must not describe itself as the whole vault")
	}
}

// ---------------------------------------------------------------------------
// FR-020h — the platform refusal, at the entry point that owes it
// ---------------------------------------------------------------------------

// TestTypedIntegrity_RefusesOnSQLiteLessBuild is the assertion Stage 1 left the
// harness for. It is a no-op on a SQLite-capable build and the whole point of
// the forcing tag on one without:
//
//	go test -tags goolm,stdjson,records_no_sqlite ./pkg/knowledge/
//
// What it catches is not a missing refusal — propindex_stub_test.go covers
// that — but an entry point that SWALLOWS one and returns an empty result,
// which reads to an operator as "your vault is clean".
func TestTypedIntegrity_RefusesOnSQLiteLessBuild(t *testing.T) {
	records.AssertRefusesWhenIndexUnavailable(t, records.CapabilityIntegrityCheck,
		func() (*TypedIntegrityResult, error) {
			return TypedIntegrity(context.Background(), TypedIntegrityInput{
				Store:   &fakePropertyIndex{},
				Schemas: records.NewSchemaSet(),
			}, newFindingSink(IntegrityFindingsPerCategory))
		})
}

// TestTypedIntegrity_StoreFailureIsReportedNotSwallowed — a store that errors
// halfway through must not leave a half-swept report looking complete.
func TestTypedIntegrity_StoreFailureIsReportedNotSwallowed(t *testing.T) {
	if !records.PropertyIndexAvailable {
		t.Skip("the platform refusal fires first on this build; covered by the test above")
	}
	root := writeNote(t, "", "a.md", "x\n")
	schemas := integrityFixtureSchemas(t, root)

	report, err := CheckIntegrity(context.Background(), IntegrityOptions{
		FS: OSLinkFS(), Root: mustCollectionRoot(t, root), Schemas: schemas,
		Store: &fakePropertyIndex{recordsErr: fmt.Errorf("the index file is corrupt")},
	})
	if err != nil {
		t.Fatalf("a typed-half failure must not fail the whole sweep: %v", err)
	}
	c := report.Category(CategoryDuplicateID)
	if c.NotRun == "" {
		t.Fatalf("a store failure must be reported as NOT CHECKED, not as zero findings")
	}
	if !strings.Contains(c.NotRun, "corrupt") {
		t.Errorf("the reason must reach the report rather than being replaced: %q", c.NotRun)
	}
}
