// Omnipus — saved views as rows of the Library listing (#1017, server side):
// GET /api/v1/library/{workspace_id}/entries?include_views=true.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHAT THIS ADDS
//
// Saved views stay stored in <knowledge base>/.omnipus-vault/views/ (founder
// LQ1 C, 2026-11-07). With include_views=true the listing of a folder inside
// a knowledge base K also carries one row per view PLACED in that folder
// (records.ViewPlacement: next to its source .base, otherwise K's root). The
// row describes the view's real storage file — its path is that file, so a
// row's path is outside the listed folder by design (contract L7, risk R3).
//
// One load per request: K is found once, its schemas and views are loaded
// once through loadKnowledgeViewSet — the same loader the views endpoint
// uses, so the two can never disagree about which views exist (L4) — and
// placement probes are memoized per folder.
//
// Not a knowledge query: nothing here touches allowKnowledgeRetrieval (L15).
// A load failure never fails the listing (L14): it is logged and the
// ordinary rows are returned unchanged.
// ---------------------------------------------------------------------------

// listingKnowledgeBase is K for a listed folder: the deepest knowledge base
// enclosing (or equal to) rel, found through the confined root, and accepted
// only when its real root lies within one of the workspace's in-scope
// knowledge collections (knowledge.ResolveScope).
type listingKnowledgeBase struct {
	rel      string // workspace-relative root of K ("" = the work tree root)
	realRoot string // resolved real path of K's root
}

// findListingKnowledgeBase walks rel and its ancestors and returns the deepest
// one that is a knowledge base. A level whose detection cannot complete stops
// the walk with no answer: picking a shallower K there would place views by
// the wrong root (detectKnowledgeBaseInRoot already logged the failure).
func (a *restAPI) findListingKnowledgeBase(root *library.Root, workspaceID, rel string) (listingKnowledgeBase, bool) {
	for p := rel; ; p = parentRel(p) {
		isKB, established := detectKnowledgeBaseInRoot(root, p)
		if !established {
			return listingKnowledgeBase{}, false
		}
		if isKB {
			realRoot, err := knowledge.ResolveCollectionRoot(root.HostPath(p))
			if err != nil {
				logger.WarnCF("rest", "library listing: knowledge base root unresolvable; no view rows",
					map[string]any{"workspace_id": workspaceID, "knowledge_base": p, "error": err.Error()})
				return listingKnowledgeBase{}, false
			}
			if !knowledgeRootInScope(a.homePath, workspaceID, realRoot) {
				return listingKnowledgeBase{}, false
			}
			return listingKnowledgeBase{rel: p, realRoot: realRoot}, true
		}
		if p == "" {
			return listingKnowledgeBase{}, false
		}
	}
}

// knowledgeRootInScope reports whether realRoot is one of the workspace's
// in-scope collections or lies inside one (a nested knowledge base, which
// scope discovery does not descend into but whose files the enclosing
// collection's grant already covers).
func knowledgeRootInScope(home, workspaceID, realRoot string) bool {
	for _, c := range knowledge.ResolveScope(home, workspaceID).Collections() {
		if realRoot == c.Root || strings.HasPrefix(realRoot, c.Root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func parentRel(rel string) string {
	if rel == "" {
		return ""
	}
	parent := path.Dir(rel)
	if parent == "." {
		return ""
	}
	return parent
}

func joinRel(base, rel string) string {
	switch {
	case rel == "" || rel == records.ViewPlacementRoot:
		return base
	case base == "":
		return rel
	default:
		return base + "/" + rel
	}
}

// loadKnowledgeViewSet loads a knowledge base's saved views WITH its record
// schemas — the one loader shared by the views endpoint and the listing, so
// both see exactly the same set (a view naming a type the schemas no longer
// declare is rejected in both).
func loadKnowledgeViewSet(realRoot string) (*records.ViewSet, *records.ViewLoadReport, error) {
	schemas, _, err := records.LoadSchemas(realRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("loading record schemas: %w", err)
	}
	views, report, err := records.LoadViews(realRoot, schemas)
	if err != nil {
		return nil, nil, fmt.Errorf("loading saved views: %w", err)
	}
	return views, report, nil
}

// savedViewKindAndSource returns a view's declared kind and source, each nil
// when the file declares none (blank counts as none) — never invented (L8).
func savedViewKindAndSource(v *records.SavedView) (kind, source *string) {
	if v.Def.Kind != nil && strings.TrimSpace(string(*v.Def.Kind)) != "" {
		k := string(*v.Def.Kind)
		kind = &k
	}
	if src := v.DeclaredSource(); src != "" {
		s := src
		source = &s
	}
	return kind, source
}

func setViewFacts(entry *gen.LibraryEntry, v *records.SavedView, collectionID string) {
	isView := true
	kind, source := savedViewKindAndSource(v)
	entry.IsView = &isView
	entry.View = &gen.LibraryEntryView{
		Name:         v.Name(),
		Label:        v.DisplayLabel(),
		CollectionId: collectionID,
		Kind:         kind,
		Source:       source,
	}
}

// addLibraryViewRows implements include_views=true for the listing of rel.
// entries is the already-built listing; the result is that listing plus the
// view rows placed in rel (or, for the storage folder itself, the same rows
// with their loaded views marked — L11), in the listing's one sort order.
func (a *restAPI) addLibraryViewRows(
	root *library.Root, workspaceID, rel string, includeHidden bool, entries []gen.LibraryEntry,
) []gen.LibraryEntry {
	kb, ok := a.findListingKnowledgeBase(root, workspaceID, rel)
	if !ok {
		return entries
	}
	views, _, err := loadKnowledgeViewSet(kb.realRoot)
	if err != nil {
		logger.WarnCF("rest", "library listing: saved views could not be loaded; listing without view rows",
			map[string]any{"workspace_id": workspaceID, "knowledge_base": kb.rel, "error": err.Error()})
		return entries
	}
	if views.Len() == 0 {
		return entries
	}
	collectionID := knowledgeCollectionID(kb.realRoot)
	storageRel := joinRel(kb.rel, records.VaultMarkerDirName+"/"+records.ViewsDirName)

	if rel == storageRel {
		byFile := make(map[string]*records.SavedView, views.Len())
		for _, v := range views.Views() {
			byFile[storageRel+"/"+filepath.Base(v.SourcePath)] = v
		}
		for i := range entries {
			if v, isLoaded := byFile[entries[i].Path]; isLoaded && !entries[i].IsDir {
				setViewFacts(&entries[i], v, collectionID)
			}
		}
		return entries
	}

	folder := records.ViewPlacementRoot
	switch {
	case rel == kb.rel:
	case kb.rel == "":
		folder = rel
	default:
		folder = strings.TrimPrefix(rel, kb.rel+"/")
	}
	probe := newListingPlacementProbe(root, kb.rel)
	added := false
	for _, v := range views.Views() {
		if records.ViewPlacement(v.DeclaredSource(), probe) != folder {
			continue
		}
		fileRel := storageRel + "/" + filepath.Base(v.SourcePath)
		fi, statErr := root.StatFile(fileRel)
		if statErr != nil {
			// Raced delete, or a view file the confined root refuses (a
			// symlink leaving the Library): no row for it, and a trail.
			logger.WarnCF("rest", "library listing: saved view file not listable; row omitted",
				map[string]any{"workspace_id": workspaceID, "path": fileRel, "error": statErr.Error()})
			continue
		}
		row := library.EntryFromInfo(fileRel, fi)
		if row.IsHidden && !includeHidden {
			continue
		}
		setViewFacts(&row, v, collectionID)
		entries = append(entries, row)
		added = true
	}
	if added {
		library.SortEntries(entries)
	}
	return entries
}

// newListingPlacementProbe answers records.ViewPlacement's folder questions
// through the listing's confined root, memoized so each folder is stat'ed at
// most once per request whatever the number of views. A nested-knowledge-base
// check that cannot complete counts as "is a knowledge base": the view then
// falls back to K's root rather than being placed on an unproven answer.
func newListingPlacementProbe(root *library.Root, kbRel string) records.ViewPlacementProbe {
	dirs := map[string]bool{}
	kbs := map[string]bool{}
	return records.ViewPlacementProbe{
		IsDir: func(rel string) bool {
			if known, seen := dirs[rel]; seen {
				return known
			}
			_, err := root.StatDir(joinRel(kbRel, rel))
			if err != nil && !errors.Is(err, library.ErrNotFound) && !errors.Is(err, library.ErrNotDir) {
				logger.WarnCF("rest", "library listing: view placement folder could not be checked; view listed at the knowledge base root",
					map[string]any{"path": joinRel(kbRel, rel), "error": err.Error()})
			}
			dirs[rel] = err == nil
			return err == nil
		},
		IsKnowledgeBase: func(rel string) bool {
			if known, seen := kbs[rel]; seen {
				return known
			}
			isKB, established := detectKnowledgeBaseInRoot(root, joinRel(kbRel, rel))
			kbs[rel] = isKB || !established
			return kbs[rel]
		},
	}
}
