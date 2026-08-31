// Omnipus — FR-146: the per-formula DEPTH cap, and the reasoning behind its
// number. License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// These tests pin the ARGUMENT for maxFormulaDepth's value, not just the value.
//
// A test asserting `maxFormulaDepth == 16` would pass for a number someone
// guessed. The constant's doc comment makes three factual claims about why 8
// was wrong and 16 is right, and every one of them is measurable:
//
//   1. an N-arm `if` chain is 5N+1 nodes and N+2 deep;
//   2. the 64-node cap therefore admits a 12-arm chain (depth 14) and refuses
//      13 arms on NODES — so any depth cap below 14 refuses a shape the cost
//      budget already paid for;
//   3. the cap still refuses cheap-but-unreadable nesting.
//
// If someone later retunes the node cap, these fail and the doc comment gets
// re-derived instead of silently becoming false.
// ---------------------------------------------------------------------------

// ifChain builds the N-way lookup idiom the grammar forces on an author who
// has no `switch`: `if(c0, v0, if(c1, v1, ... , fallback))`.
func ifChain(arms int) string {
	var b strings.Builder
	for i := 0; i < arms; i++ {
		fmt.Fprintf(&b, `if(team == "t%d", "T%d", `, i, i)
	}
	b.WriteString("team")
	b.WriteString(strings.Repeat(")", arms))
	return b.String()
}

// TestIfChainCostGrowth pins claim 1: the shape of the idiom the depth cap
// meets in practice. Both numbers are read off the real parser.
func TestIfChainCostGrowth(t *testing.T) {
	for arms := 1; arms <= 12; arms++ {
		root, err := ParseFormula(ifChain(arms))
		if err != nil {
			t.Fatalf("arms=%d: parse: %v", arms, err)
		}
		wantNodes, wantDepth := 5*arms+1, arms+2
		if got := FormulaNodeCount(root); got != wantNodes {
			t.Errorf("arms=%d: nodes=%d, want %d — an if-arm is `if`+`==`+ident+literal+result; if that changed, maxFormulaDepth's derivation changed with it",
				arms, got, wantNodes)
		}
		if got := FormulaDepth(root); got != wantDepth {
			t.Errorf("arms=%d: depth=%d, want %d (arms + the `==` and the ident under the innermost condition)",
				arms, got, wantDepth)
		}
	}
}

// TestNodeCapIsTheBindingBoundOnAnIfChain pins claim 2, which is the whole
// case against 8: the NODE cap is what should stop a runaway lookup chain, and
// it does so at 12 arms. A depth cap under 14 is a second, contradicting bound.
func TestNodeCapIsTheBindingBoundOnAnIfChain(t *testing.T) {
	deepestWithinNodeCap := 0
	for arms := 1; arms <= 40; arms++ {
		root, err := ParseFormula(ifChain(arms))
		if err != nil {
			break // maxParseDepth, far above anything relevant here
		}
		if FormulaNodeCount(root) <= maxFormulaNodes {
			deepestWithinNodeCap = FormulaDepth(root)
		}
	}
	if deepestWithinNodeCap != 14 {
		t.Fatalf("deepest if-chain inside the %d-node cap is depth %d, want 14 — maxFormulaDepth's doc comment derives 16 from this number",
			maxFormulaNodes, deepestWithinNodeCap)
	}
	if maxFormulaDepth < deepestWithinNodeCap {
		t.Errorf("maxFormulaDepth=%d refuses an if-chain the %d-node cap already permits (depth %d). "+
			"That is the exact defect the old value of 8 had: two caps disagreeing about the same tree",
			maxFormulaDepth, maxFormulaNodes, deepestWithinNodeCap)
	}
}

// TestFounderTeamNameFormulaIsAccepted is the acceptance case: the real
// `team_name` formula from the founder's Tasks.base. It cost 51 of its
// permitted 64 nodes and was refused for depth alone.
func TestFounderTeamNameFormulaIsAccepted(t *testing.T) {
	const src = `if(team == "t0", "T0 · Chief-of-Staff", if(team == "t1", "T1 · R&D / Engineering", ` +
		`if(team == "t2", "T2 · Marketing / GTM", if(team == "t3", "T3 · Video & Creative Studio", ` +
		`if(team == "t4", "T4 · Finance & Accounting", if(team == "t5", "T5 · Legal & Compliance", ` +
		`if(team == "t6", "T6 · Sales / CRM", if(team == "t7", "T7 · Fundraising / IR", ` +
		`if(team == "t8", "T8 · People / Talent", if(team == "t9", "T9 · Knowledge / KB-Standards", ` +
		`team))))))))))`

	root, err := ParseFormula(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	nodes, depth := FormulaNodeCount(root), FormulaDepth(root)
	if nodes != 51 || depth != 12 {
		t.Fatalf("team_name measures nodes=%d depth=%d, want 51/12 — the fixture formula changed", nodes, depth)
	}
	if nodes > maxFormulaNodes {
		t.Fatalf("team_name is over the node cap; it was only ever refused on depth")
	}

	schema := &Schema{Type: "task", Properties: map[string]*Property{
		"team": {Name: "team", Type: TypeText},
	}, PropertyOrder: []string{"team"}}
	set, errs := ValidateFormulaSet(map[string]string{"team_name": src}, schema)
	if len(errs) > 0 {
		t.Fatalf("team_name refused: %v — it is 51/%d nodes and %d/%d deep, so nothing should reject it",
			errs[0].Reason, maxFormulaNodes, depth, maxFormulaDepth)
	}
	if _, ok := set.Get("team_name"); !ok {
		t.Fatal("team_name validated but is not in the set")
	}
}

// TestDepthCapStillRefusesCheapUnreadableNesting pins claim 3. The cap has to
// keep meaning something, and the case it exists for is the tree that is
// trivially cheap and still unreadable — raising the number must not have
// quietly turned it into a no-op.
func TestDepthCapStillRefusesCheapUnreadableNesting(t *testing.T) {
	// 30 stacked `!` — 31 nodes (well inside the 64-node cap) and 31 deep.
	src := strings.Repeat("!", 30) + "done"
	root, err := ParseFormula(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n := FormulaNodeCount(root); n > maxFormulaNodes {
		t.Fatalf("the unreadable-nesting case costs %d nodes, over the %d-node cap — "+
			"then the NODE cap would be refusing it and this test would prove nothing about depth", n, maxFormulaNodes)
	}
	if d := FormulaDepth(root); d <= maxFormulaDepth {
		t.Fatalf("depth %d is within the cap of %d; this case no longer exercises the depth cap", d, maxFormulaDepth)
	}

	schema := &Schema{Type: "task", Properties: map[string]*Property{
		"done": {Name: "done", Type: TypeCheckbox},
	}, PropertyOrder: []string{"done"}}
	_, errs := ValidateFormulaSet(map[string]string{"f": src}, schema)
	if len(errs) == 0 {
		t.Fatal("cheap but 31-deep nesting was accepted — maxFormulaDepth has become a no-op, " +
			"which is not what raising it to 16 was meant to do")
	}
	var found bool
	for _, e := range errs {
		if e.Code == FormulaErrTooLarge && strings.Contains(e.Reason, "levels deep") {
			found = true
		}
	}
	if !found {
		t.Errorf("refused, but not by the depth cap: %v", errs[0].Reason)
	}
}

// TestParseDepthGuardIsIndependentOfTheTreeCap pins the load-bearing claim in
// maxFormulaDepth's doc comment: stack safety is maxParseDepth's job, checked
// DURING descent, and it is unaffected by this cap's value. Without this,
// "raising the depth cap is stack-safe" rests on reading rather than evidence.
func TestParseDepthGuardIsIndependentOfTheTreeCap(t *testing.T) {
	if maxParseDepth <= maxFormulaDepth {
		t.Fatalf("maxParseDepth=%d must stay well above maxFormulaDepth=%d, or the tree cap stops being what an author meets",
			maxParseDepth, maxFormulaDepth)
	}
	// An expression that recurses once per byte. The parser must refuse it
	// ITSELF rather than building the tree and leaving it to the tree cap.
	//
	// 400 parens, not 5,000: maxFormulaSourceBytes (=4096, formula_lex.go)
	// would refuse a longer source on LENGTH before the descent ever started,
	// and this test would then prove nothing about the recursion guard.
	const nest = 400
	src := strings.Repeat("(", nest) + "1" + strings.Repeat(")", nest)
	if len(src) > maxFormulaSourceBytes {
		t.Fatalf("the probe is %d bytes, over the %d-byte source cap — it would be refused on length, not depth",
			len(src), maxFormulaSourceBytes)
	}
	root, err := ParseFormula(src)
	if err == nil {
		t.Fatalf("a 5000-deep parenthesis nest parsed to a tree of depth %d — "+
			"maxParseDepth did not fire, so the parser's stack is guarded by nothing",
			FormulaDepth(root))
	}
	if !strings.Contains(err.Reason, "nests too deeply to parse") {
		t.Errorf("refused with %q, want the parser's own depth guard — a refusal from anywhere else "+
			"means the recursion is still unbounded", err.Reason)
	}
}
