// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the second, independent cause behind the ADR-057 UAT "session …
// is not parked on correlation_id …" symptom (defect chain C2): a child that
// calls message_parent(kind="question", wait=true) has parkNeedsInput
// (pkg/tools/message_parent.go) correctly write session.LifecycleNeedsInput
// onto its OWN durable LifecycleRecord, and runTurn (pkg/agent/loop.go,
// commit 11e494d9) correctly stops the turn instead of finishing it — but
// executeAsync/executeSync's own post-spawn bookkeeping used to run
// unconditionally afterward and, because a parked ToolResult has err==nil,
// IsError==false, Interrupted==false (exactly the shape that falls into the
// `default:` branch), stamped the record LifecycleCompleted right on top of
// the needs_input state parkNeedsInput had just written. That clobber is
// what made `delegate respond` fail closed even once the turn loop itself
// was fixed.
//
// This file proves the fix: a `result.ParksTurn` (pkg/tools/result.go)
// branch in both executeAsync and executeSync must leave the lifecycle
// record exactly as parkNeedsInput left it, on both the async (background)
// and sync (await) dispatch paths, while a NON-parked result on the SAME
// path must still reach LifecycleCompleted (the positive control — without
// it, a test that simply asserts "not completed" would pass vacuously for a
// transition that never fires at all).
//
// Harness reused verbatim from delegate_adr053_test.go: newADR053TestTool
// (real t.TempDir()-backed session.LifecycleStore + permissive delegation-
// deny gate + mock spawner) and runAndExtractSessionID (action=run(async)
// -> durable session_id). spawnerFunc/mockDelegateSpawner are shared test-
// package helpers from delegate_test.go / delegation_deny_wiring_test.go.

package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// simulateParkNeedsInput mirrors MessageParentTool.parkNeedsInput's exact
// write (pkg/tools/message_parent.go:729) onto sessionID's own durable
// record: State -> LifecycleNeedsInput, NeedsInput populated. It stands in
// for the child's real message_parent(wait=true) call, which this package's
// unit-test spawners never actually execute (the mock spawner returns a
// canned *ToolResult, it doesn't run a real child turn). Calling this from
// inside a test spawner, immediately before returning a ParksTurn=true
// result, reproduces the exact ordering the real system guarantees: the
// child's own park write always lands BEFORE the parent's executeAsync/
// executeSync goroutine observes ParksTurn and runs its post-spawn switch.
func simulateParkNeedsInput(t *testing.T, lc *session.LifecycleStore, sessionID, correlationID string) {
	t.Helper()
	if err := lc.Mutate(sessionID, func(rec *session.LifecycleRecord) error {
		rec.State = session.LifecycleNeedsInput
		rec.NeedsInput = &session.NeedsInput{
			CorrelationID: correlationID,
			TTLDeadline:   time.Now().Add(time.Hour),
		}
		return nil
	}); err != nil {
		t.Fatalf("simulateParkNeedsInput: Mutate(%q) failed: %v", sessionID, err)
	}
}

// TestDelegateTool_ExecuteAsync_ParksTurn_LeavesNeedsInputRecord is the core
// property test for the async (background) path.
//
// Traces to: coordinator handoff 2026-08-04, defect chain C2 step 3
// (pkg/tools/delegate.go executeAsync's `case result != nil &&
// result.ParksTurn:` branch, added ahead of `default:`).
func TestDelegateTool_ExecuteAsync_ParksTurn_LeavesNeedsInputRecord(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "parent-1"), "mia")

	tool.SetSpawner(spawnerFunc(func(_ context.Context, cfg SubTurnConfig) (*ToolResult, error) {
		// The child "calls message_parent(wait=true)": parkNeedsInput writes
		// needs_input onto ITS OWN record (cfg.DelegateSessionID) before its
		// turn stops and returns ParksTurn=true — never LifecycleCompleted,
		// never an error.
		simulateParkNeedsInput(t, lc, cfg.DelegateSessionID, "corr-async-1")
		return &ToolResult{ForLLM: "waiting on the parent's confirmation", ParksTurn: true}, nil
	}))

	sessionID := runAndExtractSessionID(t, tool, ctx, "background task that asks a question")

	// Deterministic sync point: WaitForAsyncTasks blocks until executeAsync's
	// goroutine (including its post-spawn switch and the now-guarded
	// transitionLifecycle call) has fully returned — no sleep/poll needed.
	tool.WaitForAsyncTasks()

	rec, err := lc.Load(sessionID)
	if err != nil {
		t.Fatalf("Load after executeAsync's post-spawn switch failed: %v", err)
	}
	if rec.State != session.LifecycleNeedsInput {
		t.Fatalf(
			"state after executeAsync's post-spawn switch = %q, want %q — "+
				"a parked child's needs_input record must NOT be clobbered to completed",
			rec.State, session.LifecycleNeedsInput,
		)
	}
	if rec.NeedsInput == nil {
		t.Fatal("NeedsInput was cleared — parkNeedsInput's write did not survive executeAsync's post-spawn switch")
	}
	if rec.NeedsInput.CorrelationID != "corr-async-1" {
		t.Errorf("NeedsInput.CorrelationID = %q, want %q", rec.NeedsInput.CorrelationID, "corr-async-1")
	}

	// In-memory status (point 5 of the handoff): `delegate status` must
	// report needs_input, not a stale "running" (the pre-transitionLifecycle
	// value) or an incorrectly-clobbered "completed".
	statusResult := tool.Execute(ctx, map[string]any{"action": "status", "session_id": sessionID})
	if statusResult.IsError {
		t.Fatalf("status lookup for the parked task failed: %s", statusResult.ForLLM)
	}
	if !strings.Contains(statusResult.ForLLM, "status=needs_input") {
		t.Errorf("expected delegate(action=status) to report status=needs_input, got: %s", statusResult.ForLLM)
	}
}

// TestDelegateTool_ExecuteAsync_NonParkedResult_StillTransitionsToCompleted is
// the Binding Rule 4 positive control for the async path: a normal
// (non-parked) result on the SAME executeAsync code path must still reach
// LifecycleCompleted. Without this, a test that only asserts "the state
// isn't completed" would pass even for a change that (bug) never transitions
// the record to anything at all.
func TestDelegateTool_ExecuteAsync_NonParkedResult_StillTransitionsToCompleted(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "parent-1"), "mia")
	tool.SetSpawner(&mockDelegateSpawner{}) // returns a plain, non-parked ToolResult

	sessionID := runAndExtractSessionID(t, tool, ctx, "ordinary background task")
	tool.WaitForAsyncTasks()

	rec, err := lc.Load(sessionID)
	if err != nil {
		t.Fatalf("Load after executeAsync failed: %v", err)
	}
	if rec.State != session.LifecycleCompleted {
		t.Errorf("state = %q, want %q (a non-parked result must still complete normally)", rec.State, session.LifecycleCompleted)
	}

	statusResult := tool.Execute(ctx, map[string]any{"action": "status", "session_id": sessionID})
	if statusResult.IsError {
		t.Fatalf("status lookup failed: %s", statusResult.ForLLM)
	}
	if !strings.Contains(statusResult.ForLLM, "status=completed") {
		t.Errorf("expected delegate(action=status) to report status=completed, got: %s", statusResult.ForLLM)
	}
}

// TestDelegateTool_ExecuteSync_ParksTurn_LeavesNeedsInputRecord is the core
// property test for the sync (await, async=false) path — separate code from
// executeAsync, with its own independent bug (executeSync's own `default:`
// branch, guarded by a distinct switch statement).
//
// Traces to: coordinator handoff 2026-08-04, defect chain C2 step 3
// (pkg/tools/delegate.go executeSync's `case result.ParksTurn:` branch,
// added ahead of `default:`, which deliberately calls neither
// transitionLifecycle nor anything that would touch LifecycleCompleted).
func TestDelegateTool_ExecuteSync_ParksTurn_LeavesNeedsInputRecord(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "parent-1"), "mia")

	var mu sync.Mutex
	var capturedSessionID string
	tool.SetSpawner(spawnerFunc(func(_ context.Context, cfg SubTurnConfig) (*ToolResult, error) {
		mu.Lock()
		capturedSessionID = cfg.DelegateSessionID
		mu.Unlock()
		simulateParkNeedsInput(t, lc, cfg.DelegateSessionID, "corr-sync-1")
		return &ToolResult{ForLLM: "waiting on the parent's confirmation", ParksTurn: true}, nil
	}))

	result := tool.Execute(ctx, map[string]any{"task": "sync task that asks a question", "async": false})
	if result.IsError {
		t.Fatalf("executeSync with a parked spawner result must not surface as a tool error, got: %s", result.ForLLM)
	}

	mu.Lock()
	sessionID := capturedSessionID
	mu.Unlock()
	if sessionID == "" {
		t.Fatal("spawner was never invoked with a DelegateSessionID — cannot verify the lifecycle record")
	}

	rec, err := lc.Load(sessionID)
	if err != nil {
		t.Fatalf("Load after executeSync's result switch failed: %v", err)
	}
	if rec.State != session.LifecycleNeedsInput {
		t.Fatalf(
			"state after executeSync's result switch = %q, want %q — "+
				"a parked child's needs_input record must NOT be clobbered to completed",
			rec.State, session.LifecycleNeedsInput,
		)
	}
	if rec.NeedsInput == nil || rec.NeedsInput.CorrelationID != "corr-sync-1" {
		t.Errorf("NeedsInput = %+v, want CorrelationID=%q preserved", rec.NeedsInput, "corr-sync-1")
	}

	// In-memory status: executeSync's finalizeSyncTask must report
	// needs_input, not a stale "running" or a wrong "completed" — mirroring
	// executeAsync's parked case (point 5 of the handoff).
	statusResult := tool.Execute(ctx, map[string]any{"action": "status", "session_id": sessionID})
	if statusResult.IsError {
		t.Fatalf("status lookup for the parked sync task failed: %s", statusResult.ForLLM)
	}
	if !strings.Contains(statusResult.ForLLM, "status=needs_input") {
		t.Errorf("expected delegate(action=status) to report status=needs_input, got: %s", statusResult.ForLLM)
	}
}

// TestDelegateTool_ExecuteSync_NonParkedResult_StillTransitionsToCompleted is
// the Binding Rule 4 positive control for the sync path.
func TestDelegateTool_ExecuteSync_NonParkedResult_StillTransitionsToCompleted(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "parent-1"), "mia")

	var capturedSessionID string
	tool.SetSpawner(spawnerFunc(func(_ context.Context, cfg SubTurnConfig) (*ToolResult, error) {
		capturedSessionID = cfg.DelegateSessionID
		return &ToolResult{ForLLM: "all done", ForUser: "done"}, nil
	}))

	result := tool.Execute(ctx, map[string]any{"task": "ordinary sync task", "async": false})
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.ForLLM)
	}
	if capturedSessionID == "" {
		t.Fatal("spawner was never invoked with a DelegateSessionID — cannot verify the lifecycle record")
	}

	rec, err := lc.Load(capturedSessionID)
	if err != nil {
		t.Fatalf("Load after executeSync failed: %v", err)
	}
	if rec.State != session.LifecycleCompleted {
		t.Errorf("state = %q, want %q (a non-parked result must still complete normally)", rec.State, session.LifecycleCompleted)
	}

	statusResult := tool.Execute(ctx, map[string]any{"action": "status", "session_id": capturedSessionID})
	if statusResult.IsError {
		t.Fatalf("status lookup failed: %s", statusResult.ForLLM)
	}
	if !strings.Contains(statusResult.ForLLM, "status=completed") {
		t.Errorf("expected delegate(action=status) to report status=completed, got: %s", statusResult.ForLLM)
	}
}
