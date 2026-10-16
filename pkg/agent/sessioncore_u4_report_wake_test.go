// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session-core U4 — helper report class, wake/retain/queue behaviour
// (FR-012, BDD-04.1/04.2, T06/T16).
//
// Oracle: docs/internal/specs/parent-wake-visibility-spec.md, PW-D4/PW-D13,
// FR-010/011/012/025 and §16 (narrows session-core FR-012/BDD-04.1/04.2).
// PW-D4 (founder 2026-10-15): progress, checkpoint and artifact never wake or join.
// Accepted chatter is stored/shown under the same rate/count caps. Questions
// still wake/join; an intentional Stop still refuses revival. Legacy test names
// are retained for traceability; their assertions follow the new policy.

package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// u4GeneratedProgress / Checkpoint / Artifact build one message of each
// accepted non-waking chatter kind (field shapes mirror the generated structs
// the spec cites: contracts/components/schemas/SessionMessage*.yaml).
func u4GeneratedProgress(t *testing.T, childID, messageID string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId: messageID, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Text: "working on it",
	}); err != nil {
		t.Fatalf("FromSessionMessageProgress: %v", err)
	}
	return sm
}

func u4GeneratedCheckpoint(t *testing.T, childID, messageID string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageCheckpoint(generated.SessionMessageCheckpoint{
		MessageId: messageID, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Summary: "halfway",
	}); err != nil {
		t.Fatalf("FromSessionMessageCheckpoint: %v", err)
	}
	return sm
}

func u4GeneratedArtifact(t *testing.T, childID, messageID string) generated.SessionMessage {
	t.Helper()
	note := "draft report"
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageArtifact(generated.SessionMessageArtifact{
		MessageId: messageID, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Paths: []string{"reports/summary.md"}, Note: &note,
	}); err != nil {
		t.Fatalf("FromSessionMessageArtifact: %v", err)
	}
	return sm
}

// u4ProgressEvent builds a progress UpwardEvent (kind=progress) for a child.
func u4ProgressEvent(t *testing.T, childID, messageID string) steer.UpwardEvent {
	t.Helper()
	return steer.UpwardEvent{
		ChildSessionID: childID,
		Outcome:        steer.OutcomeProgress,
		Message:        u4GeneratedProgress(t, childID, messageID),
	}
}

// u4QuestionEvent supplies a real wake reason for Stop and live-join controls.
func u4QuestionEvent(t *testing.T, childID, messageID string) steer.UpwardEvent {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageQuestion(generated.SessionMessageQuestion{
		MessageId: messageID, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", CorrelationId: "corr-" + messageID, Text: "which file?",
	}); err != nil {
		t.Fatalf("FromSessionMessageQuestion: %v", err)
	}
	return steer.UpwardEvent{ChildSessionID: childID, Outcome: steer.OutcomeParkedQuestion, Message: sm}
}

// PW-D4 (founder 2026-10-15): accepted chatter is stored once, never an idle wake.
// The legacy wake-expansion test keeps all three kinds and their inbox checks.
func TestDeliver_AcceptedReportKinds_WakeIdleNonStoppedParent(t *testing.T) {
	t.Run("progress", func(t *testing.T) {
		al, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
		const parentID, childID = "parent-1", "child-1"
		seedParentAndChild(t, lifecycle, parentID, childID)
		seedUnifiedSession(t, al, parentID)

		delivery, err := deliverer.Deliver(context.Background(), u4ProgressEvent(t, childID, "p-1"))
		if err != nil {
			t.Fatalf("Deliver(progress): %v", err)
		}
		if delivery.Outcome != steer.DeliveryStoredNotWoken {
			t.Errorf("progress delivery outcome = %q, want %q — PW-D4 forbids chatter wakes", delivery.Outcome, steer.DeliveryStoredNotWoken)
		}
		msgs, _, _, derr := inbox.Drain(parentID, childID, "", 10)
		if derr != nil {
			t.Fatalf("Drain(progress): %v", derr)
		}
		if len(msgs) != 1 {
			t.Fatalf("parent inbox progress entries = %d, want exactly 1", len(msgs))
		}
	})

	t.Run("checkpoint", func(t *testing.T) {
		al, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
		const parentID, childID = "parent-1", "child-1"
		seedParentAndChild(t, lifecycle, parentID, childID)
		seedUnifiedSession(t, al, parentID)

		event := steer.UpwardEvent{
			ChildSessionID: childID,
			Outcome:        steer.OutcomeCheckpoint,
			Message:        u4GeneratedCheckpoint(t, childID, "c-1"),
		}
		delivery, err := deliverer.Deliver(context.Background(), event)
		if err != nil {
			t.Fatalf("Deliver(checkpoint): %v", err)
		}
		if delivery.Outcome != steer.DeliveryStoredNotWoken {
			t.Errorf("checkpoint delivery outcome = %q, want %q — PW-D4 forbids chatter wakes", delivery.Outcome, steer.DeliveryStoredNotWoken)
		}
		msgs, _, _, derr := inbox.Drain(parentID, childID, "", 10)
		if derr != nil || len(msgs) != 1 {
			t.Fatalf("parent inbox checkpoint entries = %d (err=%v), want exactly 1", len(msgs), derr)
		}
	})

	t.Run("artifact", func(t *testing.T) {
		al, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
		const parentID, childID = "parent-1", "child-1"
		seedParentAndChild(t, lifecycle, parentID, childID)
		seedUnifiedSession(t, al, parentID)

		event := steer.UpwardEvent{
			ChildSessionID: childID,
			Outcome:        steer.OutcomeCheckpoint,
			Message:        u4GeneratedArtifact(t, childID, "a-1"),
		}
		delivery, err := deliverer.Deliver(context.Background(), event)
		if err != nil {
			t.Fatalf("Deliver(artifact): %v", err)
		}
		if delivery.Outcome != steer.DeliveryStoredNotWoken {
			t.Errorf("artifact delivery outcome = %q, want %q — PW-D4 forbids chatter wakes", delivery.Outcome, steer.DeliveryStoredNotWoken)
		}
		msgs, _, _, derr := inbox.Drain(parentID, childID, "", 10)
		if derr != nil || len(msgs) != 1 {
			t.Fatalf("parent inbox artifact entries = %d (err=%v), want exactly 1", len(msgs), derr)
		}
	})
}

// PW-D4/PW-D13 (founder 2026-10-15): a question wakes only a non-stopped parent.
// Use questions, not chatter, so Stop refusal still tests a genuinely eligible
// wake. Keep the no-revival generation and duplicate-retention assertions.
func TestDeliver_AcceptedReportKind_WakesIdleThenRetainedWhenParentStopped(t *testing.T) {
	al, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	seedUnifiedSession(t, al, parentID)
	ctx := context.Background()

	// 1. Idle, non-stopped parent: a question still wakes it (PW-D4).
	idle, err := deliverer.Deliver(ctx, u4QuestionEvent(t, childID, "q-idle"))
	if err != nil {
		t.Fatalf("Deliver(question, idle parent): %v", err)
	}
	if idle.Outcome != steer.DeliveryWoke {
		t.Fatalf("idle-parent question outcome = %q, want %q (PW-D4)", idle.Outcome, steer.DeliveryWoke)
	}

	// 2. Parent stops (current generation). A new report must be retained, not
	// woken, and must not revive the parent.
	if merr := lifecycle.Mutate(parentID, func(rec *session.LifecycleRecord) error {
		rec.Stop = &session.Stop{
			At: time.Now().UTC(), Generation: rec.Generation,
			By: session.Principal{Kind: session.PrincipalKindHuman, ID: "dan"},
		}
		return nil
	}); merr != nil {
		t.Fatalf("stamp Stop on parent: %v", merr)
	}
	genBefore := u4Generation(t, lifecycle, parentID)

	stopped, err := deliverer.Deliver(ctx, u4QuestionEvent(t, childID, "q-stopped"))
	if err != nil {
		t.Fatalf("Deliver(question, stopped parent): %v", err)
	}
	if stopped.Outcome != steer.DeliveryStoredNotWoken {
		t.Fatalf("stopped-parent question outcome = %q, want %q — PW-D13 forbids revival by a report",
			stopped.Outcome, steer.DeliveryStoredNotWoken)
	}
	if got := u4Generation(t, lifecycle, parentID); got != genBefore {
		t.Fatalf("parent generation after a report to a stopped parent = %d, want %d (no revival)",
			got, genBefore)
	}

	// 3. Duplicate replay of the retained report adds no copy.
	if _, err := deliverer.Deliver(ctx, u4QuestionEvent(t, childID, "q-stopped")); err != nil {
		t.Fatalf("Deliver(question replay): %v", err)
	}
	msgs, _, _, derr := inbox.Drain(parentID, childID, "", 10)
	if derr != nil {
		t.Fatalf("Drain: %v", derr)
	}
	if len(msgs) != 2 {
		t.Fatalf("parent inbox entries = %d, want exactly 2 (one idle-woken, one retained; the replay "+
			"must add no copy) per BDD-04.2", len(msgs))
	}
}

// PW-D4 (founder 2026-10-15): live progress stays stored without joining a turn.
// A question control retains the original identity-bearing live-join assertion.
func TestDeliver_ProgressToLiveParent_QueuesIntoLiveTurn(t *testing.T) {
	t.Run("progress is stored and does not join", func(t *testing.T) {
		al, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
		const parentID, childID = "parent-1", "child-1"
		seedParentAndChild(t, lifecycle, parentID, childID)
		seedUnifiedSession(t, al, parentID)
		al.activeTurnStates.Store(parentID, &turnState{sessionKey: parentID})
		t.Cleanup(func() { al.activeTurnStates.Delete(parentID) })

		delivery, err := deliverer.Deliver(context.Background(), u4ProgressEvent(t, childID, "p-live"))
		if err != nil {
			t.Fatalf("Deliver(progress, live parent): %v", err)
		}
		if delivery.Outcome != steer.DeliveryStoredNotWoken {
			t.Errorf("live-parent progress outcome = %q, want %q — PW-D4 forbids chatter joins", delivery.Outcome, steer.DeliveryStoredNotWoken)
		}
		al.steering.mu.Lock()
		items := append([]steeringQueueItem(nil), al.steering.queues[parentID]...)
		al.steering.mu.Unlock()
		if len(items) != 0 {
			t.Errorf("live steering queue after progress = %+v, want empty (PW-D4)", items)
		}
		msgs, _, _, err := inbox.Drain(parentID, childID, "", 10)
		if err != nil || len(msgs) != 1 || messageIDOf(msgs[0]) != "p-live" {
			t.Fatalf("stored progress = %+v (err=%v), want exactly p-live", msgs, err)
		}
	})

	t.Run("question joins with its identity", func(t *testing.T) {
		al, lifecycle, _, deliverer := newDeliverTestLoop(t)
		const parentID, childID = "parent-1", "child-1"
		seedParentAndChild(t, lifecycle, parentID, childID)
		seedUnifiedSession(t, al, parentID)
		al.activeTurnStates.Store(parentID, &turnState{sessionKey: parentID})
		t.Cleanup(func() { al.activeTurnStates.Delete(parentID) })

		delivery, err := deliverer.Deliver(context.Background(), u4QuestionEvent(t, childID, "q-live"))
		if err != nil {
			t.Fatalf("Deliver(question, live parent): %v", err)
		}
		if delivery.Outcome != steer.DeliveryQueuedIntoLiveTurn {
			t.Fatalf("live-parent question outcome = %q, want %q (PW-D4)", delivery.Outcome, steer.DeliveryQueuedIntoLiveTurn)
		}
		al.steering.mu.Lock()
		items := append([]steeringQueueItem(nil), al.steering.queues[parentID]...)
		al.steering.mu.Unlock()
		if len(items) != 1 || items[0].wake == nil || items[0].wake.messageID != "q-live" {
			t.Fatalf("live steering queue = %+v, want one identity-bearing q-live wake", items)
		}
	})
}

// PW-D4 (founder 2026-10-15): progress under the cap stores without waking.
// Keep the typed over-cap refusal: non-waking chatter is still rate-limited.
func TestDeliver_ProgressRateCap_RefusedAfterExpansion(t *testing.T) {
	al, lifecycle, _, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	seedUnifiedSession(t, al, parentID)
	ctx := context.Background()

	const rateCap = session.DefaultChildSendRatePerMinute // 10/min (C-LIMIT)
	for i := 0; i < rateCap; i++ {
		d, err := deliverer.Deliver(ctx, u4ProgressEvent(t, childID, fmt.Sprintf("p-%02d", i)))
		if err != nil {
			t.Fatalf("Deliver progress #%d (under cap): %v", i, err)
		}
		if d.Outcome != steer.DeliveryStoredNotWoken {
			t.Errorf("progress #%d under the rate cap: outcome = %q, want %q (PW-D4)",
				i, d.Outcome, steer.DeliveryStoredNotWoken)
		}
	}

	_, err := deliverer.Deliver(ctx, u4ProgressEvent(t, childID, "p-over"))
	if !errors.Is(err, session.ErrInboxRateLimited) {
		t.Fatalf("progress #%d (over the rate cap) error = %v, want ErrInboxRateLimited — the wake "+
			"expansion must not widen the preserved rate exemption (FR-012)", rateCap, err)
	}
}

// u4Generation reads a parent's current lifecycle generation.
func u4Generation(t *testing.T, lifecycle *session.LifecycleStore, sessionID string) int {
	t.Helper()
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		t.Fatalf("load %q: %v", sessionID, err)
	}
	return rec.Generation
}
