// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 WP-B — I-5's real steer.AudienceResolver body (landing order I-5,
// "ordinary_root -> user; steered -> steering session; every other class, a
// missing record for a session that should have one, or an error -> none").
// TDD plan test 1, TestAudience_ByClass.

package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// TestAudience_ByClass exercises every one of I-8's classes through the real
// AudienceResolver sitting on top of SteerRecordClassifier, proving each
// class maps onto the correct audience per landing order I-5.
func TestAudience_ByClass(t *testing.T) {
	lifecycle, unified := newTestClassifierStores(t)
	classifier := NewSteerRecordClassifier(lifecycle, unified)
	resolver := NewSteerAudienceResolver(classifier)
	ctx := context.Background()

	t.Run("ordinary_root_is_user", func(t *testing.T) {
		id := newMetaSession(t, unified, session.SessionTypeChat, "")
		audience, class, err := resolver.Audience(ctx, id)
		if err != nil {
			t.Fatalf("Audience: %v", err)
		}
		if class != steer.ClassOrdinaryRoot {
			t.Fatalf("class = %q, want ordinary_root", class)
		}
		if audience != steer.AudienceUser {
			t.Fatalf("Audience(ordinary_root) = %q, want user", audience)
		}
	})

	t.Run("steered_is_steering_session", func(t *testing.T) {
		parentID := newMetaSession(t, unified, session.SessionTypeChat, "")
		// Pre-existing fixture gap (found while verifying ADR-091 fix lane
		// 1, unrelated to findings A-F): the parent's own ordinary_root
		// record was never persisted here, so SteerRecordClassifier.
		// chainValid (steer_classify.go) could not resolve it as an
		// ancestor and classified the child invalid_edge instead of
		// steered — chainValid's own doc comment is explicit that "an
		// ancestor with no record... is an unknown ancestor — never
		// treated as a root by default", exactly I-1's rule that every
		// steering session has a lifecycle record. A real launchSteered
		// call always mints this atomically (landing order I-1, "the
		// steering session's first delegation"); this hand-built fixture
		// must too.
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID: parentID, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws-1", AgentID: "worker",
			Origin: &session.Origin{Kind: session.OriginKindChat},
		}); err != nil {
			t.Fatalf("Persist(parent ordinary_root): %v", err)
		}
		childID := newMetaSession(t, unified, session.SessionTypeDelegate, parentID)
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID: childID, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: parentID,
			WorkspaceID: "ws-1", AgentID: "worker",
			Origin: &session.Origin{Kind: session.OriginKindDelegate},
			SteeredBy: &session.SteeredBy{
				SteeringSessionID: parentID,
				RootSessionID:     parentID,
			},
		}); err != nil {
			t.Fatalf("Persist: %v", err)
		}
		audience, class, err := resolver.Audience(ctx, childID)
		if err != nil {
			t.Fatalf("Audience: %v", err)
		}
		if class != steer.ClassSteered {
			t.Fatalf("class = %q, want steered", class)
		}
		if audience != steer.AudienceSteeringSession {
			t.Fatalf("Audience(steered) = %q, want steering_session", audience)
		}
	})

	for _, tc := range []struct {
		name  string
		class steer.Class
	}{
		{"damaged_child", steer.ClassDamagedChild},
		{"legacy_delegate", steer.ClassLegacyDelegate},
		{"unreadable", steer.ClassUnreadable},
		{"invalid_edge", steer.ClassInvalidEdge},
	} {
		t.Run(tc.name+"_is_none", func(t *testing.T) {
			fake := &fakeAgentClassifier{class: tc.class}
			r := NewSteerAudienceResolver(fake)
			audience, class, err := r.Audience(ctx, "any-session")
			if err != nil {
				t.Fatalf("Audience: %v", err)
			}
			if class != tc.class {
				t.Fatalf("class = %q, want %q", class, tc.class)
			}
			if audience != steer.AudienceNone {
				t.Fatalf("Audience(%s) = %q, want none", tc.class, audience)
			}
		})
	}

	t.Run("classifier_error_is_none", func(t *testing.T) {
		fake := &fakeAgentClassifier{err: errors.New("boom")}
		r := NewSteerAudienceResolver(fake)
		audience, _, err := r.Audience(ctx, "any-session")
		if err == nil {
			t.Fatal("expected the classifier's error to propagate")
		}
		if audience != steer.AudienceNone {
			t.Fatalf("Audience(classifier error) = %q, want none", audience)
		}
	})
}

// fakeAgentClassifier is a minimal steer.RecordClassifier double for testing
// AudienceResolver in isolation from the real classifier's store I/O.
type fakeAgentClassifier struct {
	class steer.Class
	err   error
}

var _ steer.RecordClassifier = (*fakeAgentClassifier)(nil)

func (f *fakeAgentClassifier) Classify(context.Context, string) (steer.Class, error) {
	if f.err != nil {
		return steer.ClassUnreadable, f.err
	}
	return f.class, nil
}

// TestDeliver_ReDeliveryOfAnUnackedEntryStillWakes is ADR-091 fix lane 1's
// Finding C (CRITICAL): boot_sweep.go::unacknowledged reads a still-pending
// entry straight OUT of the inbox and hands it back to Deliver UNCHANGED
// (exactly what this test's second Deliver call does), so Append's own
// content-based dedup makes res.Deduped true BY CONSTRUCTION for every
// entry boot recovery can find. Before the fix, Deliver's short-circuit
// treated that as "already fully handled" and skipped the wake — silently
// making the boot re-nudge a total no-op. The fix: only short-circuit when
// the stored entry is genuinely ACKED. A worker that finished right before
// a restart must still wake its parent on the very next boot.
func TestDeliver_ReDeliveryOfAnUnackedEntryStillWakes(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, rootID, "call-redeliver")
	// A bare test root's PeerID is empty; give the child's reporting target
	// a routable address so the wake-eligibility contrast below is
	// genuinely observable (mirrors TestCompletion_IterationLimit_NonFatal_
	// DoesNotWakeParent's own setup in steer_completion_test.go).
	if err := al.GetSessionLifecycleStore().Mutate(child.SessionID, func(r *session.LifecycleRecord) error {
		r.SteeredBy.ReportingTarget = session.ReportingTarget{Channel: "webchat", ChatID: rootID}
		return nil
	}); err != nil {
		t.Fatalf("Mutate(reporting target): %v", err)
	}

	var wakes []string
	al.asyncNotifier.registerObserver(func(e AsyncNotifyEvent) {
		if strings.HasPrefix(e.SourceKind, "message_parent:") {
			wakes = append(wakes, e.SourceKind)
		}
	})

	deliverer := al.getUpwardDeliverer()
	var message generated.SessionMessage
	if err := message.FromSessionMessageHandback(generated.SessionMessageHandback{
		MessageId: child.SessionID, SessionId: child.SessionID, CreatedAt: time.Now().UTC(),
		Depth: 1, SenderIdentity: child.AgentID, Mode: generated.SessionMessageHandbackModeFinal,
		ResultSoFar: "first delivery", Artifacts: []string{}, OpenQuestions: []string{},
	}); err != nil {
		t.Fatalf("encode handback: %v", err)
	}

	// First delivery: a genuine first-time Append + wake.
	first, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: child.SessionID, Outcome: steer.OutcomeFinalAnswer, Message: message,
	})
	if err != nil {
		t.Fatalf("Deliver(first): %v", err)
	}
	if len(wakes) != 1 {
		t.Fatalf("first delivery wakes = %d, want 1", len(wakes))
	}

	// Simulate boot_sweep.go::unacknowledged + deliverIfUnconsumed exactly:
	// read the SAME still-unacked entry straight out of the inbox (the
	// gateway crashed before the wake landed, so it was never acked) and
	// hand it BACK to Deliver unchanged.
	msgs, _, _, err := al.GetMessageInboxStore().Drain(rootID, child.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain(unacked): %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("unacked messages = %d, want 1", len(msgs))
	}
	if _, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: child.SessionID, Outcome: steer.OutcomeFinalAnswer, Message: msgs[0],
	}); err != nil {
		t.Fatalf("Deliver(re-delivery, still unacked): %v", err)
	}
	if len(wakes) != 2 {
		t.Fatalf("re-delivery of a still-UNACKED entry produced %d total wakes, want 2 — "+
			"the boot re-nudge must fire, not be silently absorbed by Append's own content dedup", len(wakes))
	}

	// Once genuinely ACKED, a further re-delivery of the identical content
	// must stay silent — the short-circuit is legitimate for a truly
	// consumed entry (this is the half of Finding C's fix that must NOT
	// regress into at-least-once wakes for every ordinary duplicate).
	if err := al.GetMessageInboxStore().Ack(rootID, []string{first.MessageID}); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if _, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: child.SessionID, Outcome: steer.OutcomeFinalAnswer, Message: msgs[0],
	}); err != nil {
		t.Fatalf("Deliver(re-delivery, acked): %v", err)
	}
	if len(wakes) != 2 {
		t.Fatalf("re-delivery of an ALREADY-ACKED entry produced %d total wakes, want still 2 "+
			"(a genuinely consumed entry must stay short-circuited)", len(wakes))
	}
}
