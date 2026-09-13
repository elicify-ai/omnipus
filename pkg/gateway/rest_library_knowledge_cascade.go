// rest_library_knowledge_cascade.go — the Library's rename / move / delete
// doors, when the entry is a NOTE INSIDE A KNOWLEDGE BASE (UAT 2026-09-13,
// #701 / D-123).
//
// THE DEFECT. The agent door (knowledge_restructure) renames a note by
// rewriting every inbound wikilink under a journal, and deletes a note by
// moving it into `.omnipus-vault/trash/` with a receipt so it can be
// restored. The Library door did neither: `root.Rename` was a plain
// filesystem rename that left every `[[link]]` to the note dangling, and
// `root.Delete` unlinked the file for good — the same note, two doors, and
// the human one was the lossy one.
//
// THE RULE. A Library operation on a MARKDOWN NOTE whose innermost enclosing
// knowledge base (enclosingCollectionRel, the same ancestor walk the
// version-guarded save uses — EMB-006a) is known, and — for a rename or a
// same-workspace move — whose destination lies in that SAME knowledge base,
// goes through pkg/knowledge's own Renamer / Trasher, followed by the same
// index refresh the agent door performs. Everything else (a directory, an
// attachment, a file outside any knowledge base, a move across knowledge
// bases or workspaces) keeps the plain filesystem semantics it always had:
// those shapes the knowledge layer does not model, and pretending otherwise
// would trade one silent behaviour for another.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// libraryCollectionNote is one Library path resolved to the knowledge base
// that governs it.
type libraryCollectionNote struct {
	// collRel is the knowledge base's workspace-relative directory ("" for
	// the work-tree root itself).
	collRel string
	col     *knowledge.Collection
	root    knowledge.CollectionRoot
	// relInCol is the note's path relative to the knowledge base.
	relInCol string
	lock     knowledge.NoteLockConfig
}

// relWithinCollection strips the knowledge base's directory off a
// workspace-relative path.
func relWithinCollection(collRel, rel string) string {
	if collRel == "" {
		return rel
	}
	return strings.TrimPrefix(rel, collRel+"/")
}

// libraryNoteInCollection answers "which knowledge base governs this note?"
// for a workspace-relative path. `governed` is false when the path is not a
// markdown note inside one (a definite answer, with no note to return). An
// error means the answer could not be established — the caller must not
// fall back to plain filesystem semantics on it, because "could not tell"
// is not "not a knowledge base".
func (a *restAPI) libraryNoteInCollection(root *library.Root, rel string) (note *libraryCollectionNote, governed bool, err error) {
	if !knowledge.IsMarkdownPath(rel) {
		return nil, false, nil
	}
	collRel, found := enclosingCollectionRel(root, rel)
	if !found {
		return nil, false, nil
	}
	col, err := knowledge.OpenCollection(root.HostPath(collRel))
	if err != nil {
		return nil, false, fmt.Errorf("open enclosing knowledge base %q: %w", collRel, err)
	}
	lockDir, err := knowledge.LockDirFor(a.homePath, col.Root())
	if err != nil {
		return nil, false, fmt.Errorf("resolve write lock directory for %q: %w", collRel, err)
	}
	croot, err := knowledge.NewCollectionRoot(knowledge.OSLinkFS(), col.Root())
	if err != nil {
		return nil, false, fmt.Errorf("resolve knowledge base root for %q: %w", collRel, err)
	}
	return &libraryCollectionNote{
		collRel:  collRel,
		col:      col,
		root:     croot,
		relInCol: relWithinCollection(collRel, rel),
		lock:     knowledge.NoteLockConfig{CollectionRoot: col.Root(), LockDir: lockDir},
	}, true, nil
}

// sameCollectionDestination reports whether toRel (workspace-relative) lands
// inside the SAME knowledge base as note, as a markdown note.
func sameCollectionDestination(root *library.Root, note *libraryCollectionNote, toRel string) bool {
	if !knowledge.IsMarkdownPath(toRel) {
		return false
	}
	toCollRel, found := enclosingCollectionRel(root, toRel)
	return found && toCollRel == note.collRel
}

// mapKnowledgeRestructureErr writes the HTTP answer for a Renamer / Trasher
// failure, mirroring mapLibraryErr's vocabulary so the SPA's existing
// handling (404 / 409 / 400 / 503) keeps working for the knowledge-routed
// case.
func mapKnowledgeRestructureErr(w http.ResponseWriter, op, workspaceID string, err error) {
	var ambiguity *knowledge.AmbiguityError
	var timeout *knowledge.LockTimeoutError
	switch {
	case errors.Is(err, knowledge.ErrRenameSourceMissing),
		errors.Is(err, knowledge.ErrTrashSourceMissing),
		errors.Is(err, knowledge.ErrNoteNotFound):
		jsonErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, knowledge.ErrRenameDestinationExists),
		errors.Is(err, knowledge.ErrNoteExists):
		jsonErr(w, http.StatusConflict, "an entry already exists at the destination path")
	case errors.Is(err, knowledge.ErrRenameDestinationParentMissing):
		jsonErr(w, http.StatusNotFound,
			"destination directory does not exist — create it first with POST /library/{workspace_id}/mkdir")
	case errors.As(err, &ambiguity):
		jsonErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, knowledge.ErrRenameInvalidPath),
		errors.Is(err, knowledge.ErrRenameSourceNotAddressable),
		errors.Is(err, knowledge.ErrRenameDestinationNotAddressable),
		errors.Is(err, knowledge.ErrReservedLocation):
		jsonErr(w, http.StatusBadRequest, err.Error())
	case errors.As(err, &timeout):
		jsonErr(w, http.StatusServiceUnavailable,
			"timed out waiting for this note's write lock — another write is in progress; try again")
	default:
		logger.ErrorCF("rest", "library: "+op+" (knowledge base) failed",
			map[string]any{"workspace_id": workspaceID, "error": err.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
	}
}

// renameNoteInCollection performs a Library rename / same-workspace move of
// a note inside a knowledge base through knowledge.Renamer, so every inbound
// link is rewritten under a journal, then refreshes both indexes. It writes
// the HTTP response itself and returns false when it has already answered
// with an error.
func (a *restAPI) renameNoteInCollection(
	w http.ResponseWriter, r *http.Request, op, workspaceID string,
	root *library.Root, note *libraryCollectionNote, fromRel, toRel string,
) bool {
	renamer := &knowledge.Renamer{
		FS: knowledge.OSLinkFS(), Root: note.root, Lock: note.lock,
		Audit: func(ev knowledge.RenameAuditEvent) {
			a.logLibraryAudit(r, "library."+op+".knowledge", workspaceID, map[string]any{
				"from": fromRel, "to": toRel, "outcome": ev.Outcome, "reason": ev.Reason,
				"journal_id": ev.JournalID, "paths": ev.Paths,
			})
		},
	}
	res, err := renamer.Rename(knowledge.RenameRequest{
		From: note.relInCol, To: relWithinCollection(note.collRel, toRel),
	})
	if err != nil {
		mapKnowledgeRestructureErr(w, op, workspaceID, err)
		return false
	}
	if !res.NoOp {
		if warning := knowledge.RefreshIndexesForRename(r.Context(), a.homePath, note.col.Root(), res.From, res.Touched); warning != "" {
			logger.WarnCF("rest", "library: "+op+" landed but an index could not be refreshed",
				map[string]any{"workspace_id": workspaceID, "path": toRel, "warning": warning})
		}
	}
	a.revokePreviewTokensForPath(workspaceID, fromRel)
	a.logLibraryAudit(r, "library."+op, workspaceID, map[string]any{
		"from": fromRel, "to": toRel, "knowledge_base": note.collRel,
		"links_rewritten": res.LinksRewritten, "files_rewritten": res.FilesRewritten,
		"journal_id": res.JournalID,
	})
	// D-107: the note moved under a new name — same listing-staleness reason
	// as the plain rename path.
	a.emitLibraryChange(workspaceID, toRel, op)
	fi, statErr := root.StatFile(toRel)
	if statErr != nil {
		mapLibraryErr(w, op, workspaceID, statErr)
		return false
	}
	jsonOK(w, library.EntryFromInfo(toRel, fi))
	return true
}

// trashNoteInCollection performs a Library delete of a note inside a
// knowledge base through knowledge.Trasher, so the note lands in
// `.omnipus-vault/trash/` with a receipt an agent can restore from, then
// drops its live entry from both indexes. It writes the HTTP response itself.
func (a *restAPI) trashNoteInCollection(
	w http.ResponseWriter, r *http.Request, workspaceID string,
	note *libraryCollectionNote, rel string,
) {
	trasher := &knowledge.Trasher{
		FS: knowledge.OSLinkFS(), Root: note.root, Lock: note.lock,
		Audit: func(ev knowledge.TrashAuditEvent) {
			a.logLibraryAudit(r, "library.delete.knowledge", workspaceID, map[string]any{
				"path": rel, "outcome": ev.Outcome, "reason": ev.Reason, "paths": ev.Paths,
			})
		},
	}
	res, err := trasher.Trash(knowledge.TrashRequest{Path: note.relInCol})
	if err != nil {
		mapKnowledgeRestructureErr(w, "delete entry", workspaceID, err)
		return
	}
	if warning := knowledge.RemoveFromIndexesForNote(context.WithoutCancel(r.Context()), a.homePath, note.col.Root(), res.OriginalPath); warning != "" {
		logger.WarnCF("rest", "library: delete landed but an index could not be refreshed",
			map[string]any{"workspace_id": workspaceID, "path": rel, "warning": warning})
	}
	a.revokePreviewTokensForPath(workspaceID, rel)
	a.logLibraryAudit(r, "library.delete", workspaceID, map[string]any{
		"path": rel, "knowledge_base": note.collRel, "trash_id": res.TrashID,
		"trash_path": res.TrashPath, "dangling_link_count": res.DanglingLinkCount,
	})
	// D-107: the note left the listing for the trash — other tabs' rows for
	// it now point at a file that no longer exists at that path.
	a.emitLibraryChange(workspaceID, rel, "delete")
	w.WriteHeader(http.StatusNoContent)
}
