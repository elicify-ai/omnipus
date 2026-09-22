// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 WP-B — I-5's real steer.UpwardDeliverer body: the durable
// inbox-then-wake path pkg/tools/message_parent.go already runs, made the
// only upward path. TDD plan tests 7, 9, 15, 18, 27.

package agent

import (
	"context"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// newDeliverTestLoop builds a real AgentLoop wired with the session-messaging
// stores (lifecycle + inbox) and a real SteerUpwardDeliverer, mirroring
// gateway_boot.go's own wiring sequence (SetSessionMessagingStores then
// SetSteerAudienceDeps).
func newDeliverTestLoop(t *testing.T) (*AgentLoop, *session.LifecycleStore, *session.MessageInboxStore, *SteerUpwardDeliverer) {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "worker"}, {ID: "parent-agent"}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &toolCallProvider{finalResp: "ok"})
	t.Cleanup(al.Close)

	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)

	deliverer := NewSteerUpwardDeliverer()
	al.SetSteerAudienceDeps(NewSteerAudienceResolver(NewSteerRecordClassifier(lifecycle, al.GetSessionStore())), steer.NopBoundaryObserver{}, deliverer)
	return al, lifecycle, inbox, deliverer
}

// seedParentAndChild persists a parent lifecycle record (ordinary root, own
// webchat address) and a child steered by it, returning both ids.
func seedParentAndChild(t *testing.T, lifecycle *session.LifecycleStore, parentID, childID string) {
	t.Helper()
	if err := lifecycle.Persist(&session.LifecycleRecord{
		SessionID: parentID, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws-1", AgentID: "parent-agent",
		OriginChannel: "webchat", OriginChatID: parentID,
		Origin: &session.Origin{Kind: session.OriginKindChat},
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	if err := lifecycle.Persist(&session.LifecycleRecord{
		SessionID: childID, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: parentID,
		WorkspaceID: "ws-1", AgentID: "worker",
		Origin:     &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-1"},
		Generation: 1,
		SteeredBy: &session.SteeredBy{
			SteeringSessionID: parentID,
			RootSessionID:     parentID,
			ReportingTarget: session.ReportingTarget{
				Channel: "webchat", ChatID: parentID,
			},
		},
	}); err != nil {
		t.Fatalf("seed child: %v", err)
	}
}

// TestDeliver_PersistsSubagentMessage_ProgressReachesParentTranscript covers
// ADR-091 D7/I-4 (US-3/AS-2): a progress report persists a subagent_message
// event into the PARENT's own transcript, readable back via the store —
// the mechanism the existing since-cursor replay (websocket_replay.go)
// returns after a reload with no new store.
func TestDeliver_PersistsSubagentMessage_ProgressReachesParentTranscript(t *testing.T) {
	al, lifecycle, _, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	seedUnifiedSession(t, al, parentID)

	var sm generated.SessionMessage
	if err := sm.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId: "p-1", SessionId: childID, CreatedAt: time.Now(), Depth: 1,
		SenderIdentity: "worker", Text: "halfway there",
	}); err != nil {
		t.Fatalf("FromSessionMessageProgress: %v", err)
	}

	if _, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: childID, Outcome: steer.OutcomeProgress, Message: sm,
	}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	entries, err := al.GetSessionStore().ReadTranscript(parentID)
	if err != nil {
		t.Fatalf("ReadTranscript(parent): %v", err)
	}
	var found *session.TranscriptEntry
	for i := range entries {
		if entries[i].SystemSubtype == session.SystemSubtypeSubagentMessage {
			found = &entries[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected a subagent_message entry in the parent's transcript, got %d entries", len(entries))
	}
	if found.SubagentMessage == nil {
		t.Fatal("expected the persisted entry to carry a SubagentMessage frame")
	}
	if found.SubagentMessage.Kind != "progress" {
		t.Fatalf("SubagentMessage.Kind = %q, want progress", found.SubagentMessage.Kind)
	}
	if found.SubagentMessage.SessionId != childID {
		t.Fatalf("SubagentMessage.SessionId = %q, want the CHILD's own id %q", found.SubagentMessage.SessionId, childID)
	}
}

// TestDeliver_PersistsSubagentState_TerminalOutcome covers ADR-091 D7/I-4
// (US-3/AS-2): a terminal outcome (a completed handback) persists a
// subagent_state(completed) event into the PARENT's own transcript.
func TestDeliver_PersistsSubagentState_TerminalOutcome(t *testing.T) {
	al, lifecycle, _, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	seedUnifiedSession(t, al, parentID)

	if _, err := deliverer.Deliver(context.Background(), handbackEvent(childID, "whatever")); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	entries, err := al.GetSessionStore().ReadTranscript(parentID)
	if err != nil {
		t.Fatalf("ReadTranscript(parent): %v", err)
	}
	var found *session.TranscriptEntry
	for i := range entries {
		if entries[i].SystemSubtype == session.SystemSubtypeSubagentState {
			found = &entries[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected a subagent_state entry in the parent's transcript, got %d entries", len(entries))
	}
	if found.SubagentState == nil || found.SubagentState.State != "completed" {
		t.Fatalf("expected SubagentState.State = completed, got %+v", found.SubagentState)
	}
}

// seedUnifiedSession creates a real *session.UnifiedStore session at the
// given id — AppendTranscriptStrict (and therefore
// deliverSubagentMessage/State, steer_frames.go) requires the session to
// already exist (a real production invariant: the parent, by definition,
// already has a live chat session by the time it ever delegates).
func seedUnifiedSession(t *testing.T, al *AgentLoop, id string) {
	t.Helper()
	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("seedUnifiedSession: no session store configured")
	}
	root, err := store.NewSession(session.SessionTypeChat, "webchat", "parent-agent")
	if err != nil {
		t.Fatalf("seedUnifiedSession(%q): create root: %v", id, err)
	}
	if _, err := store.CreateSessionWithID(id, root.ID, session.SessionTypeChat, "webchat", "parent-agent"); err != nil {
		t.Fatalf("seedUnifiedSession(%q): %v", id, err)
	}
}

func handbackEvent(childID, messageID string) steer.UpwardEvent {
	var sm generated.SessionMessage
	_ = sm.FromSessionMessageHandback(generated.SessionMessageHandback{
		MessageId: messageID, SessionId: childID, CreatedAt: time.Now(), Depth: 1,
		SenderIdentity: "worker", Mode: generated.SessionMessageHandbackModeFinal,
		ResultSoFar: "the answer", Artifacts: []string{}, OpenQuestions: []string{},
	})
	return steer.UpwardEvent{ChildSessionID: childID, Outcome: steer.OutcomeFinalAnswer, Message: sm}
}

// TestDeliver_HandbackOnePerChild_Identity covers US-2/AS-1,2 (TDD plan
// test 7): a handback lands in the parent's inbox with the deterministic
// terminal id, and the parent is woken once with its OWN identity.
func TestDeliver_HandbackOnePerChild_Identity(t *testing.T) {
	al, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)

	delivery, err := deliverer.Deliver(context.Background(), handbackEvent(childID, "caller-supplied-id"))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if delivery.Outcome != steer.DeliveryWoke {
		t.Fatalf("Delivery.Outcome = %q, want woke", delivery.Outcome)
	}
	wantID := "child-1:1:final"
	if delivery.MessageID != wantID {
		t.Fatalf("Delivery.MessageID = %q, want %q (deterministic terminal id)", delivery.MessageID, wantID)
	}

	msgs, _, _, derr := inbox.Drain(parentID, childID, "", 10)
	if derr != nil {
		t.Fatalf("Drain: %v", derr)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected exactly 1 handback in the parent's inbox, got %d", len(msgs))
	}
	kind, _ := msgs[0].Discriminator()
	if kind != "handback" {
		t.Fatalf("kind = %q, want handback", kind)
	}
	hb, herr := msgs[0].AsSessionMessageHandback()
	if herr != nil {
		t.Fatalf("AsSessionMessageHandback: %v", herr)
	}
	if hb.MessageId != wantID {
		t.Fatalf("stored MessageId = %q, want the deterministic id %q (not the caller-supplied one)", hb.MessageId, wantID)
	}
	_ = al // silence unused if go vet flags it in a future edit
}

// TestDeliver_RepeatWake_SameDeterministicID_OneEntry covers the crash-repair
// half of US-2/AS-5: redelivering the same completion after a recreated
// terminal outcome lands as the SAME entry (dedup by the deterministic id),
// never a second one.
func TestDeliver_RepeatWake_SameDeterministicID_OneEntry(t *testing.T) {
	al, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	_ = al

	ctx := context.Background()
	if _, err := deliverer.Deliver(ctx, handbackEvent(childID, "first-attempt")); err != nil {
		t.Fatalf("first Deliver: %v", err)
	}
	second, err := deliverer.Deliver(ctx, handbackEvent(childID, "second-attempt-different-caller-id"))
	if err != nil {
		t.Fatalf("second Deliver (repair): %v", err)
	}
	if second.Outcome != steer.DeliveryStoredNotWoken {
		t.Fatalf("duplicate Delivery.Outcome = %q, want stored_not_woken with no repeated effects", second.Outcome)
	}

	msgs, _, _, err := inbox.Drain(parentID, childID, "", 10)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected exactly 1 entry after a repeated terminal delivery, got %d", len(msgs))
	}
}

func TestDeliver_LiveParentUsesRawSessionKeyAndRetainsMessageIdentity(t *testing.T) {
	al, lifecycle, _, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	al.activeTurnStates.Store(parentID, &turnState{sessionKey: parentID})

	delivery, err := deliverer.Deliver(context.Background(), handbackEvent(childID, "ignored"))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if delivery.Outcome != steer.DeliveryQueuedIntoLiveTurn {
		t.Fatalf("Delivery.Outcome = %q, want queued_into_live_turn", delivery.Outcome)
	}

	al.steering.mu.Lock()
	items := append([]steeringQueueItem(nil), al.steering.queues[parentID]...)
	al.steering.mu.Unlock()
	if len(items) != 1 || items[0].wake == nil {
		t.Fatalf("live steering queue = %+v, want one identity-bearing wake", items)
	}
	if items[0].wake.messageID != "child-1:1:final" || items[0].wake.transcriptSessionID != parentID {
		t.Fatalf("wake identity = %+v, want message child-1:1:final in transcript %s", items[0].wake, parentID)
	}
}

func TestDeliver_IdleWakeFailureRemainsStoredNotWoken(t *testing.T) {
	_, lifecycle, _, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	if err := lifecycle.Mutate(childID, func(rec *session.LifecycleRecord) error {
		rec.SteeredBy.ReportingTarget = session.ReportingTarget{}
		return nil
	}); err != nil {
		t.Fatalf("clear reporting target: %v", err)
	}

	delivery, err := deliverer.Deliver(context.Background(), handbackEvent(childID, "ignored"))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if delivery.Outcome != steer.DeliveryStoredNotWoken {
		t.Fatalf("Delivery.Outcome = %q, want stored_not_woken after refused wake", delivery.Outcome)
	}
}

// TestDeliver_EmptyAnswer_FailedWithError covers US-2/AS-1b (TDD plan test
// 27): an empty final answer is delivered as an error, never a handback.
func TestDeliver_EmptyAnswer_FailedWithError(t *testing.T) {
	al, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	_ = al

	var sm generated.SessionMessage
	if err := sm.FromSessionMessageError(generated.SessionMessageError{
		MessageId: "whatever", SessionId: childID, CreatedAt: time.Now(), Depth: 1,
		SenderIdentity: "worker", Fatal: true, Text: "empty_answer: the turn produced no content",
	}); err != nil {
		t.Fatalf("FromSessionMessageError: %v", err)
	}

	delivery, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: childID, Outcome: steer.OutcomeEmptyAnswer, Message: sm,
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if delivery.Outcome != steer.DeliveryWoke {
		t.Fatalf("Delivery.Outcome = %q, want woke", delivery.Outcome)
	}

	msgs, _, _, derr := inbox.Drain(parentID, childID, "", 10)
	if derr != nil || len(msgs) != 1 {
		t.Fatalf("expected exactly 1 entry, got %d (err=%v)", len(msgs), derr)
	}
	kind, _ := msgs[0].Discriminator()
	if kind != "error" {
		t.Fatalf("kind = %q, want error (never handback) for an empty answer", kind)
	}
	e, eerr := msgs[0].AsSessionMessageError()
	if eerr != nil {
		t.Fatalf("AsSessionMessageError: %v", eerr)
	}
	if !e.Fatal {
		t.Error("expected the empty-answer error to be fatal")
	}
	if got := e.Text; len(got) < 13 || got[:13] != "empty_answer:" {
		t.Errorf("expected the error text to begin with 'empty_answer:', got %q", got)
	}
}

// TestDeliver_ProgressStoredNotWoken covers US-2/AS-10 (TDD plan test 15):
// progress is stored but never wakes the parent.
func TestDeliver_ProgressStoredNotWoken(t *testing.T) {
	al, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	_ = al

	var sm generated.SessionMessage
	if err := sm.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId: "p-1", SessionId: childID, CreatedAt: time.Now(), Depth: 1,
		SenderIdentity: "worker", Text: "working on it",
	}); err != nil {
		t.Fatalf("FromSessionMessageProgress: %v", err)
	}

	delivery, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: childID, Outcome: steer.OutcomeProgress, Message: sm,
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if delivery.Outcome != steer.DeliveryStoredNotWoken {
		t.Fatalf("Delivery.Outcome = %q, want stored_not_woken", delivery.Outcome)
	}
	msgs, _, _, derr := inbox.Drain(parentID, childID, "", 10)
	if derr != nil || len(msgs) != 1 {
		t.Fatalf("expected exactly 1 stored progress entry, got %d (err=%v)", len(msgs), derr)
	}
}

// TestDeliver_StoppedRecipientNotWoken covers US-2/AS-13 (TDD plan test 18):
// a recipient carrying a Stop marker for its current generation is not
// woken; the entry is stored for acknowledgement at revival.
func TestDeliver_StoppedRecipientNotWoken(t *testing.T) {
	al, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	_ = al

	if err := lifecycle.Mutate(parentID, func(rec *session.LifecycleRecord) error {
		rec.Stop = &session.Stop{At: time.Now(), Generation: rec.Generation, By: session.Principal{Kind: session.PrincipalKindHuman, ID: "dan"}}
		return nil
	}); err != nil {
		t.Fatalf("stamp Stop on parent: %v", err)
	}

	delivery, err := deliverer.Deliver(context.Background(), handbackEvent(childID, "whatever"))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if delivery.Outcome != steer.DeliveryStoredNotWoken {
		t.Fatalf("Delivery.Outcome = %q, want stored_not_woken (recipient is stopped)", delivery.Outcome)
	}
	msgs, _, _, derr := inbox.Drain(parentID, childID, "", 10)
	if derr != nil || len(msgs) != 1 {
		t.Fatalf("expected the entry to still be stored, got %d (err=%v)", len(msgs), derr)
	}
}

// TestDeliver_UndeliverableSurfaced covers the "steering session deleted"
// edge case: the entry is still persisted (using the child's own edge as
// the owner key), and Deliver does not error — it is best-effort from here.
func TestDeliver_UndeliverableSurfaced(t *testing.T) {
	al, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	// Only the CHILD gets a record — the parent's own record is missing,
	// simulating "steering session deleted while the child was running".
	if err := lifecycle.Persist(&session.LifecycleRecord{
		SessionID: childID, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: parentID,
		WorkspaceID: "ws-1", AgentID: "worker",
		Origin: &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-1"}, Generation: 1,
		SteeredBy: &session.SteeredBy{SteeringSessionID: parentID, RootSessionID: parentID},
	}); err != nil {
		t.Fatalf("seed child: %v", err)
	}
	_ = al

	delivery, err := deliverer.Deliver(context.Background(), handbackEvent(childID, "whatever"))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if delivery.Outcome != steer.DeliveryStoredNotWoken {
		t.Fatalf("Delivery.Outcome = %q, want stored_not_woken (parent record missing)", delivery.Outcome)
	}
	msgs, _, _, derr := inbox.Drain(parentID, childID, "", 10)
	if derr != nil || len(msgs) != 1 {
		t.Fatalf("expected the entry to still be persisted under the child's own edge, got %d (err=%v)", len(msgs), derr)
	}
}
