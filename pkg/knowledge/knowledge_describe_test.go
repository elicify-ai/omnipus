// Omnipus — tests for knowledge_describe (spec §4.1.1, AC-D1..D6, FR-072, FR-079).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// Fixture — a vault with two record types, a saved view and a template
//
// R-F: every record type, property, value and view name below is a fixture
// THIS TEST declares. The product ships none of them.
// ---------------------------------------------------------------------------

func describeFixtureVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	write := func(rel, body string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	write(".omnipus-vault/records/widget.yaml", `
schema_version: 1
type: widget
label: Widget
identity:
  prefix: WI
properties:
  name:     { type: text, required: true }
  state:    { type: enum, values: [draft, shipped, withdrawn] }
  tags:     { type: enum, many: true, values: [alpha, beta] }
  maker:    { type: relation, to: foundry }
  weight:   { type: decimal, unit: kg }
  batch:    { type: integer }
  shipped_on: { type: date }
`)
	write(".omnipus-vault/records/foundry.yaml", `
schema_version: 1
type: foundry
properties:
  name:   { type: text }
  region: { type: enum, values: [north, south] }
  lead:   { type: person }
`)
	write(".omnipus-vault/views/shipped-by-maker.yaml", `
name: shipped-by-maker
type: widget
label: Shipped widgets by maker
filter: {property: state, op: "=", value: shipped}
grouping:
  - {property: maker}
sort:
  - {property: weight, direction: desc}
properties: [name, state, weight]
limit: 50
`)
	write(".omnipus-vault/templates/widget.md", "---\ntype: widget\nname: {{title}}\n---\n\n# {{title}}\n\nCreated {{date}}.\n")

	write("Widgets/Gear.md", "---\ntype: widget\n---\nSee [[Acme Ltd]].\n")
	write("Foundries/Acme Ltd.md", "---\ntype: foundry\n---\nMakes [[Gear]].\n")
	return root
}

// describeFixtureData assembles the render input for the fixture vault.
func describeFixtureData(t *testing.T, root string, integrity *IntegrityReport) DescribeData {
	t.Helper()
	schemas, schemaReport, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !schemaReport.OK() {
		t.Fatalf("fixture schemas did not load: %v", schemaReport.Rejections)
	}
	views, viewReport, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if !viewReport.OK() {
		t.Fatalf("fixture views did not load: %v", viewReport.Rejections)
	}
	col, err := OpenCollection(root)
	if err != nil {
		t.Fatalf("OpenCollection: %v", err)
	}
	templates, err := ListTemplates(OSLinkFS(), col)
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	return DescribeData{
		Collection:         "workbench",
		CollectionsInScope: []string{"workbench"},
		ManifestCount:      2,
		ManifestKnown:      true,
		Schemas:            schemas,
		SchemaReport:       schemaReport,
		Views:              views,
		ViewReport:         viewReport,
		Templates:          templates,
		TemplatesDir:       col.TemplatesDir(),
		Integrity:          integrity,
	}
}

// ---------------------------------------------------------------------------
// The response artifact
// ---------------------------------------------------------------------------

// TestDescribe_RenderedArtifact is the definition-of-done artifact: the literal
// text a model sees for a vault with two record types, a saved view and a
// template.
//
// It asserts every FACT the response must carry rather than diffing a golden
// blob — a golden blob turns every wording improvement into a test failure and
// teaches the next person to re-bless it without reading it, which is how a
// golden test stops testing anything.
func TestDescribe_RenderedArtifact(t *testing.T) {
	root := describeFixtureVault(t)
	text := RenderDescribe(describeFixtureData(t, root, nil))
	t.Logf("\n----- BEGIN knowledge_describe RESPONSE -----\n%s----- END knowledge_describe RESPONSE -----", text)

	// Column padding is a rendering choice that depends on the longest
	// property name in the fixture. Asserting it would make this test fail on
	// a fixture edit that changed nothing about the CONTRACT, so the property
	// lines are matched with runs of spaces collapsed.
	flat := collapseSpaces(text)

	must := []struct{ what, want string }{
		{"the collection and its index state", "VAULT workbench"},
		{"the collections in scope, so the next call is never a guess", "COLLECTIONS in scope (1): workbench"},
		{"both record types", "TYPES (2)"},
		{"FR-036b: the id prefix, as SCHEMA DATA", "widget id WI-<n>"},
		{"a type's label", "'Widget'"},
		{"a required property", "required"},
		{"FR-004: integer rendered distinctly", "batch     integer"},
		{"FR-004: decimal rendered distinctly, with its unit", "weight    decimal in kg"},
		{"an enum's declared values", "draft | shipped | withdrawn"},
		{"declared arity on a many property", "tags      enum many"},
		{"a relation's target type", "maker     relation -> foundry"},
		{"the saved view, by name and type", "shipped-by-maker  type widget"},
		{"the view's filter, so it need not be opened", "filter state = shipped"},
		{"the view's grouping and sort", "group maker asc; sort weight desc"},
		{"the view's page size", "limit 50"},
		{"the nudge that a view may already exist", "before inventing a filter"},
		{"the templates directory", ".omnipus-vault/templates/"},
		{"FR-047a's closed token set", "{{title}} {{date}} {{time}} {{datetime}}"},
		{"the template itself", "widget.md"},
	}
	for _, c := range must {
		if !strings.Contains(flat, collapseSpaces(c.want)) {
			t.Errorf("the response must carry %s — %q missing from:\n%s", c.what, c.want, text)
		}
	}
}

// collapseSpaces reduces every run of spaces to one, so an assertion about
// WHAT a line says is not also an assertion about how wide a column is.
func collapseSpaces(s string) string {
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}

// TestDescribe_ResponseIsNeverAJSONDocument — AC-D3, FR-072.
//
// The rule is about the ENVELOPE. This asserts the whole response is not
// parseable as a JSON document, which is the checkable form of "never a JSON
// document" and does not forbid a brace appearing inside a template token an
// operator typed.
func TestDescribe_ResponseIsNeverAJSONDocument(t *testing.T) {
	root := describeFixtureVault(t)
	text := RenderDescribe(describeFixtureData(t, root, nil))

	var anything any
	if err := json.Unmarshal([]byte(text), &anything); err == nil {
		t.Fatalf("AC-D3: the response parsed as a JSON document:\n%s", text)
	}
	if strings.Contains(text, `":`) {
		t.Errorf("AC-D3: the response contains JSON key syntax:\n%s", text)
	}
}

// TestDescribe_SectionOrderIsTheSpecifiedOne — spec §4.1.1 states the order
// normatively: index freshness -> collections -> record types -> saved views ->
// templates -> integrity.
func TestDescribe_SectionOrderIsTheSpecifiedOne(t *testing.T) {
	root := describeFixtureVault(t)
	d := describeFixtureData(t, root, &IntegrityReport{
		ScopeLabel: "whole vault",
		NotesSwept: 2,
		Categories: newFindingSink(IntegrityFindingsPerCategory).results(),
	})
	text := RenderDescribe(d)

	order := []string{"VAULT ", "COLLECTIONS in scope", "TYPES (", "VIEWS (", "TEMPLATES (", "INTEGRITY:"}
	at := -1
	for _, marker := range order {
		i := strings.Index(text, marker)
		if i < 0 {
			t.Fatalf("section marker %q is missing from:\n%s", marker, text)
		}
		if i < at {
			t.Errorf("section %q rendered out of the order spec 4.1.1 states:\n%s", marker, text)
		}
		at = i
	}
}

// TestDescribe_IncludeTrimsSections — the `include` parameter.
func TestDescribe_IncludeTrimsSections(t *testing.T) {
	root := describeFixtureVault(t)
	d := describeFixtureData(t, root, nil)
	d.Sections = map[string]bool{DescribeSectionViews: true}
	text := RenderDescribe(d)

	if !strings.Contains(text, "VIEWS (") {
		t.Errorf("the requested section is missing:\n%s", text)
	}
	for _, unwanted := range []string{"TYPES (", "TEMPLATES (", "VAULT "} {
		if strings.Contains(text, unwanted) {
			t.Errorf("include must TRIM the response; %q survived:\n%s", unwanted, text)
		}
	}
}

// TestDescribe_MinimalDetailOmitsEnumValues — the `detail` parameter, whose
// whole purpose is the token budget.
func TestDescribe_MinimalDetailOmitsEnumValues(t *testing.T) {
	root := describeFixtureVault(t)
	d := describeFixtureData(t, root, nil)

	standard := RenderDescribe(d)
	d.Detail = DetailMinimal
	minimal := RenderDescribe(d)

	if !strings.Contains(standard, "draft | shipped | withdrawn") {
		t.Fatalf("standard detail must list enum values:\n%s", standard)
	}
	if strings.Contains(minimal, "draft | shipped | withdrawn") {
		t.Errorf("minimal detail must omit enum value lists:\n%s", minimal)
	}
	if !strings.Contains(collapseSpaces(minimal), "state enum") {
		t.Errorf("minimal detail must still name the property and its type:\n%s", minimal)
	}
	if len(minimal) >= len(standard) {
		t.Errorf("minimal (%d bytes) must be smaller than standard (%d bytes)", len(minimal), len(standard))
	}
}

// ratioPattern matches a rendered "<n> of <n>" denominator claim.
var ratioPattern = regexp.MustCompile(`[0-9][0-9,]* of [0-9]`)

// TestDescribe_IndexFreshnessNeverInventsARatio — FR-036. "0 of 0" cannot be
// produced from this package, and a description rendered during a first index
// must say it is a fraction of the vault.
func TestDescribe_IndexFreshnessNeverInventsARatio(t *testing.T) {
	cases := []struct {
		name    string
		data    DescribeData
		want    string
		noRatio bool
	}{
		{
			name: "never indexed",
			data: DescribeData{ManifestKnown: false},
			want: "NOT INDEXED yet",
		},
		{
			name: "indexed and empty is not the same as never indexed",
			data: DescribeData{ManifestKnown: true, ManifestCount: 0},
			want: "indexed and empty",
		},
		{
			name: "enumerating, total unknown",
			data: DescribeData{IndexProgress: IndexProgress{Phase: IndexPhaseEnumerating, Found: 7}},
			want: "total not yet known",
			// FR-036 forbids inventing a denominator. The pattern is a RATIO
			// — digits, "of", digits — not the word "of", which appears
			// legitimately in "a fraction of this vault".
			noRatio: true,
		},
		{
			name: "indexing with a measured total",
			data: DescribeData{IndexProgress: IndexProgress{
				Phase: IndexPhaseIndexing, Indexed: 412, Total: 1204, TotalKnown: true}},
			want: "412 of 1,204 notes",
		},
		{
			// WORDING CHANGED 2026-09-02, INTENT UNCHANGED. This case has always
			// asserted that a count mismatch is REPORTED; it used to do so by
			// matching the phrase "the two disagree", which was part of a
			// sentence ending "re-index to reconcile" — a remedy that does not
			// exist anywhere in the product (no agent tool, no CLI verb, no REST
			// endpoint indexes a collection on demand). Naming a fake remedy is
			// the defect a code review raised, so the sentence was replaced.
			//
			// The assertion now matches the SUBSTANCE rather than the phrasing:
			// both counts must appear, so a reader can see the gap. The
			// companion assertion below forbids the fake remedy returning.
			name: "the two counts disagree",
			data: DescribeData{ManifestKnown: true, ManifestCount: 1200, NotesOnDisk: 1210, NotesCounted: true},
			want: "1,200 of 1,210",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := indexFreshness(tc.data)
			if !strings.Contains(got, tc.want) {
				t.Errorf("expected %q in %q", tc.want, got)
			}
			if tc.noRatio && ratioPattern.MatchString(got) {
				t.Errorf("FR-036 forbids stating a ratio when the total is unknown; got %q", got)
			}
			// NO MESSAGE FROM THIS FUNCTION MAY NAME AN ACTION THAT DOES NOT
			// EXIST. There is no agent tool, CLI verb or REST endpoint that
			// re-indexes a collection on demand, so telling an operator to
			// "re-index" sends them to do something impossible — which is worse
			// than saying nothing, because it reads as actionable. Applied to
			// every case rather than one, so a future edit cannot reintroduce it
			// through a branch this table does not yet cover.
			if strings.Contains(strings.ToLower(got), "re-index") {
				t.Errorf("message names a remedy that does not exist (%q); state the situation and its consequence instead", got)
			}
		})
	}
}

// TestDescribe_EmptyVaultSaysSoRatherThanRenderingNothing.
//
// A section that renders nothing and a section with nothing to report are
// indistinguishable to a reader, and one of them is a bug.
func TestDescribe_EmptyVaultSaysSoRatherThanRenderingNothing(t *testing.T) {
	text := RenderDescribe(DescribeData{
		Collection:    "empty",
		ManifestKnown: true,
		Schemas:       records.NewSchemaSet(),
		Views:         records.NewViewSet(),
	})
	for _, want := range []string{
		"TYPES (0)",
		"every note in it is an ordinary note",
		"VIEWS (0)",
		"TEMPLATES (0)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%q missing from the empty-vault response:\n%s", want, text)
		}
	}
}

// TestDescribe_RejectedSchemasAndViewsAreReported.
//
// A schema that failed to load enforces NOTHING, and a description that
// silently omits it tells the agent this vault has fewer record types than the
// operator thinks it has.
func TestDescribe_RejectedSchemasAndViewsAreReported(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	write(".omnipus-vault/records/broken.yaml", "type: broken\nproperties:\n  a: {type: text}\n")
	write(".omnipus-vault/views/broken.yaml", "name: v\ntype: broken\n")

	schemas, schemaReport, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	views, viewReport, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	text := RenderDescribe(DescribeData{
		Collection: "c", ManifestKnown: true,
		Schemas: schemas, SchemaReport: schemaReport,
		Views: views, ViewReport: viewReport,
	})
	if !strings.Contains(text, "schema file(s) REJECTED and enforcing nothing") {
		t.Errorf("a rejected schema must be reported, not omitted:\n%s", text)
	}
	if !strings.Contains(text, "saved view(s) REJECTED and unusable") {
		t.Errorf("a rejected view must be reported, not omitted:\n%s", text)
	}
	if !strings.Contains(text, "schema_version") {
		t.Errorf("the rejection must say what is wrong with the file:\n%s", text)
	}
}

// ---------------------------------------------------------------------------
// The tool surface
// ---------------------------------------------------------------------------

// TestDescribeTool_DescriptionFitsTheBudgetAndNamesTheWidestOperation —
// FR-079.
//
// The description is the ONLY thing the model reads before deciding whether to
// call, and it is re-sent on every request. FR-079 caps it at ~150 tokens and
// requires it to name the WIDEST operation it grants, not the most common one.
func TestDescribeTool_DescriptionFitsTheBudgetAndNamesTheWidestOperation(t *testing.T) {
	desc := (&DescribeTool{}).Description()

	// ~4 characters per token is the working approximation for English prose;
	// the exact tokeniser is the provider's. FR-079's ~150 tokens is therefore
	// ~600 characters, and this asserts that ceiling rather than a slack
	// figure — the description is re-sent on EVERY request (FR-079's own
	// correction: there is no per-parameter lazy loading anywhere), so slack
	// here is paid per turn for the life of the product.
	const budgetChars = 600
	if len(desc) > budgetChars {
		t.Errorf("FR-079 budgets ~150 tokens (~%d chars) for a tool description; this one is %d chars:\n%s",
			budgetChars, len(desc), desc)
	}
	for _, want := range []string{"check_integrity", "WHOLE vault", "SAVED VIEWS", "Call this first"} {
		if !strings.Contains(desc, want) {
			t.Errorf("FR-079: the description must name its widest operation and why it is called first; "+
				"%q missing from:\n%s", want, desc)
		}
	}
	if (&DescribeTool{}).Name() != "knowledge_describe" {
		t.Errorf("the tool name is contract: got %q", (&DescribeTool{}).Name())
	}
}

// TestDescribeTool_ParametersAreExactlyTheSpecifiedFive.
func TestDescribeTool_ParametersAreExactlyTheSpecifiedFive(t *testing.T) {
	schema := (&DescribeTool{}).Parameters()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("the parameter schema declares no properties: %v", schema)
	}
	want := map[string]bool{
		"collection": true, "record_type": true, "include": true,
		"check_integrity": true, "detail": true,
	}
	for name := range props {
		if !want[name] {
			t.Errorf("undeclared parameter %q — spec 4.1.1 lists exactly five", name)
		}
		delete(want, name)
	}
	for name := range want {
		t.Errorf("parameter %q is specified and missing", name)
	}
	// The argument allow-list and the schema must agree, or an argument the
	// schema advertises is refused as unknown.
	for _, a := range describeArgNames {
		if _, ok := props[a]; !ok {
			t.Errorf("describeArgNames accepts %q but the schema does not declare it", a)
		}
	}
}

// TestDescribeTool_UnknownArgumentIsRefusedWithTheAcceptedOnesListed.
//
// A silently ignored argument is a caller that believes it narrowed something.
func TestDescribeTool_UnknownArgumentIsRefusedWithTheAcceptedOnesListed(t *testing.T) {
	tool := NewDescribeTool(ToolDeps{Home: t.TempDir()}, nil)
	res := tool.Execute(context.Background(), map[string]any{"record_types": "widget"})
	if res == nil || !isErrorResult(res) {
		t.Fatalf("an unknown argument must be refused, got %+v", res)
	}
	msg := resultText(res)
	for _, want := range []string{"record_types", "record_type", "collection", "check_integrity", "detail"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal must list the accepted argument names; %q missing from %q", want, msg)
		}
	}
}

// TestDescribeTool_UnknownIncludeSectionIsRefused.
func TestDescribeTool_UnknownIncludeSectionIsRefused(t *testing.T) {
	tool := NewDescribeTool(ToolDeps{Home: t.TempDir()}, nil)
	res := tool.Execute(context.Background(), map[string]any{"include": []any{"schemas"}})
	if res == nil || !isErrorResult(res) {
		t.Fatalf("an unknown include section must be refused, got %+v", res)
	}
	msg := resultText(res)
	for _, want := range append([]string{"schemas"}, describeSectionOrder...) {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal must list the accepted sections; %q missing from %q", want, msg)
		}
	}
}

// TestDescribeTool_UnknownDetailIsRefused.
func TestDescribeTool_UnknownDetailIsRefused(t *testing.T) {
	tool := NewDescribeTool(ToolDeps{Home: t.TempDir()}, nil)
	res := tool.Execute(context.Background(), map[string]any{"detail": "verbose"})
	if res == nil || !isErrorResult(res) {
		t.Fatalf("an unknown detail level must be refused, got %+v", res)
	}
	msg := resultText(res)
	for _, want := range []string{"verbose", DetailStandard, DetailMinimal} {
		if !strings.Contains(msg, want) {
			t.Errorf("%q missing from %q", want, msg)
		}
	}
}

// TestDescribeTool_UnknownCollectionIsRefusedNamingTheOnesInScope.
//
// This is the measured defect the tool exists to remove: today the first call
// is a coin flip on the collection name, and the only way to learn the valid
// names is to get one wrong. The refusal must therefore carry them.
func TestDescribeTool_UnknownCollectionIsRefusedNamingTheOnesInScope(t *testing.T) {
	tool := NewDescribeTool(ToolDeps{Home: t.TempDir()}, nil)
	res := tool.Execute(context.Background(), map[string]any{"collection": "nope"})
	if res == nil || !isErrorResult(res) {
		t.Fatalf("an unmounted collection must be refused, got %+v", res)
	}
	msg := resultText(res)
	if !strings.Contains(msg, "nope") || !strings.Contains(msg, "in scope") {
		t.Errorf("the refusal must name what was asked for and what is available; got %q", msg)
	}
	if !strings.Contains(msg, "(none)") {
		t.Errorf("an EMPTY list must say so in words — a message trailing off after 'in scope: ' "+
			"reads as truncated and the caller cannot tell which it got; got %q", msg)
	}
}

// TestDescribeTool_UnknownRecordTypeIsRefusedNotAnsweredEmpty — AC-D2.
func TestDescribeTool_UnknownRecordTypeIsRefusedNotAnsweredEmpty(t *testing.T) {
	root := describeFixtureVault(t)
	schemas, _, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	rej := &UnknownRecordTypeError{Requested: "widgt", Declared: schemas.Types()}
	for _, want := range []string{"widgt", "widget", "foundry"} {
		if !strings.Contains(rej.Error(), want) {
			t.Errorf("AC-D2 requires the declared type names to be listed; %q missing from %q", want, rej.Error())
		}
	}
}

// TestDescribe_OnlyTypeNarrowsTheTypesSection.
func TestDescribe_OnlyTypeNarrowsTheTypesSection(t *testing.T) {
	root := describeFixtureVault(t)
	d := describeFixtureData(t, root, nil)
	d.OnlyType = "foundry"
	text := RenderDescribe(d)

	if !strings.Contains(text, "  foundry") {
		t.Errorf("the requested type is missing:\n%s", text)
	}
	if strings.Contains(text, "widget id WI-<n>") {
		t.Errorf("record_type must narrow the TYPES section; the other type survived:\n%s", text)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func isErrorResult(r *tools.ToolResult) bool {
	return r != nil && r.IsError
}

func resultText(r *tools.ToolResult) string {
	if r == nil {
		return ""
	}
	return r.ContentForLLM()
}

// TestDescribe_IntegrityRenderedArtifact is the second definition-of-done
// artifact: the literal text a model sees when check_integrity finds
// something, over a vault carrying one of every category.
//
// It asserts the SHAPE of each line rather than diffing a blob, for the reason
// given on TestDescribe_RenderedArtifact.
func TestDescribe_IntegrityRenderedArtifact(t *testing.T) {
	root := describeFixtureVault(t)

	// A note nothing links to, and one whose link goes nowhere.
	write := func(rel, body string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	write("Notes/2026-08-14.md", "see [[Q2 retro]]\n")
	write("Notes/scratch.md", "nothing here\n")

	schemas, _, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	report, err := CheckIntegrity(context.Background(), IntegrityOptions{
		FS: OSLinkFS(), Root: mustCollectionRoot(t, root),
		CollectionName: "workbench", Schemas: schemas,
		Store: &fakePropertyIndex{
			records: []IndexedRecord{
				{Path: "Widgets/Gear.md", RecordType: "widget", RecordID: "WI-0142"},
				{Path: "Widgets/Gear copy.md", RecordType: "widget", RecordID: "WI-0142"},
				{Path: "Foundries/Acme Ltd.md", RecordType: "foundry", RecordID: "FO-0001"},
				{Path: "Widgets/deleted.md", RecordType: "widget", RecordID: "WI-0221"},
			},
			relations: []IndexedRelation{
				{Path: "Widgets/Gear.md", RecordID: "WI-0142", Property: "maker", Target: "Acme Corp."},
				{Path: "Widgets/Gear.md", RecordID: "WI-0142", Property: "maker", Target: "2026-08-14"},
			},
		},
	})
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}

	d := describeFixtureData(t, root, report)
	d.Sections = map[string]bool{} // integrity only, so the artifact is readable
	text := RenderDescribe(d)
	t.Logf("\n----- BEGIN check_integrity RESPONSE -----\n%s----- END check_integrity RESPONSE -----", text)

	must := []struct{ what, want string }{
		{"the header, its scope and the notes swept", "INTEGRITY: 8 finding(s) (scope: workbench, 4 notes swept)"},
		{"AC-D1: the category", "duplicate id"},
		{"AC-D1: both paths, and that neither is preferred", "WI-0142 — Widgets/Gear copy.md and Widgets/Gear.md; neither is preferred"},
		{"FR-033: a relation resolving to nothing", "WI-0142 maker -> [[Acme Corp.]] — no note resolves"},
		{"FR-034: a relation resolving to the wrong type", "expected foundry"},
		{"FR-020c: an index row whose note is gone", "properties index holds WI-0221 at Widgets/deleted.md"},
		{"AC-D5: an ordinary broken wikilink", "ordinary wikilink, not a relation"},
		{"a note nothing links to", "Notes/scratch.md — no note links to it"},
	}
	for _, c := range must {
		if !strings.Contains(text, c.want) {
			t.Errorf("the integrity report must carry %s — %q missing from:\n%s", c.what, c.want, text)
		}
	}
}
