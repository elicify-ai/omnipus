// Omnipus — the FR-018b prohibition, measured: a version-1 view's `contains`
// is never rewritten as `LIKE '%…%'` on load.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// WHAT THIS FILE PROVES, AND WHY IT IS ITS OWN FILE
//
// Spec Draft 10 said a version-1 view "loads translated to v2 semantics",
// with `contains` becoming `LIKE '%…%'`. Draft 11 withdrew that as review
// finding F5, in these words: it is BROADENING, applied automatically, in
// the revision that declared broadening the one prohibited thing.
//
// A note is not a guard. So this file does two things in order:
//
//	1. MEASURES THE WIDENING with the package's own comparator, over the
//	   `in` / `indoor` example the spec itself uses. Not asserted — run. If
//	   the two operators ever stopped differing, step 1 would fail and this
//	   whole file would be telling us so, rather than silently guarding a
//	   distinction that no longer exists.
//	2. Proves the loader performs NEITHER the substitution nor any partial
//	   one: the view loads, is listed, and is reported unservable with the
//	   widening named.
//
// Step 1 is what makes step 2 worth having. A test that only asserted
// "View() returns false" would pass just as happily on a build where
// `contains` and `LIKE` had become synonyms.
// ---------------------------------------------------------------------------

// labelSchema declares one many-valued text property, which is the shape
// `contains` is defined over (spec §8 R-9: whole-element membership on a
// list).
const labelSchemaFixture = `
schema_version: 1
type: widget
identity:
  prefix: WI
properties:
  name:   { type: text, required: true }
  labels: { type: text, many: true }
`

// TestV1Contains_LikeSubstitutionIsMeasurablyBroader is step 1.
//
// THE ORACLE IS THE SPECIFICATION, NOT THE CODE. FR-018b states the
// difference in full: "`labels contains \"in\"` matches the element `in`;
// `LIKE '%in%'` matches `indoor`, `printing`, `min`". The expected verdict
// for each record below is read off that sentence and written down here
// BEFORE the comparator is asked.
//
// `=` on a many-valued property is whole-element membership, which is the
// faithful alternative the migration refusal offers for `contains`; `LIKE`
// is the substitution Draft 10 proposed. The test requires them to disagree
// on exactly the two records the spec names.
func TestV1Contains_LikeSubstitutionIsMeasurablyBroader(t *testing.T) {
	set := loadSet(t, map[string]string{"widget.yaml": labelSchemaFixture})
	sc, ok := set.Get("widget")
	if !ok {
		t.Fatal("fixture schema did not load")
	}

	records := map[string]Record{
		// The one record whole-element membership is about.
		"exact": ParseRecord("exact.md", []byte("---\ntype: widget\nname: E\nlabels: [in]\n---\n")),
		// The two the spec names as newly, wrongly matched.
		"indoor":   ParseRecord("indoor.md", []byte("---\ntype: widget\nname: I\nlabels: [indoor]\n---\n")),
		"printing": ParseRecord("printing.md", []byte("---\ntype: widget\nname: P\nlabels: [printing, min]\n---\n")),
		// A control that neither operator may match, so a filter that
		// accidentally matched everything could not pass this test.
		"unrelated": ParseRecord("unrelated.md", []byte("---\ntype: widget\nname: U\nlabels: [outdoor]\n---\n")),
	}

	// Expected verdicts, derived from FR-018b's own sentence.
	wantMembership := map[string]bool{"exact": true, "indoor": false, "printing": false, "unrelated": false}
	wantLike := map[string]bool{"exact": true, "indoor": true, "printing": true, "unrelated": false}

	membership := Filter{Property: "labels", Op: OpEqual, Literal: "in", LiteralGiven: true}
	like := Filter{Property: "labels", Op: OpLike, Literal: "%in%", LiteralGiven: true}

	run := func(t *testing.T, f Filter, want map[string]bool, label string) int {
		t.Helper()
		matched := 0
		for name, rec := range records {
			res, err := f.Match(sc, rec)
			if err != nil {
				t.Fatalf("%s over %s: %v", label, name, err)
			}
			if res.Matched != want[name] {
				t.Fatalf("%s matched %s = %v, want %v — the spec's own example (FR-018b) says otherwise",
					label, name, res.Matched, want[name])
			}
			if res.Matched {
				matched++
			}
		}
		return matched
	}

	nMembership := run(t, membership, wantMembership, `whole-element membership (= "in")`)
	nLike := run(t, like, wantLike, `substring (LIKE "%in%")`)

	if nLike <= nMembership {
		t.Fatalf("the LIKE substitution matched %d records and whole-element membership matched %d; "+
			"this test exists because LIKE is BROADER, and if it no longer is, FR-018b's prohibition needs re-deriving rather than re-asserting",
			nLike, nMembership)
	}
	if nLike-nMembership != 2 {
		t.Fatalf("LIKE matched %d extra records, want exactly the 2 FR-018b names (indoor, printing)", nLike-nMembership)
	}
}

// TestViewFindLoader_V1ContainsIsNeverBroadenedToLike is step 2, and it is
// the exit proof for FR-018b.
//
// A version-1 view using `contains` is read off DISK — not hand-built —
// because the requirement is about files already on disk. It must:
//
//	load cleanly              (it is a valid v1 view; nothing is wrong with it)
//	appear in the ViewSet     (knowledge_describe still lists it)
//	NOT be servable by find   (no substitution was made)
//	carry a NAMED reason      (FR-018b: "the reason named")
//
// MUTATION: add `case generated.Contains:` to recordFilterOpToFindOp
// returning generated.Like, or replace the ServeRefusalV1Contains branch in
// translateRecordFilter with a `%…%` leaf, and this test fails on the
// View()/ServeRefusal assertions.
func TestViewFindLoader_V1ContainsIsNeverBroadenedToLike(t *testing.T) {
	root := writeVaultSchema(t, "", "widget.yaml", labelSchemaFixture)
	root = writeVaultView(t, root, "indoor.yaml", `
schema_version: 1
name: has-in
type: widget
label: Labelled "in"
filters:
  - property: labels
    op: contains
    values:
      - type: text
        text: "in"
`)
	schemas, report, err := LoadSchemas(root)
	if err != nil || !report.OK() {
		t.Fatalf("fixture schemas: %v / %v", err, report)
	}

	views, vreport, err := LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}

	// It LOADS. FR-018b: a v1 `contains` view "stays precisely as it is —
	// listed by knowledge_describe". Rejecting it here would be a different
	// wrong answer from broadening it, and just as unacceptable.
	if !vreport.OK() {
		t.Fatalf("a version-1 `contains` view must LOAD unchanged; it was rejected: %v", vreport.Rejections)
	}
	v, ok := views.Get("has-in")
	if !ok {
		t.Fatalf("the view is missing from the set; names: %v", views.Names())
	}
	if v.Def.SchemaVersion != ViewVersion1 {
		t.Fatalf("schema_version = %d, want 1 — the file on disk must never be upgraded on read", v.Def.SchemaVersion)
	}
	if v.Def.Filters == nil || len(*v.Def.Filters) != 1 {
		t.Fatalf("the v1 `filters` list did not survive the load: %+v", v.Def.Filters)
	}
	if op := (*v.Def.Filters)[0].Op; op != "contains" {
		t.Fatalf("the stored operator is %q, want `contains` — nothing may rewrite it on read", string(op))
	}

	loader := NewViewFindLoader(views)

	// It is NOT SERVED, and no request escapes.
	req, served := loader.View("has-in")
	if served {
		t.Fatal("knowledge_find served a version-1 `contains` view; the only way to do that is to have substituted an operator, which FR-018b prohibits")
	}
	if req.Filter != nil {
		t.Fatalf("a refused view still produced a filter: %+v — a partial translation is the broadening this rule forbids", req.Filter)
	}
	for _, n := range loader.Names() {
		if n == "has-in" {
			t.Fatal("Names() offers a view View() refuses; the two must agree")
		}
	}

	// The reason is NAMED, and it names the widening rather than shrugging.
	refusal, has := loader.ServeRefusal("has-in")
	if !has {
		t.Fatal("ServeRefusal reported nothing for an unservable view; FR-018b requires the reason to be named")
	}
	if refusal.Code != ServeRefusalV1Contains {
		t.Fatalf("refusal code = %q, want %q", refusal.Code, ServeRefusalV1Contains)
	}
	if refusal.Name != "has-in" {
		t.Fatalf("refusal names view %q, want has-in", refusal.Name)
	}
	for _, want := range []string{"contains", "indoor"} {
		if !strings.Contains(refusal.Reason, want) {
			t.Errorf("the refusal reason does not mention %q, so an operator reading it cannot see what would have widened: %s", want, refusal.Reason)
		}
	}
	if !strings.Contains(refusal.Remedy, "knowledge_configure") {
		t.Errorf("the refusal offers no route to migration: %s", refusal.Remedy)
	}

	// And it is listed as unservable, which is the health list FR-018b names
	// as the standing cost of keeping two vocabularies alive.
	un := loader.Unservable()
	if len(un) != 1 || un[0].Name != "has-in" {
		t.Fatalf("Unservable() = %+v, want exactly the one unmigrated view", un)
	}
}

// TestViewFindLoader_V1ViaStaysUntranslatableWithAReason is the second half
// of the v1 gap: a per-leaf relation hop has no request-level equivalent.
func TestViewFindLoader_V1ViaStaysUntranslatableWithAReason(t *testing.T) {
	v := newTestView("via-deals", []generated.RecordFilter{
		{Property: "industry", Op: generated.Eq, Via: ptr([]string{"company"}),
			Values: ptr([]generated.RecordValue{{Type: "text", Text: ptr("fintech")}})},
	})
	loader := NewViewFindLoader(newSet(v))

	if _, ok := loader.View("via-deals"); ok {
		t.Fatal("a `via` view was served; find's leaf grammar has no `via`")
	}
	refusal, has := loader.ServeRefusal("via-deals")
	if !has || refusal.Code != ServeRefusalV1Via {
		t.Fatalf("ServeRefusal = %+v (%v), want a %s", refusal, has, ServeRefusalV1Via)
	}
	if !strings.Contains(refusal.Reason, "company") {
		t.Errorf("the reason does not name the hop it refused: %s", refusal.Reason)
	}
}
