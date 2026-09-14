// Omnipus — ADR-068 D15.3: the knowledge_find tool adapter, wiring
// pkg/records/knowledgefind's Deps (Schemas, Store, Text, Views, Resolve,
// Epoch) to real, open collections.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultprops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE LIVES IN pkg/vaultprops
//
// knowledge_describe/read/edit/restructure/configure are all tools.Tool
// implementations already, built directly in pkg/knowledge with a
// ToolDeps/AuthoringDeps constructor — pkg/agent/knowledge_tools.go (the
// execution registry) and pkg/gateway/knowledge_tools_wire.go (the metadata
// catalog) construct them directly and are done.
//
// knowledge_find is different in kind, not degree: pkg/records/knowledgefind
// exposes a package function (Call(ctx, Deps, raw)), and building its Deps
// needs BOTH pkg/knowledge (the bleve index, NoteIndex) AND
// pkg/records/propindex (the Store) joined together — which is exactly what
// pkg/vaultprops already exists to do (reader.go's own header: "it imports
// BOTH sides, and nothing imports it except the wiring layer that constructs
// the tools — so it can never be part of a cycle").
//
// It does NOT live in pkg/gateway, where an earlier revision of this file
// put it: pkg/agent/knowledge_tools.go is the execution-registry call site
// (the registry a turn actually dispatches through), and pkg/agent does not
// import pkg/gateway — pkg/gateway imports pkg/agent, so the reverse edge
// would be a cycle. pkg/vaultprops is the one package already reachable from
// BOTH call sites (pkg/agent's execution registry and
// pkg/gateway/knowledge_tools_wire.go's metadata catalog) without creating
// one in either direction.
//
// It also does NOT live in pkg/knowledge, for the same reason
// find_text.go's header there gives: pkg/knowledge cannot import
// pkg/records/knowledgefind without a test-build cycle through
// pkg/records/propindex's own test file.
// ---------------------------------------------------------------------------

// FindTool is knowledge_find: the tools.Tool adapter around
// pkg/records/knowledgefind.Call.
type FindTool struct {
	tools.BaseTool
	// home is $OMNIPUS_HOME, exactly as every other knowledge_* tool takes
	// it — resolved per call from the calling agent's workspace via
	// knowledge.ResolveTurnScope, never from a tool argument.
	home string
}

// NewFindTool builds the tool.
func NewFindTool(home string) *FindTool {
	return &FindTool{home: home}
}

// Name is the registered tool name.
func (t *FindTool) Name() string { return knowledgefind.ToolName }

// Description is knowledgefind's own tuned description — the single source
// of truth for what the model reads, reused rather than restated so the two
// can never drift (knowledgefind/tool.go's own header: "roughly 150 tokens
// and it is the ONLY thing the model sees before deciding whether to call
// this tool").
func (t *FindTool) Description() string { return knowledgefind.Description }

// Scope classifies the tool for per-agent visibility filtering, matching
// every other knowledge_* tool.
func (t *FindTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

// Category groups the tool in the picker UI, matching every other
// knowledge_* tool.
func (t *FindTool) Category() tools.ToolCategory { return tools.CategoryMemory }

// Parameters is knowledgefind's own schema, reused for the same reason as
// Description.
func (t *FindTool) Parameters() map[string]any { return knowledgefind.Parameters() }

// Execute resolves the calling agent's workspace scope, opens the collection
// for the duration of this one call, builds a real knowledgefind.Deps
// against it, and runs the query.
//
// knowledge_find carries NO `collection` argument on the wire (unlike
// knowledge_search/describe/read) — knowledgefind/tool.go's own
// AcceptedParameters does not list one. So exactly like those tools when
// their own collection argument is left unset, this resolves the single
// collection the workspace has mounted; a workspace with zero or more than
// one is reported, by name, rather than guessed at.
func (t *FindTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	// AN UNKNOWN ARGUMENT IS REFUSED BEFORE THE SCOPE GATE, AND THE ORDER IS THE
	// WHOLE POINT OF THIS BLOCK.
	//
	// This tool carries no `collection` argument (see the doc comment above),
	// but knowledge_describe and knowledge_read both do, so an agent that has
	// just used one of those reasonably tries `collection:` here. Until this
	// check existed, that attempt hit the scope gate below and came back with a
	// refusal naming the collections in scope — which reads as "you named the
	// wrong one" and invites another attempt at naming one. There is no
	// argument to name one WITH, so every retry failed identically.
	//
	// Measured, not theorised: a UAT agent made 24 such calls in one turn, each
	// returning in about a millisecond, until the turn budget ran out. The
	// honest refusal it needed already existed one layer down
	// (knowledgefind/tool.go's "%s is not an argument of %s; accepted: %s") and
	// was simply unreachable, because Call() cannot run until buildDeps has a
	// collection. Checking the keys first — which needs no collection at all —
	// turns that 24-call timeout into one useful answer.
	//
	// AcceptedParameters is the same exported list knowledgefind itself decodes
	// against, so this cannot drift into a second, staler idea of what is legal.
	//
	// THE REFUSAL ITSELF IS knowledgefind's OWN STRUCTURED ONE, REUSED, NOT
	// REPLACED (Finding 8). An earlier version of this check built its own
	// flat string here — plain text with no generated.RecordProblem behind
	// it — which fixed the 24-retry loop above but cost every unknown-argument
	// call its Permitted list and, for a name knowledgefind recognises as a
	// specific mistake (`order_by` -> "use sort", `where` -> "there is no
	// query language here", etc.), its targeted remedy: knowledgefind/tool.go's
	// unknownParameterRemedy is unexported and decodeRequest needs no Deps to
	// run, but Call always runs decodeRequest and Find back to back with no
	// seam to stop after the first, so this tool cannot invoke Call itself
	// before a collection is resolved. findRefuseUnsupportedParameter below
	// builds the identical generated.RecordProblem/RefusalError shape
	// decodeRequest would have — same Reason text (down to ToolName and the
	// quoted argument spelling), same Permitted list, same remedy mapping —
	// and renders it through knowledgefind.Render, the one function that
	// turns a response into what the model reads, so this refusal is
	// byte-for-byte what Call() itself would have rendered for the same
	// argument, had a collection already been open to call it with.
	if unknown := unacceptedFindArgs(args); len(unknown) > 0 {
		return tools.ErrorResult(findRefuseUnsupportedParameter(unknown))
	}

	scope, _ := knowledge.ResolveTurnScope(ctx, t.home)
	// D-46 (#698): `collection` selects among several knowledge bases in
	// scope, exactly as knowledge_describe and knowledge_read do. It is
	// consumed HERE and never reaches the engine, which cannot select scope
	// (FR-060); the argument is stripped from what is forwarded so the
	// engine's own decode never sees an argument it has no meaning for.
	collectionRef := ""
	if v, ok := args["collection"].(string); ok {
		collectionRef = strings.TrimSpace(v)
	}
	col, ok := scope.Select(collectionRef)
	if !ok {
		// NAMES THE REMEDY, NOT JUST THE OBSTACLE — one shared sentence with
		// knowledge_describe and knowledge_read (D-57).
		return tools.ErrorResult(scope.SelectionRefusal("knowledge_find", collectionRef))
	}
	forwarded := make(map[string]any, len(args))
	for k, v := range args {
		if k != "collection" {
			forwarded[k] = v
		}
	}

	raw, err := json.Marshal(forwarded)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("knowledge_find: could not encode arguments: %v", err))
	}

	deps, closeDeps, err := t.buildDeps(ctx, col)
	defer closeDeps()
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("knowledge_find: %v", err))
	}
	deps.CollectionName = col.Name

	text, callErr := knowledgefind.Call(ctx, deps, raw)
	if callErr != nil {
		// Call() ALWAYS returns rendered text alongside a refusal error — the
		// model reads and acts on it exactly like knowledge_describe's own
		// refusals (an unknown record_type, an out-of-scope collection).
		return tools.ErrorResult(text)
	}
	return tools.NewToolResult(text)
}

// unacceptedFindArgs returns the argument names this tool does not accept, in
// the caller's own spelling, sorted so the message is stable between runs.
//
// It compares against knowledgefind.AcceptedParameters rather than a local
// list. That package already refuses unknown keys during decode; this is the
// same question asked earlier, not a second opinion about it.
func unacceptedFindArgs(args map[string]any) []string {
	accepted := make(map[string]bool, len(knowledgefind.AcceptedParameters))
	for _, n := range knowledgefind.AcceptedParameters {
		accepted[n] = true
	}
	var unknown []string
	for k := range args {
		if !accepted[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// findRefuseUnsupportedParameter renders the SAME structured refusal
// knowledgefind's own decodeRequest (pkg/records/knowledgefind/tool.go)
// would render for these unknown top-level argument names — reused in
// content and shape, not reimplemented as a lesser, flat string (Finding 8).
//
// It cannot call decodeRequest directly: that function is unexported, and
// Call — the exported entry point that runs it — always chains straight
// into Find(ctx, d, req) once decoding succeeds, which would dereference
// this Deps-free call's necessarily-absent Store/Text/Views against a real
// query. There is no seam to stop after decoding alone without a collection
// open, which is exactly the problem this whole unknown-argument-first
// check exists to route around. So the RefusalError and the response
// Render() turns it into are built here by hand, from knowledgefind's own
// EXPORTED surface (RefusalError, Render, the generated wire types, ToolName
// and AcceptedParameters) — every field is the one decodeRequest's
// unsupported-parameter branch sets, so the rendered text matches Call()'s
// own output for the same input, word for word.
func findRefuseUnsupportedParameter(unknown []string) string {
	quoted := make([]string, len(unknown))
	for i, u := range unknown {
		quoted[i] = `"` + u + `"`
	}
	fix := findUnknownParameterRemedy(unknown)
	permitted := append([]string{}, knowledgefind.AcceptedParameters...)
	problem := generated.RecordProblem{
		Code: generated.UnsupportedParameter,
		Reason: fmt.Sprintf("%s is not an argument of %s; accepted: %s",
			strings.Join(quoted, ", "), knowledgefind.ToolName, strings.Join(knowledgefind.AcceptedParameters, ", ")),
		Fix:       &fix,
		Permitted: &permitted,
		Records:   []string{},
	}
	resp := generated.VaultFindResponse{
		Complete:       false,
		CompleteReason: strPtr("the query was refused; no records were evaluated"),
		Refused:        true,
		QueryEcho:      "arguments as sent",
		Counts:         generated.VaultFindCounts{},
		Rows:           []generated.VaultFindRow{},
		Totals:         []generated.VaultFindTotal{},
		Problems:       []generated.RecordProblem{problem},
		// decodeRequest's own refusalActions falls to its `default` case for
		// UnsupportedParameter (every other named case is a different
		// problem code), which is exactly this one action.
		Next: []generated.VaultFindAction{{Label: "describe", Call: "knowledge_describe"}},
	}
	return knowledgefind.Render(resp)
}

// findUnknownParameterRemedy mirrors knowledgefind/tool.go's own
// unknownParameterRemedy (unexported, so not callable from here) so the
// SAME model-fluent-in-SQL mistakes get the SAME targeted remedy this tool
// would have given had a collection already been open. Kept in the exact
// same case order and wording deliberately — a caller comparing the two
// refusals (with vs. without a resolved collection) must see one answer,
// not two that happen to agree today.
func findUnknownParameterRemedy(unknown []string) string {
	for _, u := range unknown {
		switch strings.ToLower(u) {
		case "where", "sql", "query":
			return "express the predicate as a structured filter tree; there is no query language here"
		case "having":
			return "use group_by, then filter on the grouped property"
		case "order_by", "orderby":
			return "use sort"
		case "offset", "page", "skip":
			return "page with cursor, which is returned in the previous reply's next block"
		case "fields", "columns", "properties":
			return "use select"
		}
	}
	return "drop the argument, or call knowledge_describe to see what this knowledge base supports"
}

// strPtr is the pointer-taking helper the generated optional fields need,
// mirroring knowledgefind's own unexported `str` for the same reason: an
// optional field left nil means "not set", so "" must go in as a real
// pointer, never be left nil in its place.
func strPtr(s string) *string { return &s }

// buildDeps opens everything one call needs and returns a cleanup function
// that is always safe to call (nil-checked internally), even on a partial
// build — so `defer closeDeps()` above is correct whether buildDeps returned
// an error or not.
//
// The body moved to OpenFindEnv (find_env.go) when the gateway's view-result
// endpoint needed the SAME environment — one open, one set of rules — rather
// than a second copy of it; this method is the tool-shaped shim over it.
func (t *FindTool) buildDeps(ctx context.Context, col knowledge.ScopedCollection) (knowledgefind.Deps, func(), error) {
	env, closeEnv, err := OpenFindEnv(ctx, t.home, col)
	return env.Deps, closeEnv, err
}

// findTextSearcher adapts an already-open *knowledge.Index to
// knowledgefind.TextSearcher. It lives here, not in pkg/knowledge, because
// its method signatures must literally name knowledgefind's own return
// types (TextHit, generated.VaultTermCount) — see pkg/knowledge/find_text.go's
// header for the import cycle that forbids doing so from pkg/knowledge
// itself. Every piece of actual logic is pkg/knowledge's own exported
// operation (Search, SourceHashForPath, NearMissVocabularyWithCounts); this
// type only converts between knowledge-native and wire-facing shapes.
type findTextSearcher struct {
	ix *knowledge.Index
}

var _ knowledgefind.TextSearcher = (*findTextSearcher)(nil)
var _ knowledgefind.TextFreshnessReporter = (*findTextSearcher)(nil)
var _ knowledgefind.TextDeepSearcher = (*findTextSearcher)(nil)

func (s *findTextSearcher) Search(_ context.Context, words string, limit int) ([]knowledgefind.TextHit, error) {
	hits, err := s.ix.Search(words, limit)
	if err != nil {
		return nil, err
	}
	return convertIndexHits(hits), nil
}

// SearchDeep implements knowledgefind.TextDeepSearcher (F3): it calls
// knowledge.Index.SearchFiltered DIRECTLY — the same already-exported method
// Search (above) reaches through knowledge.Index.Search, which discards
// SearchFiltered's own truncated flag by design (see Index.Search's own doc
// comment for why that discard is correct at ITS layer: every OTHER
// production caller of Index.Search reaches SearchFiltered through
// Searcher.Search instead, which folds truncation into its own report).
// fetchWordHits' re-ask at propindex.BoundSurvivors is not one of those
// callers — it goes through knowledgefind.TextSearcher — so this method
// exists to carry the SAME flag through that path too, instead of leaving
// fetchWordHits to infer exhaustion from a length comparison
// indexSearchMaxFetch (SearchFiltered's own internal fetch ceiling, far
// below propindex.BoundSurvivors) can make true by coincidence regardless
// of the real corpus size.
func (s *findTextSearcher) SearchDeep(_ context.Context, words string, limit int) ([]knowledgefind.TextHit, bool, error) {
	// SearchFiltered's third result is KB-7a's AND->OR fallback flag. It is
	// deliberately not propagated here: SearchDeep answers only "was the
	// corpus exhausted", and the fallback signal already reaches callers on
	// every hit (IndexHit.FallbackMode) and through SearchReport's own
	// relaxed-match disclosure, so re-deriving it from this one call site
	// would give the same fact two owners that can disagree.
	hits, truncated, _, err := s.ix.SearchFiltered(words, limit, nil)
	if err != nil {
		return nil, false, err
	}
	return convertIndexHits(hits), !truncated, nil
}

// convertIndexHits is the pkg/knowledge -> knowledgefind IndexHit conversion
// Search and SearchDeep both need, pulled out so the two call sites (one
// hitting knowledge.Index.Search, the other knowledge.Index.SearchFiltered
// directly) share it rather than duplicating the field-by-field carry-through.
func convertIndexHits(hits []knowledge.IndexHit) []knowledgefind.TextHit {
	out := make([]knowledgefind.TextHit, 0, len(hits))
	for _, h := range hits {
		// h.Kind is pkg/knowledge's ScanKind ("note"/"attachment", scan.go) —
		// the same two strings knowledgefind.KindNote/KindAttachment name, so
		// this is a straight carry-through, not a re-derivation. Dropping it
		// here (as this conversion used to) is what let an attachment come
		// back indistinguishable from a note once TextHit reached find.go: a
		// query for one kind had no way to filter the other kind's rows out
		// of the SAME Search call.
		out = append(out, knowledgefind.TextHit{
			Path: h.Path, SourceHash: h.SourceHash, Score: h.Score, Kind: string(h.Kind),
			// KB-7a's per-hit tier flag. Dropping it here (as this conversion
			// used to) is what left knowledge_find unable to say that an
			// answer came from the OR-ranked fallback (UAT 2026-09-13, D-07).
			Relaxed: h.FallbackMode,
		})
	}
	return out
}

// TermDocumentCounts implements knowledgefind.TextTermCounter: the per-word
// breakdown a relaxed (fallback-tier) answer is declared with (D-07).
func (s *findTextSearcher) TermDocumentCounts(_ context.Context, words string) ([]generated.VaultTermCount, error) {
	counts, err := s.ix.TermDocumentCounts(words)
	if err != nil {
		return nil, err
	}
	out := make([]generated.VaultTermCount, 0, len(counts))
	for _, c := range counts {
		out = append(out, generated.VaultTermCount{Term: c.Term, Documents: c.Documents})
	}
	return out, nil
}

func (s *findTextSearcher) NearestTerms(_ context.Context, words string, limit int) ([]generated.VaultTermCount, error) {
	terms, err := s.ix.NearMissVocabularyWithCounts(words, limit)
	if err != nil {
		return nil, err
	}
	out := make([]generated.VaultTermCount, 0, len(terms))
	for _, t := range terms {
		out = append(out, generated.VaultTermCount{Term: t.Term, Documents: t.Documents})
	}
	return out, nil
}

func (s *findTextSearcher) SourceHash(_ context.Context, path string) (string, bool, error) {
	return s.ix.SourceHashForPath(path)
}

// IndexFreshness implements knowledgefind.TextFreshnessReporter (A2(d)), so a
// zero-hit words refusal can tell a STALE index from a NEVER-BUILT one and say
// by how much it is behind. It converts pkg/knowledge's own IndexFreshness
// snapshot to knowledgefind's wire-facing shape — the same knowledge-native ->
// interface conversion every other method on this adapter performs, for the
// import-cycle reason in this type's header.
func (s *findTextSearcher) IndexFreshness(ctx context.Context) (knowledgefind.TextIndexFreshness, error) {
	if s == nil || s.ix == nil {
		return knowledgefind.TextIndexFreshness{}, nil
	}
	// FreshnessCached, not Freshness: this runs on the SEARCH hot path — once per
	// successful words query and once per zero-hit refusal — so it must not pay a
	// full filesystem walk or block on the reconcile lock every time (Finding 1).
	// It serves a recent lock-free snapshot; a genuinely stale index still
	// reports so, within one refresh interval.
	f := s.ix.FreshnessCached(ctx)
	return knowledgefind.TextIndexFreshness{
		Built:        f.Built,
		Fresh:        f.Fresh,
		ScannedFiles: f.Scanned,
		IndexedFiles: f.Indexed,
		ScannedNotes: f.ScannedNotes,
		IndexedNotes: f.IndexedNotes,
		PendingFiles: f.Pending,
		NewFiles:     f.New,
		ChangedFiles: f.Changed,
		RemovedFiles: f.Removed,
	}, nil
}

// Populated answers knowledgefind.TextSearcher's build-state question from
// TWO facts, not one: the manifest's EXISTENCE, and whether it CURRENTLY
// COVERS EVERY FILE a fresh Scan of the collection finds.
//
// # Why existence alone stopped being enough (F-9, reopened)
//
// The manifest used to be written only by a full SyncWith — a whole-
// collection reconcile — so its mere presence safely meant "this collection
// has been walked at least once". pkg/knowledge/author.go's instant-indexing
// path (docs/internal/design/knowledge-index-freshness.md) broke that
// premise: Index.UpdatePath, called after a single knowledge_edit write,
// saves the SAME manifest file after touching exactly ONE path. A single
// create on a collection nobody has ever swept therefore leaves a manifest
// that EXISTS but names one file out of however many actually live in the
// collection — and existence-only Populated() reported that collection as
// fully searched, so a words= query for a term in any of the other,
// never-touched notes came back "complete: true, 0 rows" instead of
// refusing. That is F-9
// (docs/internal/uat/uat-findings-knowledge-tools-2026-09-01-run2.md) byte
// for byte, reopened by a different write path.
//
// # Why the fix is a coverage check, not "skip the instant update"
//
// Preventing the instant-indexing write itself (skipping it on a
// never-before-built collection) was the other candidate and is deliberately
// not taken: pkg/knowledge's own TestIndexFreshness_* suite requires that a
// note just created through knowledge_edit is immediately findable — through
// knowledge_search — even on a collection with no prior index, and that
// guarantee must not regress. The defect is not that the manifest gained an
// entry; it is that Populated() treated ONE entry as proof the WHOLE
// collection was searched. So this checks the claim directly: LoadManifest's
// entry count against knowledge.Scan's count of what is actually on disk
// right now, using the SAME inventory function Index.SyncWith itself scans
// with (index.go), so "the manifest matches the collection" here means
// exactly what it would mean to a real sync. A manifest seeded by one or two
// instant writes on a large, unswept collection fails this comparison and is
// correctly reported unpopulated; a manifest a real sweep produced — however
// many notes it holds, including zero — passes it, because a genuinely empty,
// fully-synced collection's count (0) equals its own Scan's count (0).
//
// This also means a collection that WAS fully synced and has since drifted
// (a note added on disk after the last sync, with no re-sync since) now
// correctly reports unpopulated too, rather than the stale "yes" it used to.
// That is not a new failure mode — knowledge_describe's own drift branch
// (indexFreshness in knowledge_describe.go) already treats that state as
// untrustworthy for the same reason.
//
// # Cost
//
// knowledge.Scan is a stat-only filepath.WalkDir — no file content is read —
// and this only runs on the already-narrow path checkTextIndexPopulated
// takes: a words= query whose text search found ZERO hits. A query that
// matches something never pays for it.
//
// A stat or scan error is returned rather than swallowed. Per the interface's
// own note the caller folds an error into "not populated", which is the
// right default here: a zero-hit answer this layer cannot confirm was
// searched deserves no more trust than one it knows was not.
func (s *findTextSearcher) Populated(_ context.Context) (bool, error) {
	if s == nil || s.ix == nil {
		return false, nil
	}
	switch _, err := os.Stat(s.ix.ManifestPath()); {
	case err == nil:
		// Fall through to the coverage check below — existence alone is
		// necessary but, since instant-indexing, no longer sufficient.
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}

	manifest, err := knowledge.LoadManifest(s.ix.ManifestPath(), s.ix.Root())
	if err != nil {
		return false, err
	}
	scan, err := knowledge.Scan(s.ix.Root())
	if err != nil {
		return false, err
	}
	return manifest.Len() == len(scan.Entries), nil
}

// openFindStore opens the properties index for one collection, and REPAIRS
// it in place when it is not usable (UAT 2026-09-13, D-02).
//
// It returns the store, its closer, a refusal reason and a coverage caveat.
// The reason is non-empty only when no usable store could be produced, and
// says — in words a refusal can quote — why. The caveat is non-empty only
// when a usable store WAS produced whose recovery could not evaluate every
// file (Codex review 2026-09-14, finding 6): the store answers queries, but
// the answer is over "every file that could be read", not "the whole
// collection", and the caller must be able to say so. On a build with no
// properties index at all the reason is empty: that is a platform posture,
// not a fault, and Find() applies its own carve-outs for it.
//
// WHY IT SELF-HEALS. The usability test below (openUsableFindStore) is
// strict on purpose — a store holding fewer rows than there are files on disk
// answers a typed query with a confident, silently partial result, which is
// the one outcome this whole surface exists to prevent. But strict-and-
// refuse, on its own, produced the UAT's worst finding: ANY drift between the
// store and the disk closed the index for every caller until the next
// lifecycle reconcile happened to run. A note written by `write_file`, an
// import rewriting notes on the host side, a second knowledge base's folder
// removed, a stray `.trash-staging.txt` — each left the row count one off,
// and from then on every `knowledge_find` and every saved view answered
// "the properties index is not open" while the remedy the message named
// (`knowledge_describe check_integrity`) rebuilt nothing. Only a gateway
// restart, or ~160 s of luck with the watcher, recovered it.
//
// The drift is real and the refusal was honest; the missing piece was the
// REPAIR. Sync (sync.go) is the one operation that establishes coverage —
// it is incremental (unchanged notes are skipped by hash), idempotent, and
// coordinated with every other writer through the store's own reconcile lock
// (propindex.Reconcile, Codex finding 4): a concurrent lifecycle reconcile or
// a direct single-path write queues behind it rather than interleaving with
// its scan-and-delete. So an unusable store is synced right here, once, and
// re-checked; a query then reads a store that matches the disk instead of
// being refused for a mismatch nothing was going to fix.
func openFindStore(ctx context.Context, home, collectionRoot string) (propindex.Store, func() error, string, string) {
	path, err := knowledge.PropertiesIndexPath(home, collectionRoot)
	if err != nil {
		slog.Debug("vaultprops: knowledge_find: properties index path unavailable", "error", err)
		return nil, nil, "its location could not be resolved: " + err.Error(), ""
	}
	store, reason, caveat := openUsableFindStore(ctx, path, collectionRoot, nil)
	if store != nil {
		return store, store.Close, "", caveat
	}
	if !records.PropertyIndexAvailable {
		// No SQLite on this build: nothing to repair, and Find() names the
		// platform carve-out itself.
		return nil, nil, "", ""
	}

	stats, serr := Sync(ctx, home, collectionRoot, SyncOptions{})
	if serr != nil {
		slog.Warn("vaultprops: knowledge_find: the properties index was unusable and rebuilding it failed",
			"path", path, "collection_root", collectionRoot, "why_unusable", reason, "error", serr)
		return nil, nil, "rebuilding it failed: " + serr.Error(), ""
	}
	slog.Info("vaultprops: knowledge_find: the properties index was out of step with the collection and was brought up to date",
		"path", path, "collection_root", collectionRoot, "why", reason,
		"scanned", stats.Scanned, "indexed", stats.Indexed, "unchanged", stats.Unchanged,
		"removed", stats.Removed, "problems", len(stats.Problems))
	store, reason, caveat = openUsableFindStore(ctx, path, collectionRoot, &stats)
	if store != nil {
		return store, store.Close, "", caveat
	}
	slog.Warn("vaultprops: knowledge_find: the properties index is still unusable after being rebuilt",
		"path", path, "collection_root", collectionRoot, "why", reason)
	return nil, nil, "after rebuilding it, " + reason, ""
}

// openUsableFindStore opens the store at path and applies the usability
// tests, returning either a store the caller may query or the reason it
// may not, plus — when a usable store is returned — a coverage caveat for
// files the recovery could not evaluate.
//
// `synced`, when non-nil, is the result of the Sync that just ran. Coverage
// is judged against CURRENT DISK STATE (a fresh, stat-only scan), never
// against the sync's own scan count: that count is a snapshot from before
// the sync wrote anything, and trusting it was how a reconcile interleaved
// with a write could accept an incomplete index as complete (Codex finding
// 4's second half). From the fresh count, the files THAT SYNC reported
// unreadable are subtracted — a file the sync itself could not read will
// never have a row and must not keep the whole index closed — and each
// unreadable file becomes part of the caveat, because "all readable files
// are indexed" and "the collection was fully evaluated" are different
// facts and a query answer must not present the first as the second
// (Codex finding 6). A collection Sync found EMPTY is a usable, empty store
// rather than "not built yet".
func openUsableFindStore(ctx context.Context, path, collectionRoot string, synced *SyncStats) (propindex.Store, string, string) {
	if _, statErr := os.Stat(path); statErr != nil {
		// Never indexed (or platform without SQLite never created the file).
		// Not logged at Warn: this is the ordinary state of a collection
		// nobody has run a sync against yet.
		return nil, "it has not been built yet", ""
	}
	store, err := propindex.Open(ctx, path, propindex.Options{})
	if err != nil {
		slog.Debug("vaultprops: knowledge_find: properties index could not be opened", "path", path, "error", err)
		return nil, "it could not be opened: " + err.Error(), ""
	}
	closeUnusable := func(why string) (propindex.Store, string, string) {
		if cerr := store.Close(); cerr != nil {
			slog.Warn("vaultprops: knowledge_find: closing an unusable properties index failed", "path", path, "error", cerr)
		}
		return nil, why, ""
	}
	// NeedsFullIndex() ALONE stopped being sufficient the moment
	// author.go's instant-indexing path could write to this store, for the
	// EXACT same reason findTextSearcher.Populated (below) stopped trusting
	// bare manifest existence — see that method's doc comment for the full
	// argument; only the mechanics differ here.
	//
	// propindex.Index.open (sqlite.go) sets its needsFull flag from ONE
	// `SELECT COUNT(*) FROM notes` read at open time: needsFull = (notes ==
	// 0). That is a snapshot of "did this database hold zero rows the
	// instant I opened it", not "has this collection ever been fully
	// swept". A single knowledge_edit create on a collection nobody has
	// ever synced writes exactly one row into properties.db
	// (pkg/knowledge's own TestIndexFreshness_Create_PropertiesIndexRowLandsInstantly
	// proves it happens instantly). openFindStore opens a FRESH
	// propindex.Index on every knowledge_find call, so the very next call
	// reads notes=1, needsFull=false, and this function used to hand the
	// caller a store it trusted as complete — even though every other note
	// on disk, of any declared type, had never reached the properties index
	// at all. Reproduced and confirmed through the real tool surface in
	// pkg/vaultprops/f9b_typed_only_reproduction_test.go: a type=deal query
	// with no words= answered "COMPLETE: yes — 0 records matched" over a
	// store holding 1 of 4 deals.
	//
	// The coverage test asks the SAME question Populated() asks, translated
	// to this store's own shape: does the number of paths the store actually
	// holds match a fresh stat-only scan of what is on disk right now? A
	// store seeded by nothing but single-path instant writes fails that
	// comparison and is repaired by openFindStore's Sync before it is used
	// for a typed query.
	if store.NeedsFullIndex() && (synced == nil || synced.Scanned != 0) {
		return closeUnusable("it holds no files yet")
	}
	rowCount, expected, scanned, unreadable, coverErr := propertiesStoreCoverage(ctx, store, collectionRoot, synced)
	if coverErr != nil {
		// "I could not confirm coverage" gets the same treatment as "I know
		// it is not covered" — a zero-hit answer this layer cannot verify
		// deserves no more trust than one it knows is incomplete. The
		// reason is logged, not swallowed.
		slog.Warn("vaultprops: knowledge_find: could not verify properties index coverage; "+
			"treating it as unusable for this call", "path", path, "collection_root", collectionRoot, "error", coverErr)
		return closeUnusable("its coverage could not be verified: " + coverErr.Error())
	}
	if rowCount != expected {
		return closeUnusable(fmt.Sprintf("it holds %d of the %d files on disk", rowCount, expected))
	}
	// Usable — but if the recovery that produced this store could not read
	// every file, the answer drawn from it is over "every readable file", and
	// that fact travels WITH the store (finding 6). The caveat names the
	// files, because "some file somewhere" is not a fact an operator can act
	// on and a path is.
	return store, "", recoveryCoverageCaveat(scanned, unreadable)
}

// recoveryCoverageCaveat renders the coverage caveat for a usable store: ""
// when every file on disk was evaluated, and otherwise a sentence that says
// exactly which files are absent from every answer and why. The wording
// distinguishes the two facts finding 6 conflated — every readable file IS
// indexed; the collection was NOT fully evaluated — and is what
// knowledgefind stamps into the response's problems, making the answer
// complete:false.
//
// `total` is the number of files the coverage comparison counted on disk (the
// fresh stat-only scan), so the sentence's arithmetic is the same one the
// comparison itself used — for the recovery path AND for the pre-sync path,
// which knows the unreadable files from its own probe rather than from a
// Sync's report.
func recoveryCoverageCaveat(total int, unreadable []string) string {
	if len(unreadable) == 0 {
		return ""
	}
	names := append([]string(nil), unreadable...)
	sort.Strings(names)
	return fmt.Sprintf(
		"this knowledge base was not fully evaluated: every readable file is indexed, but %d of the %d files on disk could not be read, "+
			"so records in them cannot appear in any answer: %s",
		len(unreadable), total, strings.Join(names, ", "))
}

// propertiesStoreCoverage counts the paths the properties store currently
// holds and the number it is expected to hold: a fresh, stat-only
// knowledge.Scan of the collection — the same inventory function
// findTextSearcher.Populated compares the text-index manifest against, and
// for the identical reason: it is the one count that means "this store was
// actually built against everything on disk right now", independent of how
// each row got there (a full vaultprops.Sync, or however many single-path
// instant writes).
//
// THE EXPECTATION IS ALWAYS THE FRESH SCAN (Codex finding 4's second half).
// When `synced` is given — the recovery path, checking the store the Sync
// that just ran produced — the sync's own `Scanned` count is deliberately
// NOT used: it is a snapshot from before the sync wrote anything, and a file
// that landed on disk after that snapshot (or a row another writer committed
// against it) would be invisible to it, letting an incomplete index pass as
// complete. Instead the fresh scan is re-taken NOW and only the sync's
// UNREADABLE problems are subtracted from it — a file that sync could not
// read will never have a row, must not keep the index closed forever, and is
// returned so the caller can put it in the coverage caveat (finding 6).
//
// THE PRE-SYNC CHECK SUBTRACTS UNREADABLE FILES TOO (round-3 cut list,
// 2026-09-14 review). There is no Sync report to read yet on this path, so
// the files the store is short by are PROBED directly, under the same
// readability rule syncReconcileBody applies (resolve through the collection
// root refusing symlinks, then read the note) — see unreadableMissingNotes.
// Without this, one permanently unreadable note made every knowledge_find
// run a full-collection Sync: the store already covered every readable file,
// but the expectation counted the unreadable one, so the comparison failed
// and openFindStore repaired it again on every single call. A store short by
// more than unreadableProbeCap files is not probed — a gap that size is a
// real gap, and the Sync that repairs it is cheaper than reading that many
// files just to keep the store open.
//
// AllPaths is used rather than a raw COUNT(*), because AllPaths is the
// store's own documented "every path currently held" walk (store.go); a
// second, parallel counting query would be a second idea of what "every
// row" means to drift out of sync with the first.
func propertiesStoreCoverage(ctx context.Context, store propindex.Store, collectionRoot string, synced *SyncStats) (rowCount, expected, scanned int, unreadable []string, err error) {
	held := make(map[string]struct{})
	if walkErr := store.AllPaths(ctx, func(n propindex.IndexedNote) error {
		held[n.Path] = struct{}{}
		return nil
	}); walkErr != nil {
		return 0, 0, 0, nil, fmt.Errorf("walking the properties index: %w", walkErr)
	}
	rowCount = len(held)
	scan, err := knowledge.Scan(collectionRoot)
	if err != nil {
		return 0, 0, 0, nil, fmt.Errorf("scanning the collection: %w", err)
	}
	scanned = len(scan.Entries)
	expected = scanned
	if synced != nil {
		for _, p := range synced.Problems {
			if p.Reason == "unreadable" {
				expected--
				unreadable = append(unreadable, p.RelPath)
			}
		}
		return rowCount, expected, scanned, unreadable, nil
	}
	if rowCount != expected {
		unreadable = unreadableMissingNotes(collectionRoot, scan.Entries, held)
		expected -= len(unreadable)
	}
	return rowCount, expected, scanned, unreadable, nil
}

// unreadableProbeCap bounds how many missing files the pre-sync coverage
// check will open to decide whether the store's shortfall is nothing but
// permanently unreadable notes. The scenario the probe exists for is a
// HANDFUL of such files; a store short by more than this is treated as a
// genuine gap and repaired by the Sync path, which fixes it outright for the
// same cost the probe would have paid just to look.
const unreadableProbeCap = 8

// unreadableMissingNames decides, for the disk entries that have no row,
// which of them are unreadable by the SAME rule Sync itself applies
// (syncReconcileBody): resolve through the collection root — refusing a path
// that reaches its target only through a symlink — then read the note. A file
// that fails either step never gets a row from Sync, so its absence from the
// store is coverage, not a gap.
//
// Attachments are never probed: Sync never opens one (FR-039a), so an
// attachment with no row is always a genuine gap, never unreadability.
//
// A path that IS readable is not reported: it is a real gap, and leaving it
// out is what keeps the coverage comparison failing so the store gets
// repaired. Construction failures (root unresolvable) also report nothing —
// the conservative answer is "gap", which repairs.
func unreadableMissingNotes(collectionRoot string, entries []knowledge.ScanEntry, held map[string]struct{}) []string {
	var missing []knowledge.ScanEntry
	for _, e := range entries {
		if _, ok := held[e.RelPath]; ok {
			continue
		}
		if e.Kind == knowledge.ScanKindAttachment {
			continue
		}
		missing = append(missing, e)
	}
	if len(missing) == 0 || len(missing) > unreadableProbeCap {
		return nil
	}
	realRoot, err := knowledge.ResolveCollectionRoot(collectionRoot)
	if err != nil {
		return nil
	}
	fsys := knowledge.OSLinkFS()
	root, err := knowledge.NewCollectionRoot(fsys, realRoot)
	if err != nil {
		return nil
	}
	var unreadable []string
	for _, e := range missing {
		abs, resolveErr := root.ResolveContainedNoSymlink(fsys, e.RelPath)
		if resolveErr != nil {
			unreadable = append(unreadable, e.RelPath)
			continue
		}
		if _, readErr := knowledge.ReadNoteContent(fsys, abs); readErr != nil {
			unreadable = append(unreadable, e.RelPath)
		}
	}
	return unreadable
}
