// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// delegate_signoff14_test.go covers four findings from the 14-reviewer
// sign-off on release/v0.1.1 (pkg/tools/delegate.go scope):
//
//   - HIGH-1: executeRespond never resumed a parked child — the answer had
//     no consumer (steering queue is only drained inside a LIVE turn, and
//     nothing re-dispatched the child) — and the un-park lifecycle Mutate
//     committed BEFORE delivery was attempted, so a delivery failure left a
//     wedged, non-retryable state.
//   - MEDIUM-2: executeInbox/executePeek keyed inbox reads by the CALLER's
//     ownerKey instead of the target's rec.ParentDurableKey, silently
//     returning empty results for an authorized ANCESTOR caller (FR-039).
//   - MEDIUM-3: executeCancel's TOCTOU "nothing to cancel" branch discarded
//     the killFailed/walkIncomplete background-shell-kill warnings it had
//     already computed.
//   - LOW-4: steerRateWindows never evicted a dead session's map key,
//     growing unboundedly for the life of the process.
//
// Per Rule 5/6 (see delegate_adr057_test.go's own note): this is a NEW
// file, and its unexported helpers/types are signoff14-prefixed so a
// same-wave package-level name never collides with another file in
// pkg/tools.
package tools

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// signoff14CapturingSpawner implements SubTurnSpawner, recording BOTH how
// many times SpawnSubTurn was invoked and the last SubTurnConfig it saw —
// unlike delegate_fixwave_test.go's capturingDelegateSpawner (config only)
// or delegate_adr057_test.go's u14CountingSpawner (count only), this file's
// tests need both facts about the SAME call.
type signoff14CapturingSpawner struct {
	mu    sync.Mutex
	calls int
	cfg   SubTurnConfig
}

func (s *signoff14CapturingSpawner) SpawnSubTurn(_ context.Context, cfg SubTurnConfig) (*ToolResult, error) {
	s.mu.Lock()
	s.calls++
	s.cfg = cfg
	s.mu.Unlock()
	return &ToolResult{ForLLM: "done", ForUser: "done"}, nil
}

func (s *signoff14CapturingSpawner) lastConfig() SubTurnConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *signoff14CapturingSpawner) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// ---------------------------------------------------------------------
// HIGH-1: respond must genuinely RESUME the parked child, not just flip
// the lifecycle record and enqueue a message nothing will ever drain.
// ---------------------------------------------------------------------

// TestDelegateTool_Respond_NativeRedispatchesWithIsResume is the "genuinely
// RUNS" proof the finding explicitly asks for (not just "1 message
// pending" in the steering queue). It proves the redispatch reuses the
// SAME isResume spawn machinery `delegate follow_up` uses: the spawner is
// actually invoked (not merely a steering message queued with no
// consumer), IsResume=true, DelegateSessionID matches the parked session
// verbatim (a warm resume, not a new session), and the answer text/
// correlation_id genuinely reaches the resumed turn's own task/system
// prompt.
func TestDelegateTool_Respond_NativeRedispatchesWithIsResume(t *testing.T) {
	spawner := &signoff14CapturingSpawner{}
	tool, lc, inbox, steer := newADR053TestTool(t)
	tool.SetSpawner(spawner)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-resume-proof", State: session.LifecycleNeedsInput,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker",
		NeedsInput: &session.NeedsInput{CorrelationID: "corr-resume", TTLDeadline: time.Now().Add(time.Hour)},
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}
	if _, err := inbox.Append("parent-1", questionMsgForDelegateTest(t, "child-resume-proof", "q-resume",
		"corr-resume", generated.SelfOk)); err != nil {
		t.Fatalf("seed question message failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": "child-resume-proof", "correlation_id": "corr-resume",
		"text": "yes, proceed with option B",
	})
	if result.IsError {
		t.Fatalf("respond failed: %s", result.ForLLM)
	}
	tool.WaitForAsyncTasks()

	// The steering enqueue is kept (harmless best-effort, see delegate.go's
	// own comment) but is NOT the mechanism proving genuine resumption —
	// the spawner capture below is.
	if msg, _ := steer.last(); msg.Content != "yes, proceed with option B" {
		t.Errorf("steering enqueue content = %q, want %q", msg.Content, "yes, proceed with option B")
	}

	cfg := spawner.lastConfig()
	if !cfg.IsResume {
		t.Fatal("HIGH-1: respond must redispatch the child with IsResume=true — the parked child's turn " +
			"already ended (TurnEndStatusParked), so a resume (not a fresh create) is required; the spawner " +
			"was never invoked at all before this fix, and the finding requires the SAME isResume machinery " +
			"delegate follow_up uses")
	}
	if cfg.DelegateSessionID != "child-resume-proof" {
		t.Errorf("DelegateSessionID = %q, want the parked session id verbatim (warm resume, not a new session)",
			cfg.DelegateSessionID)
	}
	if !strings.Contains(cfg.SystemPrompt, "yes, proceed with option B") {
		t.Errorf("expected the resumed turn's task/system prompt to carry the answer text, got: %q", cfg.SystemPrompt)
	}
	if !strings.Contains(cfg.SystemPrompt, "corr-resume") {
		t.Errorf("expected the resumed turn's task/system prompt to reference the correlation_id, got: %q", cfg.SystemPrompt)
	}
}

// TestDelegateTool_Respond_EnqueueFailure_LeavesSessionParkedNotWedged is
// the ordering-defect RED/GREEN proof: before the fix, the un-park Mutate
// committed BEFORE EnqueueSteeringMessage, so a delivery failure left the
// record flipped to `running` with NeedsInput already erased — wedged,
// with no way to retry respond() (the correlation_id it re-checks was
// already gone). After the fix, delivery is attempted FIRST; a failure
// there must leave the record untouched (still parked on the exact same
// correlation_id) so the caller can retry.
type signoff14FailingSteeringSink struct{}

func (signoff14FailingSteeringSink) EnqueueSteeringMessage(_, _ string, _ providers.Message) error {
	return errors.New("signoff14: simulated steering sink failure")
}

func TestDelegateTool_Respond_EnqueueFailure_LeavesSessionParkedNotWedged(t *testing.T) {
	spawner := &signoff14CapturingSpawner{}
	tool, lc, inbox, _ := newADR053TestTool(t)
	tool.SetSpawner(spawner)
	tool.SetSteeringSink(signoff14FailingSteeringSink{})
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-enqueue-fail", State: session.LifecycleNeedsInput,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker",
		NeedsInput: &session.NeedsInput{CorrelationID: "corr-enqueue-fail", TTLDeadline: time.Now().Add(time.Hour)},
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}
	if _, err := inbox.Append("parent-1", questionMsgForDelegateTest(t, "child-enqueue-fail", "q-ef",
		"corr-enqueue-fail", generated.SelfOk)); err != nil {
		t.Fatalf("seed question message failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": "child-enqueue-fail", "correlation_id": "corr-enqueue-fail",
		"text": "an answer that will never be delivered",
	})
	if !result.IsError {
		t.Fatal("expected respond to fail when the steering sink's enqueue fails")
	}
	tool.WaitForAsyncTasks()

	if got := spawner.callCount(); got != 0 {
		t.Errorf("HIGH-1 ordering: a failed delivery must NOT redispatch the child at all — spawner was "+
			"invoked %d time(s)", got)
	}

	rec, err := lc.Load("child-enqueue-fail")
	if err != nil {
		t.Fatalf("Load after failed respond: %v", err)
	}
	if rec.State != session.LifecycleNeedsInput {
		t.Fatalf("HIGH-1 ordering fix: an enqueue failure must leave the session PARKED (state=needs_input), "+
			"got state=%q — a delivery failure must never wedge the record into a non-parked state with no "+
			"live turn behind it", rec.State)
	}
	if rec.NeedsInput == nil || rec.NeedsInput.CorrelationID != "corr-enqueue-fail" {
		t.Fatal("HIGH-1 ordering fix: NeedsInput/correlation_id must survive a failed delivery unchanged, " +
			"so the caller can retry respond() with the identical correlation_id")
	}
}

// ---------------------------------------------------------------------
// MEDIUM-2: executeInbox/executePeek must key reads by the target's own
// rec.ParentDurableKey, not the calling ancestor's ownerKey.
// ---------------------------------------------------------------------

// TestDelegateTool_Inbox_AuthorizedAncestor_SeesMessagesUnderDirectParentKey
// proves the fix: chatA (a grandparent, authorized via the FR-039 ownership
// walk) calls inbox on grandchild D, whose message was Appended under D's
// DIRECT parent (childB) — a DIFFERENT key than chatA's own. Keying the
// Drain by the caller's ownerKey (chatA) would silently return empty;
// keying it by rec.ParentDurableKey (childB) finds it.
func TestDelegateTool_Inbox_AuthorizedAncestor_SeesMessagesUnderDirectParentKey(t *testing.T) {
	tool, lc, inbox, _ := newADR053TestTool(t)

	const chatA = "signoff14-chatA"
	const childB = "signoff14-childB"
	const grandchildD = "signoff14-grandchildD"
	u14SeedChild(t, lc, childB, chatA)
	u14SeedChild(t, lc, grandchildD, childB)

	// The message is Appended under childB (D's DIRECT parent) — exactly
	// how message_parent.go's real Append call addresses it.
	if _, err := inbox.Append(childB, questionMsgForDelegateTest(t, grandchildD, "signoff14-msg-1",
		"signoff14-corr", generated.SelfOk)); err != nil {
		t.Fatalf("seed inbox message failed: %v", err)
	}

	callerA := WithTranscriptSessionID(context.Background(), chatA)
	result := tool.Execute(callerA, map[string]any{"action": "inbox", "session_id": grandchildD})
	if result.IsError {
		t.Fatalf("expected authorized ancestor chatA to read grandchild D's inbox, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "signoff14-msg-1") {
		t.Errorf("MEDIUM-2: expected the message Appended under D's direct parent (childB) to be visible to "+
			"an authorized ancestor (chatA) keyed correctly by rec.ParentDurableKey, got empty/wrong result: %s",
			result.ForLLM)
	}
}

// TestDelegateTool_Peek_AuthorizedAncestor_SeesSnapshotUnderDirectParentKey
// is Peek's counterpart to the Inbox proof above.
func TestDelegateTool_Peek_AuthorizedAncestor_SeesSnapshotUnderDirectParentKey(t *testing.T) {
	tool, lc, inbox, _ := newADR053TestTool(t)

	const chatA = "signoff14-peek-chatA"
	const childB = "signoff14-peek-childB"
	const grandchildD = "signoff14-peek-grandchildD"
	u14SeedChild(t, lc, childB, chatA)
	u14SeedChild(t, lc, grandchildD, childB)

	if _, err := inbox.Append(childB, progressMsgForDelegateTest(t, grandchildD, "signoff14-peek-msg-1")); err != nil {
		t.Fatalf("seed inbox progress message failed: %v", err)
	}

	callerA := WithTranscriptSessionID(context.Background(), chatA)
	result := tool.Execute(callerA, map[string]any{"action": "peek", "session_id": grandchildD})
	if result.IsError {
		t.Fatalf("expected authorized ancestor chatA to peek grandchild D, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "still working") {
		t.Errorf("MEDIUM-2: expected the progress message Appended under D's direct parent (childB) to be "+
			"visible to an authorized ancestor (chatA) keyed correctly by rec.ParentDurableKey, got: %s",
			result.ForLLM)
	}
}

// ---------------------------------------------------------------------
// MEDIUM-3: executeCancel's TOCTOU "nothing to cancel" branch must still
// surface a real background-shell kill failure.
// ---------------------------------------------------------------------

// TestDelegateTool_Cancel_NothingToCancel_StillSurfacesShellKillWarnings
// forces BOTH conditions simultaneously: the turn-level cancel hook reports
// zero descendants (the TOCTOU "terminated between the terminal check and
// the cancel hook" miss) AND a real background shell for the same session
// fails to be killed (via the killProcessGroupFn seam, mirroring
// delegate_adr057_fix_test.go's own precedent). Before the fix, the "nothing
// to cancel" branch discarded the already-computed killFailed/walkIncomplete
// warnings outright.
func TestDelegateTool_Cancel_NothingToCancel_StillSurfacesShellKillWarnings(t *testing.T) {
	cases := []struct {
		name string
		hard bool
	}{
		{"hard", true},
		{"soft", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			origKillFn := killProcessGroupFn
			t.Cleanup(func() { killProcessGroupFn = origKillFn })
			killProcessGroupFn = func(pid int) error { return errors.New("signoff14: forced kill failure") }

			sm := NewSessionManager()
			childID := "signoff14-cancel-toctou-" + tc.name
			sm.Add(&ProcessSession{
				ID: "signoff14-shell-" + tc.name, OwnerSessionID: childID, Status: StatusRunning,
				PID: fakeUnusedPIDBase + 950, StartTime: time.Now().Unix(),
			})

			tool := NewDelegateTool("test-model", 0, 0)
			tool.SetSessionMessagingEnabled(func() bool { return true })
			tool.SetSessionManager(sm)
			lc := session.NewLifecycleStore(t.TempDir())
			tool.SetLifecycleStore(lc)
			parentKey := "signoff14-cancel-parent-" + tc.name
			if err := lc.Persist(&session.LifecycleRecord{
				SessionID: childID, State: session.LifecycleRunning,
				OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: parentKey,
				WorkspaceID: "ws-1", AgentID: "worker",
			}); err != nil {
				t.Fatalf("seed lifecycle record failed: %v", err)
			}
			// TOCTOU miss: both hooks report zero descendants (nil, nil).
			tool.SetCancelHooks(
				func(sessionID, hint string) ([]string, error) { return nil, nil },
				func(sessionID, hint string) ([]string, error) { return nil, nil },
			)

			callerCtx := WithTranscriptSessionID(context.Background(), parentKey)
			result := tool.Execute(callerCtx, map[string]any{
				"action": "cancel", "session_id": childID, "hard": tc.hard,
			})
			if !result.IsError {
				t.Fatalf("expected the TOCTOU miss to still return the 'nothing to cancel' error shape, "+
					"got success: %s", result.ForLLM)
			}
			if !strings.Contains(result.ForLLM, "nothing to cancel") {
				t.Errorf("expected the TOCTOU 'nothing to cancel' message, got: %s", result.ForLLM)
			}
			if !strings.Contains(result.ForLLM, "could not be killed") {
				t.Errorf("MEDIUM-3: expected the 'nothing to cancel' branch to STILL carry the "+
					"already-computed background-shell kill-failure warning instead of discarding it, got: %s",
					result.ForLLM)
			}
		})
	}
}

// ---------------------------------------------------------------------
// LOW-4: steerRateWindows must opportunistically evict a dead session's
// map key (mirror cancel_prearm.go::markPendingSpawn).
// ---------------------------------------------------------------------

// TestSteerRateWindows_EvictsExpiredOtherSessionKeys proves the map is
// bounded: a session that steered once and will never steer again must not
// leave a permanent entry behind once its own rate window has fully
// expired. The per-session logic alone can't ever observe this (a
// session's OWN call always ends by storing a non-empty window back), so
// the sweep must run for every OTHER key too.
func TestSteerRateWindows_EvictsExpiredOtherSessionKeys(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	fakeNow := time.Now()
	tool.SetClock(func() time.Time { return fakeNow })

	if err := tool.checkSteerCaps("signoff14-stale-session", "hi"); err != nil {
		t.Fatalf("seed steer call failed: %v", err)
	}
	tool.steerRateMu.Lock()
	_, seeded := tool.steerRateWindows["signoff14-stale-session"]
	tool.steerRateMu.Unlock()
	if !seeded {
		t.Fatal("precondition: expected the stale session's own steer call to populate its rate window entry")
	}

	// Advance well past the 1-minute rate window, then exercise checkSteerCaps
	// for a COMPLETELY DIFFERENT session — this must not touch
	// "signoff14-stale-session" via its own per-session logic (a different key
	// entirely), so the ONLY way it can be evicted is the opportunistic sweep.
	fakeNow = fakeNow.Add(2 * time.Minute)
	if err := tool.checkSteerCaps("signoff14-other-session", "hi"); err != nil {
		t.Fatalf("unrelated steer call failed: %v", err)
	}

	tool.steerRateMu.Lock()
	_, stillPresent := tool.steerRateWindows["signoff14-stale-session"]
	tool.steerRateMu.Unlock()
	if stillPresent {
		t.Error("LOW-4: expected the stale session's fully-expired rate-window entry to be evicted by the " +
			"opportunistic sweep (mirroring cancel_prearm.go::markPendingSpawn) — the map otherwise grows " +
			"one entry per distinct session_id ever steered/responded-to, for the life of the process")
	}
}
