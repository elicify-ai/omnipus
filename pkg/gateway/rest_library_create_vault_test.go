// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for POST /api/v1/library/{id}/vaults (feature C2 — create-vault),
// covering handleLibraryCreateVault in rest_library.go. That handler existed
// with zero test coverage before this file.

func TestLibraryCreateVault_Created_MarkerAndDirExist(t *testing.T) {
	api, id := buildLibraryTestAPI(t)

	w := libPostJSON(t, api, "/api/v1/library/"+id+"/vaults", `{"name":"Research"}`)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())

	entry := decodeEntry(t, w.Body.Bytes())
	assert.Equal(t, "Research", entry.Path)
	assert.True(t, entry.IsDir)

	vaultRoot := filepath.Join(workDir(api, id), "Research")
	isKB, err := knowledge.IsKnowledgeBase(vaultRoot)
	require.NoError(t, err)
	assert.True(t, isKB, "the created directory must be detected as an Omnipus knowledge base")

	markerDir := filepath.Join(vaultRoot, knowledge.MarkerDirName)
	assert.DirExists(t, markerDir, "the %s marker directory must exist", knowledge.MarkerDirName)
}

func TestLibraryCreateVault_NestedParent_Created(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusCreated,
		libPostJSON(t, api, "/api/v1/library/"+id+"/mkdir", `{"path":"projects"}`).Code)

	w := libPostJSON(t, api, "/api/v1/library/"+id+"/vaults", `{"name":"Notes","parent_rel_path":"projects"}`)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())

	entry := decodeEntry(t, w.Body.Bytes())
	assert.Equal(t, "projects/Notes", entry.Path)

	vaultRoot := filepath.Join(workDir(api, id), "projects", "Notes")
	isKB, err := knowledge.IsKnowledgeBase(vaultRoot)
	require.NoError(t, err)
	assert.True(t, isKB)
}

func TestLibraryCreateVault_DuplicateName_409(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusCreated,
		libPostJSON(t, api, "/api/v1/library/"+id+"/vaults", `{"name":"Research"}`).Code)

	w := libPostJSON(t, api, "/api/v1/library/"+id+"/vaults", `{"name":"Research"}`)
	assert.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
}

// TestLibraryCreateVault_AlreadyExistsAsPlainFolder_409 is the sibling case
// to the duplicate-vault check above: unlike mkdir (idempotent), this
// endpoint must refuse to adopt an existing plain directory into a vault.
func TestLibraryCreateVault_AlreadyExistsAsPlainFolder_409(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusCreated,
		libPostJSON(t, api, "/api/v1/library/"+id+"/mkdir", `{"path":"Research"}`).Code)

	w := libPostJSON(t, api, "/api/v1/library/"+id+"/vaults", `{"name":"Research"}`)
	assert.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
}

func TestLibraryCreateVault_AlreadyExistsAsFile_409(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"Research","content":"x"}`).Code)

	w := libPostJSON(t, api, "/api/v1/library/"+id+"/vaults", `{"name":"Research"}`)
	assert.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
}

// TestLibraryCreateVault_NameWithSeparator_400 is the "reject a name with a
// path separator" case: a vault NAME is a single path segment (like mkdir's
// leaf, but this endpoint has its own explicit check ahead of
// library.CleanRelPath), so "/" or "\\" anywhere in it must 400 rather than
// be silently treated as a nested path.
func TestLibraryCreateVault_NameWithSeparator_400(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	for _, name := range []string{"a/b", "a\\b", "/leading", "trailing/", "..", "."} {
		w := libPostJSON(t, api, "/api/v1/library/"+id+"/vaults", `{"name":"`+jsonEscape(name)+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code, "name=%q body=%s", name, w.Body.String())
	}
}

func TestLibraryCreateVault_EmptyName_400(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	w := libPostJSON(t, api, "/api/v1/library/"+id+"/vaults", `{"name":""}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

func TestLibraryCreateVault_UnknownWorkspace_404(t *testing.T) {
	api, _ := buildLibraryTestAPI(t)
	w := libPostJSON(t, api, "/api/v1/library/ws-nope/vaults", `{"name":"Research"}`)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestLibraryCreateVault_InvalidParentRelPath_400(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	w := libPostJSON(t, api, "/api/v1/library/"+id+"/vaults", `{"name":"Research","parent_rel_path":"../escape"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

func TestLibraryCreateVault_MissingParentDir_404(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	w := libPostJSON(t, api, "/api/v1/library/"+id+"/vaults", `{"name":"Research","parent_rel_path":"does-not-exist"}`)
	assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
}

// jsonEscape is a tiny helper for embedding an arbitrary name literal into
// the hand-built JSON bodies above without pulling in encoding/json just for
// escaping backslashes.
func jsonEscape(s string) string {
	out := make([]byte, 0, len(s)+2)
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' || s[i] == '"' {
			out = append(out, '\\')
		}
		out = append(out, s[i])
	}
	return string(out)
}
