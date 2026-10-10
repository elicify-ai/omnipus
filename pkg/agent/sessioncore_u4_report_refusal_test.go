// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session-core U4 — refused report arrival (FR-013, BDD-04.3, T06/T16; #1211).
//
// RED pack, qa-lead. Oracle source: docs/internal/specs/session-core-spec.md
// FR-013 / BDD-04.3, issue #1211 D2/D3, and the architect's U4 decision
// (coordination/squads/session-core-build-20261008/ARCHITECT-ANSWER-U4-GAPS.md,
// Q1 and Q2).
//
// Q2 (fixed shape): the refusal text on the existing tool result is
//   message_parent: not delivered (reason=rate_limited, retry_after_seconds=60): <err>. …
// typed = the Go sentinel via errors.Is PLUS the stable reason=/retry tokens;
// no new field, no switch to JSON.
//
// Q1 (one recorder, two read paths): a cap refusal records
// LifecycleRecord.NotDelivered (typed generated.DelegateNotDeliveredSummary)
// and surfaces to the parent through (i) the delegate inbox response's
// not_delivered field, (ii) a subagent_message frame of kind "not_delivered"
// in the parent's transcript, and (iii) a clause in the delegate status text.
// Refused content is never kept anywhere.
//
// COMPILE NOTE (post-regen): generated.DelegateNotDeliveredSummary and the wire
// members DelegateInboxResponse.not_delivered / SubagentMessageFrame kind
// "not_delivered" are added by backend-lead (contract-first, regen). This pack
// therefore reads the record field by REFLECTION and the inbox response by JSON
// — it must not name either new type, or the pack could not compile before the
// regeneration. Assertions that can only pass after regeneration are marked
// "(post-regen)".

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// u4RefusedBodyMarker is a distinctive string so "never kept" is checkable in
// every read path (inbox, frame, record).
const u4RefusedBodyMarker = "REFUSED-BODY-MARKER-9f3a"

// u4NewMessageParentTool wires the real message_parent tool to the real
// deliverer and returns a context carrying the child's own delegate session id.
func u4NewMessageParentTool(lifecycle *session.LifecycleStore, deliverer *SteerUpwardDeliverer, childID string) (*tools.MessageParentTool, context.Context) {
	tool := tools.NewMessageParentTool(deliverer, lifecycle)
	tool.SetSessionMessagingEnabled(func() bool { return true })
	ctx := tools.WithDelegateSessionID(tools.WithTranscriptSessionID(context.Background(), childID), childID)
	return tool, ctx
}

// u4DelegateToolFor wires a real delegate tool for the parent-side read paths
// (status is plain text; inbox is the generated JSON response). The tool's
// caller owner key is the parent (the child's direct steering ancestor).
func u4DelegateToolFor(lifecycle *session.LifecycleStore, inbox *session.MessageInboxStore, callerID string) (*tools.DelegateTool, context.Context) {
	dt := tools.NewDelegateTool("", 0, 0)
	dt.SetLifecycleStore(lifecycle)
	dt.SetMessageInbox(inbox)
	dt.SetSessionMessagingEnabled(func() bool { return true })
	ctx := tools.WithTranscriptSessionID(context.Background(), callerID)
	return dt, ctx
}

// u4Refusal is the fixture a single real cap refusal leaves behind.
type u4Refusal struct {
	al        *AgentLoop
	lifecycle *session.LifecycleStore
	inbox     *session.MessageInboxStore
	result    *tools.ToolResult
	parentID  string
	childID   string
}

// u4RefuseAtRateCap produces one real cap refusal through the real
// message_parent tool (never a fake deliverer): it fills the parent/child rate
// window, then sends one progress report that must be refused.
func u4RefuseAtRateCap(t *testing.T) u4Refusal {
	t.Helper()
	al, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	seedUnifiedSession(t, al, parentID)
	tool, ctx := u4NewMessageParentTool(lifecycle, deliverer, childID)

	for i := 0; i < session.DefaultChildSendRatePerMinute; i++ {
		if _, err := inbox.Append(parentID, u4GeneratedProgress(t, childID, fmt.Sprintf("pre-%02d", i))); err != nil {
			t.Fatalf("pre-fill Append #%d: %v", i, err)
		}
	}
	res := tool.Execute(ctx, map[string]any{"kind": "progress", "text": u4RefusedBodyMarker})
	if !res.IsError {
		t.Fatalf("message_parent at the rate cap: IsError = false, want a visible refusal")
	}
	return u4Refusal{al: al, lifecycle: lifecycle, inbox: inbox, result: res, parentID: parentID, childID: childID}
}

// u4RecordNotDelivered reads LifecycleRecord.NotDelivered by reflection WITHOUT
// naming the not-yet-existing generated type. ok=false means the field does not
// exist yet; ok=true with nil means the field exists but is absent (nil).
func u4RecordNotDelivered(t *testing.T, rec *session.LifecycleRecord) (nd map[string]any, ok bool) {
	t.Helper()
	v := reflect.ValueOf(rec)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	f := v.FieldByName("NotDelivered")
	if !f.IsValid() {
		return nil, false
	}
	if f.Kind() == reflect.Pointer && f.IsNil() {
		return nil, true
	}
	raw, err := json.Marshal(f.Interface())
	if err != nil {
		t.Fatalf("marshal NotDelivered: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode NotDelivered: %v", err)
	}
	return m, true
}

func u4IntFromJSON(t *testing.T, v any) int64 {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("value %v (%T) is not a JSON number", v, v)
	}
	return int64(f)
}

// u4NotDeliveredFromInboxResponse runs delegate action=inbox and returns the
// decoded top-level JSON object plus whether not_delivered was present.
func u4NotDeliveredFromInboxResponse(t *testing.T, f u4Refusal) (map[string]any, bool) {
	t.Helper()
	dt, ctx := u4DelegateToolFor(f.lifecycle, f.inbox, f.parentID)
	res := dt.Execute(ctx, map[string]any{"action": "inbox", "session_id": f.childID})
	if res.IsError {
		t.Fatalf("delegate inbox: %s", res.ForLLM)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(res.ForLLM), &payload); err != nil {
		t.Fatalf("delegate inbox response is not JSON: %v (payload=%q)", err, res.ForLLM)
	}
	nd, ok := payload["not_delivered"].(map[string]any)
	return nd, ok
}

// TestMessageParent_RateCapRefusal_TypedWithRetryHint covers FR-013 / BDD-04.3
// (#1211 D3) and the architect's Q2: the refusal is TYPED (errors.Is on the
// preserved sentinel) and carries the FIXED, parseable hint
// `reason=rate_limited, retry_after_seconds=60`; the refused content is never
// admitted.
//
// RED today: the typed half holds, but the refusal text carries no
// reason=/retry_after_seconds tokens.
func TestMessageParent_RateCapRefusal_TypedWithRetryHint(t *testing.T) {
	f := u4RefuseAtRateCap(t)

	if !errors.Is(f.result.Err, session.ErrInboxRateLimited) {
		t.Fatalf("refusal error = %v, want a TYPED refusal wrapping session.ErrInboxRateLimited "+
			"(FR-013 / #1211 D3)", f.result.Err)
	}
	if !strings.Contains(f.result.ForLLM, "reason=rate_limited") {
		t.Fatalf("refusal text = %q missing the fixed token %q — Q2 fixes the reason token "+
			"(ARCHITECT-ANSWER-U4-GAPS)", f.result.ForLLM, "reason=rate_limited")
	}
	if !strings.Contains(f.result.ForLLM, "retry_after_seconds=60") {
		t.Fatalf("refusal text = %q missing the fixed token %q — a rate refusal must name the "+
			"retry window (Q2)", f.result.ForLLM, "retry_after_seconds=60")
	}

	// The refused body must never have been admitted.
	msgs, _, _, derr := f.inbox.Drain(f.parentID, f.childID, "", 100)
	if derr != nil {
		t.Fatalf("Drain: %v", derr)
	}
	for _, m := range msgs {
		if p, aerr := m.AsSessionMessageProgress(); aerr == nil && strings.Contains(p.Text, u4RefusedBodyMarker) {
			t.Fatalf("refused report content reached the parent inbox — BDD-04.3 forbids claiming " +
				"rejected content delivered")
		}
	}
}

// TestParent_RejectedArrivalObservation_Visible covers FR-013 / BDD-04.3
// (#1211 D2) and the architect's Q1: after one real cap refusal, the parent has
// a rejected/not-delivered observation on every commissioned read path —
// LifecycleRecord.NotDelivered (the recorder), the delegate inbox response, the
// delegate status text, and a subagent_message frame of kind "not_delivered" in
// the parent's transcript — and no path keeps the refused content.
//
// RED today: nothing records or reports the refusal.
func TestParent_RejectedArrivalObservation_Visible(t *testing.T) {
	t.Run("lifecycle record records the cap refusal", func(t *testing.T) {
		f := u4RefuseAtRateCap(t)
		rec, err := f.lifecycle.Load(f.childID)
		if err != nil {
			t.Fatalf("load child record: %v", err)
		}
		nd, exists := u4RecordNotDelivered(t, rec)
		if !exists {
			t.Fatalf("LifecycleRecord has no NotDelivered field — required by FR-013/BDD-04.3 " +
				"(ARCHITECT-ANSWER-U4-GAPS Q1.1); the field is added by the U4 implementation")
		}
		if nd == nil {
			t.Fatalf("LifecycleRecord.NotDelivered is nil after a rate-cap refusal, want a " +
				"DelegateNotDeliveredSummary (count=1, last_reason=rate_limited, last_kind=progress)")
		}
		if got := u4IntFromJSON(t, nd["count"]); got != 1 {
			t.Fatalf("NotDelivered.count = %d, want 1", got)
		}
		if nd["last_reason"] != "rate_limited" {
			t.Fatalf("NotDelivered.last_reason = %v, want \"rate_limited\"", nd["last_reason"])
		}
		if nd["last_kind"] != "progress" {
			t.Fatalf("NotDelivered.last_kind = %v, want \"progress\"", nd["last_kind"])
		}
	})

	t.Run("delegate inbox reports not_delivered (post-regen)", func(t *testing.T) {
		f := u4RefuseAtRateCap(t)
		nd, ok := u4NotDeliveredFromInboxResponse(t, f)
		if !ok {
			t.Fatalf("delegate inbox response carries no not_delivered — required by Q1.2 " +
				"(DelegateInboxResponse.not_delivered, post-regen)")
		}
		if got := u4IntFromJSON(t, nd["count"]); got != 1 {
			t.Fatalf("not_delivered.count = %d, want 1", got)
		}
		if nd["last_reason"] != "rate_limited" {
			t.Fatalf("not_delivered.last_reason = %v, want \"rate_limited\"", nd["last_reason"])
		}
		if nd["last_kind"] != "progress" {
			t.Fatalf("not_delivered.last_kind = %v, want \"progress\"", nd["last_kind"])
		}

		// Content never kept on this path either.
		dt, ctx := u4DelegateToolFor(f.lifecycle, f.inbox, f.parentID)
		res := dt.Execute(ctx, map[string]any{"action": "inbox", "session_id": f.childID})
		if strings.Contains(res.ForLLM, u4RefusedBodyMarker) {
			t.Fatalf("the refused report body appears in the delegate inbox response — content must " +
				"never be kept (C-INBOX 'no overflow store')")
		}
	})

	t.Run("parent transcript holds a not_delivered frame", func(t *testing.T) {
		f := u4RefuseAtRateCap(t)
		entries, err := f.al.GetSessionStore().ReadTranscript(f.parentID)
		if err != nil {
			t.Fatalf("ReadTranscript(parent): %v", err)
		}
		var frame *session.TranscriptEntry
		for i := range entries {
			e := &entries[i]
			if e.SystemSubtype == session.SystemSubtypeSubagentMessage &&
				e.SubagentMessage != nil && e.SubagentMessage.Kind == "not_delivered" {
				frame = e
				break
			}
		}
		if frame == nil {
			t.Fatalf("parent transcript has no subagent_message frame of kind \"not_delivered\" — " +
				"required by Q1.4 (best-effort, same family as deliverSubagentMessage)")
		}
		if frame.SubagentMessage.ChildSessionId == nil || *frame.SubagentMessage.ChildSessionId != f.childID {
			t.Fatalf("not_delivered frame child_session_id = %v, want %q",
				frame.SubagentMessage.ChildSessionId, f.childID)
		}
		if frame.SubagentMessage.UntrustedOrigin {
			t.Fatalf("not_delivered frame untrusted_origin = true, want false — it is server-authored " +
				"(Q1.4), unlike the child-authored message frames")
		}
		if frame.SubagentMessage.Text != nil && strings.Contains(*frame.SubagentMessage.Text, u4RefusedBodyMarker) {
			t.Fatalf("not_delivered frame text contains the refused body — never the refused body (Q1.4)")
		}
	})

	t.Run("delegate status names the refusal", func(t *testing.T) {
		f := u4RefuseAtRateCap(t)
		dt, ctx := u4DelegateToolFor(f.lifecycle, f.inbox, f.parentID)
		res := dt.Execute(ctx, map[string]any{"action": "status", "session_id": f.childID})
		if res.IsError {
			t.Fatalf("delegate status: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "not delivered") {
			t.Fatalf("delegate status text = %q missing the %q clause — required by Q1.3",
				res.ForLLM, "not delivered")
		}
	})

	t.Run("accepted report leaves not_delivered absent", func(t *testing.T) {
		al, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
		const parentID, childID = "parent-1", "child-1"
		seedParentAndChild(t, lifecycle, parentID, childID)
		seedUnifiedSession(t, al, parentID)
		tool, ctx := u4NewMessageParentTool(lifecycle, deliverer, childID)
		if res := tool.Execute(ctx, map[string]any{"kind": "progress", "text": "an accepted report"}); res.IsError {
			t.Fatalf("accepted progress rejected: %s", res.ForLLM)
		}

		rec, err := lifecycle.Load(childID)
		if err != nil {
			t.Fatalf("load child record: %v", err)
		}
		if nd, exists := u4RecordNotDelivered(t, rec); exists && nd != nil {
			t.Fatalf("accepted report set NotDelivered = %v, want absent/nil (negative control)", nd)
		}

		f := u4Refusal{al: al, lifecycle: lifecycle, inbox: inbox, parentID: parentID, childID: childID}
		if nd, ok := u4NotDeliveredFromInboxResponse(t, f); ok {
			t.Fatalf("accepted report produced not_delivered = %v, want absent (negative control)", nd)
		}
	})
}
