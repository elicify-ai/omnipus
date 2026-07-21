// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adhocore/gronx"
	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// ErrNotFound is returned when a task ID does not exist on disk.
var ErrNotFound = errors.New("task not found")

// ErrValidation is the sentinel wrapped by every user-facing field/transition
// validation error the store returns (vs. an internal I/O failure). The REST
// seam uses errors.Is(err, ErrValidation) to map to HTTP 400 rather than
// matching error substrings. Cycle/self-edge/depth sentinels (blocked_by.go)
// also wrap ErrValidation.
var ErrValidation = errors.New("task validation")

// ErrIllegalTransition is returned when a status PATCH requests a transition the
// lifecycle does not allow (e.g. done→inbox, or a client-supplied `blocked`).
// It wraps ErrValidation so the REST seam maps it to HTTP 400.
var ErrIllegalTransition = fmt.Errorf("%w: illegal status transition", ErrValidation)

// ErrBlockedNotSettable is returned when a client tries to set status=blocked
// directly. `blocked` is a derived side-state: the store sets it when a
// dependency is unmet and clears it to `next` when every blocker reaches done.
// It wraps ErrValidation so the REST seam maps it to HTTP 400.
var ErrBlockedNotSettable = fmt.Errorf(
	"%w: status %q is a derived side-state and cannot be set directly",
	ErrValidation,
	StatusBlocked,
)

// verr wraps a formatted message as a user-facing validation error (ErrValidation).
func verr(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrValidation}, args...)...)
}

// Store manages per-entity JSON task files under a single directory
// (~/.omnipus/tasks/). It is the unified task store. All read-modify-write
// paths are serialized by the process-wide TaskFileLock keyed by task ID, plus
// an advisory flock on the file itself.
type Store struct {
	dir  string
	lock *StripedLock
}

// New creates a Store rooted at dir using the process-wide TaskFileLock. dir
// is, by universal convention across every call site in this codebase,
// "<home>/tasks" — New derives home from it to run the one-way Milestone→Tag
// migration (ADR-049 D1, migrate_milestones.go) and the `planning`→`next` task
// status backfill (ADR-051 D5, migrate_planning_status.go) exactly once each,
// guarded by their own completion sentinels, before the store is used. New's
// signature cannot itself return an error without a breaking change rippling
// across ~30 call sites in other packages, so a migration failure is logged at
// Error and does NOT prevent the Store from being constructed — each
// migration is safe to retry on the next call/boot (idempotent, crash-safe).
func New(dir string) *Store {
	home := filepath.Dir(dir)
	if err := MigrateMilestonesToTags(home); err != nil {
		slog.Error("task: milestone migration failed", "error", err)
	}
	if err := MigratePlanningStatusToNext(home); err != nil {
		slog.Error("task: planning-status migration failed", "error", err)
	}
	return &Store{dir: dir, lock: TaskFileLock}
}

// Dir returns the store's task directory.
func (s *Store) Dir() string { return s.dir }

// validateID rejects IDs containing path separators, "..", or null bytes.
func validateID(id string) error {
	if id == "" {
		return fmt.Errorf("id must not be empty")
	}
	if strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") || strings.ContainsRune(id, 0) {
		return fmt.Errorf("invalid id %q", id)
	}
	return nil
}

// path returns the absolute path for a task file.
func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// load reads and parses a single task file. It never rewrites on read.
func (s *Store) load(id string) (*Task, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("task: read %q: %w", id, err)
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("task: parse %q: %w", id, err)
	}
	if t.ID == "" {
		t.ID = id
	}
	return &t, nil
}

// write persists a task atomically under the per-task lock with an advisory
// flock. Callers that perform a read-modify-write MUST already hold the
// per-task lock (Lock); write itself does not take it.
func (s *Store) write(t *Task) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("task: create dir: %w", err)
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("task: marshal %q: %w", t.ID, err)
	}
	p := s.path(t.ID)
	return fileutil.WithFlock(p, func() error {
		return fileutil.WriteFileAtomic(p, data, 0o600)
	})
}

// Lock returns the per-task striped mutex for id. Callers performing a manual
// read-modify-write outside Create/Update should hold this for the whole RMW.
func (s *Store) Lock(id string) interface {
	Lock()
	Unlock()
} {
	return s.lock.Get(id)
}

// Filter narrows the result of List. All fields are optional (zero = skip).
type Filter struct {
	WorkspaceID  string
	Status       Status
	AgentID      string
	CreatedBy    string
	PlanID       string
	Surface      Surface
	ParentTaskID string
	// ParentTaskIDSet, when true, applies the ParentTaskID filter even when
	// ParentTaskID is empty — i.e. "only top-level tasks" (no parent).
	ParentTaskIDSet bool
	// BlockedByID, when non-empty, returns only tasks whose BlockedBy contains
	// the given ID.
	BlockedByID string
	// Tag, when non-empty, returns only tasks whose (normalized) Tags contain
	// this exact tag.
	Tag string
}

// matches reports whether t passes every active filter field.
func (f Filter) matches(t *Task) bool {
	if f.WorkspaceID != "" && t.WorkspaceID != f.WorkspaceID {
		return false
	}
	if f.Status != "" && t.Status != f.Status {
		return false
	}
	if f.AgentID != "" && t.AgentID != f.AgentID {
		return false
	}
	if f.CreatedBy != "" && t.CreatedBy != f.CreatedBy {
		return false
	}
	if f.PlanID != "" && t.PlanID != f.PlanID {
		return false
	}
	if f.Tag != "" && !containsString(t.Tags, f.Tag) {
		return false
	}
	if f.Surface != "" && t.EffectiveSurface() != f.Surface {
		return false
	}
	if f.ParentTaskIDSet && t.ParentTaskID != f.ParentTaskID {
		return false
	}
	if !f.ParentTaskIDSet && f.ParentTaskID != "" && t.ParentTaskID != f.ParentTaskID {
		return false
	}
	if f.BlockedByID != "" && !containsString(t.BlockedBy, f.BlockedByID) {
		return false
	}
	return true
}

// scanTaskIDs returns the task IDs present in the store directory, derived
// from every regular *.json file's name (ReadDir → skip subdirectories and
// non-.json entries → trim the .json suffix). It is the shared "scan the task
// dir" idiom used by List and the blocked_by dependency-cascade scans
// (AdvanceBlockedDependents, cascadeDeleteEdges, DropOrphanEdges). It returns
// the raw os.ReadDir error as-is (including a not-exist error) so each caller
// keeps its own missing-dir handling and error-wrapping context.
func (s *Store) scanTaskIDs() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
	}
	return ids, nil
}

// List returns all tasks matching filter, sorted by priority ASC then
// created_at ASC. Unreadable/corrupt files are logged at Warn and skipped.
func (s *Store) List(filter Filter) ([]Task, error) {
	ids, err := s.scanTaskIDs()
	if err != nil {
		if os.IsNotExist(err) {
			return []Task{}, nil
		}
		return nil, fmt.Errorf("task: list dir: %w", err)
	}

	result := make([]Task, 0, len(ids))
	for _, id := range ids {
		t, err := s.load(id)
		if err != nil {
			slog.Warn("task: skip unreadable task file", "id", id, "error", err)
			continue
		}
		if !filter.matches(t) {
			continue
		}
		result = append(result, *t)
	}

	sort.Slice(result, func(i, j int) bool {
		pi, pj := result[i].EffectivePriority(), result[j].EffectivePriority()
		if pi != pj {
			return pi < pj
		}
		return result[i].CreatedAt < result[j].CreatedAt
	})
	return result, nil
}

// Get returns the task with the given id, or ErrNotFound if absent.
func (s *Store) Get(id string) (*Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	return s.load(id)
}

// normalize applies field defaults and validates the entity. It does NOT touch
// the filesystem (cycle checks are the caller's responsibility via the DAG
// validator). Returns a user-facing error on invalid input.
func (t *Task) normalize() error {
	if t.Title == "" {
		return verr("title is required")
	}
	if len([]rune(t.Title)) > 200 {
		return verr("title must be 200 characters or fewer")
	}
	if t.Action == "" {
		t.Action = ActionLLM
	}
	if !IsValidAction(t.Action) {
		return verr("invalid action %q (only %q in Tier 2)", t.Action, ActionLLM)
	}
	if t.Status == "" {
		t.Status = StatusInbox
	}
	if !IsValidStatus(t.Status) {
		return verr("invalid status %q", t.Status)
	}
	if t.CancelReason != "" {
		if !IsValidCancelReason(t.CancelReason) {
			return verr("invalid cancel_reason %q", t.CancelReason)
		}
		if t.Status != StatusFailed {
			return verr("cancel_reason is only valid when status is failed")
		}
	}
	if t.Priority != 0 && (t.Priority < 1 || t.Priority > 5) {
		return verr("priority must be between 1 and 5, got %d", t.Priority)
	}
	if t.WorkspaceID == "" {
		return verr("workspace_id is required")
	}
	if t.Surface == "" {
		t.Surface = SurfaceUser
	}
	if !IsValidSurface(t.Surface) {
		return verr("invalid surface %q", t.Surface)
	}
	if len(t.Description) > 2000 {
		return verr("description must be 2000 characters or fewer")
	}
	if len(t.Prompt) > 10000 {
		return verr("prompt must be 10000 characters or fewer")
	}
	if len(t.Result) > 50000 {
		return verr("result must be 50000 characters or fewer")
	}
	if err := validateTodos(t.Todos); err != nil {
		return err
	}
	if t.Trigger != nil {
		if err := ValidateTrigger(t.Trigger); err != nil {
			return err
		}
	}
	normalizedTags, err := normalizeTags(t.Tags)
	if err != nil {
		return err
	}
	t.Tags = normalizedTags
	if t.Criteria != nil {
		normalizedCriteria, err := normalizeCriteria(t.Criteria)
		if err != nil {
			return err
		}
		t.Criteria = normalizedCriteria
	}
	if t.MaxAttempts != nil && *t.MaxAttempts < 1 {
		return verr("max_attempts must be at least 1")
	}
	return nil
}

// maxTagRunes and maxTagsPerTask bound Task.Tags (spec Part A §B). Also used
// by the milestone migration (migrate_milestones.go) when sizing the
// generated "milestone:<name>" tag.
const (
	maxTagRunes    = 64
	maxTagsPerTask = 16
)

// normalizeTags normalizes (lowercase+trim), validates, and dedups tags, per
// the exact ordering documented in spec Part A §B: (1) normalize, (2) reject
// empty-after-trim, (3) reject >64 runes, (4) reject >16 tags — checked
// against the normalized-but-NOT-YET-deduped count (so 17 raw entries that
// would dedup down to fewer than 16 distinct tags are still rejected; this
// matches the spec's literal numbered order, which places the count check
// before dedup), (5) dedup case-fold collisions preserving first-seen order.
// A nil/empty input returns (nil, nil) — tags are optional.
func normalizeTags(tags []string) ([]string, error) {
	if len(tags) == 0 {
		return nil, nil
	}
	normalized := make([]string, len(tags))
	for i, raw := range tags {
		tag := strings.ToLower(strings.TrimSpace(raw))
		if tag == "" {
			return nil, verr("tags[%d]: tag must not be empty", i)
		}
		if n := len([]rune(tag)); n > maxTagRunes {
			return nil, verr("tags[%d]: tag must be %d characters or fewer", i, maxTagRunes)
		}
		normalized[i] = tag
	}
	if len(normalized) > maxTagsPerTask {
		return nil, verr("tags: at most %d tags per task", maxTagsPerTask)
	}
	seen := make(map[string]bool, len(normalized))
	out := make([]string, 0, len(normalized))
	for _, tag := range normalized {
		if seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out, nil
}

// validateTodos checks each todo's text length (1..500) and that status is a
// known tri-state value. The UnmarshalJSON default-to-pending is only for
// legacy disk reads; tool/REST input must supply a valid status explicitly.
func validateTodos(todos []Todo) error {
	for i, td := range todos {
		n := len([]rune(td.Text))
		if n < 1 {
			return verr("todo[%d]: text is required", i)
		}
		if n > 500 {
			return verr("todo[%d]: text must be 500 characters or fewer", i)
		}
		if !IsValidTodoStatus(td.Status) {
			return verr("todo[%d]: status %q is not valid (must be pending, in_progress, or completed)", i, td.Status)
		}
	}
	return nil
}

// minTriggerIntervalSeconds is the floor on a recurring/every trigger's
// effective interval (LOW-1). A trigger that would fire more often than once per
// minute is rejected to prevent a `* * * * * *` self-DoS, mirroring the
// every_ms >= 1000 intent at the cron layer.
const minTriggerIntervalSeconds = 60

// ValidateTrigger validates a trigger's type and the config required for that
// type (Detail #3). Empty/unset config keys for the wrong type are ignored. For
// a recurring trigger it parses config.cron_expr with the same cron library the
// trigger executor uses (gronx) and enforces a >= 60s effective interval
// (LOW-1) by computing the next two fires.
func ValidateTrigger(tr *Trigger) error {
	if !IsValidTriggerType(tr.Type) {
		return verr("invalid trigger type %q", tr.Type)
	}
	switch tr.Type {
	case TriggerOnce:
		if tr.Config.AtMs == nil {
			return verr("trigger %q requires config.at_ms", tr.Type)
		}
	case TriggerEvery:
		if tr.Config.EveryMs == nil {
			return verr("trigger %q requires config.every_ms", tr.Type)
		}
		if *tr.Config.EveryMs < 1000 {
			return verr("trigger %q config.every_ms must be at least 1000ms", tr.Type)
		}
	case TriggerRecurring:
		if tr.Config.CronExpr == nil || *tr.Config.CronExpr == "" {
			return verr("trigger %q requires config.cron_expr", tr.Type)
		}
		if err := validateCronExpr(*tr.Config.CronExpr); err != nil {
			return err
		}
	case TriggerManual:
		// no config required
	}
	return nil
}

// validateCronExpr rejects an unparseable cron expression and enforces the
// minTriggerIntervalSeconds floor by computing the next two fire instants from a
// fixed reference and rejecting when they are < 60s apart (LOW-1 self-DoS guard).
func validateCronExpr(expr string) error {
	if !gronx.IsValid(expr) {
		return verr("trigger config.cron_expr %q is not a valid cron expression", expr)
	}
	// Use a fixed reference instant so the floor check is deterministic. The
	// second field of a 6-field expr is the only way to fire sub-minute; a
	// 5-field expr can never fire more than once per minute.
	ref := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	first, err := gronx.NextTickAfter(expr, ref, false)
	if err != nil {
		return verr("trigger config.cron_expr %q could not be evaluated: %v", expr, err)
	}
	second, err := gronx.NextTickAfter(expr, first, false)
	if err != nil {
		return verr("trigger config.cron_expr %q could not be evaluated: %v", expr, err)
	}
	if second.Sub(first) < minTriggerIntervalSeconds*time.Second {
		return verr(
			"trigger config.cron_expr %q fires more often than once per %ds (self-DoS guard)",
			expr,
			minTriggerIntervalSeconds,
		)
	}
	return nil
}

// Create persists a new task. It generates a UUID when ID is empty, stamps
// CreatedAt/UpdatedAt, applies defaults, validates fields, and validates the
// blocked_by DAG (rejecting cycles). The new task always lands per its Status
// default (inbox) unless the caller set a different valid status.
//
// Create takes the per-task lock internally.
func (s *Store) Create(t *Task) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	if err := validateID(t.ID); err != nil {
		return err
	}
	if err := t.normalize(); err != nil {
		return err
	}

	mu := s.lock.Get(t.ID)
	mu.Lock()
	defer mu.Unlock()

	// Reject a blocked_by set that would create a cycle (or reference missing
	// tasks). The task is not yet on disk, so no inbound edges to it exist.
	if len(t.BlockedBy) > 0 {
		if err := s.validateBlockedByLocked(t.ID, t.BlockedBy); err != nil {
			return err
		}
	}
	// Reject a parent edge that would create a cycle in the parent chain.
	if t.ParentTaskID != "" {
		if err := s.checkParentAcyclicLocked(t.ID, t.ParentTaskID); err != nil {
			return err
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	t.CreatedAt = now
	t.UpdatedAt = now
	return s.write(t)
}

// Patch is a partial update applied by Update. Only non-nil fields are written.
type Patch struct {
	Title       *string
	Description *string
	Prompt      *string
	Status      *Status
	// CancelReason is the write path for Task.CancelReason (ADR-052 FR-028).
	// nil = unchanged; pointing at "" clears it; pointing at a valid
	// CancelReason value sets it (mirrors plan.Patch.FailedReason,
	// pkg/plan/store.go:259 — a single, non-double pointer, since "" is
	// itself a legal target value, not a "no clear" sentinel).
	CancelReason *CancelReason
	AgentID      *string
	Priority     *int
	BlockedBy    *[]string
	Todos        *[]Todo
	Trigger      **Trigger // double pointer: outer nil = unchanged, *outer nil = clear
	Due          *string
	PlanID       *string
	Tags         *[]string
	Criteria     *[]AcceptanceCriterion
	// MaxAttempts is a double pointer (mirrors Trigger): outer nil = unchanged,
	// *outer nil = clear the override (inherit the global PlanningConfig
	// default), *outer non-nil = set to that value.
	MaxAttempts **int
	// AttemptCount is the runtime goal-loop's own write path for
	// Task.AttemptCount (ADR-049 D7/R4/C17). Server-set only — the REST/PATCH
	// wire boundary must never accept this field from a client; only the
	// runtime engine (pkg/agent's TaskExecutor) writes it, once per attempt.
	AttemptCount  *int
	Surface       *Surface
	Result        *string
	Artifacts     *[]string
	SessionID     *string
	StartedAt     *string
	CompletedAt   *string
	SourceChannel *string
	SourceChatID  *string
	// PendingJudgeClaim is the runtime/tool-layer write path for
	// Task.PendingJudgeClaim (ADR-049 C1/SD-B2, review r1). A non-nil pointer
	// to "" clears it (adjudication finished, either outcome).
	PendingJudgeClaim *string

	// allowBlockedSet is the internal escape hatch that permits a Status patch to
	// set or clear the derived `blocked` side-state. It is NEVER set from the wire
	// (the REST/PATCH seam leaves it false); only the dependency-recompute paths
	// (recomputeBlockedStateLocked) set it. Unexported so callers outside this
	// package cannot bypass the ErrBlockedNotSettable guard.
	allowBlockedSet bool
}

// validateTransition reports whether moving from→to is a legal lifecycle
// transition (N1). The policy is deliberately permissive — the lifecycle is a
// workflow aid, not a strict state machine — and only forbids the transitions
// that would corrupt invariants:
//
//   - `done` is terminal and FROZEN: no transition out of `done` is permitted
//     (rejects the reviewer's example, done→inbox). A successful task is final;
//     re-running is done via a fresh trigger fire (SpawnReset), not a status
//     edit. (`failed` is NOT frozen — a failed task may be re-queued for retry.)
//   - `blocked` is a derived side-state: it is entered/left only through the
//     internal allowBlockedSet hatch (the dependency-recompute paths). A direct
//     wire move TO `blocked` is rejected upstream (ErrBlockedNotSettable);
//     leaving `blocked` other than via the hatch is rejected here.
//
// A no-op (from == to) is always allowed. When internal is true any transition
// into or out of `blocked` is permitted. Returns ErrIllegalTransition (wrapping
// ErrValidation) on rejection.
func validateTransition(from, to Status, internal bool) error {
	if from == to {
		return nil
	}
	if internal && (to == StatusBlocked || from == StatusBlocked) {
		return nil
	}
	// `done` is frozen — a completed task is final.
	if from == StatusDone {
		return fmt.Errorf("%w: %q → %q is not permitted (done is terminal)", ErrIllegalTransition, from, to)
	}
	// Leaving `blocked` is only legal via the internal recompute hatch.
	if from == StatusBlocked && !internal {
		return fmt.Errorf(
			"%w: %q → %q is not permitted (blocked clears automatically when dependencies complete)",
			ErrIllegalTransition,
			from,
			to,
		)
	}
	return nil
}

// Update applies patch to the task identified by id and persists the result.
// It validates field constraints and the blocked_by DAG (when blocked_by is
// changed). It takes the per-task lock internally.
func (s *Store) Update(id string, patch Patch) (*Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	mu := s.lock.Get(id)
	mu.Lock()
	defer mu.Unlock()
	return s.updateLocked(id, patch)
}

// updateLocked is the body of Update; the caller must hold the per-task lock.
func (s *Store) updateLocked(id string, patch Patch) (*Task, error) {
	t, err := s.load(id)
	if err != nil {
		return nil, err
	}

	if patch.Title != nil {
		if *patch.Title == "" {
			return nil, verr("title must not be empty")
		}
		if len([]rune(*patch.Title)) > 200 {
			return nil, verr("title must be 200 characters or fewer")
		}
		t.Title = *patch.Title
	}
	if patch.Description != nil {
		if len(*patch.Description) > 2000 {
			return nil, verr("description must be 2000 characters or fewer")
		}
		t.Description = *patch.Description
	}
	if patch.Prompt != nil {
		if len(*patch.Prompt) > 10000 {
			return nil, verr("prompt must be 10000 characters or fewer")
		}
		t.Prompt = *patch.Prompt
	}
	if patch.Status != nil {
		if !IsValidStatus(*patch.Status) {
			return nil, verr("invalid status %q", *patch.Status)
		}
		// `blocked` is a derived side-state — it is never settable through the
		// public update path. The store sets it when a dependency is unmet and
		// clears it to `next` when every blocker reaches done. allowBlockedSet is
		// the internal escape hatch used by the dependency-recompute paths only.
		if *patch.Status == StatusBlocked && !patch.allowBlockedSet {
			return nil, ErrBlockedNotSettable
		}
		// Reject illegal lifecycle transitions (N1). A no-op (same status) and any
		// transition out of the derived `blocked` state via the internal hatch are
		// always allowed.
		if err := validateTransition(t.Status, *patch.Status, patch.allowBlockedSet); err != nil {
			return nil, err
		}
		// A genuine transition INTO in_progress (not a same-status no-op) stamps
		// the task's real execution start, unless the caller already supplied an
		// explicit StartedAt in this same patch (which wins). This is the single
		// choke point for every Update-based path that flips a task to
		// in_progress: it closes the gap where the REST PATCH "Start" action
		// (rest_tasks.go's handleTaskPatch, which only sets status and then
		// hands off to TaskExecutor.StartTaskNow) left started_at permanently
		// empty because neither of those steps stamped it. The scheduler/
		// heartbeat dispatch path (TaskExecutor.ExecuteTask) does not go through
		// Update at all — it claims via ClaimForRun, which already stamps
		// StartedAt itself — so this is unaffected there. A retry (failed →
		// in_progress) re-stamps to the new attempt's start time, which is the
		// correct "most recent execution start" semantics.
		if *patch.Status == StatusInProgress && t.Status != StatusInProgress && patch.StartedAt == nil {
			t.StartedAt = time.Now().UTC().Format(time.RFC3339)
		}
		t.Status = *patch.Status
	}
	// Fix-wave finding #3 (run_task on a stopped task breaks): when THIS
	// patch moves Status OFF failed and the caller did NOT also touch
	// CancelReason in the same call, auto-clear the now-stale reason before
	// the merged cross-field check further down — mirrors RestartReset's
	// CancelReason clear on restart (a task no longer sitting at failed
	// cannot still be "stopped_by_user"). Without this, a run_task-shaped
	// patch that only sets Status (e.g. failed+stopped_by_user ->
	// in_progress, to resume a stopped task) would fail the cross-field
	// check purely because it forgot to also clear the now-irrelevant
	// reason. An explicit CancelReason supplied ALONGSIDE a non-failed
	// Status in the SAME patch is deliberately NOT auto-cleared here — it
	// falls through to patch.CancelReason's own handling below and is caught
	// by the merged check as before (explicit conflicting data is rejected,
	// never silently dropped; the merged check stays the backstop).
	if patch.Status != nil && *patch.Status != StatusFailed && patch.CancelReason == nil {
		t.CancelReason = ""
	}
	if patch.CancelReason != nil {
		if *patch.CancelReason != "" && !IsValidCancelReason(*patch.CancelReason) {
			return nil, verr("invalid cancel_reason %q", *patch.CancelReason)
		}
		t.CancelReason = *patch.CancelReason
	}
	if patch.AgentID != nil {
		t.AgentID = *patch.AgentID
	}
	if patch.Priority != nil {
		if *patch.Priority < 1 || *patch.Priority > 5 {
			return nil, verr("priority must be between 1 and 5, got %d", *patch.Priority)
		}
		t.Priority = *patch.Priority
	}
	if patch.BlockedBy != nil {
		newDeps := *patch.BlockedBy
		if len(newDeps) > 0 {
			if err := s.validateBlockedByLocked(t.ID, newDeps); err != nil {
				return nil, err
			}
		}
		t.BlockedBy = newDeps
		if len(t.BlockedBy) == 0 {
			t.BlockedBy = nil
		}
		// Recompute the derived `blocked` side-state from the new blocked_by set,
		// mirroring AddDependency. Without this, a `next` task that gains an
		// unmet blocker stays `next` (dispatchable), violating the store's own
		// contract that `blocked` reflects an unmet dependency. recompute...Locked
		// is a no-op for statuses other than `next`/`blocked`, so terminal and
		// in_progress tasks are unaffected.
		s.recomputeBlockedStateLocked(t)
	}
	if patch.Todos != nil {
		if err := validateTodos(*patch.Todos); err != nil {
			return nil, err
		}
		t.Todos = *patch.Todos
		if len(t.Todos) == 0 {
			t.Todos = nil
		}
	}
	if patch.Trigger != nil {
		newTrigger := *patch.Trigger
		if newTrigger != nil {
			if err := ValidateTrigger(newTrigger); err != nil {
				return nil, err
			}
		}
		t.Trigger = newTrigger
	}
	if patch.Due != nil {
		t.Due = *patch.Due
	}
	if patch.PlanID != nil {
		t.PlanID = *patch.PlanID
	}
	if patch.Tags != nil {
		normalizedTags, err := normalizeTags(*patch.Tags)
		if err != nil {
			return nil, err
		}
		t.Tags = normalizedTags
	}
	if patch.Criteria != nil {
		normalizedCriteria, err := normalizeCriteria(*patch.Criteria)
		if err != nil {
			return nil, err
		}
		t.Criteria = normalizedCriteria
		if len(t.Criteria) == 0 {
			t.Criteria = nil
		}
	}
	if patch.MaxAttempts != nil {
		newMax := *patch.MaxAttempts
		if newMax != nil && *newMax < 1 {
			return nil, verr("max_attempts must be at least 1")
		}
		t.MaxAttempts = newMax
	}
	if patch.AttemptCount != nil {
		if *patch.AttemptCount < 0 {
			return nil, verr("attempt_count must not be negative")
		}
		t.AttemptCount = *patch.AttemptCount
	}
	if patch.Surface != nil {
		if !IsValidSurface(*patch.Surface) {
			return nil, verr("invalid surface %q", *patch.Surface)
		}
		t.Surface = *patch.Surface
	}
	if patch.Result != nil {
		if len(*patch.Result) > 50000 {
			return nil, verr("result must be 50000 characters or fewer")
		}
		t.Result = *patch.Result
	}
	if patch.Artifacts != nil {
		t.Artifacts = *patch.Artifacts
		if len(t.Artifacts) == 0 {
			t.Artifacts = nil
		}
	}
	if patch.SessionID != nil {
		t.SessionID = *patch.SessionID
	}
	if patch.StartedAt != nil {
		t.StartedAt = *patch.StartedAt
	}
	if patch.CompletedAt != nil {
		t.CompletedAt = *patch.CompletedAt
	}
	if patch.SourceChannel != nil {
		t.SourceChannel = *patch.SourceChannel
	}
	if patch.SourceChatID != nil {
		t.SourceChatID = *patch.SourceChatID
	}
	if patch.PendingJudgeClaim != nil {
		t.PendingJudgeClaim = *patch.PendingJudgeClaim
	}

	// Cross-field invariant (ADR-052 FR-028, mirrors plan.Plan's normalize()-
	// enforced FailedReason/State coupling — pkg/plan/plan.go:299-306):
	// CancelReason is only meaningful on a failed task. Checked here against
	// the FULLY merged t.Status/t.CancelReason regardless of which patch
	// field(s) were actually touched this call (unlike plan's Update, this
	// store does not re-run the whole normalize() on every patch — see
	// normalize()'s own per-field checks for the Create-time equivalent), so
	// e.g. a caller that patches Status away from failed without also
	// clearing CancelReason in the same call is rejected rather than landing
	// an inconsistent record on disk.
	if t.CancelReason != "" && t.Status != StatusFailed {
		return nil, verr("cancel_reason is only valid when status is failed")
	}

	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.write(t); err != nil {
		return nil, err
	}
	return t, nil
}

// ErrNotRestartable is returned by RestartReset when the task is not
// currently `failed` — restart (ADR-052 FR-026/028) only applies to a failed
// task, for ANY failure reason (a genuine attempt-limit exhaustion or a user
// cancel alike — task-level `failed→next` is un-frozen unconditionally,
// unlike the plan-level restart guard which is reason-gated). A task in any
// other status has nothing to restart. Control-flow sentinel (mirrors
// ErrAlreadyClaimed/ErrAlreadyRunning), NOT a wire validation error — the
// restart handler maps it to its own HTTP status (spec FR-026: 409).
var ErrNotRestartable = errors.New("task: not restartable: task is not failed")

// RestartReset atomically resets a failed task to a fresh runnable state for
// a plan/task restart (ADR-052 FR-016/017/028) in a single write: it resets
// AttemptCount to 0 (a restarted task's goal loop starts its attempt count
// over — the "attempt_count reset primitive" restart needs), clears
// CancelReason (mirrors the plan's FailedReason clear on restart — a re-run
// task is no longer "stopped by user"), and clears the previous run's
// session/result/artifacts/timestamps — the same "fresh" field set
// SpawnReset clears for a trigger re-fire, reached here via a distinct path
// because SpawnReset (a) does not touch AttemptCount/CancelReason and (b) is
// scoped to a `next` re-fire, not a restart of a `failed` task. The task
// lands on `next`, or on the derived `blocked` side-state if a blocked_by
// dependency is not yet `done` — the same dependency recompute Update applies
// when BlockedBy is patched (DS-5: restart resets a member to "next/blocked").
//
// Returns ErrNotRestartable when the task is not currently `failed`. Takes
// the per-task lock once internally.
func (s *Store) RestartReset(id string) (*Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	mu := s.lock.Get(id)
	mu.Lock()
	defer mu.Unlock()

	t, err := s.load(id)
	if err != nil {
		return nil, err
	}
	if t.Status != StatusFailed {
		return nil, fmt.Errorf("%w: task %q is %q", ErrNotRestartable, id, t.Status)
	}
	t.Status = StatusNext
	t.CancelReason = ""
	t.AttemptCount = 0
	t.Result = ""
	t.Artifacts = nil
	t.SessionID = ""
	t.StartedAt = ""
	t.CompletedAt = ""
	t.FollowedUp = false
	t.PendingJudgeClaim = ""
	s.recomputeBlockedStateLocked(t)
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.write(t); err != nil {
		return nil, err
	}
	return t, nil
}

// ErrStandaloneRestartNotPermitted is returned by ValidateStandaloneRestart
// when a standalone-task restart (POST /tasks/{id}/restart, ADR-052 §6.7/
// §6.8, spec FR-026) is requested on a task that is not, right now,
// Status==failed with CancelReason==stopped_by_user. It wraps
// ErrIllegalTransition (and therefore ErrValidation), mirroring the shape of
// plan.ErrRestartNotPermitted (pkg/plan/plan.go), so any existing
// errors.Is(err, ErrValidation) 400-mapping still holds; callers that need to
// distinguish "not restartable" (spec FR-026: HTTP 409 — wrong status or
// wrong reason) from a malformed request (400) should check
// errors.Is(err, ErrStandaloneRestartNotPermitted) specifically.
var ErrStandaloneRestartNotPermitted = fmt.Errorf(
	"%w: restart (failed -> next) is only permitted from failed(stopped_by_user)",
	ErrIllegalTransition,
)

// ValidateStandaloneRestart reports whether a STANDALONE-task RESTART
// (POST /tasks/{id}/restart -> RestartReset, ADR-052 FR-026, the ▶ Play
// route for a task the user previously Stopped) is legal, given the task's
// CURRENT status and cancel reason. This is the pkg/task mirror of
// plan.ValidateRestartTransition (pkg/plan/plan.go) — a single, independently
// table-testable source of truth for the gate, replacing the equivalent
// inline check that previously lived only in the REST handler
// (rest_tasks.go's handleTaskRestart).
//
// Restart is legal ONLY when status == StatusFailed AND
// cancelReason == CancelReasonStoppedByUser (a user Stop, not a genuine
// failure). This is DELIBERATELY narrower than RestartReset itself, which
// stays reason-agnostic on purpose: RestartReset is also the reset primitive
// the PLAN-member restart path uses (FR-016/FR-017), and that path
// un-freezes a failed member for ANY failure reason. This gate applies ONLY
// to the standalone-task restart route — it is a caller-side precondition on
// RestartReset, not a change to RestartReset's own contract.
//
// The sole caller today is handleTaskRestart (rest_tasks.go), which
// continues to own the specific, actionable 409 message returned to the
// client; this helper's error is available via errors.Is for that mapping.
func ValidateStandaloneRestart(status Status, cancelReason CancelReason) error {
	if status != StatusFailed {
		return fmt.Errorf(
			"%w: restart is only valid from status %q, got %q",
			ErrStandaloneRestartNotPermitted, StatusFailed, status,
		)
	}
	if cancelReason != CancelReasonStoppedByUser {
		return fmt.Errorf(
			"%w: got cancel_reason %q, want %q",
			ErrStandaloneRestartNotPermitted, cancelReason, CancelReasonStoppedByUser,
		)
	}
	return nil
}

// Delete removes the task file for id and cascade-cleans inbound blocked_by
// edges (every other task that depended on id loses that edge). It returns the
// IDs of tasks that became fully unblocked (their blocked_by list emptied).
func (s *Store) Delete(id string) (unblockedIDs []string, err error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	mu := s.lock.Get(id)
	mu.Lock()
	rmErr := os.Remove(s.path(id))
	mu.Unlock()
	if rmErr != nil {
		if os.IsNotExist(rmErr) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("task: delete %q: %w", id, rmErr)
	}
	// Cascade: remove the task's evidence directory (FR-023, SD-A10). The
	// task's own file is already gone at this point; a failure here is
	// best-effort/logged, not fatal to the delete (mirrors the cascade-edge
	// cleanup failure handling below).
	evidenceDir := filepath.Join(evidenceDirForTaskStoreDir(s.dir), id)
	if rmEvErr := os.RemoveAll(evidenceDir); rmEvErr != nil {
		slog.Warn("task: delete: could not remove evidence dir", "id", id, "dir", evidenceDir, "error", rmEvErr)
	}
	return s.cascadeDeleteEdges(id)
}

// AppendTodo atomically appends a checklist item to the task's embedded todos
// under the per-task lock taken ONCE internally (load + mutate + validate +
// write). It must NOT be called while already holding the per-task lock — the
// store mutex is non-reentrant. Returns the updated task. This is the atomic
// append primitive (the full-replace counterpart is SetTodos, the primary path
// for the set_todos scratchpad tool); both avoid the Lock()+Update()
// re-entrancy deadlock.
func (s *Store) AppendTodo(id string, td Todo) (*Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	mu := s.lock.Get(id)
	mu.Lock()
	defer mu.Unlock()

	t, err := s.load(id)
	if err != nil {
		return nil, err
	}
	newTodos := append(append([]Todo{}, t.Todos...), td)
	if err := validateTodos(newTodos); err != nil {
		return nil, err
	}
	t.Todos = newTodos
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.write(t); err != nil {
		return nil, err
	}
	return t, nil
}

// SetTodos replaces the task's entire checklist (full-replace, idempotent).
// It validates the todos, then atomically writes. This is the primary path
// for the set_todos scratchpad tool (replaces append-only AppendTodo).
func (s *Store) SetTodos(id string, todos []Todo) (*Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	return s.Update(id, Patch{Todos: &todos})
}

// AddDependency atomically adds a single blocked_by edge (id depends on
// blockerID) under the per-task lock taken ONCE internally. It is idempotent —
// re-adding an existing edge is a no-op success. It validates the new edge set
// against the DAG (self-edge / missing / cycle / depth) before persisting, and
// recomputes the derived `blocked` side-state. Must NOT be called while holding
// the per-task lock. Returns the updated task and whether the edge was newly
// added.
func (s *Store) AddDependency(id, blockerID string) (updated *Task, added bool, err error) {
	if idErr := validateID(id); idErr != nil {
		return nil, false, idErr
	}
	if idErr := validateID(blockerID); idErr != nil {
		return nil, false, verr("blocked_by contains invalid ID %q", blockerID)
	}
	mu := s.lock.Get(id)
	mu.Lock()
	defer mu.Unlock()

	t, err := s.load(id)
	if err != nil {
		return nil, false, err
	}
	if containsString(t.BlockedBy, blockerID) {
		return t, false, nil
	}
	newDeps := append(append([]string{}, t.BlockedBy...), blockerID)
	if err := s.validateBlockedByLocked(t.ID, newDeps); err != nil {
		return nil, false, err
	}
	t.BlockedBy = newDeps
	s.recomputeBlockedStateLocked(t)
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.write(t); err != nil {
		return nil, false, err
	}
	return t, true, nil
}

// recomputeBlockedStateLocked derives the `blocked` side-state from the task's
// current blocked_by set. A `next` task with at least one not-`done` blocker
// becomes `blocked`; a `blocked` task whose blockers are now all `done` (or
// empty) returns to `next`. It mutates t in place and never touches a task that
// is past dispatch (in_progress/terminal). The caller holds the
// per-task lock for t.ID; this reads OTHER task files (the blockers) without
// their locks, which is safe because it only reads their Status.
func (s *Store) recomputeBlockedStateLocked(t *Task) {
	if t.Status != StatusNext && t.Status != StatusBlocked {
		return
	}
	anyUnmet := false
	for _, depID := range t.BlockedBy {
		dep, derr := s.load(depID)
		if derr != nil || dep.Status != StatusDone {
			anyUnmet = true
			break
		}
	}
	switch {
	case anyUnmet && t.Status == StatusNext:
		t.Status = StatusBlocked
	case !anyUnmet && t.Status == StatusBlocked:
		t.Status = StatusNext
	}
}

// SpawnReset atomically resets a task to a fresh runnable state for a trigger
// re-fire: it clears the previous run's session/result/artifacts/timestamps and
// moves the task to `next` so the executor can claim it. It returns
// ErrAlreadyRunning when the task is already `in_progress` (a trigger fire must
// not stomp an in-flight run — the overlap guard). Used by the trigger executor
// to spawn a fresh run for once/every/recurring triggers. Takes the per-task
// lock once internally.
func (s *Store) SpawnReset(id string) (*Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	mu := s.lock.Get(id)
	mu.Lock()
	defer mu.Unlock()

	t, err := s.load(id)
	if err != nil {
		return nil, err
	}
	if t.Status == StatusInProgress {
		return nil, ErrAlreadyRunning
	}
	t.Status = StatusNext
	t.Result = ""
	t.Artifacts = nil
	t.SessionID = ""
	t.StartedAt = ""
	t.CompletedAt = ""
	t.FollowedUp = false
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.write(t); err != nil {
		return nil, err
	}
	return t, nil
}

// AdvanceUnblocked transitions a task from the derived `blocked` side-state to
// `next` once its dependencies are satisfied (e.g. after a blocker was deleted).
// It is the exported, hatch-using counterpart the REST/tool delete paths call —
// a plain Update(Status: next) from `blocked` is rejected by the transition
// guard because `blocked` is cleared only via this internal path. It is a no-op
// success when the task is not currently `blocked`. Takes the per-task lock once.
func (s *Store) AdvanceUnblocked(id string) (*Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	mu := s.lock.Get(id)
	mu.Lock()
	defer mu.Unlock()

	t, err := s.load(id)
	if err != nil {
		return nil, err
	}
	if t.Status != StatusBlocked {
		return t, nil
	}
	next := StatusNext
	return s.updateLocked(id, Patch{Status: &next, allowBlockedSet: true})
}

// Exists reports whether a task with id exists on disk.
func (s *Store) Exists(id string) bool {
	if validateID(id) != nil {
		return false
	}
	_, err := os.Stat(s.path(id))
	return err == nil
}

// containsString reports whether slice contains v.
func containsString(slice []string, v string) bool {
	for _, x := range slice {
		if x == v {
			return true
		}
	}
	return false
}
