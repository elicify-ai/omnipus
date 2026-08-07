package tools

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// --- test doubles (W2: delegate action:"status" live-progress snapshot) ---

// stubDelegateSessionStore is a minimal DelegateSessionStore test double
// keyed by session ID, mirroring stubSessionStore in handoff_test.go.
type stubDelegateSessionStore struct {
	mu      sync.Mutex
	entries map[string][]session.TranscriptEntry
}

func (s *stubDelegateSessionStore) ReadTranscript(sessionID string) ([]session.TranscriptEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]session.TranscriptEntry(nil), s.entries[sessionID]...), nil
}

// stubDelegateAgentRegistry is a minimal DelegateAgentRegistry test double.
type stubDelegateAgentRegistry struct {
	externalCLI map[string]bool
}

func (r *stubDelegateAgentRegistry) IsExternalCLI(agentID string) bool {
	return r.externalCLI[agentID]
}

// extractDelegateSessionIDFromAck pulls the "session_id: X" token out of an
// async delegate ack message (ADR-057 FR-043: recentActivityLines now reads
// via the child's own DelegateSessionID, not the shared transcript session
// id captured at task-creation time — so a test seeding synthetic
// transcript activity must key it by THIS value, which is only known after
// the run ack, not chosen up front).
func extractDelegateSessionIDFromAck(t *testing.T, forLLM string) string {
	t.Helper()
	const marker = "session_id: "
	idx := strings.Index(forLLM, marker)
	if idx == -1 {
		t.Fatalf("expected %q in run ack, got: %s", marker, forLLM)
	}
	rest := forLLM[idx+len(marker):]
	end := strings.IndexAny(rest, ")\n")
	if end == -1 {
		t.Fatalf("could not find end of session_id in ack: %s", forLLM)
	}
	return rest[:end]
}

// TestDelegateStatus_RunningNative_IncludesRecentActivity is the regression
// proof for W2: action:"status" on a RUNNING NATIVE task must surface the
// child sub-turn's recent transcript activity. The data already exists —
// delegated children share their parent's transcript session, and every
// intermediate/final assistant-text entry the child writes is tagged with
// ParentSpawnCallID == the delegate tool call's own ID (see
// session.TranscriptEntry.ParentSpawnCallID's doc comment and
// pkg/agent/subturn.go's parentSpawnCallID) — this feature reads that back
// instead of leaving the calling LLM blind until completion.
func TestDelegateStatus_RunningNative_IncludesRecentActivity(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
	// ADR-057 FR-021/BDD-20 (W7a): a real delegation now requires a lifecycle
	// store and a resolvable calling-agent identity — neither is this test's
	// concern (it exercises the action:"status" activity snapshot), so both
	// are wired past.
	tool.SetLifecycleStore(session.NewLifecycleStore(t.TempDir()))
	t.Cleanup(tool.WaitForAsyncTasks)

	store := &stubDelegateSessionStore{entries: map[string][]session.TranscriptEntry{}}
	tool.SetSessionStore(store)
	tool.SetAgentRegistry(func() DelegateAgentRegistry {
		return &stubDelegateAgentRegistry{externalCLI: map[string]bool{"ray": false}}
	})

	release := make(chan struct{})
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		<-release // block so the task stays "running" for the status check below
		return NewToolResult("done"), nil
	}))

	// Task-creation context carries the correlation anchors DelegateTool
	// captures at creation time (W2): the transcript session ID and this
	// call's own tool-call ID — mirroring what pkg/agent/loop.go injects via
	// tools.WithTranscriptSessionID / tools.WithToolCallID before every real
	// tool dispatch. WithAgentID carries the CALLING agent's own identity
	// (FR-015) — distinct from "agent_id" in the args below, which names the
	// delegation TARGET.
	createCtx := WithTranscriptSessionID(context.Background(), "sess-native-1")
	createCtx = WithToolCallID(createCtx, "call-native-1")
	createCtx = WithAgentID(createCtx, "test-caller")

	runResult := tool.Execute(createCtx, map[string]any{
		"task": "research native", "label": "research", "agent_id": "ray",
	})
	if runResult == nil || runResult.IsError {
		t.Fatalf("expected successful delegation, got: %+v", runResult)
	}
	taskID := extractTaskID(t, runResult.ForLLM)
	// ADR-057 FR-043: recentActivityLines now reads via the child's OWN
	// DelegateSessionID (a fresh UUID minted at run time), not the shared
	// transcript session id ("sess-native-1") captured above — seed the
	// synthetic transcript activity under THAT id instead.
	delegateSessionID := extractDelegateSessionIDFromAck(t, runResult.ForLLM)

	// Seed the child sub-turn's own transcript activity — exactly what
	// pkg/agent/turn.go's appendIntermediateAssistantTranscript writes for a
	// running child, tagged with ParentSpawnCallID == this task's own
	// SpawnCallID (call-native-1). One unrelated sibling entry (a different
	// spawn call sharing the same session) proves the filter, not just
	// presence.
	store.mu.Lock()
	store.entries[delegateSessionID] = []session.TranscriptEntry{
		{Role: "assistant", Content: "Let me search for X first...", ParentSpawnCallID: "call-native-1"},
		{
			Role: "assistant", Content: "unrelated sibling sub-turn narration",
			ParentSpawnCallID: "some-other-call",
		},
		{
			Role: "assistant", Content: "Found 3 relevant sources, now analyzing them in depth.",
			ParentSpawnCallID: "call-native-1",
		},
	}
	store.mu.Unlock()

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
	if !strings.Contains(statusResult.ForLLM, "recent activity:") {
		t.Errorf("expected a 'recent activity:' snapshot section, got: %s", statusResult.ForLLM)
	}
	if !strings.Contains(statusResult.ForLLM, "Let me search for X first...") {
		t.Errorf("expected the child's own narration in the snapshot, got: %s", statusResult.ForLLM)
	}
	if !strings.Contains(statusResult.ForLLM, "Found 3 relevant sources") {
		t.Errorf("expected the child's second narration line in the snapshot, got: %s", statusResult.ForLLM)
	}
	if strings.Contains(statusResult.ForLLM, "unrelated sibling sub-turn narration") {
		t.Errorf("must NOT leak another spawn call's narration into this task's snapshot, got: %s", statusResult.ForLLM)
	}
}

// TestDelegateStatus_RunningNative_NoMatchingActivity_OmitsHeader is distinct
// from TestDelegateStatus_RunningNative_IncludesRecentActivity's "no store
// wired" degrade case (recentActivityLines' own doc comment: nil sessionStore
// is one no-op branch) — here the session store IS wired and readable, but
// simply has ZERO entries whose ParentSpawnCallID matches this task's own
// SpawnCallID (the child sub-turn hasn't written anything yet, e.g. it is
// still on its very first LLM call). delegateStatusExtra must fall through
// exactly the same "" (len(lines)==0) branch either way, so the rendered
// status output must be byte-identical to the pre-W2 shape: status=running
// with NO "recent activity:" header at all, not an empty/dangling one.
func TestDelegateStatus_RunningNative_NoMatchingActivity_OmitsHeader(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
	// ADR-057 FR-021/BDD-20 (W7a): see the sibling test's comment.
	tool.SetLifecycleStore(session.NewLifecycleStore(t.TempDir()))
	t.Cleanup(tool.WaitForAsyncTasks)

	// Store is wired (unlike a nil sessionStore) and has real data — for an
	// UNRELATED session id, just to prove a wired-but-non-matching store
	// behaves identically to a wired-and-empty one; the actual child session
	// id is not known until after the run ack (FR-043 — see below).
	store := &stubDelegateSessionStore{entries: map[string][]session.TranscriptEntry{
		"unrelated-session": {
			{
				Role: "assistant", Content: "unrelated entry from a different spawn call",
				ParentSpawnCallID: "some-other-call",
			},
		},
	}}
	tool.SetSessionStore(store)
	tool.SetAgentRegistry(func() DelegateAgentRegistry {
		return &stubDelegateAgentRegistry{externalCLI: map[string]bool{"ray": false}}
	})

	release := make(chan struct{})
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		<-release // block so the task stays "running" for the status check below
		return NewToolResult("done"), nil
	}))

	createCtx := WithTranscriptSessionID(context.Background(), "sess-native-2")
	createCtx = WithToolCallID(createCtx, "call-native-2")
	createCtx = WithAgentID(createCtx, "test-caller")

	runResult := tool.Execute(createCtx, map[string]any{
		"task": "research native", "label": "research", "agent_id": "ray",
	})
	if runResult == nil || runResult.IsError {
		t.Fatalf("expected successful delegation, got: %+v", runResult)
	}
	taskID := extractTaskID(t, runResult.ForLLM)

	// No entries are seeded for the child's own DelegateSessionID — the
	// store stays exactly as constructed above (only the unrelated entry
	// under an unrelated key).

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
	if strings.Contains(statusResult.ForLLM, "recent activity:") {
		t.Errorf("a wired store with zero matching entries must NOT render a 'recent activity:' "+
			"header (byte-identical to the pre-W2 shape) — got: %s", statusResult.ForLLM)
	}
	if strings.Contains(statusResult.ForLLM, "unrelated entry from a different spawn call") {
		t.Errorf("must NOT leak another spawn call's narration into this task's snapshot, got: %s",
			statusResult.ForLLM)
	}
}

// TestDelegateStatus_RunningExternalCLI_NoLiveSnapshot proves the SCOPE
// boundary for W2: a RUNNING subagent_3p (external-CLI) task's
// action:"status" must NOT attempt a live transcript snapshot — external-CLI
// dispatch is treated as batch/report-on-completion by design (see
// DelegateTaskState.Is3P's doc comment) — and instead renders a fixed note,
// even when matching transcript entries already exist in the store.
func TestDelegateStatus_RunningExternalCLI_NoLiveSnapshot(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
	// ADR-057 FR-021/BDD-20 (W7a): see the sibling tests' comment.
	tool.SetLifecycleStore(session.NewLifecycleStore(t.TempDir()))
	t.Cleanup(tool.WaitForAsyncTasks)

	store := &stubDelegateSessionStore{entries: map[string][]session.TranscriptEntry{
		"sess-3p-1": {
			{
				Role: "assistant", Content: "external CLI narration that must stay hidden",
				ParentSpawnCallID: "call-3p-1",
			},
		},
	}}
	tool.SetSessionStore(store)
	tool.SetAgentRegistry(func() DelegateAgentRegistry {
		return &stubDelegateAgentRegistry{externalCLI: map[string]bool{"codex-worker": true}}
	})

	release := make(chan struct{})
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		<-release
		return NewToolResult("done"), nil
	}))

	createCtx := WithTranscriptSessionID(context.Background(), "sess-3p-1")
	createCtx = WithToolCallID(createCtx, "call-3p-1")
	createCtx = WithAgentID(createCtx, "test-caller")

	runResult := tool.Execute(createCtx, map[string]any{
		"task": "research via external cli", "agent_id": "codex-worker",
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
	if strings.Contains(statusResult.ForLLM, "external CLI narration that must stay hidden") {
		t.Errorf("subagent_3p tasks must NEVER get a live transcript snapshot, got: %s", statusResult.ForLLM)
	}
	if strings.Contains(statusResult.ForLLM, "recent activity:") {
		t.Errorf("subagent_3p tasks must not render the native 'recent activity:' section, got: %s",
			statusResult.ForLLM)
	}
	if !strings.Contains(statusResult.ForLLM, "external agent — no live progress; results on completion") {
		t.Errorf("expected the fixed no-live-progress note, got: %s", statusResult.ForLLM)
	}
}
