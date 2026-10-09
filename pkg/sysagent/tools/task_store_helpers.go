// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// The shared task-store helpers that used to live in the now-deleted task.go,
// kept here after DEL-23 removed that file's four *_in_workspace task tools.
// They are the small vocabulary the OTHER sysagent tools already speak —
// agent.go's cascade-unassign, workspace.go's task counts and cascade delete —
// so they remain: the retired tools' implementations are gone, these four
// helpers stay because real callers remain.

// unifiedTask is the canonical on-disk task type used by the sysagent tools.
// Using task.Task ensures field-preserving read-modify-write: all fields survive
// a round-trip through writeEntity.
type unifiedTask = task.Task

func tasksDir(home string) string { return filepath.Join(home, "tasks") }

// taskStoreFor returns a task.Store rooted at the home's tasks directory. It
// shares the process-wide task.TaskFileLock so its DAG validation, auto-advance,
// and cascade operations interleave correctly with the gateway REST handlers.
func taskStoreFor(home string) *task.Store { return task.New(tasksDir(home)) }

// isValidTaskStatus reports whether s is one of the 6 unified statuses.
func isValidTaskStatus(s string) bool { return task.IsValidStatus(task.Status(s)) }
