// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package boardtask defines the canonical on-disk shape for GTD board tasks
// stored under ~/.omnipus/tasks/<id>.json.
//
// This is the single source of truth for the GTD task struct used by:
//   - pkg/gateway/rest_board.go (REST API handlers)
//   - pkg/sysagent/tools/task.go (system.task.* tools)
//
// GTD board tasks are distinct from workflow tasks (pkg/taskstore / ~/.omnipus/workflow-tasks/).
// GTD statuses are: inbox, next, active, waiting, done, failed.
// Workflow statuses are: queued, assigned, running, completed, failed.
package boardtask

// Task is the canonical on-disk format for a GTD board task stored at
// ~/.omnipus/tasks/<id>.json.
//
// not-wire-format: internal disk struct; mapped to gen.BoardTask at the REST layer.
type Task struct { //nolint:revive // exported name matches package purpose
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Prompt is the agent execution instruction (multiline markdown); max 10000 chars.
	Prompt string `json:"prompt,omitempty"`
	// Priority is 1 (critical) – 5 (low); 0 = unset (treated as 3 on read).
	Priority int `json:"priority,omitempty"`
	// MilestoneID is an optional FK to a milestone in the same project.
	MilestoneID string `json:"milestone_id,omitempty"`
	// SessionID is set when an agent starts executing the task; links to a chat session.
	SessionID string `json:"session_id,omitempty"`
	// Result is the execution output; set on done/failed.
	Result    string `json:"result,omitempty"`
	Status    string `json:"status"`
	ProjectID string `json:"project_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	// Owner is the username of the user who owns this task. Set at creation; read-only.
	// Carried through all reads/writes so it is never lost, even though enforcement
	// is deferred to a later implementation wave.
	Owner     string `json:"owner,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// GTDStatuses is the set of valid GTD task status values.
// Workflow tasks (pkg/taskstore) use queued/assigned/running/completed/failed — never these.
var GTDStatuses = map[string]bool{
	"inbox":   true,
	"next":    true,
	"active":  true,
	"waiting": true,
	"done":    true,
	"failed":  true,
}

// IsGTDStatus returns true when status is a known GTD status value.
func IsGTDStatus(status string) bool {
	return GTDStatuses[status]
}

// WorkflowStatuses is the set of valid workflow (taskstore) status values.
// Used by the boot migration to classify ambiguous files.
var WorkflowStatuses = map[string]bool{
	"queued":    true,
	"assigned":  true,
	"running":   true,
	"completed": true,
}
