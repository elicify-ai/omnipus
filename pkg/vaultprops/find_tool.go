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
	if unknown := unacceptedFindArgs(args); len(unknown) > 0 {
		return tools.ErrorResult(fmt.Sprintf(
			"knowledge_find: %s is not an argument of knowledge_find; accepted: %s",
			strings.Join(unknown, ", "), strings.Join(knowledgefind.AcceptedParameters, ", ")))
	}

	scope, _ := knowledge.ResolveTurnScope(ctx, t.home)
	col, ok := scope.Select("")
	if !ok {
		// NAMES THE REMEDY, NOT JUST THE OBSTACLE. Listing the collections in
		// scope without saying what to do with them is what made the earlier
		// refusal read as recoverable when it was not: this tool has no
		// argument that accepts one of those names.
		return tools.ErrorResult(fmt.Sprintf(
			"knowledge_find: no single knowledge base is unambiguously in scope for this workspace "+
				"(none mounted, or more than one); in scope: %s. knowledge_find has NO `collection` "+
				"argument — it queries the one knowledge base in scope, so naming one here is not "+
				"possible. Use knowledge_describe or knowledge_read, which do take a `collection`, "+
				"or run this query in a workspace with exactly one knowledge base",
			joinFindScopeNames(scope.Names())))
	}

	raw, err := json.Marshal(args)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("knowledge_find: could not encode arguments: %v", err))
	}

	deps, closeDeps, err := t.buildDeps(ctx, col)
	defer closeDeps()
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("knowledge_find: %v", err))
	}

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

// buildDeps opens everything one call needs and returns a cleanup function
// that is always safe to call (nil-checked internally), even on a partial
// build — so `defer closeDeps()` above is correct whether buildDeps returned
// an error or not.
func (t *FindTool) buildDeps(ctx context.Context, col knowledge.ScopedCollection) (knowledgefind.Deps, func(), error) {
	closers := make([]func() error, 0, 2)
	closeAll := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			if cerr := closers[i](); cerr != nil {
				slog.Warn("vaultprops: knowledge_find: closing a dependency failed", "error", cerr)
			}
		}
	}

	root, err := knowledge.NewCollectionRoot(knowledge.OSLinkFS(), col.Root)
	if err != nil {
		return knowledgefind.Deps{}, closeAll, err
	}

	schemas, _, err := records.LoadSchemas(root.Path())
	if err != nil {
		return knowledgefind.Deps{}, closeAll, fmt.Errorf("loading record schemas: %w", err)
	}

	views, _, err := records.LoadViews(root.Path(), schemas)
	if err != nil {
		return knowledgefind.Deps{}, closeAll, fmt.Errorf("loading saved views: %w", err)
	}

	// index_epoch, read BEFORE anything else opens: a cursor check is
	// meaningless against an epoch this call could not honestly read, and
	// epoch.go's IndexEpoch distinguishes "never touched" (0, nil) from
	// "unreadable" (0, non-nil) precisely so this refuses rather than
	// silently treating a corrupt epoch file as a fresh index.
	epoch, err := knowledge.IndexEpoch(t.home, col.Root)
	if err != nil {
		return knowledgefind.Deps{}, closeAll, fmt.Errorf("reading the index generation counter: %w", err)
	}

	ix, err := knowledge.OpenIndex(t.home, col.Root)
	if err != nil {
		return knowledgefind.Deps{}, closeAll, fmt.Errorf("opening the text index: %w", err)
	}
	closers = append(closers, ix.Close)

	// The properties index (Store) is BEST-EFFORT TO OPEN — nil is a
	// documented, supported wiring, exactly like knowledge_describe's
	// injected OpenPropertyIndexFunc — but it is NOT optional for what
	// Find() can actually answer. findRecords requires a non-nil Store
	// UNCONDITIONALLY, even for a bare `words` query with no typed filter:
	// the store is what enumerates the candidate population to text-search
	// within, not only typed metadata, so a `words`-only query still refuses
	// with "the properties index is not open, so no record can be read"
	// when Store is nil. This function does not paper over that by forcing
	// the open to succeed — Find() already reports the honest refusal by
	// name — it only means "no dependency this call never touches" is the
	// WRONG mental model here; every non-explain, non-cursor-error query
	// reaches the store.
	//
	// THE PRODUCTION WRITER EXISTS — this paragraph used to say it did not,
	// and that stale sentence was read as a live defect on 2026-08-31 and
	// reported as "the properties index has no production writer". It is
	// dated here so the next reader can check it rather than trust it.
	//
	// Wired 2026-08-30 in commit 015afa0e ("feat(records): build the
	// properties-index sync pipeline", Stage 4/Wave 2.5):
	// pkg/gateway/knowledge_lifecycle.go defaults its `propsSync` hook to
	// vaultprops.Sync, and calls it UNCONDITIONALLY after every text-index
	// reconcile — on a brand-new mount and on every later one alike. Sync
	// writes through Store.UpsertNote (two call sites in sync.go: the
	// attachment row and the note row). So on a real install the store is
	// populated by the same lifecycle that builds the text index, and the
	// refusal described above is the store-unopenable case, NOT the
	// steady state.
	//
	// ONE HALF OF THE OLD CLAIM IS STILL TRUE, and collapsing the two would
	// swap one wrong comment for another: propindex.IndexNote — the ordering
	// wrapper in pkg/records/propindex/store.go — genuinely has no caller
	// outside pkg/records/propindex/ordering_test.go. Sync calls
	// Store.UpsertNote directly rather than going through it. That is a real
	// observation about IndexNote's reachability and nothing more; it does
	// not mean the index goes unwritten.
	store, closeStore := openFindStore(ctx, t.home, col.Root)
	if closeStore != nil {
		closers = append(closers, closeStore)
	}

	var resolve records.RelationResolver
	if store != nil {
		walk, werr := knowledge.WalkContained(knowledge.OSLinkFS(), root)
		if werr != nil {
			slog.Warn("vaultprops: knowledge_find: could not walk collection for relation resolution; "+
				"relation comparisons will report unresolved", "root", col.Root, "error", werr)
		} else {
			notes := knowledge.NewNoteIndex(walk.Files)
			resolve = NewRelationResolver(ctx, notes, store).AsFunc()
		}
	}

	return knowledgefind.Deps{
		Schemas:    schemas,
		Store:      store,
		PathPrefix: "", // the whole mounted collection — knowledge_find has no per-call folder narrowing argument
		Text:       &findTextSearcher{ix: ix},
		Views:      records.NewViewFindLoader(views),
		Resolve:    resolve,
		Epoch:      epoch,
	}, closeAll, nil
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

func (s *findTextSearcher) Search(_ context.Context, words string, limit int) ([]knowledgefind.TextHit, error) {
	hits, err := s.ix.Search(words, limit)
	if err != nil {
		return nil, err
	}
	out := make([]knowledgefind.TextHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, knowledgefind.TextHit{Path: h.Path, SourceHash: h.SourceHash, Score: h.Score})
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

// Populated answers knowledgefind.TextSearcher's build-state question from the
// MANIFEST'S EXISTENCE, not from a document count.
//
// The manifest is written when a build pass completes, so its presence means
// "this collection has been walked at least once" regardless of what that walk
// found. That is exactly the distinction the interface asks for and the reason
// DocCount cannot answer it: a genuinely empty vault whose index finished
// building reports 0 documents, and so does one that was never built at all.
// Reading the count would collapse an honest zero and an unsearched vault back
// into the single indistinguishable answer that produced F-9.
//
// A stat error other than not-exists is returned rather than swallowed. Per the
// interface's own note the caller folds an error into "not populated", which is
// the right default here: a zero-hit answer this layer cannot confirm was
// searched deserves no more trust than one it knows was not.
func (s *findTextSearcher) Populated(_ context.Context) (bool, error) {
	if s == nil || s.ix == nil {
		return false, nil
	}
	switch _, err := os.Stat(s.ix.ManifestPath()); {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

// openFindStore opens the properties index for one collection, best-effort.
// Every failure mode is non-fatal here on purpose (see buildDeps' comment):
// no SQLite on this platform, the index never built, or a path error all
// return (nil, nil) so the caller proceeds with Store=nil and Find() itself
// decides, per query, whether that dependency was actually needed.
func openFindStore(ctx context.Context, home, collectionRoot string) (propindex.Store, func() error) {
	path, err := knowledge.PropertiesIndexPath(home, collectionRoot)
	if err != nil {
		slog.Debug("vaultprops: knowledge_find: properties index path unavailable", "error", err)
		return nil, nil
	}
	if _, statErr := os.Stat(path); statErr != nil {
		// Never indexed (or platform without SQLite never created the file).
		// Not logged at Warn: this is the ordinary state of a collection
		// nobody has run check_integrity/indexing against yet.
		return nil, nil
	}
	store, err := propindex.Open(ctx, path, propindex.Options{})
	if err != nil {
		slog.Debug("vaultprops: knowledge_find: properties index could not be opened", "path", path, "error", err)
		return nil, nil
	}
	if store.NeedsFullIndex() {
		if cerr := store.Close(); cerr != nil {
			slog.Warn("vaultprops: knowledge_find: closing an unusable properties index failed", "path", path, "error", cerr)
		}
		return nil, nil
	}
	return store, store.Close
}

// joinFindScopeNames renders a workspace's addressable collection names for
// a refusal message, matching every other knowledge_* tool's "in scope: …"
// wording.
func joinFindScopeNames(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
