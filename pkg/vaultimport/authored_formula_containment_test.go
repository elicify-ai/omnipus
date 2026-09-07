// Omnipus — the importer may AUTHOR a formula for a filter clause, and every
// condition that permission was granted under is asserted here.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY EVERY ONE OF THESE IS A TEST AND NOT A COMMENT
//
// An importer that writes a definition into a file its operator owns is a new
// surface, and it was ruled against once for exactly that reason. The ruling
// was reversed under CONTAINMENT — a list of conditions — and a condition that
// exists only in a doc comment is a condition that stops holding the first time
// somebody generalises the code around it. So each condition below has a case
// that FAILS if the condition is dropped:
//
//	the expression types as a truth value    a number-valued clause is refused
//	                                         a list-valued clause is refused
//	the name cannot collide                  a base that has taken the name gets
//	                                         a different one, never a shadow
//	it is reported                           the outcome names it (the header is
//	                                         asserted in the clock test, on a
//	                                         real produced file)
//	it fits FR-146's per-view budget         over budget, NOTHING is authored —
//	                                         refused, never truncated
//	it is a positive conjunct                inside `or:`/`not:` the group is
//	                                         lost whole, as before
//	two namespaces are excluded              `file.` stays FR-134's; `formula.`
//	                                         keeps its own diagnosis
//
// AND THE LAST ONE, WHICH IS THE EASIEST TO LOSE: an authored formula must
// never be reachable under a negation. That is enforced structurally (an
// undecided expression counts as lost, so `or:`/`not:` lose their group whole),
// which is a stronger guarantee than a polarity flag and is why it is asserted
// at the tree level rather than at the leaf.
// ---------------------------------------------------------------------------

// authoredTestSchema is a record type with the property types each case below
// needs — a date to build a truth value from, a number to build a number from,
// and a many-valued property to build a list from.
func authoredTestSchema() *SchemaIndex {
	return NewSchemaIndex(map[string][]InferredProperty{
		"deal": {
			{Name: "close_date", Type: records.TypeDate},
			{Name: "value", Type: records.TypeInteger},
			{Name: "stage", Type: records.TypeEnum, EnumValues: []string{"open", "won"}},
			{Name: "tags", Type: records.TypeText, Many: true},
		},
	})
}

// authoredTranslate runs one `.base` file source through the real translator
// and returns the single view's outcome and the file it produced.
func authoredTranslate(t *testing.T, src string) (ViewOutcome, *ProducedView) {
	t.Helper()
	pb, err := ParseBaseFile([]byte(src))
	if err != nil {
		t.Fatalf("parsing the fixture base: %v", err)
	}
	outcome, produced := TranslateBase(pb, "Deals.base", authoredTestSchema(), NewSlugRegistry())
	if len(outcome.Views) != 1 {
		t.Fatalf("the fixture base is meant to hold exactly one view; it produced %d", len(outcome.Views))
	}
	var pv *ProducedView
	if len(produced) == 1 {
		pv = &produced[0]
	}
	return outcome.Views[0], pv
}

// authoredBase wraps one filter body in a minimal single-view base.
func authoredBase(filters string) string {
	return "filters:\n  and:\n    - type == \"deal\"\nviews:\n  - type: table\n    name: One\n    filters:\n" + filters
}

// TestAuthored_OnlyATruthValueIsCarried is condition 1.
//
// The leaf that carries an authored formula is `= true` and nothing else. A
// formula of any other type has no such leaf, and inventing one — `> 0` for a
// number, `!= ""` for text — would be this importer choosing a threshold the
// operator never wrote.
func TestAuthored_OnlyATruthValueIsCarried(t *testing.T) {
	for _, tc := range []struct {
		name, clause, wantType string
	}{
		{"a number-valued expression", `date(close_date).year + 1`, "number"},
		{"a date-valued expression", `date(close_date)`, "date"},
		{"a presentation-valued expression", `format(today(), "YYYY")`, "presentation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The oracle for what the clause IS comes from the product's own
			// validator, not from this test's opinion of it.
			set, errs := records.ValidateFormulaSet(map[string]string{"probe": tc.clause}, authoredRealSchema(t))
			if len(errs) > 0 {
				t.Skipf("the grammar refuses %q outright (%v), so this case is about the wrong branch — pick another clause", tc.clause, errs[0])
			}
			if decl, _ := set.Get("probe"); string(decl.Type) == "boolean" {
				t.Fatalf("%q types as a truth value, so it does not exercise the refusal this case is about", tc.clause)
			}

			vo, _ := authoredTranslate(t, authoredBase("      and:\n        - "+tc.clause+"\n"))
			if len(vo.AuthoredFormulas) != 0 {
				t.Errorf("a %s-valued clause was carried as an authored formula: %v", tc.wantType, vo.AuthoredFormulas)
			}
			if !vo.Disabled {
				t.Errorf("the clause was dropped and the view is still ENABLED, which is the broadening FR-105 forbids. losses: %v", vo.Losses)
			}
			if !authoredLossMentions(vo.Losses, "not as a truth value") {
				t.Errorf("the loss does not say WHY the clause was not carried:\n  %v", vo.Losses)
			}
		})
	}
}

// TestAuthored_AListValuedExpressionIsRefused is the other half of condition 1.
// A list has no single truth value for a leaf to compare, and `=` over a list
// is element-wise (R-9) — a different question from the one the operator asked.
func TestAuthored_AListValuedExpressionIsRefused(t *testing.T) {
	// A list of TRUTH VALUES, deliberately. A list of numbers would be caught
	// by the boolean gate above and this case would then pass without ever
	// reaching the arity gate — which is exactly what a mutation run found.
	const clause = `list(date(close_date).year == today().year, date(close_date).month == today().month)`
	set, errs := records.ValidateFormulaSet(map[string]string{"probe": clause}, authoredRealSchema(t))
	if len(errs) > 0 {
		t.Skipf("the grammar refuses %q outright (%v); this case needs a clause it READS", clause, errs[0])
	}
	decl, _ := set.Get("probe")
	if decl.Arity != records.ArityMany {
		t.Fatalf("%q has arity %v, so it does not exercise the list refusal", clause, decl.Arity)
	}
	if decl.Type != records.FormulaBoolean {
		t.Fatalf("%q types as %s; this case needs a BOOLEAN list, or the type gate refuses it before the arity gate is reached and the arity gate goes untested",
			clause, decl.Type)
	}

	vo, _ := authoredTranslate(t, authoredBase("      and:\n        - "+clause+"\n"))
	if len(vo.AuthoredFormulas) != 0 {
		t.Errorf("a LIST-valued clause was carried as an authored formula: %v", vo.AuthoredFormulas)
	}
	if !vo.Disabled {
		t.Errorf("the clause was dropped and the view is still ENABLED. losses: %v", vo.Losses)
	}
	if !authoredLossMentions(vo.Losses, "produces a LIST") {
		t.Errorf("the loss does not name the arity as the reason:\n  %v", vo.Losses)
	}
}

// TestAuthored_NeverUnderADisjunctionOrANegation is condition 5, and it is the
// one whose loss would be silent.
//
// Every approximate translation in this importer is safe at the top level and
// unsafe under a `not:`, because knowledge_find negates a combinator as a bare
// `!inner.matched` with no absence rule of its own. So the authored path is
// confined to a positive conjunct STRUCTURALLY: an undecided expression counts
// as lost, and an `or:`/`not:` holding one is lost whole exactly as it was
// before this path existed.
func TestAuthored_NeverUnderADisjunctionOrANegation(t *testing.T) {
	for _, tc := range []struct{ name, filters string }{
		{
			name: "inside an or:",
			filters: "      and:\n        - or:\n            - date(close_date).year == today().year\n" +
				"            - stage == \"won\"\n",
		},
		{
			name:    "inside a not:",
			filters: "      and:\n        - not:\n            - date(close_date).year == today().year\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vo, _ := authoredTranslate(t, authoredBase(tc.filters))
			if len(vo.AuthoredFormulas) != 0 {
				t.Errorf("an expression %s produced an authored formula: %v.\n"+
					"Under a negation `formula.x = true` stops being the clause the operator wrote, and this importer has no proof for that case.",
					tc.name, vo.AuthoredFormulas)
			}
			if !vo.Disabled {
				t.Errorf("the group was dropped and the view is still ENABLED. losses: %v", vo.Losses)
			}
		})
	}
}

// TestAuthored_TheTwoExcludedNamespacesKeepTheirOwnAnswers is the boundary
// leaf.go draws, asserted from the outside.
//
// `file.` is FR-134's: records.TranslateFileMethod already translates
// `inFolder`/`hasTag`/`hasLink` into ordinary leaves, and a second translation
// of the same file metadata is two answers to one question. `formula.` already
// IS a computed property, and buildFormulaLeafNode can say something specific
// about why one could not be built; burying that under a name the operator
// never saw would name the wrong gap.
func TestAuthored_TheTwoExcludedNamespacesKeepTheirOwnAnswers(t *testing.T) {
	for _, tc := range []struct{ name, clause, wantReason string }{
		{"a file.* expression", `file.tags.contains("urgent")`, "`file.` namespace"},
		{"a formula.* expression", `formula.is_stale`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if expressionFilterCandidate(tc.clause) {
				t.Fatalf("%q was admitted to the authored-formula candidate pool; both excluded namespaces must be refused before any of it runs", tc.clause)
			}
			vo, _ := authoredTranslate(t, authoredBase("      and:\n        - "+tc.clause+"\n"))
			if len(vo.AuthoredFormulas) != 0 {
				t.Errorf("%q produced an authored formula: %v", tc.clause, vo.AuthoredFormulas)
			}
			if tc.wantReason != "" && !authoredLossMentions(vo.Losses, tc.wantReason) {
				t.Errorf("the loss for %q does not name why the namespace is excluded:\n  %v", tc.clause, vo.Losses)
			}
		})
	}
}

// TestAuthored_TheNameNeverShadowsOneTheOperatorWrote is condition 2.
//
// The namespace is a convention; the CHECK is what makes it a guarantee. A base
// that has already taken `imported__filter_1` — whether the importer could
// carry that formula or not — must not have it silently redefined.
func TestAuthored_TheNameNeverShadowsOneTheOperatorWrote(t *testing.T) {
	src := "filters:\n  and:\n    - type == \"deal\"\n" +
		"formulas:\n  " + authoredFormulaPrefix + "1: value * 2\n" +
		"views:\n  - type: table\n    name: One\n    filters:\n" +
		"      and:\n        - date(close_date).year == today().year\n"

	vo, pv := authoredTranslate(t, src)
	if len(vo.AuthoredFormulas) != 1 {
		t.Fatalf("expected one authored formula, got %v (losses: %v)", vo.AuthoredFormulas, vo.Losses)
	}
	taken := authoredFormulaPrefix + "1"
	if strings.Contains(vo.AuthoredFormulas[0], fmt.Sprintf("%q", taken)) {
		t.Errorf("the importer authored %q, which the base already declares — the operator's own formula would be silently redefined:\n  %s", taken, vo.AuthoredFormulas[0])
	}
	if pv == nil {
		t.Fatal("no view file was produced")
	}
	// AND THE FILE MUST NOT REDEFINE IT EITHER. A view declares only the
	// formulas it uses (FR-146's budget), so the operator's own
	// `imported__filter_1` is legitimately absent here — this view never names
	// it. What must never appear is that name bound to the importer's
	// expression, which is the shadowing this check is about.
	if strings.Contains(string(pv.Bytes), taken+": date(") {
		t.Errorf("the produced view binds `%s` — a name the base already declares — to an expression the importer wrote:\n%s", taken, pv.Bytes)
	}
	// The name it DID choose has to be the next free one, deterministically, so
	// re-importing the same vault produces the same file.
	if !strings.Contains(string(pv.Bytes), authoredFormulaPrefix+"2: date(close_date).year == today().year") {
		t.Errorf("the importer did not fall through to the next free name in the reserved namespace:\n%s", pv.Bytes)
	}
}

// TestAuthored_OverBudgetIsRefusedNotTruncated is condition 4, and the word
// that matters is "not truncated".
//
// FR-146 caps a view at 16 formulas / 256 nodes because each is evaluated once
// per candidate. A view already at the cap plus an expression clause is over
// it. Writing the clauses that fit and dropping the rest would leave a filter
// MISSING A CONJUNCT — strictly more rows than the Obsidian original, which is
// the one direction FR-105 forbids — so nothing is authored at all and the
// clause becomes a named loss that disables the view.
func TestAuthored_OverBudgetIsRefusedNotTruncated(t *testing.T) {
	var b strings.Builder
	b.WriteString("filters:\n  and:\n    - type == \"deal\"\nformulas:\n")
	// Sixteen formulas, which is the cap exactly. Each is referenced by the
	// view (as a displayed column), so all sixteen count against ITS budget.
	const filler = 16
	for i := 1; i <= filler; i++ {
		fmt.Fprintf(&b, "  f%d: value + %d\n", i, i)
	}
	b.WriteString("views:\n  - type: table\n    name: One\n    filters:\n")
	b.WriteString("      and:\n        - date(close_date).year == today().year\n")
	b.WriteString("    order:\n")
	for i := 1; i <= filler; i++ {
		fmt.Fprintf(&b, "      - formula.f%d\n", i)
	}

	vo, pv := authoredTranslate(t, b.String())

	if len(vo.AuthoredFormulas) != 0 {
		t.Errorf("the importer authored a formula although the view is already at FR-146's 16-formula cap: %v", vo.AuthoredFormulas)
	}
	if !vo.Disabled {
		t.Errorf("the clause could not be carried and the view is still ENABLED — a filter short one conjunct returns MORE rows. losses: %v", vo.Losses)
	}
	if !authoredLossMentions(vo.Losses, "FR-146") {
		t.Errorf("the loss does not name the budget that refused it:\n  %v", vo.Losses)
	}
	if !authoredLossMentions(vo.Losses, "never a truncation") {
		t.Errorf("the loss does not say the budget refuses rather than truncates, which is the property that makes it safe:\n  %v", vo.Losses)
	}
	// And nothing in the reserved namespace reached the file.
	if pv != nil && strings.Contains(string(pv.Bytes), authoredFormulaPrefix) {
		t.Errorf("the produced view carries a name from the reserved namespace although the budget refused it:\n%s", pv.Bytes)
	}
}

// TestAuthored_JustInsideTheBudgetIsStillCarried is the other side of the same
// boundary, and without it the test above passes for a code path that refuses
// everything.
func TestAuthored_JustInsideTheBudgetIsStillCarried(t *testing.T) {
	var b strings.Builder
	b.WriteString("filters:\n  and:\n    - type == \"deal\"\nformulas:\n")
	const filler = 15 // fifteen plus one authored is exactly the cap
	for i := 1; i <= filler; i++ {
		fmt.Fprintf(&b, "  f%d: value + %d\n", i, i)
	}
	b.WriteString("views:\n  - type: table\n    name: One\n    filters:\n")
	b.WriteString("      and:\n        - date(close_date).year == today().year\n")
	b.WriteString("    order:\n")
	for i := 1; i <= filler; i++ {
		fmt.Fprintf(&b, "      - formula.f%d\n", i)
	}

	vo, _ := authoredTranslate(t, b.String())
	if len(vo.AuthoredFormulas) != 1 {
		t.Errorf("fifteen declared formulas plus one authored is exactly FR-146's cap of 16, so the clause must be carried; got %v (losses: %v)",
			vo.AuthoredFormulas, vo.Losses)
	}
	if vo.Disabled {
		t.Errorf("the view is disabled although every clause was carried: %v", vo.Losses)
	}
}

// TestAuthored_AnUntypedViewAuthorsNothing is the honest answer for a view with
// no record type: a formula naming a property has nothing to type that property
// against, so every property operand is refused and an authored formula would
// be refused with it. Saying that is more useful than quoting a parser about a
// property it could not look up.
func TestAuthored_AnUntypedViewAuthorsNothing(t *testing.T) {
	src := "views:\n  - type: table\n    name: One\n    filters:\n" +
		"      and:\n        - date(close_date).year == today().year\n"
	vo, _ := authoredTranslate(t, src)
	if len(vo.AuthoredFormulas) != 0 {
		t.Errorf("an UNTYPED view authored a formula over a property nothing declares: %v", vo.AuthoredFormulas)
	}
	if !vo.Disabled {
		t.Errorf("the clause was dropped and the untyped view is still ENABLED. losses: %v", vo.Losses)
	}
	if !authoredLossMentions(vo.Losses, "declares no record type") {
		t.Errorf("the loss does not tell the operator the remedy (give the view a record type):\n  %v", vo.Losses)
	}
}

// TestAuthored_EveryRefusalIsStillClassifiedByTheReport is the coupling that is
// invisible from inside this file.
//
// report.go's closed gap table classifies a loss by matching substrings of the
// reason the importer wrote. A new sentence that matches no token lands in
// UNCLASSIFIED, and the founder reads a bucket growing for no reason. Every
// refusal this change introduces opens with the same shared literal the old
// expression refusals did, so they stay in the same bucket — asserted here
// rather than hoped for.
func TestAuthored_EveryRefusalIsStillClassifiedByTheReport(t *testing.T) {
	for _, why := range []string{
		"it types as number, not as a truth value",
		"carrying it would put this view over FR-146's per-view formula budget",
		"this view declares no record type",
		fileNamespaceExpressionRefusal,
	} {
		reason := expressionNotCarriedAsFormula(why)
		if !strings.HasPrefix(reason, filterIsNotAnExpression) {
			t.Fatalf("a refusal that does not open with the shared literal cannot be classified:\n  %s", reason)
		}
		line := lossf(LossViewFilter, "%s — %s", `date(close_date).year == today().year`, reason)
		if got := classifyLoss(line); got != gapBaseFunction {
			t.Errorf("the refusal %q classified as %v, want gapBaseFunction — improving a message must never move a loss into UNCLASSIFIED", why, got)
		}
	}
}

// authoredLossMentions reports whether any loss line carries the substring.
func authoredLossMentions(losses []string, want string) bool {
	for _, l := range losses {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

// authoredRealSchema renders the test schema through the REAL renderer and
// parser, exactly as schemaForType does in production, so a clause typed here
// is typed against the same declaration the importer will type it against.
func authoredRealSchema(t *testing.T) *records.Schema {
	t.Helper()
	sc := schemaForType(authoredTestSchema(), "deal")
	if sc == nil {
		t.Fatal("the test schema did not render as a real *records.Schema, so nothing below types anything")
	}
	return sc
}
