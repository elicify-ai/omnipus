// Omnipus — FR-104b's best-fit tie-break: when several inferred types all
// accept a note, which one gets written, and when the rule must decline.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHAT THESE TESTS ARE FOR
//
// The four-condition match rule is SAFE — it never types a note into a
// schema the note violates — and measured against the founder's real vault
// it was also useless: 27 untyped notes, 0 typed, 14 refused as ambiguous.
// The best-fit rule exists to decide the ambiguous ones, and the only thing
// that makes a guess acceptable is that it is REPORTED as one, with the
// numbers that produced it.
//
// So these tests hold three separate promises apart, because a suite that
// merged them would pass while any one of them died:
//
//	(a) a clear winner is WRITTEN,
//	(b) an unclear winner is NOT written and the report says by how little
//	    it lost,
//	(c) the winner is chosen ONLY from types conditions (1)-(4) already
//	    accepted — the tie-break can never promote a type into the set.
//
// The expected numbers below are computed from the RULE as stated in
// typeinfer.go's header ("the share of the frontmatter a type's notes
// actually fill in which this note carries"), by hand, in the comment on
// each case — never read off a run of the code.
// ---------------------------------------------------------------------------

// weighted builds one inferred type's declarations with explicit evidence
// weights: `name:count` sets ObservedCount, and a `!` suffix on the name
// marks the property required.
//
// It is a separate helper from props() rather than an extension of it
// because the two answer different questions — props() is for tests about
// SHAPE, where evidence is irrelevant and zero is the honest value.
func weighted(spec ...string) []InferredProperty {
	out := make([]InferredProperty, 0, len(spec))
	for _, s := range spec {
		name, countText, ok := strings.Cut(s, ":")
		if !ok {
			panic("weighted: spec must be name[!]:count, got " + s)
		}
		p := InferredProperty{Type: records.TypeText}
		if strings.HasSuffix(name, "!") {
			p.Name, p.Required = strings.TrimSuffix(name, "!"), true
		} else {
			p.Name = name
		}
		n := 0
		for _, r := range countText {
			if r < '0' || r > '9' {
				panic("weighted: count must be digits, got " + countText)
			}
			n = n*10 + int(r-'0')
		}
		p.ObservedCount = n
		out = append(out, p)
	}
	return out
}

// ---------------------------------------------------------------------------
// (a) A clear winner is written, and the report shows its working.
// ---------------------------------------------------------------------------

// TestBestFit_ClearWinnerIsWrittenAndTheRankingIsReported is the case the
// founder's 14 ambiguous notes are made of.
//
// Both types accept the note. Their evidence:
//
//	tight  declares p,q            filled 10 + 10          = 20
//	loose  declares p,q,r,s        filled 10 + 10 + 1 + 1  = 22
//
// The note carries {p, q}, worth 20 against either. So tight scores
// 20/20 = 100.0% and loose 20/22 = 90.9%: a 9.1-point lead, outside the
// 5-point margin, so `tight` is written.
func TestBestFit_ClearWinnerIsWrittenAndTheRankingIsReported(t *testing.T) {
	dir := t.TempDir()
	n := noteOnDisk(t, dir, "untyped.md", "---\np: one\nq: two\n---\n\nbody\n")

	rep := InferTypesForUntypedNotes([]NoteRecord{n}, map[string][]InferredProperty{
		"tight": weighted("p!:10", "q!:10"),
		"loose": weighted("p!:10", "q!:10", "r:1", "s:1"),
	}, true)

	if rep.Written != 1 || rep.Ambiguous != 0 {
		t.Fatalf("written=%d ambiguous=%d no-match=%d, want exactly one written — best fit had a 9.1-point lead to work with",
			rep.Written, rep.Ambiguous, rep.NoMatch)
	}
	out := outcomeFor(t, rep, "untyped.md")
	if out.Inferred != "tight" {
		t.Errorf("inferred %q, want \"tight\": the note fills 100%% of what tight's notes fill in and 90.9%% of loose's", out.Inferred)
	}
	if !out.TieBroken {
		t.Error("TieBroken is false on a type chosen from two candidates — the report would present a guess as a certainty")
	}
	if len(out.Candidates) != 2 {
		t.Errorf("candidates = %v, want both contenders recorded", out.Candidates)
	}

	// The ranking, scored and ordered.
	if len(out.Fit) != 2 {
		t.Fatalf("Fit = %v, want both candidates scored", out.Fit)
	}
	if out.Fit[0].Type != "tight" || out.Fit[1].Type != "loose" {
		t.Errorf("ranking = %v, want tight ahead of loose", out.Fit)
	}
	if got := out.Fit[0].Carried; got != 20 {
		t.Errorf("tight Carried = %d, want 20 (p=10 + q=10)", got)
	}
	if got := out.Fit[0].Total; got != 20 {
		t.Errorf("tight Total = %d, want 20 (it declares nothing else)", got)
	}
	if got := out.Fit[1].Total; got != 22 {
		t.Errorf("loose Total = %d, want 22 (10 + 10 + 1 + 1)", got)
	}
	if got := out.Fit[1].Percent(); got < 90.8 || got > 91.0 {
		t.Errorf("loose Percent() = %.2f, want 90.9 (20/22)", got)
	}

	// The founder-facing half. A guess he cannot audit is not the guess he
	// asked for: the line must name the winner, the runner-up, both scores,
	// and say that a choice was made at all.
	for _, want := range []string{"BEST FIT", "tight", "loose", "100.0%", "90.9%"} {
		if !strings.Contains(out.Reason, want) {
			t.Errorf("the reason is missing %q, so the guess cannot be checked in one line: %s", want, out.Reason)
		}
	}

	// And the note itself was actually edited, minimally.
	want := "---\ntype: tight\np: one\nq: two\n---\n\nbody\n"
	if got := readFile(t, n.AbsPath); got != want {
		t.Errorf("the write was not the minimal one-line insertion.\n got: %q\nwant: %q", got, want)
	}
}

// TestBestFit_PrevalenceBeatsPropertyCount is the test that pins WHICH rule
// this is, and it is the one that fails if anyone "simplifies" the score to
// the obvious thing.
//
// The obvious rule is "the note covers the largest share of the type's
// DECLARED PROPERTY LIST", i.e. prefer the type with fewest properties.
// Here that rule and this one give OPPOSITE answers:
//
//	skeletal declares p,q,r,s        (4 properties) filled 1 + 1 + 1 + 1 = 4
//	lived_in declares p,q,r,s,t,u,v  (7 properties) filled 50 + 50 + 1 + 1 + 1 + 1 + 1 = 105
//
// The note carries {p, q}.
//
//	by property COUNT:      skeletal 2/4 = 50.0%  beats  lived_in 2/7 = 28.6%
//	by EVIDENCE (this rule): skeletal 2/4 =  50.0%  loses to lived_in 100/105 = 95.2%
//
// The evidence answer is the right one, and the difference is not academic:
// `skeletal` is a type nobody fills in, so "the note covers half of it"
// means half of almost nothing. `lived_in` is a type whose notes reliably
// carry p and q and only occasionally anything else — which is exactly what
// this note looks like. On the founder's vault this is the difference
// between typing 12 generic notes `note` (correct) and typing them
// `reference`, a 48-property type whose extra 46 properties are one-offs.
func TestBestFit_PrevalenceBeatsPropertyCount(t *testing.T) {
	dir := t.TempDir()
	n := noteOnDisk(t, dir, "untyped.md", "---\np: one\nq: two\n---\n")

	rep := InferTypesForUntypedNotes([]NoteRecord{n}, map[string][]InferredProperty{
		"skeletal": weighted("p:1", "q:1", "r:1", "s:1"),
		"lived_in": weighted("p:50", "q:50", "r:1", "s:1", "t:1", "u:1", "v:1"),
	}, true)

	out := outcomeFor(t, rep, "untyped.md")
	if out.Inferred != "lived_in" {
		t.Fatalf("inferred %q, want \"lived_in\". The score is the share of the frontmatter a type's notes ACTUALLY FILL IN (lived_in 100/105 = 95.2%%, skeletal 2/4 = 50.0%%), not the share of its declared property LIST — by that reading skeletal would win 50%% to 28.6%%. Ranking: %v",
			out.Inferred, out.Fit)
	}
	if got := out.Fit[0]; got.Carried != 100 || got.Total != 105 {
		t.Errorf("lived_in scored %d/%d, want 100/105 (p=50 + q=50 carried, of 105 filled in total)", got.Carried, got.Total)
	}
	if got := out.Fit[1]; got.Carried != 2 || got.Total != 4 {
		t.Errorf("skeletal scored %d/%d, want 2/4", got.Carried, got.Total)
	}
}

// ---------------------------------------------------------------------------
// (b) An unclear winner is not written, and the refusal shows the margin.
// ---------------------------------------------------------------------------

// TestBestFit_InsideTheMarginRefusesAndSaysByHowLittle reproduces, in
// miniature, the case that put the margin in the rule at all:
// `content-production MOC.md` scores 67.2% against `note` and 66.7% against
// `moc`, and both print as "67%".
//
// Here:
//
//	alpha declares p,q,r     filled 10 + 10 + 1           = 21, carried 20 -> 95.238%
//	beta  declares p,q,s,t   filled 20 + 20 + 2 + 1       = 43, carried 40 -> 93.023%
//
// A 2.2-point lead. Under a bare "highest score wins" rule alpha would be
// written; under the 5-point margin nothing is, and that is the honest
// answer — nothing in this note's frontmatter really tells alpha and beta
// apart.
func TestBestFit_InsideTheMarginRefusesAndSaysByHowLittle(t *testing.T) {
	dir := t.TempDir()
	const src = "---\np: one\nq: two\n---\n"
	n := noteOnDisk(t, dir, "untyped.md", src)

	rep := InferTypesForUntypedNotes([]NoteRecord{n}, map[string][]InferredProperty{
		"alpha": weighted("p:10", "q:10", "r:1"),
		"beta":  weighted("p:20", "q:20", "s:2", "t:1"),
	}, true)

	if rep.Ambiguous != 1 || rep.Written != 0 {
		t.Fatalf("written=%d ambiguous=%d, want 0/1: alpha leads beta by only 2.2 points, inside the %d-point margin",
			rep.Written, rep.Ambiguous, bestFitMarginNum)
	}
	out := outcomeFor(t, rep, "untyped.md")
	if out.Inferred != "" {
		t.Errorf("a type (%q) was written despite the margin — this is the coin toss the margin exists to stop", out.Inferred)
	}
	if out.TieBroken {
		t.Error("TieBroken is true on a note where the tie was NOT broken")
	}
	if got := readFile(t, n.AbsPath); got != src {
		t.Errorf("the file of a refused note was modified.\n got: %q\nwant: %q", got, src)
	}

	// The refusal has to be as auditable as the guess would have been:
	// which types tied, and by how little. "It was ambiguous" is not a
	// finding the founder can act on; "95.2 against 93.0" is.
	if len(out.Fit) != 2 {
		t.Fatalf("Fit = %v, want both candidates scored even though neither won — the ranking IS the explanation", out.Fit)
	}
	for _, want := range []string{"alpha", "beta", "95.2%", "93.0%"} {
		if !strings.Contains(out.Reason, want) {
			t.Errorf("the refusal is missing %q, so the founder cannot see how close it was: %s", want, out.Reason)
		}
	}
}

// TestBestFit_ExactTieRefuses is the degenerate half of the same promise,
// and it is the one the founder's `00-Inbox/2026-07-25.md` hits: three
// types (concept, person, product) each declare exactly `up`, each of their
// notes fills it in, and all three score 100%. There is nothing to choose
// between them and the rule must say so rather than reach for a second
// criterion — the type with more notes, or the first one alphabetically.
func TestBestFit_ExactTieRefuses(t *testing.T) {
	dir := t.TempDir()
	n := noteOnDisk(t, dir, "untyped.md", "---\nup: \"[[Home]]\"\n---\n")

	rep := InferTypesForUntypedNotes([]NoteRecord{n}, map[string][]InferredProperty{
		"concept": weighted("up!:8"),
		"person":  weighted("up!:2"),
		"product": weighted("up!:8"),
	}, true)

	if rep.Ambiguous != 1 || rep.Written != 0 {
		t.Fatalf("written=%d ambiguous=%d, want 0/1 — three candidates all score 100%%", rep.Written, rep.Ambiguous)
	}
	out := outcomeFor(t, rep, "untyped.md")
	if out.Inferred != "" {
		t.Errorf("wrote %q on a three-way exact tie. Whatever broke it (note count? alphabetical order?) is a preference nothing in the note supports", out.Inferred)
	}
	for _, f := range out.Fit {
		if p := f.Percent(); p != 100 {
			t.Errorf("%s scored %.1f%%, want 100 — every one of these types is exactly what the note carries", f.Type, p)
		}
	}
}

// TestBestFit_UnweightedSchemasDeclineRatherThanGuess pins the direction the
// rule fails in when it has nothing to work with.
//
// A caller that builds InferredProperty values by hand (every unit test in
// this package that is about SHAPE, and any future caller that does not
// come from InferSchema) leaves ObservedCount zero. There is then no
// evidence at all, and the only two possible designs are "score everything
// zero and decline" or "fall back to some other preference". This asserts
// the first: an importer with no evidence must refuse, not invent a
// tiebreak.
func TestBestFit_UnweightedSchemasDeclineRatherThanGuess(t *testing.T) {
	dir := t.TempDir()
	n := noteOnDisk(t, dir, "untyped.md", "---\nname: Barbara\nemail: b@example.com\n---\n")

	rep := InferTypesForUntypedNotes([]NoteRecord{n}, map[string][]InferredProperty{
		// Deliberately DIFFERENT property counts, so a count-based
		// fallback would have something to prefer.
		"person":  props("name!", "email!"),
		"contact": props("name!", "email!", "phone", "address"),
	}, true)

	if rep.Ambiguous != 1 || rep.Written != 0 {
		t.Fatalf("written=%d ambiguous=%d, want 0/1: with no evidence weights there is nothing to rank on, and the rule must decline rather than substitute a different preference",
			rep.Written, rep.Ambiguous)
	}
	out := outcomeFor(t, rep, "untyped.md")
	if out.Inferred != "" {
		t.Errorf("wrote %q from schemas carrying no evidence at all", out.Inferred)
	}
}

// ---------------------------------------------------------------------------
// (c) The tie-break can never promote a type the match rule rejected.
// ---------------------------------------------------------------------------

// TestBestFit_NeverPromotesATypeConditionFourRejected is the guard on the
// hard invariant: the number of notes the importer TYPES and then itself
// reports INVALID must be zero.
//
// The shape here is the dangerous one. `perfect` would score 100% — the
// note carries exactly its two properties and its notes fill in nothing
// else — and it is exactly the type a ranking run over the wrong set would
// hand back. But `perfect` declares `status` as enum(active, done) and this
// note says `blocked`, so condition (4) removed it before ranking began.
//
// TWO other types are left on purpose. An earlier version of this test left
// only one, which meant chooseType returned on the single-candidate path
// and never reached the ranking at all: the mutation that ranks over every
// shape instead of over the accepted candidates SURVIVED it. With `roomy`
// (100/101 = 99.0%) and `baggy` (100/115 = 87.0%) both surviving condition
// (4), the ranking really runs, and a ranking taken over the wrong set puts
// `perfect` at the top of it and writes a note that fails the very schema
// this run produced.
func TestBestFit_NeverPromotesATypeConditionFourRejected(t *testing.T) {
	dir := t.TempDir()
	n := noteOnDisk(t, dir, "untyped.md", "---\nstatus: blocked\nnotes: hmm\n---\n")

	perfect := weighted("status!:50", "notes!:50")
	perfect[0].Type = records.TypeEnum
	perfect[0].EnumValues = []string{"active", "done"}

	roomy := weighted("status!:50", "notes!:50", "extra:1")
	baggy := weighted("status!:50", "notes!:50", "extra:15")

	rep := InferTypesForUntypedNotes([]NoteRecord{n}, map[string][]InferredProperty{
		"perfect": perfect,
		"roomy":   roomy,
		"baggy":   baggy,
	}, true)

	out := outcomeFor(t, rep, "untyped.md")
	if out.Inferred == "perfect" {
		t.Fatalf("wrote `type: perfect` on a note whose `status: blocked` that type does not permit. Condition (4) rejected it and the ranking put it back — this is exactly the self-invalidation the acceptance bar forbids. Reason: %s", out.Reason)
	}
	for _, f := range out.Fit {
		if f.Type == "perfect" {
			t.Errorf("`perfect` appears in the ranking (%v) though condition (4) rejected it — a report that ranks a type the note violates invites someone to pick it by hand", out.Fit)
		}
	}
	if len(out.Fit) != 2 {
		t.Errorf("Fit = %v, want exactly the 2 candidates that survived condition (4) — if the ranking is not running here, this test cannot see a type being promoted into it", out.Fit)
	}
	if out.Inferred != "roomy" {
		t.Errorf("inferred %q, want \"roomy\" — 99.0%% against baggy's 87.0%%", out.Inferred)
	}
	if !out.TieBroken {
		t.Error("TieBroken is false though two candidates were ranked — the report would present a guess as a certainty")
	}
}

// TestBestFit_RequiredPropertyStillGatesTheWinner keeps condition (3) ahead
// of the ranking too. `full` would score 100% on evidence, but it requires
// `owner`, which the note does not carry, so it is not a candidate and
// cannot be ranked into one.
func TestBestFit_RequiredPropertyStillGatesTheWinner(t *testing.T) {
	dir := t.TempDir()
	n := noteOnDisk(t, dir, "untyped.md", "---\ntitle: A thing\n---\n")

	rep := InferTypesForUntypedNotes([]NoteRecord{n}, map[string][]InferredProperty{
		"full":    weighted("title!:50", "owner!:50"),
		"partial": weighted("title!:50", "owner:1"),
	}, true)

	out := outcomeFor(t, rep, "untyped.md")
	if out.Inferred == "full" {
		t.Fatalf("wrote `type: full` on a note missing full's required `owner` — the note would fail validation immediately. Reason: %s", out.Reason)
	}
	if out.Inferred != "partial" {
		t.Errorf("inferred %q, want \"partial\"", out.Inferred)
	}
}

// ---------------------------------------------------------------------------
// The arithmetic itself.
// ---------------------------------------------------------------------------

// TestTypeFit_LeadsIsExactAtTheMarginBoundary pins the comparison at the
// boundary, in both directions, because "at least 5 points" and "more than
// 5 points" differ by exactly the cases a rounding bug produces.
//
// 55/100 against 50/100 is a lead of exactly 5.0 points and MUST count;
// 5499/10000 against 50/100 is 4.99 points and MUST NOT.
func TestTypeFit_LeadsIsExactAtTheMarginBoundary(t *testing.T) {
	base := TypeFit{Type: "b", Carried: 50, Total: 100}

	exactly5 := TypeFit{Type: "a", Carried: 55, Total: 100}
	if !exactly5.leads(base, bestFitMarginNum, bestFitMarginDen) {
		t.Error("a lead of exactly 5.0 points does not count as leading; the rule is `at least`, and a strict `>` here would silently drop every borderline decision")
	}

	justUnder := TypeFit{Type: "a", Carried: 5499, Total: 10000}
	if justUnder.leads(base, bestFitMarginNum, bestFitMarginDen) {
		t.Error("a lead of 4.99 points counts as leading — the margin is not being applied exactly")
	}

	// A candidate nothing was ever filled in for cannot lead anything, and
	// must not divide by zero trying.
	empty := TypeFit{Type: "e"}
	if empty.leads(base, bestFitMarginNum, bestFitMarginDen) {
		t.Error("a type with zero evidence claims to lead one with evidence")
	}
	if base.leads(empty, bestFitMarginNum, bestFitMarginDen) {
		t.Error("leading an unscorable candidate was treated as a win; there is no score to beat")
	}
}

// TestRankFit_TiesAreOrderedByNameSoAReportNeverReshuffles: Go randomises
// map iteration, and a report whose ranking reorders between two identical
// runs teaches an operator to distrust every number in it.
func TestRankFit_TiesAreOrderedByNameSoAReportNeverReshuffles(t *testing.T) {
	shapes := buildTypeShapes(map[string][]InferredProperty{
		"zeta":  weighted("p!:5"),
		"alpha": weighted("p!:5"),
		"mid":   weighted("p!:5"),
	})
	want := []string{"alpha", "mid", "zeta"}
	for i := 0; i < 25; i++ {
		fits := rankFit([]string{"p"}, []string{"zeta", "alpha", "mid"}, shapes)
		for j, f := range fits {
			if f.Type != want[j] {
				t.Fatalf("run %d: ranking %v, want %v — equal scores must fall back to name order", i, fits, want)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// The real vault: the exit proof's other half.
// ---------------------------------------------------------------------------

// TestFixtureVault_TypedNotesAreNeverSelfInvalidated carries the acceptance
// bar onto the founder's OWN vault, which is where it matters: after an
// import, the number of notes the importer typed AND then reported invalid
// must be ZERO.
//
// The committed-fixture version of this bar
// (TestImporter_TypedNotesAreNeverSelfInvalidated) proves the rule on notes
// written to make a point. This one proves it on 757 notes nobody wrote for
// a test, including the 11 the best-fit rule now GUESSES at — which is the
// only population where a bad tie-break could show up as a self-inflicted
// validation finding.
//
// Like the rest of this harness it SKIPS without OMNIPUS_KB_FIXTURE, so it
// contributes nothing to CI. It is a measurement, and the number it prints
// is the thing to read.
func TestFixtureVault_TypedNotesAreNeverSelfInvalidated(t *testing.T) {
	root := fixtureVaultCopy(t)

	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	schemaSet, _, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("loading the schemas this run wrote: %v", err)
	}

	var typed, tieBroken, selfInvalidated int
	for _, o := range rep.TypeInference.Notes {
		if !o.Written {
			continue
		}
		typed++
		if o.TieBroken {
			tieBroken++
		}
		abs := filepath.Join(root, filepath.FromSlash(o.RelPath))
		raw, readErr := os.ReadFile(abs)
		if readErr != nil {
			t.Fatalf("re-reading %s: %v", o.RelPath, readErr)
		}
		rec := records.ParseRecord(abs, raw)
		if rec.ParseError != "" {
			selfInvalidated++
			t.Errorf("%s: the importer's own edit made the note unparseable: %s", o.RelPath, rec.ParseError)
			continue
		}
		rr := records.ValidateRecord(schemaSet, rec, records.ValidateOptions{ReportUndeclaredProperties: true})
		if !rr.Recognised {
			selfInvalidated++
			t.Errorf("%s: typed %q, but no schema recognises that type", o.RelPath, o.Inferred)
			continue
		}
		if !rr.Valid() {
			selfInvalidated++
			t.Errorf("%s: the importer wrote `type: %s` and the SAME run then reports it invalid. Findings:\n      %v",
				o.RelPath, o.Inferred, rr.Findings)
		}
	}

	t.Logf("REAL VAULT acceptance bar: %d notes typed (%d of them by the best-fit guess), %d self-invalidated — the bar is 0",
		typed, tieBroken, selfInvalidated)

	if typed == 0 {
		t.Fatal("the run typed nothing, so this measurement is vacuous — the vault or the rule has changed")
	}
	if selfInvalidated != 0 {
		t.Errorf("ACCEPTANCE BAR FAILED: %d of %d notes typed by this import are invalid against the schemas the same import wrote; the bar is zero",
			selfInvalidated, typed)
	}
}
