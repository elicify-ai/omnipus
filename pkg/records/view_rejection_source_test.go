// Omnipus — tests for ViewRejection.Source, the field that lets a caller say
// "and N more views from this base could not be loaded" rather than quietly
// showing fewer views than the base has.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import "testing"

// A view file that fails to load still names the base it came from, so a
// caller enumerating one base's views can COUNT it. Without this the count
// would have to be guessed from the filename spelling, which is the
// importer's convenience and not an index.
func TestViewRejection_CarriesTheDeclaredSource(t *testing.T) {
	root := writeVaultView(t, "", "invoices--broken.yaml",
		"name: invoices--broken\ntype: widget\ngroup-by: colour\nsource: CRM/Invoices.base\n")
	_, schemas := viewFixtureSchemas(t, root)

	set, report, err := LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if set.Len() != 0 {
		t.Fatalf("an unknown key must be refused, not loaded; got %v", set.Names())
	}
	if len(report.Rejections) != 1 {
		t.Fatalf("expected one rejection, got %v", report.Rejections)
	}
	if got := report.Rejections[0].Source; got != "CRM/Invoices.base" {
		t.Fatalf("a rejection must carry the base it came from; got %q", got)
	}
}

// A view somebody authored declares no source, and must not be attributed to
// any base — otherwise "" would match a base whose source failed to resolve.
func TestViewRejection_AuthoredViewHasNoSource(t *testing.T) {
	root := writeVaultView(t, "", "hand-written.yaml",
		"name: hand-written\ntype: widget\ngroup-by: colour\n")
	_, schemas := viewFixtureSchemas(t, root)

	_, report, err := LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if len(report.Rejections) != 1 {
		t.Fatalf("expected one rejection, got %v", report.Rejections)
	}
	if got := report.Rejections[0].Source; got != "" {
		t.Fatalf("a view with no `source` must be attributed to no base; got %q", got)
	}
}

// A duplicate-name conflict rejects SEVERAL files at once. When they disagree
// about which base they came from, the group is attributed to NONE — an
// under-count that cannot mislead, rather than blaming one member's base for a
// file that came from somewhere else.
func TestViewRejection_DuplicateGroupWithMixedSourcesIsAttributedToNoBase(t *testing.T) {
	root := writeVaultView(t, "", "one.yaml",
		"name: clash\ntype: widget\nsource: CRM/Invoices.base\n")
	writeVaultView(t, root, "two.yaml",
		"name: clash\ntype: widget\nsource: CRM/Deals.base\n")
	_, schemas := viewFixtureSchemas(t, root)

	_, report, err := LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if len(report.Rejections) != 1 {
		t.Fatalf("expected one duplicate rejection, got %v", report.Rejections)
	}
	rej := report.Rejections[0]
	if rej.Code != RejectViewDuplicateName {
		t.Fatalf("expected a duplicate-name rejection, got %s", rej.Code)
	}
	if rej.Source != "" {
		t.Fatalf("a duplicate group whose members name different bases must be attributed to none; got %q", rej.Source)
	}
}

// When every member of a duplicate group DOES agree, the group is attributed
// to that base — both files are that base's, and both failed to load.
func TestViewRejection_DuplicateGroupWithOneSourceIsAttributedToIt(t *testing.T) {
	root := writeVaultView(t, "", "one.yaml",
		"name: clash\ntype: widget\nsource: CRM/Invoices.base\n")
	writeVaultView(t, root, "two.yaml",
		"name: clash\ntype: widget\nsource: CRM/Invoices.base\n")
	_, schemas := viewFixtureSchemas(t, root)

	_, report, err := LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if len(report.Rejections) != 1 {
		t.Fatalf("expected one duplicate rejection, got %v", report.Rejections)
	}
	rej := report.Rejections[0]
	if rej.Source != "CRM/Invoices.base" {
		t.Fatalf("expected the shared source, got %q", rej.Source)
	}
	if len(rej.Paths) != 2 {
		t.Fatalf("a duplicate rejection must name every file it refuses; got %v", rej.Paths)
	}
}
