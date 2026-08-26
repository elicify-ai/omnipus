// Omnipus — tests for the two ValidateOptions branches, and for the source
// positions the duplicate-list warning reports.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
)

// TestResolveProperty_PositionsSurviveADroppedElement pins the invariant the
// duplicate-list warning (and anything else that reports a position) rests on.
//
// PropertyValue.Values is FILTERED — a non-conforming element is reported and
// then skipped — so an index into Values stops matching the file the moment
// anything above it is dropped. SourceIndex is what keeps the two reconciled,
// and this asserts it directly rather than only through one caller.
func TestResolveProperty_PositionsSurviveADroppedElement(t *testing.T) {
	set := loadSet(t, map[string]string{"widget.yaml": arityFixture})
	sc, ok := set.Get("widget")
	if !ok {
		t.Fatalf("fixture schema did not load")
	}
	tags, ok := sc.Property("tags")
	if !ok {
		t.Fatalf("fixture must declare `tags`")
	}

	// Element 0 is a mapping (non-conforming), elements 1..3 are good values.
	// Everything conforming therefore sits one place lower in Values than in
	// the file.
	rec := ParseRecord("notes/a.md", []byte(
		"---\ntype: widget\nname: A\ntags:\n  - {a: b}\n  - red\n  - green\n  - blue\n---\n"))

	pv := ResolveProperty(rec, tags)

	if pv.State != StateNonConforming {
		t.Fatalf("an element that does not conform makes the property non-conforming; got %s", pv.State)
	}
	if len(pv.Findings) != 1 {
		t.Fatalf("expected exactly one finding for the one bad element, got %d: %v", len(pv.Findings), pv.Findings)
	}
	// The finding for the bad element is produced from the SOURCE list, so it
	// was already correct — assert it so a "fix" that renumbers the source walk
	// cannot pass.
	if pv.Findings[0].ElementIndex != 0 {
		t.Fatalf("the non-conforming element is at source position 0; the finding said %d", pv.Findings[0].ElementIndex)
	}

	if len(pv.Values) != 3 {
		t.Fatalf("three elements conform; got %d values", len(pv.Values))
	}
	if len(pv.SourceIndex) != len(pv.Values) {
		t.Fatalf("SourceIndex must be parallel to Values: %d positions for %d values", len(pv.SourceIndex), len(pv.Values))
	}
	// red/green/blue are at file positions 1, 2, 3 — never 0, 1, 2.
	for i, want := range []int{1, 2, 3} {
		if got := pv.SourcePosition(i); got != want {
			t.Fatalf("Values[%d] (%q) came from source position %d; SourcePosition said %d — a finding built on that number sends the operator to the wrong line",
				i, pv.Values[i].String(), want, got)
		}
	}

	// A hand-built PropertyValue records no positions. It must not panic and
	// must not lie: with nothing filtered, the index IS the position.
	bare := PropertyValue{Property: tags, State: StatePresent, Values: pv.Values}
	if got := bare.SourcePosition(1); got != 1 {
		t.Fatalf("with no recorded positions SourcePosition must fall back to the index; got %d", got)
	}
}

// TestValidate_ReportDuplicateListValues exercises the ReportDuplicateListValues
// branch of ValidateOptions, which no committed test had ever set to true, and
// pins the positions it reports to the SOURCE list.
func TestValidate_ReportDuplicateListValues(t *testing.T) {
	set := loadSet(t, map[string]string{"widget.yaml": arityFixture})

	t.Run("off by default", func(t *testing.T) {
		rec := ParseRecord("notes/dup.md", []byte("---\ntype: widget\nname: A\ntags: [red, red]\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})
		if len(rep.Findings) != 0 {
			t.Fatalf("a repeated list value is legal and must be silent unless asked for; got %v", rep.Findings)
		}
	})

	t.Run("on, it warns and names both positions", func(t *testing.T) {
		rec := ParseRecord("notes/dup.md", []byte("---\ntype: widget\nname: A\ntags: [red, red]\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{ReportDuplicateListValues: true})

		if !rep.Valid() {
			t.Fatalf("a repeat is a WARNING, not an error; the record must stay valid. findings: %v", rep.Findings)
		}
		if len(rep.Findings) != 1 {
			t.Fatalf("expected exactly one warning, got %d: %v", len(rep.Findings), rep.Findings)
		}
		f := rep.Findings[0]
		if f.Code != FindingDuplicateListValue {
			t.Fatalf("expected %q, got %q (%s)", FindingDuplicateListValue, f.Code, f.Reason)
		}
		if f.Severity != SeverityWarning {
			t.Fatalf("a repeat must be a warning, got %q", f.Severity)
		}
		if f.Property != "tags" || f.RecordPath != "notes/dup.md" {
			t.Fatalf("the warning must name the property and the record; got %q on %q", f.Property, f.RecordPath)
		}
		if f.ElementIndex != 1 {
			t.Fatalf("the second `red` is at position 1; the warning said %d", f.ElementIndex)
		}
		if !strings.Contains(f.Reason, "positions 0 and 1") {
			t.Fatalf("the reason must name both positions; got %q", f.Reason)
		}
		if f.Got != "red" {
			t.Fatalf("the warning must say WHICH value repeated; Got = %q", f.Got)
		}
	})

	t.Run("a dropped element above the duplicates does not shift the reported positions", func(t *testing.T) {
		// THE REGRESSION. `tags` element 0 is a mapping — reported and skipped
		// — so the two `green`s sit at Values indexes 0 and 1 while the file
		// has them at 1 and 2. Reporting the Values indexes told the operator
		// to look at the wrong two lines of their own note.
		rec := ParseRecord("notes/shifted.md", []byte(
			"---\ntype: widget\nname: A\ntags:\n  - {a: b}\n  - green\n  - green\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{ReportDuplicateListValues: true})

		var dup *Finding
		for i := range rep.Findings {
			if rep.Findings[i].Code == FindingDuplicateListValue {
				dup = &rep.Findings[i]
			}
		}
		if dup == nil {
			t.Fatalf("the two conforming `green`s are still a duplicate and must still be warned about; findings: %v", rep.Findings)
		}
		if dup.ElementIndex != 2 {
			t.Fatalf("the second `green` is at SOURCE position 2 (the mapping occupies 0); the warning said %d — that is a different line of the operator's file", dup.ElementIndex)
		}
		if !strings.Contains(dup.Reason, "positions 1 and 2") {
			t.Fatalf("both positions must be source positions, so `positions 1 and 2`; got %q", dup.Reason)
		}
		if strings.Contains(dup.Reason, "positions 0 and 1") {
			t.Fatalf("`positions 0 and 1` are the indexes into the FILTERED value slice, not the file; got %q", dup.Reason)
		}

		// The bad element is still an error in its own right — the warning
		// must not have displaced it.
		if rep.Valid() {
			t.Fatalf("the mapping element is non-conforming and must still fail the record; findings: %v", rep.Findings)
		}
	})

	t.Run("distinct values and scalars are never warned about", func(t *testing.T) {
		for _, body := range []string{
			"---\ntype: widget\nname: A\ntags: [red, green, blue]\n---\n",
			"---\ntype: widget\nname: A\nsegment: vendor\n---\n",
		} {
			rec := ParseRecord("notes/clean.md", []byte(body))
			rep := ValidateRecord(set, rec, ValidateOptions{ReportDuplicateListValues: true})
			if len(rep.Findings) != 0 {
				t.Fatalf("no duplicate exists in %q; got %v", body, rep.Findings)
			}
		}
	})
}

// TestValidate_ReportUndeclaredProperties exercises the other ValidateOptions
// branch no committed test had ever set to true.
//
// The default matters as much as the behaviour: ADR-068 D8 keeps this OFF
// because a real vault's notes carry `tags`, `aliases`, `cssclasses` and a
// decade of the operator's own conventions, and reporting every one of them
// would bury the faults that matter.
func TestValidate_ReportUndeclaredProperties(t *testing.T) {
	set := loadSet(t, map[string]string{"widget.yaml": arityFixture})

	body := "---\ntype: widget\nid: WI-0001\nname: A\naliases: [Acme, ACME]\ncssclasses: wide\n---\n"

	t.Run("off by default", func(t *testing.T) {
		rep := ValidateRecord(set, ParseRecord("notes/a.md", []byte(body)), ValidateOptions{})
		if len(rep.Findings) != 0 {
			t.Fatalf("D8: the operator's own fields are not our business unless asked; got %v", rep.Findings)
		}
	})

	t.Run("on, it warns once per undeclared key and lists the declared names", func(t *testing.T) {
		rep := ValidateRecord(set, ParseRecord("notes/a.md", []byte(body)), ValidateOptions{ReportUndeclaredProperties: true})

		if !rep.Valid() {
			t.Fatalf("an undeclared key is a WARNING; the record must stay valid. findings: %v", rep.Findings)
		}
		got := map[string]Finding{}
		for _, f := range rep.Findings {
			if f.Code != FindingUndeclaredProperty {
				t.Fatalf("unexpected finding %q: %s", f.Code, f.Reason)
			}
			if f.Severity != SeverityWarning {
				t.Fatalf("undeclared keys are warnings, got %q for %q", f.Severity, f.Property)
			}
			if _, twice := got[f.Property]; twice {
				t.Fatalf("property %q was warned about more than once", f.Property)
			}
			got[f.Property] = f
		}
		for _, want := range []string{"aliases", "cssclasses"} {
			if _, ok := got[want]; !ok {
				t.Fatalf("expected a warning for undeclared key %q; got warnings for %v", want, keysOf(got))
			}
		}
		// `type` and `id` are the record's own machinery, not undeclared
		// properties — warning about them would be noise on EVERY record.
		for _, never := range []string{RecordTypeKey, RecordIDKey, "name"} {
			if _, bad := got[never]; bad {
				t.Fatalf("%q must never be reported as undeclared", never)
			}
		}
		if len(got) != 2 {
			t.Fatalf("expected exactly two warnings, got %d: %v", len(got), keysOf(got))
		}

		f := got["aliases"]
		if f.RecordPath != "notes/a.md" || f.RecordType != "widget" || f.RecordID != "WI-0001" {
			t.Fatalf("the warning must carry the record's identity; got path=%q type=%q id=%q", f.RecordPath, f.RecordType, f.RecordID)
		}
		if f.Line == 0 {
			t.Fatalf("the warning must name the line the key is on so the operator can find it; Line was 0")
		}
		// FR-024's "valid names listed" — a warning that does not say what IS
		// declared makes the operator go and read the schema file.
		for _, declared := range []string{"name", "segment", "tags"} {
			if !strings.Contains(f.Expected, declared) {
				t.Fatalf("the warning must list the declared property names; %q is missing from %q", declared, f.Expected)
			}
		}
		if !strings.Contains(f.Reason, "aliases") {
			t.Fatalf("the reason must name the key; got %q", f.Reason)
		}
	})

	t.Run("an unrecognised note is never scanned for undeclared keys", func(t *testing.T) {
		// FR-005: a note whose type no schema declares is an ORDINARY NOTE.
		// Turning the option on must not start reporting every key in the vault.
		rec := ParseRecord("notes/ordinary.md", []byte("---\ntype: diary\nmood: fine\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{ReportUndeclaredProperties: true})
		if rep.Recognised {
			t.Fatalf("`diary` is declared by no schema; it must not be recognised")
		}
		if len(rep.Findings) != 0 {
			t.Fatalf("FR-005: an ordinary note produces zero findings; got %v", rep.Findings)
		}
	})
}

func keysOf(m map[string]Finding) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
