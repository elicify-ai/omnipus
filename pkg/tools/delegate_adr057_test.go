// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 U14 (Wave F) — pkg/tools/delegate.go coverage for:
//   - W7a (BDD-20, FR-021): delegation MUST be refused, not silently
//     degraded, when no durable lifecycle store is wired.
//   - W12 (BDD-41…BDD-44, FR-039/FR-040): the ownership check is an
//     ancestor-chain walk, not a one-hop equality — a direct parent,
//     grandparent, etc. up to a configured depth bound may act on a
//     descendant; a sibling/cousin may not.
//   - W14 (BDD-49…BDD-52, FR-043…FR-045/FR-087): action:"status" no longer
//     silently reports emptiness for a completed sync delegation or a
//     running task whose activity snapshot genuinely has nothing yet, and
//     t.tasks/t.sessionIndex are bounded.
//
// Per Rule 5/6: this is a NEW file, and its unexported helpers are
// u14-prefixed so a same-wave package-level name never collides with
// another unit's new file in pkg/tools.

package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// u14CountingSpawner implements SubTurnSpawner and records how many times
// SpawnSubTurn was actually invoked — used by the W7a refusal test to prove
// a refused delegation never reaches dispatch at all (BDD-20's "no child
// session is created" clause), not merely that the returned ToolResult
// happens to carry an error.
type u14CountingSpawner struct {
	calls int
}

func (s *u14CountingSpawner) SpawnSubTurn(ctx context.Context, cfg SubTurnConfig) (*ToolResult, error) {
	s.calls++
	return &ToolResult{ForLLM: "spawned", ForUser: "spawned"}, nil
}

// u14PermissiveTool builds a DelegateTool with a u14CountingSpawner and
// permissive delegation-deny gates for both modes, WITHOUT wiring a
// lifecycle store — the caller decides whether to call SetLifecycleStore.
func u14PermissiveTool(t *testing.T) (*DelegateTool, *u14CountingSpawner) {
	t.Helper()
	tool := NewDelegateTool("test-model", 0, 0)
	spawner := &u14CountingSpawner{}
	tool.SetSpawner(spawner)
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
	tool.SetDelegationDenyCheckerAwait(func(context.Context, string) *DelegationDenial { return nil })
	t.Cleanup(tool.WaitForAsyncTasks)
	return tool, spawner
}

// TestDelegate_RefusedWithoutLifecycleStore is test #48 (BDD-20, FR-021,
// W7a). Before this fix, executeRun's `if t.lifecycle != nil { mint +
// persist }` block was simply SKIPPED when no lifecycle store was wired,
// and execution fell straight through to executeAsync/executeSync below —
// spawning a real child sub-turn with no durable record at all and
// returning a success-shaped result, exactly the silent degradation
// BDD-20 forbids ("it is refused with an operator-visible error... no
// child session is created and no success payload is returned").
//
// Both the negative assertion (refused, spawner never called) AND its
// positive lower bound (with a lifecycle store wired, the identical call
// DOES succeed and DOES reach the spawner) are asserted here per Rule 4 —
// a zero-count check with no positive control would also pass if
// SetSpawner or the deny-checker wiring were silently broken instead.
func TestDelegate_RefusedWithoutLifecycleStore(t *testing.T) {
	t.Run("no lifecycle store: refused, no dispatch (async)", func(t *testing.T) {
		tool, spawner := u14PermissiveTool(t)
		// Deliberately never call tool.SetLifecycleStore — t.lifecycle stays nil.
		ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "u14-w7a-chat-A"), "u14-agent")
		result := tool.Execute(ctx, map[string]any{"task": "do something", "async": true})
		if !result.IsError {
			t.Fatalf("BDD-20: expected the delegation to be refused when no lifecycle store is configured, "+
				"got a success-shaped result: %s", result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "lifecycle store") {
			t.Errorf("expected an operator-visible error naming the missing lifecycle store, got: %s", result.ForLLM)
		}
		if spawner.calls != 0 {
			t.Errorf("BDD-20: expected NO child session to be created (spawner never invoked) when the "+
				"delegation is refused, got %d SpawnSubTurn call(s)", spawner.calls)
		}
	})

	t.Run("no lifecycle store: refused, no dispatch (sync)", func(t *testing.T) {
		tool, spawner := u14PermissiveTool(t)
		ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "u14-w7a-chat-A-sync"), "u14-agent")
		result := tool.Execute(ctx, map[string]any{"task": "do something", "async": false})
		if !result.IsError {
			t.Fatalf("BDD-20 (sync path): expected refusal, got success: %s", result.ForLLM)
		}
		if spawner.calls != 0 {
			t.Errorf("BDD-20 (sync path): expected no dispatch, got %d SpawnSubTurn call(s)", spawner.calls)
		}
	})

	t.Run("positive control: with a lifecycle store wired, the same call proceeds", func(t *testing.T) {
		tool, spawner := u14PermissiveTool(t)
		lc := session.NewLifecycleStore(t.TempDir())
		tool.SetLifecycleStore(lc)

		ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "u14-w7a-chat-A-control"), "u14-agent")
		result := tool.Execute(ctx, map[string]any{"task": "do something", "async": false})
		if result.IsError {
			t.Fatalf("expected the delegation to succeed once a lifecycle store is wired, got error: %s", result.ForLLM)
		}
		if spawner.calls == 0 {
			t.Error("expected the spawner to have been invoked at least once — proves the zero-call " +
				"assertions above are testing the real refusal path, not a coincidentally-broken spawner wire-up")
		}
	})
}

// u14SeedChild persists a minimal, non-terminal LifecycleRecord for
// sessionID whose ParentDurableKey is parentDurableKey (its DIRECT parent,
// one hop — see pkg/session/lifecycle.go's own doc comment on the field).
//
// LaunchProfile is "specialist" (not the "utility" default) so this helper
// remains usable by every gated action this file exercises via the shared
// ownership-walk tests, including steer/respond (G-8: a "utility" launch
// refuses both — see requireSteerableLaunchProfile in delegate.go). The
// ownership-walk assertions here are about WHO may act, not about the
// launch_profile contract, so a uniformly steerable profile keeps that
// axis out of the way.
func u14SeedChild(t *testing.T, lc *session.LifecycleStore, sessionID, parentDurableKey string) {
	t.Helper()
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID:        sessionID,
		State:            session.LifecycleRunning,
		OwnerScopeKind:   session.OwnerScopeHuman,
		ParentDurableKey: parentDurableKey,
		WorkspaceID:      "ws-1",
		AgentID:          "worker",
		LaunchProfile:    session.LaunchProfileSpecialist,
	}); err != nil {
		t.Fatalf("u14SeedChild(%s, parent=%s): seed failed: %v", sessionID, parentDurableKey, err)
	}
}

// TestOwnershipWalk_SiblingRejectedAncestorAllowed is test #50 (BDD-41,
// BDD-42). Pre-ADR-057, a plain one-hop ParentDurableKey equality check
// happened to reject BOTH the illegitimate sibling case AND the legitimate
// root-over-grandchild case, because ParentDurableKey used to be shared
// across the whole subtree (so equality passed for cousins too — the leak
// FR-040 closes) OR named only the immediate parent post-U13 (so equality
// rejected a root reaching its own grandchild — the regression BDD-42
// exists to fix). This test proves the W12 ancestor walk gets BOTH right
// at once: A (root chat) → B (child) → D (grandchild), with C as B's
// sibling.
func TestOwnershipWalk_SiblingRejectedAncestorAllowed(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)

	u14SeedChild(t, lc, "u14-ow-child-B", "u14-ow-chat-A")
	u14SeedChild(t, lc, "u14-ow-child-C", "u14-ow-chat-A")
	u14SeedChild(t, lc, "u14-ow-grandchild-D", "u14-ow-child-B")

	t.Run("sibling cannot address another sibling (BDD-41)", func(t *testing.T) {
		callerB := WithTranscriptSessionID(context.Background(), "u14-ow-child-B")
		result := tool.Execute(callerB, map[string]any{"action": "peek", "session_id": "u14-ow-child-C"})
		if !result.IsError {
			t.Fatalf("BDD-41: expected sibling B to be rejected acting on sibling C, got success: %s", result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "not owned") {
			t.Errorf("expected an ownership rejection message, got: %s", result.ForLLM)
		}
	})

	t.Run("root chat can still address a grandchild (BDD-42)", func(t *testing.T) {
		callerA := WithTranscriptSessionID(context.Background(), "u14-ow-chat-A")
		result := tool.Execute(callerA, map[string]any{"action": "peek", "session_id": "u14-ow-grandchild-D"})
		if result.IsError {
			t.Fatalf("BDD-42: expected root chat A to reach grandchild D via the ancestor walk, got error: %s",
				result.ForLLM)
		}
	})

	t.Run("positive lower bound: direct parent still allowed (regression)", func(t *testing.T) {
		// Proves the walk's depth-1 (direct parent) case — the ONLY case the
		// pre-W12 equality check ever handled — was not broken by this
		// rewrite; without this, the two subtests above could both be
		// "passing" only because every ownership check now vacuously denies
		// or vacuously allows.
		callerA := WithTranscriptSessionID(context.Background(), "u14-ow-chat-A")
		result := tool.Execute(callerA, map[string]any{"action": "peek", "session_id": "u14-ow-child-B"})
		if result.IsError {
			t.Fatalf("expected direct parent A to reach its own child B, got error: %s", result.ForLLM)
		}
	})
}

// TestOwnershipWalk_DepthBounded is test #51 (BDD-43): the walk must
// terminate at the configured max delegation depth rather than climbing
// (or scanning) indefinitely. A chain of X0 (root, untracked) → X1 → X2 →
// X3 → X4 (target) is 4 hops from X4 back to X0; bounding the walk at 2
// must reject, and widening it must permit — proving the rejection above
// is the DEPTH BOUND firing, not some unrelated ownership bug.
func TestOwnershipWalk_DepthBounded(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)

	u14SeedChild(t, lc, "u14-depth-X1", "u14-depth-X0")
	u14SeedChild(t, lc, "u14-depth-X2", "u14-depth-X1")
	u14SeedChild(t, lc, "u14-depth-X3", "u14-depth-X2")
	u14SeedChild(t, lc, "u14-depth-X4", "u14-depth-X3")

	caller := WithTranscriptSessionID(context.Background(), "u14-depth-X0")

	t.Run("bounded at 2: rejects before reaching the caller", func(t *testing.T) {
		tool.SetOwnershipWalkMaxDepth(2)
		result := tool.Execute(caller, map[string]any{"action": "peek", "session_id": "u14-depth-X4"})
		if !result.IsError {
			t.Fatalf("BDD-43: expected the walk to stop at depth bound 2 (X4 needs depth 4 to reach X0), "+
				"got success: %s", result.ForLLM)
		}
	})

	t.Run("positive control: a wide-enough bound reaches the caller", func(t *testing.T) {
		tool.SetOwnershipWalkMaxDepth(10)
		result := tool.Execute(caller, map[string]any{"action": "peek", "session_id": "u14-depth-X4"})
		if result.IsError {
			t.Fatalf("expected a depth bound of 10 to let X0 reach X4 (4 hops) — proves the walk mechanism "+
				"itself works and the bounded case above is a real depth cutoff, not a vacuous always-reject: %s",
				result.ForLLM)
		}
	})
}

// TestOwnershipWalk_AllSixGatedActions is test #52 (BDD-44): every one of
// the six gated call sites (inbox, steer, respond, cancel, follow_up,
// peek) uses the SAME ancestor walk — root chat A can reach grandchild D
// through each of them, and B's sibling C is rejected by each of them.
// Each action gets its own D session (and, where needed, its own
// preconditions) so no subtest's side effects leak into another's.
func TestOwnershipWalk_AllSixGatedActions(t *testing.T) {
	tool, lc, inbox, steer := newADR053TestTool(t)

	const chatA = "u14-six-chat-A"
	const childB = "u14-six-child-B"
	const siblingC = "u14-six-sibling-C"
	u14SeedChild(t, lc, childB, chatA)
	u14SeedChild(t, lc, siblingC, chatA)

	callerA := WithTranscriptSessionID(context.Background(), chatA)
	callerC := WithTranscriptSessionID(context.Background(), siblingC)

	t.Run("inbox", func(t *testing.T) {
		const d = "u14-six-inbox-D"
		u14SeedChild(t, lc, d, childB)

		rejected := tool.Execute(callerC, map[string]any{"action": "inbox", "session_id": d})
		if !rejected.IsError {
			t.Fatalf("expected sibling C to be rejected for inbox against D, got success: %s", rejected.ForLLM)
		}

		allowed := tool.Execute(callerA, map[string]any{"action": "inbox", "session_id": d})
		if allowed.IsError {
			t.Fatalf("expected root chat A to be permitted for inbox against grandchild D, got error: %s",
				allowed.ForLLM)
		}
	})

	t.Run("steer", func(t *testing.T) {
		const d = "u14-six-steer-D"
		u14SeedChild(t, lc, d, childB)

		rejected := tool.Execute(callerC, map[string]any{"action": "steer", "session_id": d, "text": "hi"})
		if !rejected.IsError {
			t.Fatalf("expected sibling C to be rejected for steer against D, got success: %s", rejected.ForLLM)
		}

		allowed := tool.Execute(callerA, map[string]any{"action": "steer", "session_id": d, "text": "hi"})
		if allowed.IsError {
			t.Fatalf("expected root chat A to be permitted for steer against grandchild D, got error: %s",
				allowed.ForLLM)
		}
		if _, scope := steer.last(); scope != d {
			t.Errorf("expected the steering message to be scoped to %q, got %q", d, scope)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		const d = "u14-six-cancel-D"
		u14SeedChild(t, lc, d, childB)
		tool.SetCancelHooks(
			func(sessionID, hint string) ([]string, error) { return []string{sessionID}, nil },
			func(sessionID, hint string) ([]string, error) { return []string{sessionID}, nil },
		)

		rejected := tool.Execute(callerC, map[string]any{"action": "cancel", "session_id": d})
		if !rejected.IsError {
			t.Fatalf("expected sibling C to be rejected for cancel against D, got success: %s", rejected.ForLLM)
		}

		allowed := tool.Execute(callerA, map[string]any{"action": "cancel", "session_id": d})
		if allowed.IsError {
			t.Fatalf("expected root chat A to be permitted for cancel against grandchild D, got error: %s",
				allowed.ForLLM)
		}
	})

	t.Run("follow_up", func(t *testing.T) {
		const d = "u14-six-followup-D"
		if err := lc.Persist(&session.LifecycleRecord{
			SessionID: d, State: session.LifecycleCompleted, OwnerScopeKind: session.OwnerScopeHuman,
			ParentDurableKey: childB, WorkspaceID: "ws-1", AgentID: "worker",
			LaunchProfile: session.LaunchProfileUtility,
		}); err != nil {
			t.Fatalf("seed failed: %v", err)
		}

		rejected := tool.Execute(callerC, map[string]any{"action": "follow_up", "session_id": d, "text": "more"})
		if !rejected.IsError {
			t.Fatalf("expected sibling C to be rejected for follow_up against D, got success: %s", rejected.ForLLM)
		}

		allowed := tool.Execute(callerA, map[string]any{"action": "follow_up", "session_id": d, "text": "more"})
		if allowed.IsError {
			t.Fatalf("expected root chat A to be permitted for follow_up against terminal grandchild D, got error: %s",
				allowed.ForLLM)
		}
	})

	t.Run("respond", func(t *testing.T) {
		const d = "u14-six-respond-D"
		if err := lc.Persist(&session.LifecycleRecord{
			SessionID: d, State: session.LifecycleNeedsInput, OwnerScopeKind: session.OwnerScopeHuman,
			ParentDurableKey: childB, WorkspaceID: "ws-1", AgentID: "worker",
			LaunchProfile: session.LaunchProfileSpecialist,
			NeedsInput:    &session.NeedsInput{CorrelationID: "u14-corr-1"},
		}); err != nil {
			t.Fatalf("seed failed: %v", err)
		}
		// The question must be positively verifiable in D's DIRECT parent's
		// (childB's) inbox with self_ok authority, or executeRespond's
		// separate R§8.2 authority gate denies it regardless of ownership.
		selfOK := generated.SessionMessageQuestionAuthority("self_ok")
		var sm generated.SessionMessage
		if err := sm.FromSessionMessageQuestion(generated.SessionMessageQuestion{
			MessageId: "u14-msg-1", SessionId: d, CreatedAt: time.Now().UTC(), Depth: 1,
			UntrustedOrigin: true, SenderIdentity: "worker", Text: "need input",
			Wait: false, CorrelationId: "u14-corr-1", Authority: &selfOK,
		}); err != nil {
			t.Fatalf("encode question failed: %v", err)
		}
		if _, err := inbox.Append(childB, sm); err != nil {
			t.Fatalf("seed inbox message failed: %v", err)
		}

		rejected := tool.Execute(callerC, map[string]any{
			"action": "respond", "session_id": d, "correlation_id": "u14-corr-1", "text": "answer",
		})
		if !rejected.IsError {
			t.Fatalf("expected sibling C to be rejected for respond against D, got success: %s", rejected.ForLLM)
		}

		allowed := tool.Execute(callerA, map[string]any{
			"action": "respond", "session_id": d, "correlation_id": "u14-corr-1", "text": "answer",
		})
		if allowed.IsError {
			t.Fatalf("expected root chat A to be permitted for respond against grandchild D, got error: %s",
				allowed.ForLLM)
		}
	})

	t.Run("peek", func(t *testing.T) {
		const d = "u14-six-peek-D"
		u14SeedChild(t, lc, d, childB)

		rejected := tool.Execute(callerC, map[string]any{"action": "peek", "session_id": d})
		if !rejected.IsError {
			t.Fatalf("expected sibling C to be rejected for peek against D, got success: %s", rejected.ForLLM)
		}

		allowed := tool.Execute(callerA, map[string]any{"action": "peek", "session_id": d})
		if allowed.IsError {
			t.Fatalf("expected root chat A to be permitted for peek against grandchild D, got error: %s",
				allowed.ForLLM)
		}
	})
}

// TestDelegateStatus_SyncAndAsyncSnapshotsNonEmpty is test #55 (BDD-49,
// BDD-50, FR-044). Before this fix, executeSync never wrote to t.tasks/
// t.sessionIndex at all — only executeAsync did — so action:"status" for a
// completed SYNCHRONOUS (await) delegation could not even find the task
// (a harder failure than "empty": genuinely absent, "No subagent found").
func TestDelegateStatus_SyncAndAsyncSnapshotsNonEmpty(t *testing.T) {
	t.Run("sync", func(t *testing.T) {
		tool, _, _, _ := newADR053TestTool(t)
		ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "u14-status-sync-parent"), "u14-agent")
		result := tool.Execute(ctx, map[string]any{"task": "do the sync thing", "async": false})
		if result.IsError {
			t.Fatalf("sync run failed: %s", result.ForLLM)
		}

		tool.mu.Lock()
		var sessionID string
		for sid := range tool.sessionIndex {
			sessionID = sid
		}
		tool.mu.Unlock()
		if sessionID == "" {
			t.Fatal("BDD-49/FR-044: expected executeSync to register an entry in t.sessionIndex, found none")
		}

		statusResult := tool.Execute(ctx, map[string]any{"action": "status", "session_id": sessionID})
		if statusResult.IsError {
			t.Fatalf("BDD-49: expected action=status to find the completed sync delegation, got error: %s",
				statusResult.ForLLM)
		}
		if strings.TrimSpace(statusResult.ForLLM) == "" {
			t.Error("BDD-49: expected a non-empty activity snapshot for the completed sync delegation")
		}
		if !strings.Contains(statusResult.ForLLM, "completed") {
			t.Errorf("expected the status report to show status=completed, got: %s", statusResult.ForLLM)
		}
	})

	t.Run("async", func(t *testing.T) {
		tool, _, _, _ := newADR053TestTool(t)
		ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "u14-status-async-parent"), "u14-agent")
		sessionID := runAndExtractSessionID(t, tool, ctx, "do the async thing")
		tool.WaitForAsyncTasks()

		statusResult := tool.Execute(ctx, map[string]any{"action": "status", "session_id": sessionID})
		if statusResult.IsError {
			t.Fatalf("BDD-50: expected action=status to find the completed async delegation, got error: %s",
				statusResult.ForLLM)
		}
		if strings.TrimSpace(statusResult.ForLLM) == "" {
			t.Error("BDD-50: expected a non-empty activity snapshot for the completed async delegation")
		}
		if !strings.Contains(statusResult.ForLLM, "completed") {
			t.Errorf("expected the status report to show status=completed, got: %s", statusResult.ForLLM)
		}
	})
}

// u14FakeSessionStore implements DelegateSessionStore for
// TestRecentActivityLines_LogsEmptyPath.
type u14FakeSessionStore struct {
	entries []session.TranscriptEntry
}

func (f *u14FakeSessionStore) ReadTranscript(sessionID string) ([]session.TranscriptEntry, error) {
	return f.entries, nil
}

// TestRecentActivityLines_LogsEmptyPath is test #30 (BDD-51, FR-043): a
// clean transcript read that simply has no entry matching this task's own
// ParentSpawnCallID yet must log the empty result, not degrade silently
// into the exact same "nothing available" shape as an unwired store or a
// read error.
func TestRecentActivityLines_LogsEmptyPath(t *testing.T) {
	t.Run("genuinely empty: logs", func(t *testing.T) {
		tool := NewDelegateTool("test-model", 0, 0)
		tool.SetSessionStore(&u14FakeSessionStore{entries: []session.TranscriptEntry{
			{ParentSpawnCallID: "other-call-id", Content: "irrelevant activity from a different task"},
		}})
		getLogs := captureLogs(t)

		lines := tool.recentActivityLines("u14-child-session", "this-spawn-call", 5)
		if lines != nil {
			t.Fatalf("expected nil lines for a genuinely empty activity path, got %v", lines)
		}
		if logs := getLogs(); !strings.Contains(logs, "no recent activity") {
			t.Errorf("BDD-51: expected a log line recording the empty result, got logs: %q", logs)
		}
	})

	t.Run("positive lower bound: a real match neither logs the empty message nor returns empty", func(t *testing.T) {
		tool := NewDelegateTool("test-model", 0, 0)
		tool.SetSessionStore(&u14FakeSessionStore{entries: []session.TranscriptEntry{
			{ParentSpawnCallID: "this-spawn-call", Content: "real activity"},
		}})
		getLogs := captureLogs(t)

		lines := tool.recentActivityLines("u14-child-session", "this-spawn-call", 5)
		if len(lines) == 0 {
			t.Fatal("expected non-empty lines when a matching transcript entry exists — proves the empty-path " +
				"assertion above is a real signal, not a function that always returns nil")
		}
		if logs := getLogs(); strings.Contains(logs, "no recent activity") {
			t.Errorf("must not log the empty-path message when activity WAS found, got logs: %q", logs)
		}
	})
}

// TestDelegateTaskMaps_BoundedAfterNCompletions is test #31 (BDD-52,
// FR-045/FR-087, renamed per grill M-10 from a weaker "has a deletion path"
// framing): N ≫ C terminal delegations whose last status read is older
// than the configured TTL must be evicted down to at most C entries in
// BOTH t.tasks and t.sessionIndex.
func TestDelegateTaskMaps_BoundedAfterNCompletions(t *testing.T) {
	tool, spawner := u14PermissiveTool(t)
	lc := session.NewLifecycleStore(t.TempDir())
	tool.SetLifecycleStore(lc)

	fakeNow := time.Now()
	tool.SetClock(func() time.Time { return fakeNow })
	const cap0 = 5
	tool.SetTaskRetentionPolicy(cap0, time.Hour)

	const n = 20 // N >> C
	for i := 0; i < n; i++ {
		ctx := WithAgentID(WithTranscriptSessionID(context.Background(), fmt.Sprintf("u14-evict-parent-%d", i)), "u14-agent")
		if r := tool.Execute(ctx, map[string]any{"task": "old", "async": false}); r.IsError {
			t.Fatalf("run %d failed: %s", i, r.ForLLM)
		}
	}
	if spawner.calls != n {
		t.Fatalf("precondition: expected %d dispatches, spawner saw %d", n, spawner.calls)
	}

	// Positive lower bound: BEFORE the clock advances past the TTL, all N
	// tasks are still fresh (LastStatusRead == fakeNow), so none are stale
	// yet and the map must actually have grown past C — otherwise the
	// bounded assertion below would pass vacuously (registration alone
	// never exceeding C, eviction never actually exercised).
	tool.mu.Lock()
	beforeCount := len(tool.tasks)
	tool.mu.Unlock()
	if beforeCount <= cap0 {
		t.Fatalf("precondition: expected t.tasks to have grown past the cap (%d) before eviction, got %d",
			cap0, beforeCount)
	}

	// Advance the clock past the TTL so every one of the N terminal tasks
	// is now stale, then dispatch ONE more delegation — FR-045 requires
	// eviction to run "without an external caller", i.e. driven by the
	// tool's own bookkeeping (here: the next registration), not a ticker.
	fakeNow = fakeNow.Add(2 * time.Hour)
	triggerCtx := WithAgentID(WithTranscriptSessionID(context.Background(), "u14-evict-trigger"), "u14-agent")
	if r := tool.Execute(triggerCtx, map[string]any{"task": "trigger", "async": false}); r.IsError {
		t.Fatalf("trigger run failed: %s", r.ForLLM)
	}

	tool.mu.Lock()
	gotTasks := len(tool.tasks)
	gotIndex := len(tool.sessionIndex)
	tool.mu.Unlock()
	if gotTasks > cap0 {
		t.Errorf("BDD-52: expected len(t.tasks) <= %d after eviction, got %d", cap0, gotTasks)
	}
	if gotIndex > cap0 {
		t.Errorf("BDD-52: expected len(t.sessionIndex) <= %d after eviction, got %d", cap0, gotIndex)
	}
}

// TestDelegateTaskMaps_RetainWithinTTL is test #93 (BDD-52's "But" clause):
// the complement of #31 — a terminal task still within its TTL MUST be
// retained through an eviction pass driven by unrelated bookkeeping, so
// eviction can never break a caller's next action:"status" poll for it.
func TestDelegateTaskMaps_RetainWithinTTL(t *testing.T) {
	tool, _ := u14PermissiveTool(t)
	lc := session.NewLifecycleStore(t.TempDir())
	tool.SetLifecycleStore(lc)

	t0 := time.Now()
	cur := t0
	tool.SetClock(func() time.Time { return cur })
	tool.SetTaskRetentionPolicy(3, time.Hour)

	const nOld = 10 // N >> C, all becoming stale together
	for i := 0; i < nOld; i++ {
		ctx := WithAgentID(WithTranscriptSessionID(context.Background(), fmt.Sprintf("u14-retain-old-%d", i)), "u14-agent")
		if r := tool.Execute(ctx, map[string]any{"task": "old", "async": false}); r.IsError {
			t.Fatalf("old run %d failed: %s", i, r.ForLLM)
		}
	}

	// Advance the clock, then register ONE fresh task — its LastStatusRead
	// will be well inside the TTL window relative to the eviction pass below.
	cur = t0.Add(2 * time.Hour)
	freshCtx := WithAgentID(WithTranscriptSessionID(context.Background(), "u14-retain-fresh-parent"), "u14-agent")
	freshResult := tool.Execute(freshCtx, map[string]any{"task": "fresh", "async": false})
	if freshResult.IsError {
		t.Fatalf("fresh run failed: %s", freshResult.ForLLM)
	}
	tool.mu.Lock()
	var freshSessionID string
	for sid, taskID := range tool.sessionIndex {
		if st, ok := tool.tasks[taskID]; ok && st.Task == "fresh" {
			freshSessionID = sid
		}
	}
	tool.mu.Unlock()
	if freshSessionID == "" {
		t.Fatal("could not find the freshly-registered task's session id")
	}

	// One more dispatch at the SAME "cur" time drives the eviction pass
	// over the now-stale old batch (FR-045's bookkeeping-driven trigger).
	triggerCtx := WithAgentID(WithTranscriptSessionID(context.Background(), "u14-retain-trigger"), "u14-agent")
	if r := tool.Execute(triggerCtx, map[string]any{"task": "trigger", "async": false}); r.IsError {
		t.Fatalf("trigger run failed: %s", r.ForLLM)
	}

	// BDD-52's "But" clause: the fresh, within-TTL task must still resolve.
	statusResult := tool.Execute(freshCtx, map[string]any{"action": "status", "session_id": freshSessionID})
	if statusResult.IsError {
		t.Fatalf("expected the fresh (within-TTL) task to still be resolvable by action:status after "+
			"eviction, got error: %s", statusResult.ForLLM)
	}

	// Positive lower bound: prove eviction actually ran and reclaimed the
	// stale batch — otherwise the retention assertion above could pass even
	// if evictStaleTasksLocked were a no-op.
	tool.mu.Lock()
	remaining := len(tool.tasks)
	tool.mu.Unlock()
	if remaining >= nOld {
		t.Errorf("expected the stale old batch (%d entries) to have been evicted, remaining=%d — "+
			"eviction does not appear to have run", nOld, remaining)
	}
}
