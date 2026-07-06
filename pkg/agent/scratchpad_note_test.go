// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// newAlWithTaskStore creates a minimal AgentLoop with a real task.Store rooted
// at dir. It avoids NewAgentLoop's full boot (which requires a config, bus, and
// provider) by directly setting the unexported field via GetTaskStore's inverse —
// we can't do that from outside the package, but we ARE in the agent package
// (package agent), so we can set it directly.
func newAlWithTaskStore(dir string) *AgentLoop {
	return &AgentLoop{
		taskStore: task.New(dir),
	}
}

// TestBuildScratchpadNote_NilStore proves that a nil taskStore returns "".
func TestBuildScratchpadNote_NilStore(t *testing.T) {
	al := &AgentLoop{taskStore: nil}
	note := al.buildScratchpadNote("agent-a")
	if note != "" {
		t.Errorf("nil store must return empty string, got: %q", note)
	}
}

// TestBuildScratchpadNote_EmptyAgentID proves that an empty agentID returns "".
func TestBuildScratchpadNote_EmptyAgentID(t *testing.T) {
	al := newAlWithTaskStore(t.TempDir())
	note := al.buildScratchpadNote("")
	if note != "" {
		t.Errorf("empty agentID must return empty string, got: %q", note)
	}
}

// TestBuildScratchpadNote_NonScratchpadTaskIgnored proves that a real
// create_task card (Scratchpad==false) with todos is NOT injected even if the
// agent has it. Fix 4: discriminator filter in buildScratchpadNote.
func TestBuildScratchpadNote_NonScratchpadTaskIgnored(t *testing.T) {
	al := newAlWithTaskStore(t.TempDir())

	// Seed a real task (Scratchpad=false, the default) with todos.
	realTask := &task.Task{
		Title:       "real task with todos",
		Action:      task.ActionLLM,
		AgentID:     "agent-a",
		CreatedBy:   "agent-a",
		WorkspaceID: "ws-1",
		Status:      task.StatusInProgress,
		Priority:    3,
		Todos:       []task.Todo{{Text: "real step", Status: task.TodoPending}},
		// Scratchpad is false — this is the key discriminator.
	}
	if err := al.taskStore.Create(realTask); err != nil {
		t.Fatalf("create real task: %v", err)
	}

	// buildScratchpadNote must NOT pick up this non-scratchpad card.
	note := al.buildScratchpadNote("agent-a")
	if note != "" {
		t.Errorf("non-scratchpad task must not be injected; got note: %q", note)
	}
}

// TestBuildScratchpadNote_ScratchpadCardReturnsNote proves that a scratchpad card
// (Scratchpad==true) with todos produces a note containing the goal and todos.
// Fix 4: unified selection path matches set_todos facade.
func TestBuildScratchpadNote_ScratchpadCardReturnsNote(t *testing.T) {
	al := newAlWithTaskStore(t.TempDir())

	// Seed a proper scratchpad card.
	scratchpad := &task.Task{
		Title:       "implement search feature",
		Action:      task.ActionLLM,
		AgentID:     "agent-a",
		CreatedBy:   "agent-a",
		WorkspaceID: "ws-1",
		Status:      task.StatusInProgress,
		Priority:    3,
		Scratchpad:  true,
		Todos: []task.Todo{
			{Text: "write tests", Status: task.TodoPending},
			{Text: "write code", Status: task.TodoInProgress},
			{Text: "review", Status: task.TodoCompleted},
		},
	}
	if err := al.taskStore.Create(scratchpad); err != nil {
		t.Fatalf("create scratchpad: %v", err)
	}

	note := al.buildScratchpadNote("agent-a")
	if note == "" {
		t.Fatal("expected a non-empty scratchpad note")
	}
	if !strings.Contains(note, "implement search feature") {
		t.Errorf("note must contain goal title, got: %q", note)
	}
	if !strings.Contains(note, "write tests") {
		t.Errorf("note must contain first todo, got: %q", note)
	}
	if !strings.Contains(note, "write code") {
		t.Errorf("note must contain second todo, got: %q", note)
	}
	if !strings.Contains(note, "review") {
		t.Errorf("note must contain third todo, got: %q", note)
	}
}

// TestBuildScratchpadNote_AfterArchivePreviousPicksActive proves that after
// the archive-previous lifecycle step (where the old scratchpad card is set to
// done), buildScratchpadNote returns the note for the NEW active scratchpad card,
// not the archived one.
func TestBuildScratchpadNote_AfterArchivePreviousPicksActive(t *testing.T) {
	al := newAlWithTaskStore(t.TempDir())

	// Create an "old" scratchpad card and immediately mark it done (simulating
	// what archive-previous does after a goal switch).
	old := &task.Task{
		Title:       "old goal",
		Action:      task.ActionLLM,
		AgentID:     "agent-a",
		CreatedBy:   "agent-a",
		WorkspaceID: "ws-1",
		Status:      task.StatusInProgress,
		Priority:    3,
		Scratchpad:  true,
		Todos:       []task.Todo{{Text: "old step", Status: task.TodoPending}},
	}
	if err := al.taskStore.Create(old); err != nil {
		t.Fatalf("create old card: %v", err)
	}
	done := task.StatusDone
	if _, err := al.taskStore.Update(old.ID, task.Patch{Status: &done}); err != nil {
		t.Fatalf("archive old card: %v", err)
	}

	// Create the new active scratchpad card.
	active := &task.Task{
		Title:       "new goal",
		Action:      task.ActionLLM,
		AgentID:     "agent-a",
		CreatedBy:   "agent-a",
		WorkspaceID: "ws-1",
		Status:      task.StatusInProgress,
		Priority:    3,
		Scratchpad:  true,
		Todos:       []task.Todo{{Text: "new step", Status: task.TodoPending}},
	}
	if err := al.taskStore.Create(active); err != nil {
		t.Fatalf("create active card: %v", err)
	}

	note := al.buildScratchpadNote("agent-a")
	if note == "" {
		t.Fatal("expected a non-empty note for the active scratchpad")
	}
	if !strings.Contains(note, "new goal") {
		t.Errorf("note must contain the active goal title, got: %q", note)
	}
	if strings.Contains(note, "old goal") {
		t.Errorf("note must NOT contain the archived goal title, got: %q", note)
	}
	if !strings.Contains(note, "new step") {
		t.Errorf("note must contain the active todo, got: %q", note)
	}
}

// TestBuildScratchpadNote_ScratchpadCardWithNoTodosIgnored proves that a
// scratchpad card that has no todos does not produce a note (the guard
// `len(tk.Todos) == 0` must still hold for scratchpad cards).
func TestBuildScratchpadNote_ScratchpadCardWithNoTodosIgnored(t *testing.T) {
	al := newAlWithTaskStore(t.TempDir())

	// Scratchpad card but no todos (e.g. checklist was cleared).
	empty := &task.Task{
		Title:       "empty goal",
		Action:      task.ActionLLM,
		AgentID:     "agent-a",
		CreatedBy:   "agent-a",
		WorkspaceID: "ws-1",
		Status:      task.StatusInProgress,
		Priority:    3,
		Scratchpad:  true,
		// Todos is nil/empty.
	}
	if err := al.taskStore.Create(empty); err != nil {
		t.Fatalf("create empty scratchpad: %v", err)
	}

	note := al.buildScratchpadNote("agent-a")
	if note != "" {
		t.Errorf("scratchpad card with no todos must not produce a note, got: %q", note)
	}
}
