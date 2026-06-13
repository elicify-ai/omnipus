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
//
// Note: "failed" appears in both status vocabularies. During boot migration the
// disambiguator uses the title field (non-empty title → workflow task) rather than
// status alone, so a workflow task with status "failed" is never misclassified as GTD.
package boardtask

// Status is the typed GTD task status. The wire JSON representation is a plain
// string (unchanged); using a named type catches accidental mixing with workflow
// status strings at compile time.
type Status string

// GTD board task status constants. These are the only valid values for Task.Status.
// Workflow tasks (pkg/taskstore) use a separate vocabulary; see IsWorkflowStatus.
const (
	StatusInbox   Status = "inbox"
	StatusNext    Status = "next"
	StatusActive  Status = "active"
	StatusWaiting Status = "waiting"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

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
	// MilestoneID is an optional FK to a milestone in the same workspace.
	MilestoneID string `json:"milestone_id,omitempty"`
	// SessionID is set when an agent starts executing the task; links to a chat session.
	SessionID string `json:"session_id,omitempty"`
	// Result is the execution output; set on done/failed.
	Result      string `json:"result,omitempty"`
	Status      Status `json:"status"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	// Owner is the username of the user who created this task. Attribution only — not an access gate.
	Owner     string `json:"owner,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// GTDStatuses is the set of valid GTD task status values.
// Workflow tasks (pkg/taskstore) use queued/assigned/running/completed/failed — never these.
//
// Treat this map as read-only. External callers must use IsGTDStatus for membership
// tests; the exported map is retained only for backward compatibility with code that
// requires the full set (e.g. range iteration or initialisation of a local alias).
// Do not mutate this map — mutations will corrupt GTD status validation globally.
var GTDStatuses = map[string]bool{ //nolint:gochecknoglobals
	string(StatusInbox):   true,
	string(StatusNext):    true,
	string(StatusActive):  true,
	string(StatusWaiting): true,
	string(StatusDone):    true,
	string(StatusFailed):  true,
}

// IsGTDStatus returns true when status is a known GTD status value.
func IsGTDStatus(status string) bool {
	return GTDStatuses[status]
}

// workflowStatuses is the unexported canonical set of valid workflow (taskstore) status values.
// "failed" is intentionally included: it is a terminal workflow status (see pkg/taskstore
// validStatuses). Disambiguation between GTD and workflow files with status "failed" is
// resolved by the title field: a non-empty title wins as workflow regardless of status.
//
// Unexported to prevent external mutation; use IsWorkflowStatus for membership tests.
var workflowStatuses = map[string]bool{
	"queued":    true,
	"assigned":  true,
	"running":   true,
	"completed": true,
	"failed":    true,
}

// IsWorkflowStatus returns true when status is a known workflow (taskstore) status value.
// Note: "failed" is a valid workflow status; use the title field to disambiguate when
// a file could belong to either vocabulary.
func IsWorkflowStatus(status string) bool {
	return workflowStatuses[status]
}
