package taskstore

import (
	"encoding/json"
	"errors"
	"fmt"
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

// TaskEntity is the persistent shape for one workflow task stored at
// ~/.omnipus/workflow-tasks/<id>.json.
type TaskEntity struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	Prompt        string     `json:"prompt"`
	AgentID       string     `json:"agent_id"`
	CreatedBy     string     `json:"created_by"`
	ParentTaskID  string     `json:"parent_task_id,omitempty"`
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
}

// TaskFilter filters the result of List.  All fields are optional (zero = skip filter).
type TaskFilter struct {
	Status       string
	AgentID      string
	CreatedBy    string
	ParentTaskID string
}

// TaskPatch is a partial update applied by Update.
type TaskPatch struct {
	Status        *string
	Result        *string
	Artifacts     *[]string
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
	if raw.Title == "" {
		return nil, fmt.Errorf("taskstore: %w: file %q has no title field (not a workflow task)", ErrNotFound, id)
	}
	if !validStatuses[raw.Status] {
		return nil, fmt.Errorf("taskstore: %w: file %q has non-workflow status %q", ErrNotFound, id, raw.Status)
	}

	t := &TaskEntity{
		ID:            raw.ID,
		Title:         raw.Title,
		Prompt:        raw.Prompt,
		AgentID:       raw.AgentID,
		CreatedBy:     raw.CreatedBy,
		ParentTaskID:  raw.ParentTaskID,
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

	if err := s.write(t); err != nil {
		return nil, err
	}
	return t, nil
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
