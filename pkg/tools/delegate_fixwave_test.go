// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// delegate_fixwave_test.go covers a cluster of delegate-tool bugs found
// during a live UAT of the delegation tools (feature/plan-swimlane-board,
// ADR-052/053 epic): #579 (follow_up field mismatch), #580 (timeout_seconds
// never read), #581 (steer TOCTOU race), #583 (list_jobs actionable resolver,
// unit-level — the loop.go wiring itself is covered in pkg/agent), #587
// (empty agent_id silently accepted), and #588/N9 (cancel-on-terminal
// misleading success). See pkg/tools/message_parent_real_context_test.go's
// sibling in pkg/agent for #576.
package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// --- #587: delegate silently accepts an empty agent_id -----------------

func TestDelegateTool_Run_AgentID_Validation(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetSpawner(&mockDelegateSpawner{})
	tool.SetDelegationDenyCheckerAwait(func(context.Context, string) *DelegationDenial { return nil })
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
	t.Cleanup(tool.WaitForAsyncTasks)

	t.Run("empty string agent_id rejected", func(t *testing.T) {
		result := tool.Execute(context.Background(), map[string]any{"task": "x", "agent_id": "", "async": false})
		if !result.IsError {
			t.Fatal("expected agent_id=\"\" to be rejected")
		}
		if !strings.Contains(result.ForLLM, "agent_id") {
			t.Errorf("expected error to mention agent_id, got: %s", result.ForLLM)
		}
	})

	t.Run("whitespace-only agent_id rejected", func(t *testing.T) {
		result := tool.Execute(context.Background(), map[string]any{"task": "x", "agent_id": "   ", "async": false})
		if !result.IsError {
			t.Fatal("expected whitespace-only agent_id to be rejected")
		}
	})

	t.Run("non-string agent_id rejected", func(t *testing.T) {
		result := tool.Execute(context.Background(), map[string]any{"task": "x", "agent_id": 42, "async": false})
		if !result.IsError {
			t.Fatal("expected a non-string agent_id to be rejected")
		}
	})

	t.Run("omitted agent_id still accepted (generic subagent)", func(t *testing.T) {
		result := tool.Execute(context.Background(), map[string]any{"task": "x", "async": false})
		if result.IsError {
			t.Fatalf("expected omitted agent_id to be accepted, got error: %s", result.ForLLM)
		}
	})

	t.Run("non-empty agent_id still accepted", func(t *testing.T) {
		result := tool.Execute(context.Background(), map[string]any{"task": "x", "agent_id": "worker-1", "async": false})
		if result.IsError {
			t.Fatalf("expected a valid agent_id to be accepted, got error: %s", result.ForLLM)
		}
	})
}

// --- #580: delegate timeout_seconds is never read -----------------------

// capturingDelegateSpawner implements SubTurnSpawner and records the last
// SubTurnConfig it was invoked with (for asserting Timeout threading).
type capturingDelegateSpawner struct {
	mu  sync.Mutex
	cfg SubTurnConfig
}

func (c *capturingDelegateSpawner) SpawnSubTurn(_ context.Context, cfg SubTurnConfig) (*ToolResult, error) {
	c.mu.Lock()
	c.cfg = cfg
	c.mu.Unlock()
	return &ToolResult{ForLLM: "done", ForUser: "done"}, nil
}

func (c *capturingDelegateSpawner) lastConfig() SubTurnConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg
}

func TestDelegateTool_Run_TimeoutSeconds_ThreadsIntoSubTurnConfig(t *testing.T) {
	spawner := &capturingDelegateSpawner{}
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetSpawner(spawner)
	tool.SetDelegationDenyCheckerAwait(func(context.Context, string) *DelegationDenial { return nil })
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
	t.Cleanup(tool.WaitForAsyncTasks)

	t.Run("nonzero timeout_seconds threads through on the sync path", func(t *testing.T) {
		result := tool.Execute(context.Background(), map[string]any{"task": "x", "async": false, "timeout_seconds": 1800})
		if result.IsError {
			t.Fatalf("run failed: %s", result.ForLLM)
		}
		if got := spawner.lastConfig().Timeout; got != 1800*time.Second {
			t.Errorf("Timeout = %v, want %v", got, 1800*time.Second)
		}
	})

	t.Run("nonzero timeout_seconds threads through on the async path", func(t *testing.T) {
		result := tool.Execute(context.Background(), map[string]any{"task": "x", "async": true, "timeout_seconds": 900})
		if result.IsError {
			t.Fatalf("run failed: %s", result.ForLLM)
		}
		tool.WaitForAsyncTasks()
		if got := spawner.lastConfig().Timeout; got != 900*time.Second {
			t.Errorf("Timeout = %v, want %v", got, 900*time.Second)
		}
	})

	t.Run("zero/absent timeout_seconds leaves Timeout unset (use spawner default)", func(t *testing.T) {
		result := tool.Execute(context.Background(), map[string]any{"task": "x", "async": false})
		if result.IsError {
			t.Fatalf("run failed: %s", result.ForLLM)
		}
		if got := spawner.lastConfig().Timeout; got != 0 {
			t.Errorf("Timeout = %v, want 0 (no override)", got)
		}
	})

	t.Run("explicit zero timeout_seconds also leaves Timeout unset", func(t *testing.T) {
		result := tool.Execute(context.Background(), map[string]any{"task": "x", "async": false, "timeout_seconds": 0})
		if result.IsError {
			t.Fatalf("run failed: %s", result.ForLLM)
		}
		if got := spawner.lastConfig().Timeout; got != 0 {
			t.Errorf("Timeout = %v, want 0 (no override)", got)
		}
	})

	t.Run("out-of-bounds (too large) timeout_seconds rejected", func(t *testing.T) {
		result := tool.Execute(context.Background(), map[string]any{"task": "x", "async": false, "timeout_seconds": 999999})
		if !result.IsError {
			t.Fatal("expected an out-of-bounds timeout_seconds to be rejected")
		}
		if !strings.Contains(result.ForLLM, "timeout_seconds") {
			t.Errorf("expected error to mention timeout_seconds, got: %s", result.ForLLM)
		}
	})

	t.Run("negative timeout_seconds rejected", func(t *testing.T) {
		result := tool.Execute(context.Background(), map[string]any{"task": "x", "async": false, "timeout_seconds": -5})
		if !result.IsError {
			t.Fatal("expected a negative timeout_seconds to be rejected")
		}
	})

	t.Run("non-numeric timeout_seconds rejected", func(t *testing.T) {
		result := tool.Execute(context.Background(), map[string]any{"task": "x", "async": false, "timeout_seconds": "soon"})
		if !result.IsError {
			t.Fatal("expected a non-numeric timeout_seconds to be rejected")
		}
	})
}

// --- #579: delegate follow_up silently drops the new instruction --------

func TestDelegateTool_FollowUp_UsesTextField(t *testing.T) {
	spawner := &capturingDelegateSpawner{}
	tool, lc, _, _ := newADR053TestTool(t)
	tool.SetSpawner(spawner)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-followup-text", State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "follow_up", "session_id": "child-followup-text", "text": "also do this",
	})
	if result.IsError {
		t.Fatalf("follow_up failed: %s", result.ForLLM)
	}
	tool.WaitForAsyncTasks()

	sp := spawner.lastConfig().SystemPrompt
	if !strings.Contains(sp, "also do this") {
		t.Errorf("expected the resumed instruction to contain the text field's content, got SystemPrompt=%q", sp)
	}
	if strings.Contains(sp, "Continue the previous task.") {
		t.Error("must not fall back to the generic placeholder when text is provided")
	}
}

func TestDelegateTool_FollowUp_TaskAliasStillWorks(t *testing.T) {
	// Back-compat: "task" remains an accepted alias when "text" is absent.
	spawner := &capturingDelegateSpawner{}
	tool, lc, _, _ := newADR053TestTool(t)
	tool.SetSpawner(spawner)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-followup-task-alias", State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "follow_up", "session_id": "child-followup-task-alias", "task": "legacy field still works",
	})
	if result.IsError {
		t.Fatalf("follow_up failed: %s", result.ForLLM)
	}
	tool.WaitForAsyncTasks()
	if sp := spawner.lastConfig().SystemPrompt; !strings.Contains(sp, "legacy field still works") {
		t.Errorf("expected the deprecated task alias to still work, got SystemPrompt=%q", sp)
	}
}

func TestDelegateTool_FollowUp_TextWinsOverTask(t *testing.T) {
	spawner := &capturingDelegateSpawner{}
	tool, lc, _, _ := newADR053TestTool(t)
	tool.SetSpawner(spawner)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-followup-both", State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "follow_up", "session_id": "child-followup-both",
		"text": "text wins", "task": "task should be ignored",
	})
	if result.IsError {
		t.Fatalf("follow_up failed: %s", result.ForLLM)
	}
	tool.WaitForAsyncTasks()
	sp := spawner.lastConfig().SystemPrompt
	if !strings.Contains(sp, "text wins") {
		t.Errorf("expected text to win over task, got SystemPrompt=%q", sp)
	}
	if strings.Contains(sp, "task should be ignored") {
		t.Errorf("task must not be used when text is present, got SystemPrompt=%q", sp)
	}
}

func TestDelegateTool_FollowUp_EmptyInstruction_Rejected(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-followup-empty", State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{"action": "follow_up", "session_id": "child-followup-empty"})
	if !result.IsError {
		t.Fatal("expected follow_up with neither text nor task to be rejected, not silently substituted")
	}
	if strings.Contains(result.ForLLM, "Continue the previous task") {
		t.Error("must not silently substitute the generic placeholder")
	}

	whitespace := tool.Execute(ctx, map[string]any{
		"action": "follow_up", "session_id": "child-followup-empty", "text": "   ",
	})
	if !whitespace.IsError {
		t.Fatal("expected whitespace-only text to be rejected")
	}
}

// --- #581: delegate steer on a just-completed session (TOCTOU race) -----

// callCountingLifecycleStore wraps a real *session.LifecycleStore, counting
// Load vs Mutate calls so a test can assert WHICH primitive a caller used
// without needing to fabricate an actual goroutine race.
type callCountingLifecycleStore struct {
	*session.LifecycleStore
	mu          sync.Mutex
	loadCalls   int
	mutateCalls int
}

func (c *callCountingLifecycleStore) Load(sessionID string) (*session.LifecycleRecord, error) {
	c.mu.Lock()
	c.loadCalls++
	c.mu.Unlock()
	return c.LifecycleStore.Load(sessionID)
}

func (c *callCountingLifecycleStore) Mutate(sessionID string, fn func(*session.LifecycleRecord) error) error {
	c.mu.Lock()
	c.mutateCalls++
	c.mu.Unlock()
	return c.LifecycleStore.Mutate(sessionID, fn)
}

func (c *callCountingLifecycleStore) counts() (loads, mutates int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadCalls, c.mutateCalls
}

func TestDelegateTool_Steer_TerminalCheck_RoutesThroughMutate(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetSpawner(&mockDelegateSpawner{})
	tool.SetSessionMessagingEnabled(func() bool { return true })
	steer := &fakeSteeringSink{}
	tool.SetSteeringSink(steer)

	real := session.NewLifecycleStore(t.TempDir())
	wrapper := &callCountingLifecycleStore{LifecycleStore: real}
	tool.SetLifecycleStore(wrapper)

	if err := real.Persist(&session.LifecycleRecord{
		SessionID: "child-mutate-check", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	result := tool.Execute(ctx, map[string]any{"action": "steer", "session_id": "child-mutate-check", "text": "hi"})
	if result.IsError {
		t.Fatalf("steer failed: %s", result.ForLLM)
	}

	loads, mutates := wrapper.counts()
	if mutates == 0 {
		t.Error("#581: expected executeSteer's terminal check to route through Mutate (the atomic RMW " +
			"primitive that holds the per-session lock across the read+decide), got 0 Mutate calls")
	}
	if loads != 0 {
		t.Errorf("#581: expected executeSteer to no longer use a naked Load() for its terminal check "+
			"(that is precisely the check-then-act TOCTOU this fix closes), got %d Load call(s)", loads)
	}
	if len(steer.delivered) != 1 {
		t.Errorf("expected exactly 1 steering message delivered for a legitimate non-terminal steer, got %d",
			len(steer.delivered))
	}
}

func TestDelegateTool_Steer_TerminalSession_Rejected(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetSpawner(&mockDelegateSpawner{})
	tool.SetSessionMessagingEnabled(func() bool { return true })
	steer := &fakeSteeringSink{}
	tool.SetSteeringSink(steer)
	lc := session.NewLifecycleStore(t.TempDir())
	tool.SetLifecycleStore(lc)

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-terminal-steer", State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	result := tool.Execute(ctx, map[string]any{"action": "steer", "session_id": "child-terminal-steer", "text": "too late"})
	if !result.IsError {
		t.Fatal("expected steer on an already-terminal session to be rejected, not queued")
	}
	if !strings.Contains(result.ForLLM, "terminal") {
		t.Errorf("expected a clear terminal-session error, got: %s", result.ForLLM)
	}
	if len(steer.delivered) != 0 {
		t.Errorf("expected no steering message to be delivered for a terminal session, got %d", len(steer.delivered))
	}
}

// --- #583 (unit level): DelegateTool.ResolvableSessionIDs ---------------

func TestDelegateTool_ResolvableSessionIDs(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetSpawner(&mockDelegateSpawner{})
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
	t.Cleanup(tool.WaitForAsyncTasks)

	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	sessionID := runAndExtractSessionID(t, tool, ctx, "some task")

	resolvable := tool.ResolvableSessionIDs([]string{sessionID, "unknown-session-id"})
	if !resolvable[sessionID] {
		t.Errorf("#583: expected %q to resolve via ResolvableSessionIDs (present in t.sessionIndex)", sessionID)
	}
	if resolvable["unknown-session-id"] {
		t.Error("#583: expected an unrelated session id to resolve to false")
	}

	// Confirm this is reachable through the JobSessionResolver interface
	// exactly as list_jobs (pkg/tools/list_jobs_sources.go) consumes it —
	// not merely that the concrete method exists.
	var resolver JobSessionResolver = tool
	batch := resolver.ResolvableSessionIDs([]string{sessionID})
	if !batch[sessionID] {
		t.Error("expected DelegateTool, used as a JobSessionResolver, to resolve its own session id")
	}
}

func TestDelegateTool_ResolvableSessionIDs_EmptyIndex(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	got := tool.ResolvableSessionIDs([]string{"never-seen"})
	if got["never-seen"] {
		t.Error("expected a session id never dispatched by this tool to resolve to false")
	}
}

// --- #588 (N9): cancel on an already-terminal session -------------------

func TestDelegateTool_Cancel_AlreadyTerminal_Rejected(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetSessionMessagingEnabled(func() bool { return true })
	lc := session.NewLifecycleStore(t.TempDir())
	tool.SetLifecycleStore(lc)

	var softCalled, hardCalled bool
	tool.SetCancelHooks(
		func(sessionID, hint string) ([]string, error) { softCalled = true; return nil, nil },
		func(sessionID, hint string) ([]string, error) { hardCalled = true; return nil, nil },
	)

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-already-done", State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	result := tool.Execute(ctx, map[string]any{"action": "cancel", "session_id": "child-already-done"})
	if !result.IsError {
		t.Fatalf("expected cancel on an already-terminal session to return an explicit error, "+
			"got a success-shaped result: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "terminal") {
		t.Errorf("expected a clear 'already terminal' message, got: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "cooperatively cancelled") || strings.Contains(result.ForLLM, "hard-cancelled") {
		t.Errorf("must not return the success-shaped cancel message for an already-terminal session, got: %s",
			result.ForLLM)
	}
	if softCalled || hardCalled {
		t.Error("expected neither cancel hook to be invoked for an already-terminal session")
	}

	// State must remain unchanged (still "completed", never corrupted).
	rec, err := lc.Load("child-already-done")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if rec.State != session.LifecycleCompleted {
		t.Errorf("state = %q, want unchanged %q", rec.State, session.LifecycleCompleted)
	}
}

func TestDelegateTool_Cancel_NonTerminal_StillSucceeds(t *testing.T) {
	// Regression guard: the #588/N9 fix must not affect the existing
	// non-terminal cancel path (already covered end-to-end by
	// TestDelegateTool_Cancel_SoftThenHardBackstop in delegate_adr053_test.go;
	// this is a narrow, focused duplicate scoped to the new terminal gate).
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetSessionMessagingEnabled(func() bool { return true })
	lc := session.NewLifecycleStore(t.TempDir())
	tool.SetLifecycleStore(lc)
	tool.SetCancelHooks(
		func(sessionID, hint string) ([]string, error) { return nil, nil },
		func(sessionID, hint string) ([]string, error) { return nil, nil },
	)

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-still-running", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	result := tool.Execute(ctx, map[string]any{"action": "cancel", "session_id": "child-still-running", "hard": true})
	if result.IsError {
		t.Fatalf("expected cancel on a non-terminal session to succeed, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "hard-cancelled") {
		t.Errorf("expected the normal hard-cancel success message, got: %s", result.ForLLM)
	}
}
