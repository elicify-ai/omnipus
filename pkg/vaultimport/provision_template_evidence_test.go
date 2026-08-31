// Omnipus — FR-018d provisioning composed with the operator's own
// `type: template` notes: a template settles EXISTENCE, and existence is a
// different claim from any use a `.base` file makes of the same name.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
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
	// 2. It is declared TEXT, not a numeric type guessed from the summary. A
	//    guessed `decimal` would REJECT the first invoice note written in the
	//    founder's demonstrated house style; text can reject nothing.
	if provisionedPropertyType != "text" {
		t.Errorf("provisioning declares %q, not text — a type that validates shape can reject a note nobody has written yet", provisionedPropertyType)
	}
	// 3. The template is CREDITED. A declaration whose provenance is reported
	//    as a base file's is a declaration the operator cannot audit.
	if len(p.Templates) == 0 || !anyContains(p.Templates, "Tpl — invoice.md") {
		t.Errorf("the template that donated the property is not named in the provenance: Templates=%v", p.Templates)
	}
	if !anyContains(p.ReportLines(), "Tpl — invoice.md") {
		t.Errorf("the schema file's own header does not say a template spoke for these names: %v", p.ReportLines())
	}
	// 4. The summary is STILL DROPPED, because `sum` is not defined over text
	//    and a view carrying it is refused in full at query time.
	v := findView(t, rep, "Aging")
	if !anyContains(v.Losses, "sum(amount)") {
		t.Errorf("sum(amount) was carried over a text property; the whole find request is then refused. Losses=%v", v.Losses)
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
