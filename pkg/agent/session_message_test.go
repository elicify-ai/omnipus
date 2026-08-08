// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package-internal (white-box) tests for the ADR-053 S3 SessionMessage
// transport generalization: steering.go's DeliverSessionMessage and
// async_notifier.go's WakeParent. Lives in `package agent` (not
// `package agent_test`) because it exercises unexported queue-drain
// helpers, mirroring async_notifier_test.go's own note.

package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func steerMsg(t *testing.T, sessionID, text string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageSteer(generated.SessionMessageSteer{
		MessageId: "steer-1", SessionId: sessionID, CreatedAt: time.Now(), Text: text,
	}); err != nil {
		t.Fatalf("FromSessionMessageSteer failed: %v", err)
	}
	return sm
}

func respondMsg(t *testing.T, sessionID, correlationID, text string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageRespond(generated.SessionMessageRespond{
		MessageId: "respond-1", SessionId: sessionID, CreatedAt: time.Now(),
		CorrelationId: correlationID, Text: text,
	}); err != nil {
		t.Fatalf("FromSessionMessageRespond failed: %v", err)
	}
	return sm
}

func progressSessionMsg(t *testing.T, sessionID string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId: "progress-1", SessionId: sessionID, CreatedAt: time.Now(),
		SenderIdentity: "child", Text: "still working",
	}); err != nil {
		t.Fatalf("FromSessionMessageProgress failed: %v", err)
	}
	return sm
}

// TestDeliverSessionMessage_Steer_LandsInSteeringQueue proves the
// GENERALIZATION claim: a typed `steer` SessionMessage is delivered via the
// SAME steeringQueue/enqueueSteeringMessage mechanism chat steering already
// uses — same scope key, same drain call.
func TestDeliverSessionMessage_Steer_LandsInSteeringQueue(t *testing.T) {
	al, _ := newAsyncNotifierTestLoop(t)

	scope := "agent:child-agent:child-session-1"
	msg := steerMsg(t, "child-session-1", "please check the tests too")

	if err := al.DeliverSessionMessage(context.Background(), scope, "child-agent", msg); err != nil {
		t.Fatalf("DeliverSessionMessage(steer) failed: %v", err)
	}

	drained := al.dequeueSteeringMessagesForScope(scope)
	if len(drained) != 1 {
		t.Fatalf("expected 1 queued steering message, got %d", len(drained))
	}
	if drained[0].Content != "please check the tests too" {
		t.Errorf("Content = %q, want %q", drained[0].Content, "please check the tests too")
	}
	if drained[0].Role != "user" {
		t.Errorf("Role = %q, want %q", drained[0].Role, "user")
	}
}

// TestDeliverSessionMessage_Respond_LandsInSteeringQueue mirrors the steer
// case for `respond` — the OTHER parent->child turn-injection kind.
func TestDeliverSessionMessage_Respond_LandsInSteeringQueue(t *testing.T) {
	al, _ := newAsyncNotifierTestLoop(t)

	scope := "agent:child-agent:child-session-2"
	msg := respondMsg(t, "child-session-2", "corr-1", "yes, proceed")

	if err := al.DeliverSessionMessage(context.Background(), scope, "child-agent", msg); err != nil {
		t.Fatalf("DeliverSessionMessage(respond) failed: %v", err)
	}

	drained := al.dequeueSteeringMessagesForScope(scope)
	if len(drained) != 1 || drained[0].Content != "yes, proceed" {
		t.Fatalf("expected the respond text queued, got: %+v", drained)
	}
}

// TestDeliverSessionMessage_NonInjectableKind_ReturnsSentinel proves a
// child->parent (or engine) kind is REJECTED as a turn injection rather
// than silently accepted into the wrong mechanism — the caller must route
// it to the inbox/wake instead.
func TestDeliverSessionMessage_NonInjectableKind_ReturnsSentinel(t *testing.T) {
	al, _ := newAsyncNotifierTestLoop(t)

	msg := progressSessionMsg(t, "child-session-3")
	err := al.DeliverSessionMessage(context.Background(), "agent:child-agent:child-session-3", "child-agent", msg)
	if !errors.Is(err, ErrSessionMessageNotTurnInjectable) {
		t.Errorf("expected ErrSessionMessageNotTurnInjectable, got: %v", err)
	}
}

// TestWakeParent_RejectsNonWakeableKind proves the closed kind allow-list —
// progress/checkpoint/artifact never trigger a wake (they are inbox-only).
func TestWakeParent_RejectsNonWakeableKind(t *testing.T) {
	al, _ := newAsyncNotifierTestLoop(t)
	notifier := al.asyncNotifier

	err := notifier.WakeParent(context.Background(), "progress", tools.MessageParentWakeEvent{
		Channel: "slack", ChatID: "C1", AgentID: "parent-agent", Content: "x",
	})
	if err == nil {
		t.Fatal("expected an error for a non-wakeable kind, got nil")
	}
}

// TestWakeParent_DebounceAndRateCap proves the >=15s debounce and <=4/h
// rate cap using a fake clock — a debounced/rate-limited wake returns nil
// (never an error; the message is already durably stored elsewhere) but
// does NOT actually publish.
func TestWakeParent_DebounceAndRateCap(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)
	notifier := al.asyncNotifier

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	notifier.SetWakeClock(func() time.Time { return now })

	event := tools.MessageParentWakeEvent{Channel: "slack", ChatID: "C1", AgentID: "parent-agent", Content: "a blocker"}

	if err := notifier.WakeParent(context.Background(), "blocker", event); err != nil {
		t.Fatalf("first WakeParent failed: %v", err)
	}
	select {
	case <-msgBus.InboundChan():
	case <-time.After(time.Second):
		t.Fatal("expected the first wake to publish an inbound message")
	}

	// Immediately retrying (< 15s later) must be debounced — no error, no
	// second publish.
	if err := notifier.WakeParent(context.Background(), "blocker", event); err != nil {
		t.Fatalf("debounced WakeParent should return nil, got: %v", err)
	}
	select {
	case <-msgBus.InboundChan():
		t.Fatal("expected the debounced wake to NOT publish a second inbound message")
	case <-time.After(50 * time.Millisecond):
	}

	// Advance past the debounce window repeatedly to exhaust the 4/h cap.
	for i := 0; i < 3; i++ {
		now = now.Add(20 * time.Second)
		if err := notifier.WakeParent(context.Background(), "blocker", event); err != nil {
			t.Fatalf("WakeParent #%d failed: %v", i, err)
		}
		<-msgBus.InboundChan()
	}

	// The 5th wake within the hour (1 initial + 3 above + this one) must be
	// suppressed by the rate cap.
	now = now.Add(20 * time.Second)
	if err := notifier.WakeParent(context.Background(), "blocker", event); err != nil {
		t.Fatalf("rate-capped WakeParent should return nil, got: %v", err)
	}
	select {
	case <-msgBus.InboundChan():
		t.Fatal("expected the 5th wake within the hour to be suppressed by the rate cap")
	case <-time.After(50 * time.Millisecond):
	}
}

// TestWakeParent_SweepsQuiescentWakeWindows is the LOW-finding regression
// guard (mirroring pkg/session/message_inbox.go's identical rateWindows
// leak/fix): wakeWindows must not accumulate one permanent entry per
// ever-woken parent conversation forever. Once the map's key count crosses
// wakeWindowSweepThreshold, the next WakeParent call sweeps every OTHER key
// whose window has fully expired (all timestamps older than the 1-hour rate
// window), deleting it.
func TestWakeParent_SweepsQuiescentWakeWindows(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)
	notifier := al.asyncNotifier

	oldThreshold := wakeWindowSweepThreshold
	wakeWindowSweepThreshold = 5
	t.Cleanup(func() { wakeWindowSweepThreshold = oldThreshold })

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	notifier.SetWakeClock(func() time.Time { return now })

	for i := 0; i < 5; i++ {
		event := tools.MessageParentWakeEvent{
			Channel: "slack", ChatID: "quiescent-" + string(rune('A'+i)), AgentID: "parent-agent", Content: "x",
		}
		if err := notifier.WakeParent(context.Background(), "blocker", event); err != nil {
			t.Fatalf("WakeParent #%d failed: %v", i, err)
		}
		select {
		case <-msgBus.InboundChan():
		case <-time.After(time.Second):
			t.Fatalf("WakeParent #%d: expected a publish", i)
		}
	}
	if got := len(notifier.wakeWindows); got != 5 {
		t.Fatalf("fixture broken: want 5 wake-window keys before advancing time, got %d", got)
	}

	// Advance well past the 1-hour rate window so all 5 existing keys are
	// now fully quiescent, then push the map size past the (lowered)
	// threshold with one more distinct conversation's wake — the call that
	// must trigger the sweep.
	now = now.Add(2 * time.Hour)
	triggerEvent := tools.MessageParentWakeEvent{
		Channel: "slack", ChatID: "trigger-conversation", AgentID: "parent-agent", Content: "x",
	}
	if err := notifier.WakeParent(context.Background(), "blocker", triggerEvent); err != nil {
		t.Fatalf("trigger WakeParent failed: %v", err)
	}
	select {
	case <-msgBus.InboundChan():
	case <-time.After(time.Second):
		t.Fatal("trigger WakeParent: expected a publish")
	}

	// Positive lower bound alongside the near-zero assertion: exactly the
	// ONE key the triggering call itself just wrote must survive — proving
	// the sweep is targeted (only quiescent keys), not "delete everything".
	if got := len(notifier.wakeWindows); got != 1 {
		t.Fatalf("want exactly 1 surviving wake-window key after the sweep (the triggering call's own), got %d: %v",
			got, notifier.wakeWindows)
	}
}

// TestValidateContextSnapshot_AllowlistAndCap proves R§8.5: a nil/empty
// snapshot is always valid; an over-cap discretionary snapshot is rejected
// with a narrow-the-snapshot error naming which cap was exceeded.
func TestValidateContextSnapshot_AllowlistAndCap(t *testing.T) {
	if err := ValidateContextSnapshot(nil, 0, 0); err != nil {
		t.Errorf("nil snapshot should always validate, got: %v", err)
	}

	small := &ContextSnapshot{References: []string{"file.txt"}, Notes: "short"}
	if err := ValidateContextSnapshot(small, 0, 0); err != nil {
		t.Errorf("small snapshot should validate under defaults, got: %v", err)
	}

	tooManyRefs := &ContextSnapshot{References: make([]string, 51)}
	for i := range tooManyRefs.References {
		tooManyRefs.References[i] = "ref"
	}
	err := ValidateContextSnapshot(tooManyRefs, 0, 0)
	if !errors.Is(err, ErrSnapshotOverCap) {
		t.Errorf("expected ErrSnapshotOverCap for >50 refs, got: %v", err)
	}

	overBytes := &ContextSnapshot{Notes: string(make([]byte, 10*1024))}
	err = ValidateContextSnapshot(overBytes, 0, 0)
	if !errors.Is(err, ErrSnapshotOverCap) {
		t.Errorf("expected ErrSnapshotOverCap for oversized notes, got: %v", err)
	}

	// Explicit override caps are honored.
	err = ValidateContextSnapshot(&ContextSnapshot{Notes: "12345"}, 4, 50)
	if !errors.Is(err, ErrSnapshotOverCap) {
		t.Errorf("expected ErrSnapshotOverCap with an explicit maxBytes=4 override, got: %v", err)
	}
}
