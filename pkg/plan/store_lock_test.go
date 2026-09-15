// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package plan

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/fileutil/fileutiltest"
)

// A plan file is written with fileutil.WriteFileAtomic (temp file + rename) so
// that no reader ever sees half a plan. The cross-process lock around that
// write used to be taken on the plan file itself; fileutil.WithFlock opens its
// path with O_CREATE, so a plan's first write created an EMPTY <id>.json and
// left it there until the rename replaced it. A List or Get in that window —
// from another process, or anything not holding this store's striped lock —
// read zero bytes and skipped or failed the plan, and a crash in that window
// left the empty file for good.

func TestPlanStore_FirstWriteNeverExposesAnIncompleteFile(t *testing.T) {
	s := newStore(t)
	p := mkPlan("lock test", "ws", "agent")
	p.ID = "01JLOCKPLANFIRSTWRITE00001"
	target := filepath.Join(s.Dir(), p.ID+".json")
	parked, release := fileutiltest.HoldFirstWrite(t, &writeFileAtomicFn, p.ID+".json")

	done := make(chan error, 1)
	go func() { done <- s.Create(p) }()
	require.Equal(t, target, fileutiltest.WaitParked(t, parked))
	fileutiltest.RequireAbsentOrCompleteJSON(t, target)

	release()
	fileutiltest.WaitDone(t, done)
	fileutiltest.RequireCompleteJSON(t, target)
	require.FileExists(t, fileutil.SidecarLockPath(target), "the plan write must take its lock on the sidecar")

	plans, err := s.List(Filter{})
	require.NoError(t, err)
	require.Len(t, plans, 1, "the sidecar lock file must never be listed as a plan")
}

func TestPlanStore_DeleteRemovesTheSidecarLockFile(t *testing.T) {
	s := newStore(t)
	p := mkPlan("delete me", "ws", "agent")
	require.NoError(t, s.Create(p))
	target := filepath.Join(s.Dir(), p.ID+".json")
	lock := fileutil.SidecarLockPath(target)
	require.FileExists(t, lock, "precondition: the plan's write created its sidecar lock file")

	require.NoError(t, s.Delete(p.ID))
	require.NoFileExists(t, target)
	require.NoFileExists(t, lock, "a deleted plan must not leave its lock file behind")

	require.ErrorIs(t, s.Delete(p.ID), ErrNotFound)
	require.NoFileExists(t, lock, "deleting a plan that does not exist must not create a lock file")
}
