// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The list-jobs spec's FR-037 exists because Task.CreatedBy is mixed-namespace:
// the REST path writes a human username (pkg/gateway/rest_tasks.go, c.Username)
// and the agent tool path writes an agent id (pkg/tools/task.go, callerID). The
// tests below fix the OUTCOME that makes the collision harmless — a task a human
// created is never attributed to an agent whose id happens to equal that human's
// username — rather than the mechanism that achieves it.

// restCreatedTask mirrors the shape pkg/gateway/rest_tasks.go builds for a
// human-created task: Owner and CreatedBy both carry the caller's USERNAME.
// The username here deliberately collides with the agent id used below.
func restCreatedTask(title, ws, username string) *Task {
	return &Task{
		Title:       title,
		Action:      ActionLLM,
		Status:      StatusInbox,
		WorkspaceID: ws,
		Owner:       username,
		CreatedBy:   username,
	}
}

func TestCreatedByAgentID_AgentPathAttributes_RESTPathDoesNot(t *testing.T) {
	s := newStore(t)

	human := restCreatedTask("Human's private task", "ws-1", "mia")
	if err := s.Create(human); err != nil {
		t.Fatalf("REST-shaped create: %v", err)
	}

	agentTask := mkTask("Work agent mia dispatched", "ws-1")
	if err := s.CreateByAgent(agentTask, "mia"); err != nil {
		t.Fatalf("agent create: %v", err)
	}

	gotHuman, err := s.Get(human.ID)
	if err != nil {
		t.Fatalf("get human task: %v", err)
	}
	if gotHuman.CreatedByAgentID != "" {
		t.Errorf(
			"a task created through the REST/human path must carry NO agent attribution, got %q",
			gotHuman.CreatedByAgentID,
		)
	}
	if gotHuman.CreatedByAgent("mia") {
		t.Error("agent \"mia\" must not be credited with a task created by the human user \"mia\"")
	}

	gotAgent, err := s.Get(agentTask.ID)
	if err != nil {
		t.Fatalf("get agent task: %v", err)
	}
	if gotAgent.CreatedByAgentID != "mia" {
		t.Errorf("agent-created task: created_by_agent_id = %q, want %q", gotAgent.CreatedByAgentID, "mia")
	}
	if !gotAgent.CreatedByAgent("mia") {
		t.Error("agent \"mia\" must be credited with the task it created")
	}
}

// The disclosure this requirement is really about: agent "mia" asking for the
// work it dispatched must not receive the human user "mia"'s task titles.
func TestCreatedByAgentIDFilter_UsernameCollisionIsNotDisclosed(t *testing.T) {
	s := newStore(t)

	if err := s.Create(restCreatedTask("Salary review notes", "ws-1", "mia")); err != nil {
		t.Fatalf("REST-shaped create: %v", err)
	}
	if err := s.CreateByAgent(mkTask("Reindex the docs", "ws-1"), "mia"); err != nil {
		t.Fatalf("agent create: %v", err)
	}

	got, err := s.List(Filter{CreatedByAgentID: "mia"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("agent mia's dispatched roster: got %d tasks, want 1", len(got))
	}
	if got[0].Title != "Reindex the docs" {
		t.Errorf("agent mia's roster returned %q; the human user's task title must never appear", got[0].Title)
	}
}

// "An empty value never matches" — in BOTH directions, because each direction is
// a different real failure. An empty stored value must not be claimable by any
// caller, and an empty caller identity must not claim anything.
func TestCreatedByAgent_EmptyNeverMatches(t *testing.T) {
	unattributed := &Task{Title: "pre-FR-037 or REST-created", CreatedByAgentID: ""}
	if unattributed.CreatedByAgent("mia") {
		t.Error("a task with an empty created_by_agent_id must be matched by nobody")
	}
	if unattributed.CreatedByAgent("") {
		t.Error("an empty created_by_agent_id must not match an empty caller identity either")
	}

	attributed := &Task{Title: "agent-created", CreatedByAgentID: "mia"}
	if attributed.CreatedByAgent("") {
		t.Error("an empty caller identity must never match an attributed task")
	}
	if attributed.CreatedByAgent("jim") {
		t.Error("a different agent must never match")
	}
	if !attributed.CreatedByAgent("mia") {
		t.Error("the creating agent must match")
	}
}

func TestCreateByAgent_RejectsEmptyAgentID(t *testing.T) {
	s := newStore(t)

	err := s.CreateByAgent(mkTask("no caller identity", "ws-1"), "")
	if err == nil {
		t.Fatal("CreateByAgent with an empty agent id must fail closed, not write an unattributed task")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("error must wrap ErrValidation (maps to HTTP 400 at the REST seam), got %v", err)
	}

	got, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("nothing must be persisted when the caller identity is unresolved, got %d tasks", len(got))
	}
}

// Additive-field guarantee (spec dataset row: "task.Task without
// CreatedByAgentID"). Greenfield means no backfill, so a record written before
// the field existed must still load, still list, and still be attributed to
// nobody.
func TestCreatedByAgentID_LegacyRecordOnDiskLoadsUnattributed(t *testing.T) {
	s := newStore(t)

	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := []byte(`{
	  "id": "legacy-1",
	  "title": "Written before FR-037",
	  "action": "llm",
	  "status": "next",
	  "workspace_id": "ws-1",
	  "created_by": "mia",
	  "created_at": "2026-01-01T00:00:00Z",
	  "updated_at": "2026-01-01T00:00:00Z"
	}`)
	if err := os.WriteFile(filepath.Join(s.Dir(), "legacy-1.json"), legacy, 0o600); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	got, err := s.Get("legacy-1")
	if err != nil {
		t.Fatalf("get legacy task: %v", err)
	}
	if got.CreatedByAgentID != "" {
		t.Errorf("legacy record must load unattributed, got %q", got.CreatedByAgentID)
	}
	if got.CreatedByAgent("mia") {
		t.Error("a legacy record whose created_by is a username must not be attributed to agent \"mia\"")
	}

	rows, err := s.List(Filter{CreatedByAgentID: "mia"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("legacy record must not appear in any agent's roster, got %d rows", len(rows))
	}
}

// The field is disk-only (FR-037/FR-025: no contract change). Assert the
// persisted JSON key so a rename can't silently break the consumer that reads
// it, and assert it is absent — not null/empty — when unset.
func TestCreatedByAgentID_PersistedJSONKey(t *testing.T) {
	s := newStore(t)

	agentTask := mkTask("agent work", "ws-1")
	if err := s.CreateByAgent(agentTask, "mia"); err != nil {
		t.Fatalf("agent create: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir(), agentTask.ID+".json"))
	if err != nil {
		t.Fatalf("read task file: %v", err)
	}
	var onDisk map[string]any
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("parse task file: %v", err)
	}
	if got := onDisk["created_by_agent_id"]; got != "mia" {
		t.Errorf("on-disk created_by_agent_id = %v, want %q", got, "mia")
	}

	human := restCreatedTask("human work", "ws-1", "mia")
	if err := s.Create(human); err != nil {
		t.Fatalf("REST-shaped create: %v", err)
	}
	rawHuman, err := os.ReadFile(filepath.Join(s.Dir(), human.ID+".json"))
	if err != nil {
		t.Fatalf("read human task file: %v", err)
	}
	var humanOnDisk map[string]any
	if err := json.Unmarshal(rawHuman, &humanOnDisk); err != nil {
		t.Fatalf("parse human task file: %v", err)
	}
	if _, present := humanOnDisk["created_by_agent_id"]; present {
		t.Error("created_by_agent_id must be omitted entirely from a human-created record (omitempty)")
	}
}

// Attribution is creation-time and immutable: no Patch field writes it, so an
// update can never move a task into another agent's roster.
func TestCreatedByAgentID_SurvivesUpdateUnchanged(t *testing.T) {
	s := newStore(t)

	tk := mkTask("agent work", "ws-1")
	if err := s.CreateByAgent(tk, "mia"); err != nil {
		t.Fatalf("agent create: %v", err)
	}

	newTitle := "retitled by someone else"
	inProgress := StatusInProgress
	updated, err := s.Update(tk.ID, Patch{Title: &newTitle, Status: &inProgress})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.CreatedByAgentID != "mia" {
		t.Errorf("after update: created_by_agent_id = %q, want %q (attribution is immutable)",
			updated.CreatedByAgentID, "mia")
	}
}
