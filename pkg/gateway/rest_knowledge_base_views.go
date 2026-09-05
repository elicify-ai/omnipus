// Omnipus — GET /api/v1/library/{workspace_id}/knowledge/base-views: which
// saved views came from one `.base` file (view-kinds-design-2026-09-03 §7).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"path"
	"path/filepath"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY THIS ENDPOINT EXISTS
//
// Import is one-shot (FR-102): a `.base` file's views were translated into
// `<vault>/.omnipus-vault/views/<slug>.yaml` and the `.base` is never read
// again on the query path. Nothing, however, listed WHICH saved views came
// from WHICH source file — so the SPA read the `.base` itself, walked its
// `views:` block with a hand-rolled parser, and re-derived each view's slug by
// mirroring the importer's slugger. Two defects followed, both confirmed:
//
//	1. The importer's SlugRegistry appends a collision counter (`-2`) over
//	   everything it has already handed out. A mirror outside the importer
//	   cannot reconstruct that counter, so two view names that kebab alike
//	   ("A/B" and "A B") collapsed onto ONE slug: the second tab fetched the
//	   FIRST view and rendered its rows under the second view's name, with two
//	   React children sharing a key.
//
//	2. The walk took any `name:` line inside a view item as the view's name, so
//	   a nested mapping key (`summaries:\n  name: count`) clobbered the display
//	   name — and the clobbered name derived a slug no view file has, which
//	   answered as the `unknown_view` refusal on a perfectly valid view.
//
// Both are re-derivations of facts the server already holds. Every imported
// view file records the `source` it came from, its own `name` (the slug the
// importer actually used, counter and all) and its `label`. This endpoint
// reports them, and the SPA re-derives nothing — the .base parser is deleted.
//
// THE LOADER IS records.LoadViews AND NOTHING ELSE. There is one view parser
// in this system (pkg/records/view.go, which states in its own header why
// three readers were collapsed into one); a second one here — even a small
// one, even only to read a `source:` key — would be the same mistake at a
// different layer.
//
// THE CONFINEMENT CHAIN IS THE EXISTING ONE. The `.base` is path-addressed, so
// it resolves exactly as every rest_library.go handler resolves a path —
// workspace.Exists → library.CleanRelPath → library.OpenRoot → Root.StatFile,
// which enforces containment through os.Root at the syscall boundary — and
// the collection is then found by collectionContaining, which only ever
// considers collections inside THIS workspace's scope. A `.base` outside every
// in-scope collection answers is_knowledge_base=false: a stated answer, not a
// 403, so the response cannot confirm what exists in another workspace.
// ---------------------------------------------------------------------------

func (a *restAPI) handleKnowledgeBaseViews(w http.ResponseWriter, r *http.Request, workspaceID string) {
	raw := r.URL.Query().Get("path")
	if strings.TrimSpace(raw) == "" {
		jsonErr(w, http.StatusBadRequest, "path is required")
		return
	}
	rel, err := library.CleanRelPath(raw)
	if err != nil || rel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	// A `.base` file is the only thing this question has an answer for. Any
	// other file would answer "no views", which reads as a fact about the file
	// rather than as the category error it is.
	if !strings.EqualFold(path.Ext(rel), ".base") {
		jsonErr(w, http.StatusBadRequest, "path is not a base file")
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer func() {
		if cerr := root.Close(); cerr != nil {
			logger.WarnCF("rest", "knowledge: closing the library root after listing base views",
				map[string]any{"workspace_id": workspaceID, "error": cerr.Error()})
		}
	}()

	if _, statErr := root.StatFile(rel); statErr != nil {
		mapLibraryErr(w, "knowledge base views", workspaceID, statErr)
		return
	}

	out := gen.KnowledgeBaseViews{
		BasePath:        rel,
		IsKnowledgeBase: false,
		// Empty, never null: "this base imported no views" is an answer the
		// caller must be able to render, and a nil array makes every consumer
		// write its own nil-check before it can.
		Views:           []gen.KnowledgeBaseView{},
		UnloadableCount: 0,
	}

	col, inKB := a.collectionContaining(workspaceID, root.HostPath(rel))
	if !inKB {
		jsonOK(w, out)
		return
	}
	out.IsKnowledgeBase = true
	collectionID := knowledgeCollectionID(col.Root)
	out.CollectionId = &collectionID

	// The `source` an imported view carries is VAULT-relative, so the question
	// "which views came from this file" is asked in the vault's own spelling.
	// It is derived from the RESOLVED real paths — the same basis
	// collectionContaining matched on — because a collection reached through a
	// symlinked mount would otherwise produce a path no view ever recorded.
	source, srcOK := viewSourceRelPath(col.Root, root.HostPath(rel))
	if !srcOK {
		// Unreachable through collectionContaining, which already established
		// the file is inside this root. Refusing beats emitting a `source` the
		// match below would silently compare against nothing.
		logger.ErrorCF("rest", "knowledge: base file is inside a collection but not relative to its root",
			map[string]any{"workspace_id": workspaceID, "collection_root": col.Root})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	out.Source = &source

	// The collection root as the WORKSPACE spells it, taken by subtracting the
	// vault-relative tail from the workspace-relative path rather than by
	// re-resolving symlinks: both ends of that subtraction name the same file,
	// so the answer needs no second filesystem walk to be right.
	collectionRoot := workspaceRelCollectionRoot(rel, source)
	out.CollectionRoot = &collectionRoot

	// LoadViews needs the schemas, and the distinction is not a convenience:
	// without them a view naming a type or property the vault no longer
	// declares loads clean and queries nothing (LoadViews' own doc comment).
	// This surface reports such a view as unloadable, which is only possible
	// if the schemas were passed.
	schemas, _, schemaErr := records.LoadSchemas(col.Root)
	if schemaErr != nil {
		logger.ErrorCF("rest", "knowledge: loading record schemas for base views",
			map[string]any{"workspace_id": workspaceID, "error": schemaErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	views, report, viewsErr := records.LoadViews(col.Root, schemas)
	if viewsErr != nil {
		logger.ErrorCF("rest", "knowledge: loading saved views for base views",
			map[string]any{"workspace_id": workspaceID, "error": viewsErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// The SAME loader knowledge_find serves through, so "servable here" and
	// "servable there" are one answer rather than two that can drift.
	loader := records.NewViewFindLoader(views)
	for _, v := range views.Views() {
		if v.DeclaredSource() != source {
			continue
		}
		entry := gen.KnowledgeBaseView{Name: v.Name(), Label: v.DisplayLabel()}
		if v.Def.Kind != nil && strings.TrimSpace(string(*v.Def.Kind)) != "" {
			kind := string(*v.Def.Kind)
			entry.Kind = &kind
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

	// A rejected view has no usable name, so it cannot be a tab — but silently
	// showing fewer tabs than the base has views is the same silent loss this
	// whole surface exists to end. Count the FILES, so a duplicate-name
	// conflict (one rejection naming several files) is not reported as one.
	for _, rej := range report.Rejections {
		if rej.Source == source {
			out.UnloadableCount += len(rej.Paths)
		}
	}

	jsonOK(w, out)
}

// viewSourceRelPath is the vault-relative, forward-slash path a view imported
// from hostPath would carry in its own `source` field.
//
// Both ends are resolved through symlinks first, matching what
// collectionContaining compared, so a collection mounted through a link
// produces the path the importer actually wrote rather than the spelling the
// caller happened to use.
func viewSourceRelPath(collectionRoot, hostPath string) (string, bool) {
	realRoot, err := filepath.EvalSymlinks(collectionRoot)
	if err != nil {
		realRoot = filepath.Clean(collectionRoot)
	}
	realPath, err := filepath.EvalSymlinks(hostPath)
	if err != nil {
		realPath = filepath.Clean(hostPath)
	}
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil {
		return "", false
	}
	slashed := filepath.ToSlash(rel)
	if slashed == "." || slashed == ".." || strings.HasPrefix(slashed, "../") {
		return "", false
	}
	return slashed, true
}

// workspaceRelCollectionRoot subtracts a file's vault-relative tail from its
// workspace-relative path, leaving the collection root as the workspace spells
// it — "." when the work tree itself is the collection, matching the spelling
// KnowledgeBaseInfo.root_path uses for the same folder.
func workspaceRelCollectionRoot(workspaceRel, vaultRel string) string {
	trimmed := strings.TrimSuffix(workspaceRel, vaultRel)
	trimmed = strings.TrimSuffix(trimmed, "/")
	if trimmed == "" {
		return "."
	}
	return trimmed
}
