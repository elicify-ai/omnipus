// Omnipus — the guards that keep the import report's EXPLANATIONS true.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// The systemic-gaps section told the founder for weeks that "ViewDef's
// `filters:` is a flat AND-only list ... with no boolean tree at all", quoting
// a line of view.go that commit 37bfb062 had deleted. Nothing failed. A
// hardcoded English sentence about a limitation has no way to notice the
// limitation being lifted, and a report is the one place where being wrong is
// indistinguishable from being right.
//
// Every test below fails when the report's prose and the thing that decides
// the behaviour drift apart. They are not coverage; they are the mechanism.
// ---------------------------------------------------------------------------

// TestGapTokens_StillExistInTheEmittingSource is the anti-drift guard for the
// half of the classifier that reflection cannot reach.
//
// Each gapShape identifies itself from a substring of a reason THIS PACKAGE
// writes. If a peer rewords that reason, the bucket silently empties and its
// losses reappear as UNCLASSIFIED — visible, but no longer explained. This
// test fails first, by name, naming the token that no longer exists.
//
// It reads STRING LITERALS from the AST rather than raw file text, so a token
// surviving only in a comment does not count: a comment cannot emit a loss.
func TestGapTokens_StillExistInTheEmittingSource(t *testing.T) {
	lits := emittingSourceLiterals(t)
	if len(lits) < 50 {
		t.Fatalf("only %d string literals harvested from the emitting packages — the harvester is broken, so this guard would pass vacuously", len(lits))
	}
	for _, sh := range gapShapes {
		check := func(tok, role string) {
			for _, l := range lits {
				if strings.Contains(l, tok) {
					return
				}
			}
			t.Errorf("gap shape %q %s %q, but no string literal in pkg/vaultimport (outside report.go) or pkg/records contains it any more.\n"+
				"Either the importer reworded that reason — in which case this shape now catches nothing and its losses fall to UNCLASSIFIED — or the shape was never real.",
				sh.label, role, tok)
		}
		for _, tok := range sh.tokens {
			check(tok, "matches on")
		}
		for _, tok := range sh.derivedFrom {
			check(tok, "splits its count using")
		}
	}
}

// emittingSourceLiterals harvests from BOTH packages that write a loss reason.
//
// pkg/vaultimport alone was enough while every reason was written here. It
// stopped being enough the moment the importer started carrying a base's
// `formulas:` block: a formula's reason is now records.ValidateFormulaSet's
// own sentence, composed in pkg/records/formula_type.go and formula_set.go and
// prefixed in formula_lex.go. Harvesting only this package would have left the
// five newest shapes guarded by nothing at all — a guard that passes because it
// is not looking, which is the exact failure mode this file exists to prevent.
func emittingSourceLiterals(t *testing.T) []string {
	t.Helper()
	lits := packageStringLiterals(t, ".", "report.go")
	return append(lits, packageStringLiterals(t, "../records")...)
}

// TestGapTokens_GuardCatchesAnInventedToken proves the guard above can fail.
// A guard nobody has watched fail is a guard nobody should trust.
func TestGapTokens_GuardCatchesAnInventedToken(t *testing.T) {
	lits := emittingSourceLiterals(t)
	const invented = "no sort-direction field" // a real, DEAD token this report used to match on
	for _, l := range lits {
		if strings.Contains(l, invented) {
			t.Fatalf("%q is emitted somewhere after all — pick a different dead token for this proof", invented)
		}
	}
}

// packageStringLiterals returns every string literal declared in the package's
// non-test .go files, excluding the named files.
func packageStringLiterals(t *testing.T, dir string, exclude ...string) []string {
	t.Helper()
	skip := map[string]bool{}
	for _, e := range exclude {
		skip[e] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}
	var out []string
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || skip[name] {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if perr != nil {
			t.Fatalf("parsing %s: %v", name, perr)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			bl, ok := n.(*ast.BasicLit)
			if !ok || bl.Kind != token.STRING {
				return true
			}
			if s, uerr := strconv.Unquote(bl.Value); uerr == nil {
				out = append(out, s)
			} else {
				out = append(out, bl.Value)
			}
			return true
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Classification
// ---------------------------------------------------------------------------

// TestClassifyLoss_ReadsTheImportersReasonNotTheOperatorsExpression is the
// regression for the defect that made one number hide two causes.
//
// `registration_renewal_date != ""` was counted as an EMPTY-STRING COMPARISON
// because its expression contains `!= ""`. It is nothing of the kind: the
// importer's own reason says the property is not declared. Six reported
// empty-string comparisons were four. The operator's text is untrusted input;
// only the importer's reason decides a bucket.
func TestClassifyLoss_ReadsTheImportersReasonNotTheOperatorsExpression(t *testing.T) {
	line := lossf(LossFilterLeaf, `registration_renewal_date != "" — property %q is not declared in the %q schema (never observed on a legal-entity note)`,
		"registration_renewal_date", "legal-entity")
	if got := classifyLoss(line); got != gapUndeclaredProperty {
		t.Errorf("classified as %v; the expression contains `!= \"\"` but the REASON says the property is undeclared, and the reason is what this importer actually decided.\n  line: %s", got, line)
	}

	genuine := lossf(LossFilterLeaf, "renewal_date != \"\" — `renewal_date != \"\"` has no faithful translation on a TEXT property: FR-007a keeps `\"\"` a PRESENT value for text, so `IS NOT NULL` would also match a record whose renewal_date is the empty string — a record the Obsidian filter excludes")
	if got := classifyLoss(genuine); got != gapEmptyStringOnText {
		t.Errorf("a genuine empty-string-on-text loss classified as %v, so the fix above has over-corrected and the bucket is now unreachable.\n  line: %s", got, genuine)
	}
}

// TestClassifyLoss_Corpus pins one representative of every shape the founder's
// vault actually produced. Each expected value is derived from the loss's
// stated reason, not from re-running the classifier.
func TestClassifyLoss_Corpus(t *testing.T) {
	cases := []struct {
		name string
		line string
		want gapKind
	}{
		{
			name: "formula reference in a filter, no reason attached",
			line: lossf(LossViewFilter, "formula.days_to_renewal <= 60"),
			want: gapFormula,
		},
		{
			name: "formula column, reason attached",
			// The importer now CARRIES a base's `formulas:` block, so the
			// reason this corpus line used to hold ("does not yet carry a
			// base's `formulas:` block") no longer exists anywhere in the
			// package — TestGapTokens_StillExistInTheEmittingSource is what
			// caught the drift. What a formula loss says now is why that ONE
			// formula could not be translated, and this is the shape of it.
			line: lossf(LossProperties, "column %q dropped — %s", "formula.age", `the base file declares no formula "age"`),
			want: gapFormula,
		},
		{
			name: "enum literal the inferred schema does not declare",
			line: lossf(LossFilterLeaf, "status == %q — value %q is not one of %q's declared enum values (scheduled)", "published", "published", "status"),
			want: gapEnumLiteral,
		},
		{
			name: "display column naming an undeclared property",
			line: lossf(LossProperties, "column %q dropped — not a declared property of %q", "last_refreshed", "legal-entity"),
			want: gapUndeclaredProperty,
		},
		{
			name: "or: over two different record types",
			line: lossf(LossBaseOuterFilter, "or:\n    - type == \"content\"\n    - type == \"brand-kit\""),
			want: gapMixedTypeDisjunction,
		},
		{
			name: "or: over one type nested inside an and:",
			line: lossf(LossBaseOuterFilter, "or:\n    - type == \"round\"\n    - and:\n        - type == \"company\"\n        - segment.contains(\"investor\")"),
			want: gapMixedTypeDisjunction,
		},
		{
			name: "or: whose branches do not disagree about the type",
			line: lossf(LossViewFilter, "or:\n    - stage == \"a\"\n    - stage == \"b\""),
			want: gapOtherCombinator,
		},
		{
			name: "a base function call this importer does not parse",
			line: lossf(LossViewFilter, "date(close_date).year == today().year"),
			want: gapBaseFunction,
		},
		{
			name: "a bare clause whose reason was discarded upstream",
			line: lossf(LossViewFilter, "realm != %q", "personal"),
			want: gapReasonDiscarded,
		},
		{
			name: "a reason this table has never seen",
			line: lossf(LossFilterLeaf, "whatever == 1 — the flux capacitor declined"),
			want: gapUnclassified,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyLoss(tc.line); got != tc.want {
				t.Errorf("classifyLoss = %v, want %v\n  line: %s", got, tc.want, tc.line)
			}
		})
	}
}

// TestClassifyLoss_FileMethodsAreNotAFunctionGap holds the line on the second
// stale claim this report used to make. `file.inFolder(...)` IS translated
// (translate.go's reFileMethod, FR-134), so a `file.` call must never be
// classified as an unparsed function expression — that is how "folder scoping
// is not expressible at all" would grow back.
func TestClassifyLoss_FileMethodsAreNotAFunctionGap(t *testing.T) {
	if isFunctionCallExpr(`file.inFolder("99-Temp")`) {
		t.Error("`file.inFolder(...)` matched the unparsed-function shape; it is translated by records.TranslateFileMethod (FR-134) and must not be reported as a gap")
	}
	if !isFunctionCallExpr("date(close_date).year == today().year") {
		t.Error("`date(...)`/`today()` no longer match the unparsed-function shape, so those losses would fall through to UNCLASSIFIED")
	}
}

// ---------------------------------------------------------------------------
// The wire-capability probes, and the prose they compose
// ---------------------------------------------------------------------------

// TestWireCaps_AreDerivedFromTheGeneratedTypes asserts each probe against the
// generated wire type INDEPENDENTLY of the probe's own machinery, so a probe
// hardcoded to a constant fails here.
func TestWireCaps_AreDerivedFromTheGeneratedTypes(t *testing.T) {
	cases := []struct {
		name string
		cap  wireCap
		want bool
		why  string
	}{
		{"any combinator", capAnyCombinator, true, "commit 37bfb062 gave every view a real all/any/not tree; VaultFilterNode.any carries disjunction"},
		{"formulas", capFormulas, true, "ViewDef.formulas carries a base's computed properties as source text (FR-140/FR-141)"},
		{"optional type", capOptionalType, true, "ViewDef.type is a pointer — omitting it is how an untyped view is spelled (FR-018b)"},
		{"multi-type list", capMultiType, false, "there is no `types` list on ViewDef; a view holds at most ONE record type, which is why a mixed-type or: cannot be carried"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cap.Present != tc.want {
				t.Errorf("probe %q reports Present=%v, want %v.\n  %s\n"+
					"If the contract genuinely changed, this test is the place the report's prose gets re-derived — do not just flip the expectation.",
					tc.cap.Path, tc.cap.Present, tc.want, tc.why)
			}
			if tc.cap.Path == "" {
				t.Error("a probe with no Path names no evidence, so its verdict is unfalsifiable prose again")
			}
		})
	}
}

// TestWireCap_VerdictFollowsThePresenceBit is the whole anti-staleness claim in
// one assertion: the sentence about the MODEL is a function of the model, so
// flipping the bit flips the sentence with no edit anywhere.
func TestWireCap_VerdictFollowsThePresenceBit(t *testing.T) {
	present := wireCap{Path: "ViewDef.example", Present: true}.verdict()
	absent := wireCap{Path: "ViewDef.example", Present: false}.verdict()

	if !strings.Contains(present, "CAN carry this") || !strings.Contains(present, "THIS IMPORTER") {
		t.Errorf("a present capability must say the model can hold it and the importer cannot; got:\n  %s", present)
	}
	if strings.Contains(present, "cannot carry this at all") {
		t.Errorf("a present capability still claims unrepresentability — the exact defect this mechanism exists to stop; got:\n  %s", present)
	}
	if !strings.Contains(absent, "cannot carry this at all") || !strings.Contains(absent, "unrepresentable") {
		t.Errorf("an absent capability must say so plainly; got:\n  %s", absent)
	}
	if !strings.Contains(present, "ViewDef.example") || !strings.Contains(absent, "ViewDef.example") {
		t.Error("neither verdict names the field it is a claim about, so a reader cannot check it")
	}
}

// TestSystemicGaps_NeverClaimTheModelCannotDoWhatItCan renders the real
// paragraphs and checks each one against its own probe. This is the test that
// would have failed on the day 37bfb062 landed.
func TestSystemicGaps_NeverClaimTheModelCannotDoWhatItCan(t *testing.T) {
	r := reportWithOneLossOfEveryShape()
	paras := r.systemicGaps()

	type check struct {
		marker string
		cap    wireCap
	}
	checks := []check{
		{"COMPUTED / FORMULA PROPERTY THIS IMPORTER COULD NOT TRANSLATE", capFormulas},
		{"MIXED-TYPE DISJUNCTION", capMultiType},
	}
	for _, c := range checks {
		para := findParagraph(t, paras, c.marker)
		if c.cap.Present && strings.Contains(para, "cannot carry this at all") {
			t.Errorf("%s says the model cannot express something `%s` proves it can:\n  %s", c.marker, c.cap.Path, para)
		}
		if !c.cap.Present && strings.Contains(para, "CAN carry this") {
			t.Errorf("%s says the model can express something `%s` proves it cannot:\n  %s", c.marker, c.cap.Path, para)
		}
	}

	// The specific dead sentence, named so it cannot come back by any route.
	for _, p := range paras {
		for _, dead := range []string{
			"flat AND-only list",
			"combined with AND",
			"no boolean tree",
			"has no computed-property type at all",
			"no multi-type or folder-scoped view concept at all",
		} {
			if strings.Contains(p, dead) {
				t.Errorf("the report has regrown a claim that is false today: %q\n  in: %s", dead, p)
			}
		}
	}
}

func findParagraph(t *testing.T, paras []string, marker string) string {
	t.Helper()
	for _, p := range paras {
		if strings.HasPrefix(p, marker) {
			return p
		}
	}
	t.Fatalf("no paragraph starting %q in:\n%s", marker, strings.Join(paras, "\n"))
	return ""
}

// ---------------------------------------------------------------------------
// The two summaries, and the accounting between them
// ---------------------------------------------------------------------------

// TestSystemicGaps_AccountForEveryLoss is the guard against the way the old
// section under-reported: it had no catch-all, so eight losses in the founder's
// vault were counted in the FR-105 work list and appeared in NO systemic-gap
// paragraph at all. Every loss lands in exactly one bucket, and the buckets sum.
func TestSystemicGaps_AccountForEveryLoss(t *testing.T) {
	r := reportWithOneLossOfEveryShape()

	want := 0
	for _, b := range r.Bases {
		for _, v := range b.Views {
			want += len(v.Losses)
		}
	}
	if want == 0 {
		t.Fatal("the fixture report carries no losses, so this test proves nothing")
	}

	got := 0
	for _, tally := range r.tally(func(v ViewOutcome) []string { return v.Losses }, false) {
		got += tally.Count
	}
	if got != want {
		t.Errorf("the closed table accounted for %d of %d losses — %d vanished from the summary the founder reads", got, want, want-got)
	}
}

// TestSummaries_AgreeAboutTheSameLoss. The FR-105 work list and the
// systemic-gaps section used to be two independently ordered substring lists
// over the same data, which is a divergence waiting to happen: the work list
// checked `!= ""` before "is not declared in the", so the same loss was an
// empty-string comparison in one section and could have been anything in the
// other. They now read one table, and this asserts it.
func TestSummaries_AgreeAboutTheSameLoss(t *testing.T) {
	r := reportWithOneLossOfEveryShape()
	// Make every loss a disabling one, so both sections see the same input.
	for bi := range r.Bases {
		for vi := range r.Bases[bi].Views {
			v := &r.Bases[bi].Views[vi]
			v.DisablingLosses = append([]string(nil), v.Losses...)
			v.Disabled = len(v.DisablingLosses) > 0
		}
	}

	byLosses := r.tally(func(v ViewOutcome) []string { return v.Losses }, false)
	byDisabling := r.tally(func(v ViewOutcome) []string { return v.DisablingLosses }, false)

	for kind, a := range byLosses {
		b := byDisabling[kind]
		if b == nil || a.Count != b.Count {
			bc := 0
			if b != nil {
				bc = b.Count
			}
			t.Errorf("kind %v: systemic gaps counted %d, the FR-105 work list counted %d — the two summaries disagree about identical input", kind, a.Count, bc)
		}
	}
	if len(byLosses) != len(byDisabling) {
		t.Errorf("the two summaries recognise different sets of shapes (%d vs %d)", len(byLosses), len(byDisabling))
	}
}

// TestSystemicGaps_SplitTheMixedTypeDisjunctionFromTheRest. A single "OR /
// DISJUNCTION (5)" hid whether those five drop because disjunction is
// unsupported (it is not — `any:` exists) or because their branches disagree
// about the record type (they do). One number over two causes is how the old
// sentence stayed plausible while being wrong.
func TestSystemicGaps_SplitTheMixedTypeDisjunctionFromTheRest(t *testing.T) {
	mixed := lossf(LossBaseOuterFilter, "or:\n    - type == \"content\"\n    - type == \"brand-kit\"")
	same := lossf(LossViewFilter, "or:\n    - stage == \"a\"\n    - stage == \"b\"")

	if classifyLoss(mixed) == classifyLoss(same) {
		t.Fatal("a disjunction that disagrees about the record type and one that does not land in the SAME bucket — the split this report exists to make is not being made")
	}
	if got := classifyLoss(mixed); got != gapMixedTypeDisjunction {
		t.Errorf("the mixed-type disjunction classified as %v", got)
	}
	if got := classifyLoss(same); got != gapOtherCombinator {
		t.Errorf("the single-type disjunction classified as %v", got)
	}
}

// TestProvisionedTypeParagraph_ChecksItsOwnClaim. The refusal paragraph asserts
// that the refused types are absent from this run's inferred types. It must
// VERIFY that against the report it is describing, not repeat the importer's
// word for it — a type refused as "no inferred schema" while appearing in the
// inferred list is a contradiction worth shouting about.
func TestProvisionedTypeParagraph_ChecksItsOwnClaim(t *testing.T) {
	refusal := `resolved record type "compliance" has no inferred schema — no note in the vault carries ` + "`type: compliance`"
	base := BaseOutcome{
		BaseRelPath: "06-Bases/Compliance.base",
		Status:      OutcomeRefused,
		Views:       []ViewOutcome{{DisplayName: "Upcoming", Status: OutcomeRefused, RefusedReason: refusal}},
	}

	agreeing := &Report{Bases: []BaseOutcome{base}, Types: []TypeSchemaSummary{{Type: "company"}}}
	para := findParagraph(t, agreeing.systemicGaps(), "RECORD TYPE PROVISIONED AHEAD OF ITS DATA")
	if !strings.Contains(para, "none of them is among") {
		t.Errorf("with `compliance` genuinely absent the paragraph should confirm it checked; got:\n  %s", para)
	}

	contradicting := &Report{Bases: []BaseOutcome{base}, Types: []TypeSchemaSummary{{Type: "compliance"}}}
	para = findParagraph(t, contradicting.systemicGaps(), "RECORD TYPE PROVISIONED AHEAD OF ITS DATA")
	if !strings.Contains(para, "DISAGREES") {
		t.Errorf("`compliance` WAS inferred this run, so the refusal reason is wrong and the report must say so; got:\n  %s", para)
	}
}

// TestRender_DoesNotPanicOnAnEmptyReport. Render is the founder's only window;
// a nil-slice edge case that crashes it costs him the whole account.
func TestRender_DoesNotPanicOnAnEmptyReport(t *testing.T) {
	var buf bytes.Buffer
	(&Report{}).Render(&buf)
	if buf.Len() == 0 {
		t.Fatal("an empty report rendered nothing at all")
	}
	if !strings.Contains(buf.String(), "Systemic gaps") {
		t.Error("the systemic-gaps section is missing from an empty report")
	}
}

// reportWithOneLossOfEveryShape builds a Report carrying one loss of each shape
// the closed table knows, so a test can assert over all of them at once.
func reportWithOneLossOfEveryShape() *Report {
	losses := []string{
		lossf(LossViewFilter, "formula.days_to_renewal <= 60"),
		lossf(LossFilterLeaf, "renewal_date != \"\" — `renewal_date != \"\"` has no faithful translation on a TEXT property: FR-007a keeps `\"\"` a PRESENT value for text, so `IS NOT NULL` would also match a record whose renewal_date is the empty string — a record the Obsidian filter excludes"),
		lossf(LossFilterLeaf, "last_refreshed != \"\" — property %q is not declared in the %q schema (never observed on a legal-entity note)", "last_refreshed", "legal-entity"),
		lossf(LossFilterLeaf, "status == %q — value %q is not one of %q's declared enum values (scheduled)", "published", "published", "status"),
		lossf(LossBaseOuterFilter, "or:\n    - type == \"content\"\n    - type == \"brand-kit\""),
		lossf(LossViewFilter, "or:\n    - stage == \"a\"\n    - stage == \"b\""),
		lossf(LossViewFilter, "date(close_date).year == today().year"),
		lossf(LossViewFilter, "realm != %q", "personal"),
		lossf(LossFilterLeaf, "whatever == 1 — the flux capacitor declined"),
		// The five reasons records.ValidateFormulaSet writes about ONE
		// formula. They arrived together when the importer started carrying a
		// base's `formulas:` block, and they are five separate shapes here
		// because they send the reader to five different places.
		lossf(LossFilterLeaf, "formula.days_to_due <= 7 — %s", truthyGuardReason("days_to_due", "text")),
		lossf(LossProperties, "column %q dropped — %s", "formula.age", untypedOperandReason("age", "created")),
		lossf(LossProperties, "column %q dropped — %s", "formula.monthly_cost", arithmeticOverNonNumberReason("monthly_cost", "text")),
		lossf(LossProperties, "column %q dropped — %s", "formula.team_name", formulaTooBigReason("team_name")),
		lossf(LossGroupBy, "grouping %q DESCENDING is carried into the view file faithfully, but a knowledge_find request has no group direction, so applying this view is refused until it does (ServeRefusalGroupDirection) — the groups are not silently reordered ascending", "formula.backlink_count"),
	}
	return &Report{
		Types: []TypeSchemaSummary{{Type: "company"}, {Type: "contact"}},
		Bases: []BaseOutcome{{
			BaseRelPath: "06-Bases/Everything.base",
			Status:      OutcomeConvertedWithLosses,
			Views:       []ViewOutcome{{DisplayName: "All shapes", Status: OutcomeConvertedWithLosses, Losses: losses}},
		}},
	}
}

// ---------------------------------------------------------------------------
// The provisioned-types section
// ---------------------------------------------------------------------------

// TestRenderProvisionedEntry_SurvivesAnEmptyAccount. The render path indexes
// [0] and [1:] on whatever ProvisionedType.ReportLines() returns — another
// part of this package, free to change. An empty slice there would panic the
// WHOLE report, which is the founder's only window into his import: a section
// that omits one line is a blemish, a report that crashes is a blackout.
//
// This calls the entry renderer DIRECTLY with an empty slice, because that
// case is unreachable through ReportLines() as written today — a test that
// went through Render would pass whether the guard existed or not.
func TestRenderProvisionedEntry_SurvivesAnEmptyAccount(t *testing.T) {
	var buf bytes.Buffer
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("rendering a provisioned type with an empty account panicked: %v", p)
			}
		}()
		renderProvisionedEntry(&buf, "compliance", nil)
	}()
	got := buf.String()
	if !strings.Contains(got, "compliance") {
		t.Errorf("the type was not named, so the entry vanished silently: %q", got)
	}
	if !strings.Contains(got, "defect to file") {
		t.Errorf("an entry with no account must be reported as a defect, not rendered as an ordinary blank: %q", got)
	}
}

// TestRenderProvisioned_RendersARealAccountThroughRender keeps the wiring
// honest: the entry renderer above is reached from Render, not orphaned.
func TestRenderProvisioned_RendersARealAccountThroughRender(t *testing.T) {
	r := &Report{Provisioned: []ProvisionedType{{
		Type:       "compliance",
		Bases:      []string{"06-Bases/Compliance.base"},
		Properties: []string{"authority", "due_date"},
	}}}
	var buf bytes.Buffer
	r.Render(&buf)
	got := buf.String()
	if !strings.Contains(got, "DECLARED FROM A `.base` FILE") {
		t.Error("the provisioned-types section did not render at all")
	}
	for _, want := range []string{"compliance", "authority", "due_date"} {
		if !strings.Contains(got, want) {
			t.Errorf("the account does not mention %q, so the operator cannot see what was assumed", want)
		}
	}
}

// TestRenderProvisioned_ChecksItsOwnHeading. The heading asserts that no note
// carries these types. That claim is checkable against the same report, so it
// must be checked — a provisioned type that also appears among the types
// inferred from real notes is a contradiction the founder must see, not a
// heading he is asked to believe.
func TestRenderProvisioned_ChecksItsOwnHeading(t *testing.T) {
	honest := &Report{
		Provisioned: []ProvisionedType{{Type: "compliance", Bases: []string{"06-Bases/Compliance.base"}, Properties: []string{"authority"}}},
		Types:       []TypeSchemaSummary{{Type: "company"}},
	}
	var buf bytes.Buffer
	honest.Render(&buf)
	if strings.Contains(buf.String(), "CONTRADICTION") {
		t.Errorf("no provisioned type is among the inferred types, so nothing should be flagged:\n%s", buf.String())
	}

	contradicting := &Report{
		Provisioned: []ProvisionedType{{Type: "compliance", Bases: []string{"06-Bases/Compliance.base"}, Properties: []string{"authority"}}},
		Types:       []TypeSchemaSummary{{Type: "compliance", NoteCount: 12}},
	}
	buf.Reset()
	contradicting.Render(&buf)
	if !strings.Contains(buf.String(), "CONTRADICTION") {
		t.Errorf("`compliance` was provisioned as unobserved AND inferred from 12 real notes; the report must say so:\n%s", buf.String())
	}
}

// TestRenderProvisioned_IsAbsentWhenNothingWasProvisioned keeps the section
// from becoming a permanent empty heading once provisioning stops firing.
func TestRenderProvisioned_IsAbsentWhenNothingWasProvisioned(t *testing.T) {
	var buf bytes.Buffer
	(&Report{}).Render(&buf)
	if strings.Contains(buf.String(), "DECLARED FROM A `.base` FILE") {
		t.Error("the provisioned-types heading rendered with nothing to report")
	}
}

// ---------------------------------------------------------------------------
// The two newest sections, held to the same standard
// ---------------------------------------------------------------------------

// TestRenderNameEvidenced_CountsTheTypesItFoundRatherThanNamingThem is the
// anti-staleness guard for the name-inference section, and it is the same
// argument as the systemic-gaps one in a smaller place.
//
// infer.go's rule produces only `date` today, and says so in a comment. A
// report sentence saying "these are dates" would be true right up to the day a
// second rule lands, and would then be silently wrong — which is exactly how
// "ViewDef's filters: is a flat AND-only list" survived commit 37bfb062. The
// section therefore counts the types it actually finds in the data.
func TestRenderNameEvidenced_CountsTheTypesItFoundRatherThanNamingThem(t *testing.T) {
	r := &Report{
		Types: []TypeSchemaSummary{{Type: "contract"}, {Type: "deal"}},
		NameEvidenced: []NameEvidencedInference{
			{RecordType: "contract", Property: "renewal_date", Type: records.TypeDate, DeclaringNotes: 4},
			{RecordType: "deal", Property: "close_date", Type: records.TypeDate, DeclaringNotes: 2},
			{RecordType: "deal", Property: "seat_count", Type: records.TypeInteger, DeclaringNotes: 1},
		},
	}
	var buf bytes.Buffer
	r.renderNameEvidenced(&buf)
	out := buf.String()

	if !strings.Contains(out, "date x2") || !strings.Contains(out, "integer x1") {
		t.Errorf("the heading did not count the types present in the data — a hardcoded \"these are dates\" is the shape that went stale before:\n%s", out)
	}
	if !strings.Contains(out, "GUESS") {
		t.Error("a type read off a property NAME, with no value anywhere in the vault behind it, must be called a guess in the founder's own words")
	}
	if strings.Contains(out, "CONTRADICTION") {
		t.Errorf("a consistent set produced a contradiction warning:\n%s", out)
	}
}

// TestRenderNameEvidenced_ChecksItsOwnPremise. The section's premise is that
// notes DECLARE the key and leave it blank. An entry that declares the key
// nowhere, or names a record type this run did not infer, breaks that premise;
// restating the claim over it would be the report being confidently wrong.
func TestRenderNameEvidenced_ChecksItsOwnPremise(t *testing.T) {
	noDeclaringNotes := &Report{
		Types:         []TypeSchemaSummary{{Type: "contract"}},
		NameEvidenced: []NameEvidencedInference{{RecordType: "contract", Property: "renewal_date", Type: records.TypeDate, DeclaringNotes: 0}},
	}
	var buf bytes.Buffer
	noDeclaringNotes.renderNameEvidenced(&buf)
	if !strings.Contains(buf.String(), "CONTRADICTION") {
		t.Errorf("an entry whose key no note declares at all left the section's premise unchallenged:\n%s", buf.String())
	}

	unknownType := &Report{
		Types:         []TypeSchemaSummary{{Type: "contract"}},
		NameEvidenced: []NameEvidencedInference{{RecordType: "ghost", Property: "renewal_date", Type: records.TypeDate, DeclaringNotes: 3}},
	}
	buf.Reset()
	unknownType.renderNameEvidenced(&buf)
	if !strings.Contains(buf.String(), "CONTRADICTION") {
		t.Errorf("a guess attached to a record type this run never inferred was narrated as though it were fine:\n%s", buf.String())
	}
}

// TestRenderNameEvidenced_IsAbsentWhenNothingWasGuessed keeps the section from
// printing an empty heading on the overwhelming majority of runs.
func TestRenderNameEvidenced_IsAbsentWhenNothingWasGuessed(t *testing.T) {
	var buf bytes.Buffer
	(&Report{Types: []TypeSchemaSummary{{Type: "contract"}}}).renderNameEvidenced(&buf)
	if buf.Len() != 0 {
		t.Errorf("a run that guessed nothing still printed a section:\n%s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// The five formula-validation shapes
//
// When the importer started carrying a base's `formulas:` block, the single
// "computed property" shape retired itself correctly — its reason ("does not
// yet carry a base's `formulas:` block") stopped existing and the shape moved
// to the zero-occurrence list with nobody editing prose. What took its place
// was 37 losses carrying records.ValidateFormulaSet's OWN reasons, which no
// shape recognised, so they printed as UNCLASSIFIED.
//
// The temptation is to catch all five with one formula-shaped bucket. That
// would be worse than UNCLASSIFIED, not better: only ONE of the five is
// formula-translator work. Two are decided by what the founder's vault
// actually stores, one is an FR-146 policy number, and one is an inference
// result. A single paragraph would tell him the formula work is unfinished and
// send him to the wrong file — the specific failure this file was rewritten to
// eliminate after a stale explanation reached him and became a false statement.
//
// The reason builders below compose the reasons the way pkg/records composes
// them, so a corpus line here is the emitting format string's own shape rather
// than a transcript of whatever the classifier happened to do.
// ---------------------------------------------------------------------------

// truthyGuardReason mirrors formula_type.go's inferIf, prefixed the way
// formula_lex.go prefixes a formula's errors.
func truthyGuardReason(formula, gotType string) string {
	return "formula " + formula + ": `if`'s condition is a truth value; it was given " + gotType +
		" at position 0; expected a truth value"
}

// untypedOperandReason mirrors formula_type.go's unknown-property branch.
func untypedOperandReason(formula, prop string) string {
	return "formula " + formula + ": `" + prop + "` is not a property this view can type — a formula operand must have a DECLARED type, or the formula would compare FALSE on some records with nothing reported at position 3; expected a property the view's record type declares"
}

// arithmeticOverNonNumberReason mirrors requireNumberOperands.
func arithmeticOverNonNumberReason(formula, gotType string) string {
	return "formula " + formula + ": `/` is arithmetic and is defined over numbers, but its left operand is " +
		gotType + " at position 53; expected a number"
}

// formulaTooBigReason mirrors formula_set.go's depth cap.
func formulaTooBigReason(formula string) string {
	return "formula " + formula + ": the expression nests 12 levels deep; FR-146 caps one formula at 8; expected at most 8 levels of nesting"
}

// TestClassifyLoss_TheFiveFormulaReasonsAreFiveDIFFERENTShapes is the headline
// regression. Every line below is a formula failing to translate; collapsing
// them into one bucket is the failure mode, so this asserts they stay apart.
func TestClassifyLoss_TheFiveFormulaReasonsAreFiveDIFFERENTShapes(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		want   gapKind
		why    string
	}{
		{
			name:   "truthiness guard over a non-date operand",
			reason: truthyGuardReason("days_to_expiry", "text"),
			want:   gapFormulaTruthyGuard,
			why:    "the guard-dropping rewrite is proved for a scalar date only; the operand's TYPE is what decides, so this must not read as formula-translator work",
		},
		{
			name:   "operand the view's record type does not declare",
			reason: untypedOperandReason("days_since_refresh", "last_refreshed"),
			want:   gapFormulaOperandUntyped,
			why:    "this is the undeclared-property INFERENCE gap reached through a formula; the formula is fine",
		},
		{
			name:   "arithmetic over an operand typed as text",
			reason: arithmeticOverNonNumberReason("monthly_cost", "text"),
			want:   gapFormulaArithmeticOverText,
			why:    "an isType() guard is a per-record test and does not narrow a statically declared type; a different remedy again",
		},
		{
			name:   "formula past FR-146's size cap",
			reason: formulaTooBigReason("team_name"),
			want:   gapFormulaTooBig,
			why:    "the formula was understood and refused for SIZE — a policy number, not a translation gap",
		},
		{
			name:   "formula past FR-146's per-view cap",
			reason: "the view defines 40 formulas; FR-146 caps a view at 32",
			want:   gapFormulaTooBig,
			why:    "the per-view caps are the same shape as the per-formula ones and must not fall out of the table",
		},
	}

	seen := map[gapKind]string{}
	for _, tc := range cases {
		line := lossf(LossFilterLeaf, "formula.x == 1 — %s", tc.reason)
		got := classifyLoss(line)
		if got != tc.want {
			t.Errorf("%s: classified as %v, want %v.\n  why it matters: %s\n  line: %s", tc.name, got, tc.want, tc.why, line)
		}
		if tc.want != gapFormulaTooBig {
			if prev, dup := seen[got]; dup {
				t.Errorf("%s and %s landed in the SAME bucket (%v). They need different remedies, so one paragraph cannot serve both.", tc.name, prev, got)
			}
			seen[got] = tc.name
		}
	}

	// And none of them may fall back to the pre-existing formula bucket, whose
	// paragraph says the base's `formulas:` block IS carried and what remains
	// is an expression our grammar cannot express. That sentence is true of
	// none of the five.
	for _, tc := range cases {
		if classifyLoss(lossf(LossFilterLeaf, "formula.x == 1 — %s", tc.reason)) == gapFormula {
			t.Errorf("%s fell into the generic formula bucket, whose paragraph would explain it wrongly", tc.name)
		}
	}
}

// TestGapBreakdown_SplitsTheTruthinessGuardByTheOperandTypeTheCheckerNamed.
//
// The split is not decoration. `if(due, date(due) < today() && …, false)` over
// a real DATE is blocked by the rewrite's second condition and is translator
// work; `if(expiry, …)` over a property typed TEXT is blocked by the type and
// is not. One total over both would answer the founder's "what do I do next"
// with the wrong file half the time.
func TestGapBreakdown_SplitsTheTruthinessGuardByTheOperandTypeTheCheckerNamed(t *testing.T) {
	r := &Report{Bases: []BaseOutcome{{
		BaseRelPath: "06-Bases/Mixed.base",
		Status:      OutcomeConvertedWithLosses,
		Views: []ViewOutcome{{
			DisplayName: "V", Status: OutcomeConvertedWithLosses,
			Losses: []string{
				lossf(LossFilterLeaf, "formula.a == 1 — %s", truthyGuardReason("a", "text")),
				lossf(LossFilterLeaf, "formula.b == 1 — %s", truthyGuardReason("b", "text")),
				lossf(LossFilterLeaf, "formula.c == 1 — %s", truthyGuardReason("c", "date")),
			},
		}},
	}}}

	tally := r.tally(func(v ViewOutcome) []string { return v.Losses }, false)[gapFormulaTruthyGuard]
	if tally == nil {
		t.Fatal("no truthiness-guard tally at all — the shape did not fire, so the split below proves nothing")
	}
	if tally.Count != 3 {
		t.Fatalf("counted %d truthiness losses, want 3", tally.Count)
	}
	if got := tally.Sub["text"]; got != 2 {
		t.Errorf("text operands = %d, want 2 — the split is what routes the reader, so a wrong number here is a wrong instruction", got)
	}
	if got := tally.Sub["date"]; got != 1 {
		t.Errorf("date operands = %d, want 1. A `date` operand is ALREADY typed correctly: counting it with the others would tell the founder to go fix inference for a property whose inference is right", got)
	}
	if tally.subTotal() != tally.Count {
		t.Errorf("the split accounts for %d of %d — a shortfall means the breakdown stopped parsing the reason and the paragraph is summing something it cannot see", tally.subTotal(), tally.Count)
	}

	para := r.narrate(gapFormulaTruthyGuard, tally)
	for _, want := range []string{"text x2", "date x1"} {
		if !strings.Contains(para, want) {
			t.Errorf("the rendered paragraph does not carry %q, so the split never reaches the founder:\n%s", want, para)
		}
	}
}

// TestGapBreakdown_NamesTheUndeclaredFormulaOperands. The remedy is
// per-property — "six formula operands are undeclared" is not actionable and
// "`created`, `updated`, `last_refreshed`" is.
func TestGapBreakdown_NamesTheUndeclaredFormulaOperands(t *testing.T) {
	r := &Report{Bases: []BaseOutcome{{
		BaseRelPath: "06-Bases/U.base",
		Status:      OutcomeConvertedWithLosses,
		Views: []ViewOutcome{{
			DisplayName: "V", Status: OutcomeConvertedWithLosses,
			Losses: []string{
				lossf(LossProperties, "column %q dropped — %s", "formula.age", untypedOperandReason("age", "created")),
				lossf(LossProperties, "column %q dropped — %s", "formula.age2", untypedOperandReason("age2", "created")),
				lossf(LossProperties, "column %q dropped — %s", "formula.stage", untypedOperandReason("stage", "updated")),
			},
		}},
	}}}

	tally := r.tally(func(v ViewOutcome) []string { return v.Losses }, false)[gapFormulaOperandUntyped]
	if tally == nil {
		t.Fatal("the undeclared-operand shape did not fire")
	}
	if tally.Sub["created"] != 2 || tally.Sub["updated"] != 1 {
		t.Errorf("property split = %v, want created:2 updated:1 — the name is read out of the checker's own backticked sentence", tally.Sub)
	}
	para := r.narrate(gapFormulaOperandUntyped, tally)
	for _, want := range []string{"`created` x2", "`updated`"} {
		if !strings.Contains(para, want) {
			t.Errorf("the paragraph does not name %s, so the reader gets a count instead of a work list:\n%s", want, para)
		}
	}
}

// TestSystemicGaps_DoNotAccuseThisImporterWhenTheCauseIsSomewhereElse.
//
// wireCap.verdict() ends by declaring the loss an FR-107 defect to file
// against this importer. That is right when a missing importer is the only
// thing in the way, and WRONG two sentences after a paragraph has explained
// that the cause is prose in the founder's own vault, or an FR-146 cap, or the
// type inference — where it contradicts the sentence above it and sends him to
// the file he was just told not to open. This pins that the four
// externally-caused paragraphs use notTheConstraint() instead.
//
// The forbidden text is taken FROM verdict() rather than typed here, so a
// reword of verdict() cannot leave this test guarding a string nobody emits.
func TestSystemicGaps_DoNotAccuseThisImporterWhenTheCauseIsSomewhereElse(t *testing.T) {
	accusation := capFormulas.verdict()
	if !strings.Contains(accusation, "defect to file") {
		t.Fatalf("verdict() no longer accuses this importer (%q), so this test is guarding nothing — re-derive the forbidden text", accusation)
	}

	r := reportWithOneLossOfEveryShape()
	tallies := r.tally(func(v ViewOutcome) []string { return v.Losses }, false)

	external := map[gapKind]string{
		gapFormulaTruthyGuard:        "the operand's inferred type decides this, and the paragraph has just said so",
		gapFormulaOperandUntyped:     "the paragraph names the type inference as the cause and says the formula needs no change",
		gapFormulaArithmeticOverText: "the paragraph names the vault's own values and the formula TYPE SYSTEM, not this importer",
		gapFormulaTooBig:             "an FR-146 policy cap is a decision to take, not a defect to file",
		// gapGroupDirection is RETIRED — the find request can ask for a group
		// direction now, so the importer emits no such loss and this entry would
		// fail the vacuity check below rather than assert anything.
	}
	for kind, why := range external {
		tally := tallies[kind]
		if tally == nil || tally.Count == 0 {
			t.Fatalf("no loss of kind %v in the fixture, so this assertion is vacuous", kind)
		}
		para := r.narrate(kind, tally)
		if strings.Contains(para, accusation) {
			t.Errorf("the %v paragraph embeds verdict()'s accusation against this importer, contradicting itself: %s\n\nparagraph:\n%s", kind, why, para)
		}
	}

	// The control: a gap whose cause really IS a missing capability must still
	// carry its verdict, or this test has simply banned the sentence.
	mixed := tallies[gapMixedTypeDisjunction]
	if mixed == nil {
		t.Fatal("no mixed-type disjunction in the fixture — the control is missing")
	}
	if !strings.Contains(r.narrate(gapMixedTypeDisjunction, mixed), capMultiType.verdict()) {
		t.Error("the mixed-type paragraph no longer carries its model verdict, so the rule above has over-corrected into 'never state the verdict'")
	}
}

// TestProbeListElementField_FlipsWhenTheElementGainsTheField exercises the
// TRUE branch of the group-direction probe, which the real wire types cannot
// reach today. A probe whose true branch has never executed is a claim, not a
// mechanism — and the whole point of deriving the sentence is that it stops
// being printed on the day the field lands.
func TestProbeListElementField_FlipsWhenTheElementGainsTheField(t *testing.T) {
	type groupingToday struct {
		GroupBy *[]string `json:"group_by,omitempty"`
	}
	type groupingElement struct {
		Property  string `json:"property"`
		Direction string `json:"direction"`
	}
	type groupingTomorrow struct {
		GroupBy *[]groupingElement `json:"group_by,omitempty"`
	}

	today := probeListElementField("X.group_by[].direction", groupingToday{}, "group_by", "direction")
	if today.Present {
		t.Error("a []string element was reported as carrying a direction; the probe is not looking at the element type")
	}
	tomorrow := probeListElementField("X.group_by[].direction", groupingTomorrow{}, "group_by", "direction")
	if !tomorrow.Present {
		t.Error("an element struct that DOES declare `direction` was reported as not carrying one, so the paragraph could never stop calling the refusal correct")
	}

	// And the live probe reflects the real request type as it stands.
	if capFindGroupDirection.Present != elementHasDirection(generated.VaultFindRequest{}) {
		t.Error("capFindGroupDirection disagrees with the generated request type it is derived from")
	}
	if got := groupDirectionRequestClause(); capFindGroupDirection.Present == strings.Contains(got, "nowhere to ask") {
		t.Errorf("the rendered clause does not follow the presence bit: present=%v, clause=%q", capFindGroupDirection.Present, got)
	}
}

// elementHasDirection is the test's own independent reading of the wire type,
// so the assertion above is not the probe agreeing with itself.
func elementHasDirection(sample any) bool {
	tv := reflect.TypeOf(sample)
	for i := 0; i < tv.NumField(); i++ {
		f := tv.Field(i)
		if !strings.HasPrefix(f.Tag.Get("json"), "group_by") {
			continue
		}
		ft := f.Type
		for ft.Kind() == reflect.Ptr || ft.Kind() == reflect.Slice {
			ft = ft.Elem()
		}
		if ft.Kind() != reflect.Struct {
			return false
		}
		for j := 0; j < ft.NumField(); j++ {
			if strings.HasPrefix(ft.Field(j).Tag.Get("json"), "direction") {
				return true
			}
		}
	}
	return false
}

// TestClassifyLoss_ACombinatorCarryingItsChildsReasonJoinsTheChildsBucket.
//
// `!=` desugars into a `not:` wrapper, and the combinator branch used to
// report its own verbatim while discarding the child's diagnosis — six losses
// arrived with no reason at all and every other bucket was a floor rather than
// a total. The remedy is in view_write.go, but this file has to be ready to
// absorb it, and "ready" is a claim worth failing over: the classifier reads
// the reason half of a loss line, so a `not:`-wrapped loss must land in its
// CHILD's bucket the moment the child's reason is carried through.
func TestClassifyLoss_ACombinatorCarryingItsChildsReasonJoinsTheChildsBucket(t *testing.T) {
	// Before: the group's verbatim alone, no reason. Nothing can be said.
	bare := lossf(LossViewFilter, "realm != %q", "personal")
	if got := classifyLoss(bare); got != gapReasonDiscarded {
		t.Fatalf("a reason-less `!=` loss classified as %v, want gapReasonDiscarded — the fixture no longer reproduces the state this guards", got)
	}

	// After: the same verbatim with the leaf's own diagnosis appended.
	for _, tc := range []struct {
		reason string
		want   gapKind
	}{
		{`value "personal" is not one of "realm"'s declared enum values (work)`, gapEnumLiteral},
		{`property "realm" is not declared in the "contract" schema (never observed on a contract note)`, gapUndeclaredProperty},
		{truthyGuardReason("is_overdue", "text"), gapFormulaTruthyGuard},
	} {
		carried := lossf(LossViewFilter, "realm != %q — %s", "personal", tc.reason)
		if got := classifyLoss(carried); got != tc.want {
			t.Errorf("a `not:`-wrapped loss carrying its child's reason classified as %v, want %v.\n"+
				"Until this holds, every other count in the summary stays a floor rather than a total.\n  line: %s", got, tc.want, carried)
		}
	}
}

// TestClassifyLoss_TheBaseFunctionShapeSurvivesTheLeafGainingAReason.
//
// This shape used to be recognised ONLY by the expression's own shape, because
// `date(close_date).year == today().year` arrived with no reason attached. The
// moment the leaf parser started attaching the grammar's named refusal, those
// losses stopped being reason-less — and classifyLoss returns UNCLASSIFIED for
// any reason matching no token. A peer writing a strictly BETTER loss message
// would therefore have moved this bucket to 0 and grown UNCLASSIFIED by 2:
// an improvement showing up in the founder's report as a regression.
//
// So the shape has to catch BOTH forms, and both must be asserted. Without the
// reason-carrying case below, deleting the tokens breaks nothing that fails.
func TestClassifyLoss_TheBaseFunctionShapeSurvivesTheLeafGainingAReason(t *testing.T) {
	expr := "date(close_date).year == today().year"

	if got := classifyLoss(lossf(LossViewFilter, "%s", expr)); got != gapBaseFunction {
		t.Errorf("the reason-LESS form classified as %v, want gapBaseFunction", got)
	}

	// The two reasons the leaf parser hands over, both composed the way their
	// emitting sites compose them rather than copied from a run.
	for _, reason := range []string{
		"a view's filter compares a PROPERTY against a literal, and this clause is an EXPRESSION. Handed to the formula grammar, it is refused there too: `.year` is not a field the formula grammar defines at position 17; expected one of .days, .hour, .hours, .length, .milliseconds, .minutes, .seconds",
		"`.month` is not a field the formula grammar defines at position 17; expected one of .days, .hour, .hours, .length, .milliseconds, .minutes, .seconds",
	} {
		line := lossf(LossViewFilter, "%s — %s", expr, reason)
		if got := classifyLoss(line); got != gapBaseFunction {
			t.Errorf("a `.base` function loss carrying the grammar's own named refusal classified as %v, want gapBaseFunction.\n"+
				"Improving the loss MESSAGE must never move a loss into UNCLASSIFIED — that reads as a regression in the founder's report.\n  line: %s", got, line)
		}
	}
}
