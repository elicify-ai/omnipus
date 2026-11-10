package agent

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// R1 requires the saved checklist's optional phrasing on subsequent-turn readback.
func TestBuildScratchpadNoteActiveForm(t *testing.T) {
	dir := t.TempDir()
	al := newAlWithTaskStore(dir)
	var todos []task.Todo
	if err := json.Unmarshal([]byte(`[{"text":"Draft the Q4 summary","status":"in_progress","active_form":"Drafting the Q4 summary"},{"text":"Publish the summary","status":"pending"}]`), &todos); err != nil {
		t.Fatalf("decode checklist: %v", err)
	}
	card := &task.Task{
		Title: "Quarter plan", Action: task.ActionLLM, AgentID: "mia", WorkspaceID: "ws-1",
		Status: task.StatusInProgress, Scratchpad: true, Todos: todos,
	}
	if err := al.taskStore.CreateByAgent(card, "mia"); err != nil {
		t.Fatalf("create scratchpad: %v", err)
	}
	reloaded := newAlWithTaskStore(dir)
	got := reloaded.buildScratchpadNote("mia", "")
	want := "# Current scratchpad\nGoal: Quarter plan\n- [in_progress] Draft the Q4 summary (active_form: Drafting the Q4 summary)\n- [pending] Publish the summary\n"
	if got != want {
		t.Errorf("scratchpad readback = %q, want %q", got, want)
	}
}
