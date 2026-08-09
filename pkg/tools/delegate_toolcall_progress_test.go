package tools

// G1 regression coverage (review finding, criticality 9): before this fix,
// `delegate action=status` could only ever see a running native child's
// PERSISTED transcript entries (recentActivityLines), which are written only
// at full-LLM-round completion. A model spending tens of seconds streaming a
// large tool-call argument (a multi-kilobyte SVG body, a long file write)
// therefore produced NOTHING an orchestrator's status poll could see — bit
// for bit indistinguishable from a hung generation. That ambiguity had a
// real production consequence: an orchestrator polled a delegated worker 75
// times over 46 seconds, saw no activity, concluded it had hung, and killed
// it mid-write — repeatedly.
//
// DelegateProgressReader (delegate.go) is the fix's consumer-side seam:
// action:"status" now also reads a LIVE tool-call-argument progress
// snapshot, sourced from the child turn's own in-memory state
// (pkg/agent.turnState.recordToolCallProgress, wired through
// AgentLoop.ProgressForSession) rather than the persisted transcript alone.
// These tests exercise that seam end-to-end through the real DelegateTool
// (mirroring delegate_status_snapshot_test.go's existing pattern for
// recentActivityLines) using a stub DelegateProgressReader, so pkg/tools
// stays decoupled from pkg/agent exactly like every sibling seam on this
// tool (SubTurnSpawner, DelegateAgentRegistry, DelegateSessionStore).

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// stubProgressReader is a minimal DelegateProgressReader test double, keyed
// by the same DelegateSessionID string a real AgentLoop.ProgressForSession
// would be looked up by.
type stubProgressReader struct {
	mu    sync.Mutex
	byKey map[string]ToolCallProgressSnapshot
}

func (r *stubProgressReader) set(key string, snap ToolCallProgressSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byKey == nil {
		r.byKey = map[string]ToolCallProgressSnapshot{}
	}
	r.byKey[key] = snap
}

func (r *stubProgressReader) ProgressForSession(key string) (ToolCallProgressSnapshot, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snap, ok := r.byKey[key]
	return snap, ok
}

var _ DelegateProgressReader = (*stubProgressReader)(nil)

// TestDelegateStatus_RunningNative_ShowsLiveToolCallProgress is the G1
// regression proof: a running native task whose DelegateProgressReader has a
// live, recently-recorded snapshot gets a "progress:" line in its
// action:"status" output — the exact signal that lets a caller tell "still
// generating a large tool-call argument" apart from "hung". This must FAIL
// against pre-G1 code, which has no progressReader field at all and never
// calls into one.
func TestDelegateStatus_RunningNative_ShowsLiveToolCallProgress(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
	tool.SetLifecycleStore(session.NewLifecycleStore(t.TempDir()))
	t.Cleanup(tool.WaitForAsyncTasks)

	tool.SetAgentRegistry(func() DelegateAgentRegistry {
		return &stubDelegateAgentRegistry{externalCLI: map[string]bool{"ray": false}}
	})

	reader := &stubProgressReader{}
	tool.SetProgressReader(reader)

	release := make(chan struct{})
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		<-release // block so the task stays "running" for the status check below
		return NewToolResult("done"), nil
	}))

	createCtx := WithTranscriptSessionID(context.Background(), "sess-progress-1")
	createCtx = WithToolCallID(createCtx, "call-progress-1")
	createCtx = WithAgentID(createCtx, "test-caller")

	runResult := tool.Execute(createCtx, map[string]any{
		"task": "render a large SVG", "label": "render", "agent_id": "ray",
	})
	if runResult == nil || runResult.IsError {
		t.Fatalf("expected successful delegation, got: %+v", runResult)
	}
	taskID := extractTaskID(t, runResult.ForLLM)
	delegateSessionID := extractDelegateSessionIDFromAck(t, runResult.ForLLM)

	// Simulate the provider's SSE loop mid-stream on a large tool-call
	// argument: a fresh, recently-recorded progress snapshot for the
	// child's own DelegateSessionID — exactly what
	// AgentLoop.ProgressForSession would return for a genuinely live turn.
	reader.set(delegateSessionID, ToolCallProgressSnapshot{
		Name:           "web_serve",
		ArgsBytes:      4096,
		TotalArgsBytes: 4096,
		LastActivity:   time.Now(),
		Age:            2 * time.Second,
	})

	statusResult := tool.Execute(context.Background(), map[string]any{
		"action": "status", "task_id": taskID,
	})
	close(release) // let the blocked goroutine finish so it doesn't leak past the test

	if statusResult == nil || statusResult.IsError {
		t.Fatalf("expected successful status lookup, got: %+v", statusResult)
	}
	if !strings.Contains(statusResult.ForLLM, "status=running") {
		t.Fatalf("expected status=running while the sub-turn is in flight, got: %s", statusResult.ForLLM)
	}
	if !strings.Contains(statusResult.ForLLM, "progress:") {
		t.Fatalf("expected a live 'progress:' line reporting forward progress, got: %s", statusResult.ForLLM)
	}
	if !strings.Contains(statusResult.ForLLM, "generating") {
		t.Errorf("expected the fresh snapshot to render as actively 'generating', got: %s", statusResult.ForLLM)
	}
	if !strings.Contains(statusResult.ForLLM, "web_serve") {
		t.Errorf("expected the in-progress tool name in the progress line, got: %s", statusResult.ForLLM)
	}
	if !strings.Contains(statusResult.ForLLM, "4096") {
		t.Errorf("expected the accumulated argument byte count in the progress line, got: %s", statusResult.ForLLM)
	}
}

// TestDelegateStatus_RunningNative_NoProgressRecorded_OmitsProgressLine is
// the G1 fix's other half of "distinguishable from idle/hung": a running
// task whose progressReader has NOTHING recorded for its DelegateSessionID
// (the common case for most of a turn's life — no streaming tool call in
// flight yet) must NOT render a "progress:" line at all. Comparing this
// test's output against
// TestDelegateStatus_RunningNative_ShowsLiveToolCallProgress's is what
// proves the feature actually lets a caller tell the two states apart,
// rather than always showing the same text regardless of whether progress
// exists.
func TestDelegateStatus_RunningNative_NoProgressRecorded_OmitsProgressLine(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
	tool.SetLifecycleStore(session.NewLifecycleStore(t.TempDir()))
	t.Cleanup(tool.WaitForAsyncTasks)

	tool.SetAgentRegistry(func() DelegateAgentRegistry {
		return &stubDelegateAgentRegistry{externalCLI: map[string]bool{"ray": false}}
	})

	// Wired (unlike a nil progressReader, covered separately below) but
	// empty — ProgressForSession returns ok=false for every key, matching a
	// child that has not yet streamed any tool-call argument.
	tool.SetProgressReader(&stubProgressReader{})

	release := make(chan struct{})
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		<-release
		return NewToolResult("done"), nil
	}))

	createCtx := WithTranscriptSessionID(context.Background(), "sess-progress-2")
	createCtx = WithToolCallID(createCtx, "call-progress-2")
	createCtx = WithAgentID(createCtx, "test-caller")

	runResult := tool.Execute(createCtx, map[string]any{
		"task": "research native", "label": "research", "agent_id": "ray",
	})
	if runResult == nil || runResult.IsError {
		t.Fatalf("expected successful delegation, got: %+v", runResult)
	}
	taskID := extractTaskID(t, runResult.ForLLM)

	statusResult := tool.Execute(context.Background(), map[string]any{
		"action": "status", "task_id": taskID,
	})
	close(release)

	if statusResult == nil || statusResult.IsError {
		t.Fatalf("expected successful status lookup, got: %+v", statusResult)
	}
	if !strings.Contains(statusResult.ForLLM, "status=running") {
		t.Fatalf("expected status=running while the sub-turn is in flight, got: %s", statusResult.ForLLM)
	}
	if strings.Contains(statusResult.ForLLM, "progress:") {
		t.Errorf("must NOT render a 'progress:' line when nothing has been recorded — got: %s",
			statusResult.ForLLM)
	}
}

// TestDelegateStatus_NilProgressReader_DegradesGracefully proves a
// DelegateTool that never had SetProgressReader called (every tool built
// before this fix, and every existing unit test in this package that
// doesn't opt in) behaves exactly as it did before G1 — no panic, no
// "progress:" line, action:"status" unaffected.
func TestDelegateStatus_NilProgressReader_DegradesGracefully(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
	tool.SetLifecycleStore(session.NewLifecycleStore(t.TempDir()))
	t.Cleanup(tool.WaitForAsyncTasks)

	tool.SetAgentRegistry(func() DelegateAgentRegistry {
		return &stubDelegateAgentRegistry{externalCLI: map[string]bool{"ray": false}}
	})
	// Deliberately no SetProgressReader call.

	release := make(chan struct{})
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		<-release
		return NewToolResult("done"), nil
	}))

	createCtx := WithTranscriptSessionID(context.Background(), "sess-progress-3")
	createCtx = WithToolCallID(createCtx, "call-progress-3")
	createCtx = WithAgentID(createCtx, "test-caller")

	runResult := tool.Execute(createCtx, map[string]any{
		"task": "research native", "label": "research", "agent_id": "ray",
	})
	if runResult == nil || runResult.IsError {
		t.Fatalf("expected successful delegation, got: %+v", runResult)
	}
	taskID := extractTaskID(t, runResult.ForLLM)

	statusResult := tool.Execute(context.Background(), map[string]any{
		"action": "status", "task_id": taskID,
	})
	close(release)

	if statusResult == nil || statusResult.IsError {
		t.Fatalf("expected successful status lookup (no panic from a nil progressReader), got: %+v", statusResult)
	}
	if strings.Contains(statusResult.ForLLM, "progress:") {
		t.Errorf("a nil progressReader must not render a 'progress:' line — got: %s", statusResult.ForLLM)
	}
}

// TestFormatToolCallProgressLine_FreshVsStale is a pure-function unit test
// for the "still generating" vs. "may have stalled" distinction
// formatToolCallProgressLine renders, without needing to wait
// maxToolCallProgressStaleness (5 minutes) in real time. This is the second
// of the two required scenarios: a recently-recorded snapshot and a
// stale one must render text a human or orchestrator can tell apart.
func TestFormatToolCallProgressLine_FreshVsStale(t *testing.T) {
	fresh := formatToolCallProgressLine(ToolCallProgressSnapshot{
		Name: "web_serve", ArgsBytes: 100, TotalArgsBytes: 100, Age: 2 * time.Second,
	})
	if !strings.Contains(fresh, "generating") {
		t.Errorf("expected a fresh snapshot to render as 'generating', got: %s", fresh)
	}
	if strings.Contains(fresh, "stale") {
		t.Errorf("a fresh snapshot must not render as stale, got: %s", fresh)
	}

	stale := formatToolCallProgressLine(ToolCallProgressSnapshot{
		Name: "web_serve", ArgsBytes: 100, TotalArgsBytes: 100, Age: 10 * time.Minute,
	})
	if !strings.Contains(stale, "stale") {
		t.Errorf("expected a snapshot older than maxToolCallProgressStaleness to render as stale, got: %s", stale)
	}

	if fresh == stale {
		t.Fatal("fresh and stale snapshots must render distinguishably — a caller has no way to tell " +
			"'still working' from 'may have stalled' otherwise")
	}
}
