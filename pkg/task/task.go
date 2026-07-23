// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package task is the single source of truth for the unified Task entity
// (Sprint 2, Tier 2). It replaces the two legacy stores — pkg/boardtask (GTD
// board) and pkg/taskstore (workflow queue) — with one per-entity JSON store
// under ~/.omnipus/tasks/<id>.json.
//
// There is no back-compat (remediation Detail #7): one entity, one 6-state
// status vocabulary (ADR-051 D5 removed `planning`), one create/update path.
// The store carries over from
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

// Status is the unified 6-state task lifecycle (remediation Detail #1;
// ADR-051 D5 removed the redundant `planning` status now that Plans are
// first-class — existing `planning` tasks are backfilled to `next`, see
// MigratePlanningStatusToNext in migrate_planning_status.go).
//
//	inbox → next → in_progress → done   (+ failed)
//
// with blocked an auto side-state (set when a dependency is unmet, cleared to
// next when every blocked_by dep reaches done).
type Status string

// The six canonical task statuses. These are the ONLY valid Status values.
const (
	StatusInbox      Status = "inbox"       // captured / untriaged
	StatusNext       Status = "next"        // triaged & ready to start
	StatusInProgress Status = "in_progress" // worked by a human OR agent
	StatusBlocked    Status = "blocked"     // auto side-state: unmet dependency
	StatusDone       Status = "done"        // terminal success
	StatusFailed     Status = "failed"      // terminal failure
)

// validStatuses is the set of allowed Status values.
var validStatuses = map[Status]bool{ //nolint:gochecknoglobals
	StatusInbox:      true,
	StatusNext:       true,
	StatusInProgress: true,
	StatusBlocked:    true,
	StatusDone:       true,
	StatusFailed:     true,
}

// IsValidStatus reports whether s is one of the six canonical statuses.
func IsValidStatus(s Status) bool { return validStatuses[s] }

// IsTerminal reports whether s is a terminal status (done or failed).
func IsTerminal(s Status) bool { return s == StatusDone || s == StatusFailed }

// CancelReason discriminates why a Status=failed task was cancelled (ADR-052
// FR-028, mirrors plan.Plan.FailedReason — pkg/plan/plan.go:148). It is set
// only when Status == StatusFailed; empty for every other failure path (e.g.
// attempt-limit exhaustion leaves it empty) and for every non-failed status.
// A restart/re-run clears it (ADR-052 FR-028: "a re-run task is no longer
// 'stopped by user'"; mirrors FR-016's Plan.FailedReason clear on restart).
type CancelReason string

// CancelReasonStoppedByUser is the only cancel reason today: a user-initiated
// Stop (handleTaskStop, pkg/gateway/rest_tasks.go). The type mirrors
// plan.FailedReason's shape so future genuine task-level failure reasons
// (were one ever needed) slot in the same way.
const CancelReasonStoppedByUser CancelReason = "stopped_by_user"

// validCancelReasons is the set of allowed non-empty CancelReason values.
var validCancelReasons = map[CancelReason]bool{ //nolint:gochecknoglobals
	CancelReasonStoppedByUser: true,
}

// IsValidCancelReason reports whether r is a known, explicit cancel reason.
func IsValidCancelReason(r CancelReason) bool { return validCancelReasons[r] }

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
		if s := TodoStatus(raw.Status); IsValidTodoStatus(s) {
			td.Status = s
		} else {
			td.Status = TodoPending
		}
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
	// Scratchpad marks a task created exclusively by the set_todos facade. It is a
	// DISK-ONLY discriminator: the REST mapper (toWireTask) does NOT copy it to the
	// wire type, so it is invisible to the SPA. It lets set_todos distinguish its
	// own ephemeral tracking cards from real create_task tasks even when they share
	// the same Title, preventing the hijack bug where set_todos overwrites a real
	// user task's checklist.
	Scratchpad bool   `json:"scratchpad,omitempty"`
	Action     Action `json:"action"`
	Status     Status `json:"status"`
	// CancelReason is set on a Status=failed task cancelled via a user Stop
	// (ADR-052 FR-028); empty for a genuine failure (e.g. attempt-limit
	// exhaustion) and for every non-failed status. Cleared on restart/re-run.
	CancelReason CancelReason `json:"cancel_reason,omitempty"`
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
	// Tags are workspace-scoped, normalized (lowercase+trim, deduped) free-form
	// labels (ADR-049 D1, ≤16 tags, each ≤64 runes). Replaces the removed
	// MilestoneID grouping; the milestone migration seeds a `milestone:<name>`
	// tag onto member tasks (migrate_milestones.go).
	Tags []string `json:"tags,omitempty"`
	// PlanID is the optional Plan (pkg/plan) this task belongs to (ADR-049
	// D1/D4). Same-workspace FK — validated at the tool/gateway layer, not the
	// store (the store accepts and persists whatever value it is given).
	PlanID string `json:"plan_id,omitempty"`
	// WriteSet is the set of concrete paths this PLAN MEMBER task creates or
	// edits (ADR-053 §Contract Surface, US-11/G-16/FR-156). plan-lint
	// (pkg/plan.Lint) reads this at approve to reject overlapping parallel
	// streams. Empty for an exploratory member whose write footprint is
	// unknowable up front (D10) — it is exempt from the overlap check and
	// instead runs in its own isolated checkout at runtime (Phase 2, out of
	// pkg/plan's scope). Meaningful only when PlanID is set; ignored on a
	// standalone task.
	WriteSet []string `json:"write_set,omitempty"`
	// Stream is the parallel-group id this plan member belongs to (ADR-053
	// §Contract Surface, US-11/g4). Informational/UI grouping only —
	// pkg/plan.Lint's actual disjointness/join check is topological
	// (BlockedBy ordering), not keyed off this field. Absent for a member
	// not part of a parallel decomposition.
	Stream string `json:"stream,omitempty"`
	// IsJoin marks this plan member as an authored join/assemble member
	// with its own acceptance criteria, converging one or more parallel
	// streams into a single artifact (ADR-053 §Contract Surface, FR-159, g5
	// shard+assemble topology). plan-lint (pkg/plan.Lint) rejects a
	// convergence point (a member depending on >=2 mutually-parallel
	// predecessors) whose IsJoin is false, or true but with zero Criteria.
	IsJoin bool `json:"is_join,omitempty"`
	// Criteria are the task's acceptance criteria / Definition of Done
	// (ADR-049 D2/D5, FR-3). Agent-created tasks require at least one (enforced
	// at the tool layer); human/UI creation may leave this empty (SD-A7).
	Criteria []AcceptanceCriterion `json:"criteria,omitempty"`
	// AttemptCount is the current run's attempt index within its goal loop
	// (ADR-049 D7/R4/C17). Read-only, server-set by the runtime engine — never
	// a client-writable field (no Patch.AttemptCount).
	AttemptCount int `json:"attempt_count,omitempty"`
	// MaxAttempts is a per-task override of the attempt ceiling before the goal
	// loop wakes the owner (ADR-049 D7/R4/C18). nil inherits the global
	// PlanningConfig.TaskMaxAttempts default.
	MaxAttempts *int `json:"max_attempts,omitempty"`
	// ResumeFromCommit is the gitevidence boundary-commit hash a Play-resumed
	// plan member should continue from (ADR-053 D13/G-12, FR-144). Set by the
	// plan engine's recordMemberResumePoint when Play re-dispatches a
	// failed/cancelled member: the last boundary commit for this member, or ""
	// for a fresh attempt (no commit / nested-repo degrade / no resolver
	// wired). The worker turn and the plan Judge read it as the resume
	// baseline — the next attempt's diff is measured from this hash, so prior
	// committed progress counts as the starting point rather than attempt 0.
	// Server-set only (the runtime engine writes it during Play); never
	// accepted from the REST/PATCH wire surface.
	ResumeFromCommit string `json:"resume_from_commit,omitempty"`
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
	// per-workspace delegation-graph edge's depth gate (and the global subturn
	// ceiling) bounds onward delegation, and task_create rejects a create that
	// would exceed maxTaskDepth. This is an internal recursion-bound counter —
	// it is NOT part of the gen.Task wire contract and never crosses the
	// gateway/SPA boundary (the REST task mapper does not copy it).
	DelegationDepth int `json:"delegation_depth,omitempty"`
	// PendingJudgeClaim holds a worker's explicit update_task(status:"done")
	// completion summary when the task HAS acceptance criteria (ADR-049
	// C1/SD-B2, review r1): the tool layer (pkg/tools/task.go) does NOT write
	// a terminal `done` status for that case — it stages the claim here
	// instead, and pkg/agent/task_executor.go's finishTaskRun adjudicates it
	// through the SAME evidence-ladder judge path (adjudicateClaim) a
	// TASK_STATUS completion marker uses, closing the self-certification
	// bypass where an explicit tool call skipped the judge entirely. Cleared
	// once adjudicated. DISK-ONLY (mirrors Scratchpad/DelegationDepth): the
	// REST mapper (toWireTask) does NOT copy it to the wire type.
	PendingJudgeClaim string `json:"pending_judge_claim,omitempty"`
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
