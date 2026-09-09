package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// seedTask creates a task in the store and returns it. Fails the test on error.
func seedTask(t *testing.T, store *task.Store, agentID, createdBy, wsID string) *task.Task {
	t.Helper()
	tk := &task.Task{
		Title:       "test task",
		Prompt:      "do something",
		Action:      task.ActionLLM,
		AgentID:     agentID,
		CreatedBy:   createdBy,
		WorkspaceID: wsID,
		Status:      task.StatusNext,
	}
	if err := store.Create(tk); err != nil {
		t.Fatalf("seedTask: create: %v", err)
	}
	return tk
}

// validCriteriaArg returns a minimal well-formed "criteria" tool-call
// argument (one prose criterion) — the shape create_task's Execute now
// requires at least one of (FR-6/D5 strict criteria enforcement, review r1
// major M5). Tests whose concern is something other than criteria
// enforcement itself use this to stay unaffected by that requirement.
func validCriteriaArg() []any {
	return []any{
		map[string]any{"kind": "prose", "text": "the work is done"},
	}
}

// seedWorkspaceDefault writes a minimal workspace JSON with is_default:true under
// <home>/workspaces/<id>.json so workspace.ResolveDefaultID finds it.
func seedWorkspaceDefault(t *testing.T, home, id string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("seedWorkspaceDefault: mkdir: %v", err)
	}
	data, err := json.Marshal(map[string]any{"id": id, "is_default": true, "name": "My Workspace"})
	if err != nil {
		t.Fatalf("seedWorkspaceDefault: marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), data, 0o600); err != nil {
		t.Fatalf("seedWorkspaceDefault: write: %v", err)
	}
}

// TestTaskDelete_OwnershipRejection proves a non-owner cannot delete a task.
func TestTaskDelete_OwnershipRejection(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-owner", "creator-a", "ws-1")

	tool := NewTaskDeleteTool(store)
	ctx := WithAgentID(context.Background(), "intruder")
	result := tool.Execute(ctx, map[string]any{"task_id": tk.ID})
	if !result.IsError {
		t.Fatal("expected ownership rejection on delete")
	}
	if !strings.Contains(result.ForLLM, "you can only modify/delete") {
		t.Errorf("unexpected rejection message: %s", result.ForLLM)
	}

	// Task must still exist.
	if _, err := store.Get(tk.ID); err != nil {
		t.Errorf("task should still exist after rejected delete, got: %v", err)
	}
}

// TestTaskDelete_OwnerAllowed proves AgentID (owner) can delete.
func TestTaskDelete_OwnerAllowed(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-owner", "creator-a", "ws-1")

	tool := NewTaskDeleteTool(store)
	ctx := WithAgentID(context.Background(), "agent-owner")
	result := tool.Execute(ctx, map[string]any{"task_id": tk.ID})
	if result.IsError {
		t.Fatalf("owner delete failed: %s", result.ForLLM)
	}
}

// TestTaskDelete_MixedNamespaceCreatorRejected proves the creator clause reads
// the AGENT-namespaced attribution and nothing else.
//
// This test previously asserted the opposite ("CreatedBy can also delete") and
// passed, because seedTask writes its createdBy straight into the
// mixed-namespace Task.CreatedBy — the field whose own doc comment says it
// "MUST NEVER be used as an ownership or authorization predicate", because the
// REST path writes a human USERNAME into it. A task carrying only that (no
// CreatedByAgentID) is either a human's or a pre-attribution record, and is
// nobody's to delete via an agent tool. The legitimate creator case — an agent
// deleting what it created through Store.CreateByAgent — is covered by
// TestTaskDelete_AgentCreatorAllowed in task_scope_test.go.
func TestTaskDelete_MixedNamespaceCreatorRejected(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-owner", "creator-a", "ws-1")

	tool := NewTaskDeleteTool(store)
	ctx := WithAgentID(context.Background(), "creator-a")
	result := tool.Execute(ctx, map[string]any{"task_id": tk.ID})
	if !result.IsError {
		t.Fatal("a caller matching only the mixed-namespace created_by must not be able to delete")
	}
	if _, err := store.Get(tk.ID); err != nil {
		t.Errorf("task must survive a denied delete, got: %v", err)
	}
}

// TestTaskDelete_AdvancesBlockedDependent proves that deleting a blocker task
// advances a still-blocked dependent to `next`.
func TestTaskDelete_AdvancesBlockedDependent(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())

	blocker := seedTask(t, store, "agent-a", "agent-a", "ws-1")
	dependent := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	// Wire the dependency via the store directly (bypasses the tool ownership
	// check so we can set up the state without needing matching callerID for
	// the setup step).
	if _, _, err := store.AddDependency(dependent.ID, blocker.ID); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	// After adding the dependency the dependent should be `blocked`.
	dep, err := store.Get(dependent.ID)
	if err != nil {
		t.Fatalf("get dependent after AddDependency: %v", err)
	}
	if dep.Status != task.StatusBlocked {
		t.Fatalf("expected dependent to be blocked, got %q", dep.Status)
	}

	// Now delete the blocker via the tool.
	tool := NewTaskDeleteTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")
	result := tool.Execute(ctx, map[string]any{"task_id": blocker.ID})
	if result.IsError {
		t.Fatalf("delete blocker failed: %s", result.ForLLM)
	}

	// The dependent must now be `next`.
	dep, err = store.Get(dependent.ID)
	if err != nil {
		t.Fatalf("get dependent after blocker delete: %v", err)
	}
	if dep.Status != task.StatusNext {
		t.Errorf("expected dependent to advance to next, got %q", dep.Status)
	}
}

// TestResolveWorkspaceID_FromContext proves that a workspace ID bound in the
// context is returned directly without hitting disk.
func TestResolveWorkspaceID_FromContext(t *testing.T) {
	t.Parallel()
	tool := &TaskCreateTool{}
	ctx := WithWorkspaceID(context.Background(), "ws-from-ctx")
	id, err := tool.resolveWorkspaceID(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "ws-from-ctx" {
		t.Errorf("expected 'ws-from-ctx', got %q", id)
	}
}

// TestResolveWorkspaceID_FromHome proves that when ctx has no workspace ID but
// home is set, the real default workspace ULID is returned.
func TestResolveWorkspaceID_FromHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	const wantID = "01JXDEFAULT000000000000001"
	seedWorkspaceDefault(t, home, wantID)

	tool := &TaskCreateTool{home: home}
	id, err := tool.resolveWorkspaceID(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != wantID {
		t.Errorf("expected %q, got %q", wantID, id)
	}
}

// TestResolveWorkspaceID_NoDefault proves an error when no default workspace exists.
func TestResolveWorkspaceID_NoDefault(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	// workspaces/ dir exists but has no is_default workspace
	if err := os.MkdirAll(filepath.Join(home, "workspaces"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tool := &TaskCreateTool{home: home}
	_, err := tool.resolveWorkspaceID(context.Background())
	if err == nil {
		t.Fatal("expected error when no default workspace exists")
	}
}

// TestResolveWorkspaceID_NoHomeNoCtx proves an error when neither ctx workspace
// nor home is configured.
func TestResolveWorkspaceID_NoHomeNoCtx(t *testing.T) {
	t.Parallel()
	tool := &TaskCreateTool{} // home == ""
	_, err := tool.resolveWorkspaceID(context.Background())
	if err == nil {
		t.Fatal("expected error when home is not set and ctx has no workspace")
	}
	if !strings.Contains(err.Error(), "no active workspace") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestTaskCreateTool_WorkspaceFromCtx proves task_create uses the ctx workspace.
func TestTaskCreateTool_WorkspaceFromCtx(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)
	// This test's concern is workspace resolution, not the delegation-policy
	// gate — wire a permissive deny-checker so the (fail-closed per the
	// 7-reviewer-gate fix) gate doesn't mask the assertion under test.
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(context.Background(), "caller")
	ctx = WithWorkspaceID(ctx, "ws-explicit")

	result := tool.Execute(ctx, map[string]any{
		"title":    "test",
		"prompt":   "do it",
		"agent_id": "agent-b",
		"criteria": validCriteriaArg(),
	})
	if result.IsError {
		t.Fatalf("task_create failed: %s", result.ForLLM)
	}

	tasks, err := store.List(task.Filter{WorkspaceID: "ws-explicit"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task in ws-explicit, got %d", len(tasks))
	}
}

// TestResolveWorkspaceID_StaleCtxFallsBackToDefault proves the M4
// belt-and-suspenders fix: a ctx-bound workspace ID that does NOT exist on disk
// (stale/typo'd) is treated as unbound, so the task lands on the real default
// rather than an invisible board.
func TestResolveWorkspaceID_StaleCtxFallsBackToDefault(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	const wantID = "01JXDEFAULT000000000000009"
	seedWorkspaceDefault(t, home, wantID)

	tool := &TaskCreateTool{home: home}
	// Bind a workspace id that was never written to disk.
	ctx := WithWorkspaceID(context.Background(), "01JXSTALE0000000000000099")
	id, err := tool.resolveWorkspaceID(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != wantID {
		t.Errorf("expected stale ctx id to fall back to default %q, got %q", wantID, id)
	}
}

// TestTaskCreateTool_StaleCtxWorkspace_LandsOnDefault proves task_create with a
// non-existent ctx workspace_id persists the task on the default workspace, not
// the bogus id.
func TestTaskCreateTool_StaleCtxWorkspace_LandsOnDefault(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	const defaultWS = "01JXDEFAULT000000000000010"
	seedWorkspaceDefault(t, home, defaultWS)

	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)
	tool.SetHome(home)
	// This test's concern is workspace resolution, not the delegation-policy gate.
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(context.Background(), "caller")
	ctx = WithWorkspaceID(ctx, "01JXBOGUS00000000000000099")
	result := tool.Execute(ctx, map[string]any{
		"title":    "test",
		"prompt":   "do it",
		"agent_id": "agent-b",
		"criteria": validCriteriaArg(),
	})
	if result.IsError {
		t.Fatalf("task_create failed: %s", result.ForLLM)
	}

	onDefault, err := store.List(task.Filter{WorkspaceID: defaultWS})
	if err != nil {
		t.Fatalf("list default: %v", err)
	}
	if len(onDefault) != 1 {
		t.Errorf("expected task on default workspace %q, got %d", defaultWS, len(onDefault))
	}
	onBogus, err := store.List(task.Filter{WorkspaceID: "01JXBOGUS00000000000000099"})
	if err != nil {
		t.Fatalf("list bogus: %v", err)
	}
	if len(onBogus) != 0 {
		t.Errorf("expected zero tasks on the bogus workspace, got %d", len(onBogus))
	}
}

// TestTaskCreateTool_WorkspaceFromHome proves task_create resolves the default
// workspace when the context has no bound workspace but SetHome is called.
func TestTaskCreateTool_WorkspaceFromHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	const defaultWS = "01JXDEFAULT000000000000002"
	seedWorkspaceDefault(t, home, defaultWS)

	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)
	tool.SetHome(home)
	// This test's concern is workspace resolution, not the delegation-policy gate.
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(context.Background(), "caller")
	result := tool.Execute(ctx, map[string]any{
		"title":    "test",
		"prompt":   "do it",
		"agent_id": "agent-b",
		"criteria": validCriteriaArg(),
	})
	if result.IsError {
		t.Fatalf("task_create failed: %s", result.ForLLM)
	}

	tasks, err := store.List(task.Filter{WorkspaceID: defaultWS})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task in default workspace %q, got %d", defaultWS, len(tasks))
	}
}

// TestTaskCreateTool_NoWorkspaceError proves task_create returns an error when no
// workspace can be resolved (no ctx, no home).
func TestTaskCreateTool_NoWorkspaceError(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store) // home not set
	// This test's concern is workspace resolution, not the delegation-policy gate.
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(context.Background(), "caller")
	result := tool.Execute(ctx, map[string]any{
		"title":    "test",
		"prompt":   "do it",
		"agent_id": "agent-b",
		"criteria": validCriteriaArg(),
	})
	if !result.IsError {
		t.Fatal("expected error when no workspace can be resolved")
	}
	if !strings.Contains(result.ForLLM, "could not resolve workspace") {
		t.Errorf("unexpected error message: %s", result.ForLLM)
	}
}

// TestTaskCreateTool_DelegationDepthStamped proves a task_create issued from a
// root (depth-0) context stamps the child task generation 1, and that a create
// issued from within a task run carries generation parent+1.
func TestTaskCreateTool_DelegationDepthStamped(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)
	tool.SetMaxDelegationDepth(10)
	// This test's concern is delegation-depth stamping, not the
	// delegation-policy trust gate.
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	// Root context (no delegation depth) → child stamped generation 1.
	ctx := WithAgentID(context.Background(), "caller")
	ctx = WithWorkspaceID(ctx, "ws")
	res := tool.Execute(ctx, map[string]any{"title": "t1", "prompt": "p", "agent_id": "b", "criteria": validCriteriaArg()})
	if res.IsError {
		t.Fatalf("root create failed: %s", res.ForLLM)
	}
	rootTasks, _ := store.List(task.Filter{WorkspaceID: "ws"})
	if len(rootTasks) != 1 || rootTasks[0].DelegationDepth != 1 {
		t.Fatalf("expected one task at generation 1, got %+v", rootTasks)
	}

	// Context at generation 4 → child stamped generation 5.
	ctx5 := WithDelegationDepth(ctx, 4)
	res = tool.Execute(ctx5, map[string]any{"title": "t2", "prompt": "p", "agent_id": "b", "criteria": validCriteriaArg()})
	if res.IsError {
		t.Fatalf("nested create failed: %s", res.ForLLM)
	}
	all, _ := store.List(task.Filter{WorkspaceID: "ws"})
	var found bool
	for _, tk := range all {
		if tk.Title == "t2" {
			found = true
			if tk.DelegationDepth != 5 {
				t.Errorf("expected nested task generation 5, got %d", tk.DelegationDepth)
			}
		}
	}
	if !found {
		t.Fatal("nested task t2 not found")
	}
}

// TestTaskCreateTool_DelegationDepthBound proves a task→task chain is rejected
// once it would exceed maxTaskDepth — closing the A→B→A unbounded-recursion gap.
func TestTaskCreateTool_DelegationDepthBound(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)
	tool.SetMaxDelegationDepth(10)
	// This test's concern is the depth ceiling, not the delegation-policy trust gate.
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(context.Background(), "caller")
	ctx = WithWorkspaceID(ctx, "ws")

	// At generation 9, a create yields generation 10 == ceiling → allowed.
	ctxAt9 := WithDelegationDepth(ctx, 9)
	if res := tool.Execute(ctxAt9, map[string]any{"title": "ok", "prompt": "p", "agent_id": "b", "criteria": validCriteriaArg()}); res.IsError {
		t.Fatalf("create at the ceiling boundary must be allowed, got: %s", res.ForLLM)
	}

	// At generation 10, a create would yield generation 11 > ceiling → rejected.
	ctxAt10 := WithDelegationDepth(ctx, 10)
	res := tool.Execute(ctxAt10, map[string]any{"title": "blocked", "prompt": "p", "agent_id": "b", "criteria": validCriteriaArg()})
	if !res.IsError {
		t.Fatal("expected task_create past the depth ceiling to be rejected")
	}
	if !strings.Contains(res.ForLLM, "maximum task delegation depth") {
		t.Errorf("unexpected error message: %s", res.ForLLM)
	}

	// The rejected task must NOT have been persisted.
	all, _ := store.List(task.Filter{WorkspaceID: "ws"})
	for _, tk := range all {
		if tk.Title == "blocked" {
			t.Fatal("a rejected over-depth task must not be persisted")
		}
	}
}

// TestTaskTool_CreateWithoutCriteria_Rejected is review r1 major M5 (FR-6/D5
// strict criteria enforcement, ADR-049 SD-A7): an agent-created task with
// zero acceptance criteria must be rejected outright — neither an absent
// "criteria" arg nor an explicitly empty array may slip through.
func TestTaskTool_CreateWithoutCriteria_Rejected(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })
	ctx := WithAgentID(context.Background(), "caller")
	ctx = WithWorkspaceID(ctx, "ws")

	cases := []struct {
		name string
		args map[string]any
	}{
		{"absent", map[string]any{"title": "no criteria field", "prompt": "p", "agent_id": "b"}},
		{"empty_array", map[string]any{"title": "empty criteria", "prompt": "p", "agent_id": "b", "criteria": []any{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := tool.Execute(ctx, tc.args)
			if !res.IsError {
				t.Fatal("expected an agent-created task with zero criteria to be rejected")
			}
			if !strings.Contains(res.ForLLM, "criteria is required") {
				t.Errorf("unexpected error message: %s", res.ForLLM)
			}
		})
	}

	// Nothing must have been persisted for either rejected case.
	all, _ := store.List(task.Filter{WorkspaceID: "ws"})
	if len(all) != 0 {
		t.Fatalf("expected zero tasks persisted, got %d: %+v", len(all), all)
	}
}

// TestJudge_AllMachineCriteria_UnsatisfiableBashPolicy_RejectedAtWrite is
// review r1 major M5 / ADR-049 D2 rule 5 (FR-017/052): create_task must
// reject a criteria set that is ALL kind=check when the assignee's effective
// bash policy is deny or ask — a machine check that can never even run can
// never be adjudicated MET, so the write is rejected at create time instead
// of letting the goal loop retry forever against an unsatisfiable DoD.
func TestJudge_AllMachineCriteria_UnsatisfiableBashPolicy_RejectedAtWrite(t *testing.T) {
	t.Parallel()
	checkCriterion := []any{
		map[string]any{
			"kind": "check",
			"text": "tests pass",
			"check": map[string]any{
				"command":            "go test ./...",
				"expected_exit_code": float64(0),
			},
		},
	}

	cases := []struct {
		name       string
		policy     string
		policyOK   bool
		wantReject bool
	}{
		{"deny_rejected", "deny", true, true},
		{"ask_rejected", "ask", true, true},
		{"allow_accepted", "allow", true, false},
		{"unresolvable_assignee_rejected", "", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := task.New(t.TempDir())
			tool := NewTaskCreateTool(store)
			tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })
			tool.SetBashPolicyChecker(func(assigneeAgentID string) (string, bool) {
				return tc.policy, tc.policyOK
			})
			ctx := WithAgentID(context.Background(), "caller")
			ctx = WithWorkspaceID(ctx, "ws")

			res := tool.Execute(ctx, map[string]any{
				"title":    "all-machine criteria",
				"prompt":   "p",
				"agent_id": "assignee",
				"criteria": checkCriterion,
			})
			if tc.wantReject {
				if !res.IsError {
					t.Fatalf("expected rejection for bash policy %q (ok=%v), got success", tc.policy, tc.policyOK)
				}
				if !strings.Contains(res.ForLLM, "D2 rule 5") {
					t.Errorf("expected the D2 rule 5 message, got: %s", res.ForLLM)
				}
			} else if res.IsError {
				t.Fatalf("expected success for bash policy %q, got error: %s", tc.policy, res.ForLLM)
			}
		})
	}
}

// TestTaskTool_CreateWithoutBashPolicyChecker_AllCheckCriteria_FailsClosed
// proves the D2 rule 5 checker itself fails CLOSED (denies) when unwired —
// mirroring the delegation-deny gate's own unwired-fails-closed discipline
// (CLAUDE.md hard constraint 6: no default policy fallback).
func TestTaskTool_CreateWithoutBashPolicyChecker_AllCheckCriteria_FailsClosed(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })
	// tool.SetBashPolicyChecker deliberately left unset.
	ctx := WithAgentID(context.Background(), "caller")
	ctx = WithWorkspaceID(ctx, "ws")

	res := tool.Execute(ctx, map[string]any{
		"title":    "all-machine, no checker wired",
		"prompt":   "p",
		"agent_id": "assignee",
		"criteria": []any{
			map[string]any{
				"kind": "check", "text": "tests pass",
				"check": map[string]any{"command": "go test ./...", "expected_exit_code": float64(0)},
			},
		},
	})
	if !res.IsError {
		t.Fatal("expected an unwired D2 rule 5 checker to fail CLOSED (deny) for an all-check criteria set")
	}
}

// TestTaskDelete_NotFound proves a clear error on unknown task ID.
func TestTaskDelete_NotFound(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskDeleteTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")
	result := tool.Execute(ctx, map[string]any{"task_id": "nonexistent-id"})
	if !result.IsError {
		t.Fatal("expected error for non-existent task")
	}
	if !strings.Contains(result.ForLLM, "not found") {
		t.Errorf("unexpected message: %s", result.ForLLM)
	}
}

// TestSetHome_Setter verifies SetHome sets the home field so the resolver can
// use it. This documents the public API the agent loop must call.
func TestSetHome_Setter(t *testing.T) {
	t.Parallel()
	tool := NewTaskCreateTool(nil)
	if tool.home != "" {
		t.Error("home must be empty before SetHome")
	}
	tool.SetHome("/some/path")
	if tool.home != "/some/path" {
		t.Errorf("home not set, got %q", tool.home)
	}
}

// TestTaskDelete_MissingAgentID proves that a missing caller ID returns an error.
func TestTaskDelete_MissingAgentID(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-owner", "agent-owner", "ws-1")

	tool := NewTaskDeleteTool(store)
	result := tool.Execute(context.Background(), map[string]any{"task_id": tk.ID})
	if !result.IsError {
		t.Fatal("expected error when no agent ID in context")
	}
	if !strings.Contains(result.ForLLM, "agent ID not set") {
		t.Errorf("unexpected message: %s", result.ForLLM)
	}
}

// Compile-time check: ensure SetHome is the documented wiring point.
var _ = fmt.Sprintf // suppress import if no other fmt use

// --- Broadened task_update / task_create tests (blocked_by + field patches) ---
//
// Spec of to-be behavior (backend-lead editing task.go in parallel):
//   - task_update gains optional title / priority / due / assignee / blocked_by
//     alongside existing task_id / status / result / artifacts. Only-provided
//     fields apply via task.Patch + store.Update. status→done calls
//     store.AdvanceBlockedDependents. blocked_by is cycle-checked and
//     cross-workspace guarded (mirroring TaskAddDependencyTool).
//   - task_create gains optional blocked_by (cycle-checked at creation).
//
// These tests assert the SPEC behavior. If the backend-lead's impl differs, the
// review/fix round reconciles — we do NOT edit task.go here.

// TaskUpdate status constants used in field-patch tests (imported from pkg/task
// would be task.StatusDone etc.; re-aliased here for readability).
const (
	updStatusDone   = "done"
	updStatusFailed = "failed"
	updStatusInProg = "in_progress"
)

// TestTaskUpdate_EditsTitle proves task_update edits title and leaves other
// fields untouched (only-provided-applied).
func TestTaskUpdate_EditsTitle(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")
	originalPriority := tk.Priority
	originalDue := tk.Due

	tool := NewTaskUpdateTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	// Issue #593: status is deliberately OMITTED here (it was incidental
	// filler value updStatusInProg before the in_progress-forge guard landed —
	// this test's actual concern is title-only field-patch semantics).
	res := tool.Execute(ctx, map[string]any{
		"task_id": tk.ID,
		"title":   "renamed title",
	})
	if res.IsError {
		t.Fatalf("task_update title: %s", res.ForLLM)
	}

	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "renamed title" {
		t.Errorf("expected title updated to 'renamed title', got %q", got.Title)
	}
	// Only-provided-applied: priority and due must be unchanged.
	if got.Priority != originalPriority {
		t.Errorf("priority must be unchanged, got %d (was %d)", got.Priority, originalPriority)
	}
	if got.Due != originalDue {
		t.Errorf("due must be unchanged, got %q (was %q)", got.Due, originalDue)
	}
}

// TestTaskUpdate_EditsPriorityAndDue proves task_update edits priority (1-5) and
// due (RFC3339). Two different inputs produce two different outputs
// (differentiation test — catches a hardcoded response).
func TestTaskUpdate_EditsPriorityAndDue(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	tool := NewTaskUpdateTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	dueA := "2026-07-01T12:00:00Z"
	dueB := "2026-08-15T09:30:00Z"

	// Issue #593: status omitted (incidental filler before the in_progress-forge
	// guard — this test's concern is priority/due, not status).
	if res := tool.Execute(ctx, map[string]any{
		"task_id":  tk.ID,
		"priority": float64(2),
		"due":      dueA,
	}); res.IsError {
		t.Fatalf("first update: %s", res.ForLLM)
	}
	got, _ := store.Get(tk.ID)
	if got.Priority != 2 {
		t.Errorf("expected priority 2, got %d", got.Priority)
	}
	if got.Due != dueA {
		t.Errorf("expected due %q, got %q", dueA, got.Due)
	}

	if res := tool.Execute(ctx, map[string]any{
		"task_id":  tk.ID,
		"priority": float64(5),
		"due":      dueB,
	}); res.IsError {
		t.Fatalf("second update: %s", res.ForLLM)
	}
	got, _ = store.Get(tk.ID)
	if got.Priority != 5 {
		t.Errorf("expected priority 5, got %d", got.Priority)
	}
	if got.Due != dueB {
		t.Errorf("expected due %q, got %q", dueB, got.Due)
	}
}

// TestTaskUpdate_EditsAgentID proves task_update edits the assignee via the
// agent_id param (renamed from "assignee"). The store field is still AgentID.
func TestTaskUpdate_EditsAgentID(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	tool := NewTaskUpdateTool(store)
	// This test's concern is agent_id reassignment persisting, not the
	// delegation-policy gate itself — wire a permissive deny-checker so the
	// (now fail-closed-when-unwired, per the 7-reviewer-gate fix) gate
	// doesn't block an otherwise-unrelated test. See
	// TestTaskUpdateTool_NilDenyChecker_FailsClosed for the unwired case.
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })
	ctx := WithAgentID(context.Background(), "agent-a")

	// Issue #593: status omitted (incidental filler — this test's concern is
	// agent_id reassignment, not status).
	res := tool.Execute(ctx, map[string]any{
		"task_id":  tk.ID,
		"agent_id": "agent-b",
	})
	if res.IsError {
		t.Fatalf("task_update agent_id: %s", res.ForLLM)
	}

	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AgentID != "agent-b" {
		t.Errorf("expected agent_id 'agent-b', got %q", got.AgentID)
	}
}

// TestTaskUpdate_SetsBlockedBy proves task_update sets blocked_by and persists.
func TestTaskUpdate_SetsBlockedBy(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	dep := seedTask(t, store, "agent-a", "agent-a", "ws-1")
	blocker := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	tool := NewTaskUpdateTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	// Issue #593: status omitted (incidental filler — this test's concern is
	// blocked_by, not status; leaving status:in_progress here would now be
	// rejected before blocked_by is ever processed, since the seeded task
	// starts at StatusNext).
	res := tool.Execute(ctx, map[string]any{
		"task_id":    dep.ID,
		"blocked_by": []any{blocker.ID},
	})
	if res.IsError {
		t.Fatalf("task_update blocked_by: %s", res.ForLLM)
	}

	got, err := store.Get(dep.ID)
	if err != nil {
		t.Fatalf("get dep: %v", err)
	}
	if len(got.BlockedBy) != 1 || got.BlockedBy[0] != blocker.ID {
		t.Errorf("expected blocked_by=[%s], got %v", blocker.ID, got.BlockedBy)
	}
}

// TestTaskUpdate_BlockedByRejectsCycle proves task_update rejects a cycle
// (a→b→a). We set b blocked_by a, then attempt a blocked_by b — must error.
func TestTaskUpdate_BlockedByRejectsCycle(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	a := seedTask(t, store, "agent-a", "agent-a", "ws-1")
	b := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	// Wire b→a directly via the store so the edge is present without going
	// through the tool's (old) status-only path.
	if _, _, err := store.AddDependency(b.ID, a.ID); err != nil {
		t.Fatalf("seed AddDependency b→a: %v", err)
	}

	tool := NewTaskUpdateTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	// Attempt a→b: forms a cycle a→b→a. Must be rejected. (Issue #593: status
	// omitted — this test's concern is the cycle rejection, not status; the
	// seeded task starts at StatusNext, so an in_progress filler value would
	// now be rejected first, for the wrong reason.)
	res := tool.Execute(ctx, map[string]any{
		"task_id":    a.ID,
		"blocked_by": []any{b.ID},
	})
	if !res.IsError {
		t.Fatal("expected cycle rejection when setting blocked_by that forms a→b→a")
	}
	if !strings.Contains(res.ForLLM, "cycle") {
		t.Errorf("expected 'cycle' in error, got: %s", res.ForLLM)
	}

	// a must NOT have acquired the cyclic edge.
	got, _ := store.Get(a.ID)
	for _, d := range got.BlockedBy {
		if d == b.ID {
			t.Fatal("cyclic blocked_by edge must not persist on a")
		}
	}
}

// TestTaskUpdate_BlockedByRejectsCrossWorkspace proves task_update's blocked_by
// applies the cross-workspace guard (mirrors TaskAddDependencyTool).
func TestTaskUpdate_BlockedByRejectsCrossWorkspace(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	dep := seedTask(t, store, "agent-a", "agent-a", "ws-alpha")
	blocker := seedTask(t, store, "agent-a", "agent-a", "ws-beta")

	tool := NewTaskUpdateTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	// Issue #593: status omitted — see TestTaskUpdate_BlockedByRejectsCycle.
	res := tool.Execute(ctx, map[string]any{
		"task_id":    dep.ID,
		"blocked_by": []any{blocker.ID},
	})
	if !res.IsError {
		t.Fatal("expected cross-workspace rejection")
	}
	if !strings.Contains(res.ForLLM, "different workspace") {
		t.Errorf("expected 'different workspace' in error, got: %s", res.ForLLM)
	}

	// Edge must not persist.
	got, _ := store.Get(dep.ID)
	if len(got.BlockedBy) != 0 {
		t.Errorf("cross-workspace blocked_by must not persist, got %v", got.BlockedBy)
	}
}

// TestTaskUpdate_DoneAdvancesBlockedDependents proves that marking the blocker
// done advances a still-blocked dependent to `next` via
// store.AdvanceBlockedDependents.
func TestTaskUpdate_DoneAdvancesBlockedDependents(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())

	blocker := seedTask(t, store, "agent-a", "agent-a", "ws-1")
	dependent := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	// Wire dependent→blocker directly via the store.
	if _, _, err := store.AddDependency(dependent.ID, blocker.ID); err != nil {
		t.Fatalf("seed AddDependency: %v", err)
	}
	dep, err := store.Get(dependent.ID)
	if err != nil {
		t.Fatalf("get dependent: %v", err)
	}
	if dep.Status != task.StatusBlocked {
		t.Fatalf("expected dependent blocked after AddDependency, got %q", dep.Status)
	}

	tool := NewTaskUpdateTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	res := tool.Execute(ctx, map[string]any{
		"task_id": blocker.ID,
		"status":  updStatusDone,
		"result":  "blocker complete",
	})
	if res.IsError {
		t.Fatalf("task_update done on blocker: %s", res.ForLLM)
	}

	// The dependent must now be advanced to `next`.
	dep, err = store.Get(dependent.ID)
	if err != nil {
		t.Fatalf("get dependent after blocker done: %v", err)
	}
	if dep.Status != task.StatusNext {
		t.Errorf("expected dependent advanced to next, got %q", dep.Status)
	}
}

// TestTaskUpdate_StatusOnlyBackcompat proves a call with only task_id+status
// still works and does NOT touch other fields (back-compat with the old
// status-only tool).
func TestTaskUpdate_StatusOnlyBackcompat(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")
	originalTitle := tk.Title
	originalPriority := tk.Priority
	originalDue := tk.Due

	tool := NewTaskUpdateTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	// Issue #593: updStatusFailed, not updStatusInProg — in_progress is no
	// longer directly settable via update_task (see TestTaskUpdate_
	// InProgressForgeRejected), so this back-compat proof now uses "failed" as
	// its example of a direct, caller-settable status.
	res := tool.Execute(ctx, map[string]any{
		"task_id": tk.ID,
		"status":  updStatusFailed,
	})
	if res.IsError {
		t.Fatalf("status-only task_update: %s", res.ForLLM)
	}

	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != task.StatusFailed {
		t.Errorf("expected status failed, got %q", got.Status)
	}
	// No other fields touched.
	if got.Title != originalTitle {
		t.Errorf("title must be unchanged, got %q (was %q)", got.Title, originalTitle)
	}
	if got.Priority != originalPriority {
		t.Errorf("priority must be unchanged, got %d (was %d)", got.Priority, originalPriority)
	}
	if got.Due != originalDue {
		t.Errorf("due must be unchanged, got %q (was %q)", got.Due, originalDue)
	}
	if len(got.BlockedBy) != 0 {
		t.Errorf("blocked_by must be untouched, got %v", got.BlockedBy)
	}
}

// TestTaskUpdate_DoneFailedTerminal proves the terminal-status paths still
// produce a result for both done and failed (differentiation: two different
// statuses produce two different stored statuses).
func TestTaskUpdate_DoneFailedTerminal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     string
		wantStatus task.Status
	}{
		{"done", updStatusDone, task.StatusDone},
		{"failed", updStatusFailed, task.StatusFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := task.New(t.TempDir())
			tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")
			tool := NewTaskUpdateTool(store)
			ctx := WithAgentID(context.Background(), "agent-a")

			res := tool.Execute(ctx, map[string]any{
				"task_id": tk.ID,
				"status":  tc.status,
				"result":  "finished",
			})
			if res.IsError {
				t.Fatalf("task_update %s: %s", tc.status, res.ForLLM)
			}
			got, err := store.Get(tk.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.Status != tc.wantStatus {
				t.Errorf("expected %q, got %q", tc.wantStatus, got.Status)
			}
		})
	}
}

// TestTaskUpdate_InProgressForgeRejected is the Issue #593 regression test.
//
// update_task previously let ANY permitted caller — including the task's own
// creator/delegator — write status:"in_progress" directly, with no session,
// no goroutine, and no dispatch actually starting: the task then read
// "running" forever on the board. This proves the fix (Option A):
//
//   - (a) a next-status task's creator calling update_task(status:"in_progress")
//     is REJECTED with a message naming run_task as the correct path, and the
//     task is unchanged on disk (re-read to confirm — not just a non-nil error).
//   - (b) positive control: status:"failed" and a plain title edit both still
//     succeed and persist — the guard does not overreach into other status
//     values or other fields.
//   - (c) a task that is ALREADY in_progress is not broken by an unrelated
//     (non-status) update — the guard only fires on a transition INTO
//     in_progress from a different status, never on an already-in_progress
//     task being otherwise edited.
func TestTaskUpdate_InProgressForgeRejected(t *testing.T) {
	t.Parallel()

	t.Run("creator forging next to in_progress is rejected, task unchanged on disk", func(t *testing.T) {
		t.Parallel()
		store := task.New(t.TempDir())
		// Delegator "mia" creates a task assigned to "worker" via the AGENT path
		// (CreatedByAgentID stamped), mirroring the UAT repro in issue #593: the
		// creator is a legitimate update_task caller via the ownership union
		// check, even though the task is assigned to someone else.
		tk := &task.Task{
			Title: "delegated work", Action: task.ActionLLM, Status: task.StatusNext,
			WorkspaceID: "ws-1", AgentID: "worker",
		}
		if err := store.CreateByAgent(tk, "mia"); err != nil {
			t.Fatalf("seed: %v", err)
		}
		beforeUpdatedAt := tk.UpdatedAt

		tool := NewTaskUpdateTool(store)
		ctx := WithAgentID(context.Background(), "mia")

		res := tool.Execute(ctx, map[string]any{
			"task_id": tk.ID,
			"status":  "in_progress",
		})
		if !res.IsError {
			t.Fatalf("expected the forged in_progress write to be rejected, got success: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "run_task") {
			t.Errorf("expected rejection message to name run_task as the correct path, got: %s", res.ForLLM)
		}

		// Read back from disk: status AND updated_at must be untouched — this is
		// not just "an error was returned", it is "nothing was persisted".
		got, err := store.Get(tk.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Status != task.StatusNext {
			t.Errorf("task must remain %q on disk, got %q", task.StatusNext, got.Status)
		}
		if got.StartedAt != "" {
			t.Errorf("started_at must NOT be stamped by a rejected write, got %q", got.StartedAt)
		}
		if got.UpdatedAt != beforeUpdatedAt {
			t.Errorf("updated_at must be unchanged by a rejected write, got %q (was %q)",
				got.UpdatedAt, beforeUpdatedAt)
		}
	})

	t.Run("positive control: failed and title edits still work", func(t *testing.T) {
		t.Parallel()
		store := task.New(t.TempDir())
		tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")
		tool := NewTaskUpdateTool(store)
		ctx := WithAgentID(context.Background(), "agent-a")

		// A title-only edit (no status at all) must still succeed.
		if res := tool.Execute(ctx, map[string]any{
			"task_id": tk.ID,
			"title":   "renamed",
		}); res.IsError {
			t.Fatalf("title edit must succeed: %s", res.ForLLM)
		}
		got, err := store.Get(tk.ID)
		if err != nil {
			t.Fatalf("get after title edit: %v", err)
		}
		if got.Title != "renamed" {
			t.Errorf("expected title 'renamed', got %q", got.Title)
		}

		// status:"failed" (the deliberately-NOT-guarded abort primitive) must
		// still be settable directly.
		if res := tool.Execute(ctx, map[string]any{
			"task_id": tk.ID,
			"status":  "failed",
			"result":  "gave up",
		}); res.IsError {
			t.Fatalf("status:failed must succeed: %s", res.ForLLM)
		}
		got, err = store.Get(tk.ID)
		if err != nil {
			t.Fatalf("get after failed: %v", err)
		}
		if got.Status != task.StatusFailed {
			t.Errorf("expected status failed, got %q", got.Status)
		}
	})

	t.Run("already in_progress: unrelated update is not broken", func(t *testing.T) {
		t.Parallel()
		store := task.New(t.TempDir())
		tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")
		// Move the task to in_progress through a legitimate path (direct store
		// write, standing in for the executor's real ClaimForRun) rather than
		// through update_task, so this sub-test's own seed does not depend on
		// the very tool behavior under test.
		if _, err := store.Update(tk.ID, task.Patch{Status: statusPtr(task.StatusInProgress)}); err != nil {
			t.Fatalf("seed in_progress: %v", err)
		}

		tool := NewTaskUpdateTool(store)
		ctx := WithAgentID(context.Background(), "agent-a")

		// An unrelated field edit (title) must succeed and leave status alone.
		if res := tool.Execute(ctx, map[string]any{
			"task_id": tk.ID,
			"title":   "still running, renamed",
		}); res.IsError {
			t.Fatalf("unrelated update on an already-in_progress task must succeed: %s", res.ForLLM)
		}
		got, err := store.Get(tk.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Status != task.StatusInProgress {
			t.Errorf("status must remain in_progress, got %q", got.Status)
		}
		if got.Title != "still running, renamed" {
			t.Errorf("expected title updated, got %q", got.Title)
		}

		// A resend of status:"in_progress" on an ALREADY in_progress task is a
		// no-op, not a forge, and must be allowed through unchanged.
		if res := tool.Execute(ctx, map[string]any{
			"task_id": tk.ID,
			"status":  "in_progress",
		}); res.IsError {
			t.Fatalf("a same-status resend on an already in_progress task must not be rejected: %s", res.ForLLM)
		}
		got, err = store.Get(tk.ID)
		if err != nil {
			t.Fatalf("get after resend: %v", err)
		}
		if got.Status != task.StatusInProgress {
			t.Errorf("status must remain in_progress after resend, got %q", got.Status)
		}
	})
}

// statusPtr returns a pointer to s, for building task.Patch literals inline.
func statusPtr(s task.Status) *task.Status { return &s }

// TestTaskCreate_WithBlockedBy proves task_create with blocked_by persists the
// deps at creation.
func TestTaskCreate_WithBlockedBy(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)
	// This test's concern is blocked_by persistence, not the delegation-policy gate.
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	// Seed a blocker in ws-1.
	blocker := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	ctx := WithAgentID(context.Background(), "caller")
	ctx = WithWorkspaceID(ctx, "ws-1")

	res := tool.Execute(ctx, map[string]any{
		"title":      "dependent task",
		"prompt":     "do it",
		"agent_id":   "agent-b",
		"blocked_by": []any{blocker.ID},
		"criteria":   validCriteriaArg(),
	})
	if res.IsError {
		t.Fatalf("task_create with blocked_by: %s", res.ForLLM)
	}

	// Extract the created task id from the result JSON.
	var created struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &created); err != nil {
		t.Fatalf("parse result %q: %v", res.ForLLM, err)
	}
	if created.TaskID == "" {
		t.Fatalf("no task_id in result: %s", res.ForLLM)
	}

	got, err := store.Get(created.TaskID)
	if err != nil {
		t.Fatalf("get created task: %v", err)
	}
	if len(got.BlockedBy) != 1 || got.BlockedBy[0] != blocker.ID {
		t.Errorf("expected blocked_by=[%s], got %v", blocker.ID, got.BlockedBy)
	}
}

// TestTaskCreate_BlockedByRejectsNonExistentBlocker proves task_create with
// blocked_by applies the cross-task guard: a blocked_by referencing a
// non-existent task must be rejected (no dangling edge persisted). A true
// a→b→a cycle cannot be formed at creation because the new task's id is not yet
// known (no existing edge can point back to it), so the cycle guard's
// creation-time role is to validate the proposed blocked_by set against the
// existing graph — a non-existent blocker is the rejectable case here.
func TestTaskCreate_BlockedByRejectsNonExistentBlocker(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)
	// This test's concern is the blocked_by cross-task guard, not the
	// delegation-policy gate — wire a permissive checker so the rejection this
	// test asserts on genuinely comes from the blocked_by guard, not from an
	// (now fail-closed) unwired delegation gate short-circuiting first for the
	// wrong reason.
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(context.Background(), "caller")
	ctx = WithWorkspaceID(ctx, "ws-1")

	res := tool.Execute(ctx, map[string]any{
		"title":      "dangling",
		"prompt":     "p",
		"agent_id":   "agent-b",
		"blocked_by": []any{"nonexistent-blocker-id"},
		"criteria":   validCriteriaArg(),
	})
	if !res.IsError {
		t.Fatal("expected error creating task blocked_by a non-existent blocker")
	}

	// The task must NOT have been persisted with a dangling edge.
	all, _ := store.List(task.Filter{WorkspaceID: "ws-1"})
	for _, tk := range all {
		if tk.Title == "dangling" {
			t.Fatal("task with a dangling blocked_by edge must not be persisted")
		}
	}
}

// TestTaskCreate_BlockedByRejectsCrossWorkspace proves task_create with
// blocked_by applies the cross-workspace guard (mirrors TaskAddDependencyTool).
func TestTaskCreate_BlockedByRejectsCrossWorkspace(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)
	// This test's concern is the cross-workspace blocked_by guard, not the
	// delegation-policy gate — wire a permissive checker so the rejection
	// genuinely comes from the cross-workspace guard.
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	// Blocker lives in ws-beta; new task will land in ws-alpha.
	blocker := seedTask(t, store, "agent-a", "agent-a", "ws-beta")

	ctx := WithAgentID(context.Background(), "caller")
	ctx = WithWorkspaceID(ctx, "ws-alpha")

	res := tool.Execute(ctx, map[string]any{
		"title":      "cross-ws dep",
		"prompt":     "p",
		"agent_id":   "agent-b",
		"blocked_by": []any{blocker.ID},
		"criteria":   validCriteriaArg(),
	})
	if !res.IsError {
		t.Fatal("expected cross-workspace rejection at task_create")
	}
	if !strings.Contains(res.ForLLM, "different workspace") {
		t.Errorf("expected 'different workspace' in error, got: %s", res.ForLLM)
	}
}

// --- Review gap coverage (task_update validation / read-back / negative paths) ---
//
// These close the review gap: the field-patch tests proved the happy path for
// title/priority/due/blocked_by, but did not exercise the rejection, read-back,
// response-shape, and negative-advance paths. Each test below asserts on REAL
// content (specific error substrings, persisted field values, JSON shape), never
// just "no error".

// TestTaskUpdate_NoUpdatableFields proves a call with ONLY task_id (no updatable
// field) is rejected with a clear "no updatable fields" error. Catches a no-op
// tool that silently succeeds.
func TestTaskUpdate_NoUpdatableFields(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	tool := NewTaskUpdateTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	res := tool.Execute(ctx, map[string]any{"task_id": tk.ID})
	if !res.IsError {
		t.Fatal("expected error when no updatable fields provided")
	}
	if !strings.Contains(res.ForLLM, "no updatable fields") {
		t.Errorf("expected 'no updatable fields' in error, got: %s", res.ForLLM)
	}

	// Task must be unchanged.
	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != task.StatusNext {
		t.Errorf("status must be unchanged, got %q", got.Status)
	}
}

// TestTaskUpdate_PriorityOutOfRange proves priority 0 and 6 are both rejected
// (rejection test — catches a validator that accepts anything).
func TestTaskUpdate_PriorityOutOfRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		priority float64
	}{
		{"zero", 0},
		{"six", 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := task.New(t.TempDir())
			tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")
			originalPriority := tk.Priority

			tool := NewTaskUpdateTool(store)
			ctx := WithAgentID(context.Background(), "agent-a")

			res := tool.Execute(ctx, map[string]any{
				"task_id":  tk.ID,
				"priority": tc.priority,
			})
			if !res.IsError {
				t.Fatalf("expected error for priority %v", tc.priority)
			}
			if !strings.Contains(res.ForLLM, "priority must be between 1 and 5") {
				t.Errorf("unexpected error: %s", res.ForLLM)
			}

			// Priority must be unchanged.
			got, _ := store.Get(tk.ID)
			if got.Priority != originalPriority {
				t.Errorf("priority must be unchanged, got %d (was %d)", got.Priority, originalPriority)
			}
		})
	}
}

// TestTaskUpdate_DueInvalidRFC3339 proves an invalid due date is rejected.
// Differentiation: two different invalid inputs both reject (not silently
// accepted).
func TestTaskUpdate_DueInvalidRFC3339(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		due  string
	}{
		{"not-a-date", "not-a-date"},
		{"invalid-month-day", "2026-13-99T00:00:00Z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := task.New(t.TempDir())
			tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")

			tool := NewTaskUpdateTool(store)
			ctx := WithAgentID(context.Background(), "agent-a")

			res := tool.Execute(ctx, map[string]any{
				"task_id": tk.ID,
				"due":     tc.due,
			})
			if !res.IsError {
				t.Fatalf("expected error for invalid due %q", tc.due)
			}
			if !strings.Contains(res.ForLLM, "invalid due date") {
				t.Errorf("expected 'invalid due date' in error, got: %s", res.ForLLM)
			}

			// Due must be unchanged (empty on a fresh seed).
			got, _ := store.Get(tk.ID)
			if got.Due != "" {
				t.Errorf("due must be unchanged, got %q", got.Due)
			}
		})
	}
}

// TestTaskCreate_DueRoundTrip proves create_task accepts and persists an RFC 3339
// due date: the schema-consistency gap that previously gated `due` behind
// update_task only is closed on the create path too. The two cases use two
// different valid RFC 3339 inputs (differentiation — catches a hardcoded
// response that ignores the field).
func TestTaskCreate_DueRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		due  string
	}{
		{"utc", "2026-08-15T09:30:00Z"},
		{"with_offset", "2026-08-16T14:00:00-05:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := task.New(t.TempDir())
			tool := NewTaskCreateTool(store)
			// create_task's concern here is the `due` arg, not the
			// delegation-policy gate — wire a permissive deny-checker so the
			// (fail-closed per the 7-reviewer-gate fix) gate does not mask the
			// assertion under test.
			tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

			ctx := WithAgentID(context.Background(), "caller")
			ctx = WithWorkspaceID(ctx, "ws-1")

			res := tool.Execute(ctx, map[string]any{
				"title":    "with due",
				"prompt":   "do it",
				"agent_id": "agent-b",
				"due":      tc.due,
				"criteria": validCriteriaArg(),
			})
			if res.IsError {
				t.Fatalf("create_task with valid due %q failed: %s", tc.due, res.ForLLM)
			}

			var created struct {
				TaskID string `json:"task_id"`
			}
			if err := json.Unmarshal([]byte(res.ForLLM), &created); err != nil {
				t.Fatalf("parse result %q: %v", res.ForLLM, err)
			}
			got, err := store.Get(created.TaskID)
			if err != nil {
				t.Fatalf("get created task: %v", err)
			}
			if got.Due != tc.due {
				t.Errorf("expected due %q persisted, got %q", tc.due, got.Due)
			}
		})
	}
}

// TestTaskCreate_DueInvalidRFC3339 mirrors TestTaskUpdate_DueInvalidRFC3339 for
// the create path: an invalid due date is rejected at create time too, and the
// task must NOT be persisted. Differentiation: two different invalid inputs both
// reject (not silently accepted).
func TestTaskCreate_DueInvalidRFC3339(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		due  string
	}{
		{"not-a-date", "not-a-date"},
		{"invalid-month-day", "2026-13-99T00:00:00Z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := task.New(t.TempDir())
			tool := NewTaskCreateTool(store)
			// create_task's concern here is `due` validation, not the
			// delegation-policy gate — wire a permissive checker so the
			// rejection this test asserts on genuinely comes from the due-date
			// validator, not an (fail-closed) unwired delegation gate
			// short-circuiting first for the wrong reason.
			tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

			ctx := WithAgentID(context.Background(), "caller")
			ctx = WithWorkspaceID(ctx, "ws-1")

			res := tool.Execute(ctx, map[string]any{
				"title":    "bad due",
				"prompt":   "do it",
				"agent_id": "agent-b",
				"due":      tc.due,
				"criteria": validCriteriaArg(),
			})
			if !res.IsError {
				t.Fatalf("expected error for invalid due %q", tc.due)
			}
			if !strings.Contains(res.ForLLM, "invalid due date") {
				t.Errorf("expected 'invalid due date' in error, got: %s", res.ForLLM)
			}

			// Nothing must have been persisted for the rejected create.
			all, _ := store.List(task.Filter{WorkspaceID: "ws-1"})
			if len(all) != 0 {
				t.Fatalf("expected zero tasks persisted after rejected create, got %d: %+v", len(all), all)
			}
		})
	}
}

// TestTaskUpdate_InvalidStatus proves a bogus status string is rejected.
func TestTaskUpdate_InvalidStatus(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	tool := NewTaskUpdateTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	res := tool.Execute(ctx, map[string]any{
		"task_id": tk.ID,
		"status":  "bogus",
	})
	if !res.IsError {
		t.Fatal("expected error for invalid status 'bogus'")
	}
	if !strings.Contains(res.ForLLM, "invalid status") {
		t.Errorf("expected 'invalid status' in error, got: %s", res.ForLLM)
	}

	// Status must be unchanged.
	got, _ := store.Get(tk.ID)
	if got.Status != task.StatusNext {
		t.Errorf("status must be unchanged, got %q", got.Status)
	}
}

// TestTaskUpdate_NotFound proves an unknown task_id returns a clear not-found
// error (not a silent success or a nil panic).
func TestTaskUpdate_NotFound(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskUpdateTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	res := tool.Execute(ctx, map[string]any{
		"task_id": "nonexistent-id",
		"status":  updStatusInProg,
	})
	if !res.IsError {
		t.Fatal("expected error for non-existent task")
	}
	if !strings.Contains(res.ForLLM, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", res.ForLLM)
	}
}

// TestTaskUpdate_OwnershipRejection proves a caller who is NEITHER the
// assignee NOR the (agent-namespaced) creator is rejected — mirrors
// TestTaskDelete_OwnershipRejection / TestTaskAddDependency_OwnershipRejection.
//
// task_update's gate is now the SAME union check delete_task uses: assignee
// OR agent-namespaced creator (CreatedByAgentID, via
// task.Task.CreatedByAgent). The "creator-a" sub-case below is still rejected
// here, but NOT because creators are disallowed in general — seedTask stamps
// its createdBy into the MIXED-NAMESPACE Task.CreatedBy field only (never
// CreatedByAgentID), so "creator-a" carries no agent-namespaced attribution on
// this task and is exercising the same namespace-safety property
// TestTaskDelete_MixedNamespaceCreatorRejected proves for delete_task. The
// legitimate agent-creator-can-update case (CreatedByAgentID set via
// Store.CreateByAgent) is covered by TestTaskUpdate_AgentCreatorAllowed below.
func TestTaskUpdate_OwnershipRejection(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	// Task assigned to agent-owner, created by creator-a (mixed-namespace only).
	tk := seedTask(t, store, "agent-owner", "creator-a", "ws-1")

	tool := NewTaskUpdateTool(store)

	// An unrelated caller must be rejected.
	res := tool.Execute(WithAgentID(context.Background(), "intruder"), map[string]any{
		"task_id": tk.ID,
		"status":  updStatusInProg,
	})
	if !res.IsError {
		t.Fatal("expected ownership rejection for an unrelated caller")
	}
	if !strings.Contains(res.ForLLM, "you can only update tasks you own or are assigned") {
		t.Errorf("unexpected rejection message: %s", res.ForLLM)
	}

	// A caller matching only the MIXED-NAMESPACE created_by (not
	// CreatedByAgentID) must ALSO be rejected — see the namespace-safety note
	// in the doc comment above.
	resCreator := tool.Execute(WithAgentID(context.Background(), "creator-a"), map[string]any{
		"task_id": tk.ID,
		"status":  updStatusInProg,
	})
	if !resCreator.IsError {
		t.Fatal("expected rejection for a caller matching only the mixed-namespace created_by")
	}
	if !strings.Contains(resCreator.ForLLM, "you can only update tasks you own or are assigned") {
		t.Errorf("unexpected creator rejection message: %s", resCreator.ForLLM)
	}

	// Status must be unchanged.
	got, _ := store.Get(tk.ID)
	if got.Status != task.StatusNext {
		t.Errorf("status must be unchanged after rejected updates, got %q", got.Status)
	}
}

// TestTaskUpdate_AgentCreatorAllowed proves the legitimate creator case: an
// agent that created a task through the AGENT path (Store.CreateByAgent, which
// stamps the agent-id-namespaced CreatedByAgentID) can update it even though
// it is assigned to a different agent — mirroring
// TestTaskDelete_AgentCreatorAllowed (task_scope_test.go) for update_task's
// now-matching union check.
func TestTaskUpdate_AgentCreatorAllowed(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())

	tk := &task.Task{
		Title: "DELEGATED", Action: task.ActionLLM, Status: task.StatusNext,
		WorkspaceID: "ws-1", AgentID: "agent-owner",
	}
	if err := store.CreateByAgent(tk, "creator-a"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := NewTaskUpdateTool(store)
	res := tool.Execute(WithAgentID(context.Background(), "creator-a"), map[string]any{
		"task_id":  tk.ID,
		"priority": float64(1),
	})
	if res.IsError {
		t.Fatalf("the agent that created the task must still be able to update it: %s", res.ForLLM)
	}

	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Priority != 1 {
		t.Errorf("expected priority update to persist, got %d", got.Priority)
	}
	// The assignee (agent-owner) must be untouched by this creator-initiated update.
	if got.AgentID != "agent-owner" {
		t.Errorf("creator update must not reassign the task, got agent_id %q", got.AgentID)
	}
}

// TestTaskUpdate_ResultAndArtifactsReadBack proves result + artifacts persist
// (persistence test — write, then store.Get, assert full content matches). Two
// different artifact sets produce two different stored values
// (differentiation test — catches a hardcoded response).
func TestTaskUpdate_ResultAndArtifactsReadBack(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	tool := NewTaskUpdateTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	// First write: result + two artifacts.
	res := tool.Execute(ctx, map[string]any{
		"task_id":   tk.ID,
		"status":    updStatusDone,
		"result":    "shipped the feature",
		"artifacts": []any{"/tmp/a.txt", "/tmp/b.txt"},
	})
	if res.IsError {
		t.Fatalf("task_update result+artifacts: %s", res.ForLLM)
	}

	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Result != "shipped the feature" {
		t.Errorf("expected result 'shipped the feature', got %q", got.Result)
	}
	if len(got.Artifacts) != 2 || got.Artifacts[0] != "/tmp/a.txt" || got.Artifacts[1] != "/tmp/b.txt" {
		t.Errorf("expected artifacts [a,b], got %v", got.Artifacts)
	}
}

// TestTaskUpdate_ResponseCarriesUpdatedFields proves the response JSON carries an
// "updated_fields" array listing the changed field names (content test — asserts
// the actual field names appear, not just that the JSON parses).
func TestTaskUpdate_ResponseCarriesUpdatedFields(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	tool := NewTaskUpdateTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	// Issue #593: updStatusFailed, not updStatusInProg — this test only cares
	// that "status" appears among the updated FIELD NAMES, which is unaffected
	// by which valid direct status is used.
	res := tool.Execute(ctx, map[string]any{
		"task_id":  tk.ID,
		"status":   updStatusFailed,
		"title":    "new title",
		"priority": float64(2),
	})
	if res.IsError {
		t.Fatalf("task_update: %s", res.ForLLM)
	}

	var payload struct {
		TaskID        string   `json:"task_id"`
		Status        string   `json:"status"`
		UpdatedFields []string `json:"updated_fields"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &payload); err != nil {
		t.Fatalf("parse result %q: %v", res.ForLLM, err)
	}
	if payload.TaskID != tk.ID {
		t.Errorf("expected task_id %q, got %q", tk.ID, payload.TaskID)
	}
	// All three provided fields must be listed.
	want := map[string]bool{"status": true, "title": true, "priority": true}
	for _, f := range payload.UpdatedFields {
		if !want[f] {
			t.Errorf("unexpected updated field %q in %v", f, payload.UpdatedFields)
		}
		delete(want, f)
	}
	if len(want) != 0 {
		t.Errorf("missing updated fields: %v (got %v)", want, payload.UpdatedFields)
	}
}

// TestTaskUpdate_FailedDoesNotAdvanceDependents proves the NEGATIVE: marking a
// blocker `failed` does NOT advance a dependent (it stays `blocked`). Only
// `done` advances dependents. Catches a tool that advances on any terminal
// status.
func TestTaskUpdate_FailedDoesNotAdvanceDependents(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())

	blocker := seedTask(t, store, "agent-a", "agent-a", "ws-1")
	dependent := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	// Wire dependent→blocker via the store so it lands in `blocked`.
	if _, _, err := store.AddDependency(dependent.ID, blocker.ID); err != nil {
		t.Fatalf("seed AddDependency: %v", err)
	}
	dep, err := store.Get(dependent.ID)
	if err != nil {
		t.Fatalf("get dependent: %v", err)
	}
	if dep.Status != task.StatusBlocked {
		t.Fatalf("expected dependent blocked after AddDependency, got %q", dep.Status)
	}

	tool := NewTaskUpdateTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	// Mark the blocker FAILED (not done).
	res := tool.Execute(ctx, map[string]any{
		"task_id": blocker.ID,
		"status":  updStatusFailed,
		"result":  "blocker failed",
	})
	if res.IsError {
		t.Fatalf("task_update failed on blocker: %s", res.ForLLM)
	}

	// The dependent must STILL be blocked — failed does not satisfy a dep.
	dep, err = store.Get(dependent.ID)
	if err != nil {
		t.Fatalf("get dependent after blocker failed: %v", err)
	}
	if dep.Status != task.StatusBlocked {
		t.Errorf("expected dependent to STAY blocked after blocker failed, got %q", dep.Status)
	}
}

// TestTaskUpdate_BlockedByClearsDeps proves the new fix behavior: passing
// blocked_by:[] CLEARS existing deps. Set deps, then update with blocked_by:[]
// → deps gone and the task returns to a dispatchable state.
func TestTaskUpdate_BlockedByClearsDeps(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())

	dep := seedTask(t, store, "agent-a", "agent-a", "ws-1")
	blocker := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	// Wire dep→blocker via the store so dep is `blocked` with one edge.
	if _, _, err := store.AddDependency(dep.ID, blocker.ID); err != nil {
		t.Fatalf("seed AddDependency: %v", err)
	}
	got, err := store.Get(dep.ID)
	if err != nil {
		t.Fatalf("get dep after AddDependency: %v", err)
	}
	if len(got.BlockedBy) != 1 || got.BlockedBy[0] != blocker.ID {
		t.Fatalf("expected one blocker wired, got %v", got.BlockedBy)
	}
	if got.Status != task.StatusBlocked {
		t.Fatalf("expected dep blocked, got %q", got.Status)
	}

	tool := NewTaskUpdateTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	// Now CLEAR deps via blocked_by:[].
	res := tool.Execute(ctx, map[string]any{
		"task_id":    dep.ID,
		"blocked_by": []any{},
	})
	if res.IsError {
		t.Fatalf("task_update blocked_by clear: %s", res.ForLLM)
	}

	got, err = store.Get(dep.ID)
	if err != nil {
		t.Fatalf("get dep after clear: %v", err)
	}
	if len(got.BlockedBy) != 0 {
		t.Errorf("expected blocked_by cleared, got %v", got.BlockedBy)
	}
	// Clearing all blockers must move a `blocked` task back to `next`.
	if got.Status != task.StatusNext {
		t.Errorf("expected dep to advance to next after clearing deps, got %q", got.Status)
	}
}

// TestTaskUpdate_AgentIDReassignment_RoutesThroughDelegationGate proves that
// reassigning a task to a DIFFERENT agent routes through the delegation-deny
// gate (reassignment is re-delegation). Mirrors task_create's denial test shape
// (delegation_deny_wiring_test.go). The tool starts unwired, so the test
// installs the checker via SetDelegationDenyChecker.
func TestTaskUpdate_AgentIDReassignment_RoutesThroughDelegationGate(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	tool := NewTaskUpdateTool(store)

	var gotTarget string
	tool.SetDelegationDenyChecker(func(_ context.Context, targetAgentID string) *DelegationDenial {
		gotTarget = targetAgentID
		return &DelegationDenial{
			Reason:        "reassignment to untrusted agent denied",
			Policy:        DenyTrustSet,
			TargetAgentID: targetAgentID,
		}
	})

	ctx := WithAgentID(context.Background(), "agent-a")
	res := tool.Execute(ctx, map[string]any{
		"task_id":  tk.ID,
		"agent_id": "evil-agent",
	})

	if res == nil || !res.IsError {
		t.Fatalf("expected denied reassignment to return an error, got %+v", res)
	}
	failure := decodeDelegationFailure(t, res)
	if failure.Tool != "update_task" {
		t.Errorf("expected structured failure tool 'update_task', got %q", failure.Tool)
	}
	if failure.Policy != string(DenyTrustSet) {
		t.Errorf("expected policy %q, got %q", DenyTrustSet, failure.Policy)
	}
	if !strings.Contains(failure.Reason, "reassignment to untrusted agent denied") {
		t.Errorf("expected the checker's reason to surface, got: %s", failure.Reason)
	}
	if gotTarget != "evil-agent" {
		t.Errorf("expected checker to receive 'evil-agent', got %q", gotTarget)
	}

	// The task must NOT have been reassigned (deny short-circuits before store.Update).
	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AgentID != "agent-a" {
		t.Errorf("agent_id must be unchanged after denied reassignment, got %q", got.AgentID)
	}
}

// TestTaskUpdate_AgentIDReassignment_SameAgentSkipsGate proves that a no-op
// reassign (same agent) does NOT invoke the delegation gate — it is not a
// re-delegation when the assignee is unchanged. A status change is included so
// the call is not a no-op overall (a same-agent agent_id is intentionally not
// counted as an updatable field by the tool).
func TestTaskUpdate_AgentIDReassignment_SameAgentSkipsGate(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	tool := NewTaskUpdateTool(store)

	called := false
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial {
		called = true
		return nil
	})

	ctx := WithAgentID(context.Background(), "agent-a")
	// Reassign to the SAME agent (plus a status change so the call is valid) —
	// the gate must NOT be consulted for the same-agent value. Issue #593:
	// updStatusFailed, not updStatusInProg — the seeded task starts at
	// StatusNext, so in_progress would now be rejected by the forge guard.
	res := tool.Execute(ctx, map[string]any{
		"task_id":  tk.ID,
		"status":   updStatusFailed,
		"agent_id": "agent-a",
	})
	if res.IsError {
		t.Fatalf("same-agent reassign must succeed, got: %s", res.ForLLM)
	}
	if called {
		t.Error("delegation gate must NOT be consulted for a same-agent (no-op) reassign")
	}
}

// --- subagent_3p (external-CLI) worker task assignment (Fix C) ---
//
// Historically create_task/update_task rejected any agent_id naming a
// subagent_3p (external-CLI) worker outright, because
// TaskExecutor.processTaskDirect routed every task run through the native
// engine unconditionally — assigning a task to an external-CLI worker would
// have silently mis-executed it on the wrong engine with full system-level
// tool access instead of the configured external CLI. AgentLoop.
// processTaskDirect (pkg/agent/loop.go) now branches on runner.ResolveDispatch
// the same way spawnSubTurn does for agent-to-agent delegation, dispatching a
// subagent_3p's task run through runExternalCLISubTurn — so the tool-layer
// guard (and its SetExternalCLIWorkerChecker plumbing) was removed. The tests
// below prove a subagent_3p target is now accepted like any other delegate,
// governed solely by the ordinary delegation-policy gate.

// TestTaskCreate_AllowsSubagent3pWorker_WhenDelegationAllows proves
// create_task persists agent_id=<subagent_3p target> when the delegation
// policy gate allows it — the engine-limitation rejection that used to fire
// independently of that gate is gone now that processTaskDirect can dispatch
// the run correctly.
func TestTaskCreate_AllowsSubagent3pWorker_WhenDelegationAllows(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)

	// Delegation policy ALLOWS (a real, permitted task-mode edge to the target).
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial {
		return nil
	})

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")
	res := tool.Execute(ctx, map[string]any{
		"title":    "dispatched to external CLI",
		"prompt":   "do it",
		"agent_id": "external-worker",
		"criteria": validCriteriaArg(),
	})

	if res == nil || res.IsError {
		t.Fatalf("expected the subagent_3p target to be accepted, got %+v", res)
	}

	tasks, err := store.List(task.Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected exactly one task persisted, got %d", len(tasks))
	}
	if tasks[0].AgentID != "external-worker" {
		t.Errorf("expected agent_id %q, got %q", "external-worker", tasks[0].AgentID)
	}
}

// TestTaskUpdate_AllowsSubagent3pWorkerReassignment_WhenDelegationAllows
// mirrors the create-path test above for update_task's reassignment path.
func TestTaskUpdate_AllowsSubagent3pWorkerReassignment_WhenDelegationAllows(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	tool := NewTaskUpdateTool(store)
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial {
		return nil // delegation policy ALLOWS
	})

	ctx := WithAgentID(context.Background(), "agent-a")
	res := tool.Execute(ctx, map[string]any{
		"task_id":  tk.ID,
		"agent_id": "external-worker",
	})

	if res == nil || res.IsError {
		t.Fatalf("expected the subagent_3p reassignment to be accepted, got %+v", res)
	}

	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AgentID != "external-worker" {
		t.Errorf("expected agent_id %q after reassignment, got %q", "external-worker", got.AgentID)
	}
}

// TestAgentListTool_NilLister_FailsClosedInsteadOfPanicking is a regression
// test for the F3 finding (exhaustive tool-catalog review): the catalog
// constructs list_agents via NewAgentListTool(nil) for metadata-only purposes
// (GeneralBuiltinMetadata), and before this fix a stray Execute() on that
// instance dereferenced the nil listAgents func directly, panicking — the
// registry's recover() then reported it to the caller as "Tool 'list_agents'
// crashed with panic" rather than a clean, ordinary tool error. Every other
// tool in this file's package that takes an injected dependency (store,
// checker, hook) guards it explicitly; list_agents was the one exception.
func TestAgentListTool_NilLister_FailsClosedInsteadOfPanicking(t *testing.T) {
	t.Parallel()
	tool := NewAgentListTool(nil)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Execute panicked with a nil lister instead of failing closed: %v", r)
		}
	}()

	res := tool.Execute(context.Background(), map[string]any{})
	if res == nil || !res.IsError {
		t.Fatalf("expected an ErrorResult for a nil agent lister, got %+v", res)
	}
}

// TestTaskListTool_NilStore_FailsClosedInsteadOfPanicking, along with the three
// sibling tests below, are regression tests for the X1 cross-cutting finding:
// task.go's five general-scope tools (list_tasks, create_task, update_task,
// delete_task, list_agents) were the only ones in the package with no
// nil-store guard, unlike every plan/run_task/stop_plan/plan_correct/
// inspect_session tool. Defense-in-depth: nothing in production wiring
// constructs these with a nil store, but a metadata-only catalog instance
// (GeneralBuiltinMetadata) legitimately does, and a stray Execute() on one
// must fail cleanly, not panic.
func TestTaskListTool_NilStore_FailsClosedInsteadOfPanicking(t *testing.T) {
	t.Parallel()
	tool := NewTaskListTool(nil)
	res := tool.Execute(WithAgentID(context.Background(), "agent-a"), map[string]any{"role": "assignee"})
	if res == nil || !res.IsError {
		t.Fatalf("expected an ErrorResult for a nil task store, got %+v", res)
	}
}

func TestTaskCreateTool_NilStore_FailsClosedInsteadOfPanicking(t *testing.T) {
	t.Parallel()
	tool := NewTaskCreateTool(nil)
	res := tool.Execute(WithAgentID(context.Background(), "agent-a"), map[string]any{
		"title":    "x",
		"prompt":   "x",
		"agent_id": "agent-b",
		"criteria": validCriteriaArg(),
	})
	if res == nil || !res.IsError {
		t.Fatalf("expected an ErrorResult for a nil task store, got %+v", res)
	}
}

func TestTaskUpdateTool_NilStore_FailsClosedInsteadOfPanicking(t *testing.T) {
	t.Parallel()
	tool := NewTaskUpdateTool(nil)
	res := tool.Execute(WithAgentID(context.Background(), "agent-a"), map[string]any{
		"task_id": "some-task",
		"status":  "failed",
	})
	if res == nil || !res.IsError {
		t.Fatalf("expected an ErrorResult for a nil task store, got %+v", res)
	}
}

func TestTaskDeleteTool_NilStore_FailsClosedInsteadOfPanicking(t *testing.T) {
	t.Parallel()
	tool := NewTaskDeleteTool(nil)
	res := tool.Execute(WithAgentID(context.Background(), "agent-a"), map[string]any{"task_id": "some-task"})
	if res == nil || !res.IsError {
		t.Fatalf("expected an ErrorResult for a nil task store, got %+v", res)
	}
}
