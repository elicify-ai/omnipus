// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// library_chat_copy.go: one explicit Library deletion removes both copies of
// a chat file (#1449 D26, FR-086). The association is the media entry's
// durable library_path, resolved only inside the same workspace. Order:
// chat copy first, Library file last, so a failure leaves the file listed
// and a repeat completes the work; a chat copy that is already gone counts
// as removed. Retention owners never block an explicit deletion.

package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	wkspace "github.com/elicify-ai/omnipus/pkg/workspace"
)

// chatCopyLibrary is the workspace's media library, or nil when it has none
// (then no file has an association).
func (a *restAPI) chatCopyLibrary(workspaceID string) *library.Library {
	if a.agentLoop == nil {
		return nil
	}
	return a.agentLoop.GetWorkspaceLibrary(workspaceID)
}

// deleteChatCopies removes the chat copy of every file at or under rel. It
// answers 500 and returns false when one could not be removed (the Library
// file is then still present). removed reports whether any copy was removed.
func (a *restAPI) deleteChatCopies(w http.ResponseWriter, workspaceID, rel string) (removed, ok bool) {
	lib := a.chatCopyLibrary(workspaceID)
	if lib == nil {
		return false, true
	}
	for _, id := range lib.IDsForLibraryPath(rel, true) {
		if _, err := lib.Delete(id); err != nil && !errors.Is(err, library.ErrNotFound) {
			logger.ErrorCF("rest", "library delete: the chat copy could not be removed; the Library file is kept",
				map[string]any{"workspace_id": workspaceID, "path": rel, "media_id": id, "error": err.Error()})
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not delete %s completely: try again", path.Base(rel)))
			return removed, false
		}
		removed = true
	}
	return removed, true
}

// followChatCopies keeps the association with a renamed or moved file (D26).
// A move to another workspace drops it (to == ""). A failure is logged: the
// file operation already happened, and the association is only metadata.
func (a *restAPI) followChatCopies(workspaceID, from, to string) {
	lib := a.chatCopyLibrary(workspaceID)
	if lib == nil {
		return
	}
	if err := lib.MoveLibraryPath(from, to); err != nil {
		logger.ErrorCF("rest", "library: a chat copy association could not follow its file",
			map[string]any{"workspace_id": workspaceID, "from": from, "to": to, "error": err.Error()})
	}
}

// markChatCopies sets LibraryEntry.chat_copy on listed files that have an
// associated chat copy (FR-087).
func (a *restAPI) markChatCopies(workspaceID string, entries []gen.LibraryEntry) {
	lib := a.chatCopyLibrary(workspaceID)
	if lib == nil {
		return
	}
	for i := range entries {
		if entries[i].IsDir {
			continue
		}
		if len(lib.IDsForLibraryPath(entries[i].Path, false)) > 0 {
			yes := true
			entries[i].ChatCopy = &yes
		}
	}
}

// duplicateSuffix matches the " (N)" a dedupe adds before the extension.
var duplicateSuffix = regexp.MustCompile(` \(\d+\)$`)

func baseNameWithoutDuplicate(name string) string {
	ext := filepath.Ext(name)
	return duplicateSuffix.ReplaceAllString(strings.TrimSuffix(name, ext), "") + ext
}

// repairLibraryAssociations is the one-time repair for uploads made before
// #1449 (A-14): a .library file is associated with the media entry of the
// same workspace that has the same SHA-256 and the same file name (ignoring
// a " (N)" suffix), only when exactly one entry matches. Never across
// workspaces. A failure leaves the repair to the next start.
func repairLibraryAssociations(home, workspaceID string, lib *library.Library) {
	if lib == nil || lib.LibraryPathsRepaired() {
		return
	}
	workDir, err := wkspace.SafeWorkDir(home, workspaceID)
	if err != nil {
		return
	}
	dir := filepath.Join(workDir, agent.LibraryDirName())
	files, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.WarnCF("rest", "library association repair: the .library folder could not be read",
			map[string]any{"workspace_id": workspaceID, "error": err.Error()})
		return
	}
	entries := lib.UnassociatedEntries()
	matches := map[string][]string{} // media id -> rel paths
	fileHits := map[string][]string{}
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, f.Name()))
		if readErr != nil {
			continue
		}
		sum := shaHex(data)
		rel := agent.LibraryDirName() + "/" + f.Name()
		for _, e := range entries {
			if e.Sha256 != nil && *e.Sha256 == sum && baseNameWithoutDuplicate(f.Name()) == baseNameWithoutDuplicate(e.Filename) {
				id := e.Id.String()
				matches[id] = append(matches[id], rel)
				fileHits[rel] = append(fileHits[rel], id)
			}
		}
	}
	paths := map[string]string{}
	for id, rels := range matches {
		if len(rels) == 1 && len(fileHits[rels[0]]) == 1 {
			paths[id] = rels[0]
		}
	}
	if err := lib.CompleteLibraryPathRepair(paths); err != nil {
		logger.WarnCF("rest", "library association repair could not be saved; it runs again on the next start",
			map[string]any{"workspace_id": workspaceID, "error": err.Error()})
	}
}
