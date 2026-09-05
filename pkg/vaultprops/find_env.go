// Omnipus — the one open-everything path for evaluating queries and saved
// views over a collection: knowledgefind.Deps plus the loaded schema and view
// sets, shared by knowledge_find (FindTool.buildDeps) and the gateway's
// view-result endpoint.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultprops

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
)

// FindEnv is everything one evaluation over one collection needs, opened
// together so the two consumers — the knowledge_find tool and the gateway's
// GET .../knowledge/view handler — can never drift apart in what they load or
// how they load it.
//
// Deps is knowledgefind's own dependency set, ready for Find/Call. Schemas,
// Views and ViewReport are the SAME objects Deps was built from (Deps.Views
// wraps Views in a ViewFindLoader), exposed because the view-result endpoint
// needs more than the loader interface carries: EffectiveParts on the
// SavedView, unit_property on the schema, and the load report so a view that
// was REJECTED can be reported by its rejection rather than as "unknown".
type FindEnv struct {
	Deps       knowledgefind.Deps
	Schemas    *records.SchemaSet
	Views      *records.ViewSet
	ViewReport *records.ViewLoadReport
}

// OpenFindEnv opens everything one evaluation needs and returns a cleanup
// function that is always safe to call (nil-checked internally), even on a
// partial build — so `defer closeEnv()` is correct whether OpenFindEnv
// returned an error or not.
//
// This is FindTool.buildDeps' moved body (the method is now a shim over it);
// every comment below predates the move and still holds.
func OpenFindEnv(ctx context.Context, home string, col knowledge.ScopedCollection) (FindEnv, func(), error) {
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
		return FindEnv{}, closeAll, err
	}

	schemas, _, err := records.LoadSchemas(root.Path())
	if err != nil {
		return FindEnv{}, closeAll, fmt.Errorf("loading record schemas: %w", err)
	}

	views, viewReport, err := records.LoadViews(root.Path(), schemas)
	if err != nil {
		return FindEnv{}, closeAll, fmt.Errorf("loading saved views: %w", err)
	}

	// index_epoch, read BEFORE anything else opens: a cursor check is
	// meaningless against an epoch this call could not honestly read, and
	// epoch.go's IndexEpoch distinguishes "never touched" (0, nil) from
	// "unreadable" (0, non-nil) precisely so this refuses rather than
	// silently treating a corrupt epoch file as a fresh index.
	epoch, err := knowledge.IndexEpoch(home, col.Root)
	if err != nil {
		return FindEnv{}, closeAll, fmt.Errorf("reading the index generation counter: %w", err)
	}

	ix, err := knowledge.OpenIndex(home, col.Root)
	if err != nil {
		return FindEnv{}, closeAll, fmt.Errorf("opening the text index: %w", err)
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
	store, closeStore := openFindStore(ctx, home, col.Root)
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

	return FindEnv{
		Deps: knowledgefind.Deps{
			Schemas:    schemas,
			Store:      store,
			PathPrefix: "", // the whole mounted collection — knowledge_find has no per-call folder narrowing argument
			Text:       &findTextSearcher{ix: ix},
			Views:      records.NewViewFindLoader(views),
			Resolve:    resolve,
			Epoch:      epoch,
		},
		Schemas:    schemas,
		Views:      views,
		ViewReport: viewReport,
	}, closeAll, nil
}
