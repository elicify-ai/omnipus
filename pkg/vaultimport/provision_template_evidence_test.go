// Omnipus — FR-018d provisioning composed with the operator's own
// `type: template` notes: a template settles EXISTENCE, and existence is a
// different claim from any use a `.base` file makes of the same name.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"strings"
	"testing"
)

// invoiceTemplate is the shape the founder's own `Template — invoice.md` has:
// a `type: template` note whose `template_type` names the record it templates
// and whose remaining keys are that record's properties, left blank.
const invoiceTemplate = "---\ntype: template\ntemplate_type: invoice\ntemplate_kind: record\n" +
	"client:\namount:\ndue_date:\n---\n\nbody\n"

// arBase sums `amount` and displays it — the exact pair that cost three column
// losses: the summary is TYPE evidence the provisioner cannot act on, and it
// was allowed to suppress the DECLARATION, which the column needed.
const arBase = `
filters:
  and:
    - type == "invoice"
views:
  - type: table
    name: Aging
    order:
      - client
      - amount
    summaries:
      amount: Sum
`

// TestTemplateEvidence_RestoresTheColumnAndStillDropsTheSummary is the whole
// composition in one assertion set, and all four parts have to hold together —
// any three of them are satisfiable by a change that is wrong.
func TestTemplateEvidence_RestoresTheColumnAndStillDropsTheSummary(t *testing.T) {
	_, rep := provisionVault(t, map[string]string{
		"notes/anchor.md": anchorNote,
		"03-Reference/Templates/Tpl — invoice.md": invoiceTemplate,
		"bases/AR.base": arBase,
	})

	p := findProvisioned(t, rep, "invoice")

	// 1. The property is DECLARED. The operator wrote it down in his own hand;
	//    nothing about a summary contradicts that it exists.
	if !contains(p.Properties, "amount") {
		t.Errorf("`amount` is still undeclared although the operator's own template lists it. Properties=%v Omitted=%+v", p.Properties, p.Omitted)
	}
	// 2. It is declared DECIMAL, from the base file's own `Sum`.
	//
	//    THIS ASSERTION USED TO REQUIRE `text`, on the argument that a guessed
	//    numeric type "would REJECT the first invoice note written in the
	//    founder's demonstrated house style". That argument was sound when it
	//    was written and is not sound now, for a reason that did not exist
	//    then: TypePropertiesFromBaseSummaries reads the summary ONLY through
	//    typeEligibleForFormulaEvidence, whose clause 2 is "data beats a base
	//    file". The rejection the old assertion protected against cannot
	//    happen, because the note that would be rejected is itself the
	//    evidence that silences the rule — see
	//    TestSummaryEvidence_TheFoundersPlaceholderSilencesTheRule below,
	//    which asserts exactly that and is the reason this line was safe to
	//    change.
	//
	//    The house style is real and was verified against the founder's vault
	//    before this was touched: he writes `PLACEHOLDER — cost unknown` into
	//    money fields, eight times and counting. Every one of them is on
	//    `cost`, a prose field that carries its own currency inline
	//    ("PLACEHOLDER — usage-based; US$20.60 (Apr)"). `cost` keeps its text
	//    type under this rule because notes carry values for it. `amount` and
	//    `target` are the other shape — each paired with a SEPARATE
	//    `currency:` property in the founder's own templates, which is his
	//    schema saying the amount holds a bare number and the unit lives
	//    elsewhere.
	if got := declaredTypeFromBaseEvidence(rep, "invoice", "amount"); got != "decimal" {
		t.Errorf("the run declared `amount` as %q, not decimal — the operator's own base file totals this property, and `sum` is defined over numbers and nothing else", got)
	}
	// 3. The template is CREDITED. A declaration whose provenance is reported
	//    as a base file's is a declaration the operator cannot audit.
	if len(p.Templates) == 0 || !anyContains(p.Templates, "Tpl — invoice.md") {
		t.Errorf("the template that donated the property is not named in the provenance: Templates=%v", p.Templates)
	}
	if !anyContains(p.ReportLines(), "Tpl — invoice.md") {
		t.Errorf("the schema file's own header does not say a template spoke for these names: %v", p.ReportLines())
	}
	// 4. The summary is KEPT, because the property is now the type `sum` is
	//    defined over. This is the whole point of the change: the total was
	//    being dropped from three of the founder's views on the strength of
	//    this package declining to read a statement he had already written.
	v := findView(t, rep, "Aging")
	if anyContains(v.Losses, "sum(amount)") {
		t.Errorf("sum(amount) is still dropped although the property is now decimal. Losses=%v", v.Losses)
	}
	if anyContains(v.Losses, "column \"amount\"") {
		t.Errorf("the COLUMN is still reported lost after the template declared the property: %v", v.Losses)
	}
	if v.Disabled {
		t.Errorf("restoring a column disabled the view; an aggregate loss is an annotation and decides no rows. DisablingLosses=%v", v.DisablingLosses)
	}
}

// ---------------------------------------------------------------------------
// THE ASYMMETRY, WHICH IS THE SAFETY ARGUMENT
// ---------------------------------------------------------------------------

// TestTemplateEvidence_DoesNotLiftARowSetOmission is the FR-105 half. A
// template says the property EXISTS. It says nothing about whether a lexical
// text comparison means what the operator's `>` meant, and `50 > 100` answers
// TRUE on text — MORE rows than Obsidian returns.
//
// Both unsafe shapes are covered, and the ORDER of the two views is varied,
// because the omission that gets recorded first used to be the omission that
// stuck: a summary in the first view could claim the slot and a broadening
// comparison in the second would then find the property already omitted, say
// nothing, and be lifted by the template along with the summary.
func TestTemplateEvidence_DoesNotLiftARowSetOmission(t *testing.T) {
	cases := []struct {
		name         string
		clause       string
		summaryFirst bool
	}{
		{"ordering_comparison_after_summary", `amount > 100`, true},
		{"ordering_comparison_before_summary", `amount > 100`, false},
		{"contains_after_summary", `amount.contains("50")`, true},
		{"contains_before_summary", `amount.contains("50")`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			summaryView := `
  - type: table
    name: Aging
    order:
      - amount
    summaries:
      amount: Sum
`
			clauseView := `
  - type: table
    name: Large
    filters:
      and:
        - ` + tc.clause + `
`
			views := clauseView + summaryView
			if tc.summaryFirst {
				views = summaryView + clauseView
			}
			_, rep := provisionVault(t, map[string]string{
				"notes/anchor.md": anchorNote,
				"03-Reference/Templates/Tpl — invoice.md": invoiceTemplate,
				"bases/AR.base": `
filters:
  and:
    - type == "invoice"
views:` + views,
			})

			p := findProvisioned(t, rep, "invoice")
			if contains(p.Properties, "amount") {
				t.Fatalf("`amount` was declared text although a base clause over it would return MORE rows than the Obsidian original (FR-105). The template settles existence, never a comparison. Properties=%v Omitted=%+v",
					p.Properties, p.Omitted)
			}
			var reason string
			for _, o := range p.Omitted {
				if o.Property == "amount" {
					reason = o.Reason
				}
			}
			if reason == "" {
				t.Fatalf("`amount` is undeclared with no reason recorded: Omitted=%+v", p.Omitted)
			}
			if omissionLiftedByTemplateEvidence(reason) {
				t.Errorf("the recorded reason is the LIFTABLE one, so the property survived only by luck of ordering: %q", reason)
			}
			if v := findView(t, rep, "Large"); !v.Disabled {
				t.Errorf("the view carrying the untranslatable clause is ENABLED; a dropped conjunct broadens. Losses=%v", v.Losses)
			}
		})
	}
}

// TestTemplateEvidence_ADonationWithNoTemplateChangesNothing is the control.
// Without a template, the numeric-summary omission must still stand — the
// composition adds a new source of evidence, it does not weaken the rule that
// applies when there is none.
func TestTemplateEvidence_ADonationWithNoTemplateChangesNothing(t *testing.T) {
	_, rep := provisionVault(t, map[string]string{
		"notes/anchor.md": anchorNote,
		"bases/AR.base":   arBase,
	})
	p := findProvisioned(t, rep, "invoice")
	if contains(p.Properties, "amount") {
		t.Errorf("`amount` was declared with no template behind it — the base's Sum is the only evidence and it is evidence about TYPE, not existence. Properties=%v", p.Properties)
	}
	if len(p.Templates) != 0 {
		t.Errorf("Templates=%v on a vault holding no template", p.Templates)
	}
}

// TestTemplateEvidence_ATemplateForAnObservedTypeIsInferGoSJob keeps the two
// passes' territories apart. Once a real note carries the type, provisioning
// declines the type entirely and infer.go's applyTemplateDeclarations is what
// reads the template. Two passes both acting would declare a property twice
// with two different type decisions behind it.
func TestTemplateEvidence_ATemplateForAnObservedTypeIsInferGoSJob(t *testing.T) {
	_, rep := provisionVault(t, map[string]string{
		"notes/anchor.md": anchorNote,
		"notes/inv-1.md":  "---\ntype: invoice\nclient: Acme\n---\n",
		"03-Reference/Templates/Tpl — invoice.md": invoiceTemplate,
		"bases/AR.base": arBase,
	})
	for _, p := range rep.Provisioned {
		if p.Type == "invoice" {
			t.Fatalf("`invoice` was PROVISIONED although a real note carries it; observation always wins. %+v", p)
		}
	}
}

// TestTemplateEvidence_SentinelIsStillWrittenByTheAggregateBranch pins the one
// coupling this design has: the aggregate branch's reason text carries the
// substring recordTemplate keys its lift off. If somebody rewords that sentence
// without the substring, template evidence silently stops lifting anything and
// three column losses come back with no test failing anywhere else.
func TestTemplateEvidence_SentinelIsStillWrittenByTheAggregateBranch(t *testing.T) {
	uses := provisionUsesFromDisplay(map[string]any{
		"summaries": map[string]any{"amount": "Sum"},
	})
	if len(uses) != 1 {
		t.Fatalf("uses = %+v, want exactly one", uses)
	}
	if uses[0].unsafeReason == "" {
		t.Fatal("a numeric summary recorded no unsafe reason at all")
	}
	if !strings.Contains(uses[0].unsafeReason, aggregateOmissionSentinel) {
		t.Fatalf("the aggregate branch no longer writes %q, so omissionLiftedByTemplateEvidence can never fire: %q",
			aggregateOmissionSentinel, uses[0].unsafeReason)
	}
	if !omissionLiftedByTemplateEvidence(uses[0].unsafeReason) {
		t.Fatal("the sentinel is present and the predicate still says the omission is not liftable")
	}
	// And the reason must no longer claim the engine computes anything: it
	// refuses, by name, and it refuses the whole request.
	if strings.Contains(uses[0].unsafeReason, "silently computes") {
		t.Errorf("the reason still says the engine silently computes nonsense; it refuses loudly and in full: %q", uses[0].unsafeReason)
	}
}

// ---------------------------------------------------------------------------
// THE CONTAINMENT THAT MADE ASSERTION 2 SAFE TO INVERT
// ---------------------------------------------------------------------------

// TestSummaryEvidence_TheFoundersPlaceholderSilencesTheRule is the guard the
// old `text` assertion was really asking for, stated as the thing that
// actually matters: the founder's own writing must always beat a base file.
//
// He writes `PLACEHOLDER — amount unknown` into money fields. If a base's
// `Sum` could type a property numeric ANYWAY, that note would be invalid under
// a schema derived from his own vault — which is the harm the previous
// assertion named, and it is a real harm. It does not happen, and this test is
// why anyone may believe that: the moment one note carries a value, the base
// file stops being the only voice and stops being heard.
//
// It is deliberately asserted on the FOUNDER'S ACTUAL TEXT rather than a
// convenient non-numeric string. A rule that admitted "PLACEHOLDER" but choked
// on his real "PLACEHOLDER — amount unknown" would pass a tidier test and fail
// on his vault.
func TestSummaryEvidence_TheFoundersPlaceholderSilencesTheRule(t *testing.T) {
	// SEVEN DISTINCT VALUES, ALL WRITTEN ONCE, and that count is load-bearing
	// rather than padding. Inference reads a small repeated vocabulary as an
	// ENUM, and an enum is not text, so clause 3 ("only ever strengthens the
	// fallback") would refuse the promotion on its own and this test would
	// pass with clause 2 deleted — which is exactly what an earlier draft of
	// it did. Seven distinct values used once each clears the enum rule and
	// lands on text, which is the one state in which clause 2 is the only
	// thing standing between the base file's `Sum` and a numeric declaration.
	//
	// The values are the founder's own, copied from his vault: he really does
	// write prose into money fields, and a rule that admitted a tidy
	// "PLACEHOLDER" but choked on "PLACEHOLDER — usage-based; US$20.60 (Apr)"
	// would pass a neater test and fail on his data.
	amounts := []string{
		"PLACEHOLDER — amount unknown",
		"PLACEHOLDER — cost unknown",
		"PLACEHOLDER — usage-based; US$20.60 (Apr), US$0.185 (May)",
		"PLACEHOLDER — no paid signal; free tier or trial",
		"PLACEHOLDER — free allowance; charges only if overage",
		"PLACEHOLDER — annual cost unknown; unreconciled bank charges",
		"PLACEHOLDER — amount not stated by founder; no receipt read",
	}
	files := map[string]string{
		"notes/anchor.md": anchorNote,
		"03-Reference/Templates/Tpl — invoice.md": invoiceTemplate,
		"bases/AR.base": arBase,
	}
	for i, amt := range amounts {
		files[fmt.Sprintf("notes/inv%d.md", i)] = fmt.Sprintf(
			"---\ntype: invoice\nclient: C%d\namount: %s\n---\n\nbody\n", i, amt)
	}
	_, rep := provisionVault(t, files)

	// 1. The rule must not have fired: the founder's own notes contradict it.
	if got := declaredTypeFromBaseEvidence(rep, "invoice", "amount"); got != "" {
		t.Fatalf("a base file's `Sum` typed `amount` as %q although notes of that type carry the founder's placeholder prose — those notes are now invalid under a schema derived from the vault that contains them",
			got)
	}
	// 2. The consequence is the SAFE one: an annotation is lost, no rows are.
	v := findView(t, rep, "Aging")
	if !anyContains(v.Losses, "sum(amount)") {
		t.Errorf("the summary was carried over a non-numeric property; knowledge_find then refuses the whole request. Losses=%v", v.Losses)
	}
	// 3. THE PRECONDITION, ASSERTED LAST because it is only meaningful once 1
	//    has held: `amount` has to have landed on TEXT for clause 2 to be the
	//    clause that refused. If inference read these seven values as an enum
	//    instead, clause 3 would have refused on its own, assertion 1 would
	//    pass with clause 2 deleted, and this test would be guarding nothing.
	//    An earlier draft did exactly that — it passed with the protection
	//    disabled — so the type is now read out of the run's own refusal
	//    sentence rather than assumed.
	if !anyContains(v.Losses, "amount is a text property") {
		t.Fatalf("PRECONDITION FAILED: the run does not describe `amount` as text, so clause 2 is not the clause under test and this test proves nothing about it. Losses=%v", v.Losses)
	}
	if v.Disabled {
		t.Errorf("a dropped total disabled the view; an aggregate decides no rows. DisablingLosses=%v", v.DisablingLosses)
	}
}

// declaredTypeFromBaseEvidence returns the type this run decided for
// recordType.property FROM A BASE FILE, and "" when no base-file rule spoke
// for it.
//
// IT READS THE RUN'S OWN REPORTED DECISION rather than the
// provisionedPropertyType constant. The assertions here used to compare that
// CONSTANT against a literal, which is a comparison between two things neither
// of which the import can change — it held whatever the rule did, and it held
// after the rule was inverted. A test that cannot fail is not a guard, and
// this one guarded the founder's money fields.
func declaredTypeFromBaseEvidence(rep *Report, recordType, property string) string {
	for _, f := range rep.FormulaEvidenced {
		if f.RecordType == recordType && f.Property == property {
			return string(f.Type)
		}
	}
	return ""
}
