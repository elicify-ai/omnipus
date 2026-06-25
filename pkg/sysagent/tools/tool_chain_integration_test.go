// Omnipus — Tool Chain Integration Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// tool_chain_integration_test.go — exercises sequences of tools that must
// compose correctly: create → list → update → delete, remember → recall,
// send_message delivery, and workspace + task cascade delete.
//
// These are "chain" tests in the sense of Plan §10: multiple tools called in
// sequence with state checked after each step.  No real LLM or network is used.
//
// All tests are deterministic, use real on-disk stores via t.TempDir(), and
// assert real content at every step — not just "no error".
//
// Traces to: Plan §8/§9.8/§10, issue #440

package systools_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// chainDeps returns Deps wired to a fresh temp dir.
func chainDeps(t *testing.T) (*systools.Deps, string) {
	t.Helper()
	return newTestDepsWithHome(t)
}

// extractID pulls the "id" field out of a successful tool result body.
func extractID(t *testing.T, body string) string {
	t.Helper()
	m := parseSuccess(t, body)
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("tool result missing 'id': %s", body)
	}
	return id
}

// assertJSONField unmarshals body as a map and checks that key == wantValue.
func assertJSONField(t *testing.T, body, key string, wantValue any) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("assertJSONField: body is not JSON: %v\nbody: %s", err, body)
	}
	got := m[key]
	// Convert both sides to string for a robust comparison.
	gotStr := jsonVal(got)
	wantStr := jsonVal(wantValue)
	if gotStr != wantStr {
		t.Errorf("field %q = %v, want %v\nbody: %s", key, gotStr, wantStr, body)
	}
}

func jsonVal(v any) string {
	if v == nil {
		return "<nil>"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// ── inMemoryMemoryStore — implements tools.MemoryAccess for test chains ──────

// inMemoryMemoryStore is a minimal MemoryAccess implementation backed by a
// slice.  Used to test the remember → recall_memory chain without touching the
// bleve/JSONL infrastructure (which has its own unit tests).
type inMemoryMemoryStore struct {
	mu      sync.Mutex
	entries []tools.MemoryEntry
}

func (s *inMemoryMemoryStore) AppendLongTerm(content, category string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, tools.MemoryEntry{
		Content:  content,
		Category: category,
	})
	return nil
}

func (s *inMemoryMemoryStore) AppendRetro(_ string, _ tools.MemoryRetro) error {
	return nil
}

func (s *inMemoryMemoryStore) SearchEntries(query string, limit int) ([]tools.MemoryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	query = strings.ToLower(query)
	var results []tools.MemoryEntry
	for _, e := range s.entries {
		if strings.Contains(strings.ToLower(e.Content), query) {
			results = append(results, e)
			if len(results) >= limit {
				break
			}
		}
	}
	return results, nil
}

// ── Chain 1: create_task → list_tasks → update_task → delete_task ────────────

// TestToolChain_TaskCRUD exercises the full create → list → update → delete
// sequence through the sysagent tool layer, asserting real state at every step.
//
// BDD:
//
//	Given a workspace exists
//	When  create_task_in_workspace is called with name="Chain Test"
//	Then  the task appears in list_tasks_in_workspace
//	And   update_task_in_workspace changes its status to "in_progress"
//	And   the updated status is visible in a fresh list
//	And   delete_task_in_workspace removes it (list is empty afterward)
//
// Traces to: Plan §10 — Chain 1 (create_task → list_tasks → update_task → delete_task)
func TestToolChain_TaskCRUD(t *testing.T) {
	deps, _ := chainDeps(t)
	ctx := context.Background()

	// Precondition: create a workspace to hold the task.
	wsResult := systools.NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{
		"name": "Chain Workspace",
	})
	if wsResult.IsError {
		t.Fatalf("create_workspace: %s", wsResult.ForLLM)
	}
	wsID := extractID(t, wsResult.ForLLM)

	// ── step 1: create ────────────────────────────────────────────────────
	createResult := systools.NewTaskCreateTool(deps).Execute(ctx, map[string]any{
		"name":         "Chain Test",
		"workspace_id": wsID,
	})
	if createResult.IsError {
		t.Fatalf("create_task: %s", createResult.ForLLM)
	}
	taskID := extractID(t, createResult.ForLLM)

	// Content test: status defaults to "inbox".
	assertJSONField(t, createResult.ForLLM, "status", "inbox")
	assertJSONField(t, createResult.ForLLM, "name", "Chain Test")

	// ── step 2: list — task must appear ───────────────────────────────────
	listResult := systools.NewTaskListTool(deps).Execute(ctx, map[string]any{
		"workspace_id": wsID,
	})
	if listResult.IsError {
		t.Fatalf("list_tasks: %s", listResult.ForLLM)
	}
	if !strings.Contains(listResult.ForLLM, taskID) {
		t.Errorf("list_tasks after create: task id %q not in result:\n%s", taskID, listResult.ForLLM)
	}
	if !strings.Contains(listResult.ForLLM, "Chain Test") {
		t.Errorf("list_tasks after create: task name 'Chain Test' not in result:\n%s", listResult.ForLLM)
	}

	// ── step 3: update status ─────────────────────────────────────────────
	updateResult := systools.NewTaskUpdateTool(deps).Execute(ctx, map[string]any{
		"id":     taskID,
		"status": "in_progress",
	})
	if updateResult.IsError {
		t.Fatalf("update_task: %s", updateResult.ForLLM)
	}

	// Verify the updated status is reflected in a fresh list.
	listResult2 := systools.NewTaskListTool(deps).Execute(ctx, map[string]any{
		"workspace_id": wsID,
	})
	if listResult2.IsError {
		t.Fatalf("list_tasks after update: %s", listResult2.ForLLM)
	}
	if !strings.Contains(listResult2.ForLLM, "in_progress") {
		t.Errorf("list_tasks after update: 'in_progress' status not in result:\n%s", listResult2.ForLLM)
	}

	// ── step 4: delete ────────────────────────────────────────────────────
	deleteResult := systools.NewTaskDeleteTool(deps).Execute(ctx, map[string]any{
		"id":      taskID,
		"confirm": true,
	})
	if deleteResult.IsError {
		t.Fatalf("delete_task: %s", deleteResult.ForLLM)
	}
	assertJSONField(t, deleteResult.ForLLM, "deleted", true)

	// Persistence test: task must be gone from list.
	listResult3 := systools.NewTaskListTool(deps).Execute(ctx, map[string]any{
		"workspace_id": wsID,
	})
	if listResult3.IsError {
		t.Fatalf("list_tasks after delete: %s", listResult3.ForLLM)
	}
	if strings.Contains(listResult3.ForLLM, taskID) {
		t.Errorf("delete: task id %q still appears in list after delete:\n%s", taskID, listResult3.ForLLM)
	}
}

// TestToolChain_TaskCRUD_DifferentInputsDifferentIDs is the differentiation
// test: two different tasks created in sequence must receive distinct IDs and
// names, proving create_task is not returning hardcoded data.
//
// Traces to: Plan §10 — differentiation guard for Chain 1
func TestToolChain_TaskCRUD_DifferentInputsDifferentIDs(t *testing.T) {
	deps, _ := chainDeps(t)
	ctx := context.Background()

	wsResult := systools.NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{
		"name": "Diff Workspace",
	})
	if wsResult.IsError {
		t.Fatalf("create_workspace: %s", wsResult.ForLLM)
	}
	wsID := extractID(t, wsResult.ForLLM)

	r1 := systools.NewTaskCreateTool(deps).Execute(ctx, map[string]any{
		"name": "Task Alpha", "workspace_id": wsID,
	})
	r2 := systools.NewTaskCreateTool(deps).Execute(ctx, map[string]any{
		"name": "Task Beta", "workspace_id": wsID,
	})

	if r1.IsError {
		t.Fatalf("create Task Alpha: %s", r1.ForLLM)
	}
	if r2.IsError {
		t.Fatalf("create Task Beta: %s", r2.ForLLM)
	}

	id1 := extractID(t, r1.ForLLM)
	id2 := extractID(t, r2.ForLLM)

	if id1 == id2 {
		t.Errorf("differentiation FAIL: two tasks have the same id %q", id1)
	}
	if strings.Contains(r1.ForLLM, "Task Beta") {
		t.Error("differentiation FAIL: Task Alpha result contains 'Task Beta'")
	}
	if strings.Contains(r2.ForLLM, "Task Alpha") {
		t.Error("differentiation FAIL: Task Beta result contains 'Task Alpha'")
	}
}

// ── Chain 2: remember → recall_memory ────────────────────────────────────────

// TestToolChain_RememberRecall exercises the remember → recall_memory chain
// using an in-memory store.  Asserts that a written memory is returned by
// recall, and that a distinct query does NOT return the wrong memory.
//
// BDD:
//
//	Given an in-memory memory store
//	When  remember is called with content="omnipus uses bleve for search"
//	Then  recall_memory with query="bleve" returns content containing "bleve"
//	And   recall_memory with query="postgres" returns no results
//
// Traces to: Plan §10 — Chain 2 (remember → recall_memory)
func TestToolChain_RememberRecall(t *testing.T) {
	store := &inMemoryMemoryStore{}
	rememberTool := tools.NewRememberTool(store, (*audit.Logger)(nil))
	recallTool := tools.NewRecallMemoryTool(store)
	ctx := context.Background()

	// ── step 1: remember a fact ───────────────────────────────────────────
	remResult := rememberTool.Execute(ctx, map[string]any{
		"content":  "omnipus uses bleve for full-text search in long-term memory",
		"category": "reference",
	})
	if remResult.IsError {
		t.Fatalf("remember: %s", remResult.ForLLM)
	}

	// ── step 2: recall — must find the remembered fact ────────────────────
	recallResult := recallTool.Execute(ctx, map[string]any{
		"query": "bleve",
	})
	if recallResult.IsError {
		t.Fatalf("recall_memory bleve: %s", recallResult.ForLLM)
	}
	if !strings.Contains(recallResult.ForLLM, "bleve") {
		t.Errorf("recall_memory: expected 'bleve' in result, got:\n%s", recallResult.ForLLM)
	}

	// ── step 3: recall with a non-matching query — must be empty ─────────
	recallEmpty := recallTool.Execute(ctx, map[string]any{
		"query": "postgres",
	})
	// A no-match recall is not an error — it returns an empty/no-results message.
	if recallEmpty.IsError {
		// Some implementations return error for no-result; tolerate it here but
		// the result must NOT contain "bleve".
		t.Logf("recall_memory (no-match) returned IsError=true: %s", recallEmpty.ForLLM)
	}
	if strings.Contains(recallEmpty.ForLLM, "bleve") {
		t.Errorf("recall_memory postgres: unexpectedly returned 'bleve' content:\n%s", recallEmpty.ForLLM)
	}
}

// TestToolChain_RememberRecall_Differentiation verifies that two different
// memories are recalled independently — both appear by their distinct queries.
//
// Traces to: Plan §10 — differentiation test for Chain 2
func TestToolChain_RememberRecall_Differentiation(t *testing.T) {
	store := &inMemoryMemoryStore{}
	rememberTool := tools.NewRememberTool(store, (*audit.Logger)(nil))
	recallTool := tools.NewRecallMemoryTool(store)
	ctx := context.Background()

	// Write two distinct memories.
	r1 := rememberTool.Execute(ctx, map[string]any{
		"content":  "the project uses golang for all backend logic",
		"category": "reference",
	})
	if r1.IsError {
		t.Fatalf("remember golang: %s", r1.ForLLM)
	}

	r2 := rememberTool.Execute(ctx, map[string]any{
		"content":  "the frontend stack uses react and vite",
		"category": "reference",
	})
	if r2.IsError {
		t.Fatalf("remember react: %s", r2.ForLLM)
	}

	// Recall each by its distinctive keyword.
	recallGo := recallTool.Execute(ctx, map[string]any{"query": "golang"})
	recallReact := recallTool.Execute(ctx, map[string]any{"query": "react"})

	if recallGo.IsError {
		t.Fatalf("recall golang: %s", recallGo.ForLLM)
	}
	if recallReact.IsError {
		t.Fatalf("recall react: %s", recallReact.ForLLM)
	}

	if !strings.Contains(recallGo.ForLLM, "golang") {
		t.Errorf("recall golang: expected 'golang' in result:\n%s", recallGo.ForLLM)
	}
	if !strings.Contains(recallReact.ForLLM, "react") {
		t.Errorf("recall react: expected 'react' in result:\n%s", recallReact.ForLLM)
	}
	// Differentiation: the two results must be different.
	if recallGo.ForLLM == recallReact.ForLLM {
		t.Error("differentiation FAIL: recall golang and recall react returned identical responses")
	}
}

// ── Chain 3: send_message → assert delivery ───────────────────────────────────

// TestToolChain_SendMessage_ChannelDelivery exercises the send_message tool
// with a mock send callback, verifying channel delivery by inspecting captured
// outbound messages.
//
// BDD:
//
//	Given a send_message tool wired to a capture callback
//	When  Execute is called with content="hello chain test"
//	Then  the callback receives channel="test-channel", chatID="room-1", content="hello chain test"
//
// Traces to: Plan §10 — Chain 3 (send_message → channel delivery)
func TestToolChain_SendMessage_ChannelDelivery(t *testing.T) {
	type captured struct {
		channel string
		chatID  string
		content string
	}
	var sentMessages []captured
	var mu sync.Mutex

	callback := func(channel, chatID, content string) error {
		mu.Lock()
		defer mu.Unlock()
		sentMessages = append(sentMessages, captured{channel: channel, chatID: chatID, content: content})
		return nil
	}

	msgTool := tools.NewMessageTool()
	msgTool.SetSendCallback(callback)

	// Build a context with channel and chatID so the tool can infer delivery target.
	ctx := tools.WithToolContext(context.Background(), "test-channel", "room-1")

	result := msgTool.Execute(ctx, map[string]any{
		"content": "hello chain test",
	})
	if result.IsError {
		t.Fatalf("send_message: %s", result.ForLLM)
	}

	mu.Lock()
	n := len(sentMessages)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("send_message: expected 1 delivery, got %d", n)
	}

	mu.Lock()
	got := sentMessages[0]
	mu.Unlock()

	if got.channel != "test-channel" {
		t.Errorf("delivery channel = %q, want %q", got.channel, "test-channel")
	}
	if got.chatID != "room-1" {
		t.Errorf("delivery chatID = %q, want %q", got.chatID, "room-1")
	}
	if got.content != "hello chain test" {
		t.Errorf("delivery content = %q, want %q", got.content, "hello chain test")
	}
}

// TestToolChain_SendMessage_Differentiation verifies that two different messages
// produce two distinct deliveries — proving the tool is not hardcoding content.
//
// Traces to: Plan §10 — differentiation test for Chain 3
func TestToolChain_SendMessage_Differentiation(t *testing.T) {
	type captured struct{ content string }
	var sent []captured
	var mu sync.Mutex

	callback := func(_, _, content string) error {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, captured{content: content})
		return nil
	}

	msgTool := tools.NewMessageTool()
	msgTool.SetSendCallback(callback)
	ctx := tools.WithToolContext(context.Background(), "ch", "cid")

	r1 := msgTool.Execute(ctx, map[string]any{"content": "first distinct message"})
	r2 := msgTool.Execute(ctx, map[string]any{"content": "second distinct message"})

	if r1.IsError {
		t.Fatalf("send_message 1: %s", r1.ForLLM)
	}
	if r2.IsError {
		t.Fatalf("send_message 2: %s", r2.ForLLM)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 {
		t.Fatalf("expected 2 deliveries, got %d", len(sent))
	}
	if sent[0].content == sent[1].content {
		t.Errorf("differentiation FAIL: both deliveries have content %q", sent[0].content)
	}
	if sent[0].content != "first distinct message" {
		t.Errorf("delivery[0] content = %q, want 'first distinct message'", sent[0].content)
	}
	if sent[1].content != "second distinct message" {
		t.Errorf("delivery[1] content = %q, want 'second distinct message'", sent[1].content)
	}
}

// ── Chain 4: create_workspace → create_task → delete_workspace cascade ────────

// TestToolChain_WorkspaceCascadeDelete verifies that deleting a workspace whose
// tasks have already been listed works correctly, and that a task in the workspace
// can be retrieved/listed before the workspace is deleted.
//
// NOTE: The current delete_workspace implementation deletes the workspace file
// only; it does NOT cascade-delete child tasks (that is a v0.3 concern per the
// BRD).  This test asserts the CURRENT behaviour: after delete_workspace, the
// workspace itself is gone (list_workspaces does not include it), while the
// task store is still accessible separately.
//
// If cascade-delete is later implemented, this test should be updated to assert
// that the task is also gone.
//
// BDD:
//
//	Given create_workspace returns workspace W
//	And   create_task_in_workspace creates task T in W
//	When  list_tasks_in_workspace for W returns T
//	And   delete_workspace W is called
//	Then  the workspace file is gone (list_workspaces no longer lists W)
//	And   the task was observable in the list before deletion
//
// Traces to: Plan §10 — Chain 4 (create_workspace → create_task → delete_workspace)
func TestToolChain_WorkspaceCascadeDelete(t *testing.T) {
	deps, _ := chainDeps(t)
	ctx := context.Background()

	// ── step 1: create workspace ──────────────────────────────────────────
	wsResult := systools.NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{
		"name":        "Cascade Workspace",
		"description": "to be deleted",
	})
	if wsResult.IsError {
		t.Fatalf("create_workspace: %s", wsResult.ForLLM)
	}
	wsID := extractID(t, wsResult.ForLLM)

	// ── step 2: create a task inside the workspace ────────────────────────
	taskResult := systools.NewTaskCreateTool(deps).Execute(ctx, map[string]any{
		"name":         "Orphaned Task",
		"workspace_id": wsID,
	})
	if taskResult.IsError {
		t.Fatalf("create_task: %s", taskResult.ForLLM)
	}
	taskID := extractID(t, taskResult.ForLLM)

	// ── step 3: list — task appears before deletion ───────────────────────
	listBefore := systools.NewTaskListTool(deps).Execute(ctx, map[string]any{
		"workspace_id": wsID,
	})
	if listBefore.IsError {
		t.Fatalf("list_tasks before delete: %s", listBefore.ForLLM)
	}
	if !strings.Contains(listBefore.ForLLM, taskID) {
		t.Errorf("list before delete: task %q not found:\n%s", taskID, listBefore.ForLLM)
	}

	// ── step 4: delete workspace ──────────────────────────────────────────
	delResult := systools.NewWorkspaceDeleteTool(deps).Execute(ctx, map[string]any{
		"id":      wsID,
		"confirm": true,
	})
	if delResult.IsError {
		t.Fatalf("delete_workspace: %s", delResult.ForLLM)
	}
	assertJSONField(t, delResult.ForLLM, "deleted", true)

	// ── step 5: workspace no longer visible in list_workspaces ────────────
	listWS := systools.NewWorkspaceListTool(deps).Execute(ctx, map[string]any{})
	if listWS.IsError {
		t.Fatalf("list_workspaces after delete: %s", listWS.ForLLM)
	}
	if strings.Contains(listWS.ForLLM, wsID) {
		t.Errorf("delete_workspace: workspace %q still appears in list_workspaces:\n%s", wsID, listWS.ForLLM)
	}
	if strings.Contains(listWS.ForLLM, "Cascade Workspace") {
		t.Errorf("delete_workspace: 'Cascade Workspace' still appears in list_workspaces:\n%s", listWS.ForLLM)
	}
}

// TestToolChain_WorkspaceCascadeDelete_Differentiation verifies that two
// workspaces created with different names produce different IDs, and that
// deleting one does not affect the other.
//
// Traces to: Plan §10 — differentiation test for Chain 4
func TestToolChain_WorkspaceCascadeDelete_Differentiation(t *testing.T) {
	deps, _ := chainDeps(t)
	ctx := context.Background()

	rA := systools.NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{"name": "Workspace Alpha"})
	rB := systools.NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{"name": "Workspace Beta"})

	if rA.IsError {
		t.Fatalf("create Alpha: %s", rA.ForLLM)
	}
	if rB.IsError {
		t.Fatalf("create Beta: %s", rB.ForLLM)
	}

	idA := extractID(t, rA.ForLLM)
	idB := extractID(t, rB.ForLLM)
	if idA == idB {
		t.Errorf("differentiation FAIL: both workspaces have id %q", idA)
	}

	// Delete only Alpha.
	delA := systools.NewWorkspaceDeleteTool(deps).Execute(ctx, map[string]any{
		"id": idA, "confirm": true,
	})
	if delA.IsError {
		t.Fatalf("delete Alpha: %s", delA.ForLLM)
	}

	// Beta must still be listed.
	listWS := systools.NewWorkspaceListTool(deps).Execute(ctx, map[string]any{})
	if listWS.IsError {
		t.Fatalf("list_workspaces: %s", listWS.ForLLM)
	}
	if strings.Contains(listWS.ForLLM, idA) {
		t.Errorf("Workspace Alpha (%s) still listed after delete", idA)
	}
	if !strings.Contains(listWS.ForLLM, idB) {
		t.Errorf("Workspace Beta (%s) missing from list after Alpha deleted", idB)
	}
}

// ── Rejection / invalid-input tests ──────────────────────────────────────────

// TestToolChain_CreateTask_InvalidWorkspaceID verifies that create_task_in_workspace
// rejects a non-existent workspace_id with a structured error.
//
// Traces to: Plan §10 — rejection test for Chain 1
func TestToolChain_CreateTask_InvalidWorkspaceID(t *testing.T) {
	deps, _ := chainDeps(t)
	ctx := context.Background()

	result := systools.NewTaskCreateTool(deps).Execute(ctx, map[string]any{
		"name":         "Orphan Task",
		"workspace_id": "nonexistent-workspace-id-99999",
	})
	if !result.IsError {
		t.Fatalf("expected error for invalid workspace_id, got success:\n%s", result.ForLLM)
	}
	// Content test: error must reference the invalid input.
	if !strings.Contains(result.ForLLM, "workspace_id") &&
		!strings.Contains(result.ForLLM, "INVALID_INPUT") {
		t.Errorf("error body does not mention workspace_id or INVALID_INPUT:\n%s", result.ForLLM)
	}
}

// TestToolChain_DeleteTask_RequiresConfirm verifies the confirmation guard.
//
// Traces to: Plan §10 — rejection test for Chain 1
func TestToolChain_DeleteTask_RequiresConfirm(t *testing.T) {
	deps, _ := chainDeps(t)
	ctx := context.Background()

	// Create a workspace and a task.
	wsRes := systools.NewWorkspaceCreateTool(deps).Execute(ctx, map[string]any{"name": "Del Guard"})
	if wsRes.IsError {
		t.Fatalf("create_workspace: %s", wsRes.ForLLM)
	}
	wsID := extractID(t, wsRes.ForLLM)

	taskRes := systools.NewTaskCreateTool(deps).Execute(ctx, map[string]any{
		"name": "Must Confirm", "workspace_id": wsID,
	})
	if taskRes.IsError {
		t.Fatalf("create_task: %s", taskRes.ForLLM)
	}
	taskID := extractID(t, taskRes.ForLLM)

	// Delete without confirm=true must fail.
	delResult := systools.NewTaskDeleteTool(deps).Execute(ctx, map[string]any{
		"id":      taskID,
		"confirm": false,
	})
	if !delResult.IsError {
		t.Fatalf("expected error when confirm=false, got success:\n%s", delResult.ForLLM)
	}
	if !strings.Contains(delResult.ForLLM, "confirm") &&
		!strings.Contains(delResult.ForLLM, "CONFIRMATION_REQUIRED") {
		t.Errorf("rejection body does not mention 'confirm':\n%s", delResult.ForLLM)
	}

	// Task must still be in the list (not deleted).
	listResult := systools.NewTaskListTool(deps).Execute(ctx, map[string]any{"workspace_id": wsID})
	if listResult.IsError {
		t.Fatalf("list after rejected delete: %s", listResult.ForLLM)
	}
	if !strings.Contains(listResult.ForLLM, taskID) {
		t.Errorf("task %q disappeared after rejected delete:\n%s", taskID, listResult.ForLLM)
	}
}

// TestToolChain_Remember_EmptyContent verifies that the remember tool rejects
// empty content with a specific error.
//
// Traces to: Plan §10 — rejection test for Chain 2
func TestToolChain_Remember_EmptyContent(t *testing.T) {
	store := &inMemoryMemoryStore{}
	rememberTool := tools.NewRememberTool(store, (*audit.Logger)(nil))
	ctx := context.Background()

	result := rememberTool.Execute(ctx, map[string]any{
		"content":  "",
		"category": "reference",
	})
	if !result.IsError {
		t.Fatalf("expected error for empty content, got success: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "content") && !strings.Contains(result.ForLLM, "empty") {
		t.Errorf("error message does not mention content/empty:\n%s", result.ForLLM)
	}
	// The store must be empty — no write should have occurred.
	results, _ := store.SearchEntries("", 10)
	if len(results) != 0 {
		t.Errorf("expected no memories written after rejection, got %d", len(results))
	}
}

// newTestDepsForChains is an alias for clarity in chain tests; same as
// newTestDepsWithHome but signals intent.
// (newTestDepsWithHome is defined in agent_test.go within this package.)
var _ = func() *systools.Deps {
	// Ensure the function signature matches what chainDeps uses.
	// This is a compile-time guard, not a runtime call.
	return &systools.Deps{}
}

// configDefaultForChains returns a default config for chain tests that need one.
// Defined here so chain tests do not import config directly when newTestDeps suffices.
func configDefaultForChains() *config.Config {
	return config.DefaultConfig()
}
