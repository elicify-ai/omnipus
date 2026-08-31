// Omnipus — tests for the ViewDef version-2 loader (ADR-068 D24.1,
// spec FR-018b..FR-018d, FR-109).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// WHAT THESE TESTS ARE ORACLED AGAINST
//
// Every expected value below is read off spec FR-018b..FR-018d and FR-109, not
// off view.go. Where a number appears (64 leaves, depth 8) it is FR-023c's,
// quoted at the assertion.
//
// The single most important case in this file is
// TestView_V1ContainsNeverBecomesLikeOnLoad. Spec Draft 10 said a version-1
// view "loads translated to v2 semantics", with `contains` becoming
// `LIKE '%…%'`; Draft 11 withdrew that as review finding F5 because it
// BROADENS — `labels contains "in"` would newly match `indoor` and `printing`
// — automatically, on files already on disk. That test does not merely assert
// the translation is absent; it MEASURES what the translation would have
// returned, so it cannot pass on a build where the two operators had quietly
// become synonyms.
//
// Every view here is written to a real vault directory and read back through
// LoadViews, never hand-built as a struct. The requirement is about files on
// disk, and a struct literal skips the decode that FR-018b's version partition
// lives in.
// ---------------------------------------------------------------------------

// v2FixtureSchema declares the record type the version-2 fixtures query. It
// adds the many-valued `labels` property the broadening measurement needs;
// `state`, `maker` and `batch` mirror viewFixtureSchemas so a v1 and a v2 view
// over the same type can be compared side by side.
const v2FixtureSchema = `
schema_version: 1
type: widget
label: Widget
identity:
  prefix: WI
properties:
  name:   { type: text, required: true }
  state:  { type: enum, values: [draft, shipped, withdrawn] }
  maker:  { type: relation, to: foundry }
  batch:  { type: integer }
  labels: { type: text, many: true }
`

const v2FixtureRelatedSchema = `
schema_version: 1
type: foundry
properties:
  name:   { type: text }
  region: { type: enum, values: [north, south] }
`

// v2Vault writes the two fixture schemas plus the named view files into one
// vault and loads both, returning the view set and the load report.
//
// It returns the REPORT rather than failing on a rejection, because half the
// cases below are about the rejection.
func v2Vault(t *testing.T, views map[string]string) (*ViewSet, *ViewLoadReport, *SchemaSet) {
	t.Helper()
	root := writeVaultSchema(t, "", "widget.yaml", v2FixtureSchema)
	root = writeVaultSchema(t, root, "foundry.yaml", v2FixtureRelatedSchema)
	schemas, sreport, err := LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !sreport.OK() {
		t.Fatalf("fixture schemas did not load: %v", sreport.Rejections)
	}
	for filename, body := range views {
		root = writeVaultView(t, root, filename, body)
	}
	set, report, err := LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	return set, report, schemas
}

// mustLoadView loads one view file and fails with the rejection if it did not
// load — the common case, spelled once.
func mustLoadView(t *testing.T, filename, body string) *SavedView {
	t.Helper()
	set, report, _ := v2Vault(t, map[string]string{filename: body})
	if !report.OK() {
		t.Fatalf("the view was rejected: %v", report.Rejections)
	}
	if set.Len() != 1 {
		t.Fatalf("expected exactly one loaded view, got %d (%v)", set.Len(), set.Names())
	}
	return set.Views()[0]
}

// ---------------------------------------------------------------------------
// FR-018b — the version-2 grammar, whole
// ---------------------------------------------------------------------------

// TestView_V2TreeExpressesDisjunction is spec test 90.
//
// FR-018b's opening sentence is the requirement: "Seven filter groups in the
// founder's vault use disjunction; none was expressible as a view." So the
// case is a view whose filter is `{any: [...]}` with a nested `{all: [...]}`,
// carrying the rest of the v2 grammar alongside it — a directional `grouping`,
// a `layout`, a `property_config` — read off disk and then handed to the
// knowledge_find bridge.
//
// THE TREE MUST ARRIVE AT FIND UNCHANGED. A v2 `filter` already IS find's
// VaultFilterNode, so anything other than an identical tree on the other side
// is a translation, and FR-018b's whole point is that version 2 needs none.
func TestView_V2TreeExpressesDisjunction(t *testing.T) {
	v := mustLoadView(t, "active.yaml", `
schema_version: 2
name: active-widgets
type: widget
label: Active widgets
layout: cards
filter:
  any:
    - property: state
      op: "="
      value: shipped
    - all:
        - property: state
          op: "="
          value: draft
        - property: batch
          op: ">="
          value: "7"
    - not:
        property: batch
        op: "IS NULL"
grouping:
  - property: state
  - property: maker
    direction: asc
sort:
  - property: name
    direction: asc
properties: [name, state]
limit: 25
property_config:
  state:
    display_name: Stage
`)

	if v.Def.SchemaVersion != ViewVersion2 {
		t.Fatalf("schema_version = %d, want %d", v.Def.SchemaVersion, ViewVersion2)
	}
	// The v1 keys must be absent, not back-filled. A loader that populated
	// `filters` from `filter` would make the two versions indistinguishable
	// downstream, which is the drift the partition exists to stop.
	if v.Def.Filters != nil {
		t.Errorf("a version-2 view carries no v1 `filters` list; got %+v", v.Def.Filters)
	}
	if v.Def.GroupBy != nil {
		t.Errorf("a version-2 view carries no v1 `group_by` list; got %+v", v.Def.GroupBy)
	}

	// ── the tree ───────────────────────────────────────────────────────────
	if v.Def.Filter == nil {
		t.Fatal("the `filter` tree did not survive the load")
	}
	root := *v.Def.Filter
	if root.Any == nil {
		t.Fatalf("the root node is not a disjunction: %+v", root)
	}
	if len(*root.Any) != 3 {
		t.Fatalf("the disjunction has %d branches, want 3", len(*root.Any))
	}
	branches := *root.Any

	// branch 0 — a plain leaf.
	assertLeaf(t, branches[0], "state", generated.Equal, "shipped")

	// branch 1 — a nested conjunction, which is the shape v1 could not nest.
	if branches[1].All == nil || len(*branches[1].All) != 2 {
		t.Fatalf("branch 1 is not a two-child `all`: %+v", branches[1])
	}
	assertLeaf(t, (*branches[1].All)[0], "state", generated.Equal, "draft")
	assertLeaf(t, (*branches[1].All)[1], "batch", generated.GreaterThanEqual, "7")

	// branch 2 — tree negation, which is NOT `<>` (spec §8 R-2).
	if branches[2].Not == nil {
		t.Fatalf("branch 2 is not a `not`: %+v", branches[2])
	}
	if branches[2].Not.Op == nil || *branches[2].Not.Op != generated.ISNULL {
		t.Fatalf("the negated leaf's operator is %v, want `IS NULL`", branches[2].Not.Op)
	}

	// ── grouping carries a direction ───────────────────────────────────────
	// FR-018b: "`group_by` entries are `{property, direction: asc|desc}` — the
	// bare name list dropped 24 real direction declarations silently."
	if v.Def.Grouping == nil || len(*v.Def.Grouping) != 2 {
		t.Fatalf("grouping = %+v, want two keys", v.Def.Grouping)
	}
	g := *v.Def.Grouping
	if g[0].Property != "state" || g[0].Direction != nil {
		t.Errorf("grouping[0] = %+v; an omitted direction must stay ABSENT — the contract states asc is the default rather than declaring one, so a loader that filled it in would be inventing a declaration the file never made", g[0])
	}
	if g[1].Property != "maker" || g[1].Direction == nil || *g[1].Direction != generated.ViewGroupByDirectionAsc {
		t.Errorf("grouping[1] = %+v, want maker/asc", g[1])
	}

	// ── layout and display config ──────────────────────────────────────────
	if v.Def.Layout == nil || *v.Def.Layout != generated.ViewDefLayoutCards {
		t.Errorf("layout = %v, want cards (FR-109)", v.Def.Layout)
	}
	if v.Def.PropertyConfig == nil {
		t.Fatal("property_config did not survive the load")
	}
	cfg, ok := (*v.Def.PropertyConfig)["state"]
	if !ok || cfg.DisplayName == nil || *cfg.DisplayName != "Stage" {
		t.Errorf("property_config[state] = %+v, want display_name Stage", cfg)
	}

	// ── and it produces the right query ────────────────────────────────────
	req, served := NewViewFindLoader(newSet(v)).View("active-widgets")
	if !served {
		t.Fatal("a fully version-2 view was not servable; version 2 exists so that it is")
	}
	if req.Type == nil || *req.Type != "widget" {
		t.Errorf("request type = %v, want widget", req.Type)
	}
	if !reflect.DeepEqual(req.Filter, v.Def.Filter) {
		t.Errorf("the request's filter differs from the view's own tree.\n view: %s\n  req: %s",
			renderNode(*v.Def.Filter), renderNode(*req.Filter))
	}
	if req.Filter == v.Def.Filter {
		t.Error("the request aliases the saved view's own tree; a request the engine normalises in place would then rewrite the view on disk's in-memory copy")
	}
	if req.GroupBy == nil || !reflect.DeepEqual(*req.GroupBy, []string{"state", "maker"}) {
		t.Errorf("request group_by = %v, want [state maker]", req.GroupBy)
	}
	if req.Select == nil || !reflect.DeepEqual(*req.Select, []string{"name", "state"}) {
		t.Errorf("request select = %v, want [name state]", req.Select)
	}
	if req.Limit == nil || *req.Limit != 25 {
		t.Errorf("request limit = %v, want 25", req.Limit)
	}
	if req.Sort == nil || len(*req.Sort) != 1 || (*req.Sort)[0].Property != "name" {
		t.Errorf("request sort = %+v, want one key on name", req.Sort)
	}
}

// assertLeaf checks one leaf node's three fields at once, so a failure names
// which of them was wrong rather than dumping a struct.
func assertLeaf(t *testing.T, n generated.VaultFilterNode, property string, op generated.VaultFilterNodeOp, value string) {
	t.Helper()
	if n.Property == nil || *n.Property != property {
		t.Errorf("leaf property = %v, want %q", n.Property, property)
	}
	if n.Op == nil || *n.Op != op {
		t.Errorf("leaf operator = %v, want %q", n.Op, string(op))
	}
	if n.Value == nil || *n.Value != value {
		t.Errorf("leaf value = %v, want %q", n.Value, value)
	}
}

// renderNode renders a filter tree as one line, so a diff failure is readable.
func renderNode(n generated.VaultFilterNode) string {
	switch {
	case n.All != nil:
		return "all(" + renderChildren(*n.All) + ")"
	case n.Any != nil:
		return "any(" + renderChildren(*n.Any) + ")"
	case n.Not != nil:
		return "not(" + renderNode(*n.Not) + ")"
	}
	op, val := "", ""
	if n.Op != nil {
		op = string(*n.Op)
	}
	if n.Value != nil {
		val = *n.Value
	}
	return fmt.Sprintf("%s %s %q", derefOrEmpty(n.Property), op, val)
}

func renderChildren(children []generated.VaultFilterNode) string {
	parts := make([]string, 0, len(children))
	for _, c := range children {
		parts = append(parts, renderNode(c))
	}
	return strings.Join(parts, ", ")
}

// ---------------------------------------------------------------------------
// FR-018b — a version-1 file keeps version-1 semantics, verbatim
// ---------------------------------------------------------------------------

// TestView_V1FileKeepsItsOwnSemanticsVerbatim reads a version-1 file off disk
// alongside the version-2 one above and proves the loader gives it the OLD
// meaning, unmodified.
//
// FR-018b: "A v1 view evaluates with its recorded v1 operators, verbatim …
// files on disk are never rewritten on read."
//
// The assertions are deliberately about the SPELLING as well as the shape. v1
// spells its operators `eq`/`gte` (the RecordFilter vocabulary ruling R-B
// retired) and v2 spells them `=`/`>=`. A loader that had normalised the
// stored file into the new vocabulary would satisfy a shape-only test while
// having done exactly the rewrite the requirement forbids.
func TestView_V1FileKeepsItsOwnSemanticsVerbatim(t *testing.T) {
	v := mustLoadView(t, "shipped.yaml", `
schema_version: 1
name: shipped-widgets
type: widget
filters:
  - property: state
    op: eq
    values:
      - type: enum
        enum: shipped
  - property: batch
    op: gte
    values:
      - type: integer
        integer: "7"
group_by: [state]
sort:
  - property: name
    direction: asc
`)

	if v.Def.SchemaVersion != ViewVersion1 {
		t.Fatalf("schema_version = %d, want 1 — a file on disk is never upgraded on read", v.Def.SchemaVersion)
	}
	// The v2 keys must be absent. Nothing translated them into existence.
	if v.Def.Filter != nil {
		t.Errorf("a version-1 view grew a v2 `filter` tree on load: %s", renderNode(*v.Def.Filter))
	}
	if v.Def.Grouping != nil {
		t.Errorf("a version-1 view grew a v2 `grouping` list on load: %+v", v.Def.Grouping)
	}
	if v.Def.Layout != nil {
		t.Errorf("a version-1 view grew a `layout` on load: %v", v.Def.Layout)
	}

	if v.Def.Filters == nil || len(*v.Def.Filters) != 2 {
		t.Fatalf("the v1 `filters` list did not survive: %+v", v.Def.Filters)
	}
	f := *v.Def.Filters
	if f[0].Op != generated.Eq {
		t.Errorf("filter 1 operator = %q, want the v1 spelling `eq`", string(f[0].Op))
	}
	if f[1].Op != generated.Gte {
		t.Errorf("filter 2 operator = %q, want the v1 spelling `gte`", string(f[1].Op))
	}
	if v.Def.GroupBy == nil || !reflect.DeepEqual(*v.Def.GroupBy, []string{"state"}) {
		t.Errorf("group_by = %v, want [state]", v.Def.GroupBy)
	}

	// And it still produces the right query: v1's flat list is a conjunction,
	// so the request is one `all` of two leaves in find's own spelling. The
	// TRANSLATION AT THE SEAM is legitimate — it happens when the view is
	// served, from the verbatim file, and never writes anything back.
	req, served := NewViewFindLoader(newSet(v)).View("shipped-widgets")
	if !served {
		t.Fatal("a version-1 view using only translatable operators must still be servable")
	}
	if req.Filter == nil || req.Filter.All == nil || len(*req.Filter.All) != 2 {
		t.Fatalf("request filter = %+v, want an `all` of two leaves", req.Filter)
	}
	assertLeaf(t, (*req.Filter.All)[0], "state", generated.Equal, "shipped")
	assertLeaf(t, (*req.Filter.All)[1], "batch", generated.GreaterThanEqual, "7")
	if req.GroupBy == nil || !reflect.DeepEqual(*req.GroupBy, []string{"state"}) {
		t.Errorf("request group_by = %v, want [state]", req.GroupBy)
	}

	// The file on disk was not touched by any of that.
	if v.Def.SchemaVersion != ViewVersion1 || (*v.Def.Filters)[0].Op != generated.Eq {
		t.Error("serving the view mutated the loaded view")
	}
}

// TestView_V1ContainsNeverBecomesLikeOnLoad is the exit proof for FR-018b's
// review finding F5, and it is deliberately not an assertion about absence.
//
// A test that only said "the v1 view is not servable" would pass just as
// happily on a build where `contains` and `LIKE '%…%'` had become synonyms —
// the very state the requirement is about. So this runs in two halves:
//
//  1. MEASURE what the substitution would return. A version-2 view whose
//     leaf is `labels LIKE '%in%'` loads, is servable, and its pattern
//     matches `indoor` and `printing` as well as the element `in` — the
//     three records FR-018b names.
//  2. Prove the version-1 `contains` view produces NO such query: its
//     operator is still `contains` after the load, no request escapes at
//     all, and the refusal names the widening.
//
// Half 1 is what makes half 2 worth having. If LIKE ever stopped being
// broader, half 1 fails and this test says so, rather than silently guarding
// a distinction that no longer exists.
func TestView_V1ContainsNeverBecomesLikeOnLoad(t *testing.T) {
	// The records the spec's own example is stated over (FR-018b: "`labels
	// contains \"in\"` matches the element `in`; `LIKE '%in%'` matches
	// `indoor`, `printing`, `min`").
	records := map[string]Record{
		"exact":     ParseRecord("exact.md", []byte("---\ntype: widget\nname: E\nlabels: [in]\n---\n")),
		"indoor":    ParseRecord("indoor.md", []byte("---\ntype: widget\nname: I\nlabels: [indoor]\n---\n")),
		"printing":  ParseRecord("printing.md", []byte("---\ntype: widget\nname: P\nlabels: [printing]\n---\n")),
		"unrelated": ParseRecord("unrelated.md", []byte("---\ntype: widget\nname: U\nlabels: [outdoor]\n---\n")),
	}
	// Expected verdicts, read off that sentence BEFORE the comparator is
	// asked. `outdoor` is the control: an operator that matched everything
	// could not pass either column.
	wantLike := map[string]bool{"exact": true, "indoor": true, "printing": true, "unrelated": false}
	wantMembership := map[string]bool{"exact": true, "indoor": false, "printing": false, "unrelated": false}

	set, report, schemas := v2Vault(t, map[string]string{
		// The version-2 view that DOES ask for substring matching.
		"substring.yaml": `
schema_version: 2
name: labels-like-in
type: widget
filter:
  property: labels
  op: LIKE
  value: "%in%"
`,
		// The version-1 view that asks for whole-element membership and must
		// never become the one above.
		"membership.yaml": `
schema_version: 1
name: labels-contains-in
type: widget
filters:
  - property: labels
    op: contains
    values:
      - type: text
        text: "in"
`,
	})
	if !report.OK() {
		t.Fatalf("both views are valid files and must LOAD; rejected: %v", report.Rejections)
	}
	sc, ok := schemas.Get("widget")
	if !ok {
		t.Fatal("fixture schema missing")
	}
	loader := NewViewFindLoader(set)

	// ── half 1: what the substitution would have returned ──────────────────
	likeReq, served := loader.View("labels-like-in")
	if !served {
		t.Fatal("a version-2 LIKE view must be servable; without it this test cannot measure what the substitution would do")
	}
	if likeReq.Filter == nil || likeReq.Filter.Op == nil || *likeReq.Filter.Op != generated.LIKE {
		t.Fatalf("the v2 request's operator is %+v, want LIKE", likeReq.Filter)
	}
	likeMatched := runLeaf(t, sc, records, Filter{
		Property: "labels", Op: OpLike, Literal: *likeReq.Filter.Value, LiteralGiven: true,
	}, wantLike, `LIKE "%in%"`)

	// Whole-element membership is the faithful alternative the migration
	// refusal offers for `contains`, and it is the narrower of the two.
	membershipMatched := runLeaf(t, sc, records, Filter{
		Property: "labels", Op: OpEqual, Literal: "in", LiteralGiven: true,
	}, wantMembership, `whole-element membership (= "in")`)

	if likeMatched-membershipMatched != 2 {
		t.Fatalf("LIKE matched %d records and membership matched %d; FR-018b's example requires LIKE to match exactly 2 more (indoor, printing). "+
			"If that is no longer true, the prohibition needs re-deriving rather than re-asserting", likeMatched, membershipMatched)
	}

	// ── half 2: the version-1 view produces no such query ──────────────────
	v1, ok := set.Get("labels-contains-in")
	if !ok {
		t.Fatalf("the v1 view is missing from the set; names: %v", set.Names())
	}
	if v1.Def.SchemaVersion != ViewVersion1 {
		t.Errorf("the v1 file's schema_version is %d after load, want 1", v1.Def.SchemaVersion)
	}
	if v1.Def.Filters == nil || len(*v1.Def.Filters) != 1 || (*v1.Def.Filters)[0].Op != generated.Contains {
		t.Fatalf("the stored operator was rewritten on read: %+v", v1.Def.Filters)
	}
	if v1.Def.Filter != nil {
		t.Fatalf("the v1 view grew a v2 tree on load: %s — that is the translation FR-018b withdrew", renderNode(*v1.Def.Filter))
	}

	req, servedV1 := loader.View("labels-contains-in")
	if servedV1 {
		t.Fatalf("knowledge_find served a version-1 `contains` view as %s; the only way to do that is to have substituted an operator, which would newly return the %d extra records measured above",
			renderNode(*req.Filter), likeMatched-membershipMatched)
	}
	if req.Filter != nil {
		t.Fatalf("a refused view still produced a filter: %s — a partial translation broadens exactly as a whole one does", renderNode(*req.Filter))
	}
	refusal, has := loader.ServeRefusal("labels-contains-in")
	if !has {
		t.Fatal("the view is unservable and FR-018b requires the reason to be NAMED; nothing was reported")
	}
	if refusal.Code != ServeRefusalV1Contains {
		t.Errorf("refusal code = %q, want %q", refusal.Code, ServeRefusalV1Contains)
	}
	if !strings.Contains(refusal.Reason, "contains") {
		t.Errorf("the reason does not name the operator it refused: %s", refusal.Reason)
	}
}

// runLeaf evaluates one filter over the fixture records, checks every verdict
// against the expected map, and returns how many matched.
func runLeaf(t *testing.T, sc *Schema, records map[string]Record, f Filter, want map[string]bool, label string) int {
	t.Helper()
	matched := 0
	for name, rec := range records {
		res, err := f.Match(sc, rec)
		if err != nil {
			t.Fatalf("%s over %s: %v", label, name, err)
		}
		if res.Matched != want[name] {
			t.Fatalf("%s matched %s = %v, want %v — read off FR-018b's own example", label, name, res.Matched, want[name])
		}
		if res.Matched {
			matched++
		}
	}
	return matched
}

// ---------------------------------------------------------------------------
// FR-018b — an unknown version is REFUSED, never defaulted
// ---------------------------------------------------------------------------

// TestView_UnsupportedVersionIsRefusedNamingWhatIsSupported pins the shape of
// the refusal, not just its existence.
//
// A loader that fell back to the nearest version it knew would be the same
// silent-wrong-answer failure the whole surface is written against: the file
// would evaluate under semantics its author never wrote. So the refusal must
// (a) happen, (b) name the version found, and (c) list what IS supported —
// otherwise an operator holding a version-3 file has no way to learn that 1
// and 2 are the choices.
func TestView_UnsupportedVersionIsRefusedNamingWhatIsSupported(t *testing.T) {
	for _, version := range []string{"3", "0", "-1", "99"} {
		t.Run("schema_version "+version, func(t *testing.T) {
			body := fmt.Sprintf("schema_version: %s\nname: future\ntype: widget\n", version)
			_, rej := ParseView("/vault/.omnipus-vault/views/future.yaml", []byte(body))
			if rej == nil {
				t.Fatalf("schema_version %s parsed; an unknown version must never be read as the nearest known one", version)
			}
			if rej.Code != RejectViewUnsupportedVersion {
				t.Fatalf("code = %s, want %s (%s)", rej.Code, RejectViewUnsupportedVersion, rej.Reason)
			}
			if !strings.Contains(rej.Reason, version) {
				t.Errorf("the refusal does not name the version it found (%s): %s", version, rej.Reason)
			}
			for _, supported := range []string{"1", "2"} {
				if !strings.Contains(rej.Reason, supported) {
					t.Errorf("the refusal does not list supported version %s, so the reader cannot learn what to write: %s", supported, rej.Reason)
				}
			}
		})
	}

	// And the set itself is exactly {1, 2} — FR-018b: "`SupportedViewVersion`
	// becomes the set {1, 2}". Asserted directly so that adding a version
	// without a decision fails here.
	if got := supportedViewVersions(); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("supportedViewVersions() = %v, want [1 2]", got)
	}
	for _, v := range []int{1, 2} {
		if !IsSupportedViewVersion(v) {
			t.Errorf("version %d must be readable", v)
		}
	}
	for _, v := range []int{0, 3, -1} {
		if IsSupportedViewVersion(v) {
			t.Errorf("version %d must not be readable", v)
		}
	}

	// SupportedViewVersion is the WRITER's constant, and it is 2: the only
	// in-tree writer (pkg/vaultimport/view_write.go) emits VERSION-2 KEYS — one
	// `filter` tree, `grouping` with a direction, an optional `type`, `layout`.
	//
	// The constant and the writer moved in the same change, which is the only
	// way they may move: stamping a version onto a file made of the OTHER
	// version's keys makes the version partition below refuse it on the very
	// next load, and it would do so silently until somebody re-ran an import.
	// This assertion is one half of the tripwire; the other half cannot live
	// here (pkg/records must not import the importer) and is
	// pkg/vaultimport's TestWrittenViews_LoadBackThroughTheRealLoader, which
	// reloads every produced file through ParseView.
	if SupportedViewVersion != ViewVersion2 {
		t.Errorf("SupportedViewVersion = %d, want %d. Change this only together with pkg/vaultimport/view_write.go — the constant says what KEYS the writer emits, and the two disagreeing produces views this loader rejects",
			SupportedViewVersion, ViewVersion2)
	}
	if !IsSupportedViewVersion(SupportedViewVersion) {
		t.Errorf("the version the writer stamps (%d) is not one this release can READ — every file it writes would be rejected on load", SupportedViewVersion)
	}
}

// ---------------------------------------------------------------------------
// FR-109 — layout is carried, and an unrenderable one is not flattened
// ---------------------------------------------------------------------------

// TestView_LayoutIsCarriedAndPoliced covers the measured failure FR-109 was
// written after: "An Obsidian CARDS view imported as a table, recorded no loss
// at all, and scored CLEAN under the parity exit criterion."
//
// Two halves, and the second is the one that matters. Carrying `cards` is
// necessary; REFUSING an unrecognised layout is what stops the flattening,
// because a bare string field accepts anything and an unrecognised value
// renders as the default table with nobody told.
func TestView_LayoutIsCarriedAndPoliced(t *testing.T) {
	// Every declared layout survives the round trip, including the four the
	// SPA does not render — they exist precisely so the importer can RECORD
	// what an Obsidian view asked for.
	for _, layout := range viewLayoutNames() {
		t.Run("carried: "+layout, func(t *testing.T) {
			v := mustLoadView(t, "l.yaml", fmt.Sprintf(
				"schema_version: 2\nname: l\ntype: widget\nlayout: %s\n", layout))
			if v.Def.Layout == nil {
				t.Fatalf("layout %q was dropped on load", layout)
			}
			if string(*v.Def.Layout) != layout {
				t.Fatalf("layout = %q, want %q", string(*v.Def.Layout), layout)
			}
		})
	}

	// An omitted layout stays ABSENT rather than being filled in with `table`.
	// The contract states table is the default; a loader that wrote it in
	// would make "the author asked for a table" and "the author said nothing"
	// indistinguishable, and the importer's loss report is built on telling
	// them apart.
	v := mustLoadView(t, "plain.yaml", "schema_version: 2\nname: plain\ntype: widget\n")
	if v.Def.Layout != nil {
		t.Errorf("an omitted layout was filled in as %q; absent must stay absent", string(*v.Def.Layout))
	}

	// An unrecognised layout is REFUSED, naming the permitted set. `card`
	// (singular) is the realistic typo and the one that would otherwise render
	// as a silent table.
	for _, bad := range []string{"card", "Cards", "grid", ""} {
		t.Run("refused: "+bad, func(t *testing.T) {
			_, report, _ := v2Vault(t, map[string]string{
				"bad.yaml": fmt.Sprintf("schema_version: 2\nname: bad\ntype: widget\nlayout: %q\n", bad),
			})
			if report.OK() {
				t.Fatalf("layout %q loaded; an unrecognised layout renders as the default table with nobody told (FR-109)", bad)
			}
			rej := report.Rejections[0]
			if rej.Code != RejectViewInvalidLayout {
				t.Fatalf("code = %s, want %s (%s)", rej.Code, RejectViewInvalidLayout, rej.Reason)
			}
			if !strings.Contains(rej.Reason, "cards") || !strings.Contains(rej.Reason, "table") {
				t.Errorf("the refusal does not list the permitted layouts: %s", rej.Reason)
			}
		})
	}

	// `layout` is a VERSION-2 key. A version-1 file setting it is refused
	// rather than honoured — otherwise the relaxation leaks backwards and a v1
	// file starts carrying meaning its version never had.
	_, report, _ := v2Vault(t, map[string]string{
		"v1layout.yaml": "schema_version: 1\nname: v1layout\ntype: widget\nlayout: cards\n",
	})
	if report.OK() {
		t.Fatal("a version-1 view was allowed to set `layout`")
	}
	if report.Rejections[0].Code != RejectViewVersionKeyMismatch {
		t.Fatalf("code = %s, want %s (%s)", report.Rejections[0].Code,
			RejectViewVersionKeyMismatch, report.Rejections[0].Reason)
	}
}

// ---------------------------------------------------------------------------
// The version partition
// ---------------------------------------------------------------------------

// TestView_EveryWireKeyIsVersionClassified is the guard view.go's partition
// comment promises BY NAME.
//
// The comment's claim is the whole reason the three sets are a partition and
// not a denylist: "every `json:` tag on the generated wire type must appear in
// exactly one of the three sets, and every name in the three sets must be a
// real tag. Adding a wire key to ViewDef without deciding which version owns
// it fails TestView_EveryWireKeyIsVersionClassified BY NAME."
//
// Without this test that sentence is a comment describing a test that does not
// exist, and the partition erodes exactly the way a denylist does.
func TestView_EveryWireKeyIsVersionClassified(t *testing.T) {
	classified := map[string]string{}
	for k := range viewV1OnlyKeys {
		classified[k] = "viewV1OnlyKeys"
	}
	for k := range viewV2OnlyKeys {
		if where, dup := classified[k]; dup {
			t.Errorf("key %q is in both %s and viewV2OnlyKeys; a partition assigns each key exactly once", k, where)
		}
		classified[k] = "viewV2OnlyKeys"
	}
	for k := range viewSharedKeys {
		if where, dup := classified[k]; dup {
			t.Errorf("key %q is in both %s and viewSharedKeys; a partition assigns each key exactly once", k, where)
		}
		classified[k] = "viewSharedKeys"
	}

	wire := map[string]struct{}{}
	rt := reflect.TypeOf(generated.ViewDef{})
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			continue
		}
		wire[name] = struct{}{}
		if _, ok := classified[name]; !ok {
			t.Errorf("ViewDef wire key %q belongs to no version set. Add it to viewV1OnlyKeys, viewV2OnlyKeys or viewSharedKeys in view.go — a key in none of them is accepted by BOTH versions, which is what the partition exists to prevent", name)
		}
	}
	for name, where := range classified {
		if _, ok := wire[name]; !ok {
			t.Errorf("%s names %q, which is not a `json:` tag on generated.ViewDef; the partition is guarding a key that no longer crosses the wire", where, name)
		}
	}
}

// TestView_VersionKeyMismatchIsRefusedBothWays proves the partition is
// enforced in BOTH directions and that the refusal is informative.
//
// A file carrying a key from the other version is a file whose author
// disagrees with itself about which query it is. Preferring either spelling
// would answer a question nobody asked — and in the v1-file-with-`filter:`
// direction the loss is silent, because both keys decode cleanly onto the one
// generated type and the tree would simply never be read.
func TestView_VersionKeyMismatchIsRefusedBothWays(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		foreign   string
		otherSide string
	}{
		{
			name: "a version-1 file with a v2 filter tree",
			body: `
schema_version: 1
name: mixed
type: widget
filters:
  - property: state
    op: eq
    values: [{type: enum, enum: shipped}]
filter:
  property: state
  op: "="
  value: draft
`,
			foreign:   "filter",
			otherSide: "2",
		},
		{
			name:      "a version-1 file with v2 grouping",
			body:      "schema_version: 1\nname: mixed\ntype: widget\ngrouping: [{property: state}]\n",
			foreign:   "grouping",
			otherSide: "2",
		},
		{
			name:      "a version-2 file with a v1 filters list",
			body:      "schema_version: 2\nname: mixed\ntype: widget\nfilters: []\n",
			foreign:   "filters",
			otherSide: "1",
		},
		{
			name:      "a version-2 file with a bare group_by",
			body:      "schema_version: 2\nname: mixed\ntype: widget\ngroup_by: [state]\n",
			foreign:   "group_by",
			otherSide: "1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, rej := ParseView("/vault/.omnipus-vault/views/mixed.yaml", []byte(tc.body))
			if rej == nil {
				t.Fatal("the mixed-vocabulary file parsed")
			}
			if rej.Code != RejectViewVersionKeyMismatch {
				t.Fatalf("code = %s, want %s (%s)", rej.Code, RejectViewVersionKeyMismatch, rej.Reason)
			}
			if !strings.Contains(rej.Reason, tc.foreign) {
				t.Errorf("the refusal does not name the offending key %q: %s", tc.foreign, rej.Reason)
			}
			if !strings.Contains(rej.Reason, tc.otherSide) {
				t.Errorf("the refusal does not say which version %q belongs to: %s", tc.foreign, rej.Reason)
			}
		})
	}

	// `filters: []` above is the case a struct-side check would MISS — an
	// empty v1 list decodes to a non-nil-but-empty slice that is
	// indistinguishable from absence once decoded. Stated here so that a
	// later "simplification" to reading the decoded struct fails with the
	// reason attached.
}

// ---------------------------------------------------------------------------
// FR-018b / FR-018d — the optional type
// ---------------------------------------------------------------------------

// TestView_UntypedV2ViewLoads covers FR-018b's "`type` is OPTIONAL" and the
// two edges either side of it.
//
// The relaxation is BY VERSION. A version-1 view has no untyped semantics to
// fall back on, so it stays rejected exactly as it always was; and a `type:`
// that is present but blank is a typo at either version, never the deliberate
// absence — treating it as untyped would turn a misspelling into a vault-wide
// query.
func TestView_UntypedV2ViewLoads(t *testing.T) {
	// An untyped v2 view loads, and its ordinary property names are NOT
	// checked against any single schema: FR-018b resolves them by name over
	// FR-021e's rows at query time, so there is no name the loader could
	// refuse without refusing a query FR-018b requires to work.
	v := mustLoadView(t, "folder.yaml", `
schema_version: 2
name: folder-scoped
filter:
  property: undeclared_anywhere
  op: IS NOT NULL
properties: [name, undeclared_anywhere]
`)
	if v.Def.Type != nil {
		t.Fatalf("type = %v, want absent", v.Def.Type)
	}

	// A version-1 view with no type is still rejected.
	_, rej := ParseView("/v/x.yaml", []byte("schema_version: 1\nname: v\n"))
	if rej == nil || rej.Code != RejectViewMissingType {
		t.Fatalf("a v1 view with no type must be rejected, got %+v", rej)
	}

	// A blank type is a typo at either version.
	for _, version := range []int{1, 2} {
		body := fmt.Sprintf("schema_version: %d\nname: v\ntype: \"   \"\n", version)
		_, rej := ParseView("/v/x.yaml", []byte(body))
		if rej == nil || rej.Code != RejectViewMissingType {
			t.Fatalf("version %d: a blank `type` must be refused as a typo, got %+v", version, rej)
		}
	}

	// A type NO schema declares is still drift, at version 2 as at version 1
	// (FR-018d: "`RejectViewUnknownType` still fires for a type NO schema
	// declares — that is drift, not provisioning").
	_, report, _ := v2Vault(t, map[string]string{
		"gone.yaml": "schema_version: 2\nname: gone\ntype: sprocket\n",
	})
	if report.OK() {
		t.Fatal("a v2 view naming an undeclared type loaded; the optional type does not make an unknown one acceptable")
	}
	if report.Rejections[0].Code != RejectViewUnknownType {
		t.Fatalf("code = %s, want %s (%s)", report.Rejections[0].Code,
			RejectViewUnknownType, report.Rejections[0].Reason)
	}
}

// ---------------------------------------------------------------------------
// FR-023c — the tree bound, applied to a view
// ---------------------------------------------------------------------------

// TestView_V2FilterTreeBoundsAreRefusedAtLoad checks the two numbers FR-023c
// states, on the side of each that must fail.
//
// The bound is measured at LOAD rather than left to the query path because a
// view is written once and evaluated forever: a tree that will be refused on
// every query should be refused when it is stored, naming which bound it
// broke.
func TestView_V2FilterTreeBoundsAreRefusedAtLoad(t *testing.T) {
	// FR-023c's two numbers, WRITTEN OUT rather than read from the code's own
	// constants. A test that sized its fixtures from maxViewFilterLeaves would
	// follow the constant anywhere it was moved to, and pass at a cap of 128
	// as happily as at 64 — which is precisely the guard failing open.
	const specLeafCap = 64
	const specDepthCap = 8
	if maxViewFilterLeaves != specLeafCap || maxViewFilterDepth != specDepthCap {
		t.Fatalf("the code caps a view filter at %d leaves / depth %d; FR-023c states %d and %d",
			maxViewFilterLeaves, maxViewFilterDepth, specLeafCap, specDepthCap)
	}

	leafYAML := func(indent string) string {
		return indent + "- property: batch\n" + indent + "  op: IS NOT NULL\n"
	}
	flat := func(n int) string {
		var b strings.Builder
		b.WriteString("schema_version: 2\nname: wide\ntype: widget\nfilter:\n  all:\n")
		for i := 0; i < n; i++ {
			b.WriteString(leafYAML("    "))
		}
		return b.String()
	}

	// 64 leaves is the cap and must LOAD; 65 must not.
	if _, report, _ := v2Vault(t, map[string]string{"w.yaml": flat(specLeafCap)}); !report.OK() {
		t.Fatalf("a tree at FR-023c's cap of %d leaves was refused: %v", specLeafCap, report.Rejections)
	}
	_, report, _ := v2Vault(t, map[string]string{"w.yaml": flat(specLeafCap + 1)})
	if report.OK() {
		t.Fatalf("a tree of %d leaves loaded; FR-023c caps a filter at %d", specLeafCap+1, specLeafCap)
	}
	if report.Rejections[0].Code != RejectViewFilterTooLarge {
		t.Fatalf("code = %s, want %s (%s)", report.Rejections[0].Code,
			RejectViewFilterTooLarge, report.Rejections[0].Reason)
	}
	if !strings.Contains(report.Rejections[0].Reason, "64") {
		t.Errorf("the refusal does not name the bound it broke: %s", report.Rejections[0].Reason)
	}

	// Depth. `not` nests one node per level, so the depth is exact.
	nested := func(levels int) string {
		var b strings.Builder
		b.WriteString("schema_version: 2\nname: deep\ntype: widget\nfilter:\n")
		indent := "  "
		for i := 0; i < levels-1; i++ {
			b.WriteString(indent + "not:\n")
			indent += "  "
		}
		b.WriteString(indent + "property: batch\n" + indent + "op: IS NOT NULL\n")
		return b.String()
	}
	if _, atCap, _ := v2Vault(t, map[string]string{"d.yaml": nested(specDepthCap)}); !atCap.OK() {
		t.Fatalf("a tree at FR-023c's depth cap of %d was refused: %v", specDepthCap, atCap.Rejections)
	}
	_, report, _ = v2Vault(t, map[string]string{"d.yaml": nested(specDepthCap + 1)})
	if report.OK() {
		t.Fatalf("a tree %d levels deep loaded; FR-023c caps depth at %d", specDepthCap+1, specDepthCap)
	}
	if report.Rejections[0].Code != RejectViewFilterTooLarge {
		t.Fatalf("code = %s, want %s (%s)", report.Rejections[0].Code,
			RejectViewFilterTooLarge, report.Rejections[0].Reason)
	}
	if !strings.Contains(report.Rejections[0].Reason, "8") {
		t.Errorf("the refusal does not name the depth bound it broke: %s", report.Rejections[0].Reason)
	}

	// A node that is neither a leaf nor a combinator, and one that is both.
	for _, tc := range []struct{ name, filter string }{
		{"empty node", "filter:\n  {}\n"},
		{"leaf and combinator at once", "filter:\n  property: batch\n  op: IS NOT NULL\n  all:\n    - property: batch\n      op: IS NULL\n"},
		{"childless combinator", "filter:\n  all: []\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "schema_version: 2\nname: odd\ntype: widget\n" + tc.filter
			_, report, _ := v2Vault(t, map[string]string{"o.yaml": body})
			if report.OK() {
				t.Fatal("a malformed filter node loaded")
			}
			if report.Rejections[0].Code != RejectViewInvalidFilterNode {
				t.Fatalf("code = %s, want %s (%s)", report.Rejections[0].Code,
					RejectViewInvalidFilterNode, report.Rejections[0].Reason)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FR-140s / FR-018b — formulas and property_config
// ---------------------------------------------------------------------------

// TestView_V2FormulasAreRevalidatedOnLoad covers the second half of FR-140's
// rule: "The parser lives in the write path … the view loader re-validates on
// load, so a hand-edited file is re-checked."
//
// A view file is a text file an operator can open. If only the writer checked
// formulas, an edit made in a text editor would be discovered broken at query
// time — the failure this loader exists to move forward.
func TestView_V2FormulasAreRevalidatedOnLoad(t *testing.T) {
	// A valid formula loads, and is referenceable as `formula.<name>` in a
	// property position (FR-018c's reserved namespace).
	v := mustLoadView(t, "calc.yaml", `
schema_version: 2
name: calc
type: widget
formulas:
  doubled: batch * 2
properties: [name, formula.doubled]
`)
	if v.Def.Formulas == nil || (*v.Def.Formulas)["doubled"] != "batch * 2" {
		t.Fatalf("the formula source text did not survive verbatim: %+v", v.Def.Formulas)
	}

	// A reference to a formula the view does not declare is refused, naming
	// what IS declared — a dangling `formula.` reference would otherwise
	// resolve against nothing and return an empty column in silence.
	_, report, _ := v2Vault(t, map[string]string{"dangling.yaml": `
schema_version: 2
name: dangling
type: widget
formulas:
  doubled: batch * 2
properties: [formula.tripled]
`})
	if report.OK() {
		t.Fatal("a reference to an undeclared formula loaded")
	}
	if report.Rejections[0].Code != RejectViewUnknownFormula {
		t.Fatalf("code = %s, want %s (%s)", report.Rejections[0].Code,
			RejectViewUnknownFormula, report.Rejections[0].Reason)
	}
	if !strings.Contains(report.Rejections[0].Reason, "doubled") {
		t.Errorf("the refusal does not list the formulas that ARE declared: %s", report.Rejections[0].Reason)
	}

	// An expression that does not parse is refused at LOAD, not stored and
	// discovered later.
	_, report, _ = v2Vault(t, map[string]string{"broken.yaml": `
schema_version: 2
name: broken
type: widget
formulas:
  bad: "batch * "
`})
	if report.OK() {
		t.Fatal("an unparseable formula loaded; FR-140 requires the loader to re-check a hand-edited file")
	}
	if report.Rejections[0].Code != RejectViewInvalidFormula {
		t.Fatalf("code = %s, want %s (%s)", report.Rejections[0].Code,
			RejectViewInvalidFormula, report.Rejections[0].Reason)
	}

	// property_config keys are PROPERTY names and are checked as such — a
	// config entry for a property that does not exist is a column heading
	// nothing will ever render.
	_, report, _ = v2Vault(t, map[string]string{"cfg.yaml": `
schema_version: 2
name: cfg
type: widget
property_config:
  nonexistent:
    display_name: Ghost
`})
	if report.OK() {
		t.Fatal("property_config named an undeclared property and loaded")
	}
	if report.Rejections[0].Code != RejectViewUnknownProperty {
		t.Fatalf("code = %s, want %s (%s)", report.Rejections[0].Code,
			RejectViewUnknownProperty, report.Rejections[0].Reason)
	}
	if !strings.Contains(report.Rejections[0].Reason, "property_config") {
		t.Errorf("the refusal does not say WHERE the bad name is: %s", report.Rejections[0].Reason)
	}
}
