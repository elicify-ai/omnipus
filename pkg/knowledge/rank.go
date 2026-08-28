// Omnipus — knowledge base rank fusion (ADR-068 D21.3, FR-112/FR-113/FR-116).
//
// This file ranks. It does not retrieve, and the distinction is the whole
// design.
//
// D21.3 names four signals — BM25 over weighted fields, exact/prefix name
// match, recency, and link-graph backlink degree — combined with Reciprocal
// Rank Fusion. RRF is the right combiner because it operates on RANKS: there is
// no principled way to normalise a BM25 score against a modification time
// against an integer degree count, and RRF removes the question instead of
// answering it badly.
//
// # A prior is not a retriever, and conflating them ruins the ranking
//
// The naive reading of "fuse four ranked lists" is that each signal ranks the
// whole corpus and RRF merges them. That produces a specific, severe failure,
// and it is worth spelling out because it is not obvious from the RRF paper:
//
// Recency over the whole corpus puts the vault's most recently edited note at
// rank 1. With k=60 and unit weights, that note scores 1/(60+1) = 0.0164 from
// recency alone — MORE than a note sitting at BM25 rank 30 earns from BM25
// (1/90 = 0.0111). So the note you edited this morning surfaces for every query
// in the vault, whether or not it contains a single query term. There is no
// error; the ranking is just quietly wrong, which is the failure mode D21.5
// calls the worst kind.
//
// So this package draws a line that RRF itself does not:
//
//   - RETRIEVERS establish the candidate pool. BM25F is one. Nothing else is.
//   - PRIORS (recency, backlink degree) may only REORDER the pool. They can
//     never introduce a document no retriever found, because they have no
//     opinion about whether a document answers the query at all.
//
// The name signal sits deliberately on the prior side of that line too, and the
// reason is worth stating because D21.3 describes it as a retrieval case ("I
// know what it's called"). The `name` field is already one of the fields the
// pool query searches, so a note whose name matches is retrieved by BM25F
// anyway — it is just ranked badly, buried under long notes that mention the
// words more often. Reordering fixes that. Retrieving separately would let a
// name signal inject a note the pool query rejected, which reintroduces exactly
// the pathology above through a second door. Pool depth (FusionPoolSize) is set
// well above the visible page precisely so a badly-ranked exact-name match is
// still inside the pool to be rescued.
//
// PriorDepth is the second half of the same defence. An untruncated prior list
// gives EVERY pool member recency credit, so the ordering of the pool's tail
// leaks into the fused top ten. Truncating a prior to its top few means a prior
// is a bonus for a handful of documents and silence for the rest — which is
// what a prior with no query-dependence should be.
//
// # FR-116 — the tokenizer hazard, and the explicit decision it demands
//
// FR-116 forbids shipping Go-side ranking without either threading one shared
// token function through it or recording an explicit decision that the Go pass
// is deliberately unstemmed. This is that decision, and it is narrower than it
// looks because of the retriever/prior split above.
//
// The hazard D21.5 describes is: bleve matches a document on a stemmed form and
// a Go tokenizer that never produces that form then scores it as though the
// query term were absent. That cannot happen here for BM25, because THIS
// PACKAGE NEVER RE-SCORES BM25 IN GO. It consumes bleve's own rank order. There
// is exactly one Go-side tokenizer in this file, and it serves only the name
// signal.
//
// DECISION: the name signal is deliberately UNSTEMMED and deliberately not
// bleve's `en` analyzer.
//
// WHY: the name signal exists for the literal case — the operator knows the
// note is called "Quarterly Pricing Review" and types that. Stemming would make
// "pricing" and "price" the same token, which is right for prose recall and
// wrong for a name match: it would report an exact-name hit for a note that is
// not exactly named that, and exactness is the entire value of the signal.
// Stopword removal would be worse still — half of real note names are mostly
// stopwords ("How to Onboard a Client"), and an analyzer that drops them leaves
// nothing to match on.
//
// WHAT IT COSTS: a query for "pricing reviews" does not earn an exact- or
// prefix-name match against a note named "Pricing Review", so that note gets no
// name bonus and is ranked by BM25F and the priors alone. It is still retrieved
// — bleve's stemmed `name` field matched it into the pool — so the cost is a
// missed bonus, never a missing result. That asymmetry is the reason this
// decision is acceptable: the unstemmed comparison can only fail to promote,
// never fail to find.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/blevesearch/bleve/v2"
	bleveQuery "github.com/blevesearch/bleve/v2/search/query"
)

// RankSignal names one input list to the fusion. The set is closed: adding a
// signal means adding a constant here and a producer below, so no caller can
// fuse a list nobody named.
type RankSignal string

const (
	// SignalBM25F is the retriever — BM25 over weighted fields. It is the only
	// signal permitted to decide WHICH documents are candidates.
	SignalBM25F RankSignal = "bm25f"
	// SignalName is the exact/prefix name match, as a reordering prior.
	SignalName RankSignal = "name"
	// SignalRecency orders the pool by modification time, newest first.
	SignalRecency RankSignal = "recency"
	// SignalBacklinks orders the pool by inbound link count, most-linked first.
	// D21.3 calls this the differentiator: the link graph is already computed
	// and nothing in this space ranks with it.
	SignalBacklinks RankSignal = "backlinks"
)

const (
	// RRFDefaultK is the k of Cormack, Clarke & Buettcher (2009), who introduced
	// RRF with k=60 and found it insensitive over a wide range. It damps the
	// difference between the top ranks: without it, rank 1 would be worth twice
	// rank 2 in every list, and a single signal's confident first place would
	// decide the fused order on its own.
	RRFDefaultK = 60.0

	// FusionPoolSize is how deep the BM25F retriever goes before the priors
	// reorder. It is far above any visible page on purpose — a badly ranked
	// exact-name match has to still be IN the pool for the name prior to rescue
	// it, and the whole design forbids the priors from reaching outside it.
	FusionPoolSize = 200

	// FusionPriorDepth truncates a prior's ranked list. Beyond this depth a
	// prior contributes nothing rather than a small amount, so the ordering of
	// the pool's tail cannot leak into the fused top ten. Zero means untruncated
	// and is available for measurement, not recommended for use.
	FusionPriorDepth = 10
)

// FusionWeights is the per-signal RRF weight. A signal absent from the map, or
// present with a non-positive weight, does not participate — which is how the
// ablation runs a subset of the signals without a second code path.
type FusionWeights map[RankSignal]float64

// FusionConfig is every tunable of the fusion in one value, so an ablation can
// vary one of them and a caller cannot silently disagree with the measurement.
type FusionConfig struct {
	// K is RRF's rank damping constant.
	K float64
	// PoolSize is the retriever's depth.
	PoolSize int
	// PriorDepth truncates each prior list; zero means untruncated.
	PriorDepth int
	// Weights selects and weights the signals.
	Weights FusionWeights
}

// DefaultFusionConfig is D21.3's composition at its stated defaults.
//
// The weights are NOT tuned and are not claimed to be: BM25F carries 1.0 as the
// retriever, the name prior carries 1.0 because an exact name match is a strong
// intent signal, and the two query-independent priors carry 0.5 because they
// know nothing about the query. Whether this composition beats plain BM25 at all
// is the question FR-113 makes measurable; see rank_eval_test.go.
func DefaultFusionConfig() FusionConfig {
	return FusionConfig{
		K:          RRFDefaultK,
		PoolSize:   FusionPoolSize,
		PriorDepth: FusionPriorDepth,
		Weights: FusionWeights{
			SignalBM25F:     1.0,
			SignalName:      1.0,
			SignalRecency:   0.5,
			SignalBacklinks: 0.5,
		},
	}
}

// normalized returns the config with zero fields replaced by their defaults, so
// a caller may pass FusionConfig{} and get the documented behaviour rather than
// a division by k=0.
func (c FusionConfig) normalized() FusionConfig {
	d := DefaultFusionConfig()
	if c.K <= 0 {
		c.K = d.K
	}
	if c.PoolSize <= 0 {
		c.PoolSize = d.PoolSize
	}
	if c.PriorDepth < 0 {
		c.PriorDepth = 0
	}
	if c.Weights == nil {
		c.Weights = d.Weights
	}
	return c
}

// weight returns the weight of one signal, or zero if it does not participate.
func (c FusionConfig) weight(s RankSignal) float64 {
	w, ok := c.Weights[s]
	if !ok || w <= 0 {
		return 0
	}
	return w
}

// RankedList is one signal's opinion: note paths, best first, at most one entry
// per path.
type RankedList struct {
	Signal RankSignal
	Paths  []string
}

// FusedHit is one document's fused result, carrying the per-signal ranks that
// produced it.
//
// Ranks is not debug output. A fused score is a sum of reciprocals with no
// interpretable magnitude, so "why is this third?" is unanswerable from the
// score alone. Carrying the contributing ranks means the ordering can be
// explained to an operator, and — more to the point here — means a test can
// assert WHICH signal moved a document rather than only that the order changed.
type FusedHit struct {
	// Path is the collection-relative note path.
	Path string
	// Score is the RRF score. Comparable within one fusion, meaningless across
	// two.
	Score float64
	// Ranks maps each participating signal to this document's 1-based rank in
	// it. A signal that did not rank the document is absent from the map.
	Ranks map[RankSignal]int
}

// FuseRRF combines ranked lists by Reciprocal Rank Fusion.
//
//	score(d) = sum over lists i of  weight(i) / (k + rank_i(d))
//
// with ranks 1-based and a document absent from a list contributing nothing
// from it. Ties break on path ascending, so the order is total and reproducible
// (FR-046) rather than dependent on map iteration.
//
// A list whose signal has no positive weight is skipped entirely — it cannot
// contribute a document to the output either. That is what makes an ablation
// honest: dropping a signal drops its documents too, exactly as if the signal
// had never been computed.
func FuseRRF(lists []RankedList, cfg FusionConfig) []FusedHit {
	cfg = cfg.normalized()

	acc := make(map[string]*FusedHit)
	for _, l := range lists {
		w := cfg.weight(l.Signal)
		if w == 0 {
			continue
		}
		seen := make(map[string]bool, len(l.Paths))
		rank := 0
		for _, p := range l.Paths {
			if p == "" || seen[p] {
				// A repeated path within one list would otherwise be counted
				// twice and would also inflate every following rank.
				continue
			}
			seen[p] = true
			rank++
			h := acc[p]
			if h == nil {
				h = &FusedHit{Path: p, Ranks: make(map[RankSignal]int, len(lists))}
				acc[p] = h
			}
			h.Score += w / (cfg.K + float64(rank))
			h.Ranks[l.Signal] = rank
		}
	}

	out := make([]FusedHit, 0, len(acc))
	for _, h := range acc {
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// ---------------------------------------------------------------------------
// The four signals.
// ---------------------------------------------------------------------------

// fieldWeight is one field's BM25F weight.
//
// TRUE BM25F pools a term's frequencies across fields BEFORE saturation, so a
// term appearing once in the title and once in the body saturates as a single
// weighted occurrence. bleve exposes no such scorer. What is implementable here
// is the standard practical approximation: a weighted disjunction of per-field
// match queries, where each field saturates independently and the boosts
// combine afterwards. It is BM25F-STYLE, which is what D21.3 says, and it is
// not BM25F. The difference shows up on a term repeated many times in one field
// — the approximation over-rewards it relative to true BM25F. Naming it
// accurately here is cheaper than discovering the gap during a ranking
// investigation.
type fieldWeight struct {
	Field  string
	Weight float64
}

// fusionFieldWeights is the BM25F-style field weighting: name and path above
// body, per D21.3 ("title and name weigh more than body").
//
// THIS IS THE ONE PLACE FIELD WEIGHTS ARE DECLARED. Fielded indexing (D21.2)
// adds title, headings, property-key and property-value fields; those belong as
// ADDITIONAL ROWS HERE, not in a second table somewhere else. Two weight tables
// would diverge, and the symptom would be a ranking that disagrees with itself
// depending on which one a code path happened to read.
//
// The values are a reasoned starting point, not a tuned result. Whether the
// weighting helps at all is measured in rank_eval_test.go, and if it does not,
// that is the answer.
// prop_key is deliberately ABSENT, and its absence is the one row here that
// needs defending. It is keyword-analysed, so a match against it asks "is the
// whole query string the name of a property this note declares?" — a structural
// question. Giving it a weight would mean a free-text search for "status"
// ranked every note that HAS a status above notes that discuss status, which is
// a different question answered confidently. It stays reachable through
// SearchField, where the caller has asked it on purpose.
//
// fieldPath is deliberately ABSENT too, and for a measured reason rather than a
// taste one. It is keyword-analysed, so a match query against it asks whether
// the WHOLE query string equals the whole path. Measured on the committed query
// set, a path clause returned 0 hits for every multi-term query — it was pure
// dead weight in the disjunction, contributing nothing but a larger query norm.
// A weight on a field that cannot match is not a small effect; it is a row that
// makes the table look more thorough than the ranking is.
//
// # How boost actually behaves in bleve, measured rather than assumed
//
// A SINGLE-CLAUSE boosted query is normalised by its own boost, so its score is
// byte-identical at boost 1 and boost 100. Read on its own that looks exactly
// like "SetBoost does nothing", and it cost most of an afternoon to not report
// it as such. In a DISJUNCTION the relative boosts do decide the order — the
// crossover on a two-field probe sits between 0.5x and 1x and saturates by 10x.
//
// The consequence for tuning: only RATIOS between these rows mean anything, and
// the useful range is narrow. Scaling every row by a constant is a no-op, and
// anything past roughly 10x is indistinguishable from 100x.
var fusionFieldWeights = []fieldWeight{
	{Field: fieldTitle, Weight: 3.0},
	{Field: fieldName, Weight: 3.0},
	{Field: fieldHeadings, Weight: 2.0},
	{Field: fieldPropValue, Weight: 1.5},
	{Field: fieldBody, Weight: 1.0},
}

// bm25fPool runs the weighted-field retriever and returns the candidate pool,
// collapsed to one hit per note (FR-034a) and ordered best first.
//
// It deliberately does not go through Index.SearchFiltered: that method builds
// an UNWEIGHTED disjunction, which is the plain-BM25 baseline the fusion is
// measured against. Sharing one query builder between the baseline and the
// treatment would make the comparison measure nothing.
func (ix *Index) bm25fPool(query string, size int, keep func(string) bool) ([]IndexHit, error) {
	if size <= 0 {
		size = FusionPoolSize
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}

	qs := make([]bleveQuery.Query, 0, len(fusionFieldWeights))
	for _, fw := range fusionFieldWeights {
		mq := bleveQuery.NewMatchQuery(q)
		mq.SetField(fw.Field)
		mq.SetBoost(fw.Weight)
		qs = append(qs, mq)
	}

	// Over-fetch: the pool is per-NOTE but bleve ranks per-SEGMENT, so a
	// collection of segmented notes yields fewer notes than segments. Four times
	// is the same ratio SearchFiltered's escalating loop starts at.
	fetch := size * 4
	if fetch > indexSearchMaxFetch {
		fetch = indexSearchMaxFetch
	}

	req := bleve.NewSearchRequestOptions(bleveQuery.NewDisjunctionQuery(qs), fetch, 0, false)
	req.Fields = []string{fieldPath, fieldKind, fieldOffset}
	req.SortBy([]string{"-_score", "_id"}) // deterministic ties (FR-046)

	res, err := ix.idx.Search(req)
	if err != nil {
		return nil, fmt.Errorf("knowledge: bm25f pool %q: %w", query, err)
	}

	hits := make([]IndexHit, 0, len(res.Hits))
	for _, h := range res.Hits {
		relPath, ordinal := splitSegmentDocID(h.ID)
		hit := IndexHit{Path: relPath, Score: h.Score, Segment: ordinal, Kind: ScanKindNote}
		if v, ok := h.Fields[fieldPath].(string); ok && v != "" {
			hit.Path = v
		}
		if v, ok := h.Fields[fieldKind].(string); ok && v != "" {
			hit.Kind = ScanKind(v)
		}
		if v, ok := h.Fields[fieldOffset].(float64); ok {
			hit.Offset = int64(v)
		}
		if keep != nil && !keep(hit.Path) {
			continue
		}
		hits = append(hits, hit)
	}
	collapsed := collapseSegmentHits(hits)
	if len(collapsed) > size {
		collapsed = collapsed[:size]
	}
	return collapsed, nil
}

// pathsOf projects hits to their paths, preserving order.
func pathsOf(hits []IndexHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Path)
	}
	return out
}

// nameMatchTier grades how well a query matches a note's name, higher is
// better, zero meaning no match at all.
//
// The tiers are ordinal, not scores — they only ever decide an ORDER that RRF
// then converts to reciprocal ranks, so their magnitudes never reach the fused
// score. That is deliberate: a "3" here does not have to be three times as good
// as a "1", which is exactly the normalisation problem RRF exists to avoid.
//
// Unstemmed and case-folded only — see this file's FR-116 decision.
//
// # This was written wrong first, and the wrong version was silent
//
// The obvious implementation compares the WHOLE query string against the name:
// equal, prefix, or substring. It is what "exact / prefix name match" reads
// like, and it is dead on arrival — a real query is a phrase ("what did we
// decide about lowfield ledger programme pricing"), and a six-term phrase is
// never equal to, a prefix of, or a substring of a three-word note name.
// Measured on the committed query set, that version fired on 0 of 60 queries
// and the ablation row for it was byte-identical to the row without it. It
// would have shipped as a signal that does nothing, and the evidence for it
// doing nothing would have read exactly like evidence that the signal does not
// help.
//
// So the comparison is over TOKENS, and the tiers are the three genuinely
// distinct ways a name can be named inside a longer query:
//
//	3  the query IS the name (order-insensitive)
//	2  the name appears as a contiguous PHRASE inside the query — "I know what
//	   it's called and I typed it, with other words around it"
//	1  every token of the name appears somewhere in the query, out of order
//
// There is deliberately no partial-coverage tier. A signal that fires on "some
// of the name's words appear" is a worse BM25 over the name field, which the
// pool query already runs; the value here is sharpness, and a fuzzy tier would
// spend it.
func nameMatchTier(query, relPath string) int {
	qt := foldTokens(query)
	if len(qt) == 0 {
		return 0
	}
	nt := foldTokens(trimMarkdownExt(path.Base(relPath)))
	if len(nt) == 0 {
		return 0
	}

	if len(qt) == len(nt) && containsAll(qt, nt) {
		return 3
	}
	if containsPhrase(qt, nt) {
		return 2
	}
	if containsAll(qt, nt) {
		return 1
	}
	return 0
}

// containsAll reports whether every token of want appears in have, ignoring
// order and repetition.
func containsAll(have, want []string) bool {
	set := make(map[string]bool, len(have))
	for _, h := range have {
		set[h] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

// containsPhrase reports whether want appears in have as a contiguous run.
func containsPhrase(have, want []string) bool {
	if len(want) == 0 || len(want) > len(have) {
		return false
	}
	for i := 0; i+len(want) <= len(have); i++ {
		ok := true
		for j := range want {
			if have[i+j] != want[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// foldTokens is foldName split into tokens.
func foldTokens(s string) []string {
	f := foldName(s)
	if f == "" {
		return nil
	}
	return strings.Split(f, " ")
}

// foldName normalises a name or query for comparison: lowercase, and every run
// of non-alphanumeric runes collapsed to a single space, trimmed.
//
// This is the one Go-side tokenizer in the ranking path (FR-116). It splits on
// non-alphanumerics like retroTokenize rather than trimming edges like
// bm25Tokenize, because a note name's separators are structural — "2026-Q3
// Pricing" and "2026 Q3 Pricing" name the same thing and an operator will type
// either. It does NOT stem and does NOT drop stopwords; the decision and its
// cost are recorded in this file's header.
func foldName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
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
	return b.String()
}

// nameList ranks the pool by name-match tier, best first, dropping every note
// whose name does not match at all.
//
// Dropping non-matches is what makes this a signal rather than a re-shuffle: a
// list that ranked every pool member would hand a name bonus to notes with no
// name match, and the signal would be indistinguishable from noise.
func nameList(query string, pool []IndexHit) RankedList {
	type scored struct {
		path string
		tier int
		ord  int
	}
	matches := make([]scored, 0, len(pool))
	for i, h := range pool {
		if tier := nameMatchTier(query, h.Path); tier > 0 {
			matches = append(matches, scored{path: h.Path, tier: tier, ord: i})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].tier != matches[j].tier {
			return matches[i].tier > matches[j].tier
		}
		return matches[i].ord < matches[j].ord // BM25F order breaks tier ties
	})
	paths := make([]string, 0, len(matches))
	for _, m := range matches {
		paths = append(paths, m.path)
	}
	return RankedList{Signal: SignalName, Paths: paths}
}

// recencyList ranks the pool by modification time, newest first.
//
// A note whose time is unknown is DROPPED rather than sorted last. Sorting it
// last would still give it a rank, and therefore a small recency bonus, for the
// arbitrary reason that some other note's time was also unknown. Absence of
// evidence earns no credit.
func recencyList(pool []IndexHit, modTime func(relPath string) (time.Time, bool)) RankedList {
	if modTime == nil {
		return RankedList{Signal: SignalRecency}
	}
	type scored struct {
		path string
		at   time.Time
		ord  int
	}
	known := make([]scored, 0, len(pool))
	for i, h := range pool {
		if at, ok := modTime(h.Path); ok {
			known = append(known, scored{path: h.Path, at: at, ord: i})
		}
	}
	sort.SliceStable(known, func(i, j int) bool {
		if !known[i].at.Equal(known[j].at) {
			return known[i].at.After(known[j].at)
		}
		return known[i].ord < known[j].ord
	})
	paths := make([]string, 0, len(known))
	for _, s := range known {
		paths = append(paths, s.path)
	}
	return RankedList{Signal: SignalRecency, Paths: paths}
}

// backlinkList ranks the pool by inbound link count, most-linked first.
//
// A note with zero backlinks is DROPPED, for the same reason recencyList drops
// unknown times: an orphan has no popularity evidence, and ranking it 47th of
// 200 would hand it a bonus earned only by the pool's shape.
func backlinkList(pool []IndexHit, backlinks func(relPath string) int) RankedList {
	if backlinks == nil {
		return RankedList{Signal: SignalBacklinks}
	}
	type scored struct {
		path   string
		degree int
		ord    int
	}
	linked := make([]scored, 0, len(pool))
	for i, h := range pool {
		if d := backlinks(h.Path); d > 0 {
			linked = append(linked, scored{path: h.Path, degree: d, ord: i})
		}
	}
	sort.SliceStable(linked, func(i, j int) bool {
		if linked[i].degree != linked[j].degree {
			return linked[i].degree > linked[j].degree
		}
		return linked[i].ord < linked[j].ord
	})
	paths := make([]string, 0, len(linked))
	for _, s := range linked {
		paths = append(paths, s.path)
	}
	return RankedList{Signal: SignalBacklinks, Paths: paths}
}

// truncate caps a prior's list at depth, so a prior is a bonus for a few
// documents rather than an opinion about all of them. depth <= 0 is
// untruncated.
func truncate(l RankedList, depth int) RankedList {
	if depth > 0 && len(l.Paths) > depth {
		l.Paths = l.Paths[:depth]
	}
	return l
}

// RankSources supplies the two corpus facts the priors need. Both are optional:
// a nil function means that prior contributes nothing, which is a smaller lie
// than inventing a default.
type RankSources struct {
	// ModTime returns a note's modification time, and false when it is unknown.
	ModTime func(relPath string) (time.Time, bool)
	// Backlinks returns a note's inbound resolved-link count.
	Backlinks func(relPath string) int
}

// ManifestSources builds RankSources from an index manifest. The manifest
// already records every indexed note's mtime, so recency costs no extra I/O.
func ManifestSources(m *Manifest) RankSources {
	if m == nil {
		return RankSources{}
	}
	return RankSources{
		ModTime: func(relPath string) (time.Time, bool) {
			e, ok := m.Get(relPath)
			if !ok || e.ModTimeNanos == 0 {
				return time.Time{}, false
			}
			return time.Unix(0, e.ModTimeNanos), true
		},
	}
}

// GraphBacklinks builds the backlink-degree source from a link graph.
func GraphBacklinks(g *LinkGraph) func(string) int {
	if g == nil {
		return nil
	}
	return func(relPath string) int { return len(g.Backlinks(relPath)) }
}

// FusionEnabledByDefault records, in code, the shipping decision D21.3's own
// exit criterion forced.
//
// It is false, and the reason is not that the fusion measured badly. It is that
// the evidence available cannot answer the question the default asks.
//
// FR-113 requires a human-authored, graded query set. None exists. What exists
// — and what rank_eval_test.go builds and commits — is a SELF-GENERATED
// KNOWN-ITEM eval: a sentence is sampled from a note, reduced to a query, and
// the ground truth is the note it came from. That judge is unbiased, because
// relevance is established by provenance rather than by lexical overlap, and it
// is genuinely informative in ONE direction:
//
//   - It CAN detect regression. If the fusion makes the source note harder to
//     find, something is wrong, and that is the direction that protects users.
//   - It CANNOT establish improvement. Known-item retrieval is a different task
//     from graded relevance, and the corpus is constructed so that recency and
//     backlink degree are statistically INDEPENDENT of which note is ground
//     truth (asserted by TestRankEval_GroundTruthIsNotPrivileged). Those two
//     priors therefore carry no signal by construction. The eval measures
//     whether they do harm, not whether they do good.
//
// An eval that cannot prove a property must not be used to authorise it. So the
// halves ship on different evidence, and the split is deliberate:
//
//   - BM25F FIELD WEIGHTING SHIPS ON. Weighting a title above a body mention is
//     established IR, decades old and not our invention. It needs the
//     regression check and nothing more, and it passes it.
//   - THE FOUR-SIGNAL FUSION SHIPS OFF. The code is built, tested and measured;
//     the ablation table is committed as an artifact. What waits is the DEFAULT,
//     because changing every operator's ranking on evidence that cannot speak to
//     the question is precisely the confident-and-unverified move this ADR has
//     already made four times.
//
// Flipping this constant is not the way to turn it on. A real graded query set
// is, and the ablation must be re-run against it.
const FusionEnabledByDefault = false

// RankOptions selects a ranking strategy for one query.
//
// The zero value is the SHIPPED default — BM25F weighting, no fusion — so a
// caller that has not thought about ranking gets the configuration the evidence
// supports rather than the one that is most interesting.
type RankOptions struct {
	// Fusion turns on the D21.3 four-signal RRF fusion. See
	// FusionEnabledByDefault for why this is opt-in.
	Fusion bool
	// Config tunes the fusion. Ignored when Fusion is false. The zero value is
	// DefaultFusionConfig.
	Config FusionConfig
}

// RankedSearch is the ranking entry point callers should use.
//
// With RankOptions{} it is BM25F-weighted retrieval and nothing else, which is
// what ships enabled. With Fusion set it is the full D21.3 composition. Having
// ONE entry point whose zero value is the shipped behaviour is the difference
// between a default and a convention: a convention is what the next caller
// forgets.
func (ix *Index) RankedSearch(query string, limit int, opts RankOptions, src RankSources, keep func(string) bool) ([]IndexHit, error) {
	if limit <= 0 {
		limit = SearchDefaultTopN
	}
	if opts.Fusion {
		return ix.FusedSearch(query, limit, opts.Config, src, keep)
	}
	return ix.bm25fPool(query, limit, keep)
}

// FusedSearch runs the D21.3 four-signal fusion for one query and returns at
// most limit results.
//
// The pool is retrieved once and the three priors reorder it; nothing outside
// the pool can appear in the output. keep is applied inside the pool query, so
// a folder-scoped fusion sees the best pool members INSIDE the folder rather
// than the folder's share of a collection-wide pool — the same reason
// SearchFiltered filters before it limits.
func (ix *Index) FusedSearch(query string, limit int, cfg FusionConfig, src RankSources, keep func(string) bool) ([]IndexHit, error) {
	cfg = cfg.normalized()
	if limit <= 0 {
		limit = SearchDefaultTopN
	}

	pool, err := ix.bm25fPool(query, cfg.PoolSize, keep)
	if err != nil {
		return nil, err
	}
	if len(pool) == 0 {
		return nil, nil
	}

	lists := []RankedList{
		{Signal: SignalBM25F, Paths: pathsOf(pool)},
		truncate(nameList(query, pool), cfg.PriorDepth),
		truncate(recencyList(pool, src.ModTime), cfg.PriorDepth),
		truncate(backlinkList(pool, src.Backlinks), cfg.PriorDepth),
	}

	fused := FuseRRF(lists, cfg)

	// Re-attach the pool's hit metadata: the fusion moves paths around, and the
	// caller still needs each note's best segment and byte offset for FR-050a's
	// query-time excerpt re-read. Losing the offset here would send the excerpt
	// re-read to byte zero of every note, which reads like a truncation bug
	// rather than a ranking one.
	byPath := make(map[string]IndexHit, len(pool))
	for _, h := range pool {
		byPath[h.Path] = h
	}
	out := make([]IndexHit, 0, limit)
	for _, f := range fused {
		h, ok := byPath[f.Path]
		if !ok {
			// Unreachable by construction — every fused path came from the pool.
			//
			// It is an ERROR rather than a skip, and that distinction is load
			// bearing. A `continue` here silently absorbs the exact bug the
			// retriever/prior split exists to prevent: wire a prior as a
			// corpus-wide retriever and it starts nominating documents the
			// query never matched, and this loop would quietly drop them and
			// return a short result list with no explanation. Verified by
			// mutation — with a `continue`, making recency retrieve corpus-wide
			// leaves TestFusedSearch_PriorsCannotIntroduceADocument PASSING.
			return nil, fmt.Errorf(
				"knowledge: rank fusion produced %q, which no retriever returned: "+
					"a prior has been wired as a retriever (see rank.go's header)", f.Path)
		}
		h.Score = f.Score
		out = append(out, h)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}
