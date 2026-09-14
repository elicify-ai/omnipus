// Regression tests for UAT #701 / D-123 (2026-09-13): the Library's rename,
// move and delete doors route a note INSIDE a knowledge base through the
// knowledge layer — inbound links are rewritten on rename/move, a delete
// lands in the knowledge base's trash — while everything outside a
// knowledge base keeps its plain filesystem semantics.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/library"
)

// buildLinkedVault seeds `vault/` with a person note and a project note that
// links to it two ways (a qualified alias link and a bare basename link),
// mirroring the UAT fixture B-29 renamed through the agent door.
func buildLinkedVault(t *testing.T) (*restAPI, string, string) {
	t.Helper()
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Cascade vault")
	require.NoError(t, os.MkdirAll(filepath.Join(vault, "People"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(vault, "Projects"), 0o755))
	writeNote(t, vault, "People/Tobias Brandt.md", "---\ntitle: Tobias Brandt\n---\n# Tobias\n")
	writeNote(t, vault, "Projects/Atlas.md",
		"---\ntitle: Atlas\n---\nLead: [[People/Tobias Brandt.md|lead]] and again [[Tobias Brandt]].\n")
	return api, ws, vault
}

func TestLibraryRename_NoteInVaultRewritesInboundLinks(t *testing.T) {
	api, ws, vault := buildLinkedVault(t)

	w := libPostJSON(t, api, "/api/v1/library/"+ws+"/rename",
		`{"from":"vault/People/Tobias Brandt.md","to":"vault/People/Tobias Brandt-Larsen.md"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	_, err := os.Stat(filepath.Join(vault, "People", "Tobias Brandt-Larsen.md"))
	require.NoError(t, err, "the note must exist at its new name")
	_, err = os.Stat(filepath.Join(vault, "People", "Tobias Brandt.md"))
	require.True(t, os.IsNotExist(err), "the old name must be gone")

	project, err := os.ReadFile(filepath.Join(vault, "Projects", "Atlas.md"))
	require.NoError(t, err)
	require.Contains(t, string(project), "Tobias Brandt-Larsen",
		"D-123: the inbound wikilink must be rewritten to the new name")
	require.NotContains(t, string(project), "[[People/Tobias Brandt.md|lead]]",
		"D-123: the qualified alias link must not be left dangling")
	require.Contains(t, string(project), "|lead]]", "the alias must survive the rewrite")
}

func TestLibraryMove_NoteWithinVaultRewritesInboundLinks(t *testing.T) {
	api, ws, vault := buildLinkedVault(t)
	require.NoError(t, os.MkdirAll(filepath.Join(vault, "Team"), 0o755))

	w := libPostJSON(t, api, "/api/v1/library/move",
		`{"from_workspace_id":"`+ws+`","from_path":"vault/People/Tobias Brandt.md",`+
			`"to_workspace_id":"`+ws+`","to_path":"vault/Team/Tobias Brandt.md"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	project, err := os.ReadFile(filepath.Join(vault, "Projects", "Atlas.md"))
	require.NoError(t, err)
	require.Contains(t, string(project), "Team/Tobias Brandt",
		"#701: a move inside the same knowledge base must rewrite inbound links")
	require.NotContains(t, string(project), "[[People/Tobias Brandt.md|lead]]")
}

func TestLibraryDelete_NoteInVaultGoesToTrash(t *testing.T) {
	api, ws, vault := buildLinkedVault(t)

	w := libDelete(t, api, "/api/v1/library/"+ws+"/entries?path=vault/People/Tobias%20Brandt.md")
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

	_, err := os.Stat(filepath.Join(vault, "People", "Tobias Brandt.md"))
	require.True(t, os.IsNotExist(err), "the live note must be gone from the collection")

	trashDir := filepath.Join(vault, ".omnipus-vault", "trash")
	entries, err := os.ReadDir(trashDir)
	require.NoError(t, err, "D-123: a Library delete inside a knowledge base must reach .omnipus-vault/trash/")
	require.NotEmpty(t, entries)
	var trashed []string
	require.NoError(t, filepath.WalkDir(trashDir, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if !d.IsDir() && filepath.Base(p) == "Tobias Brandt.md" {
			trashed = append(trashed, p)
		}
		return nil
	}))
	require.Len(t, trashed, 1, "exactly one trashed copy of the note")
}

func TestLibraryDelete_FileOutsideVaultStaysPlain(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	dir := workDir(api, ws)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "loose.md"), []byte("# loose\n"), 0o644))

	w := libDelete(t, api, "/api/v1/library/"+ws+"/entries?path=loose.md")
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	_, err := os.Stat(filepath.Join(dir, "loose.md"))
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(dir, ".omnipus-vault"))
	require.True(t, os.IsNotExist(err), "no knowledge base, no trash directory conjured")
}

func TestLibraryRename_NoteInVaultOntoExistingIs409(t *testing.T) {
	api, ws, _ := buildLinkedVault(t)
	w := libPostJSON(t, api, "/api/v1/library/"+ws+"/rename",
		`{"from":"vault/People/Tobias Brandt.md","to":"vault/Projects/Atlas.md"}`)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
}

// --- Attachments and folders (round-4 attachment cascade) -------------------
//
// Deferred from fix3/spa-fixes finding 7: the round-3 agent reproduced a
// Library rename of `vault/assets/diagram.png` answering 200, moving the file,
// and leaving `![[assets/diagram.png]]` in the note that embeds it dangling,
// because the cascade gate only ever recognised markdown notes.

// buildAttachmentVault extends the linked vault with an attachment embedded
// by Projects/Atlas.md.
func buildAttachmentVault(t *testing.T) (*restAPI, string, string) {
	t.Helper()
	api, ws, vault := buildLinkedVault(t)
	require.NoError(t, os.MkdirAll(filepath.Join(vault, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(vault, "assets", "diagram.png"),
		[]byte("\x89PNG\r\n\x1a\nbinary"), 0o644))
	writeNote(t, vault, "Projects/Roadmap.md", "---\ntitle: Roadmap\n---\nDiagram: ![[assets/diagram.png]]\n")
	return api, ws, vault
}

func TestLibraryRename_AttachmentInVaultRewritesEmbed(t *testing.T) {
	api, ws, vault := buildAttachmentVault(t)

	w := libPostJSON(t, api, "/api/v1/library/"+ws+"/rename",
		`{"from":"vault/assets/diagram.png","to":"vault/assets/diagram-v2.png"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	_, err := os.Stat(filepath.Join(vault, "assets", "diagram-v2.png"))
	require.NoError(t, err, "the attachment must exist at its new name")
	_, err = os.Stat(filepath.Join(vault, "assets", "diagram.png"))
	require.True(t, os.IsNotExist(err), "the old name must be gone")

	roadmap, err := os.ReadFile(filepath.Join(vault, "Projects", "Roadmap.md"))
	require.NoError(t, err)
	require.Contains(t, string(roadmap), "![[assets/diagram-v2.png]]",
		"renaming an attachment inside a knowledge base must rewrite the embed that cites it")
	require.NotContains(t, string(roadmap), "![[assets/diagram.png]]")
}

func TestLibraryMove_AttachmentWithinVaultRewritesEmbed(t *testing.T) {
	api, ws, vault := buildAttachmentVault(t)
	require.NoError(t, os.MkdirAll(filepath.Join(vault, "media"), 0o755))

	w := libPostJSON(t, api, "/api/v1/library/move",
		`{"from_workspace_id":"`+ws+`","from_path":"vault/assets/diagram.png",`+
			`"to_workspace_id":"`+ws+`","to_path":"vault/media/diagram.png"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	_, err := os.Stat(filepath.Join(vault, "media", "diagram.png"))
	require.NoError(t, err)
	roadmap, err := os.ReadFile(filepath.Join(vault, "Projects", "Roadmap.md"))
	require.NoError(t, err)
	require.Contains(t, string(roadmap), "![[media/diagram.png]]",
		"moving an attachment inside the same knowledge base must rewrite the embed that cites it")
	require.NotContains(t, string(roadmap), "![[assets/diagram.png]]")
}

// The door itself: a Library DELETE of an attachment inside a knowledge base
// must land in that knowledge base's trash with its bytes intact, not be
// unlinked for good. Driven through HandleLibrary so it fails if the delete
// handler keeps asking the markdown-only resolver.
func TestLibraryDelete_AttachmentInVaultGoesToTrash(t *testing.T) {
	api, ws, vault := buildAttachmentVault(t)

	w := libDelete(t, api, "/api/v1/library/"+ws+"/entries?path=vault/assets/diagram.png")
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

	_, err := os.Stat(filepath.Join(vault, "assets", "diagram.png"))
	require.True(t, os.IsNotExist(err), "the live attachment must be gone")
	trashed, err := filepath.Glob(filepath.Join(vault, ".omnipus-vault", "trash", "*", "assets", "diagram.png"))
	require.NoError(t, err)
	require.Len(t, trashed, 1, "a Library delete of an attachment must be recoverable from the knowledge base's trash")
	got, err := os.ReadFile(trashed[0])
	require.NoError(t, err)
	require.Equal(t, "\x89PNG\r\n\x1a\nbinary", string(got), "the trashed attachment's bytes are untouched")
}

// The cascade half on its own: trashNoteInCollection trashes an attachment
// recoverably when handed what the managed-file resolver returns (the
// Trasher itself is pinned for restore in pkg/knowledge).
func TestLibraryCascade_AttachmentTrashIsRecoverable(t *testing.T) {
	api, ws, vault := buildAttachmentVault(t)
	root, err := library.OpenRoot(api.homePath, ws)
	require.NoError(t, err)
	defer root.Close()

	file, governed, err := api.libraryManagedFileInCollection(root, "vault/assets/diagram.png")
	require.NoError(t, err)
	require.True(t, governed, "an attachment inside a knowledge base is governed by it")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/library/"+ws+"/entries?path=vault/assets/diagram.png", nil)
	api.trashNoteInCollection(w, r, ws, file, "vault/assets/diagram.png")
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

	_, err = os.Stat(filepath.Join(vault, "assets", "diagram.png"))
	require.True(t, os.IsNotExist(err), "the live attachment must be gone")

	tr := &knowledge.Trasher{Root: file.root, Lock: file.lock}
	restored, err := tr.Restore(knowledge.RestoreRequest{Path: "assets/diagram.png"})
	require.NoError(t, err, "the trashed attachment must restore")
	require.Equal(t, "assets/diagram.png", restored.OriginalPath)
	_, err = os.Stat(filepath.Join(vault, "assets", "diagram.png"))
	require.NoError(t, err, "the attachment is back at its original path")
}

// A folder is not modelled by the knowledge layer: knowledge.Renamer refuses
// a directory source (ErrRenameSourceNotAddressable) and there is no
// directory-aware Trasher. Routing a folder there would turn a working
// rename into a 400, so a folder keeps plain filesystem semantics — the
// managed-file resolver must say "not governed" for it.
func TestLibraryRename_FolderInVaultKeepsPlainSemantics(t *testing.T) {
	api, ws, vault := buildAttachmentVault(t)

	root, err := library.OpenRoot(api.homePath, ws)
	require.NoError(t, err)
	_, governed, err := api.libraryManagedFileInCollection(root, "vault/assets")
	root.Close()
	require.NoError(t, err)
	require.False(t, governed, "a directory is never governed by the knowledge cascade")

	w := libPostJSON(t, api, "/api/v1/library/"+ws+"/rename",
		`{"from":"vault/assets","to":"vault/images"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	_, err = os.Stat(filepath.Join(vault, "images", "diagram.png"))
	require.NoError(t, err, "the folder and its contents moved")
}
