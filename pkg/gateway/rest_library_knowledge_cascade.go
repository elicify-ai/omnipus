// rest_library_knowledge_cascade.go — the Library's rename / move / delete
// doors, when the entry is a NOTE, an ATTACHMENT or a FOLDER INSIDE A
// KNOWLEDGE BASE (UAT 2026-09-13, #701 / D-123).
//
// THE DEFECT. The agent door (knowledge_restructure) renames a note by
// rewriting every inbound wikilink under a journal, and deletes a note by
// moving it into `.omnipus-vault/trash/` with a receipt so it can be
// restored. The Library door did neither: `root.Rename` was a plain
// filesystem rename that left every `[[link]]` to the note dangling, and
// `root.Delete` unlinked the file for good — the same note, two doors, and
// the human one was the lossy one. Round 3 fixed that for markdown notes;
// round 4 extends it to attachments and folders, which were still lossy.
//
// THE RULE. A Library operation on a markdown note, an attachment (any other
// regular file) or a folder whose innermost enclosing knowledge base
// (enclosingCollectionRel, the same ancestor walk the version-guarded save
// uses — EMB-006a) is known, and — for a rename or a same-workspace move —
// whose destination lies in that SAME knowledge base as the same kind of
// entry, goes through pkg/knowledge's own Renamer / Trasher, followed by the
// same index refresh the agent door performs. A folder carries everything
// under it: renaming or moving it rewrites every link and embed that points
// at anything inside, and deleting it trashes the whole folder recoverably.
//
// Everything else keeps the plain filesystem semantics it always had: a file
// or folder outside any knowledge base, a symbolic link, a mounted folder's
// own entry, a folder that is itself a knowledge base, a rename that changes
// a file's kind (diagram.png -> diagram.md), and a move across knowledge
// bases or workspaces. The enclosing knowledge base does not model those
// shapes, and pretending otherwise would trade one silent behaviour for
// another.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// libraryManagedKind is which kind of entry the knowledge layer manages at a
// Library path.
type libraryManagedKind int

const (
	// libraryManagedNote is a markdown note.
	libraryManagedNote libraryManagedKind = iota
	// libraryManagedAttachment is any other regular file (an image, a PDF).
	libraryManagedAttachment
	// libraryManagedFolder is a directory, with everything under it.
	libraryManagedFolder
)

// libraryCollectionNote is one Library path resolved to the knowledge base
// that governs it.
type libraryCollectionNote struct {
	// collRel is the knowledge base's workspace-relative directory ("" for
	// the work-tree root itself).
	collRel string
	col     *knowledge.Collection
	root    knowledge.CollectionRoot
	// relInCol is the entry's path relative to the knowledge base.
	relInCol string
	lock     knowledge.NoteLockConfig
	// kind records which kind of entry the resolver vetted. A rename's
	// destination must keep it — see sameCollectionDestination.
	kind libraryManagedKind
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
//
// The lock (and the collection-relative path it keys on) comes from
// resolveCollectionNoteLock — the ONE derivation the whole-file save door
// takes too, so the two doors can never hold different locks over the same
// note (round-3 cut list, 2026-09-14 review).
func (a *restAPI) libraryNoteInCollection(root *library.Root, rel string) (note *libraryCollectionNote, governed bool, err error) {
	if !knowledge.IsMarkdownPath(rel) {
		return nil, false, nil
	}
	return a.resolveLibraryCollectionFile(root, rel, libraryManagedNote)
}

// libraryManagedEntryInCollection is libraryNoteInCollection widened to every
// entry the knowledge layer manages: a markdown note, an attachment (anything
// a note can cite with ![[embed]] or a markdown link), or a folder.
// knowledge.Renamer rewrites inbound links to all three, and knowledge.Trasher
// trashes all three recoverably, so the Library's rename / move / delete
// doors route all three (round-4 attachment cascade; fix3/spa-fixes finding 7
// reproduced a 200 attachment rename that left the embed dangling).
//
// A path that does not exist is not governed unless it is spelled as a note,
// which keeps round 3's behaviour: the knowledge layer answers the 404 for a
// missing note, the plain door for anything else.
func (a *restAPI) libraryManagedEntryInCollection(root *library.Root, rel string) (entry *libraryCollectionNote, governed bool, err error) {
	if _, dirErr := root.StatDir(rel); dirErr == nil {
		return a.libraryFolderInCollection(root, rel)
	}
	if knowledge.IsMarkdownPath(rel) {
		return a.libraryNoteInCollection(root, rel)
	}
	fi, statErr := root.StatFile(rel)
	switch {
	case errors.Is(statErr, library.ErrIsDir), errors.Is(statErr, library.ErrNotFound):
		return nil, false, nil
	case statErr != nil:
		return nil, false, statErr
	case !fi.Mode().IsRegular():
		return nil, false, nil
	}
	return a.resolveLibraryCollectionFile(root, rel, libraryManagedAttachment)
}

// libraryFolderInCollection governs a directory inside a knowledge base.
// Three kinds of directory are left to plain filesystem semantics, each
// because the knowledge layer would act on something other than the folder
// the operator sees:
//
//   - a mounted folder's own entry: trashing it would move the operator's
//     real folder into the knowledge base's trash, and the plain door already
//     refuses it with ErrIsMountRoot, which is the right answer;
//   - a symbolic link, or a folder reached through one: the knowledge layer
//     never follows a link (FR-044), so it would refuse a rename that works
//     today;
//   - a folder that is itself a knowledge base: it has its own links, index
//     and trash, which the enclosing knowledge base does not model.
func (a *restAPI) libraryFolderInCollection(root *library.Root, rel string) (*libraryCollectionNote, bool, error) {
	if _, _, _, inMount := root.MountAt(rel); inMount && !strings.Contains(rel, "/") {
		return nil, false, nil
	}
	if isKB, established := detectKnowledgeBaseInRoot(root, rel); established && isKB {
		return nil, false, nil
	}
	entry, governed, err := a.resolveLibraryCollectionFile(root, rel, libraryManagedFolder)
	if err != nil || !governed {
		return entry, governed, err
	}
	if !knowledgeSeesRealFolder(entry.root, entry.relInCol) {
		return nil, false, nil
	}
	return entry, true, nil
}

// knowledgeSeesRealFolder reports whether the knowledge layer would address
// relInCol as an ordinary folder: reachable with no symbolic link anywhere on
// the way (FR-044) and a directory itself. It is a yes/no question on
// purpose. A path the knowledge layer cannot resolve is not an error for the
// Library door, only a folder it must leave to plain filesystem semantics,
// where the plain door answers for it as it always has.
func knowledgeSeesRealFolder(croot knowledge.CollectionRoot, relInCol string) bool {
	abs, err := croot.ResolveContainedNoSymlink(knowledge.OSLinkFS(), relInCol)
	if err != nil {
		return false
	}
	info, err := os.Lstat(abs)
	return err == nil && info.IsDir()
}

// resolveLibraryCollectionFile resolves rel's innermost knowledge base and
// lock. kind records which kind of managed entry the caller vetted.
//
// An entry that is, or lies inside, a tool-state directory of that knowledge
// base (`.omnipus-vault`, `.obsidian`, `.git`, `.trash` — the set
// knowledge.IsReservedLocation answers for) is NOT governed, whatever its
// kind. The knowledge layer does not model those directories: every walker
// skips them, nothing in them is indexed or linked, and Renamer / Trasher
// refuse them by name. Routing one there could only ever turn a Library
// action that used to work into a 400. UAT re-test U-58 (2026-09-14): round 4
// routed folders here, and deleting a knowledge base's own `.omnipus-vault`
// from the Library stopped working. A tool-state entry keeps the plain
// filesystem semantics it had before round 4 (and, for a note-shaped file
// inside one, before round 3).
func (a *restAPI) resolveLibraryCollectionFile(root *library.Root, rel string, kind libraryManagedKind) (*libraryCollectionNote, bool, error) {
	collRel, col, lock, relInCol, err := resolveCollectionNoteLock(root, a.homePath, rel)
	if err != nil {
		return nil, false, err
	}
	if col == nil || knowledge.IsReservedLocation(relInCol) {
		return nil, false, nil
	}
	croot, err := knowledge.NewCollectionRoot(knowledge.OSLinkFS(), col.Root())
	if err != nil {
		return nil, false, fmt.Errorf("resolve knowledge base root for %q: %w", collRel, err)
	}
	return &libraryCollectionNote{
		collRel:  collRel,
		col:      col,
		root:     croot,
		relInCol: relInCol,
		lock:     lock,
		kind:     kind,
	}, true, nil
}

// releaseKnowledgeBaseIfDemoted runs after a PLAIN Library delete, rename or
// move of fromRel has landed. When fromRel was a marker folder
// (`.omnipus-vault` or `.obsidian`), its parent may have just stopped being a
// knowledge base, and the knowledge lifecycle must stop indexing it
// (ReleaseDemotedCollection decides, from the folder itself, whether it did).
// UAT re-test U-58: "records stop indexing".
func (a *restAPI) releaseKnowledgeBaseIfDemoted(root *library.Root, fromRel string) {
	parent, base := "", fromRel
	if i := strings.LastIndexByte(fromRel, '/'); i >= 0 {
		parent, base = fromRel[:i], fromRel[i+1:]
	}
	if base != knowledge.MarkerDirName && base != knowledge.ObsidianMarkerDirName {
		return
	}
	a.knowledgeLifecycle().ReleaseDemotedCollection(root.HostPath(parent))
}

// sameCollectionDestination reports whether toRel (workspace-relative) lands
// inside the SAME knowledge base as entry, as the same kind of entry: a
// markdown note stays a markdown note, an attachment stays a non-markdown
// file. A rename that changes kind (diagram.png -> diagram.md) is not a
// rename the link graph models, so it keeps plain filesystem semantics. A
// folder has no kind to change, so any name will do.
func sameCollectionDestination(root *library.Root, entry *libraryCollectionNote, toRel string) bool {
	switch entry.kind {
	case libraryManagedNote:
		if !knowledge.IsMarkdownPath(toRel) {
			return false
		}
	case libraryManagedAttachment:
		if knowledge.IsMarkdownPath(toRel) {
			return false
		}
	case libraryManagedFolder:
	}
	toCollRel, found := enclosingCollectionRel(root, toRel)
	return found && toCollRel == entry.collRel
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
// a note, an attachment or a folder inside a knowledge base through
// knowledge.Renamer, so every inbound link is rewritten under a journal, then
// refreshes both indexes. It writes the HTTP response itself and returns
// false when it has already answered with an error.
func (a *restAPI) renameNoteInCollection(
	w http.ResponseWriter, r *http.Request, op, workspaceID string,
	root *library.Root, note *libraryCollectionNote, fromRel, toRel string,
) bool {
	isFolder := note.kind == libraryManagedFolder
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
		From: note.relInCol, To: relWithinCollection(note.collRel, toRel), Folder: isFolder,
	})
	if err != nil {
		mapKnowledgeRestructureErr(w, op, workspaceID, err)
		return false
	}
	if !res.NoOp {
		var warning string
		if isFolder {
			warning = knowledge.RefreshIndexesForFolderRename(r.Context(), a.homePath, note.col.Root(), res)
		} else {
			warning = knowledge.RefreshIndexesForRename(r.Context(), a.homePath, note.col.Root(), res.From, res.Touched)
		}
		if warning != "" {
			logger.WarnCF("rest", "library: "+op+" landed but an index could not be refreshed",
				map[string]any{"workspace_id": workspaceID, "path": toRel, "warning": warning})
		}
	}
	a.revokePreviewTokensForPath(workspaceID, fromRel)
	a.logLibraryAudit(r, "library."+op, workspaceID, map[string]any{
		"from": fromRel, "to": toRel, "knowledge_base": note.collRel, "folder": isFolder,
		"files_moved": len(res.Moves), "links_rewritten": res.LinksRewritten,
		"files_rewritten": res.FilesRewritten, "journal_id": res.JournalID,
	})
	// D-107: the entry moved under a new name — same listing-staleness reason
	// as the plain rename path.
	a.emitLibraryChange(workspaceID, toRel, op)
	var fi os.FileInfo
	var statErr error
	if isFolder {
		fi, statErr = root.StatDir(toRel)
	} else {
		fi, statErr = root.StatFile(toRel)
	}
	if statErr != nil {
		mapLibraryErr(w, op, workspaceID, statErr)
		return false
	}
	jsonOK(w, library.EntryFromInfo(toRel, fi))
	return true
}

// trashNoteInCollection performs a Library delete of a note, an attachment or
// a folder inside a knowledge base through knowledge.Trasher, so it lands in
// `.omnipus-vault/trash/` with a receipt it can be restored from, then drops
// its live entries from both indexes. It writes the HTTP response itself.
func (a *restAPI) trashNoteInCollection(
	w http.ResponseWriter, r *http.Request, workspaceID string,
	note *libraryCollectionNote, rel string,
) {
	isFolder := note.kind == libraryManagedFolder
	trasher := &knowledge.Trasher{
		FS: knowledge.OSLinkFS(), Root: note.root, Lock: note.lock,
		Audit: func(ev knowledge.TrashAuditEvent) {
			a.logLibraryAudit(r, "library.delete.knowledge", workspaceID, map[string]any{
				"path": rel, "outcome": ev.Outcome, "reason": ev.Reason, "paths": ev.Paths,
			})
		},
	}
	res, err := trasher.Trash(knowledge.TrashRequest{Path: note.relInCol, Folder: isFolder})
	if err != nil {
		mapKnowledgeRestructureErr(w, "delete entry", workspaceID, err)
		return
	}
	ctx := context.WithoutCancel(r.Context())
	var warning string
	if isFolder {
		warning = knowledge.RemoveFromIndexesForFolderTrash(ctx, a.homePath, note.col.Root(), res)
	} else {
		warning = knowledge.RemoveFromIndexesForNote(ctx, a.homePath, note.col.Root(), res.OriginalPath)
	}
	if warning != "" {
		logger.WarnCF("rest", "library: delete landed but an index could not be refreshed",
			map[string]any{"workspace_id": workspaceID, "path": rel, "warning": warning})
	}
	a.revokePreviewTokensForPath(workspaceID, rel)
	a.logLibraryAudit(r, "library.delete", workspaceID, map[string]any{
		"path": rel, "knowledge_base": note.collRel, "folder": isFolder, "files_trashed": len(res.Members),
		"trash_id": res.TrashID, "trash_path": res.TrashPath, "dangling_link_count": res.DanglingLinkCount,
	})
	// D-107: the entry left the listing for the trash — other tabs' rows for
	// it now point at something that no longer exists at that path.
	a.emitLibraryChange(workspaceID, rel, "delete")
	w.WriteHeader(http.StatusNoContent)
}
