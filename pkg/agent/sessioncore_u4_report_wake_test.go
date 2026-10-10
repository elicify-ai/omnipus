// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session-core U4 — helper report class, wake/retain/queue behaviour
// (FR-012, BDD-04.1/04.2, T06/T16).
//
// RED pack, qa-lead. Oracle source: docs/internal/specs/session-core-spec.md
// FR-012 and BDD-04.1/04.2, read from the spec text — never from the
// implementation. The spec: every accepted helper report kind (progress,
// checkpoint, artifact, question, blocker, handback-final, engine lifecycle)
// wakes an idle non-stopped parent, joins a live parent's safe boundary, or is
// retained in a stopped parent's inbox without revival; and the wake expansion
// must not widen the preserved rate/count exemptions.

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
// newly-wake-eligible accepted kind (field shapes mirror the generated structs
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

// TestDeliver_AcceptedReportKinds_WakeIdleNonStoppedParent covers FR-012 /
// BDD-04.1: an accepted report kind delivered to an idle, non-stopped parent
// wakes it (DeliveryWoke) and is stored once in the parent's inbox.
//
// RED today: progress/checkpoint/artifact are not wake-eligible
// (session.classifyEnvelope), so every one of them returns
// DeliveryStoredNotWoken and the idle parent never learns of the report.
func TestDeliver_AcceptedReportKinds_WakeIdleNonStoppedParent(t *testing.T) {
	t.Run("progress", func(t *testing.T) {
		_, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
		const parentID, childID = "parent-1", "child-1"
		seedParentAndChild(t, lifecycle, parentID, childID)

		delivery, err := deliverer.Deliver(context.Background(), u4ProgressEvent(t, childID, "p-1"))
		if err != nil {
			t.Fatalf("Deliver(progress): %v", err)
		}
		if delivery.Outcome != steer.DeliveryWoke {
			t.Fatalf("progress delivery outcome = %q, want %q — FR-012 requires an accepted report to "+
				"wake an idle non-stopped parent (BDD-04.1)", delivery.Outcome, steer.DeliveryWoke)
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
		_, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
		const parentID, childID = "parent-1", "child-1"
		seedParentAndChild(t, lifecycle, parentID, childID)

		event := steer.UpwardEvent{
			ChildSessionID: childID,
			Outcome:        steer.OutcomeCheckpoint,
			Message:        u4GeneratedCheckpoint(t, childID, "c-1"),
		}
		delivery, err := deliverer.Deliver(context.Background(), event)
		if err != nil {
			t.Fatalf("Deliver(checkpoint): %v", err)
		}
		if delivery.Outcome != steer.DeliveryWoke {
			t.Fatalf("checkpoint delivery outcome = %q, want %q — FR-012/BDD-04.1", delivery.Outcome, steer.DeliveryWoke)
		}
		msgs, _, _, derr := inbox.Drain(parentID, childID, "", 10)
		if derr != nil || len(msgs) != 1 {
			t.Fatalf("parent inbox checkpoint entries = %d (err=%v), want exactly 1", len(msgs), derr)
		}
	})

	t.Run("artifact", func(t *testing.T) {
		_, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
		const parentID, childID = "parent-1", "child-1"
		seedParentAndChild(t, lifecycle, parentID, childID)

		event := steer.UpwardEvent{
			ChildSessionID: childID,
			Outcome:        steer.OutcomeCheckpoint,
			Message:        u4GeneratedArtifact(t, childID, "a-1"),
		}
		delivery, err := deliverer.Deliver(context.Background(), event)
		if err != nil {
			t.Fatalf("Deliver(artifact): %v", err)
		}
		if delivery.Outcome != steer.DeliveryWoke {
			t.Fatalf("artifact delivery outcome = %q, want %q — FR-012/BDD-04.1", delivery.Outcome, steer.DeliveryWoke)
		}
		msgs, _, _, derr := inbox.Drain(parentID, childID, "", 10)
		if derr != nil || len(msgs) != 1 {
			t.Fatalf("parent inbox artifact entries = %d (err=%v), want exactly 1", len(msgs), derr)
		}
	})
}

// TestDeliver_AcceptedReportKind_WakesIdleThenRetainedWhenParentStopped covers
// FR-012 / BDD-04.2: the same accepted kind wakes an idle parent, but once the
// parent is stopped the report is retained in its inbox with no
// report-triggered dispatch and no revival; a duplicate replay adds no copy.
//
// RED today: the idle-parent half fails (progress is not wake-eligible), so the
// test names the idle wake as its first assertion.
func TestDeliver_AcceptedReportKind_WakesIdleThenRetainedWhenParentStopped(t *testing.T) {
	_, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	ctx := context.Background()

	// 1. Idle, non-stopped parent: the report wakes it (RED).
	idle, err := deliverer.Deliver(ctx, u4ProgressEvent(t, childID, "p-idle"))
	if err != nil {
		t.Fatalf("Deliver(progress, idle parent): %v", err)
	}
	if idle.Outcome != steer.DeliveryWoke {
		t.Fatalf("idle-parent progress outcome = %q, want %q (FR-012/BDD-04.1)", idle.Outcome, steer.DeliveryWoke)
	}

	// 2. Parent stops (current generation). A new report must be retained, not
	// woken, and must not revive the parent.
	if err := lifecycle.Mutate(parentID, func(rec *session.LifecycleRecord) error {
		rec.Stop = &session.Stop{
			At: time.Now().UTC(), Generation: rec.Generation,
			By: session.Principal{Kind: session.PrincipalKindHuman, ID: "dan"},
		}
		return nil
	}); err != nil {
		t.Fatalf("stamp Stop on parent: %v", err)
	}
	genBefore := u4Generation(t, lifecycle, parentID)

	stopped, err := deliverer.Deliver(ctx, u4ProgressEvent(t, childID, "p-stopped"))
	if err != nil {
		t.Fatalf("Deliver(progress, stopped parent): %v", err)
	}
	if stopped.Outcome != steer.DeliveryStoredNotWoken {
		t.Fatalf("stopped-parent progress outcome = %q, want %q — a stopped parent must not be revived "+
			"by a report (FR-012/BDD-04.2)", stopped.Outcome, steer.DeliveryStoredNotWoken)
	}
	if got := u4Generation(t, lifecycle, parentID); got != genBefore {
		t.Fatalf("parent generation after a report to a stopped parent = %d, want %d (no revival)",
			got, genBefore)
	}

	// 3. Duplicate replay of the retained report adds no copy.
	if _, err := deliverer.Deliver(ctx, u4ProgressEvent(t, childID, "p-stopped")); err != nil {
		t.Fatalf("Deliver(progress replay): %v", err)
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

// TestDeliver_ProgressToLiveParent_QueuesIntoLiveTurn covers FR-012's "join
// live safe boundary": a report delivered while the parent has a live turn is
// enqueued into that turn (DeliveryQueuedIntoLiveTurn), never a second turn.
//
// RED today: progress is not wake-eligible, so it never reaches the live-turn
// path and returns DeliveryStoredNotWoken with an empty steering queue.
func TestDeliver_ProgressToLiveParent_QueuesIntoLiveTurn(t *testing.T) {
	al, lifecycle, _, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	al.activeTurnStates.Store(parentID, &turnState{sessionKey: parentID})
	t.Cleanup(func() { al.activeTurnStates.Delete(parentID) })

	delivery, err := deliverer.Deliver(context.Background(), u4ProgressEvent(t, childID, "p-live"))
	if err != nil {
		t.Fatalf("Deliver(progress, live parent): %v", err)
	}
	if delivery.Outcome != steer.DeliveryQueuedIntoLiveTurn {
		t.Fatalf("live-parent progress outcome = %q, want %q — an accepted report must join the live "+
			"safe boundary, not start a second turn (FR-012)", delivery.Outcome, steer.DeliveryQueuedIntoLiveTurn)
	}

	al.steering.mu.Lock()
	items := append([]steeringQueueItem(nil), al.steering.queues[parentID]...)
	al.steering.mu.Unlock()
	if len(items) != 1 || items[0].wake == nil {
		t.Fatalf("live steering queue = %+v, want one identity-bearing wake for the report", items)
	}
}

// TestDeliver_ProgressRateCap_RefusedAfterExpansion covers FR-012's
// "expanded wake eligibility cannot widen preserved rate/count exemptions" at
// the delivery boundary: a progress report under the rate cap wakes the parent
// (RED today) AND the over-cap report is still refused with the typed rate
// error — i.e. making progress wake-eligible must not silently exempt it from
// the rate window.
func TestDeliver_ProgressRateCap_RefusedAfterExpansion(t *testing.T) {
	_, lifecycle, _, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	ctx := context.Background()

	const cap = session.DefaultChildSendRatePerMinute // 10/min (C-LIMIT)
	for i := 0; i < cap; i++ {
		d, err := deliverer.Deliver(ctx, u4ProgressEvent(t, childID, fmt.Sprintf("p-%02d", i)))
		if err != nil {
			t.Fatalf("Deliver progress #%d (under cap): %v", i, err)
		}
		if d.Outcome != steer.DeliveryWoke {
			t.Fatalf("progress #%d under the rate cap: outcome = %q, want %q (FR-012 idle wake)",
				i, d.Outcome, steer.DeliveryWoke)
		}
	}

	_, err := deliverer.Deliver(ctx, u4ProgressEvent(t, childID, "p-over"))
	if !errors.Is(err, session.ErrInboxRateLimited) {
		t.Fatalf("progress #%d (over the rate cap) error = %v, want ErrInboxRateLimited — the wake "+
			"expansion must not widen the preserved rate exemption (FR-012)", cap, err)
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
