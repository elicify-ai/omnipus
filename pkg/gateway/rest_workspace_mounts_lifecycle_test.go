// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Tests the mount lifecycle REST endpoints (rest_workspace_mounts.go):
// POST /api/v1/workspaces/{id}/mounts and DELETE
// /api/v1/workspaces/{id}/mounts/{name} (spec FR-7.1/7.2/7.3/8.1, ADR-061
// D4/D6). rest_workspace_mounts_test.go covers the GET wire shape and the
// POST/PUT /workspaces raw-body-sniff guard — this file covers the dedicated
// lifecycle endpoints themselves.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func postMount(t *testing.T, api *restAPI, id, name, hostPath string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(gen.WorkspaceMountCreateRequest{Name: name, HostPath: hostPath})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+id+"/mounts", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces/" + id + "/mounts"
	api.HandleWorkspaces(w, r)
	return w
}

func deleteMount(t *testing.T, api *restAPI, id, name string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	path := "/api/v1/workspaces/" + id + "/mounts/" + name
	r := httptest.NewRequest(http.MethodDelete, path, nil)
	r.URL.Path = path
	api.HandleWorkspaces(w, r)
	return w
}

// getWorkspaceMounts GETs the workspace and returns just its Mounts field
// (an anonymous *[]struct{...} on gen.Workspace — there is no named
// gen.WorkspaceMount type because WorkspaceMount.yaml is only ever
// referenced from inside Workspace.yaml's array field, so oapi-codegen never
// emits a standalone type for it), so callers get real generated-type field
// access instead of re-declaring a parallel anonymous shape that could drift
// from the real one.
func getWorkspaceMounts(t *testing.T, api *restAPI, id string) gen.Workspace {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+id, nil)
	r.URL.Path = "/api/v1/workspaces/" + id
	api.HandleWorkspaces(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var got gen.Workspace
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	return got
}

// TestPostWorkspaceMount_CreateAppearsInGet proves the happy path end to
// end: create via the lifecycle endpoint, then confirm it shows up in
// GET /workspaces/{id} with status "ok" — the round-trip the task brief
// requires ("mount appears in GET with status: ok").
func TestPostWorkspaceMount_CreateAppearsInGet(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "MountLifecycle", "")
	target := t.TempDir()

	w := postMount(t, api, id, "client-repo", target)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())

	var resp gen.WorkspaceMountCreateResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "client-repo", resp.Name)
	assert.Equal(t, gen.Ok, resp.Status)
	assert.Nil(t, resp.Warning, "an ordinary target must not carry a warning")

	realTarget, err := filepath.EvalSymlinks(target)
	require.NoError(t, err)
	assert.Equal(t, realTarget, resp.HostPath)

	ws := getWorkspaceMounts(t, api, id)
	require.NotNil(t, ws.Mounts)
	require.Len(t, *ws.Mounts, 1)
	assert.Equal(t, "client-repo", (*ws.Mounts)[0].Name)
	require.NotNil(t, (*ws.Mounts)[0].Status)
	assert.Equal(t, gen.WorkspaceMountsStatusOk, *(*ws.Mounts)[0].Status)
}

// TestPostWorkspaceMount_InvalidName covers a name that traverses ("../x")
// — 400.
func TestPostWorkspaceMount_InvalidName(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "MountInvalidName", "")
	target := t.TempDir()

	w := postMount(t, api, id, "../x", target)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
}

// TestPostWorkspaceMount_CollidesWithExistingWorkEntry covers a name that
// collides with an entry already present in work/ (not a mount — a plain
// file/dir an operator or agent put there) — 400.
func TestPostWorkspaceMount_CollidesWithExistingWorkEntry(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "MountWorkCollision", "")

	workDir, err := workspace.EnsureWorkDir(api.homePath, id)
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(workDir, "existing-entry"), 0o700))

	target := t.TempDir()
	w := postMount(t, api, id, "existing-entry", target)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
}

// TestPostWorkspaceMount_DuplicatesExistingMount covers a name that
// duplicates an existing mount on the same workspace — 400.
func TestPostWorkspaceMount_DuplicatesExistingMount(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "MountDuplicate", "")

	first := t.TempDir()
	w := postMount(t, api, id, "dup-name", first)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())

	second := t.TempDir()
	w = postMount(t, api, id, "dup-name", second)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
}

// TestPostWorkspaceMount_TargetIsOmnipusHome_Refused covers the direct form
// of FR-7.5: the raw target IS $OMNIPUS_HOME (a.homePath in this test
// harness) — 403.
func TestPostWorkspaceMount_TargetIsOmnipusHome_Refused(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "MountRefuseDirect", "")

	w := postMount(t, api, id, "home-mount", api.homePath)
	assert.Equal(t, http.StatusForbidden, w.Code, "body=%s", w.Body.String())
}

// TestPostWorkspaceMount_TargetSymlinkedToOmnipusHome_Refused covers the
// symlink form FR-7.5 explicitly calls out as the one that matters ("the
// direct form is not what anyone would actually attempt"): a symlink whose
// resolved target IS $OMNIPUS_HOME — 403.
func TestPostWorkspaceMount_TargetSymlinkedToOmnipusHome_Refused(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "MountRefuseSymlink", "")

	// The symlink itself must live outside api.homePath (a directory INSIDE
	// $OMNIPUS_HOME would itself already be refused via the "lies inside"
	// branch, which is not the case this test is isolating), pointing AT
	// api.homePath.
	outside := t.TempDir()
	linkPath := filepath.Join(outside, "home-link")
	require.NoError(t, os.Symlink(api.homePath, linkPath))

	w := postMount(t, api, id, "home-symlink-mount", linkPath)
	assert.Equal(t, http.StatusForbidden, w.Code, "body=%s", w.Body.String())
}

// TestPostWorkspaceMount_TargetContainsOmnipusHome_WarnsAndAllows covers
// FR-7.4/FR-7.6: a target that CONTAINS $OMNIPUS_HOME (the parent directory
// of a.homePath, in this harness) succeeds (201) but MUST carry a non-empty
// warning in the response body — the operator's explicit decision, and the
// task brief requires asserting the warning is actually returned, not just
// that the call succeeded.
func TestPostWorkspaceMount_TargetContainsOmnipusHome_WarnsAndAllows(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "MountWarnContains", "")

	parent := filepath.Dir(api.homePath)

	w := postMount(t, api, id, "broad-mount", parent)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())

	var resp gen.WorkspaceMountCreateResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Warning, "a broad-but-allowed target must return a non-empty warning in the response body")
	assert.NotEmpty(t, *resp.Warning)

	ws := getWorkspaceMounts(t, api, id)
	require.NotNil(t, ws.Mounts)
	require.Len(t, *ws.Mounts, 1)
	assert.Equal(t, "broad-mount", (*ws.Mounts)[0].Name)
}

// TestDeleteWorkspaceMount_RemovesFromGetAndDiskButPreservesOperatorFolder
// covers FR-8.6: delete removes the mount from GET and removes the work/
// symlink from disk, but the operator's real folder and its contents
// survive untouched.
func TestDeleteWorkspaceMount_RemovesFromGetAndDiskButPreservesOperatorFolder(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "MountDelete", "")

	target := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(target, "operator-file.txt"), []byte("do not touch"), 0o600))

	w := postMount(t, api, id, "client-repo", target)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())

	workDir, err := workspace.SafeWorkDir(api.homePath, id)
	require.NoError(t, err)
	linkPath := filepath.Join(workDir, "client-repo")
	_, statErr := os.Lstat(linkPath)
	require.NoError(t, statErr, "the symlink must exist on disk right after create")

	w = deleteMount(t, api, id, "client-repo")
	require.Equal(t, http.StatusNoContent, w.Code, "body=%s", w.Body.String())

	ws := getWorkspaceMounts(t, api, id)
	assert.Nil(t, ws.Mounts, "the mount must be gone from GET after delete")

	_, statErr = os.Lstat(linkPath)
	assert.True(t, os.IsNotExist(statErr), "the work/ symlink must be removed from disk")

	data, err := os.ReadFile(filepath.Join(target, "operator-file.txt"))
	require.NoError(t, err, "the operator's real folder must survive mount deletion (FR-8.6)")
	assert.Equal(t, "do not touch", string(data))
}

// TestDeleteWorkspaceMount_UnknownName covers deleting a name that does not
// name any mount on the workspace — 404.
func TestDeleteWorkspaceMount_UnknownName(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "MountDeleteUnknown", "")

	w := deleteMount(t, api, id, "does-not-exist")
	assert.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
}

// TestPostWorkspaceMount_UnknownWorkspace covers POST against a workspace ID
// that does not exist — 404.
func TestPostWorkspaceMount_UnknownWorkspace(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	target := t.TempDir()

	w := postMount(t, api, "does-not-exist-ws", "client-repo", target)
	assert.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
}

// TestDeleteWorkspaceMount_UnknownWorkspace covers DELETE against a
// workspace ID that does not exist — 404.
func TestDeleteWorkspaceMount_UnknownWorkspace(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := deleteMount(t, api, "does-not-exist-ws", "client-repo")
	assert.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
}
