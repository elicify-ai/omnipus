// Tests for search behaviour and honesty (ADR-067 unit B4).
//
// Every oracle below comes from the specification, never from what the code
// happens to do:
//
//	FR-035  partial results carry an incompleteness statement in the SAME response
//	FR-036  an unknown total is reported as indeterminate — a count found so far,
//	        never a ratio, never "0 of 0"
//	FR-037  a requested count above the cap is clamped AND the clamping is reported
//	FR-034a segment hits collapse to ONE result per note, so every count here
//	        counts notes and not index documents
//	US-6 AS-4  a finished index shows no incompleteness statement at all
//	US-6 AS-5  the indexing state outranks the empty-collection first run
//	US-6 AS-6  a fast unchanged reconcile shows no banner
//	D7/AC-7.3  top_n <= 100, default 20; above the cap is clamped, NOT errored
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// b4RatioPattern matches "1240 of 98000" — the shape FR-036 forbids while the
// total is unknown. Asserting on the rendered sentence matters because that
// sentence is what a reader actually sees; a struct field they never read
// cannot mislead them.
var b4RatioPattern = regexp.MustCompile(`\d+ of \d+`)

// b4Searcher builds an index over root and a searcher on a tracker under the
// test's control.
func b4Searcher(t *testing.T, home, root string) (*Searcher, *ProgressTracker) {
	t.Helper()
	ix := b2Open(t, home, root)
	b2Sync(t, ix)
	tracker := NewProgressTracker()
	s, err := NewSearcher(ix, tracker)
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	return s, tracker
}

// b4Paths returns the result paths in rank order.
func b4Paths(hits []IndexHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Path)
	}
	return out
}

// ---------------------------------------------------------------------------
// FR-037 — counts above the cap are clamped, and the clamping is REPORTED
// ---------------------------------------------------------------------------

// TestSearchResultCap_ClampedAndReported is spec test 28 and BDD "Requested
// result counts above the cap are clamped and reported":
//
//	Given a knowledge base with 500 matching notes
//	When an agent requests 400 results
//	Then 100 results are returned
//	And the response states that the count was clamped
//
// The fixture size is the spec's, not a convenience: with fewer than the cap of
// matching notes, "exactly 100 results" would be satisfied by an implementation
// that simply ran out of matches, and the test would prove nothing about
// clamping.
//
// The second half is the half that matters. ADR-067 AC-7.3 requires the clamp to
// be "reported rather than silently applied": an agent that asks for 400,
// receives 100 and is told nothing has been told, in effect, that the collection
// holds 100 matches.
func TestSearchResultCap_ClampedAndReported(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	const matching = 500
	for i := 0; i < matching; i++ {
		b2WriteFile(t, root, fmt.Sprintf("notes/n%03d.md", i),
			fmt.Sprintf("note %d about zarquonclamp and other matters", i))
	}
	s, tracker := b4Searcher(t, home, root)
	tracker.Finish(false) // fully indexed: nothing but the clamp may be reported

	resp, err := s.Search("zarquonclamp", SearchOptions{TopN: 400})
	if err != nil {
		// AC-7.3: clamped, NOT errored.
		t.Fatalf("Search(top_n=400) returned an error %v; the cap must clamp, never refuse", err)
	}
	hits, report := resp.Results()

	if len(hits) != SearchMaxTopN {
		t.Fatalf("got %d results for top_n=400 over %d matching notes, want exactly %d (the cap)",
			len(hits), matching, SearchMaxTopN)
	}
	if !report.Clamped {
		t.Error("report.Clamped = false; FR-037 requires the clamping to be reported, not silently applied")
	}
	if report.RequestedTopN != 400 {
		t.Errorf("report.RequestedTopN = %d, want 400 (what the caller asked for)", report.RequestedTopN)
	}
	if report.AppliedTopN != SearchMaxTopN {
		t.Errorf("report.AppliedTopN = %d, want %d", report.AppliedTopN, SearchMaxTopN)
	}
	if report.MaxTopN != SearchMaxTopN {
		t.Errorf("report.MaxTopN = %d, want %d", report.MaxTopN, SearchMaxTopN)
	}
	if report.Statement == "" {
		t.Fatal("report.Statement is empty on a clamped response; the clamping must be stated, not left to a boolean nobody prints")
	}
	if !strings.Contains(report.Statement, "400") || !strings.Contains(report.Statement, "100") {
		t.Errorf("report.Statement = %q; it must name both the requested count (400) and the cap (100)", report.Statement)
	}
	// The results themselves must be distinct notes, so "100" counts notes.
	seen := map[string]bool{}
	for _, h := range hits {
		if seen[h.Path] {
			t.Fatalf("duplicate path %q in the clamped result set", h.Path)
		}
		seen[h.Path] = true
	}

	// D7's stated default and cap, at their exact boundaries.
	t.Run("at the cap is not a clamp", func(t *testing.T) {
		resp, err := s.Search("zarquonclamp", SearchOptions{TopN: SearchMaxTopN})
		if err != nil {
			t.Fatal(err)
		}
		hits, report := resp.Results()
		if report.Clamped {
			t.Errorf("top_n = %d (exactly the cap) reported as clamped; only counts ABOVE the cap are clamped", SearchMaxTopN)
		}
		if len(hits) != SearchMaxTopN {
			t.Errorf("got %d results at top_n = %d, want %d", len(hits), SearchMaxTopN, SearchMaxTopN)
		}
	})

	t.Run("one over the cap is a clamp", func(t *testing.T) {
		resp, err := s.Search("zarquonclamp", SearchOptions{TopN: SearchMaxTopN + 1})
		if err != nil {
			t.Fatal(err)
		}
		_, report := resp.Results()
		if !report.Clamped {
			t.Errorf("top_n = %d (one over the cap) was not reported as clamped", SearchMaxTopN+1)
		}
	})

	t.Run("unspecified uses the default and is not a clamp", func(t *testing.T) {
		resp, err := s.Search("zarquonclamp", SearchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		hits, report := resp.Results()
		if len(hits) != SearchDefaultTopN {
			t.Errorf("got %d results with no top_n, want the default %d", len(hits), SearchDefaultTopN)
		}
		if report.Clamped {
			t.Error("applying the DEFAULT was reported as a clamp; FR-037 reports counts above the cap, and crying clamp on every call teaches callers to ignore the flag")
		}
		if report.Statement != "" {
			t.Errorf("report.Statement = %q for a complete, unclamped search, want empty", report.Statement)
		}
	})
}

// ---------------------------------------------------------------------------
// FR-036 — an unknown total is indeterminate, never a ratio
// ---------------------------------------------------------------------------

// TestProgress_EnumerationHasNoRatio is spec test 29 and BDD "An unknown total
// is not shown as a ratio":
//
//	Given a knowledge base whose file tree is still being walked
//	When the operator searches
//	Then the response states a count found so far
//	And it does not state a ratio
//
// The trap this catches is the natural implementation: one "total" field that
// starts at zero, producing "0 of 0" or "1240 of 0" — a ratio computed from a
// denominator nobody has measured. ADR-067 D5 names both forbidden renderings.
func TestProgress_EnumerationHasNoRatio(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "a.md", "alpha zarquonwalk")
	b2WriteFile(t, root, "b.md", "bravo zarquonwalk")
	s, tracker := b4Searcher(t, home, root)

	tracker.BeginEnumeration(false)
	tracker.SetFound(12400)

	resp, err := s.Search("zarquonwalk", SearchOptions{TopN: 10})
	if err != nil {
		t.Fatal(err)
	}
	hits, report := resp.Results()

	if len(hits) == 0 {
		t.Fatal("no results during enumeration; partial results must still be returned, not withheld")
	}
	if report.Complete {
		t.Error("report.Complete = true while the tree is still being walked")
	}
	if !report.Indeterminate {
		t.Error("report.Indeterminate = false while the total is unknown; FR-036 requires the indeterminate state")
	}
	if report.Found != 12400 {
		t.Errorf("report.Found = %d, want 12400 — the count found SO FAR is the only number available", report.Found)
	}
	if report.Total != 0 {
		t.Errorf("report.Total = %d during enumeration; there is no measured total, so none may be published", report.Total)
	}
	if _, _, ok := report.Ratio(); ok {
		t.Error("report.Ratio() reported ok while the total is unknown; FR-036 forbids stating a ratio here")
	}
	if report.Statement == "" {
		t.Fatal("report.Statement is empty during enumeration; the incompleteness must be stated")
	}
	if !strings.Contains(report.Statement, "12400") {
		t.Errorf("report.Statement = %q, want it to state the 12400 files found so far", report.Statement)
	}
	if b4RatioPattern.MatchString(report.Statement) {
		t.Errorf("report.Statement = %q contains a ratio; FR-036 forbids one while the total is unknown", report.Statement)
	}
	if strings.Contains(report.Statement, "0 of 0") {
		t.Errorf("report.Statement = %q renders the exact string D5 names as forbidden", report.Statement)
	}
}

// TestProgress_ZeroTotalCannotBecomeAZeroOfZeroRatio pins the structural half of
// FR-036: "0 of 0" is not merely avoided by the sentence-writing code, it is
// unreachable, because a run with nothing to index is not a run.
//
// The case is ordinary, not exotic: an incremental reconcile of an unchanged
// collection finds zero changed files, and that is exactly when a naive
// implementation enters the indexing phase with a zero denominator.
func TestProgress_ZeroTotalCannotBecomeAZeroOfZeroRatio(t *testing.T) {
	tracker := NewProgressTracker()
	tracker.BeginEnumeration(true)
	tracker.SetFound(4)
	tracker.BeginIndexing(0) // the enumeration found nothing to do

	p := tracker.Progress()
	if p.InFlight() {
		t.Errorf("phase = %q after BeginIndexing(0); a run with nothing to index must not stay in flight, "+
			"because the only ratio it could state is the forbidden \"0 of 0\"", p.Phase)
	}
	if _, _, ok := p.Ratio(); ok {
		t.Error("Ratio() reported ok with a total of zero")
	}
	if p.BannerVisible() {
		t.Error("BannerVisible() = true with nothing to index; US-6 AS-6 requires a fast unchanged reconcile to show nothing")
	}

	report := buildSearchReport(p, 0, SearchDefaultTopN, false)
	if !report.Complete {
		t.Error("a run with nothing to index left the report incomplete")
	}
	if report.Statement != "" {
		t.Errorf("report.Statement = %q; nothing needed indexing, so there is nothing to warn about", report.Statement)
	}
}

// ---------------------------------------------------------------------------
// FR-035 — the incompleteness statement travels WITH the results
// ---------------------------------------------------------------------------

// TestProgress_PartialResultsCarryIncompleteness is spec test 30 and BDD
// "Partial results are labelled as partial":
//
//	Given a knowledge base whose first index is in progress with a known total
//	When the operator searches
//	Then results are returned
//	And the same response states how many notes of the total are indexed
//
// "The same response" is the whole requirement — ADR-067 AC-5.2 spells out why:
// "not a separate race-prone channel". So this test asserts more than the
// numbers. It asserts that the results and the statement come out of ONE call,
// and that they survive together into JSON, because a wire payload that carried
// the hits and dropped the report would reproduce the failure exactly.
func TestProgress_PartialResultsCarryIncompleteness(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "a.md", "alpha zarquonpartial")
	b2WriteFile(t, root, "b.md", "bravo zarquonpartial")
	s, tracker := b4Searcher(t, home, root)

	tracker.BeginEnumeration(false)
	tracker.BeginIndexing(98000)
	tracker.SetIndexed(1240)

	resp, err := s.Search("zarquonpartial", SearchOptions{TopN: 10})
	if err != nil {
		t.Fatal(err)
	}
	hits, report := resp.Results()

	if len(hits) == 0 {
		t.Fatal("no results mid-index; US-6 requires partial results to be RETURNED and labelled, not withheld")
	}
	if report.Complete {
		t.Fatal("report.Complete = true while indexing is under way")
	}
	if report.Indeterminate {
		t.Error("report.Indeterminate = true although the total is known; a measured total must be reported as a ratio")
	}
	indexed, total, ok := report.Ratio()
	if !ok {
		t.Fatal("report.Ratio() reported not-ok although the total is known")
	}
	if indexed != 1240 || total != 98000 {
		t.Errorf("report.Ratio() = %d of %d, want 1240 of 98000", indexed, total)
	}
	if !strings.Contains(report.Statement, "1240 of 98000") {
		t.Errorf("report.Statement = %q, want it to state \"1240 of 98000\"", report.Statement)
	}

	// The same payload, on the wire — marshalled from a VALUE, which is how any
	// caller will hold it. A pointer-receiver MarshalJSON would be skipped here
	// and the whole response would encode as "{}", a silent empty answer.
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal SearchResponse: %v", err)
	}
	var wire struct {
		Hits   []map[string]any `json:"hits"`
		Report SearchReport     `json:"report"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal SearchResponse: %v", err)
	}
	if len(wire.Hits) != len(hits) {
		t.Errorf("marshalled hits = %d, want %d — a response that serialises its results and loses them is worse than one that fails",
			len(wire.Hits), len(hits))
	}
	if len(wire.Hits) > 0 && wire.Hits[0]["path"] != hits[0].Path {
		t.Errorf("marshalled hits[0].path = %v, want %q", wire.Hits[0]["path"], hits[0].Path)
	}
	if wire.Report.Statement != report.Statement {
		t.Errorf("marshalled statement = %q, want %q — the incompleteness must survive onto the wire with the results",
			wire.Report.Statement, report.Statement)
	}
}

// TestSearchResponse_ResultsCannotBeTakenWithoutTheReport is FR-035's
// "the caller cannot miss it", asserted structurally rather than hoped for.
//
// A response with an exported Hits field beside an exported Report field
// satisfies "same response" on paper and nothing in practice: ignoring a struct
// field costs a caller nothing and is invisible in review. Here the hits are
// unexported and Results() is the only way to reach them, so taking the results
// and leaving the honesty behind is not a discipline anyone has to remember —
// it does not compile.
//
// This test fails the moment someone adds an exported field or a hits-only
// accessor, which is exactly when the guarantee would have quietly ended.
func TestSearchResponse_ResultsCannotBeTakenWithoutTheReport(t *testing.T) {
	rt := reflect.TypeOf(SearchResponse{})
	for i := 0; i < rt.NumField(); i++ {
		if rt.Field(i).IsExported() {
			t.Errorf("SearchResponse has exported field %q; the results must be reachable only through Results(), "+
				"which hands back the incompleteness statement with them (FR-035)", rt.Field(i).Name)
		}
	}

	allowed := map[string]bool{
		"Results":     true, // hits AND report, together, or nothing
		"Len":         true, // a count cannot be mistaken for an answer
		"MarshalJSON": true,
	}
	var got []string
	for _, typ := range []reflect.Type{reflect.TypeOf(SearchResponse{}), reflect.TypeOf(&SearchResponse{})} {
		for i := 0; i < typ.NumMethod(); i++ {
			name := typ.Method(i).Name
			if !contains(got, name) {
				got = append(got, name)
			}
		}
	}
	sort.Strings(got)
	for _, name := range got {
		if !allowed[name] {
			t.Errorf("SearchResponse has exported method %q, which is not in the allow-list; "+
				"any accessor that returns the hits WITHOUT the report reopens the US-6 failure", name)
		}
	}

	// Results must genuinely return both, not a report zero value.
	resp := SearchResponse{
		hits:   []IndexHit{{Path: "a.md"}},
		report: SearchReport{Complete: false, Statement: "incomplete"},
	}
	hits, report := resp.Results()
	if len(hits) != 1 || report.Statement != "incomplete" {
		t.Fatalf("Results() = %v, %+v; want the hits and the report that qualifies them", hits, report)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestSearcher_RefusesToBeBuiltWithoutAProgressSource pins the wiring rule.
//
// A searcher with no progress source reports every answer as complete —
// including the answers it gives while a first index is a tenth of the way
// through. That is not a degraded mode, it is the US-6 failure with no symptom.
// Refusing to construct turns an invisible runtime lie into a loud wiring error.
func TestSearcher_RefusesToBeBuiltWithoutAProgressSource(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "a.md", "alpha")
	ix := b2Open(t, home, root)

	if _, err := NewSearcher(ix, nil); err == nil {
		t.Error("NewSearcher(ix, nil) succeeded; a searcher with no progress source silently claims completeness")
	} else if err != ErrNoProgressSource {
		t.Errorf("NewSearcher(ix, nil) = %v, want ErrNoProgressSource", err)
	}
	if _, err := NewSearcher(nil, NewProgressTracker()); err == nil {
		t.Error("NewSearcher(nil, tracker) succeeded")
	} else if err != ErrNoIndex {
		t.Errorf("NewSearcher(nil, tracker) = %v, want ErrNoIndex", err)
	}
}

// ---------------------------------------------------------------------------
// US-6 AS-4 — a finished index says nothing
// ---------------------------------------------------------------------------

// TestProgress_CompletedIndexingShowsNoIncompletenessNotice is BDD "Completed
// indexing shows no incompleteness notice". A banner that never goes away is a
// banner nobody reads, which would cost the incompleteness statement all its
// value in the one case that matters.
func TestProgress_CompletedIndexingShowsNoIncompletenessNotice(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "a.md", "alpha zarquondone")
	s, tracker := b4Searcher(t, home, root)

	tracker.BeginEnumeration(false)
	tracker.BeginIndexing(1)
	tracker.SetIndexed(1)
	tracker.Finish(false)

	resp, err := s.Search("zarquondone", SearchOptions{TopN: 10})
	if err != nil {
		t.Fatal(err)
	}
	hits, report := resp.Results()
	if len(hits) != 1 {
		t.Fatalf("got %v, want exactly [a.md]", b4Paths(hits))
	}
	if !report.Complete {
		t.Error("report.Complete = false after indexing finished")
	}
	if report.Indeterminate {
		t.Error("report.Indeterminate = true after indexing finished")
	}
	if report.Statement != "" {
		t.Errorf("report.Statement = %q after indexing finished, want empty", report.Statement)
	}
	if tracker.Progress().BannerVisible() {
		t.Error("BannerVisible() = true after indexing finished")
	}
}

// ---------------------------------------------------------------------------
// US-6 AS-5 — indexing outranks the empty-collection first run
// ---------------------------------------------------------------------------

// TestProgress_EmptyVsIndexingPrecedence is spec test 31 and BDD "Indexing state
// outranks the empty-collection first run".
//
// The failure it catches is a first-run screen that says "this collection is
// empty — add your first note" while the index is thirty seconds into walking
// 100,000 files. That is a confident answer to a question nobody has finished
// asking, and it is the same defect as a confidently incomplete search.
func TestProgress_EmptyVsIndexingPrecedence(t *testing.T) {
	cases := []struct {
		name  string
		drive func(*ProgressTracker)
		want  Presentation
	}{
		{
			name:  "still enumerating an as-yet-empty index",
			drive: func(tr *ProgressTracker) { tr.Finish(true); tr.BeginEnumeration(false) },
			want:  PresentationIndexing,
		},
		{
			name: "indexing an as-yet-empty index",
			drive: func(tr *ProgressTracker) {
				tr.Finish(true)
				tr.BeginEnumeration(false)
				tr.BeginIndexing(100000)
			},
			want: PresentationIndexing,
		},
		{
			name:  "finished, and genuinely empty",
			drive: func(tr *ProgressTracker) { tr.Finish(true) },
			want:  PresentationEmpty,
		},
		{
			name:  "finished, with content",
			drive: func(tr *ProgressTracker) { tr.Finish(false) },
			want:  PresentationReady,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewProgressTracker()
			tc.drive(tr)
			if got := tr.Progress().Presentation(); got != tc.want {
				t.Errorf("Presentation() = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// US-6 AS-6 — a fast unchanged reconcile shows no banner
// ---------------------------------------------------------------------------

// TestProgress_FastReconcileShowsNoBannerButStillTellsTheTruth covers BDD "A
// fast unchanged reconcile shows nothing" AND the boundary that must not be
// confused with it.
//
// ADR-067 D5 gives the banner a two-second threshold for an incremental
// reconcile. It gives the SEARCH RESPONSE no threshold at all, and FR-035 has
// none: suppressing the statement for the first two seconds of a reconcile would
// mean every search in that window answered from a partly stale index and said
// nothing. So the two rules are asserted apart, on the same state, because
// collapsing them is the plausible mistake.
//
// The clock is injected. Sleeping through two seconds would make this test slow
// AND flaky, and a test that sleeps 2.1 s to prove a 2 s threshold proves
// nothing about the threshold itself.
func TestProgress_FastReconcileShowsNoBannerButStillTellsTheTruth(t *testing.T) {
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	now := base
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}

	tracker := NewProgressTrackerWithClock(clock)
	tracker.BeginEnumeration(true) // an incremental reconcile
	tracker.BeginIndexing(3)
	tracker.SetIndexed(1)

	// Just under the threshold: no banner.
	advance(ReconcileBannerDelay - time.Millisecond)
	p := tracker.Progress()
	if p.BannerVisible() {
		t.Errorf("BannerVisible() = true after %v of an incremental reconcile; D5 shows the banner only past %v",
			p.Elapsed, ReconcileBannerDelay)
	}
	// ... but the response still tells the truth.
	report := buildSearchReport(p, 0, SearchDefaultTopN, false)
	if report.Complete {
		t.Error("report.Complete = true during a reconcile; the banner threshold must not reach the response")
	}
	if report.Statement == "" {
		t.Error("report.Statement is empty during a quick reconcile; FR-035 has no time threshold, " +
			"and a silent partial answer in the first two seconds is exactly the US-6 failure")
	}

	// At the threshold: banner.
	advance(time.Millisecond)
	if !tracker.Progress().BannerVisible() {
		t.Errorf("BannerVisible() = false at exactly %v of an incremental reconcile, want true", ReconcileBannerDelay)
	}

	// A FIRST index is never delayed — there is no previously indexed corpus to
	// answer from, so every second of silence is a second of confident emptiness.
	first := NewProgressTrackerWithClock(clock)
	first.BeginEnumeration(false)
	if !first.Progress().BannerVisible() {
		t.Error("BannerVisible() = false at the very start of a FIRST index; the delay applies to reconciles only")
	}
}

// ---------------------------------------------------------------------------
// FR-034a at this layer — a segmented note is ONE result, and one result slot
// ---------------------------------------------------------------------------

// b4WriteBulkNote writes a note of at least minBytes made of identical lines,
// each line containing term once. Every segment cut from it is therefore dense
// in that term, which is what makes several of its segments outrank a note that
// mentions the term only once.
func b4WriteBulkNote(t *testing.T, path, term string, minBytes int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriterSize(f, 1<<20)
	line := term + " alpha bravo charlie delta echo foxtrot golf hotel india\n"
	for written := 0; written < minBytes; {
		n, wErr := w.WriteString(line)
		if wErr != nil {
			t.Fatal(wErr)
		}
		written += n
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}

// b4WriteSparseNote writes a note of at least minBytes that mentions term
// exactly once, in its first segment.
func b4WriteSparseNote(t *testing.T, path, term string, minBytes int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriterSize(f, 1<<20)
	if _, err := w.WriteString(term + " mentioned once and never again\n"); err != nil {
		t.Fatal(err)
	}
	line := "alpha bravo charlie delta echo foxtrot golf hotel india juliet\n"
	for written := 0; written < minBytes; {
		n, wErr := w.WriteString(line)
		if wErr != nil {
			t.Fatal(wErr)
		}
		written += n
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}

// TestSearch_SegmentedNoteConsumesOneResultSlot is FR-034a as it shows up in
// this file rather than in the index.
//
// index_test.go already proves the collapse itself. What is unproven there, and
// is this layer's own responsibility, is that the collapse happens BEFORE the
// count the caller asked for is honoured. A note cut into several index
// documents must occupy ONE of the requested slots, not several: the naive
// implementation returns the same note three times and drops the two other notes
// that matched, so the caller is told the collection holds one relevant note
// when it holds three.
//
// THE FIXTURE IS THE TEST. An earlier version of it used one large note and two
// short ones, and it passed even when this layer was rewired to the raw,
// per-segment query — because BM25 normalises by document length, so a forty-byte
// note outranks a half-megabyte segment however many times the term appears in
// it, and the top three raw hits happened to be three different files anyway.
// That is a fixture proving nothing while looking convincing, which is the
// failure mode docs/internal/false-green-patterns.md is about.
//
// So all three notes here are the same size, and only one of them is DENSE in
// the search term. Its segments are then genuinely the three highest-scoring
// documents in the index, and the difference between collapsing and not
// collapsing is the difference between three notes and one note listed three
// times. The fixture's own shape is asserted before the property is, so it fails
// loudly rather than silently stops exercising anything.
func TestSearch_SegmentedNoteConsumesOneResultSlot(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()

	const term = "zarquondense"
	const size = 3 * IndexSegmentSize // several segments each
	b4WriteBulkNote(t, filepath.Join(root, "dense.md"), term, size)
	b4WriteSparseNote(t, filepath.Join(root, "sparse-a.md"), term, size)
	b4WriteSparseNote(t, filepath.Join(root, "sparse-b.md"), term, size)

	ix := b2Open(t, home, root)
	stats := b2Sync(t, ix)
	if stats.Segments < 9 {
		t.Fatalf("Segments = %d for three notes of %d bytes, want at least 9; the notes were not segmented",
			stats.Segments, size)
	}

	tracker := NewProgressTracker()
	tracker.Finish(false)
	s, err := NewSearcher(ix, tracker)
	if err != nil {
		t.Fatal(err)
	}

	// Fixture check: the three highest-scoring INDEX DOCUMENTS must all be
	// segments of dense.md. Without that, "three distinct notes" is true of the
	// raw per-segment answer too, and the test proves nothing.
	raw, _, err := ix.searchRaw(term, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 3 {
		t.Fatalf("only %d raw segment hits; the fixture cannot exercise collapsing", len(raw))
	}
	segs := map[int]bool{}
	for _, h := range raw[:3] {
		if h.Path != "dense.md" {
			t.Fatalf("raw hit %d is %q, want a segment of dense.md; the fixture no longer distinguishes "+
				"a collapsed answer from a per-segment one", len(segs), h.Path)
		}
		segs[h.Segment] = true
	}
	if len(segs) != 3 {
		t.Fatalf("the top three raw hits cover %d distinct segments of dense.md, want 3", len(segs))
	}

	resp, err := s.Search(term, SearchOptions{TopN: 3})
	if err != nil {
		t.Fatal(err)
	}
	hits, report := resp.Results()

	if len(hits) != 3 {
		t.Fatalf("got %d results for top_n=3, want 3: %v", len(hits), b4Paths(hits))
	}
	got := b4Paths(hits)
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
	want := []string{"dense.md", "sparse-a.md", "sparse-b.md"}
	if !reflect.DeepEqual(sorted, want) {
		t.Errorf("results = %v, want the three distinct notes %v; a segmented note must consume ONE slot, not three",
			got, want)
	}
	if got[0] != "dense.md" {
		t.Errorf("top result = %q, want dense.md — the collapsed hit keeps its BEST segment's score", got[0])
	}
	if report.Clamped {
		t.Error("report.Clamped = true for top_n=3, which is far below the cap")
	}
	if report.AppliedTopN != 3 {
		t.Errorf("report.AppliedTopN = %d, want 3", report.AppliedTopN)
	}
}

// ---------------------------------------------------------------------------
// The wiring, end to end
// ---------------------------------------------------------------------------

// TestSyncTracked_ASearchDuringAnIndexCannotClaimCompleteness is FR-035 proved
// against a REAL indexing run rather than a hand-driven tracker.
//
// Every other progress test in this file sets the tracker's state directly,
// which is legitimate — the tracker is production code driven through its
// production API — but leaves one thing unproven: that a real Sync actually
// publishes progress. If nothing did, the tracker would sit idle, every
// mid-index search would report "complete", and every one of those tests would
// still pass. This is that gap closed.
//
// Indexing is suspended mid-run by blocking the package's single file-read seam,
// so the assertion is deterministic rather than a race against a fast index.
func TestSyncTracked_ASearchDuringAnIndexCannotClaimCompleteness(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	for i := 0; i < 6; i++ {
		b2WriteFile(t, root, fmt.Sprintf("n%d.md", i), fmt.Sprintf("note %d zarquonlive", i))
	}
	ix := b2Open(t, home, root)
	tracker := NewProgressTracker()
	s, err := NewSearcher(ix, tracker)
	if err != nil {
		t.Fatal(err)
	}

	// Index once so there is something to find, then suspend a SECOND run.
	if _, syncErr := SyncTracked(context.Background(), ix, tracker, SyncOptions{}); syncErr != nil {
		t.Fatal(syncErr)
	}
	if p := tracker.Progress(); p.InFlight() {
		t.Fatalf("tracker still in flight after SyncTracked returned: phase %q", p.Phase)
	}

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	prev := openFileForRead
	var once sync.Once
	openFileForRead = func(path string) (*os.File, error) {
		if strings.HasSuffix(path, ".md") {
			once.Do(func() {
				entered <- struct{}{}
				<-release
			})
		}
		return prev(path)
	}
	defer func() { openFileForRead = prev }()

	// Force a re-parse of every note so the suspended run has real work.
	syncDone := make(chan error, 1)
	go func() {
		_, deepErr := SyncTracked(context.Background(), ix, tracker, SyncOptions{Deep: true})
		syncDone <- deepErr
	}()

	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the indexing run never reached a note read")
	}

	resp, err := s.Search("zarquonlive", SearchOptions{TopN: 10})
	if err != nil {
		t.Fatal(err)
	}
	hits, report := resp.Results()
	if len(hits) == 0 {
		t.Fatal("no results during a live index; partial results must still be served")
	}
	if report.Complete {
		t.Error("a search issued DURING a real indexing run reported Complete = true — " +
			"nothing published progress, so every mid-index answer is a confident fraction of the truth")
	}
	if report.Statement == "" {
		t.Error("a search issued during a real indexing run carried no incompleteness statement (FR-035)")
	}

	close(release)
	if syncErr := <-syncDone; syncErr != nil {
		t.Fatalf("SyncTracked: %v", syncErr)
	}

	resp, err = s.Search("zarquonlive", SearchOptions{TopN: 10})
	if err != nil {
		t.Fatal(err)
	}
	_, report = resp.Results()
	if !report.Complete {
		t.Error("the search still reported incomplete after indexing finished; a banner that never clears is a banner nobody reads")
	}
	if report.Statement != "" {
		t.Errorf("report.Statement = %q after indexing finished, want empty", report.Statement)
	}
}
