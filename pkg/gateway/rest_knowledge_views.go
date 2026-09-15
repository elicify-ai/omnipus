// Omnipus — GET /api/v1/library/{workspace_id}/knowledge/views: every saved
// view a COLLECTION owns, file or no file (UAT D-13, web half).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY THIS ENDPOINT EXISTS
//
// knowledge_configure's create_view/write_view write
// `<vault>/.omnipus-vault/views/<slug>.yaml` and produce NO `.base` file, so
// a collection without a `.base` still owns saved views that answered
// correctly over the API — while having no UI surface at all, because the
// only views listing was base-views, which is FILE-addressed (`?path=<.base>`)
// and answers "path is not a base file" for everything else. This endpoint is
// the collection-addressed list: every view file records.LoadViews accepts,
// each with the slug it is actually addressed by, its label, its kind,
// whether it can be served, and — for an imported view — the `.base` it came
// from (KnowledgeBaseView.source).
//
// Base previews and dashboard embeds keep listing by `source`; nothing about
// base-views changes here.
//
// THE LOADER IS records.LoadViews AND NOTHING ELSE — the same single view
// parser base-views serves through (see that file's header for why a second
// parser here would be the same mistake at a different layer), and the same
// NewViewFindLoader whose ServeRefusal decides "servable" in both places, so
// the two listings can never disagree about a view's servability.
//
// THE COLLECTION BOUNDARY IS THE SHARED ONE (US-9 / FR-052 / FR-053): a
// collection_id outside this workspace's scope resolves through
// resolveScopedCollection like every collection-addressed knowledge endpoint
// and returns the empty-but-complete shape — never a 403 or 404, which would
// confirm the collection exists.
// ---------------------------------------------------------------------------

func (a *restAPI) handleKnowledgeViews(w http.ResponseWriter, r *http.Request, workspaceID string) {
	collectionID := strings.TrimSpace(r.URL.Query().Get("collection_id"))
	if collectionID == "" {
		jsonErr(w, http.StatusBadRequest, "collection_id is required")
		return
	}

	// Empty, never null, in every early-exit shape: "this collection owns no
	// views" is an answer a caller must be able to render without a nil
	// check, and an out-of-scope answer is indistinguishable from it by
	// design.
	out := gen.KnowledgeCollectionViews{
		CollectionId:    collectionID,
		Views:           []gen.KnowledgeBaseView{},
		UnloadableCount: 0,
	}

	// US-9 AS-2 as an empty answer rather than an error: another workspace's
	// knowledge base is not addressable, and saying so with a 403 would
	// confirm it exists.
	col, inScope := a.resolveScopedCollection(workspaceID, collectionID)
	if !inScope {
		jsonOK(w, out)
		return
	}

	if !a.allowKnowledgeRetrieval(w, r, workspaceID, knowledgeRead) {
		return
	}

	// LoadViews needs the schemas for the same reason base-views does: without
	// them a view naming a type or property the vault no longer declares
	// loads clean and queries nothing. With them it is reported unservable —
	// which is the honest answer this surface exists to give.
	schemas, _, schemaErr := records.LoadSchemas(col.Root)
	if schemaErr != nil {
		logger.ErrorCF("rest", "knowledge: loading record schemas for the views list",
			map[string]any{"workspace_id": workspaceID, "error": schemaErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	views, report, viewsErr := records.LoadViews(col.Root, schemas)
	if viewsErr != nil {
		logger.ErrorCF("rest", "knowledge: loading saved views for the views list",
			map[string]any{"workspace_id": workspaceID, "error": viewsErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	loader := records.NewViewFindLoader(views)
	for _, v := range views.Views() {
		entry := gen.KnowledgeBaseView{Name: v.Name(), Label: v.DisplayLabel()}
		if v.Def.Kind != nil && strings.TrimSpace(string(*v.Def.Kind)) != "" {
			kind := string(*v.Def.Kind)
			entry.Kind = &kind
		}
		// D-13: an imported view names the `.base` it came from; an authored
		// view has no source, and its absence is precisely the signal this
		// endpoint exists to carry (the view belongs to the collection).
		if src := v.DeclaredSource(); src != "" {
			source := src
			entry.Source = &source
		}
		if refusal, refused := loader.ServeRefusal(v.Name()); refused {
			unservable := true
			entry.Unservable = &unservable
			reason := refusal.Reason
			if refusal.Remedy != "" {
				reason += " — " + refusal.Remedy
			}
			entry.UnservableReason = &reason
		}
		out.Views = append(out.Views, entry)
	}

	// A rejected view file has no usable slug, so it cannot be listed — but
	// silently showing fewer views than the vault declares is the same silent
	// loss base-views refuses. Count the FILES and name each rejection, the
	// D-70 rule: a count alone says nothing a reader can act on.
	unloadable := []gen.KnowledgeBaseUnloadableView{}
	for _, rej := range report.Rejections {
		out.UnloadableCount += len(rej.Paths)
		entry := gen.KnowledgeBaseUnloadableView{
			Paths:  append([]string(nil), rej.Paths...),
			Code:   string(rej.Code),
			Reason: rej.Reason,
		}
		if rej.Name != "" {
			name := rej.Name
			entry.Name = &name
		}
		unloadable = append(unloadable, entry)
	}
	out.Unloadable = &unloadable

	jsonOK(w, out)
}
