// Omnipus — knowledge base search behaviour and honesty (ADR-067 stage 2, unit B4).
//
// This file is US-6 in code: NEVER A CONFIDENTLY INCOMPLETE ANSWER.
//
// The failure it exists to prevent is not a crash. It is a search that quietly
// returns three of thirty real matches while the index is still being built.
// The reader sees three results, has no way to tell they are a fraction, and
// acts on them. ADR-067 D5 states the consequence plainly: "a search returning
// 3 of 30 real hits is indistinguishable from a KB with 3 matches." That is
// unfalsifiable after the fact, which is why the incompleteness has to arrive
// WITH the results rather than near them.
//
// Three requirements, and the shape each one forces:
//
//  1. FR-035 — partial results MUST carry an incompleteness statement IN THE
//     SAME RESPONSE. Not a log line, not a WebSocket frame that may or may not
//     have arrived, not a second field a caller may or may not read. So the hits
//     are UNEXPORTED and the only way to obtain them is
//     SearchResponse.Results(), which returns the report alongside them. A
//     caller physically cannot take the results and leave the honesty behind.
//     (The response still marshals to JSON correctly — see MarshalJSON — so the
//     unexported fields are an API property, not a serialisation trap.)
//
//  2. FR-036 — while the tree is still being walked the total is UNKNOWN, and an
//     unknown total MUST be reported as indeterminate: "12,400 files found so
//     far", never "1,240 of 0" and never "0 of 0". A ratio invented from a
//     denominator nobody has measured is a worse lie than saying nothing. So
//     IndexProgress separates "found so far" from "total", and Ratio() refuses
//     to produce one until the enumeration has finished. It is structurally
//     impossible for this package to emit "0 of 0": BeginIndexing(0) means there
//     was nothing to index and returns the tracker to idle.
//
//  3. FR-037 — a requested result count above the cap is CLAMPED and the
//     clamping is REPORTED. ADR-067 AC-7.3: "clamped, not errored, and the clamp
//     is reported". An agent that asks for 400 and receives 100 with no word
//     about it has been told, in effect, that there were only 100.
//
// Segment collapsing (FR-034a) is the fourth property, and it belongs here as
// much as in index.go: a note big enough to be several index DOCUMENTS must
// present as ONE result scored by its best segment. If it did not, every count
// this file reports — the clamp, the "how many results" a caller sees — would be
// counting segments while claiming to count notes.
//
// WHAT THIS FILE DOES NOT DO. It does not clamp inside Index.Search: that layer
// honours the limit it is given, deliberately, so the clamping decision has
// exactly one home and is reported from it. It does not decide what the UI
// draws; BannerVisible is offered as the D5 banner rule, and it is explicitly
// NOT allowed to suppress the response's own statement.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// SearchDefaultTopN and SearchMaxTopN are ADR-067 D7's stated cost bounds:
// "knowledge_search accepts top_n <= 100 (default 20)". The cap is what FR-037
// clamps to.
const (
	SearchDefaultTopN = 20
	SearchMaxTopN     = 100
)

// ReconcileBannerDelay is ADR-067 D5's rule for an INCREMENTAL reconcile: "the
// banner appears only if that reconcile exceeds 2 seconds" (US-6 AS-6 — a fast
// unchanged freshness check shows nothing at all).
//
// It governs the UI banner ONLY. It never suppresses the incompleteness
// statement carried by a search response: FR-035 has no time threshold, and a
// response that withheld the statement for two seconds would be exactly the
// confidently-incomplete answer US-6 exists to forbid.
const ReconcileBannerDelay = 2 * time.Second

// IndexPhase is the closed set of states an index build can be in.
type IndexPhase string

const (
	// IndexPhaseIdle — no build in flight. What is in the index is everything
	// there is.
	IndexPhaseIdle IndexPhase = "idle"
	// IndexPhaseEnumerating — the tree is being walked to establish the total.
	// The total is UNKNOWN in this phase, and FR-036 forbids inventing one.
	IndexPhaseEnumerating IndexPhase = "enumerating"
	// IndexPhaseIndexing — the total is known and files are being indexed. This
	// is the only phase in which a ratio exists.
	IndexPhaseIndexing IndexPhase = "indexing"
)

// Presentation is what a surface should show for a collection, with the
// precedence US-6 AS-5 requires already applied.
type Presentation string

const (
	// PresentationIndexing — show the indexing state. It OUTRANKS the
	// empty-collection first run: a knowledge base that is both empty and still
	// indexing is being indexed, and telling the operator it is empty would be a
	// confident answer about a question nobody has finished asking.
	PresentationIndexing Presentation = "indexing"
	// PresentationEmpty — the D13 empty-collection first run. Reachable only
	// once indexing has finished and the corpus is genuinely empty.
	PresentationEmpty Presentation = "empty"
	// PresentationReady — an indexed, non-empty collection.
	PresentationReady Presentation = "ready"
)

// IndexProgress is the observable state of one collection's index build.
//
// Found and Total are deliberately separate numbers. Found is the enumeration
// counter — how many files the walk has seen — and it exists while the total is
// unknown. Total is a measured denominator and is meaningful ONLY when
// TotalKnown is true. Collapsing the two into one "total" field is precisely
// how "0 of 0" gets shipped.
type IndexProgress struct {
	// Phase is idle, enumerating or indexing.
	Phase IndexPhase `json:"phase"`
	// Found is how many files the enumeration has seen so far. It is the only
	// number available during IndexPhaseEnumerating.
	Found int `json:"found"`
	// Indexed is how many files this run has indexed so far.
	Indexed int `json:"indexed"`
	// Total is the denominator. Meaningless unless TotalKnown.
	Total int `json:"total"`
	// TotalKnown reports whether the enumeration has finished and Total is a
	// measured number rather than a placeholder.
	TotalKnown bool `json:"total_known"`
	// Incremental reports that this run is a reconcile of an already-indexed
	// collection, not a first index. Only an incremental run is eligible for
	// D5's banner delay.
	Incremental bool `json:"incremental"`
	// StartedAt is when the current run began; the zero time when idle.
	StartedAt time.Time `json:"started_at,omitempty"`
	// Elapsed is how long the current run has been going; zero when idle.
	Elapsed time.Duration `json:"elapsed"`
	// CorpusEmpty reports that the last completed run left the collection with
	// no indexable files.
	CorpusEmpty bool `json:"corpus_empty"`
}

// InFlight reports whether an index build is running.
//
// The UNSET phase counts as not-in-flight. IndexPhase is a string, so its zero
// value is "", which is not IndexPhaseIdle — a caller that builds an
// IndexProgress without a tracker (any host with no progress source wired) would
// otherwise be told a build is running, forever, and no later event can clear it
// because nothing is running to finish.
//
// This is not a defensive nicety: the gateway's own phase switch already maps an
// unrecognised phase to idle (knowledge_lifecycle.go, the default branch), so
// without this line the domain predicate and the wire layer disagree about the
// meaning of the same zero value. The search path cannot reach that state today
// — NewSearcher refuses a nil tracker with ErrNoProgressSource and the only
// tracker constructor stamps IndexPhaseIdle — but Presentation, BannerVisible
// and every future caller are one unpopulated struct away from it.
func (p IndexProgress) InFlight() bool {
	return p.Phase != IndexPhaseIdle && p.Phase != ""
}

// Ratio returns done, total and whether a ratio may be stated at all.
//
// ok is false whenever the total is unknown — FR-036. It is also false when the
// total is zero, so no caller can ever render "0 of 0" out of this package.
func (p IndexProgress) Ratio() (done, total int, ok bool) {
	if !p.InFlight() || !p.TotalKnown || p.Total <= 0 {
		return 0, 0, false
	}
	return p.Indexed, p.Total, true
}

// BannerVisible applies ADR-067 D5's banner rule (US-6 AS-6).
//
// A first index always shows. An incremental reconcile shows only once it has
// exceeded ReconcileBannerDelay, so an unchanged collection that reconciles in
// milliseconds produces no banner at all.
//
// This is a UI rule. It does NOT gate the incompleteness statement in a search
// response — see SearchResponse.
func (p IndexProgress) BannerVisible() bool {
	if !p.InFlight() {
		return false
	}
	if !p.Incremental {
		return true
	}
	return p.Elapsed >= ReconcileBannerDelay
}

// Presentation applies US-6 AS-5's precedence: indexing outranks empty.
func (p IndexProgress) Presentation() Presentation {
	if p.InFlight() {
		return PresentationIndexing
	}
	if p.CorpusEmpty {
		return PresentationEmpty
	}
	return PresentationReady
}

// ProgressTracker records the phase of one collection's index build so a search
// issued during that build can tell the truth about it.
//
// It is deliberately a separate object from Index rather than a field on it.
// The indexer's job is to index; making it the owner of the progress state would
// mean every future caller of Sync had to remember to publish progress, and
// forgetting would produce a searcher that silently reports completeness. Here
// the searcher CANNOT be constructed without one (NewSearcher rejects a nil
// tracker), so "nobody wired the progress up" is a build-time failure rather
// than a confidently wrong answer at runtime.
//
// Safe for concurrent use: the indexing goroutine writes, search goroutines read.
type ProgressTracker struct {
	mu  sync.Mutex
	now func() time.Time
	p   IndexProgress
}

// NewProgressTracker returns an idle tracker on the real clock.
func NewProgressTracker() *ProgressTracker {
	return NewProgressTrackerWithClock(time.Now)
}

// NewProgressTrackerWithClock returns an idle tracker on the supplied clock.
// Tests inject a clock so D5's two-second banner threshold is asserted by
// counting time, not by sleeping through it.
func NewProgressTrackerWithClock(now func() time.Time) *ProgressTracker {
	if now == nil {
		now = time.Now
	}
	return &ProgressTracker{now: now, p: IndexProgress{Phase: IndexPhaseIdle}}
}

// BeginEnumeration starts a run in the enumeration phase: the tree is being
// walked and the total is not yet known.
//
// incremental marks a reconcile of an already-indexed collection, which is what
// makes it eligible for D5's banner delay.
func (t *ProgressTracker) BeginEnumeration(incremental bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.p = IndexProgress{
		Phase:       IndexPhaseEnumerating,
		Incremental: incremental,
		StartedAt:   t.now(),
		CorpusEmpty: t.p.CorpusEmpty,
	}
}

// SetFound records how many files the enumeration has seen SO FAR. It is an
// absolute count, not an increment, and it is not a denominator.
func (t *ProgressTracker) SetFound(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n < 0 {
		n = 0
	}
	t.p.Found = n
}

// BeginIndexing moves the run into the indexing phase with a measured total.
//
// total <= 0 means the enumeration found nothing to index — an unchanged
// incremental reconcile is the ordinary case — and returns the tracker to idle
// rather than entering a phase whose only honest rendering would be "0 of 0"
// (FR-036). This is why that string cannot be produced by this package.
func (t *ProgressTracker) BeginIndexing(total int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if total <= 0 {
		t.p.Phase = IndexPhaseIdle
		t.p.Total = 0
		t.p.TotalKnown = false
		t.p.Indexed = 0
		t.p.StartedAt = time.Time{}
		return
	}
	t.p.Phase = IndexPhaseIndexing
	t.p.Total = total
	t.p.TotalKnown = true
	t.p.Indexed = 0
}

// SetIndexed records how many files this run has indexed so far. An absolute
// count, clamped to the known total so a ratio can never exceed 1.
func (t *ProgressTracker) SetIndexed(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n < 0 {
		n = 0
	}
	if t.p.TotalKnown && n > t.p.Total {
		n = t.p.Total
	}
	t.p.Indexed = n
}

// Finish ends the run. corpusEmpty records whether the collection turned out to
// hold no indexable files, which is what US-6 AS-5's precedence needs in order
// to distinguish "empty" from "not finished looking".
func (t *ProgressTracker) Finish(corpusEmpty bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.p = IndexProgress{Phase: IndexPhaseIdle, CorpusEmpty: corpusEmpty}
}

// Progress returns a snapshot, with Elapsed computed against the clock.
func (t *ProgressTracker) Progress() IndexProgress {
	t.mu.Lock()
	defer t.mu.Unlock()
	p := t.p
	if p.Phase != IndexPhaseIdle && !p.StartedAt.IsZero() {
		p.Elapsed = t.now().Sub(p.StartedAt)
		if p.Elapsed < 0 {
			p.Elapsed = 0
		}
	}
	return p
}

// SearchReport is the honesty half of a search response: everything about the
// answer that is not one of the results.
//
// It carries THREE kinds of "this is not all of it" — an index that is still
// being built (FR-035, FR-036), a result count that was clamped (FR-037), and
// (FIX F7) a search that stopped at the raw-fetch safety ceiling before it
// could tell whether more matches existed — because a caller that has to
// remember to check three separate places will eventually check one.
type SearchReport struct {
	// Complete reports that the index held every indexable file at query time
	// AND that the search itself examined every raw match, never stopping at
	// indexSearchMaxFetch with unexamined hits still on the table (FIX F7).
	// When it is false the answer is a fraction of the truth.
	Complete bool `json:"complete"`
	// Indeterminate reports FR-036's state: the tree is still being walked, so
	// how much is missing is not merely unfinished but UNMEASURED. When this is
	// true there is no ratio and none may be invented.
	Indeterminate bool `json:"indeterminate"`
	// Found is the enumeration's running count. Meaningful when Indeterminate.
	Found int `json:"found"`
	// Indexed and Total are the ratio. Both are zero unless Ratio reports ok.
	Indexed int `json:"indexed"`
	Total   int `json:"total"`
	// Clamped reports FR-037: the requested result count exceeded the cap.
	Clamped bool `json:"clamped"`
	// RequestedTopN is what the caller asked for; AppliedTopN is what was used;
	// MaxTopN is the cap. All three are reported so the caller can see the
	// clamp rather than infer it from a short result list.
	RequestedTopN int `json:"requested_top_n"`
	AppliedTopN   int `json:"applied_top_n"`
	MaxTopN       int `json:"max_top_n"`
	// FetchTruncated is FIX F7: Index.SearchFiltered's escalating fetch loop
	// hit its raw-hit safety ceiling with more matches unexamined and fewer
	// than the requested count found. It is INDEPENDENT of the index-build
	// ratio above — a fully-built, idle index can still set this, when a
	// narrow `keep` filter (a folder scope, in production) is thin enough
	// that the matches it accepts rank below the ceiling. When this is true
	// there is no "N of M" ratio to state, because the total that WOULD have
	// matched was never counted — only that more existed than were looked at.
	FetchTruncated bool `json:"fetch_truncated"`
	// Statement is the human-readable sentence(s) describing everything above.
	// It is EMPTY only when the answer is complete and nothing was clamped —
	// which is US-6 AS-4: a finished index shows no incompleteness notice.
	Statement string `json:"statement,omitempty"`
}

// Ratio returns indexed, total and whether a ratio may be stated. It mirrors
// IndexProgress.Ratio so no caller has to reconstruct FR-036's rule.
func (r SearchReport) Ratio() (indexed, total int, ok bool) {
	if r.Complete || r.Indeterminate || r.Total <= 0 {
		return 0, 0, false
	}
	return r.Indexed, r.Total, true
}

// SearchResponse is the result of one search.
//
// The hits are unexported ON PURPOSE. FR-035 requires the incompleteness
// statement to arrive in the same response as the results, and the only way to
// make that structurally true in Go is to make the results unreachable without
// it: Results() hands back both, together, or nothing. An exported Hits field
// beside an exported Report field satisfies the letter of "same response" and
// none of its intent, because ignoring a struct field costs nothing.
type SearchResponse struct {
	hits   []IndexHit
	report SearchReport
}

// Results returns the hits and the report that qualifies them. This is the ONLY
// way to obtain the hits.
func (r SearchResponse) Results() ([]IndexHit, SearchReport) {
	return r.hits, r.report
}

// Len is the number of results, for a caller that needs a count without taking
// the results themselves. It carries no honesty risk: a count alone cannot be
// mistaken for an answer.
func (r SearchResponse) Len() int { return len(r.hits) }

// searchHitJSON and searchResponseJSON are the JSON shape of a SearchResponse.
//
// They exist so the unexported fields above cannot become a silent serialisation
// bug: without them, json.Marshal of a SearchResponse would produce "{}" — an
// empty answer indistinguishable from a legitimate no-results response, which is
// the same class of failure this whole file is about. The hit shape is spelled
// out here rather than reusing IndexHit directly because IndexHit carries no
// json tags, so its field names would land on the wire as Go identifiers and
// drift silently the day one is renamed.
//
// This is NOT a cross-boundary wire type in the Hard Constraint #8 sense — the
// gateway/SPA contract for a knowledge search is generated from contracts/ and
// mapped by the tool and REST layers. This is a debuggable, lossless rendering
// of an in-process value.
type searchHitJSON struct {
	Path    string   `json:"path"`
	Kind    ScanKind `json:"kind"`
	Score   float64  `json:"score"`
	Offset  int64    `json:"offset"`
	Segment int      `json:"segment"`
}

type searchResponseJSON struct {
	Hits   []searchHitJSON `json:"hits"`
	Report SearchReport    `json:"report"`
}

// MarshalJSON emits hits and report together.
func (r SearchResponse) MarshalJSON() ([]byte, error) {
	hits := make([]searchHitJSON, 0, len(r.hits))
	for _, h := range r.hits {
		hits = append(hits, searchHitJSON{
			Path: h.Path, Kind: h.Kind, Score: h.Score, Offset: h.Offset, Segment: h.Segment,
		})
	}
	return json.Marshal(searchResponseJSON{Hits: hits, Report: r.report})
}

// There is deliberately no UnmarshalJSON. It would have to take a POINTER
// receiver, and Go's encoding/json only reaches a pointer-receiver MarshalJSON
// when it is handed a pointer — so a value passed to json.Marshal would silently
// fall back to the default encoder and emit "{}" for a struct whose fields are
// all unexported. Mixing the two receiver kinds here would therefore reintroduce
// the exact silent-empty-answer failure MarshalJSON exists to prevent, in a form
// that only shows up at whichever call site happens to pass a value. Decoding a
// SearchResponse is not a thing this package needs; emitting one honestly is.

// SearchOptions tunes one query.
type SearchOptions struct {
	// TopN is how many results the caller wants. Zero or negative means
	// SearchDefaultTopN. Anything above SearchMaxTopN is clamped to it and the
	// clamping is reported (FR-037) — never an error (AC-7.3).
	TopN int
	// Folder restricts the answer to one collection-relative subtree, given
	// slash-separated with no leading or trailing slash ("projects/omnipus").
	// Empty means the whole collection.
	//
	// It is applied BEFORE the TopN clamp, deliberately. Filtering the clamped
	// answer instead looks identical on any small fixture and is wrong on
	// every real one: with more matches than the cap, the in-folder hits that
	// ranked below the cap are dropped and never counted, so a folder-scoped
	// search returns a subset while the report attached to it describes the
	// unfiltered set. That is a confidently incomplete answer (US-6, P0).
	Folder string
}

// ErrNoProgressSource is returned by NewSearcher when no ProgressTracker is
// supplied.
//
// It is an error rather than a tolerated nil for one reason: a searcher with no
// progress source would report every answer as complete, including the answers
// given while a first index is a tenth of the way through. That is the exact
// failure US-6 is written against, and it would be invisible. Refusing to build
// turns it into a compile-and-run failure at wiring time.
var ErrNoProgressSource = errors.New("knowledge: a searcher requires a progress tracker (FR-035/FR-036)")

// ErrNoIndex is returned by NewSearcher when no index is supplied.
var ErrNoIndex = errors.New("knowledge: a searcher requires an open index")

// Searcher answers queries against one collection's index and qualifies every
// answer with what was and was not searched.
type Searcher struct {
	ix       *Index
	progress *ProgressTracker
}

// NewSearcher builds a searcher over an open index and a progress tracker.
// Both are required; see ErrNoProgressSource.
func NewSearcher(ix *Index, progress *ProgressTracker) (*Searcher, error) {
	if ix == nil {
		return nil, ErrNoIndex
	}
	if progress == nil {
		return nil, ErrNoProgressSource
	}
	return &Searcher{ix: ix, progress: progress}, nil
}

// Search runs one query.
//
// The results are collapsed to ONE PER NOTE (FR-034a) by the index layer, so
// every count in the report counts notes and not segments. The requested count
// is clamped to SearchMaxTopN and the clamping is reported (FR-037). The report
// travels with the results and cannot be separated from them (FR-035).
func (s *Searcher) Search(query string, opts SearchOptions) (SearchResponse, error) {
	requested := opts.TopN
	applied := requested
	clamped := false
	switch {
	case applied <= 0:
		// Not a clamp: nothing above the cap was asked for. FR-037 reports
		// clamping, and calling a default a clamp would train callers to ignore
		// the flag.
		applied = SearchDefaultTopN
	case applied > SearchMaxTopN:
		applied = SearchMaxTopN
		clamped = true
	}

	hits, truncated, err := s.ix.SearchFiltered(query, applied, folderFilter(opts.Folder))
	if err != nil {
		return SearchResponse{}, err
	}

	report := buildSearchReport(s.progress.Progress(), requested, applied, clamped, truncated)
	return SearchResponse{hits: hits, report: report}, nil
}

// folderFilter builds the per-path predicate for SearchOptions.Folder, or nil
// for "the whole collection".
//
// Membership is by path SEGMENT, so a folder named "keep" never matches a
// sibling named "keeping" — the same trailing-separator rule the containment
// layer applies for the same reason.
func folderFilter(folder string) func(string) bool {
	folder = strings.Trim(strings.TrimSpace(folder), "/")
	if folder == "" {
		return nil
	}
	prefix := folder + "/"
	return func(relPath string) bool { return strings.HasPrefix(relPath, prefix) }
}

// SyncTracked runs a reconcile with the tracker reporting a run in flight for
// its whole duration, so no search issued while it runs can claim completeness.
//
// WHY THIS WRAPPER EXISTS. The tracker's honesty is only as good as the wiring:
// a caller that runs Index.SyncWith and forgets to publish progress produces a
// searcher that reports every mid-index answer as complete — the exact US-6
// failure, and an invisible one. Bundling the two into a single call means there
// is one thing to get right instead of five, and no half-wired state.
//
// The phase is INDETERMINATE throughout, and that is the honest report rather
// than a shortcut: SyncWith walks the tree itself and does not publish a
// running total, so a denominator stated here would be one this package had not
// measured — which is precisely what FR-036 forbids. A caller that can report a
// real ratio should drive the tracker's phase methods directly instead.
func SyncTracked(ctx context.Context, ix *Index, tracker *ProgressTracker, opts SyncOptions) (SyncStats, error) {
	if ix == nil {
		return SyncStats{}, ErrNoIndex
	}
	if tracker == nil {
		return SyncStats{}, ErrNoProgressSource
	}

	// A manifest on disk means this collection has been indexed before, so this
	// run is a reconcile and is eligible for D5's banner delay.
	_, statErr := os.Stat(ix.ManifestPath())
	incremental := statErr == nil

	tracker.BeginEnumeration(incremental)
	stats, err := ix.SyncWith(ctx, opts)
	tracker.SetFound(stats.Scanned)
	tracker.Finish(stats.Scanned == 0)
	return stats, err
}

// buildSearchReport turns a progress snapshot, a clamp decision and (FIX F7) a
// fetch-truncation decision into the report that must accompany the results.
//
// The two "why is this incomplete" causes are independent and both fold into
// Complete: an idle, fully-built index can still be truncated (a thin `keep`
// filter whose matches rank below indexSearchMaxFetch), and an in-flight index
// build can happen alongside a search that ALSO truncated. Neither is allowed
// to hide the other.
func buildSearchReport(p IndexProgress, requested, applied int, clamped, truncated bool) SearchReport {
	r := SearchReport{
		Complete:       !p.InFlight() && !truncated,
		RequestedTopN:  requested,
		AppliedTopN:    applied,
		MaxTopN:        SearchMaxTopN,
		Clamped:        clamped,
		FetchTruncated: truncated,
	}
	// The index-build ratio/indeterminate state is a property of p alone, not
	// of r.Complete — r.Complete can now be false purely because the search
	// truncated, and that must NOT make this branch invent an index-build
	// ratio (or an indeterminate "still scanning" line) for a build that
	// isn't running.
	if p.InFlight() {
		if indexed, total, ok := p.Ratio(); ok {
			r.Indexed, r.Total = indexed, total
		} else {
			// FR-036: the total is not known. Report what IS known — the
			// enumeration's running count — and nothing else.
			r.Indeterminate = true
			r.Found = p.Found
		}
	}
	r.Statement = composeStatement(r)
	return r
}

// composeStatement writes the sentence a reader cannot misinterpret.
//
// The indeterminate form never contains a ratio, because there is no
// denominator to put in one (FR-036). FIX F7's fetch-truncation sentence is a
// separate clause from the index-build one, because the two causes can be
// true independently (see buildSearchReport) and a reader needs to know WHICH
// applies: "the index isn't finished yet" has a different remedy (wait) from
// "the search itself gave up before finishing" (narrow the query or scope).
func composeStatement(r SearchReport) string {
	parts := make([]string, 0, 3)
	switch {
	case r.Indeterminate:
		parts = append(parts, fmt.Sprintf(
			"These results are incomplete: this collection is still being scanned, %d files found so far.",
			r.Found))
	case r.Total > 0:
		// r.Total is only ever populated from the p.InFlight() branch above,
		// so reaching this case (rather than falling through to nothing, the
		// way a fully-built, non-truncated search does) means an index build
		// really is in progress with a measured ratio.
		parts = append(parts, fmt.Sprintf(
			"These results are incomplete: %d of %d notes indexed so far.",
			r.Indexed, r.Total))
	}
	if r.FetchTruncated {
		parts = append(parts, fmt.Sprintf(
			"This search stopped after examining the top %d matches by relevance; more may exist beyond that scope and were never considered. Narrow the query or the folder scope to bring them into range.",
			indexSearchMaxFetch))
	}
	if r.Clamped {
		parts = append(parts, fmt.Sprintf(
			"The requested result count of %d was clamped to the maximum of %d.",
			r.RequestedTopN, r.MaxTopN))
	}
	return strings.Join(parts, " ")
}
