// Omnipus — the deferred type-guarded disjunction: translateOr's case 3 and
// ReduceTypedDisjunctions.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// THE ORACLE IS THE ABSORPTION LAW, NOT THE IMPLEMENTATION.
//
// Every expectation below is written from `(X ∨ Y) ∧ X ≡ X` and from the four
// stated stopping cases, and each was written before the shape the code
// actually produces was inspected. The shapes are asserted through a renderer
// that prints the WHOLE tree, so a reduction that silently kept an extra
// disjunct cannot pass by matching on a prefix.
// ---------------------------------------------------------------------------

// renderRaw prints an intermediate tree completely and unambiguously.
func renderRaw(n *rawNode) string {
	if n == nil {
		return "-"
	}
	switch n.Kind {
	case rawKindLost:
		return "LOST"
	case rawKindLeaf:
		return fmt.Sprintf("leaf(%s|shape=%d|op=%s|val=%s)", n.Leaf.Property, n.Leaf.Shape, n.Leaf.Op, n.Leaf.Value)
	case rawKindPrebuilt:
		return "prebuilt(" + strings.Join(strings.Fields(n.Verbatim), " ") + ")"
	case rawKindAll:
		return "all(" + renderKids(n.Kids) + ")"
	case rawKindAny:
		return "any(" + renderKids(n.Kids) + ")"
	case rawKindNot:
		return "not(" + renderKids(n.Kids) + ")"
	case rawKindTypedAny:
		parts := make([]string, 0, len(n.Branches))
		for _, b := range n.Branches {
			parts = append(parts, b.RecordType+"=>"+renderRaw(b.Remainder))
		}
		return "typedAny(" + strings.Join(parts, ",") + ")"
	}
	return "?"
}

func renderKids(kids []*rawNode) string {
	parts := make([]string, 0, len(kids))
	for _, k := range kids {
		parts = append(parts, renderRaw(k))
	}
	return strings.Join(parts, ",")
}

// tree parses one Base `filters:` block from YAML, so the tests exercise the
// same entry point a real base file does rather than a hand-built tree.
func tree(t *testing.T, yamlSrc string) TreeTranslation {
	t.Helper()
	pb, err := ParseBaseFile([]byte(yamlSrc + "\nviews:\n  - name: V\n    type: table\n"))
	if err != nil {
		t.Fatalf("parsing the fixture base failed: %v", err)
	}
	return TranslateFilterTree(pb.Filters)
}

// contentOuter is Content.base's outer filter, verbatim.
const contentOuter = `filters:
  and:
    - file.inFolder("01-Areas/Content")
    - or:
        - type == "content"
        - type == "brand-kit"`

// fundraisingOuter is Fundraising.base's outer filter, verbatim. Its second
// branch carries a remainder, which is the case that distinguishes "the
// disjunction vanishes" from "the disjunction becomes its surviving branch".
const fundraisingOuter = `filters:
  and:
    - file.inFolder("01-Areas/Fundraising")
    - or:
        - type == "round"
        - and:
            - type == "company"
            - segment.contains("investor")`

func TestTypedDisjunction_IsDeferredNotLost(t *testing.T) {
	// Case 3 of translateOr: the group survives translation as a deferred
	// node carrying both branches, where version 1 produced LOST.
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{
			name: "Content — both branches are bare type literals",
			src:  contentOuter,
			want: `all(prebuilt(file.inFolder("01-Areas/Content")),typedAny(content=>-,brand-kit=>-))`,
		},
		{
			name: "Fundraising — the second branch has a remainder",
			src:  fundraisingOuter,
			want: `all(prebuilt(file.inFolder("01-Areas/Fundraising")),typedAny(round=>-,company=>leaf(segment|shape=1|op=|val=investor)))`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tree(t, tc.src)
			if r := renderRaw(got.Root); r != tc.want {
				t.Fatalf("tree shape:\n got  %s\n want %s", r, tc.want)
			}
			// "Either type" is not "this type": a deferred disjunction must
			// harvest NO type, or resolveViewType would pin the view to one of
			// two types the base never asserted unconditionally.
			if len(got.TypeLiterals) != 0 {
				t.Fatalf("a deferred disjunction harvested %v; it must harvest nothing", got.TypeLiterals)
			}
		})
	}
}

func TestTypedDisjunction_AbsorbsUnderEachViewType(t *testing.T) {
	// THE ABSORPTION LAW. Every one of the seven real groups reduces here.
	//
	// Content: both branches are bare type literals, so under either type the
	// matching branch is TRUE and the whole disjunction contributes nothing —
	// `inFolder AND (content OR brand-kit) AND content == inFolder AND content`,
	// and the `content` half is the VIEW's own conjunct, not this tree's.
	//
	// Fundraising: under `round` branch 1 is TRUE and the group vanishes;
	// under `company` branch 1 is impossible and the group becomes branch 2's
	// remainder alone.
	for _, tc := range []struct {
		name, src, recordType, want string
	}{
		{"Content under content", contentOuter, "content", `prebuilt(file.inFolder("01-Areas/Content"))`},
		{"Content under brand-kit", contentOuter, "brand-kit", `prebuilt(file.inFolder("01-Areas/Content"))`},
		{"Fundraising under round", fundraisingOuter, "round", `prebuilt(file.inFolder("01-Areas/Fundraising"))`},
		{"Fundraising under company", fundraisingOuter, "company",
			`all(prebuilt(file.inFolder("01-Areas/Fundraising")),leaf(segment|shape=1|op=|val=investor))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ReduceTypedDisjunctions(tree(t, tc.src), tc.recordType)
			if r := renderRaw(got.Root); r != tc.want {
				t.Fatalf("reduced under %q:\n got  %s\n want %s", tc.recordType, r, tc.want)
			}
			if strings.Contains(renderRaw(got.Root), "LOST") {
				t.Fatalf("reduced under %q still holds a loss: %s", tc.recordType, renderRaw(got.Root))
			}
		})
	}
}

func TestTypedDisjunction_TheFourStoppingCasesStayLost(t *testing.T) {
	// Each of these must degrade to LOST — the view stays disabled and the
	// report keeps classifying the group exactly as it did before.
	notWrapped := `filters:
  not:
    - or:
        - type == "content"
        - type == "brand-kit"`
	// NOT `contentOuter` NESTED. Both of contentOuter's branches are bare type
	// literals, so its reduction is the TRUE case, which the nil-check in
	// reduceTypedNode's any/not arm catches whether or not AND-position is
	// tracked at all. This shape reduces to a REAL node instead, so it
	// separates the two guards: without the AND-position rule it would be
	// carried, and `any(draft, published)` is not what an unreduced group
	// means.
	nestedInAnOr := `filters:
  or:
    - or:
        - and:
            - type == "content"
            - status == "draft"
        - and:
            - type == "brand-kit"
            - status == "final"
    - status == "published"`

	for _, tc := range []struct {
		name, src, recordType, why string
		// wantExact is the whole rendered tree when the refusal is expected to
		// replace it outright, rather than merely appear somewhere inside it.
		wantExact string
	}{
		{
			name: "an untyped view cannot reduce", src: contentOuter, recordType: "",
			why: "with no type asserted the disjunction really does span two domains",
		},
		{
			name: "no branch survives the view's type", src: contentOuter, recordType: "task",
			why: "the effective filter is FALSE; emitting an unmatchable view is a different decision",
		},
		{
			// EXACT, not "contains": translateNot must refuse the WHOLE `not:`
			// block, so the tree is one lost node. A tree of `not(LOST)` would
			// also contain "LOST" while meaning the group was carried into a
			// negation and only refused underneath it.
			name: "under a negation", src: notWrapped, recordType: "content",
			why:       "refused by translateNot rather than carried somewhere unproved",
			wantExact: "LOST",
		},
		{
			name: "outside AND-position", src: nestedInAnOr, recordType: "content",
			why:       "a reduction is only conditioned on `type == T` where it is conjoined with it",
			wantExact: "any(LOST,leaf(status|shape=0|op==|val=published))",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ReduceTypedDisjunctions(tree(t, tc.src), tc.recordType)
			r := renderRaw(got.Root)
			if tc.wantExact != "" {
				if r != tc.wantExact {
					t.Fatalf("tree:\n got  %s\n want %s\n(%s)", r, tc.wantExact, tc.why)
				}
			} else if !strings.Contains(r, "LOST") {
				t.Fatalf("expected a loss (%s), got %s", tc.why, r)
			}
			if strings.Contains(r, "typedAny") {
				t.Fatalf("a deferred node escaped the reduction into the resolution pass: %s", r)
			}
		})
	}
}

// TestTypedDisjunction_ABareTypeLiteralBranchIsTRUENotEmpty pins the encoding
// that the rest of the reduction rests on.
//
// A branch that is nothing but `type == T` is TRUE under T, so the WHOLE
// disjunction is true and contributes no constraint — the reduction must return
// "nothing", not a disjunction with an empty member. The two are easy to
// confuse because `anyNode` collapses a one-element list, which makes the wrong
// encoding look right whenever exactly one branch survives. Here TWO survive,
// so they cannot be confused.
func TestTypedDisjunction_ABareTypeLiteralBranchIsTRUENotEmpty(t *testing.T) {
	tautology := `filters:
  or:
    - type == "decision"
    - and:
        - type == "decision"
        - status == "accepted"`
	got := ReduceTypedDisjunctions(tree(t, tautology), "decision")
	if r := renderRaw(got.Root); r != "-" {
		t.Fatalf("a disjunction with a TRUE branch must contribute nothing, got %s", r)
	}
}

func TestTypedDisjunction_LostVerbatimIsUnchangedFromTheOldRefusal(t *testing.T) {
	// The report classifies this gap by the expression's SHAPE
	// (report.go's matchExpr: a combinator with >1 distinct type literal), so
	// an irreducible group has to carry the same text it always did or a loss
	// that is still real would drop out of its category.
	got := ReduceTypedDisjunctions(tree(t, contentOuter), "")
	var lost *rawNode
	var walk func(*rawNode)
	walk = func(n *rawNode) {
		if n == nil {
			return
		}
		if n.Kind == rawKindLost {
			lost = n
		}
		for _, k := range n.Kids {
			walk(k)
		}
	}
	walk(got.Root)
	if lost == nil {
		t.Fatal("no lost node to inspect")
	}
	if !strings.Contains(lost.Verbatim, `type == "content"`) || !strings.Contains(lost.Verbatim, `type == "brand-kit"`) {
		t.Fatalf("the loss no longer quotes both branches, so the report cannot classify it:\n%s", lost.Verbatim)
	}
	if len(distinctTypeLiterals(lost.Verbatim)) != 2 {
		t.Fatalf("report.go's matchExpr counts %d distinct type literals, needs 2:\n%s",
			len(distinctTypeLiterals(lost.Verbatim)), lost.Verbatim)
	}
	if !isCombinatorExpr(lost.Verbatim) {
		t.Fatalf("report.go's matchExpr no longer sees a combinator:\n%s", lost.Verbatim)
	}
}

func TestTypedDisjunction_ReducingDoesNotMutateTheSharedOuterTree(t *testing.T) {
	// A base's outer filter is translated ONCE and handed to every view in the
	// base. Content.base has five views resolving to TWO types; reducing in
	// place under the first would apply that type's reduction to the rest.
	shared := tree(t, contentOuter)
	before := renderRaw(shared.Root)

	underContent := renderRaw(ReduceTypedDisjunctions(shared, "content").Root)
	after := renderRaw(shared.Root)
	if after != before {
		t.Fatalf("reducing mutated the shared tree:\n before %s\n after  %s", before, after)
	}

	underBrandKit := renderRaw(ReduceTypedDisjunctions(shared, "brand-kit").Root)
	if renderRaw(shared.Root) != before {
		t.Fatalf("the second reduction mutated the shared tree: %s", renderRaw(shared.Root))
	}
	// Both reductions must be reachable from the SAME untouched input.
	if underContent == "" || underBrandKit == "" {
		t.Fatal("empty reduction")
	}
	if strings.Contains(underContent, "typedAny") || strings.Contains(underBrandKit, "typedAny") {
		t.Fatalf("deferred node survived: %s / %s", underContent, underBrandKit)
	}
}

func TestTypedDisjunction_SameTypeDistributivityIsUnchanged(t *testing.T) {
	// REGRESSION GUARD on translateOr's case 2, which is Subscriptions.base and
	// was already carried before this change. Case 3 must not have swallowed
	// it: the type is still HARVESTED (so the view is typed) and the remainders
	// still become the disjunction.
	subscriptions := `filters:
  or:
    - and:
        - file.inFolder("01-Areas/Ops")
        - type == "subscription"
    - and:
        - file.inFolder("02-Projects")
        - type == "subscription"`
	got := tree(t, subscriptions)
	if want := []string{"subscription"}; !slices.Equal(sorted(got.TypeLiterals), want) {
		t.Fatalf("the distributive case stopped harvesting its type: got %v want %v", got.TypeLiterals, want)
	}
	want := `any(prebuilt(file.inFolder("01-Areas/Ops")),prebuilt(file.inFolder("02-Projects")))`
	if r := renderRaw(got.Root); r != want {
		t.Fatalf("distributive shape changed:\n got  %s\n want %s", r, want)
	}
	if r := renderRaw(ReduceTypedDisjunctions(got, "subscription").Root); r != want {
		t.Fatalf("the reduction disturbed an already-factored disjunction:\n got  %s\n want %s", r, want)
	}
}

func TestTypedDisjunction_AMixOfTypedAndUntypedBranchesIsStillRefused(t *testing.T) {
	// Deliberately NOT reduced (see translateOr case 3's comment): under
	// `type == T` an untyped branch survives whatever T is, so the reduction
	// would still be exact — but no base in this vault has the shape, so there
	// is nothing to grade it against.
	mixed := `filters:
  or:
    - type == "content"
    - status == "draft"`
	got := ReduceTypedDisjunctions(tree(t, mixed), "content")
	if r := renderRaw(got.Root); r != "LOST" {
		t.Fatalf("a typed/untyped mix must stay refused, got %s", r)
	}
}

func sorted(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}
