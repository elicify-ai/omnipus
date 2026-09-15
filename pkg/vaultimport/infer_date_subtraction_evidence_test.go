// Omnipus — the third evidence shape for "a base formula declares a property's
// type": a BARE property subtracted against a zero-argument date constructor.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHAT THIS FILE DEFENDS
//
// TypePropertiesFromBaseFormulas used to read exactly one shape — a `date()`
// or `time()` call wrapped around a bare property name. infer.go's header
// carried a written REFUSAL of a second shape, `today() - x`, resting on two
// claims about the founder's vault:
//
//	(a) `-` is defined over numbers as well as dates, so the rule "only exists
//	    once the OTHER operand has been proved a date, which is a second type
//	    inference carrying its own failure modes".
//	(b) it "would recover nothing here".
//
// Both are now false of the shape actually admitted, and this file is what
// makes that a measurement rather than an assertion.
//
// (a) IS ANSWERED BY THE RESTRICTION, and TestDateSubtraction_GrammarTypes...
// is the oracle for it. The other operand is never inferred: it must be the
// SYNTACTIC token `today()` or `now()`, a call with zero arguments whose type
// is a constant of the pinned grammar. The claim the whole rule rests on —
// "`today() - P` type-checks if and only if P is a date" — is a fact about
// records.inferBinary, so it is tested AGAINST records.InferFormulaType over
// every one of records.PropertyTypes, not against anything in this package.
// If a future grammar revision adds `date - number`, that test goes RED and
// this rule must be withdrawn; nothing else in the tree would notice.
//
// (b) IS ANSWERED BY THE VAULT, in TestDateSubtraction_FiresOnExactlyThe...
// Three formulas in the founder's 18 base files subtract a bare property from
// `today()`, and the rule fires on exactly ONE of them. That test names all
// three and asserts the outcome of each, so a change that made the rule
// greedier — dropping a containment clause, or attributing an untyped view's
// property to a record type — turns it RED on the count.
//
// ORACLE INDEPENDENCE. No expected value below is read off infer.go. The
// shape table's answers come from the rule STATED in the header (bare property
// on one side, zero-argument constructor on the other, nothing else); the
// grammar oracle's answers come from records' own typing rules; and the vault
// test's three formulas are quoted from the founder's `.base` files.
// ---------------------------------------------------------------------------

// oneTypeSchema is a record type declaring a single scalar property `p` of the
// given type — the smallest environment in which `today() - p` can be typed.
func oneTypeSchema(pt records.PropertyType) *records.Schema {
	return &records.Schema{
		SchemaVersion: 1,
		Type:          "probe",
		Properties: map[string]*records.Property{
			"p": {Name: "p", Type: pt},
		},
		PropertyOrder: []string{"p"},
	}
}

// TestDateSubtraction_GrammarTypesTheShapeForDatesAndNothingElse is the oracle
// under the whole rule.
//
// The rule reads `today() - p` as the operator DECLARING p a date. That is
// only sound if the grammar refuses every other reading of the same text —
// otherwise the expression would be a statement the operator could have made
// about a number, and reading a date out of it would be a guess.
//
// So: type `today() - p` against a schema declaring p as each of the eight
// property types in turn, through records.InferFormulaType, and require that
// exactly one of them succeeds. The expected answer is derived from the pinned
// grammar's own account of `-` ("When subtracting two dates, the result is a
// Duration type", and requireNumberOperands for everything else), not from
// anything this package computes.
func TestDateSubtraction_GrammarTypesTheShapeForDatesAndNothingElse(t *testing.T) {
	// Both operand orders, and both constructors, because the rule admits all
	// four and a soundness argument that held for only some of them would be
	// a rule about spelling.
	exprs := []string{
		"today() - p",
		"p - today()",
		"now() - p",
		"p - now()",
	}

	for _, src := range exprs {
		root, perr := records.ParseFormula(src)
		if perr != nil {
			t.Fatalf("%s: the pinned grammar must PARSE this shape, else the rule is reading text the product refuses: %v", src, perr)
		}

		var accepted []records.PropertyType
		for _, pt := range records.PropertyTypes {
			env := records.SchemaFormulaEnv{Schema: oneTypeSchema(pt)}
			ft, _, _, ferr := records.InferFormulaType(root, env)
			if ferr != nil {
				continue
			}
			accepted = append(accepted, pt)
			if ft != records.FormulaDuration {
				t.Errorf("%s with p:%s typed as %s — a date minus a date is the grammar's ONLY producer of a duration; anything else here means `-` gained a typing this rule's soundness argument does not cover",
					src, pt, ft)
			}
		}

		if len(accepted) != 1 || accepted[0] != records.TypeDate {
			t.Errorf("%s type-checks for %v; the rule reads this expression as a DECLARATION that p is a date, which is only honest if `date` is the one and only type it accepts. If the grammar has gained another, withdraw the subtraction shape from dateSubtrahendFunctions rather than widening this test.",
				src, accepted)
		}
	}
}

// TestDateSubtraction_ShapeTableIsWhatTheHeaderSays walks the reader of
// formulas directly, over the shapes the header admits and the shapes it
// refuses by name.
//
// Every "want" below is the header's own rule applied by hand: a BARE
// RefProperty on one side, a ZERO-argument dateSubtrahendFunctions call on the
// other, and nothing else counts.
func TestDateSubtraction_ShapeTableIsWhatTheHeaderSays(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want map[string]string // property -> the term recorded for it
	}{
		// --- ADMITTED ------------------------------------------------------
		{
			name: "today minus a bare property, the founder's own Deals.age",
			src:  "(today() - created).days",
			want: map[string]string{"created": "today"},
		},
		{
			name: "the reverse order, admitted on the time() precedent",
			src:  "(created - today()).days",
			want: map[string]string{"created": "today"},
		},
		{
			name: "now() is the other half of inferCall's one arm",
			src:  "(now() - created).days",
			want: map[string]string{"created": "now"},
		},
		{
			name: "the founder's Hiring.age_in_stage: date() on updated, subtraction on created",
			src:  `if(updated, (today() - date(updated)).days, (today() - created).days)`,
			want: map[string]string{"updated": "date", "created": "today"},
		},

		// --- REFUSED, each for the reason the header states -----------------
		{
			name: "two properties: the header's old objection (a), still refused",
			src:  "(finished - started).days",
			want: map[string]string{},
		},
		{
			name: "the property is not bare — this is the date() rule, not this one",
			src:  "(today() - date(updated)).days",
			want: map[string]string{"updated": "date"},
		},
		{
			name: "an expression rather than a name says nothing about either name in it",
			src:  "(today() - (a + b)).days",
			want: map[string]string{},
		},
		{
			name: "file.* is not a declared property",
			src:  "(today() - file.ctime).days",
			want: map[string]string{},
		},
		{
			name: "a formula reference is not a property declaration either",
			src:  "today() - formula.age",
			want: map[string]string{},
		},
		{
			name: "arithmetic that is not subtraction carries nothing",
			src:  "today() + created",
			want: map[string]string{},
		},
		{
			name: "a plain number subtraction is untouched",
			src:  "cost - 12",
			want: map[string]string{},
		},
		{
			// FOUND BY MUTATION TESTING, and it is the case a reader is most
			// likely to think cannot arise. `today(x)` PARSES — records checks
			// call arity when it TYPES, not when it parses — so a Call named
			// `today` carrying an argument really does reach this walk. It is
			// not a date constructor: it is an expression the translator will
			// refuse, and reading a declaration out of text the product
			// refuses is the mistake the unparseable-formula branch exists to
			// avoid, arriving one step later. Neither name is recorded: not
			// `opened`, because what it was subtracted against is not a
			// constructor, and not `created`, because it sits in an argument
			// list rather than opposite one.
			name: "a constructor with an argument is not a constructor",
			src:  "today(created) - opened",
			want: map[string]string{},
		},
		{
			name: "a guard is not a declaration (the header's if(P, ...) entry)",
			src:  `if(created, formula.age > 30, false)`,
			want: map[string]string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, perr := records.ParseFormula(tc.src)
			if perr != nil {
				t.Fatalf("parse %q: %v", tc.src, perr)
			}
			got := datePropertyArguments(root)
			if len(got) != len(tc.want) {
				t.Fatalf("%q carried %v, want %v", tc.src, got, tc.want)
			}
			for prop, term := range tc.want {
				if got[prop] != term {
					t.Errorf("%q: property %q recorded as %q, want %q", tc.src, prop, got[prop], term)
				}
			}
		})
	}
}

// TestDateSubtraction_AConversionOutranksASubtraction pins the precedence
// betterDateEvidence states, and pins it in the direction that plain string
// comparison gets WRONG.
//
// One property reached through both shapes in one expression is reported once.
// Which spelling survives is a decision — a conversion names the property
// inside a call the founder can point at — and it must not be an accident of
// where the letters land. `now` sorts before `time`, so the naive ordering
// would let a subtraction beat a conversion in exactly one of the four pairs.
func TestDateSubtraction_AConversionOutranksASubtraction(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		// date < today lexicographically too — this pair would pass either way.
		{"(today() - x).days + date(x).year", "date"},
		// The pair the naive ordering gets wrong: "now" < "time".
		{"(now() - x).days + time(x)", "time"},
		// Order of appearance must not decide it, in either direction.
		{"time(x) + (now() - x).days", "time"},
	}
	for _, tc := range cases {
		root, perr := records.ParseFormula(tc.src)
		if perr != nil {
			// `.year` is outside the pinned snapshot for TYPING, but this rule
			// runs on the PARSE tree; if the parser refuses it, the case is
			// simply not expressible and skipping is honest.
			t.Logf("skipping %q — the pinned grammar does not parse it: %v", tc.src, perr)
			continue
		}
		if got := datePropertyArguments(root)["x"]; got != tc.want {
			t.Errorf("%q recorded %q for x, want %q — a CONVERSION outranks a SUBTRACTION regardless of spelling and regardless of source order",
				tc.src, got, tc.want)
		}
	}
	// The ranking itself, stated directly, so a reader does not have to
	// reconstruct it from the three expressions above.
	for _, conv := range []string{"date", "time"} {
		for _, sub := range []string{"today", "now"} {
			if !betterDateEvidence(sub, conv) {
				t.Errorf("%s() must displace a %s() subtraction as the recorded evidence", conv, sub)
			}
			if betterDateEvidence(conv, sub) {
				t.Errorf("a %s() subtraction must NOT displace an already-recorded %s()", sub, conv)
			}
		}
	}
}

// TestDateSubtraction_TheTwoSpellingSetsAreDisjoint is the invariant
// FormulaEvidence.Function's doc claims: one field says which of the two
// shapes fired, which only works while no spelling is in both maps.
//
// It is cheap and it is here because the alternative — a second boolean flag —
// was rejected on the strength of this property holding.
func TestDateSubtraction_TheTwoSpellingSetsAreDisjoint(t *testing.T) {
	for name := range dateEvidencingFunctions {
		if dateSubtrahendFunctions[name] {
			t.Errorf("%q is in BOTH evidence maps; FormulaEvidence.Function can no longer say which shape fired, and ReportLines will describe a call as a subtraction or the reverse", name)
		}
	}
	// A zero-argument constructor admitted here must actually BE one in the
	// grammar, or "its type is a constant with no operand to infer" — the
	// whole answer to objection (a) — stops being true of it.
	for name := range dateSubtrahendFunctions {
		root, perr := records.ParseFormula(name + "()")
		if perr != nil {
			t.Fatalf("%s() must parse with zero arguments: %v", name, perr)
		}
		ft, _, _, ferr := records.InferFormulaType(root, records.SchemaFormulaEnv{Schema: oneTypeSchema(records.TypeText)})
		if ferr != nil {
			t.Fatalf("%s() must type with no schema help at all: %v", name, ferr)
		}
		if ft != records.FormulaDate {
			t.Errorf("%s() types as %s, not date — it carries no date declaration and must leave dateSubtrahendFunctions", name, ft)
		}
	}
}

// TestDateSubtraction_ContainmentClausesStillRefuseTheSameThings runs the rule
// end to end over a synthetic vault, once per clause, and requires the clause
// to hold for the SUBTRACTION shape and not only for the call shape it was
// written against.
//
// The clauses are infer.go's four, and each fixture below breaks exactly one
// of them while leaving the other three satisfied.
func TestDateSubtraction_ContainmentClausesStillRefuseTheSameThings(t *testing.T) {
	base := func(formula string) string {
		return "filters:\n  and:\n    - type == \"widget\"\nformulas:\n  age: " + formula +
			"\nviews:\n  - type: table\n    name: All\n    order:\n      - file.name\n"
	}

	t.Run("clause 1 — never invents a property", func(t *testing.T) {
		_, rep := formulaGateVault(t, map[string]string{
			"06-Bases/W.base": base("(today() - never_declared).days"),
			"notes/one.md":    "---\ntype: widget\nname: one\n---\n\nbody\n",
		})
		for _, fe := range rep.FormulaEvidenced {
			if fe.Property == "never_declared" {
				t.Fatalf("the rule invented %s.%s from a formula; a vocabulary is provisioning's question, not this one", fe.RecordType, fe.Property)
			}
		}
	})

	t.Run("clause 2 — a real value beats the base file", func(t *testing.T) {
		_, rep := formulaGateVault(t, map[string]string{
			"06-Bases/W.base": base("(today() - opened).days"),
			// `opened` holds prose, so the notes typed it `text` from data.
			// A base formula must not overrule that: promoting it to `date`
			// would make this run report its own note invalid.
			"notes/one.md": "---\ntype: widget\nopened: sometime last spring\n---\n\nbody\n",
			"notes/two.md": "---\ntype: widget\nopened: whenever we got round to it\n---\n\nbody\n",
		})
		for _, fe := range rep.FormulaEvidenced {
			if fe.Property == "opened" {
				t.Fatalf("%s.opened was promoted to %s over %d real values — data beats a base file, exactly as data beats a name", fe.RecordType, fe.Type, 2)
			}
		}
		if rep.Validation.InvalidRecords != 0 {
			t.Fatalf("the run reported %d invalid record(s); a formula guess that invalidates the founder's own note is the one outcome this package admits no exception to", rep.Validation.InvalidRecords)
		}
	})

	t.Run("clause 3 — only ever strengthens the text fallback", func(t *testing.T) {
		_, rep := formulaGateVault(t, map[string]string{
			"06-Bases/W.base": base("(today() - opened).days"),
			// Real ISO values already type `opened` as `date`. The rule must
			// not fire a second time and must not report a change that is not
			// one.
			"notes/one.md": "---\ntype: widget\nopened: 2026-01-02\n---\n\nbody\n",
		})
		for _, fe := range rep.FormulaEvidenced {
			if fe.Property == "opened" {
				t.Fatalf("the rule claimed credit for %s.opened (was %s); it was already a date from observed values and this entry tells the founder to check a base file that decided nothing", fe.RecordType, fe.Was)
			}
		}
	})

	t.Run("an UNTYPED view attributes the declaration to nobody", func(t *testing.T) {
		// No `type ==` clause anywhere: FR-018b's untyped view queries every
		// note in scope, so a property name in it is not scoped to one record
		// type. This is the shape of the founder's own Inbox-Triage.base.
		_, rep := formulaGateVault(t, map[string]string{
			"06-Bases/W.base": "filters:\n  and:\n    - file.inFolder(\"notes\")\nformulas:\n  age: (today() - created).days\nviews:\n  - type: table\n    name: All\n    order:\n      - file.name\n",
			"notes/one.md":    "---\ntype: widget\nname: one\n---\n\nbody\n",
			"notes/two.md":    "---\ntype: gadget\nname: two\n---\n\nbody\n",
		})
		for _, fe := range rep.FormulaEvidenced {
			if fe.Property == "created" {
				t.Fatalf("an untyped view's formula declared %s.created; nothing scopes that name to one record type, and picking one is the guess this clause exists to refuse", fe.RecordType)
			}
		}
	})
}

// TestDateSubtraction_ReportNamesTheSubtractionNotACall defends the sentence
// the founder actually reads.
//
// The evidence line for a call says "reads `x` through date()". Reusing it for
// a subtraction would tell him to look for a `today()` call wrapped around his
// property, which he never wrote — and the point of naming the evidence at all
// is that he can go and check it.
func TestDateSubtraction_ReportNamesTheSubtractionNotACall(t *testing.T) {
	_, rep := formulaGateVault(t, map[string]string{
		"06-Bases/W.base": "filters:\n  and:\n    - type == \"widget\"\nformulas:\n  age: (today() - opened).days\nviews:\n  - type: table\n    name: All\n    order:\n      - file.name\n",
		// `opened` is declared (the note carries the key) and value-less, so
		// all four containment clauses pass.
		"notes/one.md": "---\ntype: widget\nname: one\nopened:\n---\n\nbody\n",
	})

	var got *FormulaEvidencedType
	for i := range rep.FormulaEvidenced {
		if rep.FormulaEvidenced[i].Property == "opened" {
			got = &rep.FormulaEvidenced[i]
		}
	}
	if got == nil {
		t.Fatalf("the subtraction shape did not fire on a declared, value-less text property; FormulaEvidenced=%+v", rep.FormulaEvidenced)
	}
	if got.Type != records.TypeDate || got.Was != records.TypeText {
		t.Errorf("promotion recorded as %s (was %s), want date (was text)", got.Type, got.Was)
	}
	if len(got.Evidence) != 1 {
		t.Fatalf("want exactly one evidence entry, got %d", len(got.Evidence))
	}
	ev := got.Evidence[0]
	if ev.Function != "today" {
		t.Errorf("evidence names %q; the founder is being sent to the text that carried the declaration and it was a today() subtraction", ev.Function)
	}
	if ev.Source != "(today() - opened).days" {
		t.Errorf("evidence quotes %q, want the operator's own expression verbatim", ev.Source)
	}

	lines := strings.Join(got.ReportLines(), "\n")
	if strings.Contains(lines, "through today()") {
		t.Errorf("the report describes the subtraction as a CALL:\n%s", lines)
	}
	for _, want := range []string{"subtracts the bare `opened`", "today()", "never one of each"} {
		if !strings.Contains(lines, want) {
			t.Errorf("report does not say %q; it must name the subtraction and the reason it is the only reading:\n%s", want, lines)
		}
	}
}

// TestDateSubtraction_FiresOnExactlyTheOneFormulaTheVaultJustifies is the
// answer to the header's old objection (b) — "it would recover nothing here" —
// and it is the falsifiable half of this file.
//
// THE MEASUREMENT. Three of the founder's 18 base files subtract a BARE
// property from `today()`. All three name `created`, and the three outcomes
// are different for three different reasons:
//
//	Deals.age           `(today() - created).days`, view type `deal`.
//	                    NOT recovered. Hundreds of real ISO values already
//	                    typed `deal.created` as `date` — clause 2 (a value
//	                    exists) and clause 3 (it is no longer `text`) both
//	                    refuse it, and there was nothing to recover anyway.
//	Inbox-Triage.age    `(today() - created).days`, in an UNTYPED view.
//	                    NOT recovered. No record type owns the name, so the
//	                    declaration cannot be attributed to one schema.
//	Hiring.age_in_stage `if(updated, …, (today() - created).days)`, view type
//	                    `candidate`. RECOVERED. `candidate` is declared from
//	                    Hiring.base and the operator's own template, carries no
//	                    notes at all, so `candidate.created` is a declared,
//	                    value-less `text` property — through clause 1, not
//	                    refused by it.
//
// A rule that got greedier fails this test on the count, and it fails naming
// which of the three it wrongly took. A rule that stopped firing fails it on
// candidate.created being absent. Both are the directions worth catching.
//
// It also asserts the thing FR-105 actually cares about, which the count does
// not: the promotion must not INVALIDATE a note. `candidate` has no notes, so
// the honest expectation is zero — and this test states that expectation
// rather than leaving it to a neighbouring file.
func TestDateSubtraction_FiresOnExactlyTheOneFormulaTheVaultJustifies(t *testing.T) {
	root := fixtureVaultCopy(t)
	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	// Every promotion this rule made through the SUBTRACTION shape, keyed
	// `type.property` -> the base files that carried it.
	bySubtraction := map[string][]string{}
	for _, fe := range rep.FormulaEvidenced {
		for _, ev := range fe.Evidence {
			if dateSubtrahendFunctions[ev.Function] {
				bySubtraction[fe.RecordType+"."+fe.Property] = append(
					bySubtraction[fe.RecordType+"."+fe.Property], ev.Base)
			}
		}
	}

	if len(bySubtraction) != 1 {
		t.Fatalf("the subtraction shape promoted %d propert(ies): %v — the founder's vault justifies exactly one (candidate.created). More means a containment clause stopped holding; fewer means the rule stopped firing and objection (b) is true again.",
			len(bySubtraction), bySubtraction)
	}
	bases, got := bySubtraction["candidate.created"]
	if !got {
		t.Fatalf("the one promotion is %v, want candidate.created", bySubtraction)
	}
	if len(bases) != 1 || !strings.Contains(bases[0], "Hiring.base") {
		t.Errorf("candidate.created cites %v as its evidence; only Hiring.base names it in a view that resolves to `candidate`", bases)
	}

	// The two that must NOT be recovered, named individually so a failure says
	// which containment clause stopped holding.
	for _, forbidden := range []string{"deal.created", "inbox.created", "note.created"} {
		if _, taken := bySubtraction[forbidden]; taken {
			t.Errorf("%s was promoted by a subtraction; it is either already typed from real values or named only by an untyped view, and both are refusals this rule depends on",
				forbidden)
		}
	}

	// AND THE INVALIDATION DIRECTION, WHICH THIS TEST DELIBERATELY DOES NOT
	// ASSERT ON THIS VAULT.
	//
	// The promotion is STRICTER than what it replaced (`date` accepts six ISO
	// layouts, `text` accepts every string), so the only way it can cost
	// anything is by rejecting a note of the promoted type. The one type it
	// promoted, `candidate`, has NO NOTES AT ALL — so "no candidate note was
	// invalidated" is true here however broken the rule is, and asserting it
	// would be a green that could not have gone red. The vault's own count is
	// no better: it stands at 27 invalid records for reasons that predate this
	// rule and have nothing to do with it, so an equality against it would
	// measure a peer's work rather than this one's.
	//
	// The guarantee is proved in two places that CAN fail instead. Clause 2's
	// subtest above forces a real value under a promoted name and requires the
	// promotion not to happen and the note to stay valid; and
	// TestFixtureVault_TheInvalidationCounterCanActuallyFire proves on this
	// same vault that the invalid-record counter is an instrument that reports
	// a forced invalidation rather than a zero nobody tested.
	if rep.Validation.InvalidRecords != 0 {
		t.Logf("NOT ASSERTED (would be unfalsifiable here): the run reports %d invalid record(s) over %d valid; none is of type `candidate`, which has no notes. See the comment above for where the invalidation guarantee is actually tested.",
			rep.Validation.InvalidRecords, rep.Validation.ValidRecords)
	}
}
