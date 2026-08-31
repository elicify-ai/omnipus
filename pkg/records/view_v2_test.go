// Omnipus — tests for ViewDef schema_version 2 loading (ADR-068 D24.1, spec
// FR-018b/FR-018c/FR-018d, FR-109, FR-146, FR-148).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// v2FixtureSchemas declares the record type the version-2 fixtures query. It
// is deliberately separate from viewFixtureSchemas: the v2 tests need a
// many-valued property and a date, and growing the shared fixture would have
// changed what the version-1 tests are testing.
func v2FixtureSchemas(t *testing.T) (string, *SchemaSet) {
	t.Helper()
	root := writeVaultSchema(t, "", "deal.yaml", `
schema_version: 1
type: deal
label: Deal
identity:
  prefix: DL
properties:
  name:   { type: text, required: true }
  stage:  { type: enum, values: [lead, open, won, lost] }
  amount: { type: integer }
  closed: { type: date }
  tags:   { type: text, many: true }
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

// loadOneView writes one view file and loads it, returning the view or the
// rejection — whichever the loader produced.
func loadOneView(t *testing.T, root string, schemas *SchemaSet, body string) (*SavedView, *ViewRejection) {
	t.Helper()
	root = writeVaultView(t, root, "v.yaml", body)
	views, report, err := LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if len(report.Rejections) > 1 {
		t.Fatalf("one file produced %d rejections: %v", len(report.Rejections), report.Rejections)
	}
	if len(report.Rejections) == 1 {
		return nil, &report.Rejections[0]
	}
	if views.Len() != 1 {
		t.Fatalf("expected exactly one loaded view, got %d", views.Len())
	}
	return views.Views()[0], nil
}

// ---------------------------------------------------------------------------
// The version partition
// ---------------------------------------------------------------------------

// TestView_EveryWireKeyIsVersionClassified is the guard that makes the
// version partition a partition instead of a list somebody maintains.
//
// A FLAT DENYLIST CANNOT DETECT ITS OWN DELETION. If `viewV2OnlyKeys` simply
// listed keys to refuse on v1, removing `layout` from it would break nothing
// visible: no v1 fixture sets `layout`, so no test would notice, and from
// then on a v1 file could carry a v2 key silently.
//
// A partition can, because it is checked against the SOURCE OF TRUTH —
// generated.ViewDef's own json tags, which come from contracts/openapi.yaml.
// Every tag must be classified exactly once, and every classified name must
// be a real tag. So:
//
//	deleting a key from any set        -> it is unclassified   -> FAIL
//	adding a key to two sets           -> classified twice     -> FAIL
//	adding a wire key to ViewDef.yaml  -> it is unclassified   -> FAIL
//	renaming a key in the contract     -> the old name is dead -> FAIL
//
// which is the whole point: the next person to extend the view format is told
// to decide which version owns their key, at build time, by name.
func TestView_EveryWireKeyIsVersionClassified(t *testing.T) {
	tags := viewDefJSONTags(t)
	if len(tags) < 15 {
		t.Fatalf("only %d json tags found on generated.ViewDef; the reflection walk is not reading the type it thinks it is", len(tags))
	}

	sets := map[string]map[string]struct{}{
		"viewV1OnlyKeys": viewV1OnlyKeys,
		"viewV2OnlyKeys": viewV2OnlyKeys,
		"viewSharedKeys": viewSharedKeys,
	}

	// (a) every wire key is classified exactly once.
	var unclassified, multiply []string
	for _, tag := range tags {
		in := []string{}
		for name, set := range sets {
			if _, ok := set[tag]; ok {
				in = append(in, name)
			}
		}
		switch len(in) {
		case 0:
			unclassified = append(unclassified, tag)
		case 1:
		default:
			sort.Strings(in)
			multiply = append(multiply, tag+" in "+strings.Join(in, "+"))
		}
	}
	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		t.Errorf("these ViewDef wire keys belong to no version set: %v.\n"+
			"Every key must be classified as version-1-only, version-2-only or shared — an unclassified key is a key both versions silently accept, which is how the two vocabularies start leaking into each other.",
			unclassified)
	}
	if len(multiply) > 0 {
		sort.Strings(multiply)
		t.Errorf("these ViewDef wire keys are classified more than once: %v", multiply)
	}

	// (b) every classified name is a real wire key. This half catches the
	// rename: move `group_by` to another spelling in the contract and the
	// stale entry here is reported rather than silently guarding nothing.
	real := map[string]struct{}{}
	for _, tag := range tags {
		real[tag] = struct{}{}
	}
	var phantom []string
	for setName, set := range sets {
		for k := range set {
			if _, ok := real[k]; !ok {
				phantom = append(phantom, k+" (in "+setName+")")
			}
		}
	}
	if len(phantom) > 0 {
		sort.Strings(phantom)
		t.Errorf("these names are classified but are not json keys on generated.ViewDef: %v.\n"+
			"A classified name that no longer exists guards nothing; either the contract renamed it or the set is stale.",
			phantom)
	}
}

// viewDefJSONTags reads the wire key of every field on generated.ViewDef.
func viewDefJSONTags(t *testing.T) []string {
	t.Helper()
	typ := reflect.TypeOf(generated.ViewDef{})
	out := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			t.Fatalf("generated.ViewDef field %s carries no json tag; the wire key cannot be derived", typ.Field(i).Name)
		}
		out = append(out, strings.Split(tag, ",")[0])
	}
	sort.Strings(out)
	return out
}

// TestView_VersionKeyMismatchIsRefusedBothWays proves a file speaks one
// version's vocabulary or the other, never a mixture — and that the refusal
// names the key and the version it belongs to.
//
// The v2-key-on-v1 direction is the one that matters most: without it a v1
// file could carry a `filter:` tree that decodes fine (both keys live on the
// one generated type) and is then never evaluated, because v1 evaluation
// reads `filters`. The view would load, look right, and filter by nothing.
func TestView_VersionKeyMismatchIsRefusedBothWays(t *testing.T) {
	root, schemas := v2FixtureSchemas(t)

	cases := []struct {
		name    string
		body    string
		wantKey string
	}{
		{
			name:    "a v2 tree on a version-1 file",
			wantKey: "filter",
			body: `
schema_version: 1
name: v
type: deal
filter:
  property: stage
  op: "="
  value: open
`,
		},
		{
			name:    "a v2 layout on a version-1 file",
			wantKey: "layout",
			body:    "schema_version: 1\nname: v\ntype: deal\nlayout: cards\n",
		},
		{
			name:    "a v1 filters list on a version-2 file",
			wantKey: "filters",
			body: `
schema_version: 2
name: v
type: deal
filters:
  - property: stage
    op: eq
    values: [{type: enum, enum: open}]
`,
		},
		{
			name:    "a v1 group_by on a version-2 file",
			wantKey: "group_by",
			body:    "schema_version: 2\nname: v\ntype: deal\ngroup_by: [stage]\n",
		},
		{
			// An EMPTY v1 list on a v2 file. This is the case a struct-side
			// check would miss — a nil *[]RecordFilter is indistinguishable
			// from a key that decoded to nothing — and it still says the
			// author believed they were writing v1.
			name:    "an empty v1 filters list on a version-2 file",
			wantKey: "filters",
			body:    "schema_version: 2\nname: v\ntype: deal\nfilters: []\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, rej := loadOneView(t, root, schemas, tc.body)
			if rej == nil {
				t.Fatalf("the file loaded; a view carrying the other version's keys must be refused")
			}
			if rej.Code != RejectViewVersionKeyMismatch {
				t.Fatalf("code = %s, want %s (%s)", rej.Code, RejectViewVersionKeyMismatch, rej.Reason)
			}
			if !strings.Contains(rej.Reason, tc.wantKey) {
				t.Errorf("the refusal does not name the offending key %q: %s", tc.wantKey, rej.Reason)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FR-018b — the find grammar, and what it buys
// ---------------------------------------------------------------------------

// TestView_V2TreeExpressesDisjunction is spec §7 test 90.
//
// The measured failure it closes: seven filter groups in the founder's vault
// use disjunction, and NOT ONE was expressible as a view, because v1's
// `filters` is a flat AND-only list. A v2 view must load an `any` tree,
// validate its leaves against the schema, and hand find the SAME tree — not a
// translation of it.
func TestView_V2TreeExpressesDisjunction(t *testing.T) {
	root, schemas := v2FixtureSchemas(t)
	v, rej := loadOneView(t, root, schemas, `
schema_version: 2
name: hot-or-large
type: deal
filter:
  any:
    - property: stage
      op: "="
      value: won
    - all:
        - property: amount
          op: ">="
          value: "10000"
        - property: stage
          op: "<>"
          value: lost
`)
	if rej != nil {
		t.Fatalf("a version-2 disjunctive view was rejected: %s", rej)
	}
	if v.Def.SchemaVersion != ViewVersion2 {
		t.Fatalf("schema_version = %d, want 2", v.Def.SchemaVersion)
	}
	if v.Def.Filter == nil || v.Def.Filter.Any == nil || len(*v.Def.Filter.Any) != 2 {
		t.Fatalf("the `any` tree did not survive the load: %+v", v.Def.Filter)
	}

	req, ok := NewViewFindLoader(newSet(v)).View("hot-or-large")
	if !ok {
		t.Fatal("a version-2 view whose every leaf is already find's own grammar must be servable")
	}
	// The tree is handed over UNCHANGED. Compared structurally rather than
	// field by field, because "unchanged" is the whole claim.
	if !sameFilterJSON(t, v.Def.Filter, req.Filter) {
		t.Fatalf("the served filter differs from the saved one.\nsaved: %s\nserved: %s",
			mustJSON(t, v.Def.Filter), mustJSON(t, req.Filter))
	}
	if req.Filter == v.Def.Filter {
		t.Fatal("the served request ALIASES the saved view's filter; a request the engine later normalises in place would rewrite the saved view")
	}
	if (*v.Def.Filter.Any)[0].Property == (*req.Filter.Any)[0].Property {
		t.Fatal("a child leaf's Property pointer is shared with the saved view — the deep copy is only one level deep")
	}
}

// TestView_V2GroupingCarriesDirection covers the second half of FR-018b's
// grammar change, and the failure it was written after: v1's bare `group_by`
// list has no direction field at all, so 24 real `groupBy` directions in the
// founder's vault were flattened to the default in silence.
func TestView_V2GroupingCarriesDirection(t *testing.T) {
	root, schemas := v2FixtureSchemas(t)
	v, rej := loadOneView(t, root, schemas, `
schema_version: 2
name: by-stage
type: deal
grouping:
  - property: stage
    direction: desc
  - property: closed
`)
	if rej != nil {
		t.Fatalf("rejected: %s", rej)
	}
	if v.Def.Grouping == nil || len(*v.Def.Grouping) != 2 {
		t.Fatalf("grouping did not survive: %+v", v.Def.Grouping)
	}
	g := *v.Def.Grouping
	if g[0].Direction == nil || *g[0].Direction != generated.ViewGroupByDirectionDesc {
		t.Fatalf("grouping[0].direction = %v, want desc — the direction is the whole reason this key exists", g[0].Direction)
	}
	// An OMITTED direction stays omitted on the wire. `asc` is the documented
	// default, and defaulting it HERE would make "the author asked for
	// ascending" and "the author said nothing" indistinguishable to every
	// later reader — including the importer's own loss report.
	if g[1].Direction != nil {
		t.Fatalf("grouping[1].direction = %v, want nil; the asc default belongs to the renderer, not the loader", *g[1].Direction)
	}
}

// TestView_V2DescendingGroupIsRefusedNotFlattened is the seam this loader
// refuses to paper over.
//
// VaultFindRequest.group_by is a bare []string with no direction. Serving a
// descending v2 grouping through it would reorder the groups ascending in
// SILENCE — the exact failure ViewGroupBy was added to end. So it is refused
// with the reason named, and an ASCENDING key (or one with no direction)
// still crosses, because for those nothing is lost.
func TestView_V2DescendingGroupIsRefusedNotFlattened(t *testing.T) {
	mk := func(dir *generated.ViewGroupByDirection) *SavedView {
		return &SavedView{Def: generated.ViewDef{
			SchemaVersion: ViewVersion2, Name: "g", Type: ptr("deal"),
			Grouping: ptr([]generated.ViewGroupBy{{Property: "stage", Direction: dir}}),
		}}
	}

	desc := NewViewFindLoader(newSet(mk(ptr(generated.ViewGroupByDirectionDesc))))
	if _, ok := desc.View("g"); ok {
		t.Fatal("a descending grouping was served through a request that cannot carry a direction; that flattens it silently")
	}
	refusal, has := desc.ServeRefusal("g")
	if !has || refusal.Code != ServeRefusalGroupDirection {
		t.Fatalf("ServeRefusal = %+v (%v), want %s", refusal, has, ServeRefusalGroupDirection)
	}
	if !strings.Contains(refusal.Reason, "stage") {
		t.Errorf("the reason does not name the grouping key: %s", refusal.Reason)
	}

	for _, tc := range []struct {
		name string
		dir  *generated.ViewGroupByDirection
	}{
		{"ascending", ptr(generated.ViewGroupByDirectionAsc)},
		{"unspecified", nil},
	} {
		t.Run(tc.name+" crosses losslessly", func(t *testing.T) {
			req, ok := NewViewFindLoader(newSet(mk(tc.dir))).View("g")
			if !ok {
				t.Fatal("ascending IS the documented default, so nothing is lost and the view must be servable")
			}
			if req.GroupBy == nil || len(*req.GroupBy) != 1 || (*req.GroupBy)[0] != "stage" {
				t.Fatalf("GroupBy = %v, want [stage]", req.GroupBy)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FR-018b — the optional type, and FR-018c's reserved namespaces
// ---------------------------------------------------------------------------

// TestView_V2TypeIsOptionalAndV1TypeIsNot is the relaxation, and the proof it
// did not leak backwards.
//
// Four of the founder's eighteen bases scope purely by folder and span record
// types, so an untyped view has to load. A version-1 file has no untyped
// semantics to fall back on — its filters are validated against one type's
// schema — so it stays rejected exactly as it always was.
func TestView_V2TypeIsOptionalAndV1TypeIsNot(t *testing.T) {
	root, schemas := v2FixtureSchemas(t)

	t.Run("version 2 without a type loads", func(t *testing.T) {
		v, rej := loadOneView(t, root, schemas, "schema_version: 2\nname: everything\nsort: [{property: name, direction: asc}]\n")
		if rej != nil {
			t.Fatalf("an untyped version-2 view was rejected: %s", rej)
		}
		if v.Def.Type != nil {
			t.Fatalf("Type = %q, want nil — an untyped view must not acquire one", *v.Def.Type)
		}
	})

	t.Run("version 1 without a type is still refused", func(t *testing.T) {
		_, rej := loadOneView(t, root, schemas, "schema_version: 1\nname: everything\n")
		if rej == nil {
			t.Fatal("a version-1 view with no type loaded; the relaxation is version-2's only")
		}
		if rej.Code != RejectViewMissingType {
			t.Fatalf("code = %s, want %s", rej.Code, RejectViewMissingType)
		}
	})

	t.Run("a blank type is a typo at either version", func(t *testing.T) {
		_, rej := loadOneView(t, root, schemas, "schema_version: 2\nname: everything\ntype: \"\"\n")
		if rej == nil || rej.Code != RejectViewMissingType {
			t.Fatalf("an empty `type` must be refused as a typo, not read as a deliberate absence; got %v", rej)
		}
	})
}

// TestView_UntypedViewDoesNotRefuseUnknownProperties is the honest boundary
// of the untyped case (FR-018b, as rewritten in Draft 11).
//
// An untyped view resolves a property BY NAME over the rows FR-021e keeps for
// every note, and a name no in-scope type declares resolves in the text
// domain. So there is no property name this LOADER can refuse without
// refusing a query the requirement says must work — and it must not invent
// one to look thorough.
func TestView_UntypedViewDoesNotRefuseUnknownProperties(t *testing.T) {
	root, schemas := v2FixtureSchemas(t)
	v, rej := loadOneView(t, root, schemas, `
schema_version: 2
name: folder-scoped
filter:
  property: nowhere_declared
  op: "="
  value: yes
properties: [nowhere_declared, also_undeclared]
`)
	if rej != nil {
		t.Fatalf("an untyped view naming an undeclared property was rejected: %s\n"+
			"FR-018b resolves such a name at query time in the text domain; refusing it here would refuse a query the requirement mandates.", rej)
	}
	if v.Def.Filter == nil || v.Def.Filter.Property == nil || *v.Def.Filter.Property != "nowhere_declared" {
		t.Fatalf("the filter did not survive: %+v", v.Def.Filter)
	}

	// The same name in a TYPED view is still refused, which is what shows the
	// leniency above is scoped to the untyped case rather than a hole.
	_, rej2 := loadOneView(t, root, schemas, `
schema_version: 2
name: folder-scoped
type: deal
filter:
  property: nowhere_declared
  op: "="
  value: yes
`)
	if rej2 == nil || rej2.Code != RejectViewUnknownProperty {
		t.Fatalf("a TYPED view naming an undeclared property must still be refused (FR-024); got %v", rej2)
	}
}

// TestView_V2ReservedNamespacesAreAcceptedAndV1IsUnchanged covers FR-018c in
// the positions a view uses, and the deliberate scoping of it.
//
// `file.*` and `formula.*` are valid in every property position OF A
// VERSION-2 VIEW. They are NOT newly accepted on a version-1 file: a v1 view
// naming `file.mtime` in its sort was refused as an undeclared property
// before this change, and accepting it now would quietly widen what a file
// already on disk is allowed to say — the same class of change as
// translating its operators.
func TestView_V2ReservedNamespacesAreAcceptedAndV1IsUnchanged(t *testing.T) {
	root, schemas := v2FixtureSchemas(t)

	t.Run("file.* in a version-2 sort and filter", func(t *testing.T) {
		_, rej := loadOneView(t, root, schemas, `
schema_version: 2
name: recent
type: deal
filter:
  property: file.mtime
  op: ">"
  value: "2026-01-01"
sort: [{property: file.mtime, direction: desc}]
`)
		if rej != nil {
			t.Fatalf("a version-2 view using the reserved file namespace was rejected: %s", rej)
		}
	})

	t.Run("a misspelled file property is refused with the real ones listed", func(t *testing.T) {
		_, rej := loadOneView(t, root, schemas, "schema_version: 2\nname: recent\ntype: deal\nsort: [{property: file.mtimes, direction: desc}]\n")
		if rej == nil || rej.Code != RejectViewUnknownProperty {
			t.Fatalf("`file.mtimes` must be refused as a misspelling, not accepted into the namespace; got %v", rej)
		}
		if !strings.Contains(rej.Reason, FileMtimeProp) {
			t.Errorf("the refusal does not list the real file properties: %s", rej.Reason)
		}
	})

	t.Run("file.file is not a comparison target", func(t *testing.T) {
		_, rej := loadOneView(t, root, schemas, "schema_version: 2\nname: recent\ntype: deal\nsort: [{property: file.file, direction: desc}]\n")
		if rej == nil || rej.Code != RejectViewUnknownProperty {
			t.Fatalf("`file.file` is the note itself and cannot be sorted on (FR-130); got %v", rej)
		}
		// …but it renders, so the DISPLAY position accepts it.
		if _, rej2 := loadOneView(t, root, schemas, "schema_version: 2\nname: recent\ntype: deal\nproperties: [file.file, name]\n"); rej2 != nil {
			t.Fatalf("`file.file` must remain renderable in `properties`: %s", rej2)
		}
	})

	t.Run("version 1 is not newly permissive", func(t *testing.T) {
		_, rej := loadOneView(t, root, schemas, "schema_version: 1\nname: recent\ntype: deal\nsort: [{property: file.mtime, direction: desc}]\n")
		if rej == nil {
			t.Fatal("a version-1 view naming file.mtime loaded; that is a widening of what an existing file may say, applied automatically — the same class of change FR-018b prohibits for operators")
		}
		if rej.Code != RejectViewUnknownProperty {
			t.Fatalf("code = %s, want %s", rej.Code, RejectViewUnknownProperty)
		}
	})
}

// ---------------------------------------------------------------------------
// FR-109 — layout
// ---------------------------------------------------------------------------

// TestView_LayoutIsCarriedAndPoliced covers the field and the measured
// failure behind it: an Obsidian CARDS view imported as a table, recorded no
// loss, and scored CLEAN.
//
// The enum matters because the JSON decoder does NOT police it — ViewDefLayout
// is a bare string type, so `layout: card` (singular) would load, mean nothing
// to the SPA, and render as the default table. That is the silent flattening
// again, arriving through a typo instead of an importer.
func TestView_LayoutIsCarriedAndPoliced(t *testing.T) {
	root, schemas := v2FixtureSchemas(t)

	for _, layout := range viewLayoutNames() {
		t.Run(layout+" is carried", func(t *testing.T) {
			v, rej := loadOneView(t, root, schemas, "schema_version: 2\nname: l\ntype: deal\nlayout: "+layout+"\n")
			if rej != nil {
				t.Fatalf("layout %q was rejected: %s", layout, rej)
			}
			if v.Def.Layout == nil || string(*v.Def.Layout) != layout {
				t.Fatalf("layout = %v, want %q — an unrenderable layout must still be RECORDED, so the loss is named rather than invisible", v.Def.Layout, layout)
			}
		})
	}

	t.Run("a layout outside the enum is refused", func(t *testing.T) {
		_, rej := loadOneView(t, root, schemas, "schema_version: 2\nname: l\ntype: deal\nlayout: card\n")
		if rej == nil {
			t.Fatal("`layout: card` loaded; it would render as the default table, which is the silently-flattened cards view FR-109 exists to prevent")
		}
		if rej.Code != RejectViewInvalidLayout {
			t.Fatalf("code = %s, want %s (%s)", rej.Code, RejectViewInvalidLayout, rej.Reason)
		}
		if !strings.Contains(rej.Reason, "cards") {
			t.Errorf("the refusal does not list the layouts that would have worked: %s", rej.Reason)
		}
	})
}

// ---------------------------------------------------------------------------
// FR-023c — the filter tree bound
// ---------------------------------------------------------------------------

// TestView_V2FilterTreeIsBounded proves FR-023c applies to a view's tree
// identically to a request's, and that the refusal names WHICH bound.
//
// Enforced at LOAD rather than left to the query path because a view is
// written once and evaluated forever: a tree that will be refused on every
// query should be refused when it is stored, while somebody is looking at it.
func TestView_V2FilterTreeIsBounded(t *testing.T) {
	root, schemas := v2FixtureSchemas(t)

	leaf := "    - {property: stage, op: \"=\", value: open}\n"

	t.Run("exactly at the leaf bound loads", func(t *testing.T) {
		body := "schema_version: 2\nname: wide\ntype: deal\nfilter:\n  any:\n" + strings.Repeat(leaf, maxViewFilterLeaves)
		if _, rej := loadOneView(t, root, schemas, body); rej != nil {
			t.Fatalf("%d leaves is the bound, not past it; rejected: %s", maxViewFilterLeaves, rej)
		}
	})

	t.Run("one leaf over is refused naming the bound", func(t *testing.T) {
		body := "schema_version: 2\nname: wide\ntype: deal\nfilter:\n  any:\n" + strings.Repeat(leaf, maxViewFilterLeaves+1)
		_, rej := loadOneView(t, root, schemas, body)
		if rej == nil {
			t.Fatalf("%d leaves loaded; FR-023c caps a filter at %d", maxViewFilterLeaves+1, maxViewFilterLeaves)
		}
		if rej.Code != RejectViewFilterTooLarge {
			t.Fatalf("code = %s, want %s (%s)", rej.Code, RejectViewFilterTooLarge, rej.Reason)
		}
		if !strings.Contains(rej.Reason, "leaves") {
			t.Errorf("the refusal does not say which bound was broken: %s", rej.Reason)
		}
	})

	t.Run("depth is bounded separately", func(t *testing.T) {
		// A chain of `not` nodes: one leaf, arbitrary depth. It is refused for
		// DEPTH, which is what shows the two bounds are measured separately
		// rather than one standing in for the other.
		body := "schema_version: 2\nname: deep\ntype: deal\nfilter:\n"
		indent := "  "
		for i := 0; i < maxViewFilterDepth; i++ { // root + 8 nots = depth 10 at the leaf
			body += indent + "not:\n"
			indent += "  "
		}
		body += indent + "{property: stage, op: \"=\", value: open}\n"
		_, rej := loadOneView(t, root, schemas, body)
		if rej == nil {
			t.Fatalf("a tree %d levels deep loaded with only one leaf; FR-023c caps depth at %d independently of the leaf count", maxViewFilterDepth+1, maxViewFilterDepth)
		}
		if rej.Code != RejectViewFilterTooLarge {
			t.Fatalf("code = %s, want %s (%s)", rej.Code, RejectViewFilterTooLarge, rej.Reason)
		}
		if !strings.Contains(rej.Reason, "deep") {
			t.Errorf("the refusal names the wrong bound: %s", rej.Reason)
		}
	})

	t.Run("a node that is neither leaf nor combinator is refused", func(t *testing.T) {
		_, rej := loadOneView(t, root, schemas, "schema_version: 2\nname: empty\ntype: deal\nfilter: {}\n")
		if rej == nil || rej.Code != RejectViewInvalidFilterNode {
			t.Fatalf("an empty filter node must be refused rather than treated as 'match everything'; got %v", rej)
		}
	})

	t.Run("a node that is BOTH leaf and combinator is refused", func(t *testing.T) {
		_, rej := loadOneView(t, root, schemas, `
schema_version: 2
name: both
type: deal
filter:
  property: stage
  op: "="
  value: open
  all:
    - {property: amount, op: ">", value: "1"}
`)
		if rej == nil || rej.Code != RejectViewInvalidFilterNode {
			t.Fatalf("a node that is both a leaf and a combinator must be refused, not resolved by precedence; got %v", rej)
		}
	})
}

// TestView_V2FilterTreeLeafPropertiesAreValidated proves the tree is WALKED,
// not merely counted — and that the refusal says where in the tree the fault
// is, because "unknown property" over a twelve-leaf disjunction sends the
// operator reading the whole file.
func TestView_V2FilterTreeLeafPropertiesAreValidated(t *testing.T) {
	root, schemas := v2FixtureSchemas(t)
	_, rej := loadOneView(t, root, schemas, `
schema_version: 2
name: nested
type: deal
filter:
  any:
    - property: stage
      op: "="
      value: open
    - all:
        - property: amount
          op: ">"
          value: "1"
        - not:
            property: nonesuch
            op: "="
            value: x
`)
	if rej == nil {
		t.Fatal("an undeclared property buried three levels down was accepted; the tree is not being walked")
	}
	if rej.Code != RejectViewUnknownProperty {
		t.Fatalf("code = %s, want %s (%s)", rej.Code, RejectViewUnknownProperty, rej.Reason)
	}
	if !strings.Contains(rej.Reason, "filter.any[1].all[1].not") {
		t.Errorf("the refusal does not locate the fault in the tree: %s", rej.Reason)
	}
	if !strings.Contains(rej.Reason, "stage") {
		t.Errorf("the refusal does not list the properties that would have worked: %s", rej.Reason)
	}
}

// ---------------------------------------------------------------------------
// FR-140/FR-146/FR-148 — formulas, re-validated on load
// ---------------------------------------------------------------------------

// TestView_V2FormulasAreRevalidatedOnLoad proves FR-140's second half: the
// parser lives in the write path, AND the loader re-checks, so a HAND-EDITED
// file is re-checked rather than trusted because it once passed a writer.
func TestView_V2FormulasAreRevalidatedOnLoad(t *testing.T) {
	root, schemas := v2FixtureSchemas(t)

	t.Run("a well-formed formula loads and is referenceable", func(t *testing.T) {
		v, rej := loadOneView(t, root, schemas, `
schema_version: 2
name: f
type: deal
formulas:
  doubled: "amount * 2"
properties: [name, formula.doubled]
`)
		if rej != nil {
			t.Fatalf("rejected: %s", rej)
		}
		if v.Def.Formulas == nil || (*v.Def.Formulas)["doubled"] != "amount * 2" {
			t.Fatalf("the formula SOURCE TEXT must survive verbatim (FR-141): %+v", v.Def.Formulas)
		}
	})

	t.Run("a formula that does not parse is refused", func(t *testing.T) {
		_, rej := loadOneView(t, root, schemas, "schema_version: 2\nname: f\ntype: deal\nformulas:\n  broken: \"amount * \"\n")
		if rej == nil || rej.Code != RejectViewInvalidFormula {
			t.Fatalf("a hand-edited unparseable formula must be refused on LOAD; got %v", rej)
		}
	})

	t.Run("a reference cycle is refused rather than recursed", func(t *testing.T) {
		_, rej := loadOneView(t, root, schemas, `
schema_version: 2
name: f
type: deal
formulas:
  a: "formula.b + 1"
  b: "formula.a + 1"
`)
		if rej == nil || rej.Code != RejectViewInvalidFormula {
			t.Fatalf("FR-148: a reference cycle parses clean and would recurse forever; it must be refused naming its path. Got %v", rej)
		}
	})

	t.Run("a formula reference with no declaration is refused", func(t *testing.T) {
		_, rej := loadOneView(t, root, schemas, "schema_version: 2\nname: f\ntype: deal\nproperties: [formula.nonesuch]\n")
		if rej == nil || rej.Code != RejectViewUnknownFormula {
			t.Fatalf("`formula.nonesuch` names nothing; it must be refused, not rendered as an empty column. Got %v", rej)
		}
	})
}

// TestViewFindLoader_V2FormulaViewIsRefusedWithAReason: VaultFindRequest has
// no formulas key, so every `formula.<name>` a served view named would
// resolve against nothing. That is a seam in find's REQUEST, not a defect in
// the view — so the view loads and is listed, and only the serving is
// refused, with the reason naming why.
func TestViewFindLoader_V2FormulaViewIsRefusedWithAReason(t *testing.T) {
	v := &SavedView{Def: generated.ViewDef{
		SchemaVersion: ViewVersion2, Name: "f", Type: ptr("deal"),
		Formulas: ptr(map[string]string{"doubled": "amount * 2"}),
	}}
	loader := NewViewFindLoader(newSet(v))
	if _, ok := loader.View("f"); ok {
		t.Fatal("a view declaring formulas was served through a request that carries none")
	}
	refusal, has := loader.ServeRefusal("f")
	if !has || refusal.Code != ServeRefusalFormula {
		t.Fatalf("ServeRefusal = %+v (%v), want %s", refusal, has, ServeRefusalFormula)
	}
}

// ---------------------------------------------------------------------------
// FR-105 — a disabled view is never applied
// ---------------------------------------------------------------------------

// TestViewFindLoader_DisabledViewIsNeverApplied is the broadening prohibition
// made structural. An imported view whose untranslatable expression sat in a
// row-set-affecting position is stored DISABLED precisely because applying it
// would return more rows than the original — the standing example being a
// base that filtered out two scratch folders.
//
// The view here translates PERFECTLY. That is the point: without the check it
// would be served, and it would silently include everything the dropped
// clauses excluded.
func TestViewFindLoader_DisabledViewIsNeverApplied(t *testing.T) {
	v := &SavedView{Def: generated.ViewDef{
		SchemaVersion: ViewVersion2, Name: "d", Type: ptr("deal"),
		Disabled:     ptr(true),
		Untranslated: ptr([]string{`!inFolder("99-Temp")`}),
		Filter: &generated.VaultFilterNode{
			Property: ptr("stage"), Op: ptr(generated.Equal), Value: ptr("open"),
		},
	}}
	loader := NewViewFindLoader(newSet(v))

	if _, ok := loader.View("d"); ok {
		t.Fatal("a disabled view was served; its filter translates cleanly, which is exactly why the `disabled` flag has to be checked rather than inferred")
	}
	refusal, has := loader.ServeRefusal("d")
	if !has || refusal.Code != ServeRefusalDisabled {
		t.Fatalf("ServeRefusal = %+v (%v), want %s", refusal, has, ServeRefusalDisabled)
	}
	if !strings.Contains(refusal.Reason, "99-Temp") {
		t.Errorf("FR-105 requires the refusal to name the expression that disabled the view: %s", refusal.Reason)
	}

	// A disabled VERSION-1 view is refused too — the flag is not version-scoped.
	v1 := newTestView("d1", nil)
	v1.Def.Disabled = ptr(true)
	if _, ok := NewViewFindLoader(newSet(v1)).View("d1"); ok {
		t.Fatal("a disabled version-1 view was served")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func sameFilterJSON(t *testing.T, a, b *generated.VaultFilterNode) bool {
	t.Helper()
	return mustJSON(t, a) == mustJSON(t, b)
}
