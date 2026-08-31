// Omnipus — FR-105 / the mixed-type disjunction, settled by absorption.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// WHAT THIS FILE IS ABOUT
//
// An `or:` whose branches name DIFFERENT record types was refused for seven
// groups across two of the founder's bases, and the refusal was reported as a
// WIRE FORMAT limit: "there is no ViewDef.types (a list of record types), so it
// is genuinely unrepresentable".
//
// It was not a wire format limit. Every one of the seven sits in a base's OUTER
// filter, and every view that inherits it re-asserts ONE of the disjuncts in
// its own `and:`:
//
//	Content.base outer:  file.inFolder("01-Areas/Content") AND (type=="content" OR type=="brand-kit")
//	Content.base view:   type=="content"
//	effective:           inFolder AND (content OR brand-kit) AND content
//	                  == inFolder AND content                    (X ∨ Y) ∧ X ≡ X
//
// So the disjunction is ABSORBED. Nothing about it needed to be representable,
// because after conjunction with the view's own filter it is not there.
//
// A MULTI-TYPE VIEW WAS THE OTHER ROUTE AND IT IS BUILDABLE — knowledgefind's
// untyped namespace already implements the hard part (resolveUntyped refuses a
// name two in-scope types declare with different domains, LOUDLY, which is the
// `status`-is-enum-on-task-and-text-on-content case) and kind=record is the
// precedent for narrowing by record type in Go rather than in the propindex
// Selector. It is simply not needed here, and a TYPED view is strictly better
// than a multi-type one: it keeps full schema property checking and enum
// validation, both of which a multi-type view weakens.
//
// The tests below are in two halves. The first proves the rewrite on trees
// built by hand, including every place it must REFUSE. The second serves the
// seven real views through the real loaders and grades them against the
// independent oracle — and, per view, proves the grade could have detected a
// broadening at all before it counts as evidence.
// ---------------------------------------------------------------------------

// mtdOuter is Content.base's outer filter, as the YAML parser hands it over.
func mtdOuter() map[string]any {
	return map[string]any{"and": []any{
		`file.inFolder("01-Areas/Content")`,
		map[string]any{"or": []any{`type == "content"`, `type == "brand-kit"`}},
	}}
}

// mtdFundraisingOuter is Fundraising.base's outer filter: the branches disagree
// about the type AND one of them carries a residual condition.
func mtdFundraisingOuter() map[string]any {
	return map[string]any{"and": []any{
		`file.inFolder("01-Areas/Fundraising")`,
		map[string]any{"or": []any{
			`type == "round"`,
			map[string]any{"and": []any{`type == "company"`, `segment.contains("investor")`}},
		}},
	}}
}

// mtdDescribe renders a translated tree in a shape a failure message can be
// read from. It deliberately does not use the production Verbatim: this has to
// say what the tree IS, not what it came from.
func mtdDescribe(n *rawNode) string {
	if n == nil {
		return "<nothing>"
	}
	switch n.Kind {
	case rawKindLost:
		return "LOST(" + strings.TrimSpace(n.Verbatim) + ")"
	case rawKindLeaf:
		return "leaf(" + n.Leaf.Property + ")"
	case rawKindPrebuilt:
		return "prebuilt"
	case rawKindTypedAny:
		parts := make([]string, 0, len(n.Branches))
		for _, b := range n.Branches {
			parts = append(parts, b.RecordType+"=>"+mtdDescribe(b.Remainder))
		}
		return "typedAny[" + strings.Join(parts, " | ") + "]"
	case rawKindAll, rawKindAny, rawKindNot:
		names := map[rawKind]string{rawKindAll: "all", rawKindAny: "any", rawKindNot: "not"}
		parts := make([]string, 0, len(n.Kids))
		for _, k := range n.Kids {
			parts = append(parts, mtdDescribe(k))
		}
		return names[n.Kind] + "(" + strings.Join(parts, ", ") + ")"
	}
	return "?"
}

// ---------------------------------------------------------------------------
// HALF ONE — THE REWRITE, ON TREES BUILT BY HAND
// ---------------------------------------------------------------------------

// The absorbing case: every surviving branch was nothing but its type literal,
// so under that type the disjunction is TRUE and contributes no constraint.
// What must remain is the folder scope, ALONE — not the folder scope AND a
// disjunction that has been quietly widened to always match.
func TestMixedTypeDisjunction_AbsorbedByTheViewsOwnType(t *testing.T) {
	for _, typeName := range []string{"content", "brand-kit"} {
		trans := TranslateFilterTree(mtdOuter())
		if got := mtdDescribe(trans.Root); got != "all(prebuilt, typedAny[brand-kit=><nothing> | content=><nothing>])" &&
			got != "all(prebuilt, typedAny[content=><nothing> | brand-kit=><nothing>])" {
			t.Fatalf("the outer filter should DEFER the mixed group, not decide it; got %s", got)
		}
		if len(trans.TypeLiterals) != 0 {
			t.Errorf("a deferred disjunction must harvest NO type — %q is not %q — but it harvested %v",
				"either type", "this type", trans.TypeLiterals)
		}

		reduced := ReduceTypedDisjunctions(trans, typeName)
		if got := mtdDescribe(reduced.Root); got != "prebuilt" {
			t.Errorf("under type=%q the disjunction is absorbed and only the folder scope should survive; got %s", typeName, got)
		}
	}
}

// The Fundraising case: the surviving branch has a REMAINDER, and the remainder
// is what must be left behind. Losing it would drop `segment.contains(...)` and
// return every company in the vault — the broadening this whole discipline
// exists to prevent, arriving through the fix rather than through the gap.
func TestMixedTypeDisjunction_KeepsTheSurvivingBranchsRemainder(t *testing.T) {
	trans := TranslateFilterTree(mtdFundraisingOuter())

	underCompany := mtdDescribe(ReduceTypedDisjunctions(trans, "company").Root)
	if underCompany != "all(prebuilt, leaf(segment))" {
		t.Errorf("under type=company the round branch is false and the company branch reduces to its remainder,\n"+
			"so the result must still REQUIRE segment; want all(prebuilt, leaf(segment)), got %s", underCompany)
	}

	underRound := mtdDescribe(ReduceTypedDisjunctions(trans, "round").Root)
	if underRound != "prebuilt" {
		t.Errorf("under type=round the round branch is TRUE (it was only its type literal) and the company branch is false,\n"+
			"so only the folder scope survives; want prebuilt, got %s", underRound)
	}
}

// THE SHARED-TREE TEST. A base's outer filter is translated ONCE and handed to
// every view in the base. Content.base has five views resolving to two
// different types. If the reduction mutated the tree in place, the first view's
// type would silently decide the other four — and because absorption usually
// reduces to TRUE, the failure mode is a view whose type filter quietly stops
// constraining anything.
func TestMixedTypeDisjunction_ReducingDoesNotMutateTheSharedTree(t *testing.T) {
	trans := TranslateFilterTree(mtdFundraisingOuter())
	before := mtdDescribe(trans.Root)

	// Reduce under one type, then under the OTHER, from the same source tree.
	first := mtdDescribe(ReduceTypedDisjunctions(trans, "company").Root)
	after := mtdDescribe(trans.Root)
	second := mtdDescribe(ReduceTypedDisjunctions(trans, "round").Root)

	if after != before {
		t.Errorf("reducing rewrote the SHARED source tree: before %s, after %s", before, after)
	}
	if first != "all(prebuilt, leaf(segment))" {
		t.Errorf("first reduction wrong: %s", first)
	}
	if second != "prebuilt" {
		t.Errorf("second reduction saw the first one's rewrite: got %s, want prebuilt", second)
	}
}

// An UNTYPED view cannot reduce: with no type asserted the disjunction really
// does span two domains, and importing it untyped is the vault-wide broadening
// the original refusal correctly named. The node must degrade to a loss
// carrying the SAME verbatim, so the report classifies it exactly as before.
func TestMixedTypeDisjunction_UntypedViewStillRefuses(t *testing.T) {
	trans := TranslateFilterTree(mtdOuter())
	reduced := ReduceTypedDisjunctions(trans, "")

	got := mtdDescribe(reduced.Root)
	if !strings.HasPrefix(got, "all(prebuilt, LOST(") {
		t.Fatalf("an untyped view must NOT reduce a mixed disjunction; got %s", got)
	}
	if !strings.Contains(got, `type == "content"`) || !strings.Contains(got, `type == "brand-kit"`) {
		t.Errorf("the loss must carry the original expression verbatim so the report still names it; got %s", got)
	}
}

// A view whose type matches NO branch makes the effective filter FALSE. That is
// exact — the Obsidian view returns nothing — but emitting a view that can
// never match is a different decision from translating a filter, so it stays
// lost. The point of the test is that it is not silently reduced to TRUE, which
// is the same code path with one comparison inverted.
func TestMixedTypeDisjunction_NoSurvivingBranchRefusesRatherThanEmptying(t *testing.T) {
	trans := TranslateFilterTree(mtdOuter())
	got := mtdDescribe(ReduceTypedDisjunctions(trans, "invoice").Root)
	if !strings.HasPrefix(got, "all(prebuilt, LOST(") {
		t.Errorf("no branch names type=invoice, so the group must stay lost rather than vanish or empty; got %s", got)
	}
}

// UNDER A NEGATION the rewrite is refused. The equivalence itself survives
// negation, so this is conservatism rather than necessity — but `not:` is where
// a narrowing becomes a broadening in this importer (knowledgefind's tree.go
// evaluates a bare !inner.matched, with no absence rule), no base in this vault
// has the shape, and an ungraded reduction under a negation is not worth the
// row it might invent.
func TestMixedTypeDisjunction_RefusedUnderANegation(t *testing.T) {
	negated := map[string]any{"not": []any{
		map[string]any{"or": []any{`type == "content"`, `type == "brand-kit"`}},
	}}
	trans := TranslateFilterTree(negated)
	if got := mtdDescribe(trans.Root); !strings.HasPrefix(got, "LOST(") {
		t.Fatalf("a mixed disjunction under a `not:` must be lost whole at translation time; got %s", got)
	}
	// And it must still be lost after a reduction pass, under any type.
	if got := mtdDescribe(ReduceTypedDisjunctions(trans, "content").Root); !strings.HasPrefix(got, "LOST(") {
		t.Errorf("reducing must not resurrect a negated mixed disjunction; got %s", got)
	}
}

// A branch that cannot be translated poisons the group, exactly as before. The
// branches of a deferred disjunction live OUTSIDE Kids, so the walk that
// decides whether a group is carried has to look at them specifically —
// forgetting to is how an untranslatable leaf rides into a live view inside a
// branch remainder.
func TestMixedTypeDisjunction_AnUntranslatableBranchIsStillLostWhole(t *testing.T) {
	withJunk := map[string]any{"or": []any{
		`type == "round"`,
		map[string]any{"and": []any{`type == "company"`, `date(close_date).year == today().year`}},
	}}
	trans := TranslateFilterTree(withJunk)
	if got := mtdDescribe(trans.Root); !strings.HasPrefix(got, "LOST(") {
		t.Errorf("a branch carrying an untranslatable leaf must lose the whole group; got %s", got)
	}
}

// The SAME-type disjunction — Subscriptions.base — must be untouched by all of
// this. It factors by distributivity and harvests its type, and it did so
// before this change; a regression here would disable four working views.
func TestMixedTypeDisjunction_SameTypeDistributivityIsUnchanged(t *testing.T) {
	same := map[string]any{"or": []any{
		map[string]any{"and": []any{`file.inFolder("a")`, `type == "subscription"`}},
		map[string]any{"and": []any{`file.inFolder("b")`, `type == "subscription"`}},
	}}
	trans := TranslateFilterTree(same)
	if len(trans.TypeLiterals) != 1 || trans.TypeLiterals[0] != "subscription" {
		t.Errorf("a same-type disjunction must still HARVEST its type; got %v", trans.TypeLiterals)
	}
	if got := mtdDescribe(trans.Root); got != "any(prebuilt, prebuilt)" {
		t.Errorf("the remainders must still become the disjunction; got %s", got)
	}
}

// A disjunction with an UNTYPED branch must not become a deferred one. Such a
// branch survives whatever the view's type is, so the reduction would still be
// exact — but nothing in this vault has the shape, so it stays refused rather
// than becoming an ungraded code path.
func TestMixedTypeDisjunction_AnUntypedBranchIsNotDeferred(t *testing.T) {
	mixed := map[string]any{"or": []any{`type == "round"`, `stage == "open"`}}
	if got := mtdDescribe(TranslateFilterTree(mixed).Root); !strings.HasPrefix(got, "LOST(") {
		t.Errorf("a disjunction mixing a typed and an untyped branch must stay lost; got %s", got)
	}
}

// ---------------------------------------------------------------------------
// HALF TWO — THE SEVEN REAL VIEWS, GRADED THROUGH THE REAL ENGINE
// ---------------------------------------------------------------------------

// mtdBases are the two bases whose outer filter carries a mixed-type
// disjunction, each mapped to the folder its outer filter scopes to. Every view
// in them is one of the seven groups.
var mtdBases = map[string]string{
	"Content.base":     "01-Areas/Content",
	"Fundraising.base": "01-Areas/Fundraising",
}

// mtdMentions reports whether a stored filter tree compares `property` against
// a value beginning with `prefix` anywhere.
//
// THIS IS THE ASSERTION WITH POWER ON THE VIEWS ROWS CANNOT GRADE. Absorption
// removes a conjunct from the outer filter, and the outer filter is also where
// the FOLDER SCOPE lives. If the reduction ate the scope as well, the view
// would import ENABLED, return every note of its type, and — because the vault
// holds only two `content` notes and the oracle expects both — return exactly
// the right rows anyway. The row comparison is blind to that; the tree is not.
func mtdMentions(n *generated.VaultFilterNode, property, prefix string) bool {
	if n == nil {
		return false
	}
	if n.Property != nil && *n.Property == property && n.Value != nil && strings.HasPrefix(*n.Value, prefix) {
		return true
	}
	for _, group := range []*[]generated.VaultFilterNode{n.All, n.Any} {
		if group == nil {
			continue
		}
		for i := range *group {
			if mtdMentions(&(*group)[i], property, prefix) {
				return true
			}
		}
	}
	if n.Not != nil {
		return mtdMentions(n.Not, property, prefix)
	}
	return false
}

// TestMixedTypeDisjunction_TheSevenFlippedViewsNeverBroaden re-reads the WRITTEN
// schemas and views through records.LoadSchemas / LoadViews /
// NewViewFindLoader, serves each through the real comparator with the clock
// pinned to the oracle's own instant, and compares the row set to the
// hand-derived oracle.
//
// A view on a record type the vault holds NO notes of is reported UNFALSIFIABLE
// and not counted, because 0 == 0 proves nothing — three views were graded
// EXACT that way in this project already. So is a view whose oracle is empty
// and whose whole candidate population is empty.
func TestMixedTypeDisjunction_TheSevenFlippedViewsNeverBroaden(t *testing.T) {
	oraclePath := os.Getenv(fr105OracleEnv)
	if oraclePath == "" {
		t.Skipf("%s is unset — set it to the hand-derived expected-row-set JSON for the real vault", fr105OracleEnv)
	}
	root := fixtureVaultCopy(t)
	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	data, err := os.ReadFile(oraclePath) //nolint:gosec // operator-supplied acceptance oracle
	if err != nil {
		t.Fatalf("reading the oracle: %v", err)
	}
	var oracle fr105JSONOracle
	if err = json.Unmarshal(data, &oracle); err != nil {
		t.Fatalf("parsing the oracle: %v", err)
	}
	want := map[string][]string{}
	for _, b := range oracle.Bases {
		for _, v := range b.Views {
			want[b.Base+"|"+v.Name] = fr105Sorted(v.Rows)
		}
	}

	l := w3Load(t, root)

	seen, falsifiable := 0, 0
	for _, b := range rep.Bases {
		base := filepath.Base(b.BaseRelPath)
		if _, ok := mtdBases[base]; !ok {
			continue
		}
		for _, v := range b.Views {
			seen++
			if v.OutputRelPath == "" {
				t.Errorf("view %q of %s produced no view file at all", v.DisplayName, base)
				continue
			}
			slug := strings.TrimSuffix(filepath.Base(v.OutputRelPath), ".yaml")
			sv, ok := l.views.Get(slug)
			if !ok {
				t.Errorf("the import reports writing %q but no such view loaded", slug)
				continue
			}

			// THE FLIP ITSELF. Every one of the seven was stored DISABLED
			// before this change. A disabled view serves nothing, so if this
			// assertion ever regresses the row comparisons below would pass
			// vacuously rather than fail.
			if sv.Def.Disabled != nil && *sv.Def.Disabled {
				t.Errorf("view %q of %s is STILL DISABLED (%v) — the disjunction did not absorb",
					v.DisplayName, base, w3Strings(sv.Def.Untranslated))
				continue
			}
			for _, lost := range w3Strings(sv.Def.Untranslated) {
				if strings.Contains(lost, `type == "`) {
					t.Errorf("view %q of %s still reports a type-literal loss: %s", v.DisplayName, base, lost)
				}
			}

			// The filter must still CONSTRAIN, and specifically it must still
			// carry the base's FOLDER SCOPE. On three of these seven views the
			// row comparison below cannot see the difference — the whole
			// `content` population is two notes and the oracle expects both —
			// so this structural check is the only thing standing between an
			// absorbed disjunction and an absorbed scope.
			if sv.Def.Filter == nil {
				t.Errorf("view %q of %s imported with NO filter at all — absorption must remove the disjunction, not the scope",
					v.DisplayName, base)
				continue
			}
			if folder := mtdBases[base]; !mtdMentions(sv.Def.Filter, "file.folder", folder) {
				t.Errorf("view %q of %s lost its folder scope %q: absorption removed the disjunction AND the conjunct it sat beside",
					v.DisplayName, base, folder)
			}

			expected, known := want[base+"|"+v.DisplayName]
			if !known {
				t.Errorf("view %q of %s is newly enabled and the oracle does not cover it — an ungraded view is exactly where a broadening hides", v.DisplayName, base)
				continue
			}

			candidates := 0
			for _, n := range l.notes {
				if sv.Def.Type != nil && n.Rec.TypeName() == *sv.Def.Type {
					candidates++
				}
			}
			got := w3Rows(t, l, sv.Def, w3Clock())

			// FR-105 is graded on EVERY view, falsifiable or not: a broadening
			// is a broadening even when the instrument is weak.
			if extra := fr105MissingFrom(expected, got); len(extra) > 0 {
				t.Errorf("FR-105 BROADENING in %q (%s): the imported view returns %d row(s) the Obsidian original does not: %v",
					v.DisplayName, base, len(extra), extra)
			}
			if missing := fr105MissingFrom(got, expected); len(missing) > 0 {
				t.Logf("NARROWING (allowed by FR-105, recorded anyway) in %q (%s): %d row(s): %v",
					v.DisplayName, base, len(missing), missing)
			}

			// INSTRUMENT POWER, PER VIEW, MEASURED RATHER THAN ASSUMED.
			// Serving the same view with every filter clause stripped is the
			// maximally broadened translation. If that returns nothing the
			// oracle lacks, the grade above could not have caught a broadening
			// and must not be counted as evidence.
			broad := sv.Def
			broad.Filter = nil
			widened := w3Rows(t, l, broad, w3Clock())
			extraWhenWidened := fr105MissingFrom(expected, widened)
			if len(extraWhenWidened) == 0 {
				t.Logf("UNFALSIFIABLE %q (%s): oracle=%d imported=%d, but the vault holds %d note(s) of type %v and a fully stripped filter still returns nothing the oracle lacks — this grade proves NOTHING and is not counted",
					v.DisplayName, base, len(expected), len(got), candidates, *sv.Def.Type)
				continue
			}
			falsifiable++
			t.Logf("GRADED %q (%s): oracle=%d imported=%d over %d candidate %s note(s); INSTRUMENT PROVED — stripping every clause returns %d row(s), %d of them rows the oracle lacks",
				v.DisplayName, base, len(expected), len(got), candidates, *sv.Def.Type, len(widened), len(extraWhenWidened))
		}
	}

	if seen != 7 {
		t.Errorf("expected the 7 mixed-type-disjunction views across %v, saw %d — the fixture or the base files moved", mtdBases, seen)
	}
	if falsifiable == 0 {
		t.Fatal("not one of the flipped views could be graded falsifiably, so this test asserted nothing about broadening")
	}
	t.Logf("MIXED-TYPE DISJUNCTION: %d view(s) flipped from DISABLED to enabled, %d of them graded FALSIFIABLY against the independent oracle", seen, falsifiable)
}

// ---------------------------------------------------------------------------
// HALF THREE — THE VIEW-SIDE REDUCTION, WHICH THE FOUNDER'S VAULT NEVER REACHES
//
// translateOneView reduces BOTH trees: the base's outer filter and the view's
// own. In this vault only the outer one ever carries a deferred disjunction, so
// deleting the view-side call changes nothing measurable — a mutation confirmed
// it SURVIVES the whole suite otherwise.
//
// A line that cannot fail is a line nobody can trust, and the symmetry is not
// decoration: a view may perfectly well narrow a base with its own `or:`. So it
// gets a fixture of its own rather than an argument.
// ---------------------------------------------------------------------------

// mtdViewSideBase puts the mixed-type disjunction in the VIEW's filter, beside
// the `type ==` literal that resolves the view. Under `widget` the `gadget`
// branch is false and the `widget` branch reduces to its remainder, so the view
// must keep requiring `size == "large"`.
const mtdViewSideBase = `filters:
  and:
    - file.inFolder("things")
views:
  - type: table
    name: Narrowed By Its Own Or
    filters:
      and:
        - type == "widget"
        - or:
            - and:
                - type == "widget"
                - size == "large"
            - type == "gadget"
`

func TestMixedTypeDisjunction_TheViewsOwnFilterIsReducedToo(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "things")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("creating things/: %v", err)
	}
	// Two widgets, one large one small, plus a gadget the view must never
	// return whatever happens to the disjunction.
	notes := map[string]string{
		"big":   "---\ntype: widget\nsize: large\n---\n\n# big\n",
		"small": "---\ntype: widget\nsize: small\n---\n\n# small\n",
		"other": "---\ntype: gadget\nsize: large\n---\n\n# other\n",
	}
	for stem, body := range notes {
		if err := os.WriteFile(filepath.Join(dir, stem+".md"), []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", stem, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "bases"), 0o750); err != nil {
		t.Fatalf("creating bases/: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "bases", "Things.base"), []byte(mtdViewSideBase), 0o600); err != nil {
		t.Fatalf("writing the base: %v", err)
	}

	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("importing the view-side fixture: %v", err)
	}

	l := w3Load(t, root)
	def := w3View(t, l, rep, "Narrowed By Its Own Or") // fails loudly if stored DISABLED
	got := w3Rows(t, l, def, w3Clock())

	want := []string{"things/big.md"}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("the view's own disjunction must reduce to `size == \"large\"` under type=widget.\n"+
			"want %v\ngot  %v\n"+
			"Returning both widgets means the remainder was dropped (a BROADENING); returning none means the branch was.",
			want, got)
	}

	// INSTRUMENT POWER. The one-row answer is only evidence if a broken
	// translation would have produced a different one.
	broad := def
	broad.Filter = nil
	widened := w3Rows(t, l, broad, w3Clock())
	if len(widened) != 2 {
		t.Errorf("INSTRUMENT HAS NO POWER: with every clause stripped the view returns %v, so the %d-row grade above could not have detected a dropped remainder", widened, len(got))
	}
}
