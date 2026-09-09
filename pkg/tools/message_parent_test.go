// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// NOTE: the real regression guard that proves message_parent keeps the child's
// durable delegate session id SEPARATE from the shared parent/child transcript
// session id lives in pkg/agent/message_parent_real_context_test.go (it drives
// the real spawnSubTurn context construction). This file's withChildContext
// helper intentionally stamps the SAME value into both keys, so the ~12 tests
// here contribute zero independent coverage of that separation — they exist to
// exercise the inbox/park/wake/egress mechanics, not the session-id distinction.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// fakeWaker implements MessageParentWaker for tests, recording every call.
type fakeWaker struct {
	calls []struct {
		kind  string
		event MessageParentWakeEvent
	}
	err error
}

func (f *fakeWaker) WakeParent(ctx context.Context, kind string, event MessageParentWakeEvent) error {
	f.calls = append(f.calls, struct {
		kind  string
		event MessageParentWakeEvent
	}{kind, event})
	return f.err
}

func newMessageParentTestSetup(t *testing.T) (*MessageParentTool, *session.LifecycleStore, *session.MessageInboxStore, *fakeWaker) {
	t.Helper()
	lc := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	waker := &fakeWaker{}

	tool := NewMessageParentTool(inbox, lc)
	tool.SetWaker(waker)
	// Enable the session-messaging plane by default for tests so the
	// fail-closed kill switch (fix B.5) does not reject every test call.
	// Tests that exercise the kill switch itself override this.
	tool.SetSessionMessagingEnabled(func() bool { return true })

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID:        "child-1",
		State:            session.LifecycleRunning,
		OwnerScopeKind:   session.OwnerScopeParentSession,
		OwnerScopeID:     "parent-delegate-id",
		ParentDurableKey: "parent-1",
		WorkspaceID:      "ws-1",
		AgentID:          "worker",
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}

	return tool, lc, inbox, waker
}

// withChildContext is a shortcut that stamps the SAME id as both the shared
// parent/child transcript session id AND the child's own ADR-053 durable
// delegate session id. Real production child contexts carry two DIFFERENT
// values for these (see pkg/agent/subturn.go's spawnSubTurn: the transcript
// id is inherited from the parent for cascade-cancel matching, while the
// delegate session id is a fresh per-child UUID) — this helper deliberately
// keeps both keys populated so tests using it exercise
// message_parent.go's real ToolDelegateSessionID(ctx) lookup against
// whatever LifecycleRecord they seeded under this SAME id, without asserting
// anything about the (here-identical) transcript id. Tests that need to
// prove the two ids are correctly kept SEPARATE must not use this shortcut
// — see TestMessageParent_RealSpawnSubTurnContext_ChildCanMessageParent in
// pkg/agent, which drives the real spawnSubTurn context construction.
func withChildContext(sessionID string) context.Context {
	ctx := WithTranscriptSessionID(context.Background(), sessionID)
	return WithDelegateSessionID(ctx, sessionID)
}

func TestMessageParentTool_Progress_AppendsToInbox(t *testing.T) {
	tool, _, inbox, waker := newMessageParentTestSetup(t)
	ctx := withChildContext("child-1")

	result := tool.Execute(ctx, map[string]any{"kind": "progress", "text": "halfway there", "pct": 50})
	if result.IsError {
		t.Fatalf("progress failed: %s", result.ForLLM)
	}

	msgs, _, _, err := inbox.Drain("parent-1", "child-1", "", 10)
	if err != nil {
		t.Fatalf("Drain failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message in inbox, got %d", len(msgs))
	}
	kind, _ := msgs[0].Discriminator()
	if kind != "progress" {
		t.Errorf("kind = %q, want %q", kind, "progress")
	}

	// progress is NOT in the wakeable set — no wake attempted.
	if len(waker.calls) != 0 {
		t.Errorf("expected no wake for progress, got %d calls", len(waker.calls))
	}
}

// TestMessageParentTool_Question_Wait_ParksNeedsInput proves the core
// mechanism: message_parent(question, wait=true) parks the CALLING child's
// own durable session in needs_input (INV-4/G-6).
func TestMessageParentTool_Question_Wait_ParksNeedsInput(t *testing.T) {
	tool, lc, inbox, waker := newMessageParentTestSetup(t)
	ctx := withChildContext("child-1")

	result := tool.Execute(ctx, map[string]any{
		"kind": "question", "text": "should I proceed?", "wait": true,
	})
	if result.IsError {
		t.Fatalf("question(wait=true) failed: %s", result.ForLLM)
	}

	rec, err := lc.Load("child-1")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if rec.State != session.LifecycleNeedsInput {
		t.Fatalf("state = %q, want %q", rec.State, session.LifecycleNeedsInput)
	}
	if rec.NeedsInput == nil {
		t.Fatal("expected NeedsInput to be set")
	}
	if rec.NeedsInput.CorrelationID == "" {
		t.Error("expected a non-empty correlation_id")
	}
	if rec.NeedsInput.TTLDeadline.Before(time.Now().Add(23 * time.Hour)) {
		t.Errorf("expected the default 24h TTL, deadline too soon: %v", rec.NeedsInput.TTLDeadline)
	}

	// The question message itself must ALSO be in the parent's inbox.
	msgs, _, _, err := inbox.Drain("parent-1", "child-1", "", 10)
	if err != nil {
		t.Fatalf("Drain failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 inbox message, got %d", len(msgs))
	}

	// question IS wakeable.
	if len(waker.calls) != 1 || waker.calls[0].kind != "question" {
		t.Errorf("expected exactly 1 wake call for kind=question, got: %+v", waker.calls)
	}
}

// TestMessageParentTool_Question_OmittedAuthority_DefaultsOwnerRequired
// proves FR-131 (M3): an omitted authority tag defaults to owner_required,
// fail-closed.
func TestMessageParentTool_Question_OmittedAuthority_DefaultsOwnerRequired(t *testing.T) {
	tool, _, inbox, _ := newMessageParentTestSetup(t)
	ctx := withChildContext("child-1")

	result := tool.Execute(ctx, map[string]any{"kind": "question", "text": "x?", "wait": false})
	if result.IsError {
		t.Fatalf("question failed: %s", result.ForLLM)
	}

	msgs, _, _, err := inbox.Drain("parent-1", "child-1", "", 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("expected 1 inbox message, got %d (err=%v)", len(msgs), err)
	}
	q, err := msgs[0].AsSessionMessageQuestion()
	if err != nil {
		t.Fatalf("AsSessionMessageQuestion failed: %v", err)
	}
	if q.Authority == nil || string(*q.Authority) != "owner_required" {
		t.Errorf("expected authority to default to owner_required, got: %v", q.Authority)
	}
}

// TestMessageParentTool_PerChildCeiling_FailsBackAsToolError proves D15
// from the TOOL's perspective (never-silent-drop, FR-125): once the
// per-child ceiling is hit, the tool call itself returns IsError=true with
// a clear message — it does not silently swallow the child's message.
func TestMessageParentTool_PerChildCeiling_FailsBackAsToolError(t *testing.T) {
	tool, _, inbox, _ := newMessageParentTestSetup(t)
	inbox.ChildSendRatePerMinute = 1000 // isolate the per-type CEILING from the unrelated rate cap
	ctx := withChildContext("child-1")

	for i := 0; i < 20; i++ {
		result := tool.Execute(ctx, map[string]any{
			"kind": "blocker", "text": "blocked", "severity": "low", "message_id": "b-" + string(rune('a'+i)),
		})
		if result.IsError {
			t.Fatalf("blocker #%d (within ceiling) failed: %s", i, result.ForLLM)
		}
	}

	overCeiling := tool.Execute(ctx, map[string]any{
		"kind": "blocker", "text": "one too many", "severity": "low", "message_id": "overflow",
	})
	if !overCeiling.IsError {
		t.Fatal("expected the 21st blocker to be rejected as a tool error, got success")
	}
	if !strings.Contains(overCeiling.ForLLM, "await answers") && !strings.Contains(overCeiling.ForLLM, "ceiling") {
		t.Errorf("expected a clear ceiling-exceeded message, got: %s", overCeiling.ForLLM)
	}
}

func TestMessageParentTool_3PChild_Rejected(t *testing.T) {
	lc := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	tool := NewMessageParentTool(inbox, lc)
	tool.SetSessionMessagingEnabled(func() bool { return true }) // fix B.5: default fail-closed

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-3p", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: "parent-x",
		ParentDurableKey: "parent-1", WorkspaceID: "ws-1", AgentID: "worker-3p",
		Is3P: true,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	result := tool.Execute(withChildContext("child-3p"), map[string]any{"kind": "progress", "text": "x"})
	if !result.IsError {
		t.Fatal("expected message_parent to be rejected for a 3P child (D5)")
	}
}

func TestMessageParentTool_NoSessionContext_Rejected(t *testing.T) {
	lc := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	tool := NewMessageParentTool(inbox, lc)
	tool.SetSessionMessagingEnabled(func() bool { return true }) // fix B.5: default fail-closed

	result := tool.Execute(context.Background(), map[string]any{"kind": "progress", "text": "x"})
	if !result.IsError {
		t.Fatal("expected an error with no session context")
	}
}

func TestMessageParentTool_ContentEgressFilter_Applied(t *testing.T) {
	tool, _, inbox, _ := newMessageParentTestSetup(t)
	tool.SetContentEgressFilter(func(s string) string {
		return strings.ReplaceAll(s, "sk-secret-123", "[REDACTED]")
	})
	ctx := withChildContext("child-1")

	result := tool.Execute(ctx, map[string]any{"kind": "progress", "text": "found key sk-secret-123 in config"})
	if result.IsError {
		t.Fatalf("progress failed: %s", result.ForLLM)
	}

	msgs, _, _, err := inbox.Drain("parent-1", "child-1", "", 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d (err=%v)", len(msgs), err)
	}
	p, err := msgs[0].AsSessionMessageProgress()
	if err != nil {
		t.Fatalf("AsSessionMessageProgress failed: %v", err)
	}
	if strings.Contains(p.Text, "sk-secret-123") {
		t.Errorf("expected the secret to be redacted by the content-egress filter, got: %q", p.Text)
	}
	if !strings.Contains(p.Text, "[REDACTED]") {
		t.Errorf("expected the redaction marker in the filtered text, got: %q", p.Text)
	}
}

func TestMessageParentTool_Handback_Wakes(t *testing.T) {
	tool, _, _, waker := newMessageParentTestSetup(t)
	ctx := withChildContext("child-1")

	result := tool.Execute(ctx, map[string]any{
		"kind": "handback", "mode": "final", "result_so_far": "done",
	})
	if result.IsError {
		t.Fatalf("handback failed: %s", result.ForLLM)
	}
	var resp struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal([]byte(result.ForLLM), &resp); err != nil {
		t.Fatalf("failed to decode response: %v (body=%s)", err, result.ForLLM)
	}
	if !resp.Accepted {
		t.Error("expected accepted=true")
	}
	if len(waker.calls) != 1 || waker.calls[0].kind != "handback" {
		t.Errorf("expected exactly 1 wake call for kind=handback, got: %+v", waker.calls)
	}
}

func TestMessageParentTool_InvalidKind_Rejected(t *testing.T) {
	tool, _, _, _ := newMessageParentTestSetup(t) //nolint:dogsled // Only the tool is relevant from the four test fixtures.
	result := tool.Execute(withChildContext("child-1"), map[string]any{"kind": "bogus"})
	if !result.IsError {
		t.Fatal("expected an error for an invalid kind")
	}
}

func TestMessageParentTool_WakeFailureDoesNotFailToolCall(t *testing.T) {
	tool, _, _, waker := newMessageParentTestSetup(t)
	waker.err = errors.New("simulated wake failure")

	result := tool.Execute(withChildContext("child-1"), map[string]any{
		"kind": "blocker", "text": "x", "severity": "high",
	})
	if result.IsError {
		t.Fatalf("expected the tool call to succeed even when WakeParent fails, got error: %s", result.ForLLM)
	}
}

// TestMessageParentTool_EgressFilter_AppliedToPathFields proves Security-MINOR-1:
// the content-egress filter is applied to path-like fields (artifact.paths,
// handback.artifacts, checkpoint.commit_ref) too — a child must not be able to
// exfiltrate via a filename carrying a secret.
func TestMessageParentTool_EgressFilter_AppliedToPathFields(t *testing.T) {
	tool, _, inbox, _ := newMessageParentTestSetup(t)
	tool.SetContentEgressFilter(func(s string) string {
		return strings.ReplaceAll(s, "sk-secret-999", "[REDACTED]")
	})
	ctx := withChildContext("child-1")

	t.Run("artifact_paths", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{
			"kind": "artifact", "paths": []any{"out/sk-secret-999-file.txt", "clean.txt"},
		})
		if result.IsError {
			t.Fatalf("artifact failed: %s", result.ForLLM)
		}
		msgs, _, _, err := inbox.Drain("parent-1", "child-1", "", 10)
		if err != nil || len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d (err=%v)", len(msgs), err)
		}
		a, err := msgs[0].AsSessionMessageArtifact()
		if err != nil {
			t.Fatalf("AsSessionMessageArtifact: %v", err)
		}
		for _, p := range a.Paths {
			if strings.Contains(p, "sk-secret-999") {
				t.Errorf("artifact path leaked the secret: %q", p)
			}
		}
	})

	// Each sub-test discriminates its message by `kind`, so leftover
	// messages from a prior sub-test do not interfere.

	t.Run("handback_artifacts", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{
			"kind": "handback", "mode": "final", "result_so_far": "done",
			"artifacts": []any{"build/sk-secret-999.bin"},
		})
		if result.IsError {
			t.Fatalf("handback failed: %s", result.ForLLM)
		}
		msgs, _, _, err := inbox.Drain("parent-1", "child-1", "", 10)
		if err != nil {
			t.Fatalf("Drain: %v", err)
		}
		var hb *generated.SessionMessageHandback
		for _, m := range msgs {
			if k, _ := m.Discriminator(); k == "handback" {
				if v, err := m.AsSessionMessageHandback(); err == nil {
					hb = &v
					break
				}
			}
		}
		if hb == nil {
			t.Fatal("expected a handback message in the inbox")
		}
		for _, p := range hb.Artifacts {
			if strings.Contains(p, "sk-secret-999") {
				t.Errorf("handback artifact path leaked the secret: %q", p)
			}
		}
	})

	t.Run("checkpoint_commit_ref", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{
			"kind": "checkpoint", "summary": "halfway", "commit_ref": "refs/sk-secret-999-head",
		})
		if result.IsError {
			t.Fatalf("checkpoint failed: %s", result.ForLLM)
		}
		msgs, _, _, err := inbox.Drain("parent-1", "child-1", "", 10)
		if err != nil {
			t.Fatalf("Drain: %v", err)
		}
		var cp *generated.SessionMessageCheckpoint
		for _, m := range msgs {
			if k, _ := m.Discriminator(); k == "checkpoint" {
				if v, err := m.AsSessionMessageCheckpoint(); err == nil {
					cp = &v
					break
				}
			}
		}
		if cp == nil {
			t.Fatal("expected a checkpoint message in the inbox")
		}
		if cp.CommitRef != nil && strings.Contains(*cp.CommitRef, "sk-secret-999") {
			t.Errorf("checkpoint commit_ref leaked the secret: %q", *cp.CommitRef)
		}
	})
}

// TestMessageParentTool_KillSwitchDisabled_FailsClosed_ArchM2 proves the
// arch-M2 fix: session_messaging.enabled (FR-196) is honored on the SYNC tool
// path, not just by the async consumer. When the injected closure returns
// false, Execute fails closed with a clear "plane disabled" error before
// touching the inbox. Also proves the positive path (enabled → proceeds) and
// that an unset closure fails OPEN (backward-compatible with bare unit tests).
func TestMessageParentTool_KillSwitchDisabled_FailsClosed_ArchM2(t *testing.T) {
	tool, _, _, _ := newMessageParentTestSetup(t) //nolint:dogsled // Only the tool is relevant from the four test fixtures.

	// Disabled plane → every kind is rejected at the guard, before the inbox.
	tool.SetSessionMessagingEnabled(func() bool { return false })
	res := tool.Execute(withChildContext("child-1"), map[string]any{"kind": "progress", "text": "x"})
	if res == nil || !strings.Contains(res.ForLLM, "session-messaging plane is disabled") {
		t.Fatalf("expected disabled-plane error, got: %+v", res)
	}

	// Enabled plane → the call proceeds (progress appends; no "disabled" error).
	tool.SetSessionMessagingEnabled(func() bool { return true })
	ok := tool.Execute(withChildContext("child-1"), map[string]any{"kind": "progress", "text": "all good"})
	if ok == nil || strings.Contains(ok.ForLLM, "disabled") {
		t.Fatalf("expected the call to proceed when enabled, got: %+v", ok)
	}

	// Unset closure → fail OPEN (matches the async consumer's nil-config posture).
	tool2, _, _, _ := newMessageParentTestSetup(t) //nolint:dogsled // Only the tool is relevant from the four test fixtures.
	openRes := tool2.Execute(withChildContext("child-1"), map[string]any{"kind": "progress", "text": "y"})
	if openRes == nil || strings.Contains(openRes.ForLLM, "disabled") {
		t.Fatalf("unset closure must fail open, got: %+v", openRes)
	}
}
