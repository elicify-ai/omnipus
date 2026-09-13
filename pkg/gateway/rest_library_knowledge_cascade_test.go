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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
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
