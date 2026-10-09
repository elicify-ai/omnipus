// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U9 (canonical task-family workspace merge), pkg/tools
// slice — FR-049 foreign status-only read. Spec:
// docs/internal/specs/session-core-spec.md FR-049, BDD-08.7, US8.7, T34.
//
// FR-049: "Foreign creator MAY read only status of tasks it created in
// explicit destination via CreatedByAgentID/CreatedByAgent+workspace, including
// direct-ID/alternate-role reads. Empty/unrelated/human-colliding attribution
// refuses; no private creator field, unrelated disclosure, mutation/execution
// or operator read bypass."
//
// BDD-08.7: "W2 has A-created T, other-agent U, human-name A collision and
// empty creator; explicit W2 list/direct-ID/alternate-list-role query. -> Only
// T's read-only status/resolved metadata; no U/colliding/unattributed
// record/private creator field. No mutation/execution or human/operator lookup
// bypass."
//
// Oracles are read from the spec: an explicit FOREIGN workspace_id on
// list_tasks must (a) mark the response foreign_scope:true, and (b) narrow
// every row to the read-only identity/status of the caller's own tasks — the
// id/title/status/workspace_id/agent_id allowlist, and NOTHING else (no
// prompt/result/description/plan_id/content, no private creator field). A task
// whose Task.CreatedBy collides with the caller's agent id (a human username)
// or whose creator is empty must never be attributed to the caller.

package tools

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// u9fIdentityStatusKeys is the FR-049 allowlist for a foreign row: the task's
// read-only identity/status and its resolved destination pair. Derived from
// FR-049/BDD-08.7 ("read-only status/resolved metadata", "no private creator
// field, unrelated disclosure") — NOT read off foreignTaskStatusRow.
var u9fIdentityStatusKeys = []string{"agent_id", "id", "status", "title", "workspace_id"}

// u9fListResponse decodes a list_tasks envelope into a generic map plus its
// rows, so assertions check the WIRE shape rather than a Go struct.
func u9fListResponse(t *testing.T, forLLM string) (map[string]any, []map[string]any) {
	t.Helper()
	var top map[string]any
	if err := json.Unmarshal([]byte(forLLM), &top); err != nil {
		t.Fatalf("instrument: list_tasks result is not JSON: %v (%q)", err, forLLM)
	}
	rawRows, ok := top["tasks"].([]any)
	if !ok {
		t.Fatalf("instrument: list_tasks result has no tasks array; got %T", top["tasks"])
	}
	rows := make([]map[string]any, 0, len(rawRows))
	for i, rr := range rawRows {
		m, ok := rr.(map[string]any)
		if !ok {
			t.Fatalf("instrument: task row %d is not a JSON object; got %T", i, rr)
		}
		rows = append(rows, m)
	}
	return top, rows
}

// u9fRowIDs returns the id of every row, sorted, for order-independent asserts.
func u9fRowIDs(rows []map[string]any) []string {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		id, _ := r["id"].(string)
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// u9fKeys returns the sorted JSON keys present on a row.
func u9fKeys(row map[string]any) []string {
	keys := make([]string, 0, len(row))
	for k := range row {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func u9fEqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// u9fSeedForeignWorkspace populates the BDD-08.7 board in foreignWS: an
// A-created task T (with content), another agent's task U, a human-name A
// collision, and an empty-creator task. A task in the caller's OWN workspace
// is seeded too, to prove the explicit foreign workspace really narrows.
func u9fSeedForeignWorkspace(t *testing.T, store *task.Store, ownWS, foreignWS, caller string) {
	t.Helper()

	// T — created by the caller (agent path => CreatedByAgentID set), carries
	// content the foreign projection must NOT disclose.
	taskT := &task.Task{
		ID:          "u9f-task-created-by-caller",
		Title:       "T: the caller's own task",
		Prompt:      "SECRET-PROMPT-T",
		Result:      "SECRET-RESULT-T",
		Description: "SECRET-DESCRIPTION-T",
		PlanID:      "SECRET-PLAN-T",
		AgentID:     "worker-b",
		WorkspaceID: foreignWS,
		Status:      task.StatusInProgress,
	}
	if err := store.CreateByAgent(taskT, caller); err != nil {
		t.Fatalf("seed T: %v", err)
	}

	// U — created by a DIFFERENT agent: unrelated to the caller, must not appear.
	taskU := &task.Task{
		ID:          "u9f-task-other-agent",
		Title:       "U: another agent's task",
		Prompt:      "SECRET-PROMPT-U",
		WorkspaceID: foreignWS,
		Status:      task.StatusNext,
	}
	if err := store.CreateByAgent(taskU, "other-agent"); err != nil {
		t.Fatalf("seed U: %v", err)
	}

	// Human-name collision: a human whose USERNAME equals the caller's agent id
	// wrote Task.CreatedBy, but CreatedByAgentID is empty (REST/human path).
	collision := &task.Task{
		ID:          "u9f-task-human-collision",
		Title:       "SECRET-TITLE-human-collision",
		Prompt:      "SECRET-PROMPT-collision",
		CreatedBy:   caller, // username collides with the agent id
		WorkspaceID: foreignWS,
		Status:      task.StatusNext,
	}
	if err := store.Create(collision); err != nil {
		t.Fatalf("seed collision: %v", err)
	}

	// Empty creator: unattributed, must be attributed to nobody.
	unattributed := &task.Task{
		ID:          "u9f-task-unattributed",
		Title:       "SECRET-TITLE-unattributed",
		WorkspaceID: foreignWS,
		Status:      task.StatusNext,
	}
	if err := store.Create(unattributed); err != nil {
		t.Fatalf("seed unattributed: %v", err)
	}

	// A caller-created task in the caller's OWN workspace: must not leak into
	// an explicit foreign read.
	ownTask := &task.Task{
		ID:          "u9f-task-own-workspace",
		Title:       "own workspace task",
		Prompt:      "own prompt",
		WorkspaceID: ownWS,
		Status:      task.StatusNext,
	}
	if err := store.CreateByAgent(ownTask, caller); err != nil {
		t.Fatalf("seed own-workspace task: %v", err)
	}
}

// FR-049/BDD-08.7 (delegator): an explicit foreign workspace_id must set
// foreign_scope:true and return ONLY the caller's own created task, projected
// to the id/title/status/workspace_id/agent_id allowlist — no prompt, result,
// description or plan content.
func TestSessionCoreU9_ForeignListTasksDelegatorExposesOnlyStatusIdentity(t *testing.T) {
	const ownWS = "01JXU9FOWNWS0000000000001"
	const foreignWS = "01JXU9FFOREIGNWS000000001"
	const caller = "creator-a"

	store := task.New(t.TempDir())
	u9fSeedForeignWorkspace(t, store, ownWS, foreignWS, caller)
	tool := NewTaskListTool(store)

	ctx := WithAgentID(context.Background(), caller)
	ctx = WithWorkspaceID(ctx, ownWS)

	res := tool.Execute(ctx, map[string]any{"role": "delegator", "workspace_id": foreignWS})
	if res == nil || res.IsError {
		t.Fatalf("FR-049: an explicit foreign list_tasks must succeed, got %+v", res)
	}

	top, rows := u9fListResponse(t, res.ForLLM)

	if got, _ := top["foreign_scope"].(bool); !got {
		t.Fatalf("FR-049: a foreign read must report foreign_scope:true, got %v (payload %v)",
			top["foreign_scope"], top)
	}
	if got, _ := top["workspace_scoped"].(bool); !got {
		t.Fatalf("FR-046: an explicit workspace_id must report workspace_scoped:true, got %v", top["workspace_scoped"])
	}

	gotIDs := u9fRowIDs(rows)
	wantIDs := []string{"u9f-task-created-by-caller"}
	if !u9fEqualStrings(gotIDs, wantIDs) {
		t.Fatalf("FR-049/BDD-08.7: a foreign delegator read must return ONLY the caller's own created task %v, got %v",
			wantIDs, gotIDs)
	}

	row := rows[0]
	if got, want := u9fKeys(row), u9fIdentityStatusKeys; !u9fEqualStrings(got, want) {
		t.Fatalf("FR-049: a foreign row must expose ONLY the identity/status allowlist %v, got %v (row %v)",
			want, got, row)
	}
	if row["id"] != "u9f-task-created-by-caller" {
		t.Errorf("row id = %v, want the caller's task", row["id"])
	}
	if row["title"] != "T: the caller's own task" {
		t.Errorf("row title = %v, want %q", row["title"], "T: the caller's own task")
	}
	if row["status"] != string(task.StatusInProgress) {
		t.Errorf("row status = %v, want %q", row["status"], task.StatusInProgress)
	}
	if row["workspace_id"] != foreignWS {
		t.Errorf("row workspace_id = %v, want %q", row["workspace_id"], foreignWS)
	}
	if row["agent_id"] != "worker-b" {
		t.Errorf("row agent_id = %v, want %q", row["agent_id"], "worker-b")
	}

	// Defence in depth: no content string may appear anywhere in the payload.
	for _, secret := range []string{
		"SECRET-PROMPT-T", "SECRET-RESULT-T", "SECRET-DESCRIPTION-T", "SECRET-PLAN-T",
	} {
		if strings.Contains(res.ForLLM, secret) {
			t.Fatalf("FR-049: foreign read leaked task content %q into the payload: %s", secret, res.ForLLM)
		}
	}
}

// FR-049/BDD-08.7: a task whose Task.CreatedBy collides with the caller's agent
// id (a human username) and a task with an empty creator MUST NOT be attributed
// to the caller — the delegator read must exclude both, plus the other agent's
// task and the caller's own-workspace task.
func TestSessionCoreU9_ForeignListTasksRefusesCollidingAndUnattributedCreators(t *testing.T) {
	const ownWS = "01JXU9FOWNWS0000000000002"
	const foreignWS = "01JXU9FFOREIGNWS000000002"
	const caller = "creator-a"

	store := task.New(t.TempDir())
	u9fSeedForeignWorkspace(t, store, ownWS, foreignWS, caller)
	tool := NewTaskListTool(store)

	ctx := WithAgentID(context.Background(), caller)
	ctx = WithWorkspaceID(ctx, ownWS)

	res := tool.Execute(ctx, map[string]any{"role": "delegator", "workspace_id": foreignWS})
	if res == nil || res.IsError {
		t.Fatalf("FR-049: an explicit foreign list_tasks must succeed, got %+v", res)
	}
	_, rows := u9fListResponse(t, res.ForLLM)
	ids := u9fRowIDs(rows)

	for _, forbidden := range []string{
		"u9f-task-human-collision", // Task.CreatedBy == caller, CreatedByAgentID == ""
		"u9f-task-unattributed",    // empty creator
		"u9f-task-other-agent",     // unrelated agent
		"u9f-task-own-workspace",   // different workspace
	} {
		if containsString(ids, forbidden) {
			t.Fatalf("FR-049/BDD-08.7: %q must NOT be attributed to the caller; got rows %v", forbidden, ids)
		}
	}
	for _, secret := range []string{"SECRET-TITLE-human-collision", "SECRET-TITLE-unattributed"} {
		if strings.Contains(res.ForLLM, secret) {
			t.Fatalf("FR-049: a colliding/unattributed task title %q leaked into the payload: %s", secret, res.ForLLM)
		}
	}
}

// FR-049/BDD-08.7 (alternate-list-role): the assignee role is the other read
// shape. On an explicit foreign workspace it must ALSO narrow to the
// identity/status allowlist and return only the task assigned to the caller.
func TestSessionCoreU9_ForeignListTasksAssigneeExposesOnlyStatusIdentity(t *testing.T) {
	const ownWS = "01JXU9FOWNWS0000000000003"
	const foreignWS = "01JXU9FFOREIGNWS000000003"
	const caller = "creator-a"

	store := task.New(t.TempDir())

	assigned := &task.Task{
		ID:          "u9f-task-assigned-to-caller",
		Title:       "assigned to the caller",
		Prompt:      "SECRET-PROMPT-assigned",
		Result:      "SECRET-RESULT-assigned",
		AgentID:     caller,
		WorkspaceID: foreignWS,
		// in_progress, not blocked: Task.StatusBlocked is an auto side-state
		// derived from BlockedBy (store.recomputeBlockedStateLocked), so a
		// blocked seed with no unmet dependency is normalised back to next.
		Status: task.StatusInProgress,
	}
	if err := store.CreateByAgent(assigned, "someone-else"); err != nil {
		t.Fatalf("seed assigned task: %v", err)
	}

	other := &task.Task{
		ID:          "u9f-task-assigned-to-other",
		Title:       "assigned to another agent",
		AgentID:     "other-agent",
		WorkspaceID: foreignWS,
		Status:      task.StatusNext,
	}
	if err := store.CreateByAgent(other, caller); err != nil {
		t.Fatalf("seed other-assignee task: %v", err)
	}

	tool := NewTaskListTool(store)
	ctx := WithAgentID(context.Background(), caller)
	ctx = WithWorkspaceID(ctx, ownWS)

	res := tool.Execute(ctx, map[string]any{"role": "assignee", "workspace_id": foreignWS})
	if res == nil || res.IsError {
		t.Fatalf("FR-049: an explicit foreign assignee list_tasks must succeed, got %+v", res)
	}
	top, rows := u9fListResponse(t, res.ForLLM)

	if got, _ := top["foreign_scope"].(bool); !got {
		t.Fatalf("FR-049: a foreign assignee read must report foreign_scope:true, got %v", top["foreign_scope"])
	}
	gotIDs := u9fRowIDs(rows)
	wantIDs := []string{"u9f-task-assigned-to-caller"}
	if !u9fEqualStrings(gotIDs, wantIDs) {
		t.Fatalf("FR-049: a foreign assignee read must return ONLY the task assigned to the caller %v, got %v",
			wantIDs, gotIDs)
	}
	if got, want := u9fKeys(rows[0]), u9fIdentityStatusKeys; !u9fEqualStrings(got, want) {
		t.Fatalf("FR-049: a foreign assignee row must expose ONLY %v, got %v (row %v)", want, got, rows[0])
	}
	if rows[0]["status"] != string(task.StatusInProgress) {
		t.Errorf("row status = %v, want %q", rows[0]["status"], task.StatusInProgress)
	}
	for _, secret := range []string{"SECRET-PROMPT-assigned", "SECRET-RESULT-assigned"} {
		if strings.Contains(res.ForLLM, secret) {
			t.Fatalf("FR-049: foreign assignee read leaked content %q: %s", secret, res.ForLLM)
		}
	}
}

// Control: an OWN-workspace read (workspace_id == caller's own) is NOT foreign —
// it must keep the FULL projection (prompt/result present) and must not claim
// foreign_scope. This proves the narrowing is foreign-specific, not a blanket
// strip.
func TestSessionCoreU9_OwnWorkspaceListTasksKeepsFullProjection(t *testing.T) {
	const ownWS = "01JXU9FOWNWS0000000000004"
	const caller = "creator-a"

	store := task.New(t.TempDir())
	ownTask := &task.Task{
		ID:          "u9f-task-own-full",
		Title:       "own task",
		Prompt:      "OWN-PROMPT",
		Result:      "OWN-RESULT",
		AgentID:     "worker-b",
		WorkspaceID: ownWS,
		Status:      task.StatusNext,
	}
	if err := store.CreateByAgent(ownTask, caller); err != nil {
		t.Fatalf("seed own task: %v", err)
	}

	tool := NewTaskListTool(store)
	ctx := WithAgentID(context.Background(), caller)
	ctx = WithWorkspaceID(ctx, ownWS)

	res := tool.Execute(ctx, map[string]any{"role": "delegator", "workspace_id": ownWS})
	if res == nil || res.IsError {
		t.Fatalf("own-workspace list_tasks must succeed, got %+v", res)
	}
	top, rows := u9fListResponse(t, res.ForLLM)

	if got, _ := top["foreign_scope"].(bool); got {
		t.Fatalf("an own-workspace read must NOT be flagged foreign_scope; got %v", top)
	}
	if len(rows) != 1 {
		t.Fatalf("own-workspace read must return the one own task, got %d rows", len(rows))
	}
	if rows[0]["prompt"] != "OWN-PROMPT" {
		t.Errorf("own-workspace row must keep its prompt, got %v (row %v)", rows[0]["prompt"], rows[0])
	}
	if rows[0]["result"] != "OWN-RESULT" {
		t.Errorf("own-workspace row must keep its result, got %v", rows[0]["result"])
	}
}
