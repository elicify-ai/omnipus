// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package systools

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/fileutil/fileutiltest"
)

// writeEntity writes the SAME files pkg/task and pkg/workspace write
// ($OMNIPUS_HOME/tasks/<id>.json, $OMNIPUS_HOME/workspaces/<id>.json), so it must
// take the same cross-process lock they take — the sidecar
// fileutil.SidecarLockPath names — or the system agent and the REST/task paths
// stop excluding each other. It also used to lock the entity file itself, so an
// entity's first write created it EMPTY until the rename landed.

func TestWriteEntity_FirstWriteNeverExposesAnIncompleteFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	const id = "task_lock_first_write"
	target := entityPath(dir, id)
	parked, release := fileutiltest.HoldFirstWrite(t, &writeFileAtomicFn, id+".json")

	done := make(chan error, 1)
	go func() { done <- writeEntity(dir, id, entityFixture{ID: id, Name: "first"}) }()
	require.Equal(t, target, fileutiltest.WaitParked(t, parked))
	fileutiltest.RequireAbsentOrCompleteJSON(t, target)

	release()
	fileutiltest.WaitDone(t, done)
	fileutiltest.RequireCompleteJSON(t, target)

	listed, err := listEntities[entityFixture](dir)
	require.NoError(t, err)
	require.Len(t, listed, 1, "the sidecar lock file must never be listed as an entity")
}

func TestWriteEntity_TakesTheSameLockAsTheTaskAndWorkspaceStores(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "workspaces")
	const id = "ws_lock_shared"
	require.NoError(t, writeEntity(dir, id, entityFixture{ID: id, Name: "seed"}))

	fileutiltest.RequireWaitsForSidecarLock(t, entityPath(dir, id), func() error {
		return writeEntity(dir, id, entityFixture{ID: id, Name: "rewrite"})
	})
}

func TestDeleteEntity_RemovesTheSidecarLockFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	const id = "task_lock_delete"
	require.NoError(t, writeEntity(dir, id, entityFixture{ID: id, Name: "doomed"}))
	target := entityPath(dir, id)
	lock := fileutil.SidecarLockPath(target)
	require.FileExists(t, lock, "precondition: the entity's write created its sidecar lock file")

	require.NoError(t, deleteEntity(dir, id))
	require.NoFileExists(t, target)
	require.NoFileExists(t, lock, "a deleted entity must not leave its lock file behind")

	require.NoError(t, deleteEntity(dir, id), "deleting an absent entity stays a success")
	require.NoFileExists(t, lock, "deleting an absent entity must not create a lock file")
}
