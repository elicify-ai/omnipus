// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package workspace

import "github.com/elicify-ai/omnipus/pkg/task"

// fileLock is the process-wide striped mutex pool guarding every
// load-modify-write cycle against an EXISTING workspaces/{id}.json. It
// reuses task.StripedLock — the same 64-shard, FNV-32a-keyed pool pkg/memory's
// JSONLStore uses for per-session serialization (pkg/memory/jsonl.go:21-77)
// and pkg/task itself uses for per-task files (task.TaskFileLock) — so
// memory usage stays O(1) regardless of workspace count.
//
//nolint:gochecknoglobals
var fileLock = &task.StripedLock{}

// LockID acquires the mutex shard for the given workspace ID and returns an
// unlock func. Callers MUST hold it across the FULL load-modify-write cycle
// of an EXISTING workspaces/{id}.json — from the read that establishes the
// current state through the write that persists the new state — and release
// it via the returned func (typically `defer`):
//
//	unlock := workspace.LockID(id)
//	defer unlock()
//	ws, err := readWorkspaceFile(home, id)
//	...
//	err = writeWorkspaceFile(home, ws)
//
// This is the REQUIRED guard for any load-modify-write of an EXISTING
// workspace file. Without it, two concurrent writers (e.g. the WS
// setup-kickoff consumer and a REST PUT/DELETE/delegation-PUT, or the
// sysagent workspace tools' update/delete) can interleave: a racing PUT can
// resurrect a just-cleared setup_pending flag with a stale in-memory copy, a
// kickoff's write can clobber a concurrent rename or delegation-edge change,
// and a kickoff racing a delete can resurrect the deleted file as a ghost.
//
// Every writer that loads-modifies-writes an EXISTING workspaces/{id}.json —
// pkg/gateway's REST handlers (handleWorkspacePut, handleWorkspaceDelete,
// handleWorkspaceDelegationPut), the WS setup-kickoff consumer
// (consumeWorkspaceSetupKickoff), and pkg/sysagent/tools/workspace.go's
// update/delete tools — MUST acquire this same lock, keyed by the same
// workspace ID, before touching the file.
//
// Carve-out — blind creates do NOT acquire this lock, and correctly so: a
// create writes a brand-new ID (a fresh ULID) that no concurrent writer can
// already be holding or racing against, so there is nothing to serialize
// against. This applies to handleWorkspacePost (pkg/gateway), the
// WorkspaceCreateTool (pkg/sysagent/tools/workspace.go), and
// ensureDefaultWorkspace (pkg/gateway), which instead serializes concurrent
// boots against each other with its own defaultWorkspaceSeedMu (a
// process-wide mutex, not this per-ID pool) since a racing boot could target
// the SAME "is there already a default workspace" decision. The lock is
// keyed by ID, so it does NOT serialize a create against anything — no create
// path in this codebase ever calls LockID.
//
// Non-reentrancy hazard: this is a bare, non-reentrant mutex drawn from a
// 64-shard pool. Two DISTINCT workspace IDs can hash to the SAME shard, so:
// never call LockID a second time from a goroutine that is already holding a
// LockID result (including for a different workspace ID) — with 64 shards
// there is a 1/64 chance the second call blocks forever on a shard the same
// goroutine already locked (self-deadlock), and there is no reentrant/nested
// acquisition support. Never acquire two workspace locks in one goroutine.
func LockID(id string) func() {
	mu := fileLock.Get(id)
	mu.Lock()
	return mu.Unlock
}
