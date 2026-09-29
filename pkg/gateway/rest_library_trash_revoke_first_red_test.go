// Omnipus — FR-VA-036 / TDD row 85: Library trash revokes recorded
// authority before acting, reports post-revocation failure, and can repeat.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/stretchr/testify/require"
)

func incompleteTrashFixture(t *testing.T) (*restAPI, string, string, string) {
	t.Helper()
	api, ws, vault, _ := twoKBWorkspace(t)
	plantTrackedView(t, api.homePath, vault, "Projects.base", "Sub/Open.view")
	// Block the trash destination with a regular file. Preflight and marker
	// stripping still work; the engine must fail AFTER the record save.
	blocked := filepath.Join(knowledge.MarkerDir(vault), "trash")
	require.NoError(t, os.WriteFile(blocked, []byte("blocked"), 0o600))
	return api, ws, vault, blocked
}

func requireTrashIncomplete(t *testing.T, api *restAPI, ws, folder, path string, additionalPaths ...string) {
	t.Helper()
	w := libDelete(t, api, "/api/v1/library/"+ws+"/entries?path="+folder)
	require.Equal(t, http.StatusConflict, w.Code, "post-revocation trash failure must be visible: %s", w.Body.String())
	var body gen.LibraryMoveConflictError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, gen.LibraryMoveConflictErrorCodeTrashIncomplete, body.Code)
	require.NotNil(t, body.Paths)
	for _, expected := range append([]string{path}, additionalPaths...) {
		require.Contains(t, *body.Paths, expected, "incomplete trash must identify every affected view")
	}
	require.NotContains(t, w.Body.String(), "pending_move_id", "trash is not a rename Retry receipt")
}

func TestLibraryTrash_RevokesBeforeTrashingAndReportsTrashIncompleteOnInjectedFailure(t *testing.T) {
	api, ws, vault, _ := incompleteTrashFixture(t)
	requireTrashIncomplete(t, api, ws, "vault-a/Sub", "vault-a/Sub/Open.view")
	require.DirExists(t, filepath.Join(vault, "Sub"), "trash failed without moving the source")
	require.Empty(t, loadRecordedViews(t, api.homePath, vault).Bases,
		"all authority must have been revoked before the injected trash failure")
	body, err := os.ReadFile(filepath.Join(vault, "Sub", "Open.view"))
	require.NoError(t, err)
	require.False(t, strings.Contains(string(body), "derived_from:"), "the released view marker is stripped")
}

func TestLibraryTrash_RetryingAfterTrashIncompleteSucceedsIdempotently(t *testing.T) {
	api, ws, vault, blocked := incompleteTrashFixture(t)
	requireTrashIncomplete(t, api, ws, "vault-a/Sub", "vault-a/Sub/Open.view")
	require.NoError(t, os.Remove(blocked))
	w := libDelete(t, api, "/api/v1/library/"+ws+"/entries?path=vault-a/Sub")
	require.Equal(t, http.StatusNoContent, w.Code, "repeat trash must finish without re-enrolling: %s", w.Body.String())
	require.NoDirExists(t, filepath.Join(vault, "Sub"))
	require.Empty(t, loadRecordedViews(t, api.homePath, vault).Bases)
	copies, err := filepath.Glob(filepath.Join(knowledge.MarkerDir(vault), "trash", "*", "Sub", "Open.view"))
	require.NoError(t, err)
	require.Len(t, copies, 1)
}

func TestLibraryTrash_NestedRootFolderTakesSortedLocksAndRevokesAllRootsBeforeTrashing(t *testing.T) {
	api, ws, outer, _ := twoKBWorkspace(t)
	plantTrackedView(t, api.homePath, outer, "Projects.base", "Outer/Outer.view")
	nested := filepath.Join(outer, "Outer", "NestedKB")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	makeKnowledgeBase(t, nested, "Nested KB")
	plantTrackedView(t, api.homePath, nested, "Nested.base", "Nested.view")
	blocked := filepath.Join(knowledge.MarkerDir(outer), "trash")
	require.NoError(t, os.WriteFile(blocked, []byte("blocked"), 0o600))
	requireTrashIncomplete(t, api, ws, "vault-a/Outer", "vault-a/Outer/Outer.view",
		"vault-a/Outer/NestedKB/Nested.view")
	require.DirExists(t, nested, "injected trash failure leaves the nested root in place")
	require.Empty(t, loadRecordedViews(t, api.homePath, outer).Bases,
		"outside-folder base must release its view inside the folder")
	require.Empty(t, loadRecordedViews(t, api.homePath, nested).Bases,
		"the nested KB must also revoke before the parent trash starts")
	require.FileExists(t, filepath.Join(nested, "Nested.view"))
}

func TestLibraryTrash_CopyPlantedAtTrashedPathCannotBeRewrittenOrDeleted(t *testing.T) {
	api, ws, vault, _ := twoKBWorkspace(t)
	plantTrackedView(t, api.homePath, vault, "Projects.base", "Sub/Open.view")
	w := libDelete(t, api, "/api/v1/library/"+ws+"/entries?path=vault-a/Sub")
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	copyPath := filepath.Join(vault, "Sub", "Open.view")
	require.NoError(t, os.MkdirAll(filepath.Dir(copyPath), 0o755))
	planted := []byte("name: open\nkind: table\nderived_from: Projects.base\n")
	require.NoError(t, os.WriteFile(copyPath, planted, 0o600))
	require.NoError(t, knowledge.WithViewMembership(api.homePath, vault, func(m *knowledge.ViewMembership) error {
		deleted, err := m.DeleteManagedView("Projects.base", "open")
		require.NoError(t, err)
		require.False(t, deleted, "a later base save cannot delete the planted copy")
		written, err := m.RewriteManagedView("Projects.base", "open", planted)
		require.Error(t, err, "a later base save cannot rewrite an unrecorded copy")
		require.False(t, written)
		return nil
	}))
	body, err := os.ReadFile(copyPath)
	require.NoError(t, err)
	require.Equal(t, planted, body)
	require.Empty(t, loadRecordedViews(t, api.homePath, vault).Bases)
}
