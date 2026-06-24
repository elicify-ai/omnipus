// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package task is the single source of truth for the unified Task entity
// (Sprint 2, Tier 2). It replaces the two legacy stores — pkg/boardtask (GTD
// board) and pkg/taskstore (workflow queue) — with one per-entity JSON store
// under ~/.omnipus/tasks/<id>.json.
//
// There is no back-compat (remediation Detail #7): one entity, one 7-state
// status vocabulary, one create/update path. The store carries over from
// pkg/boardtask: the blocked_by DAG cycle validator (self-edge / 2-node /
// N-node cycle rejection, max depth 50, orphan-edge drop on boot via
// DropOrphanEdges) and the auto-advance behavior (blocked → next when every
// dependency reaches done).
//
// Concurrency: per-entity read-modify-write paths are serialized by the
// process-wide 64-shard StripedLock (lock.go) keyed by task ID, with an
// advisory flock on the file itself for cross-process safety on Linux/macOS.
package task

import (
	"encoding/json"
	"time"
)

// Status is the unified 7-state task lifecycle (remediation Detail #1).
//
//	inbox → next → planning → in_progress → done   (+ failed)
//
// with blocked an auto side-state (set when a dependency is unmet, cleared to
// next when every blocked_by dep reaches done).
type Status string

// The seven canonical task statuses. These are the ONLY valid Status values.
const (
	StatusInbox      Status = "inbox"       // captured / untriaged
	StatusNext       Status = "next"        // triaged & ready to start
	StatusPlanning   Status = "planning"    // an agent is decomposing it
	StatusInProgress Status = "in_progress" // worked by a human OR agent
	StatusBlocked    Status = "blocked"     // auto side-state: unmet dependency
	StatusDone       Status = "done"        // terminal success
	StatusFailed     Status = "failed"      // terminal failure
)

// validStatuses is the set of allowed Status values.
var validStatuses = map[Status]bool{ //nolint:gochecknoglobals
	StatusInbox:      true,
	StatusNext:       true,
	StatusPlanning:   true,
	StatusInProgress: true,
	StatusBlocked:    true,
	StatusDone:       true,
	StatusFailed:     true,
}

// IsValidStatus reports whether s is one of the seven canonical statuses.
func IsValidStatus(s Status) bool { return validStatuses[s] }

// IsTerminal reports whether s is a terminal status (done or failed).
func IsTerminal(s Status) bool { return s == StatusDone || s == StatusFailed }

// Action is the kind of work a task performs (remediation D4). Tier 2 ships
// `llm` only; the type reserves room for v0.3 (human/tool/notify/sub_workflow).
type Action string

// ActionLLM is the only Tier-2 action: run an agent.
const ActionLLM Action = "llm"

// IsValidAction reports whether a is a known (Tier-2) action.
func IsValidAction(a Action) bool { return a == ActionLLM }

// Surface marks which UI surface owns a task (remediation Detail #5).
type Surface string

// Surface values. `user` shows on all general views; `heartbeat` is hidden
// from general views and rendered only by its owning feature (agent profile).
const (
	SurfaceUser      Surface = "user"
	SurfaceHeartbeat Surface = "heartbeat"
)

// IsValidSurface reports whether s is a known surface.
func IsValidSurface(s Surface) bool { return s == SurfaceUser || s == SurfaceHeartbeat }

// TriggerType is the discriminator for a TaskTrigger (remediation Detail #3).
// Tier 2 ships time-only kinds; v0.3 adds event kinds additively.
type TriggerType string

// Trigger kinds (Tier 2, time-only).
const (
	TriggerManual    TriggerType = "manual"    // no auto trigger; starts on drag-to-in_progress / Run
	TriggerOnce      TriggerType = "once"      // fire exactly once at config.at_ms
	TriggerEvery     TriggerType = "every"     // fire every config.every_ms (>=1000)
	TriggerRecurring TriggerType = "recurring" // fire on config.cron_expr
)

// IsValidTriggerType reports whether t is a known (Tier-2) trigger kind.
func IsValidTriggerType(t TriggerType) bool {
	switch t {
	case TriggerManual, TriggerOnce, TriggerEvery, TriggerRecurring:
		return true
	default:
		return false
	}
}

// TriggerConfig holds the kind-specific parameters of a TaskTrigger. The
// relevant subset depends on the trigger Type. This is the open growth surface
// for v0.3 event kinds — they add their own keys here without changing the
// outer shape.
type TriggerConfig struct {
	// AtMs is the Unix epoch-milliseconds instant for a `once` fire.
	AtMs *int64 `json:"at_ms,omitempty"`
	// EveryMs is the interval in milliseconds for an `every` fire (min 1000).
	EveryMs *int64 `json:"every_ms,omitempty"`
	// CronExpr is the 5/6-field cron expression for a `recurring` fire.
	CronExpr *string `json:"cron_expr,omitempty"`
}

// Trigger is when (and how) a task fires (remediation Detail #3). Modeled as
// an extensible {type, config} shape so the v0.3 multi-trigger future grows
// additively, but Tier-2-restricted to time-only kinds.
type Trigger struct {
	Type   TriggerType   `json:"type"`
	Config TriggerConfig `json:"config"`
}

// TodoStatus is the tri-state status of a checklist Todo.
type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

// validTodoStatuses is the set of allowed TodoStatus values.
var validTodoStatuses = map[TodoStatus]bool{ //nolint:gochecknoglobals
	TodoPending:    true,
	TodoInProgress: true,
	TodoCompleted:  true,
}

// IsValidTodoStatus reports whether s is one of the three canonical todo statuses.
func IsValidTodoStatus(s TodoStatus) bool { return validTodoStatuses[s] }

// Todo is a lightweight {text, status} checklist item embedded on a task.
// A todo is NOT a task: no agent, no trigger, no ID.
type Todo struct {
	Text   string     `json:"text"`
	Status TodoStatus `json:"status"`
}

// UnmarshalJSON implements json.Unmarshaler for Todo.
// It tolerates legacy on-disk JSON written before the tri-state migration
// that has `"done": true|false` and no `"status"` field, upgrading those
// records to the new Status vocabulary on read.
func (td *Todo) UnmarshalJSON(data []byte) error {
	// tolerant: accept legacy {"done":bool} and new {"status":"..."}.
	var raw struct {
		Text   string `json:"text"`
		Status string `json:"status"`
		Done   *bool  `json:"done"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	td.Text = raw.Text
	switch {
	case raw.Status != "":
		td.Status = TodoStatus(raw.Status)
	case raw.Done != nil && *raw.Done:
		td.Status = TodoCompleted
	case raw.Done != nil:
		td.Status = TodoPending
	default:
		td.Status = TodoPending // empty -> pending
	}
	return nil
}

// Task is the unified on-disk task entity stored at ~/.omnipus/tasks/<id>.json.
// It merges the legacy boardtask.Task and taskstore.TaskEntity into one record.
//
// not-wire-format: internal disk struct; mapped to gen.Task at the REST layer.
type Task struct { //nolint:revive // exported name matches package purpose
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// Prompt is the agent execution instruction for an `llm` action (max 10000).
	Prompt string `json:"prompt,omitempty"`
	Action Action `json:"action"`
	Status Status `json:"status"`
	// AgentID is the assigned agent. Empty for human-only tasks.
	AgentID string `json:"agent_id,omitempty"`
	// Priority is 1 (highest) – 5 (lowest); 0 = unset (treated as 3 on read).
	Priority int `json:"priority,omitempty"`
	// BlockedBy is the ordered DAG dependency set. A write-time cycle validator
	// rejects self-edges and cycles; orphan edges are dropped on load; max
	// depth 50.
	BlockedBy []string `json:"blocked_by,omitempty"`
	// Todos is the lightweight embedded checklist (Tier 1).
	Todos []Todo `json:"todos,omitempty"`
	// ParentTaskID links a subtask to its parent (delegation / decomposition).
	ParentTaskID string `json:"parent_task_id,omitempty"`
	// WorkspaceID is required-scoped — every task belongs to a workspace.
	WorkspaceID string `json:"workspace_id"`
	MilestoneID string `json:"milestone_id,omitempty"`
	// Trigger is the task's time trigger (nil = manual).
	Trigger *Trigger `json:"trigger,omitempty"`
	// Due is an optional RFC 3339 UTC deadline (separate from Trigger).
	Due     string  `json:"due,omitempty"`
	Surface Surface `json:"surface,omitempty"`
	// SourceChannel/SourceChatID route a delegated-task result back to chat.
	SourceChannel string `json:"source_channel,omitempty"`
	SourceChatID  string `json:"source_chat_id,omitempty"`
	// SessionID links the run session created when the task executes.
	SessionID string   `json:"session_id,omitempty"`
	Result    string   `json:"result,omitempty"`
	Artifacts []string `json:"artifacts,omitempty"`
	// Owner / CreatedBy are server-set attribution (read-only on the wire).
	Owner     string `json:"owner,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
	// Timestamps are RFC 3339 UTC strings.
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	StartedAt   string `json:"started_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
	// FollowedUp is set true once the parent follow-up notification has been
	// launched for this (parent) task — makes the "all children done → resume
	// parent" follow-up fire exactly once under concurrent sibling completion.
	FollowedUp bool `json:"followed_up,omitempty"`
	// DelegationDepth is the task-mode delegation generation counter. A task
	// created from a normal (root) chat/agent turn has depth 0; a task created
	// from *within* a running task carries its spawning run's depth + 1. The
	// task executor seeds the run's root turnState depth from this value so the
	// per-agent DelegationPolicy depth gate (and the global subturn ceiling)
	// bounds onward delegation, and task_create rejects a create that would
	// exceed maxTaskDepth. This is an internal recursion-bound counter — it is
	// NOT part of the gen.Task wire contract and never crosses the gateway/SPA
	// boundary (the REST task mapper does not copy it).
	DelegationDepth int `json:"delegation_depth,omitempty"`
}

// EffectivePriority returns the task priority, defaulting an unset (0) value
// to 3.
func (t *Task) EffectivePriority() int {
	if t.Priority == 0 {
		return 3
	}
	return t.Priority
}

// EffectiveSurface returns the task surface, defaulting an empty value to
// SurfaceUser.
func (t *Task) EffectiveSurface() Surface {
	if t.Surface == "" {
		return SurfaceUser
	}
	return t.Surface
}

// CreatedTime parses CreatedAt as RFC 3339, falling back to the zero time on a
// parse error.
func (t *Task) CreatedTime() time.Time {
	ts, err := time.Parse(time.RFC3339, t.CreatedAt)
	if err != nil {
		return time.Time{}
	}
	return ts
}
