// Omnipus — tests for FR-005, FR-006, FR-010, FR-011 and the per-record report.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
)

// loadSet builds a schema set from inline schema bodies, keyed by filename.
func loadSet(t *testing.T, files map[string]string) *SchemaSet {
	t.Helper()
	root := ""
	for name, body := range files {
		root = writeVaultSchema(t, root, name, body)
	}
	set, report, err := LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("fixture schemas must load cleanly; rejections: %v", report.Rejections)
	}
	return set
}

// arityFixture is the US-1.4 shape: `segment` declared as a SCALAR enum.
const arityFixture = `
schema_version: 1
type: widget
properties:
  name:    { type: text, required: true }
  segment: { type: enum, values: [vendor, customer, partner] }
  tags:    { type: enum, values: [red, green, blue], many: true }
`

// TestValidate_ArityViolationIsReported covers FR-006 — spec §7 test 3, US-1
// scenario 1.4, and DS-1's `[a, b]` row.
//
// ADR-068 D3.1 records why this is the single most-reported failure in the
// research corpus: the editor converts a scalar to a list the instant a second
// value is added, every query written against it returns nothing, and NOTHING
// REPORTS AN ERROR. So the assertion here is not merely "invalid" — it is that
// the report says ARITY and names the expected shape.
func TestValidate_ArityViolationIsReported(t *testing.T) {
	set := loadSet(t, map[string]string{"widget.yaml": arityFixture})

	t.Run("FR-006 a scalar property holding a list is an arity violation", func(t *testing.T) {
		rec := ParseRecord("notes/acme.md", []byte("---\ntype: widget\nname: Acme\nsegment: [vendor, customer]\n---\nbody\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})

		if rep.Valid() {
			t.Fatalf("FR-006 / US-1.4: a scalar property holding a list must be reported, but the record validated clean")
		}
		errs := rep.Errors()
		if len(errs) != 1 {
			t.Fatalf("expected exactly one finding, got %d: %v", len(errs), errs)
		}
		f := errs[0]
		if f.Code != FindingArity {
			t.Fatalf("US-1.4 requires an ARITY violation specifically, not a value complaint; got code %q (%s)", f.Code, f.Reason)
		}
		if f.Property != "segment" {
			t.Fatalf("the finding must name the property; got %q", f.Property)
		}
		if f.RecordPath != "notes/acme.md" {
			t.Fatalf("the finding must name the record; got %q", f.RecordPath)
		}
		// "validation reports an arity violation NAMING THE EXPECTED SHAPE"
		if f.Expected == "" {
			t.Fatalf("US-1.4 requires the expected shape to be named; Expected was empty")
		}
		if !strings.Contains(f.Expected, "single") {
			t.Fatalf("the expected shape must say a SINGLE value is wanted; got %q", f.Expected)
		}
		if !strings.Contains(f.Got, "list") {
			t.Fatalf("the finding must say what was found; Got = %q", f.Got)
		}
		// The permitted enum values must not be the headline: the fault is the
		// shape, not the values.
		if !strings.Contains(f.Reason, "list") {
			t.Fatalf("the reason must describe the shape fault; got %q", f.Reason)
		}
	})

	t.Run("FR-006 a many property holding a single value is an arity violation", func(t *testing.T) {
		rec := ParseRecord("notes/b.md", []byte("---\ntype: widget\nname: B\ntags: red\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})
		if rep.Valid() {
			t.Fatalf("FR-006 declares arity in BOTH directions; a `many: true` property holding a scalar must be reported")
		}
		f := rep.Errors()[0]
		if f.Code != FindingArity {
			t.Fatalf("expected %q, got %q (%s)", FindingArity, f.Code, f.Reason)
		}
		if !strings.Contains(f.Expected, "list") {
			t.Fatalf("the expected shape must say a LIST is wanted; got %q", f.Expected)
		}
	})

	t.Run("FR-006 the declared shapes validate clean", func(t *testing.T) {
		rec := ParseRecord("notes/c.md", []byte("---\ntype: widget\nname: C\nsegment: vendor\ntags: [red, blue]\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})
		if !rep.Valid() {
			t.Fatalf("a conforming record must validate clean; findings: %v", rep.Findings)
		}
	})

	t.Run("FR-006 a one-element list still violates a scalar declaration", func(t *testing.T) {
		// This is the exact moment D3.1 describes: the editor wraps the value
		// the instant a second is added, and the first thing it produces is a
		// one-element list that still LOOKS right.
		rec := ParseRecord("notes/d.md", []byte("---\ntype: widget\nname: D\nsegment: [vendor]\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})
		if rep.Valid() {
			t.Fatalf("FR-006: `segment: [vendor]` is a list in a scalar property and must be reported")
		}
		if rep.Errors()[0].Code != FindingArity {
			t.Fatalf("expected %q, got %q", FindingArity, rep.Errors()[0].Code)
		}
	})

	t.Run("an empty list is a value, not absence", func(t *testing.T) {
		// §8 R-3: "An empty string, an empty list and a zero are values, not
		// absence." So `tags: []` satisfies the `many` declaration.
		rec := ParseRecord("notes/e.md", []byte("---\ntype: widget\nname: E\ntags: []\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})
		if !rep.Valid() {
			t.Fatalf("R-3: an empty list is a value and conforms to `many: true`; findings: %v", rep.Findings)
		}
	})
}

// TestValidate_UnschematypedNoteIsAnOrdinaryNote covers FR-005 and US-1
// scenario 1.1.
func TestValidate_UnschematypedNoteIsAnOrdinaryNote(t *testing.T) {
	set := loadSet(t, map[string]string{"widget.yaml": arityFixture})

	cases := []struct {
		name string
		src  string
	}{
		{"no frontmatter at all", "just a note about nothing\n"},
		{"frontmatter but no type", "---\ntitle: Hello\ntags: [a, b]\n---\nbody\n"},
		{"a type no schema declares", "---\ntype: company\nname: Acme\n---\nbody\n"},
		{"an empty file", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := ParseRecord("notes/x.md", []byte(tc.src))
			rep := ValidateRecord(set, rec, ValidateOptions{})
			if rep.Recognised {
				t.Fatalf("FR-005: this note is not a record, so Recognised must be false")
			}
			if len(rep.Findings) != 0 {
				t.Fatalf("FR-005 / US-1.1: an unrecognised note must raise NO error; got %v", rep.Findings)
			}
			if !rep.Valid() {
				t.Fatalf("FR-005: an ordinary note is not invalid")
			}
		})
	}

	t.Run("ADR-068 D0 a `company` note is ordinary until the VAULT declares company", func(t *testing.T) {
		// This is the D0 guard in behavioural form: nothing about the name
		// "company" is known to the product.
		rec := ParseRecord("notes/acme.md", []byte("---\ntype: company\nname: Acme\nstatus: nonsense\n---\n"))
		if ValidateRecord(set, rec, ValidateOptions{}).Recognised {
			t.Fatalf("ADR-068 D0: `company` must not be a built-in record type")
		}
	})
}

// TestValidate_ReportsPerRecordWithReasonAndExpectedShape is the brief's own
// acceptance bar: "A report that says only 'invalid' is a failure of this task."
func TestValidate_ReportsPerRecordWithReasonAndExpectedShape(t *testing.T) {
	set := loadSet(t, map[string]string{"widget.yaml": `
schema_version: 1
type: widget
properties:
  name:     { type: text,   required: true }
  status:   { type: enum,   values: [prospect, active] }
  signed:   { type: date }
  headcount: { type: number }
  arr:      { type: money }
  owner:    { type: person }
`})

	recs := []Record{
		ParseRecord("a.md", []byte("---\ntype: widget\n---\n")),                                          // required missing
		ParseRecord("b.md", []byte("---\ntype: widget\nname: B\nstatus: Active\n---\n")),                 // enum case
		ParseRecord("c.md", []byte("---\ntype: widget\nname: C\nsigned: 2026-13-45\n---\n")),             // bad date
		ParseRecord("d.md", []byte("---\ntype: widget\nname: D\nheadcount: PLACEHOLDER\n---\n")),         // bad number
		ParseRecord("e.md", []byte("---\ntype: widget\nname: E\narr: 349.98\n---\n")),                    // money no currency
		ParseRecord("f.md", []byte("---\ntype: widget\nname: F\nowner: Daniel\n---\n")),                  // not a wikilink
		ParseRecord("g.md", []byte("---\ntype: widget\nname: G\nstatus: active\narr: 12.00 EUR\n---\n")), // clean
	}
	rep := Validate(set, recs, ValidateOptions{})

	if len(rep.Records) != len(recs) {
		t.Fatalf("validation must report PER RECORD: %d reports for %d records", len(rep.Records), len(recs))
	}

	wantCodes := map[string]FindingCode{
		"a.md": FindingMissingRequired,
		"b.md": FindingEnumNotPermitted,
		"c.md": FindingNotADate,
		"d.md": FindingNotANumber,
		"e.md": FindingMoneyNoCurrency,
		"f.md": FindingNotAWikilink,
	}
	for _, rr := range rep.Records {
		want, expectFault := wantCodes[rr.Path]
		if !expectFault {
			if !rr.Valid() {
				t.Fatalf("%s should validate clean; findings: %v", rr.Path, rr.Findings)
			}
			continue
		}
		errs := rr.Errors()
		if len(errs) != 1 {
			t.Fatalf("%s: expected exactly one finding, got %v", rr.Path, errs)
		}
		f := errs[0]
		if f.Code != want {
			t.Fatalf("%s: expected code %q, got %q (%s)", rr.Path, want, f.Code, f.Reason)
		}
		if f.RecordPath != rr.Path {
			t.Fatalf("%s: finding must name its record, got %q", rr.Path, f.RecordPath)
		}
		if strings.TrimSpace(f.Reason) == "" {
			t.Fatalf("%s: every finding must carry a REASON", rr.Path)
		}
		if strings.TrimSpace(f.Expected) == "" {
			t.Fatalf("%s: every finding must carry the EXPECTED SHAPE; a report that says only \"invalid\" is a failure", rr.Path)
		}
		if strings.EqualFold(strings.TrimSpace(f.Reason), "invalid") {
			t.Fatalf("%s: the reason must say WHAT is wrong, not \"invalid\"", rr.Path)
		}
		if !strings.Contains(f.String(), rr.Path) {
			t.Fatalf("%s: the rendered finding must name the record; got %q", rr.Path, f.String())
		}
	}

	if rep.Valid() {
		t.Fatalf("the corpus contains faults, so the report must not be valid")
	}
	invalid := rep.InvalidRecords()
	if len(invalid) != 6 {
		t.Fatalf("expected 6 invalid records named, got %v", invalid)
	}
}

// TestValidate_AbsentVsEmptyVsNull covers FR-007 at the validation layer and
// §8 R-3, using DS-1's `""` row.
func TestValidate_AbsentVsEmptyVsNull(t *testing.T) {
	set := loadSet(t, map[string]string{"widget.yaml": `
schema_version: 1
type: widget
properties:
  name: { type: text, required: true }
  note: { type: text }
`})
	widget, _ := set.Get("widget")
	noteProp, _ := widget.Property("note")

	cases := []struct {
		name  string
		src   string
		state PropertyState
	}{
		{"key missing entirely", "---\ntype: widget\nname: A\n---\n", StateAbsent},
		{"key present with no value", "---\ntype: widget\nname: A\nnote:\n---\n", StateAbsent},
		{"key present with explicit null", "---\ntype: widget\nname: A\nnote: null\n---\n", StateAbsent},
		{"empty string is a VALUE", "---\ntype: widget\nname: A\nnote: \"\"\n---\n", StatePresent},
		{"an ordinary value", "---\ntype: widget\nname: A\nnote: hello\n---\n", StatePresent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := ParseRecord("x.md", []byte(tc.src))
			pv := ResolveProperty(rec, noteProp)
			if pv.State != tc.state {
				t.Fatalf("FR-007 / R-3: expected state %v, got %v", tc.state, pv.State)
			}
		})
	}

	t.Run("FR-007 a required property declared with no value is absent, and reported so", func(t *testing.T) {
		rec := ParseRecord("x.md", []byte("---\ntype: widget\nname:\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})
		if rep.Valid() {
			t.Fatalf("an empty required key is absence, not a value, and must be reported")
		}
		f := rep.Errors()[0]
		if f.Code != FindingMissingRequired {
			t.Fatalf("expected %q, got %q", FindingMissingRequired, f.Code)
		}
		if f.Got != "absent" {
			t.Fatalf("the finding must say the value was absent; Got = %q", f.Got)
		}
	})

	t.Run("an empty string satisfies a required text property", func(t *testing.T) {
		rec := ParseRecord("x.md", []byte("---\ntype: widget\nname: \"\"\n---\n"))
		if !ValidateRecord(set, rec, ValidateOptions{}).Valid() {
			t.Fatalf("R-3: an empty string is a VALUE, so it satisfies `required`")
		}
	})
}
