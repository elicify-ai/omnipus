// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_scope_descendants_adr084_test.go covers JUDGE-FR-010 – FR-013
// (ADR-084 D1a, wave E9): resolveVerifierSessionScope must extend to every
// descendant session at any depth — not just the adjudicated unit's own
// root session — because a worker's delegated work (a subagent turn) lands
// in the CHILD's own session (ADR-057 D1/W11), never the parent's. Without
// this, a criterion whose real evidence is in delegated work could never be
// reached by inspect_session at all.
package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// mustChildSession creates a new session under store and stamps its
// ParentSessionID to parentID (mirroring pkg/agent/subturn.go's own
// SetMeta(childID, session.MetaPatch{ParentSessionID: &parentID}) call —
// the exact edge goalDescendantSessionIDs walks, C4).
func mustChildSession(t *testing.T, store *session.UnifiedStore, parentID string) *session.UnifiedMeta {
	t.Helper()
	child, err := store.NewSession(session.SessionTypeChat, "web", "native-agent")
	if err != nil {
		t.Fatalf("NewSession (child): %v", err)
	}
	pid := parentID
	if err := store.SetMeta(child.ID, session.MetaPatch{ParentSessionID: &pid}); err != nil {
		t.Fatalf("SetMeta ParentSessionID: %v", err)
	}
	return child
}

// TestScopeWithDescendants_TaskScope_IncludesMultiDepthDescendants proves
// FR-010/FR-011: a task's own session PLUS every descendant at any depth
// (grandchild included) is returned, and a session with no relationship to
// the root is excluded.
func TestScopeWithDescendants_TaskScope_IncludesMultiDepthDescendants(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("native-agent must have a UnifiedStore session store")
	}

	root, err := store.NewSession(session.SessionTypeChat, "web", "native-agent")
	if err != nil {
		t.Fatalf("NewSession (root): %v", err)
	}
	child := mustChildSession(t, store, root.ID)
	grandchild := mustChildSession(t, store, child.ID)
	unrelated, err := store.NewSession(session.SessionTypeChat, "web", "native-agent")
	if err != nil {
		t.Fatalf("NewSession (unrelated): %v", err)
	}

	taskStore := GetTaskStore(al)
	if err := taskStore.Create(&task.Task{
		ID: "t-descend", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "descend", SessionID: root.ID,
	}); err != nil {
		t.Fatalf("task Create: %v", err)
	}

	got := al.resolveVerifierSessionScope(JudgeCriteriaInput{
		Scope: task.VerdictScopeTask, TaskID: "t-descend", AssigneeAgentID: "native-agent",
	})
	want := map[string]bool{root.ID: true, child.ID: true, grandchild.ID: true}
	gotSet := make(map[string]bool, len(got))
	for _, id := range got {
		gotSet[id] = true
	}
	for id := range want {
		if !gotSet[id] {
			t.Errorf("scope missing expected session %q; got %v", id, got)
		}
	}
	if gotSet[unrelated.ID] {
		t.Errorf("scope must not include an unrelated session; got %v", got)
	}
}

// TestScopeWithDescendants_GoalScope_IncludesDescendant proves FR-010
// specifically for goal scope: the chat session carrying the goal
// condition, PLUS its descendant.
func TestScopeWithDescendants_GoalScope_IncludesDescendant(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("native-agent must have a UnifiedStore session store")
	}
	goalSess, err := store.NewSession(session.SessionTypeChat, "web", "native-agent")
	if err != nil {
		t.Fatalf("NewSession (goal session): %v", err)
	}
	delegated := mustChildSession(t, store, goalSess.ID)

	got := al.resolveVerifierSessionScope(JudgeCriteriaInput{
		Scope: task.VerdictScopeGoal, AssigneeAgentID: "native-agent", GoalSessionID: goalSess.ID,
	})
	gotSet := make(map[string]bool, len(got))
	for _, id := range got {
		gotSet[id] = true
	}
	if !gotSet[goalSess.ID] {
		t.Errorf("scope must include the goal's own session; got %v", got)
	}
	if !gotSet[delegated.ID] {
		t.Errorf("scope must include the goal session's delegated-work descendant; got %v", got)
	}
}

// TestScopeWithDescendants_PlanScope_UnionsEachMembersDescendants proves
// FR-012: plan scope extends to descendants of EACH member session, not
// just the members themselves.
func TestScopeWithDescendants_PlanScope_UnionsEachMembersDescendants(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("native-agent must have a UnifiedStore session store")
	}
	m1, err := store.NewSession(session.SessionTypeChat, "web", "native-agent")
	if err != nil {
		t.Fatalf("NewSession (m1): %v", err)
	}
	m1Child := mustChildSession(t, store, m1.ID)
	m2, err := store.NewSession(session.SessionTypeChat, "web", "native-agent")
	if err != nil {
		t.Fatalf("NewSession (m2): %v", err)
	}

	taskStore := GetTaskStore(al)
	for _, tk := range []*task.Task{
		{ID: "pm1", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "m1", PlanID: "p-descend", SessionID: m1.ID},
		{ID: "pm2", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "m2", PlanID: "p-descend", SessionID: m2.ID},
	} {
		if err := taskStore.Create(tk); err != nil {
			t.Fatalf("task Create %s: %v", tk.ID, err)
		}
	}

	got := al.resolveVerifierSessionScope(JudgeCriteriaInput{
		Scope: task.VerdictScopePlan, PlanID: "p-descend", AssigneeAgentID: "native-agent",
	})
	gotSet := make(map[string]bool, len(got))
	for _, id := range got {
		gotSet[id] = true
	}
	for _, want := range []string{m1.ID, m1Child.ID, m2.ID} {
		if !gotSet[want] {
			t.Errorf("plan scope missing expected session %q; got %v", want, got)
		}
	}
}

// TestScopeWithDescendants_NoStore_DegradesToRootsOnly is FR-013's degrade
// guarantee: when no session store is resolvable at all, the scope
// degrades to exactly the root id(s) supplied — never wider, never a hard
// failure.
func TestScopeWithDescendants_NoStore_DegradesToRootsOnly(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	// An assignee agent id with no registered session store, and no shared
	// store resolvable in this minimal harness's edge case, reproduces the
	// "store == nil" degrade path directly.
	got := al.scopeWithDescendants("does-not-exist-agent", []string{"root-a", "root-b"})
	if len(got) != 2 || got[0] != "root-a" || got[1] != "root-b" {
		// The shared store may still resolve in this harness (it is
		// process-wide, not per-agent) — if it does, the walk still must
		// not WIDEN beyond the roots for ids that have no children.
		gotSet := make(map[string]bool, len(got))
		for _, id := range got {
			gotSet[id] = true
		}
		if !gotSet["root-a"] || !gotSet["root-b"] || len(got) != 2 {
			t.Errorf("degrade must yield exactly the roots (no children exist for made-up ids), got %v", got)
		}
	}
}
