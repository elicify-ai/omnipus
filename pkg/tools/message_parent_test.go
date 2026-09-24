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
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// fakeUpwardDeliverer is a test-only steer.UpwardDeliverer (ADR-091 I-5)
// standing in for pkg/agent's real SteerUpwardDeliverer, close enough to it
// for this file's own tests: it resolves the owner key from the child's
// lifecycle record exactly like ownerKeyFor (edge first, SteeringSessionID
// fallback) and appends to a REAL *session.MessageInboxStore, so the
// existing inbox.Drain(...) assertions throughout this file keep proving
// what they always proved. Every Deliver call is recorded, mirroring the
// former fakeWaker's calls shape so this file's existing assertions
// (`waker.calls[0].kind`) needed no restructuring, only a rename.
type fakeUpwardDeliverer struct {
	lifecycle *session.LifecycleStore
	inbox     *session.MessageInboxStore
	calls     []struct {
		kind  string
		event steer.UpwardEvent
	}
	// err simulates a WAKE-TRANSPORT failure (never an Append failure) —
	// matching the real SteerUpwardDeliverer, which always swallows a wake
	// failure (logs it, returns success) since the message is already
	// durably stored by the time the wake is attempted. Recorded, never
	// returned from Deliver.
	err error
}

func (f *fakeUpwardDeliverer) Deliver(_ context.Context, event steer.UpwardEvent) (steer.Delivery, error) {
	ownerKey := ""
	if rec, err := f.lifecycle.Load(event.ChildSessionID); err == nil && rec != nil {
		ownerKey = ownerKeyFor(rec)
	}
	res, err := f.inbox.Append(ownerKey, event.Message)
	if err != nil {
		return steer.Delivery{}, err
	}
	kind, _ := event.Message.Discriminator()
	if kind == "blocker" || kind == "question" || kind == "handback" {
		f.calls = append(f.calls, struct {
			kind  string
			event steer.UpwardEvent
		}{kind, event})
		if f.err != nil {
			// Best-effort, matching the real Deliver: a wake-transport
			// failure never fails the call — the entry is already stored.
			return steer.Delivery{MessageID: res.MessageID, Outcome: steer.DeliveryWoke}, nil
		}
	}
	return steer.Delivery{MessageID: res.MessageID, Outcome: steer.DeliveryWoke}, nil
}

func newMessageParentTestSetup(t *testing.T) (*MessageParentTool, *session.LifecycleStore, *session.MessageInboxStore, *fakeUpwardDeliverer) {
	t.Helper()
	lc := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	deliverer := &fakeUpwardDeliverer{lifecycle: lc, inbox: inbox}

	tool := NewMessageParentTool(deliverer, lc)
	// Enable the session-messaging plane by default for tests so the
	// fail-closed kill switch (fix B.5) does not reject every test call.
	// Tests that exercise the kill switch itself override this.
	tool.SetSessionMessagingEnabled(func() bool { return true })

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID:      "child-1",
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeParentSession,
		OwnerScopeID:   "parent-delegate-id",
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID:    "ws-1",
		AgentID:        "worker",
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}

	return tool, lc, inbox, deliverer
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
	tool := NewMessageParentTool(&fakeUpwardDeliverer{lifecycle: lc, inbox: inbox}, lc)
	tool.SetSessionMessagingEnabled(func() bool { return true }) // fix B.5: default fail-closed

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-3p", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: "parent-x",
		SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"}, WorkspaceID: "ws-1", AgentID: "worker-3p",
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
	tool := NewMessageParentTool(&fakeUpwardDeliverer{lifecycle: lc, inbox: inbox}, lc)
	tool.SetSessionMessagingEnabled(func() bool { return true }) // fix B.5: default fail-closed

	result := tool.Execute(context.Background(), map[string]any{"kind": "progress", "text": "x"})
	if !result.IsError {
		t.Fatal("expected an error with no session context")
	}
}

// TestMessageParentTool_TaskRun_NoParentSession_RedirectsToGoalClaim proves a
// native task run's root turn — which carries tools.WithRunningTaskID on ctx
// (task_executor.go, before processTaskDirect) but never
// tools.WithDelegateSessionID (only pkg/agent/subturn.go's spawnSubTurn sets
// that, for a real delegated child) — gets an error that tells the worker
// what to do instead of the generic "no session context available for this
// call" text, since a task-dispatch session structurally has no delegating
// parent to message (task_executor.go's mintTaskLifecycleRecord leaves
// SteeringSessionID empty on purpose).
func TestMessageParentTool_TaskRun_NoParentSession_RedirectsToGoalClaim(t *testing.T) {
	lc := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	tool := NewMessageParentTool(&fakeUpwardDeliverer{lifecycle: lc, inbox: inbox}, lc)
	tool.SetSessionMessagingEnabled(func() bool { return true }) // fix B.5: default fail-closed

	ctx := WithRunningTaskID(context.Background(), "task-42")
	result := tool.Execute(ctx, map[string]any{"kind": "progress", "text": "x"})
	if !result.IsError {
		t.Fatal("expected an error: a task run has no parent session to message")
	}
	if strings.Contains(result.ForLLM, "no session context available for this call") {
		t.Fatalf("expected the task-run-specific redirect, got the generic message: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "goal_claim") {
		t.Fatalf("expected the error to redirect the worker to goal_claim, got: %s", result.ForLLM)
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

// TestToIntArg_RejectsFractionalFloat pins the semantics chosen when
// delegate_goal.go's `integerArgument` and this file's `toIntArg` — same
// package, same signature, opposite behaviour on a fractional float — were
// unified into the single parser in message_parent.go.
//
// The strict rule won because all three call sites promise the model an
// integer in their own rejection message (message_parent `pct`: "must be an
// integer 0-100"; delegate_status `max`: "must be an integer";
// delegate_goal `check.expected_exit_code`: "must be an integer from 0 to
// 255"). Truncating 50.7 to 50 accepted a value the tool had just told the
// model it would not accept. int64 is carried over from the truncating
// version because a Go-side caller can produce one.
//
// Expected values come from that contract, not from reading the
// implementation: a whole-valued float converts, a fractional one is an
// error, and a non-number is an error.
func TestToIntArg_RejectsFractionalFloat(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      any
		want    int
		wantErr bool
	}{
		{name: "int", in: 42, want: 42},
		{name: "int64", in: int64(7), want: 7},
		{name: "whole float (the shape a JSON decoder produces for 50)", in: float64(50), want: 50},
		{name: "negative whole float", in: float64(-3), want: -3},
		{name: "zero", in: float64(0), want: 0},
		{name: "fractional float is rejected, never truncated", in: 50.7, wantErr: true},
		{name: "fractional float below one is rejected", in: 0.5, wantErr: true},
		{name: "negative fractional float is rejected", in: -1.5, wantErr: true},
		{name: "string is not a number", in: "50", wantErr: true},
		{name: "bool is not a number", in: true, wantErr: true},
		{name: "nil is not a number", in: nil, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := toIntArg(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("toIntArg(%#v) = (%d, nil), want an error", tc.in, got)
				}
				if got != 0 {
					t.Fatalf("toIntArg(%#v) returned %d alongside its error, want the zero value", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("toIntArg(%#v) returned error %v, want (%d, nil)", tc.in, err, tc.want)
			}
			if got != tc.want {
				t.Fatalf("toIntArg(%#v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// --- ADR-091 fix lane RX-OUTCOME, Task 1 ---

// storedNotWokenDeliverer is a steer.UpwardDeliverer that appends to a real
// inbox and then reports the entry as STORED BUT NOT WOKEN — the outcome
// message_parent used to discard with "mt.delivery.Outcome is available for
// callers that want it (none today)".
type storedNotWokenDeliverer struct {
	inbox     *session.MessageInboxStore
	ownerKey  string
	messageID string
}

func (d *storedNotWokenDeliverer) Deliver(_ context.Context, event steer.UpwardEvent) (steer.Delivery, error) {
	res, err := d.inbox.Append(d.ownerKey, event.Message)
	if err != nil {
		return steer.Delivery{}, err
	}
	d.messageID = res.MessageID
	return steer.Delivery{MessageID: res.MessageID, Outcome: steer.DeliveryStoredNotWoken}, nil
}

// newStoredNotWokenSetup builds message_parent over a deliverer that always
// reports stored_not_woken. It seeds its OWN lifecycle record rather than
// reusing newMessageParentTestSetup: that helper's seed predates commit
// 21edbad7e's durable-field validation and no longer persists (see this
// lane's report), and these two tests must not depend on that being fixed.
func newStoredNotWokenSetup(t *testing.T) (*MessageParentTool, *storedNotWokenDeliverer) {
	t.Helper()
	lc := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	deliverer := &storedNotWokenDeliverer{inbox: inbox, ownerKey: "parent-1"}
	tool := NewMessageParentTool(deliverer, lc)
	tool.SetSessionMessagingEnabled(func() bool { return true })
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-not-woken", Generation: 3, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: "parent-1",
		SteeredBy:   &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}
	return tool, deliverer
}

// TestMessageParent_StoredNotWoken_LoggedAtError proves message_parent now
// ACTS on steer.Delivery.Outcome for a wake-eligible kind. A `blocker` the
// parent was never woken for is an entry nothing will read until boot
// recovery — previously completely silent.
func TestMessageParent_StoredNotWoken_LoggedAtError(t *testing.T) {
	logs := captureSlogInfo(t)
	tool, deliverer := newStoredNotWokenSetup(t)

	result := tool.Execute(withChildContext("child-not-woken"), map[string]any{
		"kind": "blocker", "text": "the deploy key is missing", "severity": "high",
	})
	if result.IsError {
		t.Fatalf("message_parent(blocker) failed: %s", result.ForLLM)
	}

	captured := logs.String()
	if !strings.Contains(captured, `"level":"ERROR"`) {
		t.Fatalf("a blocker the parent was never woken for produced no ERROR line — the stall is invisible; captured log:\n%s", captured)
	}
	for _, want := range []string{"child-not-woken", "parent-1", deliverer.messageID, string(steer.DeliveryStoredNotWoken)} {
		if !strings.Contains(captured, want) {
			t.Errorf("captured ERROR log does not name %q; captured log:\n%s", want, captured)
		}
	}
	if !strings.Contains(captured, `"generation":3`) {
		t.Errorf("captured ERROR log does not carry the child's generation; captured log:\n%s", captured)
	}
}

// TestMessageParent_ProgressStoredNotWokenStaysQuiet is the gate half: for
// `progress` (never wake-eligible, FR-B-010) stored_not_woken IS the
// contract, so it must produce no ERROR line. Without this, a report that
// fired on every stored_not_woken would pass the test above and drown the
// real signal in production.
func TestMessageParent_ProgressStoredNotWokenStaysQuiet(t *testing.T) {
	logs := captureSlogInfo(t)
	tool, _ := newStoredNotWokenSetup(t)

	result := tool.Execute(withChildContext("child-not-woken"), map[string]any{
		"kind": "progress", "text": "still checking the checkout page",
	})
	if result.IsError {
		t.Fatalf("message_parent(progress) failed: %s", result.ForLLM)
	}

	if captured := logs.String(); strings.Contains(captured, `"level":"ERROR"`) {
		t.Fatalf("a progress entry stored without a wake produced an ERROR line — that outcome is its contract, not a failure; captured log:\n%s", captured)
	}
}
