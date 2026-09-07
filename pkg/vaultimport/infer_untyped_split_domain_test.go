// Omnipus — why the enum-widening rule stays silent on an UNTYPED view, and
// why widening it would not have saved Inbox-Triage's Triage Queue anyway.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// THE QUESTION THIS FILE ANSWERS
//
// `Inbox-Triage.base`'s only view is stored DISABLED on one clause:
//
//	status == "unfiled"
//
// The obvious reading is that the enum-widening rule failed. It admits a
// literal a base filters on but no note carries — `task.status` gained
// "doing", `content.status` gained "archived" and "published", four more
// besides — and "unfiled" is exactly that shape. So why not this one?
//
// TWO INDEPENDENT CAUSES, and the distinction decides whether there is a bug:
//
//	(A) THE VIEW IS UNTYPED. Inbox-Triage's filter is `status == "unfiled"`
//	    plus two folder exclusions. No `type == …` clause anywhere, so
//	    resolveViewType returns "". WidenEnumsFromBases skips it deliberately:
//	    an untyped view queries every note in scope, so its literal cannot be
//	    attributed to ONE vocabulary, and guessing which record type was meant
//	    would rewrite the enum of every type that happens to share the
//	    property name. The rule is not failing; it is declining, and the
//	    clause that makes it decline is the one TypePropertiesFromBaseFormulas
//	    states for the same reason.
//
//	(B) `status` IS A SPLIT DOMAIN. Two in-scope record types declare it
//	    differently — `area` as an enum, `bank-account` as text — and
//	    knowledge_find REFUSES to split one name across two domains in an
//	    untyped query. It refuses the whole request, not the clause.
//
// WHY THE INDEPENDENCE MATTERS MORE THAN EITHER CAUSE. Cause (A) is the one
// that looks fixable, and "just widen it anyway" is the tempting move. It
// would achieve nothing: (B) refuses the query before any enum membership is
// consulted, so a widened set would sit there unread while the view stayed
// exactly as unusable. Worse, widening from an untyped view means picking a
// record type out of the several that share the name, and every one of those
// picks is a vocabulary edit to a schema the operator never pointed at.
//
// So the view stays disabled, and that is the CORRECT outcome rather than an
// unfinished one. A view whose central property cannot mean one thing should
// not be made to mean something. TestEnumWideningOnUntypedView_WideningWouldNot…
// is the proof of the independence claim; without it, "widening would not
// help" is an assertion, and this package does not carry those.
//
// WHAT IS ACTUALLY OWED HERE IS A REPORT LINE, NOT A TRANSLATION. At the time
// of writing the run report files this loss under `UNCLASSIFIED` — its closed
// table of gap shapes has no row for "untyped view names a split-domain
// property". That is a real defect and it is in report.go, which this file
// does not own. It is recorded here so the next reader does not mistake the
// UNCLASSIFIED count for evidence that the disable is wrong.
//
// The engine half of the diagnosis — that knowledge_find really does refuse
// this exact clause, in these words — is proved on the founder's own vault by
// TestFixtureVault_DisabledViewsAreOnesTheEngineActuallyRefuses. This file is
// the INFERENCE half: what the widening rule did, and why.
// ---------------------------------------------------------------------------

// splitDomainVault builds the smallest vault with the founder's shape: two
// record types that both carry `status`, declared differently, and one base
// whose single view filters on a `status` literal no note carries.
//
// `viewTypeClause` is the whole variable. Empty makes the view UNTYPED, which
// is Inbox-Triage's shape; a `type == "…"` clause makes it typed, which is the
// control.
func splitDomainVault(viewTypeClause string) map[string]string {
	filters := "filters:\n  and:\n    - status == \"unfiled\"\n"
	if viewTypeClause != "" {
		filters = "filters:\n  and:\n    - " + viewTypeClause + "\n    - status == \"unfiled\"\n"
	}
	files := map[string]string{
		"06-Bases/Triage.base": filters +
			"views:\n  - type: table\n    name: Queue\n    order:\n      - file.name\n      - status\n",

		// `area` — a small closed set of repeated values, which inference
		// reads as an enum. "unfiled" is NOT among them: that is the whole
		// point, and it is what makes the widening question live.
		"notes/a1.md": "---\ntype: area\nstatus: active\n---\n\nbody\n",
		"notes/a2.md": "---\ntype: area\nstatus: active\n---\n\nbody\n",
		"notes/a3.md": "---\ntype: area\nstatus: ready\n---\n\nbody\n",
		"notes/a4.md": "---\ntype: area\nstatus: ready\n---\n\nbody\n",
	}
	ledgerNotes(files)
	return files
}

// ledgerNotes writes the second declaration of `status` — the one that makes
// the name ambiguous. It is `bank-account`'s role in the founder's vault: the
// same property name, declared TEXT rather than enum.
//
// THE VALUE COUNT IS LOAD-BEARING AND THE FIRST VERSION OF THIS FIXTURE GOT IT
// WRONG. Three distinct prose values is still under enumMaxDistinct=15, so
// inference read them as an ENUM — and enum-against-enum is NOT a split
// domain: the engine UNIONS the two value sets, because a value set is not a
// domain (that pair sits in engine_refusal_parity_test.go's table, measured
// against a real Find). The fixture disabled nothing and the test failed
// loudly, which is the only reason this comment exists. Past the cap the
// property is text and the two domains genuinely differ.
func ledgerNotes(into map[string]string) map[string]string {
	prose := []string{
		"reconciled through Q2, pending the auditor",
		"opened 2019, dormant since the SG move",
		"closed by the bank without notice",
		"frozen pending KYC refresh",
		"migrated from the old sort code",
		"joint account, second signatory unresponsive",
		"overdraft renegotiated in March",
		"statements only, no online access",
		"held for the property deposit",
		"awaiting the corporate resolution",
		"flagged by compliance, under review",
		"consolidated into the SGD operating account",
		"opened for the payroll run only",
		"legacy, kept for the standing order",
		"closed and reopened under a new number",
		"dormant, fees still accruing",
		"pending transfer to the new entity",
	}
	for i, v := range prose {
		into[fmt.Sprintf("notes/b%d.md", i+1)] = "---\ntype: ledger\nstatus: " + v + "\n---\n\nbody\n"
	}
	return into
}

// statusWidening returns the widening reported for `status` on any record
// type, or nil.
func statusWidenings(rep *Report) []EnumWidening {
	var out []EnumWidening
	for _, w := range rep.EnumWidenings {
		if w.Property == "status" {
			out = append(out, w)
		}
	}
	return out
}

// onlyView returns the single view outcome the fixture base produced.
func onlyView(t *testing.T, rep *Report) ViewOutcome {
	t.Helper()
	for _, b := range rep.Bases {
		if !strings.Contains(b.BaseRelPath, "Triage.base") {
			continue
		}
		if len(b.Views) != 1 {
			t.Fatalf("Triage.base produced %d views, want 1", len(b.Views))
		}
		return b.Views[0]
	}
	t.Fatalf("Triage.base produced no outcome at all; bases=%d", len(rep.Bases))
	return ViewOutcome{}
}

// TestEnumWideningOnUntypedView_DeclinesRatherThanFails is cause (A),
// stated so it can fail.
//
// The literal "unfiled" is admissible in every respect the widening rule cares
// about — `area.status` IS an enum, "unfiled" is NOT in it, and a base really
// does filter on it. The ONLY thing standing in the way is that the view
// declares no record type. So: assert nothing was widened, and then assert in
// the very next test that the identical vault with a `type ==` clause DOES
// widen. One without the other proves nothing — a rule that never widened
// anything would pass this test alone.
func TestEnumWideningOnUntypedView_DeclinesRatherThanFails(t *testing.T) {
	_, rep := formulaGateVault(t, splitDomainVault(""))

	if got := statusWidenings(rep); len(got) != 0 {
		t.Errorf("an UNTYPED view widened %v; its literal is not scoped to one record type, and picking one rewrites the vocabulary of a schema the operator never named", got)
	}

	v := onlyView(t, rep)
	if v.ResolvedType != "" {
		t.Fatalf("the view resolved to record type %q; this fixture has no `type ==` clause and the whole measurement depends on it being untyped", v.ResolvedType)
	}
	if !v.Disabled {
		t.Errorf("the view imported ENABLED with `status == \"unfiled\"` unresolved; the clause decides the row set, so applying it would answer a question the Obsidian original does not")
	}
	if !strings.Contains(strings.Join(v.DisablingLosses, "\n"), "status") {
		t.Errorf("the view is disabled but no disabling loss names `status`: %v", v.DisablingLosses)
	}
}

// TestEnumWideningOnUntypedView_ATypedViewOfTheSameVaultDoesWiden is the control that
// gives the test above its power.
//
// Same vault, same literal, same base — one `type == "area"` clause added. If
// this does not widen, the rule is broken for a reason that has nothing to do
// with untyped views and the previous test's green is worthless.
func TestEnumWideningOnUntypedView_ATypedViewOfTheSameVaultDoesWiden(t *testing.T) {
	_, rep := formulaGateVault(t, splitDomainVault(`type == "area"`))

	got := statusWidenings(rep)
	if len(got) != 1 {
		t.Fatalf("a TYPED view produced %d widening(s) for `status`, want 1 — if this is 0 the rule is not working at all and the untyped test above is measuring nothing: %+v", len(got), got)
	}
	w := got[0]
	if w.RecordType != "area" {
		t.Errorf("widened %s.status; the view names `area` and no other type may be touched", w.RecordType)
	}
	if w.Refused || len(w.Added) != 1 || w.Added[0] != "unfiled" {
		t.Errorf("widening added %v (refused=%v), want exactly [unfiled]", w.Added, w.Refused)
	}
	// And nothing happened to the OTHER type that shares the name.
	for _, other := range rep.EnumWidenings {
		if other.RecordType == "ledger" {
			t.Errorf("the typed view also touched ledger.%s — a `type == \"area\"` clause is a statement about `area` alone", other.Property)
		}
	}
}

// TestEnumWideningOnUntypedView_WideningWouldNotHaveSavedTheViewAnyway is the
// independence proof, and it is the reason the disable stands.
//
// The vault here is rigged so cause (A) CANNOT be the blocker: `area.status`
// already carries "unfiled" as an observed value, so there is nothing left to
// widen and the enum contains the literal the filter names. The view is still
// untyped, `status` is still declared two ways, and if the disable survives
// that, then the widening rule was never what stood between this view and
// working.
//
// A failure here is the interesting outcome: it would mean the split domain is
// NOT independently disabling, and the case for "report it better rather than
// translate it" would have to be re-argued.
func TestEnumWideningOnUntypedView_WideningWouldNotHaveSavedTheViewAnyway(t *testing.T) {
	files := splitDomainVault("")
	// "unfiled" is now an OBSERVED value of area.status, so the closed set
	// already contains it and the widening rule has no work to do.
	files["notes/a5.md"] = "---\ntype: area\nstatus: unfiled\n---\n\nbody\n"
	files["notes/a6.md"] = "---\ntype: area\nstatus: unfiled\n---\n\nbody\n"

	_, rep := formulaGateVault(t, files)

	if got := statusWidenings(rep); len(got) != 0 {
		t.Fatalf("something was still widened (%+v); this fixture exists to remove widening from the question entirely", got)
	}

	v := onlyView(t, rep)
	if !v.Disabled {
		t.Fatalf("with the literal already IN the enum, the view imported ENABLED — the split domain is then not independently disabling and this file's argument for leaving the view alone does not hold. Re-open the question rather than deleting this test.")
	}
	losses := strings.Join(v.DisablingLosses, "\n")
	if !strings.Contains(losses, "status") {
		t.Errorf("still disabled but no disabling loss names `status`: %v", v.DisablingLosses)
	}
	// The loss must say WHY, in terms an operator can act on: the two record
	// types and the two declarations. A bare "could not translate" would leave
	// him with a disabled view and no next step.
	for _, want := range []string{"area", "ledger"} {
		if !strings.Contains(losses, want) {
			t.Errorf("the disabling loss does not name the record type %q; the operator's remedy is to give the view a type or rename one of the two, and he cannot do either without being told which two:\n%s", want, losses)
		}
	}
	t.Logf("INDEPENDENT: literal present in the enum, view still disabled — %s", firstLine(losses))
}
