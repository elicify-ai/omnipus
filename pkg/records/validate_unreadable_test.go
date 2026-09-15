// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import "testing"

// TestValidate_UnreadableFrontmatterIsReportedNotDemoted guards a branch that
// was correct but untested — deleting it left the whole suite green.
//
// A note whose frontmatter cannot be read must be REPORTED. If that guard is
// ever lost the record comes back Recognised=false with no findings and
// Valid()==true, which is indistinguishable from a note that was never a
// record at all. It then vanishes from an answer that still reports complete —
// the exact defect ADR-068 exists to remove, reached through the parser rather
// than through a query.
//
// FindingFrontmatterUnreadable was asserted in zero test files before this.
func TestValidate_UnreadableFrontmatterIsReportedNotDemoted(t *testing.T) {
	set, _ := filterSchema(t)

	// A cyclic alias: the frontmatter is syntactically valid YAML that cannot
	// be materialised. See frontmatter_bounds_test.go for why this shape.
	rec := ParseRecord("broken.md", []byte("---\ntype: widget\na: &x\n  - *x\n---\nbody\n"))
	if rec.ParseError == "" {
		t.Fatal("fixture is wrong: this frontmatter must fail to parse")
	}

	rep := ValidateRecord(set, rec, ValidateOptions{})

	if len(rep.Findings) == 0 {
		t.Fatal("an unreadable note must produce a finding. With none it is " +
			"indistinguishable from an ordinary note and disappears silently.")
	}
	found := false
	for _, f := range rep.Findings {
		if f.Code == FindingFrontmatterUnreadable {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a %s finding; got %+v", FindingFrontmatterUnreadable, rep.Findings)
	}
	if rep.Path != "broken.md" {
		t.Fatalf("the report must name the file so it can be fixed; got %q", rep.Path)
	}
	if rep.Valid() {
		t.Fatal("a note whose frontmatter cannot be read must not validate as fine")
	}
}
