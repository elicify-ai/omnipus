// Omnipus — rank fusion mechanics and per-signal power guards (ADR-068 D21.3).
//
// The ablation in rank_eval_test.go reports aggregate numbers. This file pins
// the things an aggregate cannot see: that each signal is CAPABLE of changing an
// outcome at all, and that the fusion arithmetic is the arithmetic RRF specifies.
//
// # Why the per-signal power guards exist
//
// A ranking signal that silently does nothing produces an ablation row identical
// to the row without it — which is indistinguishable from a careful measurement
// showing the signal does not help. Both of those happened here during
// development, and neither announced itself:
//
//   - the NAME signal compared the whole query phrase against the note name, so
//     it fired on 0 of 60 real queries;
//   - the CORPUS repeated every note's title inside its own body, so title and
//     body agreed on every document and no field weighting could reorder
//     anything.
//
// Each produced a clean, plausible, completely uninformative zero. So before any
// null result from the ablation may be believed, the instrument must be shown to
// detect a PLANTED POSITIVE: a case constructed so the signal must change the
// answer, where a failure to change it means the signal is broken rather than
// unhelpful.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"
)

// ---------------------------------------------------------------------------
// FuseRRF arithmetic.
// ---------------------------------------------------------------------------

func TestFuseRRF_ScoreIsTheRRFSum(t *testing.T) {
	cfg := FusionConfig{K: 60, Weights: FusionWeights{SignalBM25F: 1, SignalName: 0.5}}
	got := FuseRRF([]RankedList{
		{Signal: SignalBM25F, Paths: []string{"a.md", "b.md"}},
		{Signal: SignalName, Paths: []string{"b.md"}},
	}, cfg)

	// Derived from RRF's definition, not read off the implementation:
	//   a = 1/(60+1)                     = 0.016393...
	//   b = 1/(60+2) + 0.5/(60+1)        = 0.016129... + 0.008196... = 0.024325...
	wantA := 1.0 / 61.0
	wantB := 1.0/62.0 + 0.5/61.0

	if len(got) != 2 {
		t.Fatalf("got %d hits, want 2", len(got))
	}
	if got[0].Path != "b.md" {
		t.Errorf("b.md is in two lists and must outrank a.md; got %q first", got[0].Path)
	}
	if d := got[0].Score - wantB; d > 1e-12 || d < -1e-12 {
		t.Errorf("b.md score = %.12f, want %.12f", got[0].Score, wantB)
	}
	if d := got[1].Score - wantA; d > 1e-12 || d < -1e-12 {
		t.Errorf("a.md score = %.12f, want %.12f", got[1].Score, wantA)
	}
	if got[0].Ranks[SignalName] != 1 || got[0].Ranks[SignalBM25F] != 2 {
		t.Errorf("b.md ranks = %v, want bm25f=2 name=1", got[0].Ranks)
	}
	if _, ok := got[1].Ranks[SignalName]; ok {
		t.Errorf("a.md is absent from the name list and must carry no name rank, got %v", got[1].Ranks)
	}
}

func TestFuseRRF_ZeroWeightSignalContributesNothingAtAll(t *testing.T) {
	// A dropped signal must not contribute its DOCUMENTS either. If it did, an
	// ablation rung would still be retrieving through the signal it claims to
	// have removed, and every row below the top of the ladder would be measuring
	// a ranking nobody described.
	cfg := FusionConfig{K: 60, Weights: FusionWeights{SignalBM25F: 1, SignalRecency: 0}}
	got := FuseRRF([]RankedList{
		{Signal: SignalBM25F, Paths: []string{"a.md"}},
		{Signal: SignalRecency, Paths: []string{"zzz.md"}},
	}, cfg)
	if len(got) != 1 || got[0].Path != "a.md" {
		t.Fatalf("a zero-weighted signal introduced documents: %+v", got)
	}
}

func TestFuseRRF_TiesBreakOnPathNotMapOrder(t *testing.T) {
	// Identical lists in both signals means identical scores for both documents.
	// Map iteration order is randomised in Go, so without an explicit tie-break
	// this ordering would differ between runs (FR-046).
	cfg := FusionConfig{K: 60, Weights: FusionWeights{SignalBM25F: 1, SignalName: 1}}
	for i := 0; i < 50; i++ {
		got := FuseRRF([]RankedList{
			{Signal: SignalBM25F, Paths: []string{"b.md"}},
			{Signal: SignalName, Paths: []string{"a.md"}},
		}, cfg)
		if len(got) != 2 || got[0].Path != "a.md" || got[1].Path != "b.md" {
			t.Fatalf("run %d: tie broke non-deterministically: %+v", i, got)
		}
	}
}

func TestFuseRRF_RepeatedPathInOneListIsCountedOnce(t *testing.T) {
	// A duplicate would otherwise be scored twice AND would inflate the rank of
	// everything after it, which silently distorts every later position.
	cfg := FusionConfig{K: 60, Weights: FusionWeights{SignalBM25F: 1}}
	got := FuseRRF([]RankedList{
		{Signal: SignalBM25F, Paths: []string{"a.md", "a.md", "b.md"}},
	}, cfg)
	if len(got) != 2 {
		t.Fatalf("got %d hits, want 2", len(got))
	}
	if want := 1.0 / 61.0; got[0].Score != want {
		t.Errorf("a.md scored %.12f, want %.12f (counted once)", got[0].Score, want)
	}
	// b.md must be rank 2, not rank 3.
	if want := 1.0 / 62.0; got[1].Score != want {
		t.Errorf("b.md scored %.12f, want %.12f (rank 2, not 3)", got[1].Score, want)
	}
}

// ---------------------------------------------------------------------------
// Name signal.
// ---------------------------------------------------------------------------

func TestNameMatchTier_GradesTheThreeWaysANameIsNamed(t *testing.T) {
	const p = "projects/Lowfield Ledger Programme.md"
	cases := []struct {
		query string
		want  int
		why   string
	}{
		{"lowfield ledger programme", 3, "the query IS the name"},
		{"Programme Ledger Lowfield", 3, "same tokens, order-insensitive"},
		{"retention review lowfield ledger programme approved", 2, "the name is a contiguous phrase inside a longer query"},
		{"lowfield programme retention ledger", 1, "every name token present, out of order"},
		{"lowfield ledger", 0, "a name the query only partly covers earns nothing"},
		{"harbour atlas programme", 0, "different entity"},
		{"", 0, "empty query"},
	}
	for _, c := range cases {
		if got := nameMatchTier(c.query, p); got != c.want {
			t.Errorf("nameMatchTier(%q) = %d, want %d — %s", c.query, got, c.want, c.why)
		}
	}
}

// ---------------------------------------------------------------------------
// F1 (code review A) — nameMatchTier must fold with records.FoldKey, not
// strings.ToLower or strings.EqualFold.
// ---------------------------------------------------------------------------
//
// TestNameMatchTier_GradesTheThreeWaysANameIsNamed above is seven ASCII cases.
// Every one of them passes IDENTICALLY whether foldName folds with
// strings.ToLower, strings.EqualFold-equivalent token comparison, or
// records.FoldKey — ASCII letters fold the same way under all three, so that
// test cannot observe which folding rule nameMatchTier actually uses. An
// implementation that quietly regressed foldName back to strings.ToLower would
// pass it unchanged.
//
// This file's tests below are the replacement instrument: they run the SAME
// tier decision through three different folding rules over pairs picked
// because full Unicode folding (records.FoldKey, AC-8.9 in
// pkg/records/fold_test.go) disagrees with strings.ToLower and/or
// strings.EqualFold on them, and assert the three answer vectors differ from
// one another. That is what makes the fixture unfakeable: an implementation
// that silently used ToLower or EqualFold fails here even if it passed every
// ASCII case above.

// foldTokensToLowerForTest reproduces the OLD, BUGGY foldName this fix
// replaces — SIMPLE per-rune strings.ToLower folding — so the test can prove
// nameMatchTier no longer behaves that way. It is deliberately a copy rather
// than a call into a shared helper: the point is to pin what the code USED TO
// DO, which must not silently track what it does now.
func foldTokensToLowerForTest(s string) []string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
			continue
		}
		space = true
	}
	f := b.String()
	if f == "" {
		return nil
	}
	return strings.Split(f, " ")
}

// foldTokensRawForTest tokenizes WITHOUT folding case at all — the raw
// splitter that, paired with containsAllEqualFoldForTest below, reproduces
// strings.EqualFold's SIMPLE folding semantics for a token-SET comparison.
// strings.EqualFold has no string-transform form (it is a comparator, not a
// fold), so representing "nameMatchTier if it compared tokens with
// strings.EqualFold" means keeping tokens raw and folding only at the
// comparison, not the tokenization, step.
func foldTokensRawForTest(s string) []string {
	var b strings.Builder
	space := false
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
			continue
		}
		space = true
	}
	f := b.String()
	if f == "" {
		return nil
	}
	return strings.Split(f, " ")
}

// containsAllEqualFoldForTest is containsAll (rank.go) with the map-based
// exact-string membership test replaced by a pairwise strings.EqualFold
// comparison, so it can be driven off foldTokensRawForTest's unfolded tokens.
func containsAllEqualFoldForTest(have, want []string) bool {
	for _, w := range want {
		found := false
		for _, h := range have {
			if strings.EqualFold(h, w) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// tier3ForTest is nameMatchTier's tier-3 rule ("the query IS the name",
// order-insensitive) parameterised over which tokenizer and which
// token-containment rule to use, so the same decision can be replayed under
// strings.ToLower, strings.EqualFold, and (via the real nameMatchTier)
// records.FoldKey. Tiers 1 and 2 are deliberately not replayed here — tier 3
// alone is where AC-8.9's pairs land (each pair IS the whole name), and it is
// the tier the F1 bug report names: a false tier-3 promotes a note to rank 1
// with the fusion's full top-tier RRF bonus.
func tier3ForTest(query, name string, tokenize func(string) []string, containsAll func(have, want []string) bool) int {
	qt := tokenize(query)
	nt := tokenize(name)
	if len(qt) == 0 || len(nt) == 0 {
		return 0
	}
	if len(qt) == len(nt) && containsAll(qt, nt) {
		return 3
	}
	return 0
}

// foldDiscriminatingPairs are AC-8.9's own witnesses (pkg/records/fold_test.go),
// carried over because they are exactly the pairs on which records.FoldKey
// disagrees with strings.ToLower and/or strings.EqualFold — plus the query is
// spelled as a single-token name so a tier-3 "the query IS the name" match is
// the exact question each row asks.
var foldDiscriminatingPairs = []struct {
	ac    string
	query string
	name  string
	why   string
}{
	{
		ac: "AC-8.9e", query: "istanbul", name: "İSTANBUL",
		why: "F1's own reported bug. Turkish dotted İ and plain i are DIFFERENT LETTERS; this MUST NOT " +
			"tier-3-match. strings.ToLower says it DOES match (the Turkish-I bug) — a WRONG MATCH that " +
			"would promote an unrelated note to rank 1 with the fusion's full top-tier RRF bonus. DO NOT " +
			"'fix' this row to match; that reintroduces the bug F1 reported.",
	},
	{
		ac: "AC-8.9a", query: "strasse", name: "straße",
		why: "German ß needs FULL folding (ß → ss) to tier-3-match \"strasse\". strings.ToLower and the " +
			"strings.EqualFold-style comparison both perform only SIMPLE folding and miss it — a WRONG " +
			"NON-MATCH: a user who types the name they see does not find their own note.",
	},
	{
		ac: "AC-8.9f", query: "file", name: "ﬁle",
		why: "The ﬁ ligature (U+FB01). False under both SIMPLE folding mechanisms, for the same reason as " +
			"the ß row, with an independent character so the conclusion does not rest on German alone.",
	},
	{
		ac: "AC-8.9b", query: "σίσυφος", name: "ΣΊΣΥΦΟΣ",
		why: "Greek. strings.ToLower says NO MATCH and the strings.EqualFold-style comparison says MATCH — " +
			"the two SIMPLE-folding mechanisms disagree WITH EACH OTHER here, which is why this fixture " +
			"needs all three folding rules compared, not just one stdlib function checked against ours.",
	},
}

// TestNameMatchTier_FoldsFullUnicodeNotSimple is F1's discriminating test: it
// proves nameMatchTier's tier-3 decision agrees with records.FoldKey and
// DISAGREES, as a whole answer vector, with BOTH strings.ToLower and a
// strings.EqualFold-style comparison — mirroring
// pkg/records/fold_test.go's TestFold_DisagreesWithBothStdlibFunctions.
//
// TestNameMatchTier_GradesTheThreeWaysANameIsNamed cannot make this argument:
// every one of its cases is ASCII, and ASCII folds identically under all three
// mechanisms, so it would pass unchanged against a nameMatchTier silently
// reverted to strings.ToLower. This test fails under that reversion.
func TestNameMatchTier_FoldsFullUnicodeNotSimple(t *testing.T) {
	if len(foldDiscriminatingPairs) != 4 {
		t.Fatalf("fixture has %d pairs, want 4 — a row was added or removed without checking the vectors below still discriminate", len(foldDiscriminatingPairs))
	}

	ours := make([]int, 0, len(foldDiscriminatingPairs))
	toLower := make([]int, 0, len(foldDiscriminatingPairs))
	equalFold := make([]int, 0, len(foldDiscriminatingPairs))

	for _, p := range foldDiscriminatingPairs {
		t.Run(p.ac, func(t *testing.T) {
			// relPath wraps name the way a real pool member would arrive —
			// nameMatchTier reads path.Base and strips the markdown
			// extension, so the fixture must exercise that, not just the bare
			// name string.
			relPath := "Notes/" + p.name + ".md"
			got := nameMatchTier(p.query, relPath)
			ours = append(ours, got)
			toLower = append(toLower, tier3ForTest(p.query, p.name, foldTokensToLowerForTest, containsAll))
			equalFold = append(equalFold, tier3ForTest(p.query, p.name, foldTokensRawForTest, containsAllEqualFoldForTest))

			t.Logf("%s: query=%q name=%q — nameMatchTier=%d — %s", p.ac, p.query, p.name, got, p.why)
		})
	}

	same := func(a, b []int) bool {
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	if same(ours, toLower) {
		t.Fatalf("nameMatchTier produced the SAME answers as a strings.ToLower-folded tokenizer (%v). "+
			"strings.ToLower performs SIMPLE folding and gets the Turkish İ/i pair WRONG (a false match) — "+
			"agreeing with it means foldName has regressed to strings.ToLower and F1 is unfixed.", ours)
	}
	if same(ours, equalFold) {
		t.Fatalf("nameMatchTier produced the SAME answers as a strings.EqualFold-style comparison (%v). "+
			"strings.EqualFold also performs only SIMPLE folding and cannot match German ß against ss or "+
			"the ﬁ ligature against \"fi\" — agreeing with it means foldName is not using FULL Unicode "+
			"case folding (records.FoldKey).", ours)
	}
	if same(toLower, equalFold) {
		t.Fatalf("the fixture no longer discriminates: strings.ToLower and the strings.EqualFold-style "+
			"comparison now agree on every row (%v). AC-8.9b (Greek) exists precisely because the two "+
			"SIMPLE-folding mechanisms disagree; add a row that separates them again.", toLower)
	}

	// The Turkish row asserted a second time, on its own, exactly as
	// pkg/records/fold_test.go's TestFold_TurkishNegativeIsDeliberate does —
	// so a reader who only skims the table above still meets this sentence.
	if nameMatchTier("istanbul", "Notes/İSTANBUL.md") != 0 {
		t.Fatal(`nameMatchTier("istanbul", "Notes/İSTANBUL.md") must be 0 (no match), and this is CORRECT
BEHAVIOUR, NOT A GAP. Turkish dotted İ and plain i are different letters. If you are reading this
because you "fixed" nameMatchTier to make them match: you have reintroduced the Turkish-I bug F1
reported, which promoted an unrelated Turkish note to rank 1 in the RRF fusion for every "istanbul"
query. Do not change this assertion. Change foldName back to records.FoldKey.`)
	}
}

// TestRank_NameSignalFiresOnRealQueries is the power guard for the name signal.
//
// It pins the bug this signal actually shipped with: a whole-phrase comparison
// that fires on zero real queries. That version passed every unit test written
// against short queries and produced an ablation row identical to the row
// without it.
//
// The threshold is deliberately a floor and not an exact count — the fixture may
// legitimately change — but a floor of zero would be no guard at all, which is
// precisely what was missing.
func TestRank_NameSignalFiresOnRealQueries(t *testing.T) {
	h := newEvalHarness(t)
	fired := 0
	for _, q := range h.fixture.Queries {
		pool, err := h.ix.bm25fPool(q.Query, FusionPoolSize, nil)
		if err != nil {
			t.Fatalf("pool %q: %v", q.Query, err)
		}
		if len(nameList(q.Query, pool).Paths) > 0 {
			fired++
		}
	}
	minFired := len(h.fixture.Queries) / 4
	if fired < minFired {
		t.Errorf("the name signal fired on %d of %d committed queries (want at least %d). "+
			"A signal this quiet contributes nothing to the fusion, and its ablation row "+
			"would be identical to the row without it — which reads as 'the signal does not "+
			"help' and actually means 'the signal never ran'.",
			fired, len(h.fixture.Queries), minFired)
	}
	t.Logf("name signal fired on %d of %d committed queries", fired, len(h.fixture.Queries))
}

// ---------------------------------------------------------------------------
// Field weighting — the planted positive.
// ---------------------------------------------------------------------------

// TestRank_TitleMatchOutranksPassingBodyMention is the power guard for the
// fielded half of D21.3, and it asserts the OUTCOME rather than the mechanism —
// for a measured reason that is itself the finding.
//
// # What was expected, and what is actually true
//
// The obvious guard is: plant a note TITLED for the query whose body barely
// mentions it against a note titled otherwise whose body is saturated with it,
// then assert the winner FLIPS between a title-heavy and a body-heavy weighting.
// That guard fails, and it fails for a reason worth writing down because it
// changes what the weights table is worth.
//
// Measured on this exact planted case, through the production path:
//
//	body field alone      Migration Notes 0.1206  >  Pricing Review 0.0799
//	title field alone     Pricing Review  0.3781  >  Migration Notes (absent)
//	title 10x / body 1x   Pricing Review  0.6708  >  Migration Notes 0.0005
//	title  1x / body 10x  Pricing Review  0.4350  >  Migration Notes 0.0252
//
// The last row is the surprise. Even with body weighted TEN TIMES title, the
// title-matching note still wins — and Migration Notes' score COLLAPSED from
// 0.1206 (body clause alone) to 0.0252 the moment a title clause it does not
// match was added to the disjunction.
//
// The cause is bleve's disjunction normalisation, which behaves like a
// coordination factor: a document matching FOUR of the disjunction's clauses is
// promoted enormously over one matching ONE, and in the practical boost range
// (~0.5x-10x, past which boosts saturate) that clause-count effect DOMINATES the
// weights.
//
// # The consequence, stated plainly
//
// D21.3 wants "a title match should outrank a passing body mention". On bleve
// v2.6.1 that behaviour is obtained ALREADY, by querying the fields separately
// at all — a note whose title matches also matches name and headings, so it wins
// on clause count. The per-field WEIGHTS contribute very little on top.
//
// So this guard pins the behaviour that is real and load-bearing, and does not
// pin a weight ratio that cannot carry the outcome:
//
//  1. the fields genuinely differ — body alone prefers the saturated body;
//  2. the multi-field ranking prefers the titled note.
//
// Deleting fieldTitle/fieldName/fieldHeadings from fusionFieldWeights breaks (2)
// — verified by mutation — so this is a guard on production code and not on a
// fixture.
func TestRank_TitleMatchOutranksPassingBodyMention(t *testing.T) {
	corpus := t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(corpus, filepath.FromSlash(rel))
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("Pricing Review.md",
		"# Pricing Review\n\nThis note is mostly about migration scheduling and "+
			"migration tooling and migration risk. It mentions pricing once.\n")
	write("Migration Notes.md",
		"# Migration Notes\n\npricing pricing pricing pricing pricing pricing "+
			"pricing pricing pricing pricing pricing pricing.\n")

	ix, err := OpenIndex(t.TempDir(), corpus)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer func() {
		if err := ix.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
	if _, err := ix.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}

	orig := fusionFieldWeights
	defer func() { fusionFieldWeights = orig }()

	top := func(ws []fieldWeight) string {
		fusionFieldWeights = ws
		hits, err := ix.bm25fPool("pricing", 5, nil)
		if err != nil {
			t.Fatalf("pool: %v", err)
		}
		if len(hits) == 0 {
			t.Fatal("no hits for a term both notes contain; the planted case did not index")
		}
		return hits[0].Path
	}

	// (1) The instrument has power: the two fields disagree, so a ranking that
	// consults only the body reaches the OPPOSITE answer. Without this the test
	// below could pass on a corpus where the fields agree and prove nothing —
	// which is exactly how the first version of the eval corpus produced a clean
	// meaningless zero for every BM25F row.
	if got := top([]fieldWeight{{Field: fieldBody, Weight: 1}}); got != "Migration Notes.md" {
		t.Fatalf("body alone ranks %q first, want Migration Notes.md. The planted case does "+
			"not actually make the fields disagree, so nothing below it can be trusted.", got)
	}

	// (2) The shipped weighting reaches the D21.3 outcome.
	if got := top(orig); got != "Pricing Review.md" {
		t.Errorf("the shipped field set ranks %q first for \"pricing\"; D21.3 requires a title "+
			"match to outrank a passing body mention", got)
	}
}

// ---------------------------------------------------------------------------
// The retriever/prior boundary.
// ---------------------------------------------------------------------------

// TestFusedSearch_PriorsCannotIntroduceADocument pins the design's central rule.
//
// It is a GUARD, so it is written to fail on a PRODUCTION change rather than on
// a fixture change: it asserts that a note the query does not match cannot enter
// the results however recent or however linked it is. Wiring recency as a
// corpus-wide retriever — the naive reading of "fuse four ranked lists" — makes
// this fail.
func TestFusedSearch_PriorsCannotIntroduceADocument(t *testing.T) {
	corpus := t.TempDir()
	write := func(rel, body string, at time.Time) {
		full := filepath.Join(corpus, rel)
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(full, at, at); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-9000 * time.Hour)
	write("Relevant.md", "# Relevant\n\nlandlock seccomp sandbox kernel.\n", old)
	// Brand new, heavily linked, and containing NOT ONE query term.
	write("Fresh.md", "# Fresh\n\nentirely unrelated prose about gardening.\n", time.Now())
	write("L1.md", "# L1\n\n[[Fresh]]\n", old)
	write("L2.md", "# L2\n\n[[Fresh]]\n", old)
	write("L3.md", "# L3\n\n[[Fresh]]\n", old)

	ix, err := OpenIndex(t.TempDir(), corpus)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cerr := ix.Close(); cerr != nil {
			t.Errorf("close: %v", cerr)
		}
	}()
	if _, serr := ix.Sync(context.Background()); serr != nil {
		t.Fatal(serr)
	}
	root, err := NewCollectionRoot(OSLinkFS(), corpus)
	if err != nil {
		t.Fatal(err)
	}
	g, err := BuildLinkGraph(OSLinkFS(), root)
	if err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(ix.ManifestPath(), ix.Root())
	if err != nil {
		t.Fatal(err)
	}
	src := ManifestSources(m)
	src.Backlinks = GraphBacklinks(g)

	if got := len(g.Backlinks("Fresh.md")); got != 3 {
		t.Fatalf("the planted hub has %d backlinks, want 3; the guard would pass vacuously", got)
	}

	hits, err := ix.FusedSearch("landlock seccomp", 10, DefaultFusionConfig(), src, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Path == "Fresh.md" {
			t.Fatalf("the newest, most-linked note entered the results for a query it does "+
				"not match: %v. A prior may only REORDER the retriever's pool.", pathsOf(hits))
		}
	}
	if len(hits) == 0 || hits[0].Path != "Relevant.md" {
		t.Errorf("the matching note must rank first, got %v", pathsOf(hits))
	}
}

// TestRankedSearch_DefaultIsNotTheFusion pins the shipping decision in a test.
//
// The zero-value RankOptions must be BM25F-only. If a future edit makes fusion
// the default, this fails — which is the point: FusionEnabledByDefault is a
// decision backed by an argument about what the evidence can support, not a
// convenience toggle.
func TestRankedSearch_DefaultIsNotTheFusion(t *testing.T) {
	if FusionEnabledByDefault {
		t.Fatal("FusionEnabledByDefault is true; see rank.go for why it is false and " +
			"TestRank_FusionMeetsNDCGThreshold for the measurement")
	}
	if (RankOptions{}).Fusion {
		t.Error("the zero value of RankOptions enables fusion; the default a caller gets " +
			"without thinking must be the one the evidence supports")
	}
}
