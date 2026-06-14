package taskstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// ErrNotFound is returned when a task ID does not exist on disk.
var ErrNotFound = errors.New("task not found")

// ErrCycle is returned when a parent_task_id edge would create a cycle in the
// task dependency graph (e.g. A→B→A), or when the parent chain exceeds
// maxParentDepth. The task graph must remain a DAG.
var ErrCycle = errors.New("task parent edge would create a cycle")

// maxParentDepth bounds how far Create walks the parent chain before treating
// the graph as malformed. It also acts as a backstop against an extremely deep
// (or, on a corrupt store, looping) chain. Mirrors the executor's maxTaskDepth.
const maxParentDepth = 10

// TaskEntity is the persistent shape for one workflow task stored at
// ~/.omnipus/workflow-tasks/<id>.json.
type TaskEntity struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Prompt       string `json:"prompt"`
	AgentID      string `json:"agent_id"`
	CreatedBy    string `json:"created_by"`
	ParentTaskID string `json:"parent_task_id,omitempty"`
	// BlockedBy is a DAG-dependency list: this task will not be dispatched
	// until every task in BlockedBy has reached "completed" status.
	// The Orchestrator coordinator (task_executor.onTaskComplete) scans tasks
	// with non-empty BlockedBy after each terminal state transition and dispatches
	// those whose dependency set is now fully satisfied.
	BlockedBy     []string   `json:"blocked_by,omitempty"`
	Priority      int        `json:"priority"`
	Status        string     `json:"status"`
	Result        string     `json:"result,omitempty"`
	Artifacts     []string   `json:"artifacts,omitempty"`
	SessionID     string     `json:"session_id,omitempty"`
	TriggerType   string     `json:"trigger_type"`
	SourceChannel string     `json:"source_channel,omitempty"` // originating channel (e.g. "telegram")
	SourceChatID  string     `json:"source_chat_id,omitempty"` // originating chat ID for result delivery
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	// FollowedUp is set true (via ClaimParentFollowUp) once the parent follow-up
	// notification has been launched for this task. It exists solely to make the
	// "all siblings done → resume parent" follow-up fire exactly once: concurrent
	// sibling completions both observe allDone and would otherwise both spawn the
	// parent follow-up goroutine. The flag is on the PARENT task, claimed atomically.
	FollowedUp bool `json:"followed_up,omitempty"`
}

// workflowRaw is a superset of TaskEntity used to parse files from the
// workflow-tasks directory.  Extra fields (e.g. "name" from a mistakenly
// placed GTD file) are silently ignored via omitempty — the load function
// validates that the result is actually a workflow task before returning it.
type workflowRaw struct {
	ID            string     `json:"id"`
	Title         string     `json:"title,omitempty"`
	Prompt        string     `json:"prompt,omitempty"`
	AgentID       string     `json:"agent_id,omitempty"`
	CreatedBy     string     `json:"created_by,omitempty"`
	ParentTaskID  string     `json:"parent_task_id,omitempty"`
	BlockedBy     []string   `json:"blocked_by,omitempty"`
	Priority      int        `json:"priority,omitempty"`
	Status        string     `json:"status"`
	Result        string     `json:"result,omitempty"`
	Artifacts     []string   `json:"artifacts,omitempty"`
	SessionID     string     `json:"session_id,omitempty"`
	TriggerType   string     `json:"trigger_type,omitempty"`
	SourceChannel string     `json:"source_channel,omitempty"`
	SourceChatID  string     `json:"source_chat_id,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	FollowedUp    bool       `json:"followed_up,omitempty"`
}

// TaskFilter filters the result of List.  All fields are optional (zero = skip filter).
type TaskFilter struct {
	Status       string
	AgentID      string
	CreatedBy    string
	ParentTaskID string
	// BlockedByID, when non-empty, returns only tasks whose BlockedBy list
	// contains the given task ID. Used by the Orchestrator coordinator to find
	// tasks that are waiting on a just-completed task.
	BlockedByID string
}

// TaskPatch is a partial update applied by Update.
type TaskPatch struct {
	Status        *string
	Result        *string
	Artifacts     *[]string
	BlockedBy     *[]string
	SessionID     *string
	StartedAt     *time.Time
	CompletedAt   *time.Time
	Title         *string
	AgentID       *string
	Priority      *int
	SourceChannel *string
	SourceChatID  *string
}

// TaskStore manages per-entity JSON files in a directory.
type TaskStore struct {
	dir string
	mu  sync.Mutex
}

// New creates a TaskStore rooted at dir.
// The canonical workflow-task directory is ~/.omnipus/workflow-tasks/;
// callers should pass that path rather than the GTD tasks/ directory.
func New(dir string) *TaskStore {
	return &TaskStore{dir: dir}
}

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
func (s *TaskStore) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// load reads a workflow task file.  It never rewrites the file on read.
// Files that do not parse as a valid TaskEntity (e.g. a GTD task accidentally
// placed in the workflow-tasks directory) are skipped by returning ErrNotFound
// so callers can log a warning and move on without corrupting GTD data.
func (s *TaskStore) load(id string) (*TaskEntity, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("taskstore: read %q: %w", id, err)
	}

	var raw workflowRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("taskstore: parse %q: %w", id, err)
	}

	// A valid workflow file must have a non-empty Title and a workflow status.
	// Files that look like GTD tasks (have no Title, or have a GTD status) are
	// not owned by this store — skip them rather than mutating them.
	// WARN when a present file is structurally rejected so corruption is visible
	// in operator logs (a silent 404 would hide a corrupt or misplaced file).
	if raw.Title == "" {
		slog.Warn("taskstore: rejecting file — no title field (possible GTD task or corrupt file)",
			"id", id, "path", s.path(id))
		return nil, fmt.Errorf("taskstore: %w: file %q has no title field (not a workflow task)", ErrNotFound, id)
	}
	if !validStatuses[raw.Status] {
		slog.Warn("taskstore: rejecting file — unrecognized workflow status (possible GTD task or corrupt file)",
			"id", id, "status", raw.Status, "path", s.path(id))
		return nil, fmt.Errorf("taskstore: %w: file %q has non-workflow status %q", ErrNotFound, id, raw.Status)
	}

	t := &TaskEntity{
		ID:            raw.ID,
		Title:         raw.Title,
		Prompt:        raw.Prompt,
		AgentID:       raw.AgentID,
		CreatedBy:     raw.CreatedBy,
		ParentTaskID:  raw.ParentTaskID,
		BlockedBy:     raw.BlockedBy,
		Priority:      raw.Priority,
		Status:        raw.Status,
		Result:        raw.Result,
		Artifacts:     raw.Artifacts,
		SessionID:     raw.SessionID,
		TriggerType:   raw.TriggerType,
		SourceChannel: raw.SourceChannel,
		SourceChatID:  raw.SourceChatID,
		CreatedAt:     raw.CreatedAt,
		StartedAt:     raw.StartedAt,
		CompletedAt:   raw.CompletedAt,
		FollowedUp:    raw.FollowedUp,
	}
	if t.Priority == 0 {
		t.Priority = 3
	}
	if t.TriggerType == "" {
		t.TriggerType = "manual"
	}
	if t.CreatedBy == "" {
		t.CreatedBy = "user"
	}
	return t, nil
}

// write persists a TaskEntity atomically with an advisory flock.
func (s *TaskStore) write(t *TaskEntity) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("taskstore: create dir: %w", err)
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("taskstore: marshal %q: %w", t.ID, err)
	}
	p := s.path(t.ID)
	return fileutil.WithFlock(p, func() error {
		return fileutil.WriteFileAtomic(p, data, 0o600)
	})
}

// List returns all tasks matching filter, sorted by priority ASC then created_at ASC.
func (s *TaskStore) List(filter TaskFilter) ([]TaskEntity, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []TaskEntity{}, nil
		}
		return nil, fmt.Errorf("taskstore: list dir: %w", err)
	}

	var result []TaskEntity
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		t, err := s.load(id)
		if err != nil {
			logger.WarnCF("taskstore", "skip unreadable task", map[string]any{"id": id, "error": err.Error()})
			continue
		}
		if filter.Status != "" && t.Status != filter.Status {
			continue
		}
		if filter.AgentID != "" && t.AgentID != filter.AgentID {
			continue
		}
		if filter.CreatedBy != "" && t.CreatedBy != filter.CreatedBy {
			continue
		}
		if filter.ParentTaskID != "" && t.ParentTaskID != filter.ParentTaskID {
			continue
		}
		if filter.BlockedByID != "" && !containsString(t.BlockedBy, filter.BlockedByID) {
			continue
		}
		result = append(result, *t)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Priority != result[j].Priority {
			return result[i].Priority < result[j].Priority
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})

	if result == nil {
		result = []TaskEntity{}
	}
	return result, nil
}

// Get returns the task with the given id, or ErrNotFound if not present.
func (s *TaskStore) Get(id string) (*TaskEntity, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	return s.load(id)
}

// validStatuses is the set of allowed execution statuses.
var validStatuses = map[string]bool{
	"queued":    true,
	"assigned":  true,
	"running":   true,
	"completed": true,
	"failed":    true,
}

// validTransitions maps current status → set of allowed next statuses.
var validTransitions = map[string]map[string]bool{
	"queued":   {"assigned": true, "running": true, "failed": true},
	"assigned": {"running": true, "queued": true, "failed": true},
	"running":  {"completed": true, "failed": true},
}

// Create persists a new task entity.  It generates a UUID if ID is empty,
// sets CreatedAt, and validates Status and Priority.
func (s *TaskStore) Create(entity *TaskEntity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entity.ID == "" {
		entity.ID = uuid.New().String()
	}
	if err := validateID(entity.ID); err != nil {
		return err
	}
	if entity.Status == "" {
		entity.Status = "queued"
	}
	if entity.Status != "queued" {
		return fmt.Errorf("taskstore: new task must have status 'queued', got %q", entity.Status)
	}
	if entity.Priority < 1 || entity.Priority > 5 {
		if entity.Priority == 0 {
			entity.Priority = 3
		} else {
			return fmt.Errorf("taskstore: priority must be 1-5, got %d", entity.Priority)
		}
	}
	if entity.TriggerType == "" {
		entity.TriggerType = "manual"
	}
	if entity.CreatedBy == "" {
		entity.CreatedBy = "user"
	}
	// Reject a parent edge that would create a cycle or exceed the depth bound.
	// The task graph (parent_task_id edges) must remain a DAG so the executor's
	// parent-walk and result-aggregation terminate.
	if entity.ParentTaskID != "" {
		if err := s.checkParentAcyclic(entity.ID, entity.ParentTaskID); err != nil {
			return err
		}
	}
	entity.CreatedAt = time.Now().UTC()

	return s.write(entity)
}

// checkParentAcyclic walks the parent chain starting at parentID and rejects the
// edge newID → parentID if it would form a cycle. An edge creates a cycle when
// the chain from parentID reaches back to newID (the task being created) or
// revisits any node already seen on the walk. It also rejects chains deeper than
// maxParentDepth. A missing parent is rejected so callers cannot point at a
// non-existent task.
//
// Caller must hold s.mu (Create does).
func (s *TaskStore) checkParentAcyclic(newID, parentID string) error {
	if parentID == newID {
		return fmt.Errorf("%w: task %q cannot be its own parent", ErrCycle, newID)
	}
	visited := map[string]bool{}
	if newID != "" {
		// Seed the visited set with the node being created so that any chain
		// looping back to it is caught even before it is written to disk.
		visited[newID] = true
	}
	cur := parentID
	for depth := 0; cur != ""; depth++ {
		if depth >= maxParentDepth {
			return fmt.Errorf("%w: parent chain exceeds max depth %d", ErrCycle, maxParentDepth)
		}
		if visited[cur] {
			return fmt.Errorf("%w: parent edge %q → %q closes a loop at %q", ErrCycle, newID, parentID, cur)
		}
		visited[cur] = true
		parent, err := s.load(cur)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return fmt.Errorf("%w: parent task %q does not exist", ErrNotFound, cur)
			}
			return fmt.Errorf("taskstore: walk parent chain at %q: %w", cur, err)
		}
		cur = parent.ParentTaskID
	}
	return nil
}

// Update applies patch to the task identified by id and persists the result.
// It validates status transitions.
func (s *TaskStore) Update(id string, patch TaskPatch) (*TaskEntity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.load(id)
	if err != nil {
		return nil, err
	}

	if patch.Status != nil {
		newStatus := *patch.Status
		if !validStatuses[newStatus] {
			return nil, fmt.Errorf("taskstore: unknown status %q", newStatus)
		}
		if newStatus != t.Status {
			allowed := validTransitions[t.Status]
			if !allowed[newStatus] {
				return nil, fmt.Errorf("taskstore: invalid transition %q → %q", t.Status, newStatus)
			}
		}
		t.Status = newStatus
	}
	if patch.Result != nil {
		t.Result = *patch.Result
	}
	if patch.Artifacts != nil {
		t.Artifacts = *patch.Artifacts
	}
	if patch.SessionID != nil {
		t.SessionID = *patch.SessionID
	}
	if patch.StartedAt != nil {
		t.StartedAt = patch.StartedAt
	}
	if patch.CompletedAt != nil {
		t.CompletedAt = patch.CompletedAt
	}
	if patch.Title != nil {
		t.Title = *patch.Title
	}
	if patch.AgentID != nil {
		t.AgentID = *patch.AgentID
	}
	if patch.Priority != nil {
		p := *patch.Priority
		if p < 1 || p > 5 {
			return nil, fmt.Errorf("taskstore: priority must be 1-5, got %d", p)
		}
		t.Priority = p
	}
	if patch.SourceChannel != nil {
		t.SourceChannel = *patch.SourceChannel
	}
	if patch.SourceChatID != nil {
		t.SourceChatID = *patch.SourceChatID
	}
	if patch.BlockedBy != nil {
		t.BlockedBy = *patch.BlockedBy
	}

	if err := s.write(t); err != nil {
		return nil, err
	}
	return t, nil
}

// containsString reports whether slice contains s.
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// ValidateBlockedBy checks that all IDs in blockedBy exist and none are equal
// to selfID (trivial self-cycle). It does not detect multi-hop cycles; the
// depth limit in the orchestrator dispatch path prevents infinite loops.
func (s *TaskStore) ValidateBlockedBy(selfID string, blockedBy []string) error {
	for _, dep := range blockedBy {
		if dep == selfID {
			return fmt.Errorf("task cannot block itself: %q", dep)
		}
		if err := validateID(dep); err != nil {
			return fmt.Errorf("blocked_by contains invalid ID %q: %w", dep, err)
		}
		if _, err := s.Get(dep); err != nil {
			if errors.Is(err, ErrNotFound) {
				return fmt.Errorf("blocked_by task %q not found", dep)
			}
			return fmt.Errorf("blocked_by: could not verify task %q: %w", dep, err)
		}
	}
	return nil
}

// ErrAlreadyClaimed is returned by ClaimForRun when the task is not in queued
// status (either already running, completed, failed, or claimed by a concurrent caller).
var ErrAlreadyClaimed = errors.New("task already claimed")

// ClaimForRun atomically transitions a task from "queued" to "running" under
// the store mutex, making the transition a single critical section.
//
// This prevents the TOCTOU race where two concurrent callers (e.g. the
// heartbeat and advanceBlockedTasks) both pass a "status==queued" guard and
// both reach Update, resulting in two runTask goroutines for one task.
//
// Returns (updated entity, nil) on success.
// Returns (nil, ErrAlreadyClaimed) if the task is not "queued" when the lock is held.
// Returns (nil, ErrNotFound) if the task does not exist.
// Returns (nil, err) for I/O or validation errors.
func (s *TaskStore) ClaimForRun(id string, startedAt time.Time) (*TaskEntity, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	t, err := s.load(id)
	if err != nil {
		return nil, err
	}
	if t.Status != "queued" {
		return nil, fmt.Errorf("taskstore: %w: task %q is %q, not queued", ErrAlreadyClaimed, id, t.Status)
	}
	t.Status = "running"
	t.StartedAt = &startedAt
	if err := s.write(t); err != nil {
		return nil, err
	}
	return t, nil
}

// ClaimParentFollowUp atomically claims the right to launch the "all children
// done → resume parent" follow-up for the parent task id, returning true to
// exactly one caller.
//
// Concurrent sibling completions can each observe that every sibling is done and
// would otherwise each spawn the parent follow-up goroutine, producing duplicate
// resume messages to the parent agent. This CAS-style claim (mirroring
// ClaimForRun) closes that race: it sets FollowedUp=true under the store mutex
// and persists it, so only the first caller sees the flag flip from false→true.
//
// Returns (true, nil) when this caller won the claim (and must run the follow-up).
// Returns (false, nil) when the follow-up was already claimed (caller must skip).
// Returns (false, ErrNotFound) if the task does not exist, or (false, err) on I/O.
func (s *TaskStore) ClaimParentFollowUp(id string) (bool, error) {
	if err := validateID(id); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	t, err := s.load(id)
	if err != nil {
		return false, err
	}
	if t.FollowedUp {
		return false, nil
	}
	t.FollowedUp = true
	if err := s.write(t); err != nil {
		return false, err
	}
	return true, nil
}

// Delete removes the task file for id.
func (s *TaskStore) Delete(id string) error {
	if err := validateID(id); err != nil {
		return err
	}
	if err := os.Remove(s.path(id)); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return fmt.Errorf("taskstore: delete %q: %w", id, err)
	}
	return nil
}
