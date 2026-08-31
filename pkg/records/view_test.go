// Omnipus — tests for the saved-view read model (ADR-068 D10, spec FR-018).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeVaultView writes one view file into a vault and returns the vault root.
func writeVaultView(t *testing.T, root, filename, body string) string {
	t.Helper()
	if root == "" {
		root = t.TempDir()
	}
	dir := ViewsDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
	return root
}

// viewFixtureSchemas declares the two record types the view fixtures below
// query. R-F: every name here is a fixture this test declares, not vocabulary
// the product ships.
func viewFixtureSchemas(t *testing.T, root string) (string, *SchemaSet) {
	t.Helper()
	root = writeVaultSchema(t, root, "widget.yaml", `
schema_version: 1
type: widget
label: Widget
identity:
  prefix: WI
properties:
  name:    { type: text, required: true }
  state:   { type: enum, values: [draft, shipped, withdrawn] }
  maker:   { type: relation, to: foundry }
  batch:   { type: integer }
`)
	root = writeVaultSchema(t, root, "foundry.yaml", `
schema_version: 1
type: foundry
properties:
  name:   { type: text }
  region: { type: enum, values: [north, south] }
`)
	set, report, err := LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("fixture schemas did not load: %v", report.Rejections)
	}
	return root, set
}

// TestView_LoadPathIsTheContractsPath pins the literal directory the contract
// names (the ViewDef schema in contracts/openapi.yaml: "it lives in
// `<vault>/.omnipus-vault/views/<name>.yaml`"),
// so a refactor cannot relocate saved views out from under an operator.
func TestView_LoadPathIsTheContractsPath(t *testing.T) {
	got := ViewsDir(filepath.Join("some", "vault"))
	want := filepath.Join("some", "vault", ".omnipus-vault", "views")
	if got != want {
		t.Fatalf("the ViewDef schema puts saved views at <vault>/.omnipus-vault/views/; ViewsDir gave %q, want %q", got, want)
	}
}

// TestView_NoViewsDirectoryIsNotAnError — the ordinary state of most vaults.
func TestView_NoViewsDirectoryIsNotAnError(t *testing.T) {
	set, report, err := LoadViews(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("a vault with no views directory must load cleanly, got: %v", err)
	}
	if !report.OK() {
		t.Fatalf("expected no rejections, got %v", report.Rejections)
	}
	if set.Len() != 0 {
		t.Fatalf("expected an empty view set, got %d views", set.Len())
	}
}

// TestView_MultiWordKeysSurviveTheDecode is the trap this loader exists to
// avoid, and it is the reason the YAML is routed through JSON.
//
// generated.ViewDef carries `json:` tags and NO `yaml:` tags. A plain
// yaml.Unmarshal into it lower-cases the Go field names, so `group_by`,
// `schema_version` and `filters[].include_absent` all land nowhere — and the
// view PARSES CLEANLY having silently lost half of itself, which is the exact
// class of quiet wrong answer this whole layer exists to remove.
//
// MUTATION: replace the JSON round trip in ParseView with a direct
// yaml.Unmarshal into generated.ViewDef and this test fails on GroupBy.
func TestView_MultiWordKeysSurviveTheDecode(t *testing.T) {
	root, schemas := viewFixtureSchemas(t, "")
	root = writeVaultView(t, root, "by-state.yaml", `
schema_version: 1
name: by-state
type: widget
label: Widgets by state
group_by: [state]
properties: [name, state, batch]
limit: 25
`)
	set, report, err := LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if !report.OK() {
		t.Fatalf("expected a clean load, got %v", report.Rejections)
	}
	v, ok := set.Get("by-state")
	if !ok {
		t.Fatalf("view 'by-state' did not load; names: %v", set.Names())
	}
	if v.Def.SchemaVersion != 1 {
		t.Errorf("schema_version was dropped by the decode: got %d, want 1", v.Def.SchemaVersion)
	}
	if v.Def.GroupBy == nil || len(*v.Def.GroupBy) != 1 || (*v.Def.GroupBy)[0] != "state" {
		t.Errorf("group_by was dropped by the decode: got %v, want [state]. "+
			"This is the multi-word-key trap: generated.ViewDef has json tags only, "+
			"so a plain yaml.Unmarshal loses every snake_case field in silence.", v.Def.GroupBy)
	}
	if v.Def.Properties == nil || len(*v.Def.Properties) != 3 {
		t.Errorf("properties was dropped: got %v", v.Def.Properties)
	}
	if v.Def.Limit == nil || *v.Def.Limit != 25 {
		t.Errorf("limit was dropped: got %v", v.Def.Limit)
	}
	if v.DisplayLabel() != "Widgets by state" {
		t.Errorf("label was dropped: DisplayLabel gave %q", v.DisplayLabel())
	}
	if v.SourcePath == "" || !strings.HasSuffix(v.SourcePath, "by-state.yaml") {
		t.Errorf("SourcePath must name the file the view came from, got %q", v.SourcePath)
	}
}

// TestView_UnknownKeyIsRefusedNotDropped — the contract says
// `additionalProperties: false`, and this is what makes that a rule rather
// than a comment.
//
// MUTATION: delete `dec.DisallowUnknownFields()` from ParseView and this test
// fails — the misspelled key is dropped in silence and the view loads with no
// grouping at all.
func TestView_UnknownKeyIsRefusedNotDropped(t *testing.T) {
	root, schemas := viewFixtureSchemas(t, "")
	root = writeVaultView(t, root, "typo.yaml", `
schema_version: 1
name: typo
type: widget
group-by: [state]
`)
	set, report, err := LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if set.Len() != 0 {
		t.Fatalf("a view with an unknown key must not load; it loaded as %v", set.Names())
	}
	if len(report.Rejections) != 1 {
		t.Fatalf("expected exactly one rejection, got %v", report.Rejections)
	}
	rej := report.Rejections[0]
	if rej.Code != RejectViewUnknownKey {
		t.Errorf("expected %s, got %s (%s)", RejectViewUnknownKey, rej.Code, rej.Reason)
	}
	if !strings.Contains(rej.Reason, "group-by") {
		t.Errorf("the rejection must name the offending key so the operator can find it; got %q", rej.Reason)
	}
	if strings.Contains(rej.Reason, "json") {
		t.Errorf("the operator is reading a YAML file; the message must not send them looking for JSON: %q", rej.Reason)
	}
}

// TestView_VersionIsMandatoryAndPinned covers both halves of ADR-068 D2's
// rule as it applies to views.
func TestView_VersionIsMandatoryAndPinned(t *testing.T) {
	cases := []struct {
		name string
		body string
		want ViewRejectionCode
	}{
		{
			name: "missing schema_version",
			body: "name: v\ntype: widget\n",
			want: RejectViewMissingVersion,
		},
		{
			// 2 is now READABLE (FR-018b), so the future version this case
			// pins moved up by one. The case itself must stay: a version this
			// release does not know is rejected, never read as the nearest
			// one it does.
			name: "a future schema_version",
			body: "schema_version: 3\nname: v\ntype: widget\n",
			want: RejectViewUnsupportedVersion,
		},
		{
			name: "version zero",
			body: "schema_version: 0\nname: v\ntype: widget\n",
			want: RejectViewUnsupportedVersion,
		},
		{
			// A v2 view may omit `type`; a v1 view may not, and this is the
			// pair that keeps the relaxation FROM leaking backwards.
			name: "no type on schema_version 1",
			body: "schema_version: 1\nname: v\n",
			want: RejectViewMissingType,
		},
		{
			name: "an empty type on schema_version 2 is a typo, not an untyped view",
			body: "schema_version: 2\nname: v\ntype: \"  \"\n",
			want: RejectViewMissingType,
		},
		{
			name: "an empty file",
			body: "",
			want: RejectViewMissingVersion,
		},
		{
			name: "no name",
			body: "schema_version: 1\ntype: widget\n",
			want: RejectViewMissingName,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, rej := ParseView("/vault/.omnipus-vault/views/v.yaml", []byte(tc.body))
			if rej == nil {
				t.Fatalf("expected a rejection, the view parsed")
			}
			if rej.Code != tc.want {
				t.Fatalf("expected %s, got %s (%s)", tc.want, rej.Code, rej.Reason)
			}
			if len(rej.Paths) != 1 || rej.Paths[0] == "" {
				t.Errorf("every rejection must name the file: %v", rej.Paths)
			}
		})
	}
}

// TestView_DuplicateNameRejectsBothAndNamesBothPaths — the same posture
// FR-003 takes for a duplicate record type, and for the same reason: there is
// no basis for preferring one, and picking the alphabetically-first would make
// which view runs depend on a filename.
func TestView_DuplicateNameRejectsBothAndNamesBothPaths(t *testing.T) {
	root, schemas := viewFixtureSchemas(t, "")
	body := "schema_version: 1\nname: shared\ntype: widget\n"
	root = writeVaultView(t, root, "a.yaml", body)
	root = writeVaultView(t, root, "b.yaml", body)

	set, report, err := LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if set.Len() != 0 {
		t.Fatalf("neither duplicate may load; got %v", set.Names())
	}
	if len(report.Rejections) != 1 {
		t.Fatalf("expected one grouped rejection, got %v", report.Rejections)
	}
	rej := report.Rejections[0]
	if rej.Code != RejectViewDuplicateName {
		t.Fatalf("expected %s, got %s", RejectViewDuplicateName, rej.Code)
	}
	if len(rej.Paths) != 2 {
		t.Fatalf("both paths must be named, got %v", rej.Paths)
	}
	for _, want := range []string{"a.yaml", "b.yaml"} {
		if !strings.Contains(rej.Reason, want) {
			t.Errorf("the rejection must name %s so the operator can find both files; got %q", want, rej.Reason)
		}
	}
}

// TestView_ValidationAgainstSchemas covers every place a name can go stale.
// Each case asserts the SHAPE and the remedy — that the valid alternatives are
// listed — never these particular fixture words (R-F).
func TestView_ValidationAgainstSchemas(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantCode   ViewRejectionCode
		mustNameds []string
	}{
		{
			name:       "a record type the vault no longer declares",
			body:       "schema_version: 1\nname: v\ntype: gadget\n",
			wantCode:   RejectViewUnknownType,
			mustNameds: []string{"gadget", "widget", "foundry"},
		},
		{
			name:       "a property in group_by",
			body:       "schema_version: 1\nname: v\ntype: widget\ngroup_by: [colour]\n",
			wantCode:   RejectViewUnknownProperty,
			mustNameds: []string{"colour", "group_by", "name", "state", "maker", "batch"},
		},
		{
			name:       "a property in sort",
			body:       "schema_version: 1\nname: v\ntype: widget\nsort:\n  - {property: colour, direction: asc}\n",
			wantCode:   RejectViewUnknownProperty,
			mustNameds: []string{"colour", "sort"},
		},
		{
			name:       "a property in the displayed set",
			body:       "schema_version: 1\nname: v\ntype: widget\nproperties: [name, colour]\n",
			wantCode:   RejectViewUnknownProperty,
			mustNameds: []string{"colour", "properties"},
		},
		{
			name:       "a property in an aggregate",
			body:       "schema_version: 1\nname: v\ntype: widget\naggregates:\n  - {op: sum, property: colour}\n",
			wantCode:   RejectViewUnknownProperty,
			mustNameds: []string{"colour", "aggregates"},
		},
		{
			name:       "a property in a filter",
			body:       "schema_version: 1\nname: v\ntype: widget\nfilters:\n  - {property: colour, op: eq}\n",
			wantCode:   RejectViewUnknownProperty,
			mustNameds: []string{"colour", "filter 1"},
		},
		{
			name: "an enum value outside the declared set",
			body: "schema_version: 1\nname: v\ntype: widget\nfilters:\n" +
				"  - {property: state, op: eq, values: [{type: enum, enum: pending}]}\n",
			wantCode:   RejectViewUnknownEnumValue,
			mustNameds: []string{"pending", "draft", "shipped", "withdrawn"},
		},
		{
			name: "a via hop that is not a relation",
			body: "schema_version: 1\nname: v\ntype: widget\nfilters:\n" +
				"  - {property: name, op: eq, via: [batch]}\n",
			wantCode:   RejectViewUnknownProperty,
			mustNameds: []string{"batch", "relation"},
		},
		{
			name: "a property checked against the RELATED type after a via hop (FR-009)",
			body: "schema_version: 1\nname: v\ntype: widget\nfilters:\n" +
				"  - {property: batch, op: eq, via: [maker]}\n",
			wantCode: RejectViewUnknownProperty,
			// `batch` is declared on widget and NOT on foundry. After the hop
			// the check must run against foundry, so this must be refused —
			// property names are scoped to their record type (FR-009).
			mustNameds: []string{"batch", "foundry", "region"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, schemas := viewFixtureSchemas(t, "")
			root = writeVaultView(t, root, "v.yaml", tc.body)
			set, report, err := LoadViews(root, schemas)
			if err != nil {
				t.Fatalf("LoadViews: %v", err)
			}
			if set.Len() != 0 {
				t.Fatalf("a view naming something the schema does not declare must not load; it loaded as %v", set.Names())
			}
			if len(report.Rejections) != 1 {
				t.Fatalf("expected one rejection, got %v", report.Rejections)
			}
			rej := report.Rejections[0]
			if rej.Code != tc.wantCode {
				t.Fatalf("expected %s, got %s (%s)", tc.wantCode, rej.Code, rej.Reason)
			}
			for _, want := range tc.mustNameds {
				if !strings.Contains(rej.Reason, want) {
					t.Errorf("FR-024's pattern requires the refusal to name the valid options; "+
						"%q is missing from %q", want, rej.Reason)
				}
			}
		})
	}
}

// TestView_ValidationIsSkippedWithoutSchemas — the nil schema set means
// "format only", and a caller that has the schemas and passes nil gets views
// that look fine and query nothing. The distinction is the point, so it is
// asserted rather than assumed.
func TestView_ValidationIsSkippedWithoutSchemas(t *testing.T) {
	root := writeVaultView(t, "", "v.yaml", "schema_version: 1\nname: v\ntype: gadget\ngroup_by: [colour]\n")
	set, report, err := LoadViews(root, nil)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if !report.OK() {
		t.Fatalf("with no schema set, only the format is checked; got %v", report.Rejections)
	}
	if set.Len() != 1 {
		t.Fatalf("expected the view to load, got %d", set.Len())
	}
}

// TestView_LookupIsExactNotFolded — two files side by side spelling a name two
// ways are two views, and folding here would silently make them one.
func TestView_LookupIsExactNotFolded(t *testing.T) {
	root := writeVaultView(t, "", "a.yaml", "schema_version: 1\nname: Open-Deals\ntype: widget\n")
	set, _, err := LoadViews(root, nil)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if _, ok := set.Get("Open-Deals"); !ok {
		t.Fatalf("exact lookup failed; names: %v", set.Names())
	}
	if _, ok := set.Get("open-deals"); ok {
		t.Fatalf("view lookup must be exact: a folded lookup makes two files on disk one view")
	}
}

// TestView_DisplayLabelFallsBackToName so no consumer invents its own
// fallback and no two consumers invent different ones.
func TestView_DisplayLabelFallsBackToName(t *testing.T) {
	v, rej := ParseView("/v.yaml", []byte("schema_version: 1\nname: bare\ntype: widget\n"))
	if rej != nil {
		t.Fatalf("unexpected rejection: %v", rej)
	}
	if got := v.DisplayLabel(); got != "bare" {
		t.Fatalf("an absent label must render the name, got %q", got)
	}
}

// TestView_NonViewFilesAreSkipped — the views directory may hold a README or a
// dotfile, and neither is a view.
func TestView_NonViewFilesAreSkipped(t *testing.T) {
	root := writeVaultView(t, "", "real.yaml", "schema_version: 1\nname: real\ntype: widget\n")
	dir := ViewsDir(root)
	for _, name := range []string{"README.md", ".hidden.yaml", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("not a view"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	set, report, err := LoadViews(root, nil)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if !report.OK() {
		t.Fatalf("non-view files must be skipped, not rejected; got %v", report.Rejections)
	}
	if set.Len() != 1 {
		t.Fatalf("expected exactly the one real view, got %v", set.Names())
	}
	if len(report.ScannedFiles) != 1 {
		t.Fatalf("only candidate view files should be scanned, got %v", report.ScannedFiles)
	}
}
