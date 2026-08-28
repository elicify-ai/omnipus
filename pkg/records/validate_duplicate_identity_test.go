// Omnipus — the duplicate-list warning must ask the comparator, not the printer.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// `ReportDuplicateListValues` answers ONE question: are two elements of this
// list the same value? That question already has an authority — spec §8, as
// implemented once in compare_oracle.go and verified cell-by-cell by
// compare_truthtable_test.go. filter.go's header spells out what happens when a
// second implementation of it appears anywhere in this package: the verified
// comparator sits off the path while the unverified one does the real work.
//
// The duplicate check used to key a map on `TypedValue.String()`. That is the
// REPORT RENDERER, not an identity — so under it:
//
//	R-8  `[[Acme]]` and `[[Acme|Acme Corp]]` were two different things, though
//	     a display alias is presentation and the target is identity.
//	R-7  `2026-01-01` and `2026-01-01T00:00:00Z` were two different things,
//	     though they are one instant.
//	     `1.0` and `1.00` were two different things, though they are one number.
//
// Every expectation below is derived from those rules, never from what the
// check happens to do. The fixtures are data, not product content (ADR-068 D0).
// ---------------------------------------------------------------------------

// dupIdentityFixture declares one `many` property per declared type that has a
// spelling distinct from its identity, so each rule below has somewhere to live.
const dupIdentityFixture = `
schema_version: 1
type: deal
properties:
  name:    { type: text, required: true }
  clients: { type: relation, to: company, many: true }
  owners:  { type: person, many: true }
  seen:    { type: date, many: true }
  sizes:   { type: decimal, many: true }
  counts:  { type: integer, many: true }
  tags:    { type: enum, values: [red, green, blue], many: true }
  notes:   { type: text, many: true }
`

// dupFindings runs the duplicate check over one note body and returns only the
// duplicate warnings. It sets ReportDuplicateListValues explicitly — the option
// defaults to FALSE, so a test that forgets it passes while exercising nothing.
func dupFindings(t *testing.T, set *SchemaSet, body string) []Finding {
	t.Helper()
	rec := ParseRecord("notes/deal.md", []byte(body))
	if rec.ParseError != "" {
		t.Fatalf("fixture note did not parse: %s", rec.ParseError)
	}
	rep := ValidateRecord(set, rec, ValidateOptions{ReportDuplicateListValues: true})
	if !rep.Recognised {
		t.Fatalf("fixture note must be recognised as a `deal`; findings: %v", rep.Findings)
	}
	out := make([]Finding, 0, len(rep.Findings))
	for _, f := range rep.Findings {
		if f.Code == FindingDuplicateListValue {
			out = append(out, f)
		}
	}
	// Guard against a vacuous pass from the other direction: if the note itself
	// does not conform, the property never reaches the duplicate check and the
	// empty result would look like "no duplicates".
	for _, f := range rep.Findings {
		if f.Severity == SeverityError {
			t.Fatalf("fixture note must conform so the duplicate check is actually reached; got %v", f)
		}
	}
	return out
}

// TestValidate_DuplicateListValue_ReachesTheBranch is the anti-vacuity check for
// every test in this file. ReportDuplicateListValues defaults to false, so a
// test that forgot it would assert "no duplicates" against a check that never
// ran. This pins that the helper's option really does switch the branch on.
func TestValidate_DuplicateListValue_ReachesTheBranch(t *testing.T) {
	set := loadSet(t, map[string]string{"deal.yaml": dupIdentityFixture})
	body := "---\ntype: deal\nname: A\ntags: [red, red]\n---\n"

	if got := dupFindings(t, set, body); len(got) != 1 {
		t.Fatalf("the helper must exercise the duplicate branch; an obvious repeat produced %d warnings: %v", len(got), got)
	}
	rec := ParseRecord("notes/deal.md", []byte(body))
	if rep := ValidateRecord(set, rec, ValidateOptions{}); len(rep.Findings) != 0 {
		t.Fatalf("with the option off the same note must be silent; got %v", rep.Findings)
	}
}

// TestValidate_DuplicateListValue_RelationIdentityNotSpelling is §8 R-8 applied
// to the duplicate check.
//
// R-8 compares a relation BY TARGET IDENTITY, never by display text — value.go
// states the same thing from the other side: Wikilink.Display "is NEVER
// identity", and Wikilink.Target is "the join key". So a list holding `[[Acme]]`
// and `[[Acme|Acme Corp]]` names ONE record twice, which is precisely what this
// check exists to report.
func TestValidate_DuplicateListValue_RelationIdentityNotSpelling(t *testing.T) {
	set := loadSet(t, map[string]string{"deal.yaml": dupIdentityFixture})

	for _, tc := range []struct {
		name     string
		property string
		list     string
	}{
		{"relation", "clients", `["[[Acme]]", "[[Acme|Acme Corp]]"]`},
		{"person", "owners", `["[[Dana Fox]]", "[[Dana Fox|Dana]]"]`},
	} {
		t.Run(tc.name+" — a display alias is presentation, not identity", func(t *testing.T) {
			body := "---\ntype: deal\nname: A\n" + tc.property + ": " + tc.list + "\n---\n"
			got := dupFindings(t, set, body)
			if len(got) != 1 {
				t.Fatalf("R-8: the two links name one target, so this is ONE duplicate; got %d: %v", len(got), got)
			}
			f := got[0]
			if f.Property != tc.property {
				t.Fatalf("the warning must name the property; got %q", f.Property)
			}
			if f.Severity != SeverityWarning {
				t.Fatalf("a repeat is a warning, not an error; got %q", f.Severity)
			}
			if f.ElementIndex != 1 {
				t.Fatalf("the repeat is the SECOND element, source position 1; the warning said %d", f.ElementIndex)
			}
			if !strings.Contains(f.Reason, "positions 0 and 1") {
				t.Fatalf("the reason must name both source positions; got %q", f.Reason)
			}
			// The two spellings differ, so a message quoting only one of them
			// sends the operator looking for text their file does not contain
			// at that position.
			for _, want := range []string{"[[", "]]"} {
				if !strings.Contains(f.Reason, want) {
					t.Fatalf("the reason must quote the links it is talking about; got %q", f.Reason)
				}
			}
			if !strings.Contains(f.Reason, "|") {
				t.Fatalf("the two spellings differ, so the aliased one must appear in the message too; got %q", f.Reason)
			}
		})
	}

	t.Run("relation — different targets are not duplicates", func(t *testing.T) {
		// The counterpart assertion. Identity equality must not collapse two
		// genuinely different records just because their names look alike.
		body := "---\ntype: deal\nname: A\nclients: [\"[[Acme]]\", \"[[Acme Ltd]]\", \"[[acme]]\"]\n---\n"
		if got := dupFindings(t, set, body); len(got) != 0 {
			t.Fatalf("three distinct targets are three records; got %d duplicate warnings: %v", len(got), got)
		}
	})

	t.Run("relation — a heading is a place inside one note, not another record", func(t *testing.T) {
		// PINNED DECISION. Wikilink.Target is documented as "the join key",
		// and ParseWikilink strips both `#heading` and `|display` out of it.
		// `[[Acme#Contacts]]` and `[[Acme]]` therefore resolve to one target.
		body := "---\ntype: deal\nname: A\nclients: [\"[[Acme]]\", \"[[Acme#Contacts]]\"]\n---\n"
		if got := dupFindings(t, set, body); len(got) != 1 {
			t.Fatalf("both links point at the note `Acme`; got %d duplicate warnings: %v", len(got), got)
		}
	})
}

// TestValidate_DuplicateListValue_ValueNotRendering covers the rest of the class
// the relation case belongs to: every declared type whose SPELLING can differ
// while its VALUE does not.
//
// Each expectation comes from the rule, not from the code:
//
//	R-7  a date compares as an instant, so a day and that day's midnight are
//	     one value.
//	R-1  a number compares NUMERICALLY, so trailing zeros are not a difference —
//	     and `integer` and `decimal` are ONE comparison domain, so `3` and `3.0`
//	     are one value whichever of the two the property declares.
//	R-5  an enum resolves case-insensitively (FR-011a), so `green` and `Green`
//	     are one value rather than two de-facto ones.
func TestValidate_DuplicateListValue_ValueNotRendering(t *testing.T) {
	set := loadSet(t, map[string]string{"deal.yaml": dupIdentityFixture})

	for _, tc := range []struct {
		name     string
		property string
		list     string
		want     int
	}{
		{"R-7 a day and that day's midnight are one instant", "seen", `["2026-01-01", "2026-01-01T00:00:00Z"]`, 1},
		{"R-7 different instants are different values", "seen", `["2026-01-01", "2026-01-02"]`, 0},
		{"trailing zeros do not make a second number", "sizes", `["1.0", "1.00"]`, 1},
		{"different numbers are different values", "sizes", `["1.0", "1.5"]`, 0},
		{"R-1 an integer written with a fractional zero is the same integer", "counts", `["3", "3.0"]`, 1},
		{"R-1 different integers are different values", "counts", `["3", "4"]`, 0},
		{"R-1 an integer written in exponent notation is the same integer", "counts", `["300", "3e2"]`, 1},
		{"R-5 an enum repeat is still a repeat", "tags", `[green, green]`, 1},
		{"FR-011a two spellings of one enum value are ONE value, not two", "tags", `[green, Green]`, 1},
		{"distinct enum values are not a repeat", "tags", `[red, green, blue]`, 0},
		// THE REGRESSION GUARD FOR THE CHOICE OF ORACLE ENTRY POINT. §8 gives
		// `text` no scalar `eq` (operatorDefinedForType), so routing this check
		// through filter.go's Compare — the other plausible "use the one
		// comparator" fix — would stop reporting repeats in a `many text` list
		// entirely. R-9's elementsEqual is defined for every type, and this row
		// fails the moment somebody swaps it for Compare.
		{"a text repeat is still a repeat", "notes", `["seen at KubeCon", "seen at KubeCon"]`, 1},
		{"different text is not a repeat", "notes", `["seen at KubeCon", "seen at re:Invent"]`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "---\ntype: deal\nname: A\n" + tc.property + ": " + tc.list + "\n---\n"
			got := dupFindings(t, set, body)
			if len(got) != tc.want {
				t.Fatalf("%s %s: want %d duplicate warnings, got %d: %v", tc.property, tc.list, tc.want, len(got), got)
			}
		})
	}
}

// TestValidate_DuplicateListValue_ThreeOfOneTargetWarnsTwice pins the counting
// rule under identity equality: N occurrences of one value produce N-1
// warnings, each naming the FIRST occurrence, however each one is spelled.
func TestValidate_DuplicateListValue_ThreeOfOneTargetWarnsTwice(t *testing.T) {
	set := loadSet(t, map[string]string{"deal.yaml": dupIdentityFixture})

	body := "---\ntype: deal\nname: A\nclients: [\"[[Acme]]\", \"[[Acme|Acme Corp]]\", \"[[Acme#Billing]]\"]\n---\n"
	got := dupFindings(t, set, body)
	if len(got) != 2 {
		t.Fatalf("three spellings of one target are two repeats; got %d: %v", len(got), got)
	}
	if got[0].ElementIndex != 1 || got[1].ElementIndex != 2 {
		t.Fatalf("the repeats are at source positions 1 and 2; got %d and %d", got[0].ElementIndex, got[1].ElementIndex)
	}
	for _, f := range got {
		if !strings.Contains(f.Reason, "positions 0 and") {
			t.Fatalf("every repeat is measured against the FIRST occurrence at position 0; got %q", f.Reason)
		}
	}
}

// TestValidate_DuplicateListValue_PositionsSurviveADroppedElement is the
// existing source-position invariant re-asserted for identity equality, because
// the two interact: the value that is equal is no longer the value whose text
// was hashed, and the position must still be the file's.
func TestValidate_DuplicateListValue_PositionsSurviveADroppedElement(t *testing.T) {
	set := loadSet(t, map[string]string{"deal.yaml": dupIdentityFixture})

	// Element 0 is not a wikilink: reported and dropped. The two spellings of
	// `Acme` sit at Values indexes 0 and 1 but at FILE positions 1 and 2.
	rec := ParseRecord("notes/deal.md", []byte(
		"---\ntype: deal\nname: A\nclients:\n  - Acme\n  - \"[[Acme]]\"\n  - \"[[Acme|Acme Corp]]\"\n---\n"))
	rep := ValidateRecord(set, rec, ValidateOptions{ReportDuplicateListValues: true})

	var dup *Finding
	for i := range rep.Findings {
		if rep.Findings[i].Code == FindingDuplicateListValue {
			dup = &rep.Findings[i]
		}
	}
	if dup == nil {
		t.Fatalf("the two conforming links still name one target; findings: %v", rep.Findings)
	}
	if dup.ElementIndex != 2 {
		t.Fatalf("the repeat is at SOURCE position 2 (the bare text occupies 0); the warning said %d", dup.ElementIndex)
	}
	if !strings.Contains(dup.Reason, "positions 1 and 2") {
		t.Fatalf("both positions must be source positions; got %q", dup.Reason)
	}
	if rep.Valid() {
		t.Fatalf("the bare-text element is not a wikilink and must still fail the record; findings: %v", rep.Findings)
	}
}

// TestValidate_DuplicateListValue_UsesTheOneComparator is the structural
// assertion, and it is the one that would survive somebody rewriting the check.
//
// It supplies a RelationResolver that maps two DIFFERENT targets onto one record
// id — the aliasing only a vault index can know about. No lexical key over the
// link text can produce a duplicate here, however it is spelled. If this test
// passes, the check went through Comparator.
func TestValidate_DuplicateListValue_UsesTheOneComparator(t *testing.T) {
	set := loadSet(t, map[string]string{"deal.yaml": dupIdentityFixture})

	rec := ParseRecord("notes/deal.md", []byte(
		"---\ntype: deal\nname: A\nclients: [\"[[Acme]]\", \"[[Acme Corporation Pte Ltd]]\"]\n---\n"))

	// The index resolved both note names to one record. D7: "filename is
	// identity" is the failure R-8 exists to close, and this is the seam that
	// closes it.
	opts := ValidateOptions{
		ReportDuplicateListValues: true,
		ResolveRelation: func(l Wikilink) (string, bool) {
			switch l.Target {
			case "Acme", "Acme Corporation Pte Ltd":
				return "CO-0001", true
			}
			return "", false
		},
	}
	rep := ValidateRecord(set, rec, opts)

	var dups int
	for _, f := range rep.Findings {
		if f.Code == FindingDuplicateListValue {
			dups++
		}
	}
	if dups != 1 {
		t.Fatalf("the resolver put both links on record CO-0001, so the list names it twice; got %d warnings: %v", dups, rep.Findings)
	}

	// And the resolver is authoritative in both directions: without it, these
	// two targets are two records and nothing is reported.
	plain := ValidateRecord(set, rec, ValidateOptions{ReportDuplicateListValues: true})
	for _, f := range plain.Findings {
		if f.Code == FindingDuplicateListValue {
			t.Fatalf("with no index the two note names are two targets; got %v", f)
		}
	}
}

// TestValidate_DuplicateListValue_UnresolvedRelationIsNotADuplicate pins the
// documented disposition for a comparison the oracle REFUSES to make.
//
// A resolver that cannot place a link means "I do not know whether these are
// the same record". The duplicate check reports what it KNOWS is a repeat; it
// does not guess, and it does not turn the oracle's problem into a validation
// finding — an unresolved link is the index's report to make, not this check's.
func TestValidate_DuplicateListValue_UnresolvedRelationIsNotADuplicate(t *testing.T) {
	set := loadSet(t, map[string]string{"deal.yaml": dupIdentityFixture})

	rec := ParseRecord("notes/deal.md", []byte(
		"---\ntype: deal\nname: A\nclients: [\"[[Ghost]]\", \"[[Ghost]]\"]\n---\n"))

	opts := ValidateOptions{
		ReportDuplicateListValues: true,
		ResolveRelation:           func(Wikilink) (string, bool) { return "", false },
	}
	rep := ValidateRecord(set, rec, opts)

	for _, f := range rep.Findings {
		if f.Code == FindingDuplicateListValue {
			t.Fatalf("the resolver placed neither link, so nothing is known to repeat; got %v", f)
		}
	}
	// It must also not have invented an error out of the refusal — the record
	// itself conforms.
	if !rep.Valid() {
		t.Fatalf("an unresolved link is not a conformance fault of this record; findings: %v", rep.Findings)
	}
}

// TestValidate_DuplicateListValue_AnIncomparableElementDoesNotStopTheScan pins
// the property the retired cross-currency test used to hold, on a subject that
// still exists.
//
// It WAS TestValidate_DuplicateListValue_MixedCurrencyIsSilent, and its whole
// premise — R-6, "money compares only within one currency" — went with the
// `money` type. The valuable half is not about currency at all: when the oracle
// REFUSES one element of a list, the scan must carry on and still find the
// repeat on either side of it. A check that stopped at the first refusal would
// silently under-report on every list containing one unresolvable link.
//
// R-8 supplies the live refusal: a link the resolver cannot place. The middle
// element here is unplaceable; the first and third name the same company.
func TestValidate_DuplicateListValue_AnIncomparableElementDoesNotStopTheScan(t *testing.T) {
	set := loadSet(t, map[string]string{"deal.yaml": dupIdentityFixture})

	rec := ParseRecord("notes/deal.md", []byte(
		"---\ntype: deal\nname: A\nclients: [\"[[Acme]]\", \"[[Ghost]]\", \"[[Acme Inc]]\"]\n---\n"))

	opts := ValidateOptions{
		ReportDuplicateListValues: true,
		ResolveRelation: func(l Wikilink) (string, bool) {
			// Two spellings of one company; `Ghost` is in no index.
			switch l.Target {
			case "Acme", "Acme Inc":
				return "CO-0001", true
			}
			return "", false
		},
	}
	rep := ValidateRecord(set, rec, opts)

	var dups []Finding
	for _, f := range rep.Findings {
		if f.Code == FindingDuplicateListValue {
			dups = append(dups, f)
		}
	}
	if len(dups) != 1 {
		t.Fatalf("elements 0 and 2 are one company and must be reported; the refusal at element 1 must not end the scan. want 1 warning, got %d: %v", len(dups), dups)
	}
	if dups[0].ElementIndex != 2 {
		t.Fatalf("the repeat is at source position 2; the warning said %d", dups[0].ElementIndex)
	}
	if !strings.Contains(dups[0].Reason, "positions 0 and 2") {
		t.Fatalf("the repeat is measured against position 0, not against the unplaceable element at 1; got %q", dups[0].Reason)
	}
	// And the refusal itself is NOT converted into a conformance fault: an
	// unresolved link is the index's report to make, not this check's.
	if !rep.Valid() {
		t.Fatalf("an unresolved link is not a fault of THIS record; findings: %v", rep.Findings)
	}
}

// BenchmarkDuplicateListFindings measures what routing through the comparator
// costs, on the shape it costs the most on.
//
// The map keyed on TypedValue.String() was O(n). Comparator equality has no
// hash — R-8 identity is not derivable from one value in isolation — so the
// check compares each element against the DISTINCT values seen so far: O(n x k).
// A list of all-distinct values is therefore the worst case, k == n, and it is
// also the realistic one: a 1,000-element `many relation` property in a real
// vault is 1,000 different companies, not one repeated.
//
// MEASURED — go1.26.6 darwin/amd64, Intel i7-1068NG7 @ 2.3GHz, -benchtime=20x.
// Each figure is one whole ValidateRecord call; the DELTA is the duplicate scan:
//
//	                                   check-off   check-on     scan
//	relation, 1,000 distinct             0.32 ms    23.3 ms    ~23 ms
//	relation,   100 distinct             0.07 ms     0.38 ms   ~0.3 ms
//	relation, 1,000 all one target          —        1.56 ms   ~1.3 ms
//	text,     1,000 distinct             0.27 ms    14.2 ms    ~14 ms
//
// Read across the first two rows: 10x the list length is ~75x the scan, which
// is the O(n^2) corner arriving exactly where the algebra says it should
// (~500k comparisons at ~46ns each). The third row is the same length with
// k == 1 — linear, and most of ITS 1.3 ms is building 999 Finding values, not
// comparing. Nothing here is a hot path: this runs when an operator asks a
// vault to audit itself, not per keystroke and not per query.
//
// What it cost before: the string-keyed map was O(n) and would have finished
// the 1,000-distinct case in well under a millisecond, with the wrong answer
// for five of the seven declared types. That is the trade, in numbers.
//
// It is a benchmark, not a budget: no test fails on a number here. It exists so
// the trade is stated in measurements rather than asserted in a comment. Run it
// with `-run '^$' -bench BenchmarkDuplicateListFindings -benchtime=20x`.
func BenchmarkDuplicateListFindings(b *testing.B) {
	set := loadSetB(b, dupIdentityFixture)

	build := func(property string, element func(i int) string, n int) Record {
		var sb strings.Builder
		sb.WriteString("---\ntype: deal\nname: A\n")
		sb.WriteString(property)
		sb.WriteString(":\n")
		for i := 0; i < n; i++ {
			sb.WriteString("  - ")
			sb.WriteString(element(i))
			sb.WriteString("\n")
		}
		sb.WriteString("---\n")
		rec := ParseRecord("notes/bench.md", []byte(sb.String()))
		if rec.ParseError != "" {
			b.Fatalf("benchmark note did not parse: %s", rec.ParseError)
		}
		return rec
	}

	distinctLink := func(i int) string { return `"[[Company ` + strconv.Itoa(i) + `]]"` }
	sameLink := func(int) string { return `"[[Acme]]"` }
	distinctText := func(i int) string { return `"Company ` + strconv.Itoa(i) + `"` }

	on := ValidateOptions{ReportDuplicateListValues: true}
	off := ValidateOptions{}

	// Every "check-on" row is paired with the SAME record validated with the
	// check OFF. ValidateRecord parses and type-checks a 1,000-element list
	// either way, and that is most of the wall clock — a bare on-figure would
	// bill the duplicate scan for work it did not do. The difference is the
	// number this benchmark is actually about.
	for _, tc := range []struct {
		name     string
		property string
		element  func(int) string
		n        int
		opts     ValidateOptions
	}{
		{"relation/all-distinct/n=100/check-off", "clients", distinctLink, 100, off},
		{"relation/all-distinct/n=100/check-on", "clients", distinctLink, 100, on},
		{"relation/all-distinct/n=1000/check-off", "clients", distinctLink, 1000, off},
		{"relation/all-distinct/n=1000/check-on", "clients", distinctLink, 1000, on},
		{"relation/all-repeats/n=1000/check-on", "clients", sameLink, 1000, on},
		{"text/all-distinct/n=1000/check-off", "notes", distinctText, 1000, off},
		{"text/all-distinct/n=1000/check-on", "notes", distinctText, 1000, on},
	} {
		rec := build(tc.property, tc.element, tc.n)
		opts := tc.opts
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				rep := ValidateRecord(set, rec, opts)
				if !rep.Recognised {
					b.Fatalf("the benchmark record must be recognised, or this measures nothing")
				}
			}
		})
	}
}

// loadSetB is loadSet for a benchmark. loadSet and writeVaultSchema both take
// *testing.T, and widening them to testing.TB would touch every test in the
// package for one caller's sake.
func loadSetB(b *testing.B, body string) *SchemaSet {
	b.Helper()
	root := b.TempDir()
	dir := SchemaDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		b.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deal.yaml"), []byte(body), 0o644); err != nil {
		b.Fatalf("write schema: %v", err)
	}
	set, report, err := LoadSchemas(root)
	if err != nil {
		b.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		b.Fatalf("fixture schemas must load cleanly; rejections: %v", report.Rejections)
	}
	return set
}
