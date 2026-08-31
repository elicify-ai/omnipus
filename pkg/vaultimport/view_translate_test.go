// Omnipus — one whole `.base` file translated, view by view: the four
// outcomes FR-105/FR-106/FR-109 distinguish, each with its losses named.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// decisionSchema is the inferred schema the fixture base is translated
// against. It is written here rather than inferred from notes so this test
// measures the TRANSLATOR and nothing else — a change to type inference (a
// peer's half of this package) cannot turn these assertions green or red.
func decisionSchema() *SchemaIndex {
	return NewSchemaIndex(map[string][]InferredProperty{
		"decision": {
			{Name: "status", Type: records.TypeEnum, EnumValues: []string{"accepted", "proposed", "rejected"}},
			{Name: "priority", Type: records.TypeInteger},
			{Name: "decided", Type: records.TypeDate},
			{Name: "owner", Type: records.TypePerson, To: "person"},
			{Name: "labels", Type: records.TypeEnum, Many: true, EnumValues: []string{"legal", "urgent"}},
			{Name: "rationale", Type: records.TypeText},
			{Name: "archived", Type: records.TypeCheckbox},
		},
	})
}

// fixtureBase is one `.base` file written the way Obsidian writes one. Each
// view is here to produce exactly one of the outcomes the requirements
// distinguish; the comment on each says which and why.
const fixtureBase = `
filters:
  and:
    - type == "decision"
views:
  # (1) FULLY TRANSLATABLE. Every clause, column, sort key and summary has a
  # representation. Expected: CONVERTED, no losses, enabled.
  - type: table
    name: Accepted
    filters:
      and:
        - status == "accepted"
        - priority >= 3
        - labels.contains("urgent")
    order:
      - file.name
      - status
      - priority
    sort:
      - property: priority
        direction: desc
    summaries:
      priority: sum

  # (2) DISPLAY-ONLY LOSSES. Nothing here can change which rows come back:
  # a formula column, an undeclared column, a formula sort key, a groupBy
  # direction the wire type has no field for, and an aggregate that does not
  # exist. Expected: CONVERTED WITH NAMED LOSSES, ENABLED.
  - type: table
    name: Board
    filters:
      and:
        - status == "accepted"
    order:
      - file.name
      - formula.age_in_days
      - nowhere_property
    sort:
      - property: formula.age_in_days
        direction: asc
    groupBy:
      property: status
      direction: desc
    summaries:
      priority: avg

  # (3) UNSUPPORTED OPERATOR IN A FILTER. The standing FR-105 example: drop
  # the folder exclusion and the view quietly includes every scratch note.
  # Expected: DISABLED.
  - type: table
    name: Live only
    filters:
      and:
        - status == "accepted"
        - not:
            - file.inFolder("99-Temp")

  # (4) A CARDS VIEW. The rendering is real and this release's view file
  # format cannot carry it, so it is a NAMED loss — never a clean import.
  # Expected: CONVERTED WITH NAMED LOSSES, ENABLED (layout is an annotation).
  - type: cards
    name: Gallery

  # (5) A BARE TRUTHY TEST ON A CHECKBOX. "archived" in Obsidian means
  # truthy; our nearest operator means "has a value", which also matches
  # archived: false. MORE rows. Expected: DISABLED.
  - type: table
    name: Archived
    filters:
      and:
        - archived

  # (6) A LAYOUT THE VIEW FORMAT HAS NO VALUE FOR AT ALL. Still an
  # annotation loss, still enabled.
  - type: list
    name: Listing
`

func translateFixture(t *testing.T) map[string]ViewOutcome {
	t.Helper()
	pb, err := ParseBaseFile([]byte(fixtureBase))
	if err != nil {
		t.Fatalf("the fixture base does not parse: %v", err)
	}
	outcome, produced := TranslateBase(pb, "Decisions.base", decisionSchema(), NewSlugRegistry())

	byName := map[string]ViewOutcome{}
	for _, v := range outcome.Views {
		byName[v.DisplayName] = v
	}

	t.Logf("Decisions.base → %s (%d views, %d files produced)", outcome.Status, len(outcome.Views), len(produced))
	for _, v := range outcome.Views {
		flag := "enabled"
		if v.Disabled {
			flag = "DISABLED"
		}
		t.Logf("  %-10q %-28s layout=%-6q %s", v.DisplayName, v.Status, v.Layout, flag)
		for _, l := range v.Losses {
			marker := "   annotation"
			if lossPositionAffectsRowSet(l) {
				marker = "   ROW SET   "
			}
			t.Logf("  %s %s", marker, l)
		}
	}
	return byName
}

// TestTranslateBase_TheFourOutcomes is the exit proof: one base file, six
// views, each landing on the outcome its construction demands.
func TestTranslateBase_TheFourOutcomes(t *testing.T) {
	views := translateFixture(t)

	want := []struct {
		view          string
		status        Outcome
		disabled      bool
		layout        string
		lossPositions []LossPosition
	}{
		{"Accepted", OutcomeConverted, false, "table", nil},
		{"Board", OutcomeConvertedWithLosses, false, "table", []LossPosition{
			LossProperties, LossProperties, LossSort, LossGroupBy, LossAggregates,
		}},
		{"Live only", OutcomeConvertedWithLosses, true, "table", []LossPosition{LossViewFilter}},
		{"Gallery", OutcomeConvertedWithLosses, false, "cards", []LossPosition{LossLayout}},
		{"Archived", OutcomeConvertedWithLosses, true, "table", []LossPosition{LossFilterLeaf}},
		{"Listing", OutcomeConvertedWithLosses, false, "list", []LossPosition{LossLayout}},
	}

	for _, w := range want {
		t.Run(w.view, func(t *testing.T) {
			got, ok := views[w.view]
			if !ok {
				t.Fatalf("the fixture's %q view is missing from the outcome entirely", w.view)
			}
			if got.Status != w.status {
				t.Errorf("status = %s, want %s (losses: %v, refused because: %s)", got.Status, w.status, got.Losses, got.RefusedReason)
			}
			if got.Disabled != w.disabled {
				t.Errorf("Disabled = %v, want %v (disabling losses: %v)", got.Disabled, w.disabled, got.DisablingLosses)
			}
			if got.Layout != w.layout {
				t.Errorf("Layout = %q, want %q — FR-109: a view's rendering is carried or it is a named loss, never dropped", got.Layout, w.layout)
			}
			if got.ResolvedType != "decision" {
				t.Errorf("ResolvedType = %q, want \"decision\"", got.ResolvedType)
			}
			var gotPositions []LossPosition
			for _, l := range got.Losses {
				pos, ok := parseLossPosition(l)
				if !ok {
					t.Errorf("loss line has no recognised position prefix: %q", l)
					continue
				}
				gotPositions = append(gotPositions, pos)
			}
			if !samePositionMultiset(gotPositions, w.lossPositions) {
				t.Errorf("loss positions = %v, want %v\n  losses:\n    %s", gotPositions, w.lossPositions, strings.Join(got.Losses, "\n    "))
			}
		})
	}
}

// TestTranslateBase_DisabledExactlyWhenARowSetLossExists is the two-way rule
// the acceptance harness asserts, on a fixture that runs on every machine.
// Both directions are checked, so it cannot be satisfied by disabling
// everything OR by disabling nothing.
func TestTranslateBase_DisabledExactlyWhenARowSetLossExists(t *testing.T) {
	views := translateFixture(t)

	var enabled, disabled int
	for name, v := range views {
		rowSetLoss := false
		for _, l := range v.Losses {
			if lossPositionAffectsRowSet(l) {
				rowSetLoss = true
			}
		}
		switch {
		case rowSetLoss && !v.Disabled:
			t.Errorf("FR-105 VIOLATED: %q carries a row-set-affecting loss and is ENABLED — it would return MORE rows than the Obsidian original.\n  losses: %v", name, v.Losses)
		case !rowSetLoss && v.Disabled:
			t.Errorf("%q is disabled with no row-set-affecting loss — a view disabled for a lost colour or column order is a false negative.\n  losses: %v", name, v.Losses)
		}
		if v.Disabled {
			disabled++
		} else {
			enabled++
		}
	}
	if disabled == 0 {
		t.Error("this fixture disabled nothing — the assertion above is vacuous in the direction that matters")
	}
	if enabled == 0 {
		t.Error("this fixture disabled everything — the assertion above is vacuous in the other direction")
	}
	t.Logf("six views: %d enabled, %d disabled", enabled, disabled)
}

// TestTranslateBase_DisablingLossesAreASubsetOfLosses keeps the report
// honest: the "why" list must be drawn from the losses a reader can see, and
// every entry in it must actually be row-set-affecting.
func TestTranslateBase_DisablingLossesAreASubsetOfLosses(t *testing.T) {
	for name, v := range translateFixture(t) {
		all := map[string]bool{}
		for _, l := range v.Losses {
			all[l] = true
		}
		for _, d := range v.DisablingLosses {
			if !all[d] {
				t.Errorf("%q: DisablingLosses names %q, which is not in Losses — a reader would be diffing two lists to find out why", name, d)
			}
			if !lossPositionAffectsRowSet(d) {
				t.Errorf("%q: DisablingLosses names %q, which is an ANNOTATION loss", name, d)
			}
		}
		if v.Disabled != (len(v.DisablingLosses) > 0) {
			t.Errorf("%q: Disabled=%v but %d disabling losses — the flag and its reason disagree", name, v.Disabled, len(v.DisablingLosses))
		}
	}
}

// TestTranslateBase_WrittenFileRecordsTheRefusal checks what lands ON DISK,
// not only what the report says. A disabled view whose file does not say so
// is a view that will be applied by anything that reads the file.
func TestTranslateBase_WrittenFileRecordsTheRefusal(t *testing.T) {
	pb, err := ParseBaseFile([]byte(fixtureBase))
	if err != nil {
		t.Fatalf("ParseBaseFile: %v", err)
	}
	_, produced := TranslateBase(pb, "Decisions.base", decisionSchema(), NewSlugRegistry())

	files := map[string]map[string]any{}
	for _, pv := range produced {
		var top map[string]any
		if err := yaml.Unmarshal(pv.Bytes, &top); err != nil {
			t.Fatalf("the importer wrote a view file that is not valid YAML (%s): %v", pv.RelPath, err)
		}
		files[pv.RelPath] = top
	}

	live := files["views/decisions--live-only.yaml"]
	if live == nil {
		t.Fatalf("the disabled view was not written at all; produced: %v", keysOf(files))
	}
	if live["disabled"] != true {
		t.Errorf("the disabled view's file does not carry `disabled: true` — anything reading the file would apply it: %v", live)
	}
	untranslated, _ := live["untranslated"].([]any)
	if len(untranslated) == 0 {
		t.Error("the disabled view's file records no `untranslated` entries — FR-101 requires the refused expression verbatim")
	}
	if !strings.Contains(renderVerbatim(untranslated), "99-Temp") {
		t.Errorf("the refused folder exclusion is not preserved verbatim: %v", untranslated)
	}

	clean := files["views/decisions--accepted.yaml"]
	if clean == nil {
		t.Fatal("the fully translatable view was not written")
	}
	if _, present := clean["disabled"]; present {
		t.Errorf("a view with no losses carries a `disabled` key: %v", clean)
	}
	if _, present := clean["untranslated"]; present {
		t.Errorf("a view with no losses carries an `untranslated` key: %v", clean)
	}
	if clean["schema_version"] != records.SupportedViewVersion {
		t.Errorf("schema_version = %v, want %d", clean["schema_version"], records.SupportedViewVersion)
	}
	if clean["source"] != "Decisions.base" {
		t.Errorf("source = %v, want the base file it came from", clean["source"])
	}

	// FR-109 read from the other side: `layout` is a VERSION-2 key and this
	// writer emits version 1, so it must NOT appear in the file — the
	// rendering survives as the named loss instead. A v1 file carrying a v2
	// key is refused by records.LoadViews on the very next load.
	for path, top := range files {
		if _, present := top["layout"]; present && records.SupportedViewVersion < records.ViewVersion2 {
			t.Errorf("%s carries a `layout` key on a schema_version-%d file; records.LoadViews refuses a v1 file with a v2 key, so every imported view would fail to load", path, records.SupportedViewVersion)
		}
	}
}

// TestTranslateBase_RefusesAViewItCannotTypeAtAll covers the third arm of
// the three-way contract: not every view becomes a file.
func TestTranslateBase_RefusesAViewItCannotTypeAtAll(t *testing.T) {
	pb, err := ParseBaseFile([]byte(`
views:
  - type: table
    name: Untyped
    filters:
      and:
        - status == "accepted"
`))
	if err != nil {
		t.Fatalf("ParseBaseFile: %v", err)
	}
	outcome, produced := TranslateBase(pb, "Untyped.base", decisionSchema(), NewSlugRegistry())
	if outcome.Status != OutcomeRefused {
		t.Errorf("base status = %s, want REFUSED", outcome.Status)
	}
	if len(produced) != 0 {
		t.Errorf("a refused view produced %d files", len(produced))
	}
	if len(outcome.Views) != 1 || outcome.Views[0].Status != OutcomeRefused {
		t.Fatalf("view outcomes = %+v", outcome.Views)
	}
	if !strings.Contains(outcome.Views[0].RefusedReason, "type") {
		t.Errorf("the refusal does not say what was missing: %q", outcome.Views[0].RefusedReason)
	}
	if outcome.Views[0].Disabled {
		t.Error("a REFUSED view is not a DISABLED view — nothing was written for it to disable")
	}
}

// TestTranslateBase_RefusesAnUnknownRecordType — a view whose type resolves
// but has no inferred schema cannot be validated, so it is refused rather
// than written against a schema that does not exist.
func TestTranslateBase_RefusesAnUnknownRecordType(t *testing.T) {
	pb, err := ParseBaseFile([]byte(`
filters:
  and:
    - type == "sprocket"
views:
  - type: table
    name: Sprockets
`))
	if err != nil {
		t.Fatalf("ParseBaseFile: %v", err)
	}
	outcome, produced := TranslateBase(pb, "Sprockets.base", decisionSchema(), NewSlugRegistry())
	if len(produced) != 0 || outcome.Status != OutcomeRefused {
		t.Errorf("status = %s with %d files, want REFUSED and none", outcome.Status, len(produced))
	}
	if !strings.Contains(outcome.Views[0].RefusedReason, "sprocket") {
		t.Errorf("the refusal does not name the type it could not find: %q", outcome.Views[0].RefusedReason)
	}
}

// TestBuildFilterLeaf_RefusesWhatWouldBroadenOrMisfire pins the per-clause
// refusals that need the schema to make. Each `want` is a substring of the
// reason a human reads in the report.
func TestBuildFilterLeaf_RefusesWhatWouldBroadenOrMisfire(t *testing.T) {
	schemas := decisionSchema()
	cases := []struct {
		name string
		leaf RawLeaf
		want string
	}{
		{
			name: "a truthy test on a checkbox would return more rows",
			leaf: RawLeaf{Property: "archived", Op: "is_absent", Negate: true, Truthy: true},
			want: "MORE rows",
		},
		{
			name: "a truthy test on an integer would return more rows",
			leaf: RawLeaf{Property: "priority", Op: "is_absent", Negate: true, Truthy: true},
			want: "MORE rows",
		},
		{
			name: "an ordered comparison on a list is undefined",
			leaf: RawLeaf{Property: "labels", Op: "gte", Values: []string{"urgent"}},
			want: "many-valued",
		},
		{
			name: "equality on text is undefined",
			leaf: RawLeaf{Property: "rationale", Op: "eq", Values: []string{"because"}},
			want: "text property",
		},
		{
			name: "contains on a scalar non-text property is undefined",
			leaf: RawLeaf{Property: "priority", Op: "contains", Values: []string{"3"}},
			want: "`contains` is not defined",
		},
		{
			name: "an enum literal the schema does not declare",
			leaf: RawLeaf{Property: "status", Op: "eq", Values: []string{"superseded"}},
			want: "declared enum values",
		},
		{
			name: "an undeclared property",
			leaf: RawLeaf{Property: "nowhere", Op: "eq", Values: []string{"x"}},
			want: "not declared",
		},
		{
			name: "a checkbox literal has no version-1 wire representation",
			leaf: RawLeaf{Property: "archived", Op: "eq", Values: []string{"true"}},
			want: "checkbox",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node, reason, ok := buildFilterLeafNode("decision", tc.leaf, schemas)
			if ok {
				t.Fatalf("the clause was ACCEPTED and emitted %v — it must be refused as a named loss so its view disables", node)
			}
			if !strings.Contains(reason, tc.want) {
				t.Errorf("reason = %q, want it to contain %q", reason, tc.want)
			}
		})
	}
}

// TestBuildFilterLeaf_AcceptsTheTruthyTestWhereItIsFaithful is the other
// half: for a type whose every present value is truthy, "has a value" IS the
// truthy test, and refusing it would disable views for no reason.
func TestBuildFilterLeaf_AcceptsTheTruthyTestWhereItIsFaithful(t *testing.T) {
	schemas := decisionSchema()
	for _, prop := range []string{"status", "decided", "owner", "rationale"} {
		leaf := RawLeaf{Property: prop, Op: "is_absent", Negate: true, Truthy: true}
		if _, reason, ok := buildFilterLeafNode("decision", leaf, schemas); !ok {
			t.Errorf("the truthy test on %q was refused (%s) — an empty string is already absent (FR-007a), so \"has a value\" and \"is truthy\" agree on this type", prop, reason)
		}
	}
}

// TestTruthyPartition_CoversEveryType is view_write.go's own guard: every
// declared property type must have a classification, or a ninth type added
// tomorrow silently defaults to "safe" and the truthy test broadens again.
func TestTruthyPartition_CoversEveryType(t *testing.T) {
	for _, pt := range records.PropertyTypes {
		if _, ok := truthyFalsyLiterals[pt]; !ok {
			t.Errorf("property type %q is not classified in truthyFalsyLiterals — buildFilterLeafNode cannot tell whether \"has a value\" is broader than \"is truthy\" for it", pt)
		}
	}
	declared := map[records.PropertyType]bool{}
	for _, pt := range records.PropertyTypes {
		declared[pt] = true
	}
	for pt := range truthyFalsyLiterals {
		if !declared[pt] {
			t.Errorf("truthyFalsyLiterals classifies %q, which records.PropertyTypes does not declare", pt)
		}
	}
	if truthyAdmitsAFalsyValue(records.PropertyType("a type that does not exist")) != true {
		t.Error("an unknown property type must be treated as admitting a falsy value — the fail-safe direction is a disabled view, never a broadened one")
	}
}

// TestSlugRegistry_NeverOverwritesAView — two views with the same name in two
// bases would otherwise write the same file, and the second would silently
// replace the first.
func TestSlugRegistry_NeverOverwritesAView(t *testing.T) {
	r := NewSlugRegistry()
	a := r.Slug("Decisions.base", "All")
	b := r.Slug("Decisions.base", "All")
	c := r.Slug("Other.base", "All")
	if a == b {
		t.Errorf("two views named %q in the same base got the same slug %q — one file would overwrite the other", "All", a)
	}
	if c == a || c == b {
		t.Errorf("a view in a different base collided: %q vs %q/%q", c, a, b)
	}
}

func samePositionMultiset(got, want []LossPosition) bool {
	if len(got) != len(want) {
		return false
	}
	counts := map[LossPosition]int{}
	for _, p := range got {
		counts[p]++
	}
	for _, p := range want {
		counts[p]--
	}
	for _, n := range counts {
		if n != 0 {
			return false
		}
	}
	return true
}

func keysOf(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
