// Tests for ADR-068 D21.1 — the index must SCORE with BM25, not TF-IDF.
//
// The oracle is the two scoring formulas as bleve v2.6.1 actually implements
// them, read out of the dependency rather than assumed. Both start from
// u = sqrt(freq) (scorer_term.go's Score) and the stored length norm
// n = 1/sqrt(fieldLength):
//
//	TF-IDF   score = idf · u · n              = idf · sqrt(freq/fieldLength)
//	BM25     score = idf · k1·u / (u + k1·K)   K = 1-b + b·fieldLength/avgFieldLength
//
// Reading that off the source rather than off a textbook matters, and both
// halves bit during development. bleve's tf is sqrt(freq), not freq — so its
// TF-IDF is a term DENSITY and is scale-free in freq/length. And
// avgFieldLength is ceil(FieldCardinality / DocCount) — the number of DISTINCT
// terms in the field dictionary, not the mean document length (search_term.go's
// bm25ScoreMetrics). An earlier corpus here was derived from the textbook
// formulas and the two models agreed on it, which the guard test below caught.
//
// The discriminating property is what BM25 does that TF-IDF does not: it
// SATURATES term frequency while normalising length LINEARLY. TF-IDF's
// sqrt(freq)/sqrt(length) rewards a long note for repeating a term hundreds of
// times, because the ratio is all it sees. BM25's numerator flattens out while
// its denominator keeps growing with raw length, so the same note loses to a
// short one that mentions the term twice. That is a difference in ORDER,
// observable from outside, and it is what these tests assert — never the value
// of the setting itself.
//
// A test that asserted `buildIndexMapping().ScoringModel == "bm25"` would pass
// against an index that ranks with TF-IDF, because bleve reads the model from
// the mapping PERSISTED IN THE INDEX and not from the mapping the code builds.
// That is precisely the defect D21.1 found, so the constant is exactly the
// wrong thing to assert.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/scorch"
	bleveQuery "github.com/blevesearch/bleve/v2/search/query"
	bleveIndexAPI "github.com/blevesearch/bleve_index_api"
)

// ---------------------------------------------------------------------------
// The discriminating corpus.
//
// Two notes, one query term, so no coordination factor is in play (bleve scales
// a disjunction by matched-terms/query-terms, which in an earlier version of
// this corpus decided the ranking under BOTH models and made the comparison
// meaningless).
//
//	repeater.md   512 occurrences in 1,600 tokens   density 32%
//	concise.md      2 occurrences in    25 tokens   density  8%
//
// Measured against bleve v2.6.1, per-note score:
//
//	           repeater.md   concise.md   winner
//	TF-IDF        0.336320     0.168160   repeater  (2.0x)
//	BM25          0.006664     0.023866   concise   (3.6x)
//
// TF-IDF sees only density and prefers the repeater twofold. BM25's numerator
// has long since saturated while its denominator still carries the full 1,600
// tokens, so the same note loses by a factor of 3.6. Both margins are wide
// enough that neither verdict rests on bleve's byte-quantisation of the length
// norm.
// ---------------------------------------------------------------------------

const (
	// smTerm is an invented token: not an English stopword (the "en" analyzer
	// drops those) and carrying no suffix the Porter stemmer rewrites, so what
	// is indexed is what is written. smFiller is the same, and is never queried
	// — it exists only to give the two notes their very different lengths.
	smTerm   = "quorvex"
	smFiller = "padwordx"

	smRepeaterPath   = "repeater.md"
	smRepeaterFreq   = 512
	smRepeaterLength = 1600

	smConcisePath   = "concise.md"
	smConciseFreq   = 2
	smConciseLength = 25
)

// smBody builds a note of exactly length analysed tokens, freq of which are the
// query term and the rest inert filler.
func smBody(freq, length int) string {
	return strings.TrimSpace(
		strings.Repeat(smTerm+" ", freq) + strings.Repeat(smFiller+" ", length-freq))
}

func smRepeaterBody() string { return smBody(smRepeaterFreq, smRepeaterLength) }
func smConciseBody() string  { return smBody(smConciseFreq, smConciseLength) }

// smRankUnder indexes the two notes into a throwaway bleve index built from the
// production mapping with ONE field overridden — the scoring model — and
// returns the document ids in rank order.
//
// Everything else is production's: the same buildIndexMapping, the same field
// set and the same analyzers. Isolating a single variable is the whole point.
func smRankUnder(t *testing.T, scoringModel string) []string {
	t.Helper()

	m := buildIndexMapping()
	m.ScoringModel = scoringModel

	dir := filepath.Join(t.TempDir(), "bleve")
	idx, err := bleve.NewUsing(dir, m, scorch.Name, scorch.Name, bleveOpenConfig())
	if err != nil {
		t.Fatalf("create %s index: %v", scoringModel, err)
	}
	defer func() {
		if closeErr := idx.Close(); closeErr != nil {
			t.Errorf("close %s index: %v", scoringModel, closeErr)
		}
	}()

	for _, d := range []indexDoc{
		{Path: smRepeaterPath, Name: "repeater", Kind: string(ScanKindNote), Body: smRepeaterBody()},
		{Path: smConcisePath, Name: "concise", Kind: string(ScanKindNote), Body: smConciseBody()},
	} {
		if err := idx.Index(d.Path, d); err != nil {
			t.Fatalf("index %s into %s index: %v", d.Path, scoringModel, err)
		}
	}

	mq := bleveQuery.NewMatchQuery(smTerm)
	mq.SetField(fieldBody)
	req := bleve.NewSearchRequestOptions(mq, 10, 0, false)
	req.SortBy([]string{"-_score", "_id"})

	res, err := idx.Search(req)
	if err != nil {
		t.Fatalf("search %s index: %v", scoringModel, err)
	}
	out := make([]string, 0, len(res.Hits))
	for _, h := range res.Hits {
		out = append(out, h.ID)
	}
	return out
}

// TestScoringModel_CorpusActuallyDiscriminates is the guard on the guard.
//
// The ranking assertion below is only meaningful if BM25 and TF-IDF genuinely
// disagree about this corpus. If a future bleve release changes how it derives
// avgFieldLength or tf, or a change here alters the analyzer or the field set,
// the two models could start agreeing — and then TestScoringModel_IndexRanksWithBM25
// would keep passing while proving nothing at all: green because the question
// stopped being asked. This test fails loudly in that case instead, and it is
// the reason the corpus above could be tuned against real measurements rather
// than hoped over.
func TestScoringModel_CorpusActuallyDiscriminates(t *testing.T) {
	bm25Order := smRankUnder(t, bleveIndexAPI.BM25Scoring)
	tfidfOrder := smRankUnder(t, bleveIndexAPI.TFIDFScoring)

	if len(bm25Order) != 2 || len(tfidfOrder) != 2 {
		t.Fatalf("both notes must match the query: bm25 hits %v, tf-idf hits %v", bm25Order, tfidfOrder)
	}
	if bm25Order[0] != smConcisePath {
		t.Errorf("under BM25 the short note must rank first: term frequency saturates while length "+
			"normalisation stays linear, so 512 occurrences in 1,600 tokens lose to 2 in 25. "+
			"Got order %v, want %q first", bm25Order, smConcisePath)
	}
	if tfidfOrder[0] != smRepeaterPath {
		t.Errorf("under TF-IDF the repeating note must rank first: bleve's TF-IDF is "+
			"sqrt(freq)/sqrt(length), which sees only density, and the repeater is four times denser. "+
			"Got order %v, want %q first", tfidfOrder, smRepeaterPath)
	}
	if bm25Order[0] == tfidfOrder[0] {
		t.Fatalf("BM25 and TF-IDF agree on this corpus (%v), so it cannot discriminate between them "+
			"and TestScoringModel_IndexRanksWithBM25 proves nothing. Re-derive the corpus against the "+
			"formulas bleve currently implements before trusting any green result from that test",
			bm25Order)
	}
}

// TestScoringModel_IndexRanksWithBM25 is the assertion that dies if
// ScoringModel is unset, reverted, or lost to a mapping the index was not
// rebuilt for. It drives the REAL Index — OpenIndex, SyncWith, Search — over
// notes on disk, and asks only which note came back first.
func TestScoringModel_IndexRanksWithBM25(t *testing.T) {
	root := smWriteCorpus(t)
	ix := b2Open(t, t.TempDir(), root)
	b2Sync(t, ix)

	hits, err := ix.Search(smTerm, 10)
	if err != nil {
		t.Fatalf("Search(%q): %v", smTerm, err)
	}
	paths := b2HitPaths(hits)
	if len(paths) != 2 {
		t.Fatalf("Search(%q) returned %v, want both notes", smTerm, paths)
	}
	if paths[0] != smConcisePath {
		t.Errorf("Search(%q) ranked %v.\n"+
			"Want %q first: BM25 saturates term frequency and normalises length linearly, so a note "+
			"repeating the term 512 times across 1,600 tokens cannot outrank a 25-token note that "+
			"mentions it twice.\n"+
			"Getting %q first is the signature of TF-IDF scoring — bleve's default, which applies "+
			"whenever the mapping PERSISTED IN THE INDEX leaves ScoringModel unset (ADR-068 D21.1). "+
			"Check buildIndexMapping's ScoringModel, and whether indexFormatVersion or mappingDrift "+
			"forced the rebuild that makes it take effect.",
			smTerm, paths, smConcisePath, smRepeaterPath)
	}
}

// TestScoringModel_StaleIndexIsRebuiltNotReused pins the half of D21.1 that a
// ranking test alone cannot see: the scoring model lives in the mapping stored
// INSIDE the index, so an index created before the fix goes on scoring TF-IDF
// no matter how the code is compiled. Setting the model without forcing a
// rebuild would be a change that never reaches a single existing installation.
//
// It builds an index the old way — production mapping, ScoringModel left empty
// — stamps it as current so guard G1 cannot be what fires, and then asserts
// that opening it rebuilds anyway, says why, and ranks with BM25 afterwards.
func TestScoringModel_StaleIndexIsRebuiltNotReused(t *testing.T) {
	home := t.TempDir()
	root := smWriteCorpus(t)

	// Create a pre-fix index by hand at the exact path OpenIndex will look at.
	probe := b2Open(t, home, root)
	blevePath, formatPath := probe.blevePath, probe.formatPath
	if err := probe.Close(); err != nil {
		t.Fatalf("close probe: %v", err)
	}
	if err := os.RemoveAll(blevePath); err != nil {
		t.Fatalf("clear probe index: %v", err)
	}

	stale := buildIndexMapping()
	stale.ScoringModel = "" // exactly what shipped before ADR-068 D21.1
	staleIdx, err := bleve.NewUsing(blevePath, stale, scorch.Name, scorch.Name, bleveOpenConfig())
	if err != nil {
		t.Fatalf("create stale index: %v", err)
	}
	if err := staleIdx.Close(); err != nil {
		t.Fatalf("close stale index: %v", err)
	}
	// Stamp the CURRENT format version so G1 is satisfied and any rebuild that
	// happens must be G2 — the mapping comparison — rather than the version bump.
	if err := writeIndexFormat(formatPath); err != nil {
		t.Fatalf("stamp current format: %v", err)
	}

	ix := b2Open(t, home, root)
	reason := ix.RebuildReason()
	if reason == "" {
		t.Fatal("opening an index whose persisted mapping scores TF-IDF returned it as-is. " +
			"bleve resolves the scoring model from that persisted mapping, so this index would rank " +
			"with TF-IDF for the rest of its life while the code believes it asked for BM25. " +
			"mappingDrift must compare the scoring model (ADR-068 D21.1)")
	}
	if !strings.Contains(reason, bleveIndexAPI.TFIDFScoring) || !strings.Contains(reason, bleveIndexAPI.BM25Scoring) {
		t.Errorf("RebuildReason() = %q; it must name both the model found and the model wanted, "+
			"because this string is what an operator is shown to explain the rebuild", reason)
	}

	// A rebuild that fired and then reinstated the same wrong mapping would
	// satisfy every check above, so confirm the result actually ranks with BM25.
	b2Sync(t, ix)
	hits, err := ix.Search(smTerm, 10)
	if err != nil {
		t.Fatalf("Search after rebuild: %v", err)
	}
	paths := b2HitPaths(hits)
	if len(paths) != 2 || paths[0] != smConcisePath {
		t.Errorf("after the rebuild Search(%q) = %v, want %q first — the rebuild fired but the "+
			"recreated index is still not scoring with BM25", smTerm, paths, smConcisePath)
	}
}

// smWriteCorpus writes the two notes into a fresh collection root.
func smWriteCorpus(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for path, body := range map[string]string{
		smRepeaterPath: smRepeaterBody(),
		smConcisePath:  smConciseBody(),
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}
