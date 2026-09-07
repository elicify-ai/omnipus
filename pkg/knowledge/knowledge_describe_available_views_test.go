// Omnipus — tests wiring view_kinds.go's availability rules into
// knowledge_describe's TYPES section (view-kinds-design-2026-09-03 §6.2: "the
// agent asks, it does not remember"). view_kinds_test.go covers the gate
// rules themselves in isolation; these tests cover only that RenderDescribe
// actually emits the block, in the right place, and honours DetailMinimal
// the same way the rest of the TYPES section does.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// describeAvailableViewsFixture loads the rich fixture (view_kinds_test.go's
// richFixtureSchema) as a full SchemaSet, so RenderDescribe's real TYPES path
// (which walks d.Schemas.Types(), not one Schema in isolation) is exercised.
func describeAvailableViewsFixture(t *testing.T) DescribeData {
	t.Helper()
	root := t.TempDir()
	writeUnderMarker(t, root, "records", "invoice.yaml", `
schema_version: 1
type: invoice
properties:
  name:     { type: text, required: true }
  amount:   { type: decimal, unit_property: currency }
  currency: { type: enum, values: [usd, eur, gbp] }
  due_date: { type: date }
  cover:    { type: text }
`)
	schemas, report, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("fixture schema rejected, proves nothing: %v", report.Rejections)
	}
	return DescribeData{
		Collection:   "workbench",
		Schemas:      schemas,
		SchemaReport: report,
		Sections:     map[string]bool{DescribeSectionTypes: true},
		Detail:       DetailStandard,
	}
}

func TestRenderDescribe_TypesSection_CarriesAvailableViewsBlock(t *testing.T) {
	d := describeAvailableViewsFixture(t)
	text := RenderDescribe(d)
	t.Logf("\n----- BEGIN knowledge_describe TYPES -----\n%s----- END -----", text)

	if !strings.Contains(text, "views you can create here:") {
		t.Fatalf("TYPES section carries no available-views block:\n%s", text)
	}
	// The block must be attached to the record type it describes, not merely
	// present anywhere in the document — assert it follows the type's own
	// header line, before the next type (or end of section) begins.
	typeIdx := strings.Index(text, "  invoice\n")
	viewsIdx := strings.Index(text, "views you can create here:")
	if typeIdx == -1 {
		t.Fatalf("fixture type header not found verbatim in:\n%s", text)
	}
	if viewsIdx < typeIdx {
		t.Fatalf("available-views block rendered before its own type's header:\n%s", text)
	}
	for _, want := range []string{
		"tiles — NO (no image-capable property type exists yet)",
		"board (choice: currency)",
		"summary (number: amount, unit: currency)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("TYPES section missing %q:\n%s", want, text)
		}
	}
}

func TestRenderDescribe_TypesSection_MinimalDetailOmitsAvailableViewsBlock(t *testing.T) {
	d := describeAvailableViewsFixture(t)
	d.Detail = DetailMinimal
	text := RenderDescribe(d)

	if strings.Contains(text, "views you can create here:") {
		t.Fatalf("DetailMinimal must omit the available-views block (it elaborates on "+
			"properties this section already named, same as the enum-values list it "+
			"already skips at this detail level):\n%s", text)
	}
	// Sanity: the type itself, and its properties, must still be present —
	// DetailMinimal narrows, it does not blank the section.
	if !strings.Contains(text, "  invoice\n") {
		t.Fatalf("DetailMinimal dropped the type header entirely, not just the new block:\n%s", text)
	}
}

func TestRenderDescribe_OnlyType_StillCarriesAvailableViewsBlock(t *testing.T) {
	// DescribeData.OnlyType narrows renderTypes to one declared type; the
	// available-views block must survive that narrowing the same way
	// renderProperties does, since both walk the same loop.
	d := describeAvailableViewsFixture(t)
	d.OnlyType = "invoice"
	text := RenderDescribe(d)

	if !strings.Contains(text, "views you can create here:") {
		t.Fatalf("OnlyType narrowing dropped the available-views block:\n%s", text)
	}
}
