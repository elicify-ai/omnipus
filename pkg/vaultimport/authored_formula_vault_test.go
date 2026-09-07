// Omnipus — the two views the last DISABLED pair was blocked on, measured on
// the founder's own vault: one SERVED through knowledge_find, one still
// refused, and both graded honestly about what the grading could have caught.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package vaultimport

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
)

// ---------------------------------------------------------------------------
// ENABLED IS NOT USABLE, AND A ZERO-ROW ORACLE IS NOT A GRADE
//
// Two failures have already happened on this vault and both are guarded here.
//
// (1) A view imported ENABLED and was REFUSED the first time anyone served it,
//     with the report saying nothing. So this file does not check a flag: it
//     serves the view through knowledgefind.Find — the real engine, the real
//     namespace resolution, the real formula evaluator, the real comparator —
//     with the clock pinned to the instant the committed oracle was derived
//     against.
//
// (2) A grade was reported as an exact verdict on a view whose whole population
//     was already inside the expected set, so no filter could have failed it.
//     "Closing This Month" is EXACTLY that case and this file says so out loud
//     rather than counting it: the founder's vault holds ONE note of type
//     `deal` and its `close_date` is absent, so the first clause —
//     `close_date != ""`, which this change did not touch — empties the view on
//     its own. The two authored formulas cannot change a row here.
//
//     That is declared as an ASSERTION, not a log line, so the day a deal with
//     a close date is added the declaration fails and somebody is told to grade
//     it for real. The falsifiable measurement of the authored formulas lives
//     in authored_formula_clock_test.go, on a fixture built so that each clause
//     decides a row.
// ---------------------------------------------------------------------------

// TestFixtureVault_ClosingThisMonthImportsEnabledAndSERVES is the whole claim
// for Deals.base's second view, end to end.
func TestFixtureVault_ClosingThisMonthImportsEnabledAndSERVES(t *testing.T) {
	_, rep, s := w4Serve(t)

	vo := authoredViewOutcome(t, rep, "Deals.base", "Closing This Month")
	if vo.Disabled {
		t.Fatalf("the view is still DISABLED; losses: %v", vo.Losses)
	}
	if len(vo.Losses) != 0 {
		t.Errorf("the view imports with losses although every clause was carried: %v", vo.Losses)
	}
	if len(vo.AuthoredFormulas) != 2 {
		t.Fatalf("the outcome names %d authored formulas, want 2 (one per expression clause): %v",
			len(vo.AuthoredFormulas), vo.AuthoredFormulas)
	}

	slug := strings.TrimSuffix(filepath.Base(vo.OutputRelPath), ".yaml")

	// ENABLED IS NOT USABLE. Serve it.
	limit := 200
	resp, err := knowledgefind.Find(context.Background(), s.deps, generated.VaultFindRequest{View: &slug, Limit: &limit})
	if err != nil || resp.Refused {
		detail := ""
		if err != nil {
			detail = err.Error()
		}
		for _, pb := range resp.Problems {
			detail += " | " + pb.Reason
		}
		t.Fatalf("the view imported ENABLED and knowledge_find REFUSES it — the exact silent failure this vault has already produced twice: %s", detail)
	}

	got := s.rows(t, w4Request(slug))
	want, known := w4OracleRows(t, "Deals.base", "Closing This Month")
	if !known {
		t.Fatal("the oracle does not cover \"Closing This Month\" — an ungraded view is where a broadening hides")
	}
	if extra := fr105MissingFrom(want, got); len(extra) > 0 {
		t.Errorf("FR-105 BROADENING: the imported view returns %d row(s) the Obsidian original does not: %v", len(extra), extra)
	}
	if missing := fr105MissingFrom(got, want); len(missing) > 0 {
		t.Logf("NARROWING (allowed by FR-105, recorded anyway): the Obsidian original returns %d row(s) the import does not: %v", len(missing), missing)
	}

	// THE GRADER'S OWN POWER, measured. Everything but the record type.
	broad := s.rows(t, w4BroadRequestFor(t, s, slug))
	t.Logf("GRADED Deals.base \"Closing This Month\": oracle=%d imported=%d unfiltered=%d", len(want), len(got), len(broad))
}

// TestFixtureVault_ClosingThisMonthIsUNFALSIFIABLEOnThisVault records, as an
// assertion, why the grade above must not be counted as evidence that the
// authored formulas are right.
//
// The declaration is the deal population and its close dates. If either
// changes, this fails and names what to do about it.
func TestFixtureVault_ClosingThisMonthIsUNFALSIFIABLEOnThisVault(t *testing.T) {
	root, rep, s := w4Serve(t)

	vo := authoredViewOutcome(t, rep, "Deals.base", "Closing This Month")
	slug := strings.TrimSuffix(filepath.Base(vo.OutputRelPath), ".yaml")

	deals := authoredDealCloseDates(t, root)
	withDate := 0
	for _, d := range deals {
		if d != "" {
			withDate++
		}
	}
	if len(deals) != 1 || withDate != 0 {
		t.Fatalf("this vault now holds %d note(s) of type `deal`, %d of them with a close date (%v).\n"+
			"This test DECLARED that the population was one dateless deal, which is why the grade in "+
			"TestFixtureVault_ClosingThisMonthImportsEnabledAndSERVES could not falsify the two authored formulas. "+
			"That is no longer true: derive the expected rows by hand at the oracle's clock (2026-08-24), add them to the "+
			"acceptance oracle, and grade this view for real.", len(deals), withDate, deals)
	}

	// The strip test, spelled out: the view's OWN first clause already empties
	// it, so the authored clauses decide nothing here whatever they say.
	got := s.rows(t, w4Request(slug))
	broad := s.rows(t, w4BroadRequestFor(t, s, slug))
	if len(got) != 0 {
		t.Errorf("the view returned %v; with one dateless deal in the vault `close_date != \"\"` must empty it", got)
	}
	t.Logf("UNFALSIFIABLE — not counted: Deals.base \"Closing This Month\" filtered=%d unfiltered=%d. "+
		"The vault holds one `deal` note and it carries no `close_date`, so the clause this change did NOT touch "+
		"(`close_date != \"\"`) empties the view on its own and the two authored formulas cannot move a row. "+
		"The falsifiable measurement is TestAuthoredFormula_EachAuthoredClauseActuallyDecidesRows.",
		len(got), len(broad))
}

// ---------------------------------------------------------------------------
// PART 1 — `owner == "Daniel Piatkowski"`, checked against the vault rather
// than reasoned about.
//
// THE QUESTION. Obsidian compares the property's own text; our engine compares
// a relation by RESOLVED TARGET IDENTITY (§8 R-8), so `owner = "[[Daniel
// Piatkowski]]"` matches every spelling that resolves to the same note —
// `[[People/Daniel Piatkowski]]`, `[[Daniel Piatkowski|Danny]]`, a differently
// cased path. If the vault holds a second spelling, the identity reading is the
// LARGER set and wrapping the bare name in brackets is the broadening FR-105
// forbids. If it holds none, the two readings pick the same notes.
//
// THE ANSWER FOR THIS VAULT, measured below: they pick the same notes. Every
// `owner` value on a `task` note is a wikilink; exactly one spelling names
// Daniel; and `[[Daniel Piatkowski]]` resolves to exactly one note.
//
// AND THE VIEW STAYS DISABLED ANYWAY, which is the part worth reading. The
// importer decides that clause in view_write.go, where it has the inferred
// SCHEMA and nothing else — no note values, no wikilink spellings, no note
// index. The fact this test measures is not available at the point of decision,
// so writing the bracketed literal there would be a guess that happens to be
// right on this vault, and whose failure mode on another is the forbidden
// direction. Closing it properly needs the evidence carried from the scan to
// the translator, which is a change to files this work does not own
// (`infer.go`'s InferredProperty and the SchemaIndex built from it).
//
// This test is what makes that a decision rather than a shrug: it states the
// measurement, and it FAILS the day the vault gains a second spelling — which
// would mean the wrap was never safe even here.
// ---------------------------------------------------------------------------

// TestFixtureVault_OwnerRelationTextAndIdentityPickTheSameNotes is the
// measurement, taken through the real engine on one side and the notes' own raw
// frontmatter on the other.
func TestFixtureVault_OwnerRelationTextAndIdentityPickTheSameNotes(t *testing.T) {
	root, _, s := w4Serve(t)

	const target = "Daniel Piatkowski"
	const property = "owner"

	// SIDE A — OUR ENGINE. Serve `type: task` with the identity comparison the
	// importer would have to write, through knowledge_find itself.
	limit := 200
	recordType := "task"
	op := generated.Equal
	literal := "[[" + target + "]]"
	prop := property
	identity := s.rows(t, generated.VaultFindRequest{
		Type:   &recordType,
		Limit:  &limit,
		Filter: &generated.VaultFilterNode{Property: &prop, Op: &op, Value: &literal},
	})

	// SIDE B — OBSIDIAN'S READING, computed from the notes' own raw text. A
	// relation's TypedValue.Raw is documented as "the source text exactly as
	// the file had it", and ParseWikilink is the product's own reader of that
	// text — so this side quotes the file rather than re-implementing a parser.
	text := authoredOwnerTextMatches(t, root, property, target)

	if extra := fr105MissingFrom(text, identity); len(extra) > 0 {
		t.Errorf("FR-105: the IDENTITY reading matches %d note(s) the TEXT reading does not: %v.\n"+
			"That is the broadening direction, and it means wrapping the bare name in brackets is NOT a faithful "+
			"translation of `%s == %q` on this vault.", len(extra), extra, property, target)
	}
	if missing := fr105MissingFrom(identity, text); len(missing) > 0 {
		t.Logf("NARROWING (allowed, recorded): the TEXT reading matches %d note(s) the identity reading does not: %v",
			len(missing), missing)
	}
	if len(identity) == 0 || len(text) == 0 {
		t.Fatalf("one side matched nothing (identity=%d text=%d), so the comparison above proves nothing",
			len(identity), len(text))
	}
	t.Logf("MEASURED on the founder's vault: `%s == %q` selects %d task note(s) under Obsidian's text reading and "+
		"%d under our engine's resolved-identity reading, and they are the SAME notes. The wrap would be faithful HERE.",
		property, target, len(text), len(identity))
}

// TestFixtureVault_NeedsDanielStaysDisabledAndSaysWhy is the other half, and it
// is the one that keeps the outcome honest.
//
// The measurement above says the wrap would be faithful on this vault. The
// importer still may not make it, because the evidence it rests on is not
// visible where the decision is taken. So the view stays disabled with its
// named loss, and this asserts that the loss still names the property — an
// operator who cannot see WHICH clause went cannot act on it.
func TestFixtureVault_NeedsDanielStaysDisabledAndSaysWhy(t *testing.T) {
	_, rep, _ := w4Serve(t)

	vo := authoredViewOutcome(t, rep, "Tasks.base", "Needs Daniel")
	if !vo.Disabled {
		t.Fatalf("\"Needs Daniel\" imported ENABLED. If that was deliberate, the evidence for it must be carried from "+
			"the vault scan to the translator, not assumed — see this file's header. losses: %v", vo.Losses)
	}
	joined := strings.Join(vo.Losses, " | ")
	if !strings.Contains(joined, "owner") {
		t.Errorf("the view is disabled and its loss does not name `owner`, so a reader cannot tell which clause went: %s", joined)
	}
	if !strings.Contains(joined, "not a wikilink") {
		t.Errorf("the loss no longer carries the engine's own reason for refusing the literal: %s", joined)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// authoredViewOutcome finds one view's outcome by base file and display name.
func authoredViewOutcome(t *testing.T, rep *Report, base, view string) ViewOutcome {
	t.Helper()
	for _, b := range rep.Bases {
		if filepath.Base(b.BaseRelPath) != base {
			continue
		}
		for _, v := range b.Views {
			if v.DisplayName == view {
				if v.OutputRelPath == "" {
					t.Fatalf("the import produced no file for %q of %s", view, base)
				}
				return v
			}
		}
	}
	t.Fatalf("the import produced no view named %q in %s", view, base)
	return ViewOutcome{}
}

// authoredDealCloseDates reads every `type: deal` note's close_date, by stem,
// through the product's own record parser.
func authoredDealCloseDates(t *testing.T, root string) map[string]string {
	t.Helper()
	inv, err := ScanVault(root)
	if err != nil {
		t.Fatalf("re-scanning the imported vault: %v", err)
	}
	out := map[string]string{}
	for _, abs := range inv.Notes {
		data, readErr := os.ReadFile(abs) //nolint:gosec // path from this run's own scan
		if readErr != nil {
			t.Fatalf("reading %s: %v", abs, readErr)
		}
		rec := records.ParseRecord(abs, data)
		if rec.TypeName() != "deal" {
			continue
		}
		stem := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
		out[stem] = authoredScalarText(rec, "close_date")
	}
	return out
}

// authoredScalarText reads one frontmatter property's RAW source text —
// records.Frontmatter's own node, not a re-parse — or "" when the note carries
// no scalar value for it.
func authoredScalarText(rec records.Record, name string) string {
	n, ok := rec.Frontmatter.Get(name)
	if !ok || n.Kind != records.KindScalar {
		return ""
	}
	return strings.TrimSpace(n.Text)
}

// authoredOwnerTextMatches returns every `task` note whose relation property
// spells the target EXACTLY as written — Obsidian's own reading, taken from the
// file's raw text rather than from anything that resolves a link.
func authoredOwnerTextMatches(t *testing.T, root, property, target string) []string {
	t.Helper()
	inv, err := ScanVault(root)
	if err != nil {
		t.Fatalf("re-scanning the imported vault: %v", err)
	}
	var out []string
	for _, abs := range inv.Notes {
		data, readErr := os.ReadFile(abs) //nolint:gosec // path from this run's own scan
		if readErr != nil {
			t.Fatalf("reading %s: %v", abs, readErr)
		}
		rec := records.ParseRecord(abs, data)
		if rec.TypeName() != "task" {
			continue
		}
		raw := authoredScalarText(rec, property)
		if raw == "" {
			continue
		}
		link, isLink := records.ParseWikilink(raw)
		switch {
		case !isLink:
			// A BARE NAME would match Obsidian's `==` and could never match
			// ours (ParseValue refuses a non-wikilink relation literal). It is
			// the narrowing direction, but it is also a spelling this
			// measurement's conclusion depends on not existing, so it is
			// surfaced rather than skipped.
			t.Logf("note %q holds a NON-LINK `%s` value %q", filepath.Base(abs), property, raw)
			if raw == target {
				out = append(out, strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs)))
			}
		case link.Target == target && link.Display == "" && link.Heading == "":
			out = append(out, strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs)))
		case link.Target != target && strings.HasSuffix(link.Target, "/"+target):
			// A path-qualified spelling of the SAME note. Obsidian's text
			// comparison does not match it; ours would. Its existence is
			// exactly what would make the wrap unfaithful, so it is named.
			t.Logf("note %q spells `%s` as %q — a path-qualified form of the same target",
				filepath.Base(abs), property, raw)
		case link.Target == target && link.Display != "":
			t.Logf("note %q spells `%s` as %q — an ALIASED form of the same target",
				filepath.Base(abs), property, raw)
		}
	}
	sort.Strings(out)
	return out
}
