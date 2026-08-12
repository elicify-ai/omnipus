// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Tests wiring workspace mounts (pkg/workspace/mount.go) into the REST
// surface: the GET/list wire response (workspaceToWire's mountsToWire) and
// the FR-9.2-style raw-body-sniff guard on POST/PUT (spec FR-5, ADR-063 D4).
//
// There is no dedicated mounts CRUD endpoint yet (see this package's
// rest_workspaces.go doc comments and the task report this test file
// accompanies) — mounts are created directly via workspace.CreateMount, the
// same call a future dedicated endpoint will make.

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

// TestHandleWorkspaces_GetIncludesMountsWithLiveStatus proves the GET
// response carries every mount with its LIVE-computed status (FR-8.2): "ok"
// for a resolving target, "broken" for one whose target has since vanished
// — computed at read time, matching task_count's own convention, never
// stored on the mount record itself.
func TestHandleWorkspaces_GetIncludesMountsWithLiveStatus(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "MountsProject", "")

	okTarget := t.TempDir()
	brokenTarget := t.TempDir()

	mOK, _, err := workspace.CreateMount(api.homePath, id, "ok-mount", okTarget)
	require.NoError(t, err)
	mBroken, _, err := workspace.CreateMount(api.homePath, id, "broken-mount", brokenTarget)
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(brokenTarget))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+id, nil)
	r.URL.Path = "/api/v1/workspaces/" + id
	api.HandleWorkspaces(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var got gen.Workspace
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.NotNil(t, got.Mounts)
	require.Len(t, *got.Mounts, 2)

	byName := map[string]struct {
		HostPath string
		Status   *gen.WorkspaceMountsStatus
	}{}
	for _, m := range *got.Mounts {
		byName[m.Name] = struct {
			HostPath string
			Status   *gen.WorkspaceMountsStatus
		}{m.HostPath, m.Status}
	}

	okEntry, ok := byName["ok-mount"]
	require.True(t, ok)
	assert.Equal(t, mOK.HostPath, okEntry.HostPath)
	require.NotNil(t, okEntry.Status)
	assert.Equal(t, gen.WorkspaceMountsStatusOk, *okEntry.Status)

	brokenEntry, ok := byName["broken-mount"]
	require.True(t, ok)
	assert.Equal(t, mBroken.HostPath, brokenEntry.HostPath)
	require.NotNil(t, brokenEntry.Status)
	assert.Equal(t, gen.WorkspaceMountsStatusBroken, *brokenEntry.Status)
}

// TestHandleWorkspaces_ListOmitsMountsWhenNone verifies the "absent when no
// mount exists" contract (WorkspaceMount.yaml) — a workspace with no mounts
// must not carry an empty-but-present mounts array.
func TestHandleWorkspaces_GetOmitsMountsWhenNone(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "NoMountsProject", "")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+id, nil)
	r.URL.Path = "/api/v1/workspaces/" + id
	api.HandleWorkspaces(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var got gen.Workspace
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Nil(t, got.Mounts, "a workspace with no mounts must omit the field, not send an empty array")
}

// TestHandleWorkspaces_Delete_NeverTouchesMountedFolder is the REST-level
// proof of spec test-dataset #22/FR-8.6: deleting a workspace through the
// real DELETE handler must never touch the operator's mounted folder or its
// contents, even though the handler unconditionally os.RemoveAll's the
// workspace's own directory (which contains the mount's symlink).
func TestHandleWorkspaces_Delete_NeverTouchesMountedFolder(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "DeleteMeProject", "")

	target := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(target, "operator-file.txt"), []byte("do not touch"), 0o600))

	_, _, err := workspace.CreateMount(api.homePath, id, "client-repo", target)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/"+id, nil)
	r.URL.Path = "/api/v1/workspaces/" + id
	api.HandleWorkspaces(w, r)
	require.Equal(t, http.StatusNoContent, w.Code, "body=%s", w.Body.String())

	data, err := os.ReadFile(filepath.Join(target, "operator-file.txt"))
	require.NoError(t, err, "the operator's mounted folder must survive workspace deletion (FR-8.6)")
	require.Equal(t, "do not touch", string(data))

	// The workspace's own directory (and the symlink inside it) is gone.
	_, statErr := os.Stat(workspace.WorkspaceDir(api.homePath, id))
	require.True(t, os.IsNotExist(statErr))
}

// TestHandleWorkspaces_CreateAndUpdate_RejectMountsField mirrors the
// repository raw-body-sniff precedent (FR-9.2) for "mounts": mounts have
// their own dedicated lifecycle, not the create/update body.
func TestHandleWorkspaces_CreateAndUpdate_RejectMountsField(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	t.Run("POST with mounts field 400s", func(t *testing.T) {
		w := httptest.NewRecorder()
		body := `{"name":"X","mounts":[{"name":"foo","host_path":"/tmp"}]}`
		r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/workspaces"
		api.HandleWorkspaces(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		assert.Contains(t, w.Body.String(), "mounts")
	})

	t.Run("PUT with mounts field 400s", func(t *testing.T) {
		id := createWorkspaceViaAPI(t, api, "PutRejectMounts", "")
		w := httptest.NewRecorder()
		body := `{"mounts":[{"name":"foo","host_path":"/tmp"}]}`
		r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+id, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/workspaces/" + id
		api.HandleWorkspaces(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		assert.Contains(t, w.Body.String(), "mounts")
	})
}
