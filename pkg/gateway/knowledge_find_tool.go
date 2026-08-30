// Omnipus — ADR-068 D15.3: the knowledge_find tool adapter, wiring
// pkg/records/knowledgefind's Deps (Schemas, Store, Text, Views, Resolve,
// Epoch) to real, open collections.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE LIVES IN pkg/gateway, AND NOT BESIDE THE OTHER FIVE
//
// knowledge_describe/read/edit/restructure/configure are all tools.Tool
// implementations already, built directly in pkg/knowledge with a
// ToolDeps/AuthoringDeps constructor — pkg/agent/knowledge_tools.go (the
// execution registry) and pkg/gateway/knowledge_tools_wire.go (the metadata
// catalog) construct them and are done.
//
// knowledge_find is different in kind, not degree: pkg/records/knowledgefind
// exposes a package function (Call(ctx, Deps, raw)), and building its Deps
// needs BOTH pkg/knowledge (the bleve index, NoteIndex) AND
// pkg/records/propindex (the Store) joined together — which is exactly what
// pkg/vaultprops exists to do, and pkg/knowledge cannot import pkg/vaultprops
// (vaultprops imports pkg/knowledge; the reverse edge is the cycle
// pkg/vaultprops/reader.go's own header explains). pkg/gateway already
// depends on all four packages, which is the same reason
// knowledge_tools_wire.go's metadata registration lives here rather than in
// pkg/knowledge — see that file's header for the identical shape of this
// argument.
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
	scope, _ := knowledge.ResolveTurnScope(ctx, t.home)
	col, ok := scope.Select("")
	if !ok {
		return tools.ErrorResult(fmt.Sprintf(
			"knowledge_find: no single knowledge base is unambiguously in scope for this workspace "+
				"(none mounted, or more than one); in scope: %s", joinScopeNames(scope.Names())))
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

// buildDeps opens everything one call needs and returns a cleanup function
// that is always safe to call (nil-checked internally), even on a partial
// build — so `defer closeDeps()` above is correct whether buildDeps returned
// an error or not.
func (t *FindTool) buildDeps(ctx context.Context, col knowledge.ScopedCollection) (knowledgefind.Deps, func(), error) {
	closers := make([]func() error, 0, 2)
	closeAll := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			if cerr := closers[i](); cerr != nil {
				slog.Warn("gateway: knowledge_find: closing a dependency failed", "error", cerr)
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

	// The properties index (Store) is BEST-EFFORT, exactly like
	// knowledge_describe's injected OpenPropertyIndexFunc: nil is a
	// documented, supported wiring, because Find() itself already refuses
	// honestly — by name — whenever a query actually needs a Store it does
	// not have (find.go: "the properties index is not open, so no record
	// can be read"), and the platform gate (records.RequirePropertyIndex)
	// inside Find() covers the "no SQLite in this build" case on its own. A
	// plain-word `words`-only query needs neither and must not be refused
	// for a dependency it never touches.
	store, closeStore := openFindStore(ctx, t.home, col.Root)
	if closeStore != nil {
		closers = append(closers, closeStore)
	}

	var resolve records.RelationResolver
	if store != nil {
		walk, werr := knowledge.WalkContained(knowledge.OSLinkFS(), root)
		if werr != nil {
			slog.Warn("gateway: knowledge_find: could not walk collection for relation resolution; "+
				"relation comparisons will report unresolved", "root", col.Root, "error", werr)
		} else {
			notes := knowledge.NewNoteIndex(walk.Files)
			resolve = vaultprops.NewRelationResolver(ctx, notes, store).AsFunc()
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
// types (TextHit, generated.VaultTermCount) — see this file's header for
// the import cycle that forbids doing so from pkg/knowledge itself. Every
// piece of actual logic is pkg/knowledge's own exported operation
// (Search, SourceHashForPath, NearMissVocabularyWithCounts); this type only
// converts between knowledge-native and wire-facing shapes.
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

// openFindStore opens the properties index for one collection, best-effort.
// Every failure mode is non-fatal here on purpose (see buildDeps' comment):
// no SQLite on this platform, the index never built, or a path error all
// return (nil, nil) so the caller proceeds with Store=nil and Find() itself
// decides, per query, whether that dependency was actually needed.
func openFindStore(ctx context.Context, home, collectionRoot string) (propindex.Store, func() error) {
	path, err := knowledge.PropertiesIndexPath(home, collectionRoot)
	if err != nil {
		slog.Debug("gateway: knowledge_find: properties index path unavailable", "error", err)
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
		slog.Debug("gateway: knowledge_find: properties index could not be opened", "path", path, "error", err)
		return nil, nil
	}
	if store.NeedsFullIndex() {
		if cerr := store.Close(); cerr != nil {
			slog.Warn("gateway: knowledge_find: closing an unusable properties index failed", "path", path, "error", cerr)
		}
		return nil, nil
	}
	return store, store.Close
}

// joinScopeNames renders a workspace's addressable collection names for a
// refusal message, matching every other knowledge_* tool's "in scope: …"
// wording.
func joinScopeNames(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
