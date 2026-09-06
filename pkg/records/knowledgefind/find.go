// Omnipus — ADR-068 D15.3 / spec 4.1.2, FR-064: the retrieval path and its two bounds.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// TextHit is one result from the text index — the plain-word half of the query.
type TextHit struct {
	Path string
	// SourceHash is the hash the TEXT index holds for this note. FR-020c's
	// comparison is against THIS, not against a manifest entry.
	SourceHash string
	Score      float64
}

// TextSearcher is the bleve half. It is an interface rather than a concrete
// index so this package does not import pkg/knowledge, and so a test can drive
// the whole pipeline without a real index on disk.
type TextSearcher interface {
	// Search returns the ranked hits for a plain-word query, within the caller's
	// already-resolved scope.
	Search(ctx context.Context, words string, limit int) ([]TextHit, error)
	// NearestTerms reports the vocabulary the index actually holds near a term
	// that matched nothing (FR-114). It is what a zero-hit answer reports
	// INSTEAD of broadening the query.
	NearestTerms(ctx context.Context, words string, limit int) ([]generated.VaultTermCount, error)
	// SourceHash returns the hash the TEXT index holds for one note.
	//
	// It exists because FR-020c's freshness comparison is PER RETURNED RECORD
	// and applies to every answer — not only to answers that used `words`. A
	// purely typed query returns rows whose two indexes can disagree just as
	// easily, and checking only the word-search path would leave the commonest
	// query shape unchecked.
	//
	// ok=false means the text index holds no document for that path, which is
	// UNKNOWN freshness and is flagged — never assumed fresh.
	SourceHash(ctx context.Context, path string) (hash string, ok bool, err error)

	// Populated reports whether this text index has completed at least one
	// build pass over its collection, INDEPENDENT of how many documents that
	// pass found. A genuinely empty vault (0 notes on disk) whose index has
	// finished building reports populated=true, and a query that then finds
	// zero hits is an honest zero. A vault that has never been indexed at
	// all — the index opened, never built — reports populated=false, and a
	// query finding zero hits there has not searched anything.
	//
	// This exists to close the exact hole that produced F-9
	// (docs/internal/uat/uat-findings-knowledge-tools-2026-09-01-run2.md):
	// an unbuilt bleve index answers d.Text.Search with zero hits, which is
	// byte-for-byte indistinguishable from a real miss over a searched
	// corpus — so findRecords cannot tell "0 because nothing matched" from
	// "0 because nothing was ever indexed" from Search's return alone. This
	// is the population accessor a caller needs to tell them apart.
	//
	// err != nil means the build state itself could not be read — folded
	// into "not populated" by the caller rather than treated as a third
	// state, because a zero-hit answer this layer cannot even confirm was
	// searched is no more trustworthy than one it positively knows was not.
	Populated(ctx context.Context) (populated bool, err error)
}

// TextIndexFreshness is the richer answer TextFreshnessReporter gives beyond
// Populated's single boolean: not only WHETHER the index reflects the whole
// collection, but — when it does not — whether that is because it was NEVER
// BUILT or because it has DRIFTED since its last build. Those are the two cases
// Populated=false folds together, and they need different words to the caller:
// "index it" versus "re-index it".
type TextIndexFreshness struct {
	// Built is true when a build has completed at least once. False is the
	// never-indexed state.
	Built bool
	// Fresh is true when the index reflects the collection with nothing
	// pending. A genuinely empty, fully-swept vault is Fresh.
	Fresh bool
	// ScannedFiles is what is on disk now; IndexedFiles is what the index
	// reflects; PendingFiles is how many on-disk files the index has not yet
	// caught up with. These make a stale-index message concrete ("reflects 1 of
	// 68 files") rather than a bare "stale".
	ScannedFiles int
	IndexedFiles int
	PendingFiles int
}

// TextFreshnessReporter is an OPTIONAL capability a TextSearcher may implement
// to let a zero-hit refusal — and, in time, any answer — distinguish a STALE
// index from one that was NEVER BUILT, and to say by how much it is behind.
//
// IT IS A SEPARATE INTERFACE RATHER THAN A METHOD ON TextSearcher for the same
// reason ViewFormulaLoader is separate from ViewLoader: widening the required
// interface would silently un-satisfy every existing implementation (the
// production adapter AND every test stub) at once. A searcher that does not
// implement it degrades to Populated's boolean — the pre-existing behaviour —
// with no loss of correctness, only of specificity in the message.
//
// This is A2(d)'s "index-health / freshness" signal made reachable on the
// knowledge_find path: a caller running a zero-hit `words` query over a vault
// whose index is behind is told the index is stale and by how much, instead of
// being told it was never built (wrong) or nothing matched (also wrong).
type TextFreshnessReporter interface {
	IndexFreshness(ctx context.Context) (TextIndexFreshness, error)
}

// ViewLoader resolves a saved view by name (FR-025c). Stage 2's schema owner
// owns the loader; this package consumes it.
type ViewLoader interface {
	// View returns the saved view's request fragment. ok=false means the name is
	// not defined, and the caller refuses listing Names().
	View(name string) (generated.VaultFindRequest, bool)
	// Names lists the saved views in scope, for that refusal.
	Names() []string
}

// ViewFormulaLoader is the OPTIONAL half of ViewLoader: the saved view's
// `formulas:` map (FR-141), which the base interface does not carry because a
// view's request fragment and its computed properties are different things.
//
// IT IS A SEPARATE INTERFACE RATHER THAN A METHOD ON ViewLoader so that adding
// formulas cannot silently un-satisfy an existing loader — records.ViewFindLoader
// implements ViewLoader today and would stop compiling against a widened one at
// a wiring site that does not exist yet, which is a landmine rather than a
// compile error anybody would see.
//
// A loader that does not implement it is a vault with no formulas, which is the
// correct reading of "this view declares none" and is exactly what the
// formula-namespace refusal then says.
type ViewFormulaLoader interface {
	// Formulas returns one view's formula sources, keyed by name, with the
	// expression as SOURCE TEXT (FR-141). ok=false means the view is not
	// defined; an empty map means it defines no formulas.
	Formulas(view string) (map[string]string, bool)
}

// Deps is what knowledge_find needs from its host. Every field is a real dependency
// with a real consumer; there is no field here that exists only to be nil.
type Deps struct {
	// Schemas is the declared record types in the caller's scope.
	Schemas *records.SchemaSet
	// Store is the properties index. It may be nil ONLY on a build where the
	// index cannot be compiled — and on such a build the platform gate refuses
	// before anything reads it.
	Store propindex.Store
	// PathPrefix is FR-060's workspace scope, ALREADY RESOLVED by the caller
	// from the calling agent's workspace. It is never caller text: the model
	// does not get to choose what it can see.
	PathPrefix string
	// Text is the text index. REQUIRED, not optional, and the reason is
	// FR-020c: freshness is compared per returned record against the text
	// index's own hash, so without it no answer can honour the comparison.
	//
	// A nil Text used to mean "skip the check", which is the quiet degradation
	// this package refuses everywhere else — an answer that silently stopped
	// verifying freshness looks exactly like one that verified it and found
	// nothing wrong.
	Text TextSearcher
	// Views resolves saved views. Required when `view` is given.
	Views ViewLoader
	// Resolve maps a wikilink to a record identity (section 8 R-8). Without it
	// relation comparisons report "unresolved" rather than silently comparing
	// link text, which is the honest degradation.
	Resolve records.RelationResolver
	// ResolveNear turns `near`'s note reference (a bare path/name or a
	// [[wikilink]]) into the anchor note's own collection-relative PATH, so the
	// anchor counts as hop 0 EVEN WHEN IT IS AN ORDINARY NOTE with no record
	// identity — the case Resolve cannot serve, because Resolve returns !ok for
	// a note that is not a record (relation_resolver.go's Resolve collapses
	// "exists but has no identity" onto "did not resolve"). near/hops still
	// walks only the TYPED relation graph for hops >= 1; ResolveNear is purely
	// how the origin note itself re-enters the answer at hop 0.
	//
	// Optional: when nil, near falls back to record-graph membership only — the
	// anchor is hop 0 only if it is itself a record — which is the pre-existing
	// behaviour every test that does not wire this relies on.
	ResolveNear func(near string) (path string, ok bool)
	// Epoch is the properties index's generation counter, which a cursor is
	// issued against.
	Epoch int64
	// RenderRows lifts the two bounds that exist for a LANGUAGE-MODEL reader,
	// for an IN-PROCESS RENDERER that is not one. Zero — the default — changes
	// nothing, and no tool path sets it.
	//
	// MaxLimit (200) and ResponseBudgetBytes (4 kB) are both sized for what a
	// model should be handed in one turn. The gateway's view-result endpoint
	// is not a turn: it fills an HTTP response the SPA draws a table from, and
	// under those two bounds it could only collect rows by walking the OFFSET
	// cursor — where every page is a fresh Find() that re-runs the entire
	// evaluation (filter, sort, aggregate over the whole candidate set) and
	// discards everything before the offset. Ten pages cost ten complete
	// evaluations of one query, and the byte budget still trimmed each page,
	// so a few hundred records could not be collected at all.
	//
	// Set to the number of rows the caller can actually take, and this
	// evaluation caps the page there instead of at MaxLimit and skips the byte
	// budget entirely. It is NOT a way to ask for unbounded rows: the caller
	// states its own bound and is answered within it. Nothing else changes —
	// the same query, the same totals over the same full evaluated set, the
	// same cursor when more rows exist than were asked for.
	RenderRows int
	// Now is the instant `now()` and `today()` are evaluated at, snapshotted
	// ONCE for the whole response (FR-146). The zero value means "read the
	// clock when the query starts", which is the same snapshot taken one layer
	// down — it is a default, never a per-candidate clock read.
	Now time.Time
}

// Find answers one query.
//
// IT RETURNS BOTH A RESPONSE AND AN ERROR ON A REFUSAL, and both halves are
// load-bearing — see the note on RefusalError. The response is what the model reads
// and can act on; the error is what stops a caller mistaking a refusal for an
// answer. What it never returns is a successful empty result over a question it
// could not answer.
func Find(ctx context.Context, d Deps, req generated.VaultFindRequest) (generated.VaultFindResponse, error) {
	set := d.Schemas
	if set == nil {
		set = records.NewSchemaSet()
	}

	if d.Text == nil {
		ref := refuse(problem(generated.IndexUnavailable,
			"no text index is wired into this vault, so no answer can be checked for freshness",
			"re-open the vault; run knowledge_describe check_integrity to see the index state"), nil)
		return refusalResponse(req, rawEcho(req), ref), ref
	}

	// BEFORE THE VIEW IS EXPANDED, not after. applyView gates on
	// `req.Type == nil`, so a present-but-blank `type` is non-nil, silently
	// discards the view's own `type:` and then resolves to no schema — running
	// the view's filter against EVERY note in the vault and presenting it as
	// the view's answer, marked complete.
	if r := checkBlankNarrowing(req); r != nil {
		return refusalResponse(req, rawEcho(req), r), r
	}

	if r := applyView(&req, d.Views); r != nil {
		return refusalResponse(req, rawEcho(req), r), r
	}

	q, r := parse(req, set, viewFormulas(d.Views, req.View), d.RenderRows)
	if r != nil {
		// The echo is the RAW request here, not the executable one: parse is the
		// step that failed, so there is no "as executed" form to report. A
		// caller refused for an unknown property still has to be able to see
		// that the tool received the argument they think they sent.
		return refusalResponse(req, rawEcho(req), r), r
	}
	// D5.1 / R-8: grouping by a relation compares by target identity, not by
	// the wikilink's own text, same as the comparator. d.Resolve may be nil —
	// project.go degrades rather than panicking.
	q.resolve = d.Resolve
	echo := q.echo()

	// THE PLATFORM GATE, BEFORE ANY RETRIEVAL.
	//
	// records.RequirePropertyIndex's error is returned UNCHANGED (wrapped with
	// %w so errors.Is still finds it and the platform name survives). Returning
	// a zero value here instead would re-open the exact hole FR-020h exists to
	// close: the operator is told there is nothing to find, when the truth is
	// that the question cannot be answered on this platform.
	for _, capability := range q.capabilities() {
		if err := records.RequirePropertyIndex(capability); err != nil {
			ref := refuse(problem(generated.IndexUnavailable, err.Error(),
				"plain-word search and knowledge_read still work on this build"), err)
			return refusalResponse(req, echo, ref), fmt.Errorf("knowledge_find: %w", err)
		}
	}

	if q.explain {
		return explainResponse(q, echo), nil
	}

	if q.cursor != "" {
		if r := checkCursor(q.cursor, d.Epoch); r != nil {
			return refusalResponse(req, echo, r), r
		}
	}

	if q.kind == KindTask {
		return findTasks(ctx, d, q, echo)
	}
	return findRecords(ctx, d, q, echo)
}

// checkBlankNarrowing refuses a present-but-BLANK `type` or `kind`.
//
// The contract already rules on this for a saved view's own `type:` — "An empty
// string is not 'untyped' — it is a typo for a type name, and treating it as a
// deliberate absence would turn a misspelling into a vault-wide query" — and
// the same sentence is true of the argument. Two code paths disagreed about it:
// applyView treated blank as PRESENT (so the view's narrowing was discarded)
// and resolveType treated it as ABSENT (so nothing narrowed at all). Refusing
// is what makes the two agree, and it is the reading the contract already took.
func checkBlankNarrowing(req generated.VaultFindRequest) *RefusalError {
	blank := func(argument, value, remedy string) *RefusalError {
		p := problem(generated.UnsupportedParameter,
			fmt.Sprintf("%s was given as %q, which is not a name; a blank %s is a typo for one, "+
				"never a deliberate absence", argument, value, argument),
			remedy)
		return refuse(p, nil)
	}
	if req.Type != nil && strings.TrimSpace(*req.Type) == "" {
		return blank("type", *req.Type,
			"omit type entirely to search every note, or name a declared record type — "+
				"call knowledge_describe to see them")
	}
	if req.Kind != nil && strings.TrimSpace(string(*req.Kind)) == "" {
		return blank("kind", string(*req.Kind),
			"omit kind entirely to use the default, or name one of: "+
				strings.Join([]string{KindNote, KindRecord, KindTask, KindAttachment}, ", "))
	}
	return nil
}

// ViewRefusalReporter is the OPTIONAL half of ViewLoader that explains an
// ok=false: the view EXISTS and cannot be carried through this request shape.
//
// ViewLoader.View is (VaultFindRequest, bool) with no field for a reason, so a
// view stored `disabled`, one grouping in descending order and one declaring
// `formulas` all arrive here indistinguishable from a name nobody ever defined
// — and applyView then told the caller "no saved view named X; defined: <every
// other view>", which is flatly false about a view knowledge_configure wrote
// successfully and knowledge_describe still lists.
//
// It is a separate interface for the same reason ViewFormulaLoader is: widening
// ViewLoader would silently un-satisfy an existing implementation at a wiring
// site nobody would see. records.ViewFindLoader already carries exactly this
// method (ServeRefusal), whose own header says "knowledge_describe and
// knowledge_configure hold a *ViewFindLoader and can ask" — this is find
// asking.
type ViewRefusalReporter interface {
	ServeRefusal(name string) (records.ViewServeRefusal, bool)
}

// applyView expands a saved view UNDER the caller's own arguments, so `filter`
// refines the view rather than replacing it (spec 4.1.2: "a saved view, applied
// first; filter refines it").
func applyView(req *generated.VaultFindRequest, loader ViewLoader) *RefusalError {
	if req.View == nil || *req.View == "" {
		return nil
	}
	name := *req.View
	if loader == nil {
		return refuse(problem(generated.UnknownView,
			fmt.Sprintf("this vault has no saved views, so %q cannot be resolved", name),
			"drop the view and write the filter directly, or define the view with knowledge_configure"), nil)
	}
	view, ok := loader.View(name)
	if !ok {
		// EXISTS-BUT-UNSERVABLE IS NOT "DOES NOT EXIST". Asking first is what
		// stops this refusal making a false statement about a view the operator
		// wrote, saw accepted, and can still read back through
		// knowledge_describe.
		if reporter, isReporter := loader.(ViewRefusalReporter); isReporter {
			if refusal, unservable := reporter.ServeRefusal(name); unservable {
				p := problem(generated.UnsupportedParameter,
					fmt.Sprintf("the saved view %q exists but cannot be run through knowledge_find: %s (%s)",
						name, refusal.Reason, refusal.Code),
					refusal.Remedy)
				return refuse(p, nil)
			}
		}
		names := loader.Names()
		sort.Strings(names)
		p := problem(generated.UnknownView,
			fmt.Sprintf("no saved view named %q", name),
			"call knowledge_describe include=views to see the saved views in scope")
		if len(names) > 0 {
			p.Reason += "; defined: " + strings.Join(names, ", ")
			p.Permitted = &names
		}
		return refuse(p, nil)
	}

	// The caller's own arguments WIN over the view's. A view that could
	// overwrite an explicit argument would silently answer a different question
	// from the one asked.
	if req.Type == nil {
		req.Type = view.Type
	}
	if req.Kind == nil {
		req.Kind = view.Kind
	}
	if req.Sort == nil {
		req.Sort = view.Sort
	}
	if req.Select == nil {
		req.Select = view.Select
	}
	if req.GroupBy == nil {
		req.GroupBy = view.GroupBy
	}
	if req.Join == nil {
		req.Join = view.Join
	}
	if req.Aggregate == nil {
		req.Aggregate = view.Aggregate
	}
	// THE VIEW'S OWN PAGE SIZE. It was computed by the bridge
	// (translateViewMechanical copies def.Limit into the request), rendered by
	// knowledge_describe and documented — and then dropped here, so a `limit: 5`
	// view returned fifty rows while three surfaces stated five.
	if req.Limit == nil {
		req.Limit = view.Limit
	}
	switch {
	case view.Filter == nil:
		// nothing to compose
	case req.Filter == nil:
		req.Filter = view.Filter
	default:
		// BOTH are present: the answer is the INTERSECTION. Replacing the view's
		// filter with the caller's would widen the result set beyond what the
		// view defines, which is the opposite of "refines".
		req.Filter = &generated.VaultFilterNode{
			All: &[]generated.VaultFilterNode{*view.Filter, *req.Filter},
		}
	}
	return nil
}

// checkCursor refuses a cursor issued against a different index generation.
// FR-020c: an unhonourable cursor is an ERROR, never a silent restart — a silent
// restart returns page one while the caller believes it is reading page four.
func checkCursor(cursor string, epoch int64) *RefusalError {
	off, issued, ok := decodeCursor(cursor)
	if !ok {
		return refuse(problem(generated.StaleCursor,
			fmt.Sprintf("the cursor %q was not issued by this system", cursor),
			"re-run the query without a cursor"), nil)
	}
	if issued != epoch {
		return refuse(problem(generated.StaleCursor,
			fmt.Sprintf("that cursor was issued against index_epoch %d; the index is now at %d",
				issued, epoch),
			"re-run the query — the corpus changed underneath the page boundary"), nil)
	}
	_ = off
	return nil
}

// ---------------------------------------------------------------------------
// THE TWO BOUND REMEDIES, STATED ONCE
//
// B2's remedy used to end "or ask for a total instead", and the refusal's NEXT
// block issued exactly that call: `knowledge_find aggregate=[{op:count}]`. It
// is a GUARANTEED LOOP. B2 is counted inside the store's flush, unconditionally
// on every Accepted verdict, with no exemption parameter on Candidates — so an
// aggregate-only query streams the identical candidates through the identical
// counter and receives the identical refusal, with the identical advice. B1 is
// worse still: it is a COUNT taken before any candidate is read, so an
// aggregate-only re-run does not even reach a different code path.
//
// Both remedies now name only things that change the number that fired.
// ---------------------------------------------------------------------------
const (
	// narrowedCandidateRemedy answers B1, which counts the narrowed candidate
	// POPULATION. A filter does not change that number, so it is not offered.
	narrowedCandidateRemedy = "narrow the scope to a collection or path, or narrow the kind"

	// candidateCapRemedy answers B2, which counts SURVIVORS. Both a tighter
	// filter and a narrower scope reduce it.
	candidateCapRemedy = "add or tighten a filter, or narrow the scope to a collection or path — " +
		"an aggregate-only query streams the same candidates through the same bound and is refused the same way"
)

// findRecords is the note/record path: narrow, bound, stream, decide in Go.
func findRecords(ctx context.Context, d Deps, q *query, echo string) (generated.VaultFindResponse, error) {
	sel := q.selector(d.PathPrefix)

	// The plain-word half runs FIRST when it is asked for, because it produces a
	// PATH SET the typed half then intersects. The answer is the intersection,
	// never the union: a caller who asked for both and received either is being
	// told something false about their vault.
	var wordPaths map[string]TextHit
	// wordHits keeps the hits IN RANK ORDER as well as by path. The map is what
	// the typed half intersects against; the slice is what the text-only path
	// below returns when there is no typed half to intersect with.
	var wordHits []TextHit
	var wordsTruncated bool
	if q.words != "" {
		// FIX F6 (code review A): ask for ONE MORE than the fanout. A real
		// text index has no way to say "there were more" other than by
		// actually handing back more than was asked for — Search's own
		// contract (see TextSearcher) returns at most `limit` hits and is
		// silent about whether the corpus held any past it. Asking for
		// fanout+1 turns that silence into a fact this layer can observe:
		// getting back the (fanout+1)-th hit proves the corpus held more
		// matches than the fanout could carry, and the typed filter below
		// never got a chance to see them.
		fanout := textFanout(q.limit)
		hits, err := d.Text.Search(ctx, q.words, fanout+1)
		if err != nil {
			ref := refuse(problem(generated.IndexUnavailable,
				fmt.Sprintf("the text index could not answer %q: %v", q.words, err),
				"re-run, or run knowledge_describe check_integrity to see the index state"), err)
			return refusalResponse(generated.VaultFindRequest{}, echo, ref), ref
		}
		if len(hits) > fanout {
			// The corpus held more than the fanout. wordPaths — the set the
			// typed filter intersects against — is being built from a PREFIX
			// of the real match set, never the whole of it, so this answer
			// can no longer claim to be complete no matter what the typed
			// filter and the evaluation below find. Reported below (see
			// ev.recordProblems), never assumed away.
			wordsTruncated = true
			hits = hits[:fanout]
		}
		wordHits = hits
		wordPaths = make(map[string]TextHit, len(hits))
		for _, h := range hits {
			wordPaths[h.Path] = h
		}
		// A genuine zero-hit answer — the vocabulary check, NearestTerms,
		// "did you mean" — is refused to a TRUNCATED query: those exist to
		// tell the caller their spelling found nothing in a corpus this layer
		// actually finished searching, and offering vocabulary suggestions
		// over a corpus it gave up on partway through would imply a
		// completeness this answer does not have. A truncated-but-zero-in-
		// the-fanout query instead falls through to the ordinary evaluation
		// path below (0 candidates ever match wordPaths, exactly as an
		// ordinary zero-survivor query does), which is where the truncation
		// problem is actually recorded.
		if len(wordPaths) == 0 && !wordsTruncated {
			// R1 (docs/internal/design/knowledge-tools-remediation.md):
			// complete:true over zero hits is a claim that the corpus was
			// actually searched. Search's own zero return cannot make that
			// claim by itself — it is silent about whether the index has
			// ever been built — so that has to be checked here, BEFORE the
			// zero is reported as complete, not after. This is the one-line
			// fix for F-9: the query that reproduced it (`words="Vorlex"`
			// over an unindexed vault with 11 matching notes on disk)
			// reached this exact branch and returned Complete:true.
			if r := checkTextIndexPopulated(ctx, d.Text); r != nil {
				return refusalResponse(generated.VaultFindRequest{}, echo, r), r
			}
			return zeroHitResponse(ctx, d, q, echo), nil
		}
	}

	// `near`/`hops` runs AFTER words for the same reason B1/B2 run after both:
	// it is the most expensive narrowing input (a graph walk, not an index
	// lookup), so a query already known to be zero-hit from the cheaper words
	// check never pays for it. It produces a RECORD-IDENTITY set the candidate
	// stream then intersects — the same shape wordPaths already is, so `visit`
	// composes the two with one extra membership test rather than a second
	// mechanism (FR-076: near MUST NOT bypass, weaken or replace any filter
	// supplied alongside it, in either direction — AC-F2).
	var nearSet map[string]bool
	var nearAnchorPath string
	if q.near != "" {
		reached, anchorPath, r := nearReachable(ctx, d, q)
		if r != nil {
			return refusalResponse(generated.VaultFindRequest{}, echo, r), r
		}
		// Zero ONLY when neither the graph nor the anchor placed anything: an
		// anchor that resolved to an ordinary note (empty graph, non-empty
		// anchorPath) is NOT a zero-hit query — it still has hop 0 to return.
		if len(reached) == 0 && anchorPath == "" {
			return zeroHitResponse(ctx, d, q, echo), nil
		}
		nearSet = reached
		nearAnchorPath = anchorPath
	}

	if d.Store == nil {
		// PLAIN WORDS STILL WORK WITH NO PROPERTIES INDEX, because that is what
		// the platform posture promises in as many words: propindex_stub.go's
		// header says "What keeps working on such a build: knowledge_read, and
		// the plain-word half of knowledge_find". It did not. Every non-explain
		// query, a bare `words` one included, was refused here — and the
		// platform gate never named a capability either, because a words-only
		// query has none, so the caller received a generic "the properties
		// index is not open" for a question bleve alone could answer.
		if q.textOnlyServable() {
			return textOnlyResponse(d, q, echo, wordHits, wordsTruncated), nil
		}
		ref := refuse(problem(generated.IndexUnavailable,
			"the properties index is not open, so no record can be read",
			"re-open the vault; run knowledge_describe check_integrity to see the index state"), nil)
		return refusalResponse(generated.VaultFindRequest{}, echo, ref), ref
	}

	// ── B1: bound WORK, before anything is retrieved ────────────────────────
	//
	// It counts the narrowed candidate POPULATION and it is taken BEFORE the
	// first candidate is read. The count is exact, so the refusal quotes it. Its
	// remedy is SCOPE or KIND and deliberately NOT "add a filter" — a filter
	// does not change the number that fired, and naming a remedy that does not
	// reduce the number is worse than naming none.
	total, err := d.Store.CountCandidates(ctx, sel)
	if err != nil {
		ref := refuse(problem(generated.IndexUnavailable,
			fmt.Sprintf("the properties index could not count candidates: %v", err),
			"run knowledge_describe check_integrity"), err)
		return refusalResponse(generated.VaultFindRequest{}, echo, ref), ref
	}
	if total > propindex.BoundNarrowedCandidates {
		subject := "records"
		if q.recordType != "" {
			subject = "candidate records of type " + q.recordType
		}
		ref := refuse(problem(generated.EvaluationBoundExceeded,
			fmt.Sprintf("this query would evaluate %s %s; the limit is %s",
				group3(total), subject, group3(propindex.BoundNarrowedCandidates)),
			narrowedCandidateRemedy), nil)
		return refusalResponse(generated.VaultFindRequest{}, echo, ref), ref
	}

	// THE CHILD-TABLE PREPASSES, before the candidate stream and after B1.
	//
	// After B1 because a query that would be refused for evaluating 80,000
	// candidates must not first pay to buffer their tags; before the stream
	// because FR-131 forbids joining a second child table into the candidate
	// statement, so the only place to assemble `file.tags`/`file.links` is
	// beside the stream rather than inside it.
	files, r := newFileMetaSource(ctx, d, q)
	if r != nil {
		return refusalResponse(generated.VaultFindRequest{}, echo, r), r
	}

	cmp := records.Comparator{ResolveRelation: d.Resolve}
	ev := &evaluation{q: q, cmp: cmp, words: wordPaths, near: nearSet, nearAnchorPath: nearAnchorPath, files: files}

	// ONE evaluator for the whole scan, and `now` snapshotted ONCE (FR-146's
	// last clause) so `now()`/`today()` give the same answer for every
	// candidate. A per-candidate clock read would put records on opposite sides
	// of `due < today()` in one response that has no error to show for it.
	if ev.q.namespace().formulas.Len() > 0 {
		ev.formulas = records.NewFormulaEvaluator(ev.q.namespace().formulas, cmp, queryNow(d))
	}

	// FIX F6: the fanout truncation detected above is a property of the
	// QUERY, not of any one record — Records is deliberately empty, matching
	// scope_truncated and page_size_clamped's own shape (RecordProblem.yaml).
	// It is recorded on ev HERE, as early as ev exists, so that assemble()
	// below — the one path that reads e.problems into the response — always
	// carries it. (A B1/B2 bound refusal further down returns its OWN
	// single-problem response built directly from `ref`, bypassing e.problems
	// entirely — but a refusal already carries Complete:false and its own
	// named cause, so it is not the silent-success shape F6 is about.)
	if wordsTruncated {
		ev.recordProblems([]generated.RecordProblem{problem(generated.TextSearchTruncated,
			fmt.Sprintf("the text index holds more than %s matches for %q; "+
				"only the top-ranked %s were considered before the typed filter ran",
				group3(textFanout(q.limit)), q.words, group3(textFanout(q.limit))),
			"add or tighten a typed `filter` — unlike `words`, it is evaluated over "+
				"the full narrowed candidate population, not this fanout")})
	}

	// ── B2: bound MEMORY, during evaluation ─────────────────────────────────
	//
	// It counts SURVIVORS and aborts the stream. It is not a precondition and
	// cannot be: "the rows surviving the filter" is a quantity only the Go
	// comparator can produce, and it produces it by evaluating candidates.
	err = d.Store.Candidates(ctx, sel, ev.visit)
	if err != nil {
		if propindex.IsBoundExceeded(err) {
			ref := refuse(problem(generated.CandidateCapExceeded,
				fmt.Sprintf("this query matched more than %s records; the limit is %s",
					group3(propindex.BoundSurvivors), group3(propindex.BoundSurvivors)),
				candidateCapRemedy), err)
			return refusalResponse(generated.VaultFindRequest{}, echo, ref), ref
		}
		ref := refuse(problem(generated.IndexUnavailable,
			fmt.Sprintf("the properties index could not stream candidates: %v", err),
			"run knowledge_describe check_integrity"), err)
		return refusalResponse(generated.VaultFindRequest{}, echo, ref), ref
	}

	return ev.assemble(ctx, d, echo), nil
}

// checkTextIndexPopulated refuses a words-carrying, zero-hit query whose text
// index has never completed a build — R1's rule made concrete.
//
// CHOICE OF CHECK, STATED EXPLICITLY: this asks "has a build ever completed",
// NOT "does the index currently hold zero documents". Those read as the same
// question and are not. `knowledge.Index.DocCount()` reports the SECOND one,
// and gating on it directly would be wrong in both directions:
//
//   - An index that has never been built and holds zero documents would be
//     refused correctly, by coincidence — but so would a HEALTHY index over a
//     genuinely empty collection (0 notes on disk), which is exactly the
//     regression this fix must not introduce: an honest "complete: true, 0
//     rows" answer turned into a false refusal because the true answer and the
//     unbuilt one happen to share a document count of zero.
//   - Conversely, DocCount alone says nothing about a corpus that HAS notes
//     but whose index build genuinely failed partway through and left a
//     handful of documents behind — DocCount() > 0 there would wrongly read
//     as "populated" and let exactly F-9's shape back in for a query whose
//     words happen to miss the partial index.
//
// A build-completion flag does not have this ambiguity: it is orthogonal to
// how many documents the build found, so "populated, 0 docs" (an honest empty
// vault) and "not populated" (an unbuilt or partially-built index) stay
// distinguishable regardless of what DocCount happens to read.
func checkTextIndexPopulated(ctx context.Context, text TextSearcher) *RefusalError {
	populated, err := text.Populated(ctx)
	if err != nil {
		return refuse(problem(generated.IndexUnavailable,
			fmt.Sprintf("the text index's build state could not be read: %v", err),
			"re-run, or run knowledge_describe check_integrity to see the index state"), err)
	}
	if !populated {
		// A2(d): the caller's real question is "is this zero a real miss, or an
		// index that has not caught up?". Populated already answers that as a
		// boolean; what it could not do was say BY HOW MUCH the index is behind.
		// When the searcher can report its freshness, quote the concrete
		// coverage — "reflects N of M files on disk, P not yet indexed" — so a
		// stale/incomplete index is distinguishable from genuinely absent
		// content by the NUMBERS, not just the refusal.
		//
		// It deliberately does NOT try to label the state "stale" vs "never
		// built": an instant-indexing write (author.go) leaves a manifest with
		// a single entry, so "manifest present" and "collection swept" are not
		// the same fact and cannot be told apart from coverage alone — both are
		// simply "the index does not cover this vault yet", which is exactly
		// what the count says without over-claiming. The "never finished
		// indexing" wording is preserved because it is true of every
		// incomplete-coverage state (a vault whose index does not reflect all
		// of it has not finished indexing it) and because callers/tests key on
		// it.
		if fr, ok := text.(TextFreshnessReporter); ok {
			if fresh, ferr := fr.IndexFreshness(ctx); ferr == nil && fresh.ScannedFiles > 0 {
				return refuse(problem(generated.IndexUnavailable,
					fmt.Sprintf("the text index has never finished indexing this vault — it currently reflects "+
						"%s of the %s files on disk (%s not yet indexed), so a zero-hit answer here cannot be "+
						"trusted; it is indistinguishable from a real miss over a vault that was actually searched",
						group3(fresh.IndexedFiles), group3(fresh.ScannedFiles), group3(fresh.PendingFiles)),
					"re-run indexing for this vault; run knowledge_describe check_integrity to see the index state"), nil)
			}
		}
		return refuse(problem(generated.IndexUnavailable,
			"the text index has never finished indexing this vault, so a zero-hit answer "+
				"here cannot be trusted — it would be indistinguishable from a real miss over a "+
				"vault that was actually searched",
			"re-open the vault; run knowledge_describe check_integrity to see the index state"), nil)
	}
	return nil
}

// textFanout is how many text hits to ask for. It is deliberately wider than the
// page: the typed filter runs AFTER, so asking for exactly `limit` would page
// the text index and then throw most of it away, reporting fewer rows than exist
// with nothing saying so.
func textFanout(limit int) int {
	n := limit * 20
	if n < 200 {
		n = 200
	}
	if n > propindex.BoundSurvivors {
		n = propindex.BoundSurvivors
	}
	return n
}

// evaluation accumulates survivors. It holds ONE candidate at a time from the
// store's perspective — what it retains per survivor is the rendered row and the
// sort keys, not the decoded candidate.
type evaluation struct {
	q     *query
	cmp   records.Comparator
	words map[string]TextHit
	// near is the reachable-record-identity set nearReachable computed, nil
	// when the query carried no `near`. It narrows exactly the way words does
	// — a candidate outside it is never selected at all, not "selected and
	// excluded" — so near and words compose as an intersection with each
	// other and with the typed filter (FR-076, AC-F2), never as a filter one
	// of them could weaken.
	near map[string]bool
	// nearAnchorPath is the `near` origin note's own path (hop 0), admitted
	// regardless of record identity so an ORDINARY-note anchor re-enters the
	// answer even though it is not a node in the record graph `near` holds.
	// Empty when the query carried no `near`, or when ResolveNear could not
	// place the anchor on disk.
	nearAnchorPath string

	// files assembles FR-130's twelve virtual properties per candidate from the
	// parent row and the child-table prepasses.
	files *fileMetaSource
	// formulas is the ONE evaluator for this scan, nil when the view declares
	// no formulas.
	formulas *records.FormulaEvaluator

	selected    int
	survivors   []survivor
	problems    []generated.RecordProblem
	unevaluable int
	seenProblem map[string]bool
}

type survivor struct {
	cand  propindex.Candidate
	score float64
	// textHash is what the TEXT index holds for this note. FR-020c compares the
	// row's own hash against THIS, per returned record.
	textHash string
	hasText  bool
	values   map[string]records.PropertyValue
}

// visit is the per-candidate callback. Returning Accepted counts against B2.
func (e *evaluation) visit(c propindex.Candidate) (propindex.Verdict, error) {
	// The word half is an INTERSECTION, applied before the comparator so a
	// record outside it costs no decode.
	var hit TextHit
	hasText := false
	if e.words != nil {
		h, ok := e.words[c.Path]
		if !ok {
			return propindex.Rejected, nil
		}
		hit, hasText = h, true
	}

	// kind=record is the THIRD narrowing, and it is applied here rather than in
	// the Selector because "any declared record type" is not one of the three
	// things ruling R-A lets the store decide. A note with no `type:` is an
	// ordinary note (FR-005) and carries no RecordID/RecordType, so the test is
	// the column itself.
	if e.q.kind == KindRecord && c.RecordType == "" {
		return propindex.Rejected, nil
	}

	// The near/hops half is the SECOND intersection, same shape as words: an
	// ordinary note (no declared type, RecordID empty) can never be a graph
	// node — relation edges only connect record identities (D7) — so it is
	// correctly excluded here whenever `near` narrowed at all, without a
	// special case.
	if e.near != nil {
		// The anchor note itself is hop 0 and is admitted by PATH, so an
		// ordinary-note anchor (RecordID == "") re-enters the answer even
		// though it is not a node in the record graph. Every OTHER row must be
		// a record within the traversed radius — a plain note that is not the
		// anchor is still correctly excluded, because it can never be a graph
		// node (relation edges connect record identities only, D7).
		isAnchor := e.nearAnchorPath != "" && c.Path == e.nearAnchorPath
		inRadius := c.RecordID != "" && e.near[c.RecordID]
		if !isAnchor && !inRadius {
			return propindex.Rejected, nil
		}
	}

	e.selected++
	cand := newCandidate(c, e.q.schema, e.files.meta(c), e.formulas)
	defer e.drainFormulaProblems(cand)

	matched := true
	if e.q.filter != nil {
		res := e.q.filter.eval(e.cmp, cand)
		e.recordProblems(res.problems)
		if res.blocked {
			e.unevaluable++
			return propindex.Rejected, nil
		}
		matched = res.matched
	}
	if !matched {
		return propindex.Rejected, nil
	}

	// Decode the columns that will actually be RENDERED or SORTED, while the
	// candidate is still in hand. Doing it later would mean holding every
	// candidate, which is the memory bound this stream exists to respect.
	values, err := e.materialise(cand)
	if err != nil {
		p := problem(generated.StaleRecord, err.Error(),
			"re-index this note, or correct the value to one the schema declares", cand.identity())
		p.Paths = &[]string{c.Path}
		e.recordProblems([]generated.RecordProblem{p})
		e.unevaluable++
		return propindex.Rejected, nil
	}

	e.survivors = append(e.survivors, survivor{
		cand: c, score: hit.Score, textHash: hit.SourceHash, hasText: hasText, values: values,
	})
	return propindex.Accepted, nil
}

// materialise decodes the render and sort columns for one survivor.
func (e *evaluation) materialise(cand *candidate) (map[string]records.PropertyValue, error) {
	out := map[string]records.PropertyValue{}
	for _, prop := range e.q.renderProperties() {
		v, err := cand.value(prop)
		if err != nil {
			return nil, err
		}
		out[prop.Name] = v
	}
	return out, nil
}

// drainFormulaProblems moves a candidate's formula problems into the response.
//
// It runs on EVERY visited candidate, matched or not, because FR-026 requires
// the offending record to be named whether or not the problem happened to
// change the outcome — a division by zero in a formula the filter then rejected
// on other grounds is still a defect the reader has to fix.
func (e *evaluation) drainFormulaProblems(cand *candidate) {
	if len(cand.formulaProblems) == 0 {
		return
	}
	ps := make([]generated.RecordProblem, 0, len(cand.formulaProblems))
	for _, cp := range cand.formulaProblems {
		ps = append(ps, comparisonProblem(cand.rows.RecordID, cand.rows.Path, cp))
	}
	e.recordProblems(ps)
	cand.formulaProblems = nil
}

// queryNow is FR-146's one-per-response snapshot.
func queryNow(d Deps) time.Time {
	if d.Now.IsZero() {
		return time.Now()
	}
	return d.Now
}

// ---------------------------------------------------------------------------
// SCHEMA-DECLARED FORMULAS: REFUSED BY THE LOADER, SO NOT A CASE HERE
//
// This file used to carry refuseSchemaDeclaredFormulas, which refused EVERY
// query over a record type whose schema declared `formula:` on a property. That
// refusal has moved to where the mistake is — records/schema.go's
// propertyDeclKeys refuses the key at load, per file, so such a schema never
// enters the SchemaSet and q.schema can no longer hold a derived property.
//
// The guard is not kept "just in case": an unreachable second refusal is a
// branch no test can distinguish from a working one. What remains true, and is
// what the rest of this file relies on, is the routing rule itself — a query
// reaches a formula ONLY as `formula.<name>` (FR-140), resolved by namespace.go
// against the saved VIEW's formula set below. A bare property name always means
// the record's STORED value.
// ---------------------------------------------------------------------------

// viewFormulas reads the named view's `formulas:` map, when the loader carries
// one. Everything about it is optional and every absence means the same,
// honest thing: this query has no formulas.
func viewFormulas(loader ViewLoader, view *string) map[string]string {
	if loader == nil || view == nil || *view == "" {
		return nil
	}
	fl, ok := loader.(ViewFormulaLoader)
	if !ok {
		return nil
	}
	sources, ok := fl.Formulas(*view)
	if !ok {
		return nil
	}
	return sources
}

// recordProblems appends, deduplicating on record+property+code so a filter tree
// naming one property in three leaves reports one line rather than three.
//
// The deduplication is on the RECORD's identity, not on the message, because the
// same defect in two different notes is two problems the reader must fix twice.
func (e *evaluation) recordProblems(ps []generated.RecordProblem) {
	if e.seenProblem == nil {
		e.seenProblem = map[string]bool{}
	}
	for _, p := range ps {
		key := strings.Join(p.Records, ",") + "|" + string(p.Code)
		if p.Property != nil {
			key += "|" + *p.Property
		}
		if e.seenProblem[key] {
			continue
		}
		e.seenProblem[key] = true
		e.problems = append(e.problems, p)
	}
}

// ---------------------------------------------------------------------------
// THE PLAIN-WORD HALF, WITH NO PROPERTIES INDEX — FR-020h
// ---------------------------------------------------------------------------

// textOnlyServable reports whether this query can be answered by the text index
// ALONE.
//
// It is deliberately a whitelist of "names nothing the properties index owns",
// not a blacklist. Every argument below reaches a stored row: a typed filter, a
// record type, kind=record's record_type column, kind=attachment's kind column,
// a graph walk, a join, a grouping, a summary, a sort key and a rendered column
// are all decoded from candidates this build has none of. Answering any of them
// from a text ranking would be the silent broadening the platform gate exists
// to refuse — so anything outside this shape still gets the refusal.
func (q *query) textOnlyServable() bool {
	return q.words != "" &&
		q.kind == KindNote &&
		q.filter == nil &&
		q.recordType == "" &&
		q.near == "" &&
		len(q.join) == 0 &&
		len(q.groupBy) == 0 &&
		len(q.aggregates) == 0 &&
		len(q.sort) == 0 &&
		len(q.selectCols) == 0
}

// textOnlyResponse answers a words-only query out of the text index.
//
// TWO THINGS IT DELIBERATELY DOES NOT DO. It reports NO index state — there is
// only one index on such a build, so "N of M returned records agree across both
// indexes" would be a freshness claim with nothing behind it, and a false
// reassurance is worse than a missing line. And it renders NO cells: a column
// is a decoded property value, and there are no property rows here.
func textOnlyResponse(d Deps, q *query, echo string, hits []TextHit, truncated bool) generated.VaultFindResponse {
	// FR-060's workspace scope is the caller's, already resolved, and it is
	// applied again here rather than trusted: TextSearcher.Search states that
	// it answers "within the caller's already-resolved scope", and a prefix
	// test costs nothing next to returning a path the agent may not see.
	scoped := make([]TextHit, 0, len(hits))
	for _, h := range hits {
		if d.PathPrefix != "" && !strings.HasPrefix(h.Path, d.PathPrefix) {
			continue
		}
		scoped = append(scoped, h)
	}

	evaluated := len(scoped)
	offset := cursorOffset(q.cursor)
	page := scoped
	switch {
	case offset > 0 && offset < len(page):
		page = page[offset:]
	case offset >= len(page) && offset > 0:
		page = nil
	}
	if len(page) > q.limit {
		page = page[:q.limit]
	}

	rows := make([]generated.VaultFindRow, 0, len(page))
	for _, h := range page {
		rows = append(rows, generated.VaultFindRow{
			Path:  h.Path,
			Title: titleOf(h.Path),
			Cells: []generated.VaultFindCell{},
			Joins: []generated.VaultFindJoin{},
		})
	}

	problems := []generated.RecordProblem{}
	if truncated {
		problems = append(problems, problem(generated.TextSearchTruncated,
			fmt.Sprintf("the text index holds more than %s matches for %q; only the top-ranked %s were returned",
				group3(textFanout(q.limit)), q.words, group3(textFanout(q.limit))),
			"add more words to narrow the ranking — a typed `filter` is not available on this build"))
	}

	resp := generated.VaultFindResponse{
		Complete:  true,
		Refused:   false,
		QueryEcho: echo,
		Counts: generated.VaultFindCounts{
			Selected: evaluated, Evaluated: evaluated, Shown: len(rows),
		},
		Rows:     rows,
		Totals:   []generated.VaultFindTotal{},
		Problems: problems,
		Next:     []generated.VaultFindAction{},
	}
	applied := q.limit
	resp.LimitApplied = &applied
	if q.clamped {
		resp.LimitClamped = boolPtr(true)
		asked := q.limitAsked
		resp.LimitRequested = &asked
	}

	if q.renderRows == 0 {
		trimToBudget(&resp)
	}
	finishVerdict(&resp, q)
	if consumed := offset + resp.Counts.Shown; consumed < evaluated {
		c := encodeCursor(consumed, d.Epoch)
		resp.NextCursor = &c
	}
	resp.Next = nextActions(q, &resp)
	return resp
}

// group3 renders a count with thousands separators, for a refusal message.
func group3(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	return strings.Join(append([]string{s}, parts...), ",")
}
