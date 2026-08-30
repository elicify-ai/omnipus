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
	// ADR-057 FR-021/BDD-20 (W7a): a real delegation now requires a
	// lifecycle store and a resolvable calling-agent identity — neither is
	// this test's concern (it exercises agent_id argument validation), so
	// both are wired past for the two subtests below that reach dispatch.
	tool.SetLifecycleStore(session.NewLifecycleStore(t.TempDir()))
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
		result := tool.Execute(WithAgentID(context.Background(), "test-caller"), map[string]any{"task": "x", "async": false})
		if result.IsError {
			t.Fatalf("expected omitted agent_id to be accepted, got error: %s", result.ForLLM)
		}
	})

	t.Run("non-empty agent_id still accepted", func(t *testing.T) {
		result := tool.Execute(WithAgentID(context.Background(), "test-caller"), map[string]any{"task": "x", "agent_id": "worker-1", "async": false})
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
	// ADR-057 FR-021/BDD-20 (W7a): a real delegation now requires a
	// lifecycle store and a resolvable calling-agent identity — neither is
	// this test's concern (it exercises timeout_seconds threading), so both
	// are wired past.
	tool.SetLifecycleStore(session.NewLifecycleStore(t.TempDir()))
	ctx := WithAgentID(context.Background(), "test-caller")
	t.Cleanup(tool.WaitForAsyncTasks)

	t.Run("nonzero timeout_seconds threads through on the sync path", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{"task": "x", "async": false, "timeout_seconds": 1800})
		if result.IsError {
			t.Fatalf("run failed: %s", result.ForLLM)
		}
		if got := spawner.lastConfig().Timeout; got != 1800*time.Second {
			t.Errorf("Timeout = %v, want %v", got, 1800*time.Second)
		}
	})

	t.Run("nonzero timeout_seconds threads through on the async path", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{"task": "x", "async": true, "timeout_seconds": 900})
		if result.IsError {
			t.Fatalf("run failed: %s", result.ForLLM)
		}
		tool.WaitForAsyncTasks()
		if got := spawner.lastConfig().Timeout; got != 900*time.Second {
			t.Errorf("Timeout = %v, want %v", got, 900*time.Second)
		}
	})

	t.Run("zero/absent timeout_seconds leaves Timeout unset (use spawner default)", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{"task": "x", "async": false})
		if result.IsError {
			t.Fatalf("run failed: %s", result.ForLLM)
		}
		if got := spawner.lastConfig().Timeout; got != 0 {
			t.Errorf("Timeout = %v, want 0 (no override)", got)
		}
	})

	t.Run("explicit zero timeout_seconds also leaves Timeout unset", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{"task": "x", "async": false, "timeout_seconds": 0})
		if result.IsError {
			t.Fatalf("run failed: %s", result.ForLLM)
		}
		if got := spawner.lastConfig().Timeout; got != 0 {
			t.Errorf("Timeout = %v, want 0 (no override)", got)
		}
	})

	t.Run("out-of-bounds (too large) timeout_seconds rejected", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{"task": "x", "async": false, "timeout_seconds": 999999})
		if !result.IsError {
			t.Fatal("expected an out-of-bounds timeout_seconds to be rejected")
		}
		if !strings.Contains(result.ForLLM, "timeout_seconds") {
			t.Errorf("expected error to mention timeout_seconds, got: %s", result.ForLLM)
		}
	})

	t.Run("negative timeout_seconds rejected", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{"task": "x", "async": false, "timeout_seconds": -5})
		if !result.IsError {
			t.Fatal("expected a negative timeout_seconds to be rejected")
		}
	})

	t.Run("non-numeric timeout_seconds rejected", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{"task": "x", "async": false, "timeout_seconds": "soon"})
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

	backing := session.NewLifecycleStore(t.TempDir())
	wrapper := &callCountingLifecycleStore{LifecycleStore: backing}
	tool.SetLifecycleStore(wrapper)

	if err := backing.Persist(&session.LifecycleRecord{
		SessionID: "child-mutate-check", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileSpecialist,
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
	// ADR-057 W12 (deliberate change, not a #581 regression): executeSteer
	// now makes exactly ONE Load() call BEFORE the Mutate above, to verify
	// ownership (the ancestor-chain walk, FR-039) OUTSIDE the atomic
	// closure. Doing it inside the closure (as #581's original fix did) is
	// unsafe post-W12: the walk climbs the ParentDurableKey chain via
	// t.lifecycle.Load(ancestor) for every hop beyond the direct parent, and
	// an ancestor whose id happens to hash to the SAME striped-lock shard as
	// sessionID would deadlock against Mutate's already-held, non-reentrant
	// per-shard mutex. Ownership cannot race the way the terminal state can
	// (ParentDurableKey is immutable after mint — see
	// spawnCorrectiveFollowUp's whole-struct-copy comment), so moving ONLY
	// that check outside Mutate preserves #581's actual TOCTOU fix (the
	// terminal check stays atomic) while avoiding the new deadlock class.
	if loads != 1 {
		t.Errorf("ADR-057 W12: expected executeSteer to make exactly 1 Load() call (the ownership "+
			"pre-check, deliberately outside Mutate — see comment above), got %d", loads)
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
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileSpecialist,
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
	// ADR-057 FR-021/BDD-20 (W7a): a real delegation now requires a
	// lifecycle store — not this test's concern (it exercises
	// ResolvableSessionIDs/t.sessionIndex), so it is wired past.
	tool.SetLifecycleStore(session.NewLifecycleStore(t.TempDir()))
	t.Cleanup(tool.WaitForAsyncTasks)

	ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "parent-1"), "test-caller")
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

// TestDelegateTool_Cancel_AlreadyTerminal_IsIdempotentNoOp covers #588 (N9)
// AND its RC-3 amendment (2026-08, UAT amplification-loop fix) together.
//
// #588's ACTUAL requirement — preserved here — is that cancel on an
// already-terminal session must never reuse the success-shaped
// "cooperatively cancelled" / "hard-cancelled" wording, which would falsely
// claim an action was taken when nothing happened.
//
// #588's original implementation over-generalised that into "must be a
// tool-call FAILURE" (IsError:true). RC-3 found that this drove a real
// orchestrating agent, in production UAT, to read routine cleanup (a worker
// session finishing before the parent's cancel call landed) as breakage —
// 20 of 28 cancel calls in one session hit this branch, and the caller
// re-issued cancels and re-spawned workers in a loop. SessionManager.KillAll
// (pkg/tools/session.go:635-637) already treats an already-terminal
// candidate as a silent no-op; this test now pins THAT contract for cancel
// too: idempotent SUCCESS, with wording distinct enough that it can never be
// mistaken for "I just cancelled something". Do not "re-fix" this back to
// ErrorResult — that resurrects the RC-3 loop while only restoring a
// stricter reading of #588 than #588 itself required.
func TestDelegateTool_Cancel_AlreadyTerminal_IsIdempotentNoOp(t *testing.T) {
	terminalStates := []struct {
		name         string
		state        session.LifecycleState
		failedReason string // Persist requires a non-empty reason when State==LifecycleFailed
	}{
		{"completed", session.LifecycleCompleted, ""},
		{"cancelled", session.LifecycleCancelled, ""},
		{"failed", session.LifecycleFailed, "rc3-test: forced failure"},
	}

	for _, tc := range terminalStates {
		t.Run(tc.name, func(t *testing.T) {
			tool := NewDelegateTool("test-model", 0, 0)
			tool.SetSessionMessagingEnabled(func() bool { return true })
			lc := session.NewLifecycleStore(t.TempDir())
			tool.SetLifecycleStore(lc)

			var softCalled, hardCalled bool
			tool.SetCancelHooks(
				func(sessionID, hint string) ([]string, error) { softCalled = true; return nil, nil },
				func(sessionID, hint string) ([]string, error) { hardCalled = true; return nil, nil },
			)

			sessionID := "child-already-done-" + tc.name
			if err := lc.Persist(&session.LifecycleRecord{
				SessionID: sessionID, State: tc.state, FailedReason: tc.failedReason,
				OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
				WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
			}); err != nil {
				t.Fatalf("seed failed: %v", err)
			}

			ctx := WithTranscriptSessionID(context.Background(), "parent-1")
			args := map[string]any{"action": "cancel", "session_id": sessionID}

			// Cancel twice in a row: true idempotency means both calls
			// return the SAME shape (non-error, "already terminal" wording),
			// not just that the first call is well-formed.
			for attempt := 1; attempt <= 2; attempt++ {
				result := tool.Execute(ctx, args)
				if result.IsError {
					t.Fatalf("attempt %d: expected cancel on an already-terminal session (%s) to be an idempotent "+
						"SUCCESS (RC-3), got an error-shaped result: %s", attempt, tc.state, result.ForLLM)
				}
				if !strings.Contains(result.ForLLM, "terminal") {
					t.Errorf("attempt %d: expected a clear 'already terminal' message, got: %s", attempt, result.ForLLM)
				}
				if strings.Contains(result.ForLLM, "cooperatively cancelled") ||
					strings.Contains(result.ForLLM, "hard-cancelled") {
					t.Errorf("attempt %d: must not return the success-cancel wording for an already-terminal "+
						"session (that would falsely claim an action was taken — #588's actual requirement), got: %s",
						attempt, result.ForLLM)
				}
			}
			if softCalled || hardCalled {
				t.Error("expected neither cancel hook to be invoked for an already-terminal session")
			}

			// State must remain unchanged (never corrupted by the no-op).
			rec, err := lc.Load(sessionID)
			if err != nil {
				t.Fatalf("Load failed: %v", err)
			}
			if rec.State != tc.state {
				t.Errorf("state = %q, want unchanged %q", rec.State, tc.state)
			}
		})
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
		func(sessionID, hint string) ([]string, error) { return []string{"child-still-running"}, nil },
		func(sessionID, hint string) ([]string, error) { return []string{"child-still-running"}, nil },
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

// TestDelegateTool_Cancel_DescendantsMiss_ReturnsTerminalMessage covers the
// TOCTOU window in executeCancel BETWEEN the rec.Terminal() pre-check (which
// passes — the session is still running at that instant) and the cancelSoft/
// cancelHard hook call: the session terminates in that gap, so
// InterruptBySessionKey(Hard) finds no live turnState and returns
// (nil descendants, nil error) — the documented no-op contract. The pre-fix
// executeCancel discarded the descendants return and STILL reported the
// success-shaped "cooperatively cancelled" / "hard-cancelled immediately"
// message, so a cancel that landed nothing looked identical to one that
// actually interrupted. This test asserts that when the hook reports a miss
// (len(descendants)==0, no error), executeCancel returns the explicit
// "terminated between the terminal check and the cancel hook" message instead
// of false success — for BOTH the hard and soft paths.
//
// RC-3 (2026-08) update: this test's own setup never wires a SessionManager,
// so killChildBackgroundShells is a guaranteed no-op (killFailed==0,
// walkIncomplete==false) — a CLEAN TOCTOU miss. Per the RC-3 fix in
// executeCancel, a clean miss is now an idempotent SUCCESS (IsError:false),
// same as the pre-dispatch rec.Terminal() branch covered by
// TestDelegateTool_Cancel_AlreadyTerminal_IsIdempotentNoOp — the assertions
// below were flipped accordingly. This must NOT be confused with a real
// kill-failure/incomplete-walk miss, which delegate_signoff14_test.go's
// TestDelegateTool_Cancel_NothingToCancel_StillSurfacesShellKillWarnings
// pins as STILL an error — see executeCancel's own comment on the
// len(descendants)==0 branches for the split.
func TestDelegateTool_Cancel_DescendantsMiss_ReturnsTerminalMessage(t *testing.T) {
	t.Run("hard", func(t *testing.T) {
		tool := NewDelegateTool("test-model", 0, 0)
		tool.SetSessionMessagingEnabled(func() bool { return true })
		lc := session.NewLifecycleStore(t.TempDir())
		tool.SetLifecycleStore(lc)

		var hardCalled bool
		// Simulate the TOCTOU miss: cancelHard is wired to
		// InterruptBySessionKeyHard, which returns (nil, nil) when no live
		// turnState is registered under sessionKey — the exact signal the
		// target terminated between the pre-check and the hook.
		tool.SetCancelHooks(
			func(sessionID, hint string) ([]string, error) { return nil, nil },
			func(sessionID, hint string) ([]string, error) { hardCalled = true; return nil, nil },
		)

		if err := lc.Persist(&session.LifecycleRecord{
			SessionID: "child-racing", State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
			WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
		}); err != nil {
			t.Fatalf("seed failed: %v", err)
		}

		ctx := WithTranscriptSessionID(context.Background(), "parent-1")
		result := tool.Execute(ctx, map[string]any{"action": "cancel", "session_id": "child-racing", "hard": true})
		// RC-3: a clean TOCTOU miss (no shell-kill failure, no incomplete
		// walk — this test wires no SessionManager) is an idempotent
		// no-op SUCCESS, not an error.
		if result.IsError {
			t.Fatalf("expected a clean terminal-miss to be an idempotent SUCCESS (RC-3), got error-shaped result: %s",
				result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "terminated between the terminal check and the cancel hook") {
			t.Errorf("expected the TOCTOU terminal-miss message, got: %s", result.ForLLM)
		}
		if strings.Contains(result.ForLLM, "hard-cancelled immediately") {
			t.Errorf("must not return the success-shaped hard-cancel message on a descendants miss, got: %s", result.ForLLM)
		}
		if !hardCalled {
			t.Error("expected the hard cancel hook to be invoked (the pre-check passed; the miss happens at the hook)")
		}

		// The lifecycle record must NOT be transitioned to cancelled — the
		// hook landed nothing, so transitionLifecycle was skipped.
		rec, err := lc.Load("child-racing")
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}
		if rec.State != session.LifecycleRunning {
			t.Errorf("state = %q, want unchanged %q (no cancel landed)", rec.State, session.LifecycleRunning)
		}
	})

	t.Run("soft", func(t *testing.T) {
		tool := NewDelegateTool("test-model", 0, 0)
		tool.SetSessionMessagingEnabled(func() bool { return true })
		tool.SetCancelGrace(50 * time.Millisecond) // keep the backstop goroutine bounded for the test
		lc := session.NewLifecycleStore(t.TempDir())
		tool.SetLifecycleStore(lc)

		var softCalled bool
		tool.SetCancelHooks(
			func(sessionID, hint string) ([]string, error) { softCalled = true; return nil, nil },
			func(sessionID, hint string) ([]string, error) { return nil, nil },
		)

		if err := lc.Persist(&session.LifecycleRecord{
			SessionID: "child-racing-soft", State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
			WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
		}); err != nil {
			t.Fatalf("seed failed: %v", err)
		}

		ctx := WithTranscriptSessionID(context.Background(), "parent-1")
		result := tool.Execute(ctx, map[string]any{"action": "cancel", "session_id": "child-racing-soft"})
		// RC-3: a clean TOCTOU miss (no shell-kill failure, no incomplete
		// walk — this test wires no SessionManager) is an idempotent
		// no-op SUCCESS, not an error.
		if result.IsError {
			t.Fatalf("expected a clean terminal-miss to be an idempotent SUCCESS (RC-3), got error-shaped result: %s",
				result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "terminated between the terminal check and the cancel hook") {
			t.Errorf("expected the TOCTOU terminal-miss message, got: %s", result.ForLLM)
		}
		if strings.Contains(result.ForLLM, "cooperatively cancelled") {
			t.Errorf("must not return the success-shaped soft-cancel message on a descendants miss, got: %s", result.ForLLM)
		}
		if !softCalled {
			t.Error("expected the soft cancel hook to be invoked (the pre-check passed; the miss happens at the hook)")
		}
	})
}
