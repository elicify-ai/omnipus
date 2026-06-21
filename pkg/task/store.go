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

	"github.com/google/uuid"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

// ErrNotFound is returned when a task ID does not exist on disk.
var ErrNotFound = errors.New("task not found")

// Store manages per-entity JSON task files under a single directory
// (~/.omnipus/tasks/). It is the unified task store. All read-modify-write
// paths are serialised by the process-wide TaskFileLock keyed by task ID, plus
// an advisory flock on the file itself.
type Store struct {
	dir  string
	lock *StripedLock
}

// New creates a Store rooted at dir using the process-wide TaskFileLock.
func New(dir string) *Store {
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
	MilestoneID  string
	Surface      Surface
	ParentTaskID string
	// ParentTaskIDSet, when true, applies the ParentTaskID filter even when
	// ParentTaskID is empty — i.e. "only top-level tasks" (no parent).
	ParentTaskIDSet bool
	// BlockedByID, when non-empty, returns only tasks whose BlockedBy contains
	// the given ID.
	BlockedByID string
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
	if f.MilestoneID != "" && t.MilestoneID != f.MilestoneID {
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

// List returns all tasks matching filter, sorted by priority ASC then
// created_at ASC. Unreadable/corrupt files are logged at Warn and skipped.
func (s *Store) List(filter Filter) ([]Task, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Task{}, nil
		}
		return nil, fmt.Errorf("task: list dir: %w", err)
	}

	result := make([]Task, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
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
		return fmt.Errorf("title is required")
	}
	if len([]rune(t.Title)) > 200 {
		return fmt.Errorf("title must be 200 characters or fewer")
	}
	if t.Action == "" {
		t.Action = ActionLLM
	}
	if !IsValidAction(t.Action) {
		return fmt.Errorf("invalid action %q (only %q in Tier 2)", t.Action, ActionLLM)
	}
	if t.Status == "" {
		t.Status = StatusInbox
	}
	if !IsValidStatus(t.Status) {
		return fmt.Errorf("invalid status %q", t.Status)
	}
	if t.Priority != 0 && (t.Priority < 1 || t.Priority > 5) {
		return fmt.Errorf("priority must be between 1 and 5, got %d", t.Priority)
	}
	if t.WorkspaceID == "" {
		return fmt.Errorf("workspace_id is required")
	}
	if t.Surface == "" {
		t.Surface = SurfaceUser
	}
	if !IsValidSurface(t.Surface) {
		return fmt.Errorf("invalid surface %q", t.Surface)
	}
	if len(t.Description) > 2000 {
		return fmt.Errorf("description must be 2000 characters or fewer")
	}
	if len(t.Prompt) > 10000 {
		return fmt.Errorf("prompt must be 10000 characters or fewer")
	}
	if len(t.Result) > 50000 {
		return fmt.Errorf("result must be 50000 characters or fewer")
	}
	if err := validateTodos(t.Todos); err != nil {
		return err
	}
	if t.Trigger != nil {
		if err := ValidateTrigger(t.Trigger); err != nil {
			return err
		}
	}
	return nil
}

// validateTodos checks each todo's text length (1..500).
func validateTodos(todos []Todo) error {
	for i, td := range todos {
		n := len([]rune(td.Text))
		if n < 1 {
			return fmt.Errorf("todo[%d]: text is required", i)
		}
		if n > 500 {
			return fmt.Errorf("todo[%d]: text must be 500 characters or fewer", i)
		}
	}
	return nil
}

// ValidateTrigger validates a trigger's type and the config required for that
// type (Detail #3). Empty/unset config keys for the wrong type are ignored.
func ValidateTrigger(tr *Trigger) error {
	if !IsValidTriggerType(tr.Type) {
		return fmt.Errorf("invalid trigger type %q", tr.Type)
	}
	switch tr.Type {
	case TriggerOnce:
		if tr.Config.AtMs == nil {
			return fmt.Errorf("trigger %q requires config.at_ms", tr.Type)
		}
	case TriggerEvery:
		if tr.Config.EveryMs == nil {
			return fmt.Errorf("trigger %q requires config.every_ms", tr.Type)
		}
		if *tr.Config.EveryMs < 1000 {
			return fmt.Errorf("trigger %q config.every_ms must be at least 1000ms", tr.Type)
		}
	case TriggerRecurring:
		if tr.Config.CronExpr == nil || *tr.Config.CronExpr == "" {
			return fmt.Errorf("trigger %q requires config.cron_expr", tr.Type)
		}
	case TriggerManual:
		// no config required
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
	Title         *string
	Description   *string
	Prompt        *string
	Status        *Status
	AgentID       *string
	Priority      *int
	BlockedBy     *[]string
	Todos         *[]Todo
	Trigger       **Trigger // double pointer: outer nil = unchanged, *outer nil = clear
	Due           *string
	MilestoneID   *string
	Surface       *Surface
	Result        *string
	Artifacts     *[]string
	SessionID     *string
	StartedAt     *string
	CompletedAt   *string
	SourceChannel *string
	SourceChatID  *string
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
			return nil, fmt.Errorf("title must not be empty")
		}
		if len([]rune(*patch.Title)) > 200 {
			return nil, fmt.Errorf("title must be 200 characters or fewer")
		}
		t.Title = *patch.Title
	}
	if patch.Description != nil {
		if len(*patch.Description) > 2000 {
			return nil, fmt.Errorf("description must be 2000 characters or fewer")
		}
		t.Description = *patch.Description
	}
	if patch.Prompt != nil {
		if len(*patch.Prompt) > 10000 {
			return nil, fmt.Errorf("prompt must be 10000 characters or fewer")
		}
		t.Prompt = *patch.Prompt
	}
	if patch.Status != nil {
		if !IsValidStatus(*patch.Status) {
			return nil, fmt.Errorf("invalid status %q", *patch.Status)
		}
		t.Status = *patch.Status
	}
	if patch.AgentID != nil {
		t.AgentID = *patch.AgentID
	}
	if patch.Priority != nil {
		if *patch.Priority < 1 || *patch.Priority > 5 {
			return nil, fmt.Errorf("priority must be between 1 and 5, got %d", *patch.Priority)
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
	if patch.MilestoneID != nil {
		t.MilestoneID = *patch.MilestoneID
	}
	if patch.Surface != nil {
		if !IsValidSurface(*patch.Surface) {
			return nil, fmt.Errorf("invalid surface %q", *patch.Surface)
		}
		t.Surface = *patch.Surface
	}
	if patch.Result != nil {
		if len(*patch.Result) > 50000 {
			return nil, fmt.Errorf("result must be 50000 characters or fewer")
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

	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.write(t); err != nil {
		return nil, err
	}
	return t, nil
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
	return s.cascadeDeleteEdges(id)
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
