// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Task files and workspace records are written under the cross-process lock
// on their sidecar (fileutil.SidecarLockPath). The workspace-delete cascade
// removes both kinds of file itself, so it must remove their lock files too, or
// every deleted workspace leaves lock files behind in tasks/ and workspaces/.

func TestDeleteTasksForWorkspace_RemovesEachDeletedTasksLockFile(t *testing.T) {
	home := t.TempDir()
	store := task.New(filepath.Join(home, "tasks"))
	doomed := &task.Task{Title: "doomed", Action: task.ActionLLM, WorkspaceID: "ws-doomed"}
	kept := &task.Task{Title: "kept", Action: task.ActionLLM, WorkspaceID: "ws-kept"}
	require.NoError(t, store.Create(doomed))
	require.NoError(t, store.Create(kept))
	doomedPath := filepath.Join(home, "tasks", doomed.ID+".json")
	keptPath := filepath.Join(home, "tasks", kept.ID+".json")
	require.FileExists(t, fileutil.SidecarLockPath(doomedPath), "precondition: the task's write created its sidecar")

	require.NoError(t, deleteTasksForWorkspace(home, "ws-doomed"))

	require.NoFileExists(t, doomedPath)
	require.NoFileExists(t, fileutil.SidecarLockPath(doomedPath), "a cascade-deleted task must not leave its lock file behind")
	require.FileExists(t, keptPath, "a task in another workspace is untouched")
	require.FileExists(t, fileutil.SidecarLockPath(keptPath))
}

func TestHandleWorkspaceDelete_RemovesTheWorkspaceRecordLockFile(t *testing.T) {
	api, _ := newTestAPIWithAuditor(t)
	id := createWorkspaceViaAPI(t, api, "LockFileCleanup", "")
	recordPath := filepath.Join(api.homePath, "workspaces", id+".json")
	lock := fileutil.SidecarLockPath(recordPath)
	require.FileExists(t, lock, "precondition: creating the workspace wrote its record under the sidecar lock")

	r := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, api, id), nil)
	r = r.WithContext(contextWithUser(r.Context(), "alice"))
	w := httptest.NewRecorder()
	api.handleWorkspaceDelete(w, r, id)
	require.Equal(t, http.StatusNoContent, w.Code, "body: %s", w.Body.String())

	require.NoFileExists(t, recordPath)
	require.NoFileExists(t, lock, "a deleted workspace must not leave its record's lock file behind")
}
