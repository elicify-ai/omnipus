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

// TaskEntity is the persistent shape for one task stored at ~/.omnipus/tasks/<id>.json.
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
}

// legacyRaw is used only to detect and migrate old GTD-format task files.
type legacyRaw struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Status      string    `json:"status"`
	AgentID     string    `json:"agent_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// New fields — may be zero in old files.
	Title         string     `json:"title,omitempty"`
	Prompt        string     `json:"prompt,omitempty"`
	CreatedBy     string     `json:"created_by,omitempty"`
	ParentTaskID  string     `json:"parent_task_id,omitempty"`
	BlockedBy     []string   `json:"blocked_by,omitempty"`
	Priority      int        `json:"priority,omitempty"`
	Result        string     `json:"result,omitempty"`
	Artifacts     []string   `json:"artifacts,omitempty"`
	SessionID     string     `json:"session_id,omitempty"`
	TriggerType   string     `json:"trigger_type,omitempty"`
	SourceChannel string     `json:"source_channel,omitempty"`
	SourceChatID  string     `json:"source_chat_id,omitempty"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
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

// New creates a TaskStore rooted at dir (e.g. ~/.omnipus/tasks).
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

// load reads and (if needed) lazily migrates a task file.
func (s *TaskStore) load(id string) (*TaskEntity, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("taskstore: read %q: %w", id, err)
	}

	var raw legacyRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("taskstore: parse %q: %w", id, err)
	}

	// If the file already uses the new format (has Title), return as-is.
	if raw.Title != "" || raw.Name == "" {
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

	// Lazy migration from GTD format.
	t := &TaskEntity{
		ID:          raw.ID,
		Title:       raw.Name,
		Prompt:      raw.Description,
		AgentID:     raw.AgentID,
		CreatedBy:   "user",
		Priority:    3,
		TriggerType: "manual",
		CreatedAt:   raw.CreatedAt,
		// Preserve new-format fields that may be present even in legacy files.
		Result:        raw.Result,
		Artifacts:     raw.Artifacts,
		SessionID:     raw.SessionID,
		SourceChannel: raw.SourceChannel,
		SourceChatID:  raw.SourceChatID,
		StartedAt:     raw.StartedAt,
		CompletedAt:   raw.CompletedAt,
	}
	// Migrate GTD statuses to execution statuses.
	switch raw.Status {
	case "inbox", "next", "waiting":
		t.Status = "queued"
	case "active":
		t.Status = "running"
	case "done":
		t.Status = "completed"
	default:
		t.Status = "queued"
	}
	// Persist the migrated entity (best-effort; non-fatal if the write fails).
	if werr := s.write(t); werr != nil {
		slog.Warn("taskstore: lazy migration write failed", "id", t.ID, "error", werr)
	}
	return t, nil
}

// write persists a TaskEntity atomically.
func (s *TaskStore) write(t *TaskEntity) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("taskstore: create dir: %w", err)
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("taskstore: marshal %q: %w", t.ID, err)
	}
	return fileutil.WriteFileAtomic(s.path(t.ID), data, 0o600)
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
	entity.CreatedAt = time.Now().UTC()

	return s.write(entity)
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
