// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package workspace

import "github.com/elicify-ai/omnipus/pkg/task"

// fileLock is the process-wide striped mutex pool guarding every
// load-modify-write cycle against workspaces/{id}.json. It reuses
// task.StripedLock — the same 64-shard, FNV-32a-keyed pool pkg/memory's
// JSONLStore uses for per-session serialization (pkg/memory/jsonl.go:21-77)
// and pkg/task itself uses for per-task files (task.TaskFileLock) — so
// memory usage stays O(1) regardless of workspace count.
//
//nolint:gochecknoglobals
var fileLock = &task.StripedLock{}

// LockID acquires the mutex shard for the given workspace ID and returns an
// unlock func. Callers MUST hold it across the FULL load-modify-write cycle
// of workspaces/{id}.json — from the read that establishes the current state
// through the write that persists the new state — and release it via the
// returned func (typically `defer`):
//
//	unlock := workspace.LockID(id)
//	defer unlock()
//	ws, err := readWorkspaceFile(home, id)
//	...
//	err = writeWorkspaceFile(home, ws)
//
// This is the REQUIRED guard for any read-modify-write of a workspace file.
// Without it, two concurrent writers (e.g. the WS setup-kickoff consumer and
// a REST PUT/DELETE/delegation-PUT, or the sysagent workspace tools) can
// interleave: a racing PUT can resurrect a just-cleared setup_pending flag
// with a stale in-memory copy, a kickoff's write can clobber a concurrent
// rename or delegation-edge change, and a kickoff racing a delete can
// resurrect the deleted file as a ghost.
//
// Every writer of workspaces/{id}.json — pkg/gateway's REST handlers
// (handleWorkspacePut, handleWorkspaceDelete, handleWorkspaceDelegationPut),
// the WS setup-kickoff consumer (consumeWorkspaceSetupKickoff), and
// pkg/sysagent/tools/workspace.go's create/update/delete tools — MUST
// acquire this same lock, keyed by the same workspace ID, before touching
// the file. The lock is keyed by ID, not by file path, so it also correctly
// serializes a create against a delete/recreate racing the same ID.
func LockID(id string) func() {
	mu := fileLock.Get(id)
	mu.Lock()
	return mu.Unlock
}
