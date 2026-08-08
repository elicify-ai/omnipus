// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// White-box integration tests for the ADR-053 Phase 2 session-control-plane
// on-ramp (session_messaging_wire.go). Proves the plane is LIVE end-to-end at
// the plumbing level (the g7 round-trip): the consumer drains the bus's 4th
// channel and routes by kind — child->parent question → durable inbox Append +
// bounded typed wake; parent->child steer → DeliverSessionMessage → child
// steering queue — AND the kill switch (session_messaging.enabled=false)
// neuters it live. Lives in `package agent` to reach the unexported consumer +
// SetSessionMessagingStores directly.

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// seedLifecycleRecord persists a minimal LifecycleRecord so the consumer can
// resolve routing (agentID for steer; origin channel/chatID for wake).
func seedLifecycleRecord(t *testing.T, ls *session.LifecycleStore, rec *session.LifecycleRecord) {
	t.Helper()
	if err := ls.Persist(rec); err != nil {
		t.Fatalf("Persist lifecycle record %q: %v", rec.SessionID, err)
	}
}

// enableSessionMessaging flips the session_messaging kill switch ON on the
// loop's live config, explicitly, rather than relying on any default.
//
// Fix-wave finding #4 note: Enabled/WakeEnabled are now *bool (nil = apply
// EffectiveEnabled/EffectiveWakeEnabled's own default, matching production's
// documented fail-open posture — see sessionMessagingEnabledLive's "Nil
// config -> enabled" comment, session_messaging_wire.go). The minimal test
// configs newAsyncNotifierTestLoop builds leave the section at its Go zero
// value (nil, "operator never set this"), which therefore already resolves
// to enabled=true even without this helper. This function remains useful
// (and is still called by every positive-path test below) because it makes
// the precondition explicit and independent of whatever the default happens
// to be — the ONE test that actually depends on a specific value is the
// kill-switch test below, which sets Enabled: boolPtr(false) to positively
// prove the gate, not the zero value.
func enableSessionMessaging(al *AgentLoop) {
	cfg := al.GetConfig()
	cfg.SessionMessaging = config.SessionMessagingConfig{Enabled: boolPtr(true), WakeEnabled: boolPtr(true)}
}

// waitFor polls cond until it returns true or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

// TestSessionMessagingConsumer_ChildToParent_QuestionReachesInboxAndWakes is the
// keystone g7 round-trip at the plumbing level: publish a child->parent
// `question` SessionMessage on the bus → the kind-router consumer drains it →
// appends to the durable inbox (keyed to the parent owner key) AND fires the
// bounded typed wake (observable as an inbound bus message from the
// asyncNotifier). This proves the consumer + stores are wired live.
func TestSessionMessagingConsumer_ChildToParent_QuestionReachesInboxAndWakes(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)
	enableSessionMessaging(al)

	inbox := session.NewMessageInboxStore(t.TempDir())
	ls := session.NewLifecycleStore(t.TempDir())
	// Seed the PARENT's lifecycle record (owner key = "owner-chat-1") so the
	// wake's origin routing (channel/chatID) resolves.
	seedLifecycleRecord(t, ls, &session.LifecycleRecord{
		SessionID: "owner-chat-1",
		State:     session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeParentSession,
		AgentID:          "parent-agent",
		OriginChannel:    "testchan",
		OriginChatID:     "chat1",
		ParentDurableKey: "owner-chat-1",
	})
	// Seed the CHILD's lifecycle record so the consumer could resolve it.
	seedLifecycleRecord(t, ls, &session.LifecycleRecord{
		SessionID: "child-sess-1",
		State:     session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeParentSession,
		AgentID:          "child-agent",
		OriginChannel:    "testchan",
		OriginChatID:     "chat1",
		ParentDurableKey: "owner-chat-1",
	})

	// Wire the stores (the keystone injection) + start the consumer.
	al.SetSessionMessagingStores(inbox, ls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	al.StartSessionMessageConsumer(ctx)

	// Publish a child->parent question on the bus's 4th channel.
	var q generated.SessionMessage
	if err := q.FromSessionMessageQuestion(generated.SessionMessageQuestion{
		MessageId: "q-1", SessionId: "child-sess-1", CreatedAt: time.Now(),
		Text: "which file should I edit?", Wait: true, CorrelationId: "corr-1",
	}); err != nil {
		t.Fatalf("FromSessionMessageQuestion: %v", err)
	}
	if err := msgBus.PublishSessionMessage(context.Background(), bus.SessionMessageEvent{
		TargetSessionID: "owner-chat-1",
		Message:         q,
	}); err != nil {
		t.Fatalf("PublishSessionMessage: %v", err)
	}

	// Assert the message reached the durable inbox (consumer routed it).
	waitFor(t, 2*time.Second, func() bool {
		msgs, _, _, err := inbox.Drain("owner-chat-1", "child-sess-1", "", 10)
		return err == nil && len(msgs) == 1
	})
	msgs, _, _, _ := inbox.Drain("owner-chat-1", "child-sess-1", "", 10) //nolint:dogsled // Only messages are relevant from Drain's four returns.
	if len(msgs) != 1 {
		t.Fatalf("expected 1 inbox message after consumer drain, got %d", len(msgs))
	}

	// Assert the bounded typed wake fired — observable as an inbound bus
	// message published by asyncNotifier.Notify (channel "system").
	select {
	case <-msgBus.InboundChan():
		// wake fired — the message reached the inbox AND woke the parent.
	case <-time.After(2 * time.Second):
		t.Fatal("expected the question to fire a bounded typed wake (inbound bus message)")
	}
}

// TestSessionMessagingConsumer_Error_ReachesInboxAndWakes_M2 proves the
// silent-M2 fix: a kind:error SessionMessage (engine-emitted on a delegated
// child's behalf) used to fall to the unknown-kind default (DEBUG log + drop)
// because the consumer's child->parent and wakeable kind maps omitted it. With
// the fix, error reaches the durable inbox AND fires a bounded typed wake —
// the same routing a blocker gets.
func TestSessionMessagingConsumer_Error_ReachesInboxAndWakes_M2(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)
	enableSessionMessaging(al)

	inbox := session.NewMessageInboxStore(t.TempDir())
	ls := session.NewLifecycleStore(t.TempDir())
	seedLifecycleRecord(t, ls, &session.LifecycleRecord{
		SessionID: "owner-err", State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeParentSession,
		AgentID: "parent-agent", OriginChannel: "testchan", OriginChatID: "chat1",
		ParentDurableKey: "owner-err",
	})
	seedLifecycleRecord(t, ls, &session.LifecycleRecord{
		SessionID: "child-err", State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeParentSession,
		AgentID: "child-agent", OriginChannel: "testchan", OriginChatID: "chat1",
		ParentDurableKey: "owner-err",
	})
	al.SetSessionMessagingStores(inbox, ls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	al.StartSessionMessageConsumer(ctx)

	var em generated.SessionMessage
	if err := em.FromSessionMessageError(generated.SessionMessageError{
		MessageId: "e-1", SessionId: "child-err", CreatedAt: time.Now(),
		Text: "child hit a fatal error", Fatal: true,
	}); err != nil {
		t.Fatalf("FromSessionMessageError: %v", err)
	}
	if err := msgBus.PublishSessionMessage(context.Background(), bus.SessionMessageEvent{
		TargetSessionID: "owner-err", Message: em,
	}); err != nil {
		t.Fatalf("PublishSessionMessage: %v", err)
	}

	// Assert the error reached the durable inbox (previously dropped — silent-M2).
	waitFor(t, 2*time.Second, func() bool {
		msgs, _, _, err := inbox.Drain("owner-err", "child-err", "", 10)
		return err == nil && len(msgs) == 1
	})
	msgs, _, _, _ := inbox.Drain("owner-err", "child-err", "", 10) //nolint:dogsled // Only messages are relevant from Drain's four returns.
	if len(msgs) != 1 {
		t.Fatalf("expected 1 inbox message (error) after consumer drain, got %d", len(msgs))
	}

	// Assert the bounded typed wake fired (error is wakeable, async_notifier.go).
	select {
	case <-msgBus.InboundChan():
		// wake fired — the error reached the inbox AND woke the parent.
	case <-time.After(2 * time.Second):
		t.Fatal("expected the error kind to fire a bounded typed wake (inbound bus message)")
	}
}

// TestSessionMessagingConsumer_Handback_ReachesInbox proves the handback kind
// (the terminal/pause boundary — the rung-0 evidence gate) also flows through
// the consumer to the durable inbox.
func TestSessionMessagingConsumer_Handback_ReachesInbox(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)
	enableSessionMessaging(al)
	inbox := session.NewMessageInboxStore(t.TempDir())
	ls := session.NewLifecycleStore(t.TempDir())
	seedLifecycleRecord(t, ls, &session.LifecycleRecord{
		SessionID: "owner-hb",
		State:     session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeParentSession,
		AgentID:       "parent-hb",
		OriginChannel: "tc", OriginChatID: "c1",
		ParentDurableKey: "owner-hb",
	})
	al.SetSessionMessagingStores(inbox, ls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	al.StartSessionMessageConsumer(ctx)

	var hb generated.SessionMessage
	if err := hb.FromSessionMessageHandback(generated.SessionMessageHandback{
		MessageId: "hb-1", SessionId: "child-hb", CreatedAt: time.Now(),
		Mode: generated.SessionMessageHandbackMode("final"), ResultSoFar: "done, all tests pass",
	}); err != nil {
		t.Fatalf("FromSessionMessageHandback: %v", err)
	}
	if err := msgBus.PublishSessionMessage(context.Background(), bus.SessionMessageEvent{
		TargetSessionID: "owner-hb", Message: hb,
	}); err != nil {
		t.Fatalf("PublishSessionMessage: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		msgs, _, _, err := inbox.Drain("owner-hb", "child-hb", "", 10)
		return err == nil && len(msgs) == 1
	})
}

// TestSessionMessagingConsumer_Steer_LandsInChildSteeringQueue proves the
// parent->child path: publish a `steer` on the bus → consumer →
// DeliverSessionMessage → the text lands in the child's steering-queue scope,
// ready to inject at the child's next tool boundary.
func TestSessionMessagingConsumer_Steer_LandsInChildSteeringQueue(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)
	enableSessionMessaging(al)
	inbox := session.NewMessageInboxStore(t.TempDir())
	ls := session.NewLifecycleStore(t.TempDir())
	// Seed the child's lifecycle record so the consumer can resolve agentID.
	// sec-MAJOR-3: the record carries a ParentDurableKey — the consumer's
	// defense-in-depth gate now requires the target to be a genuine delegated
	// child (non-empty parent link) before it will inject a steer.
	seedLifecycleRecord(t, ls, &session.LifecycleRecord{
		SessionID:        "child-sess-2",
		State:            session.LifecycleRunning,
		OwnerScopeKind:   session.OwnerScopeParentSession,
		AgentID:          "child-agent-2",
		ParentDurableKey: "parent-of-child-2",
	})
	al.SetSessionMessagingStores(inbox, ls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	al.StartSessionMessageConsumer(ctx)

	var steer generated.SessionMessage
	if err := steer.FromSessionMessageSteer(generated.SessionMessageSteer{
		MessageId: "steer-1", SessionId: "child-sess-2", CreatedAt: time.Now(),
		Text: "also add a test for the edge case",
	}); err != nil {
		t.Fatalf("FromSessionMessageSteer: %v", err)
	}
	if err := msgBus.PublishSessionMessage(context.Background(), bus.SessionMessageEvent{
		TargetSessionID: "child-sess-2",
		Message:         steer,
	}); err != nil {
		t.Fatalf("PublishSessionMessage: %v", err)
	}

	// The consumer forms the scope "agent:child-agent-2:child-sess-2".
	scope := "agent:child-agent-2:child-sess-2"
	waitFor(t, 2*time.Second, func() bool {
		return al.pendingSteeringCountForScope(scope) == 1
	})
	drained := al.dequeueSteeringMessagesForScope(scope)
	if len(drained) != 1 || drained[0].Content != "also add a test for the edge case" {
		t.Fatalf("expected the steer text queued, got: %+v", drained)
	}
}

// TestSessionMessagingConsumer_KillSwitch_NoOpsWhenDisabled proves FR-196:
// session_messaging.enabled=false neuters the consumer live — it still drains
// the channel (no publisher block) but performs NO dispatch (inbox stays empty).
func TestSessionMessagingConsumer_KillSwitch_NoOpsWhenDisabled(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)
	// Flip the kill switch OFF on the live config.
	cfg := al.GetConfig()
	cfg.SessionMessaging = config.SessionMessagingConfig{Enabled: boolPtr(false)}

	inbox := session.NewMessageInboxStore(t.TempDir())
	ls := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, ls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	al.StartSessionMessageConsumer(ctx)

	var p generated.SessionMessage
	if err := p.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId: "p-1", SessionId: "child-sess-3", CreatedAt: time.Now(), Text: "working",
	}); err != nil {
		t.Fatalf("FromSessionMessageProgress: %v", err)
	}
	if err := msgBus.PublishSessionMessage(context.Background(), bus.SessionMessageEvent{
		TargetSessionID: "owner-3",
		Message:         p,
	}); err != nil {
		t.Fatalf("PublishSessionMessage: %v", err)
	}

	// Give the consumer a moment to drain + (not) dispatch.
	time.Sleep(300 * time.Millisecond)
	msgs, _, _, _ := inbox.Drain("owner-3", "child-sess-3", "", 10) //nolint:dogsled // Only messages are relevant from Drain's four returns.
	if len(msgs) != 0 {
		t.Fatalf("kill switch ON: expected 0 inbox messages, got %d (consumer must no-op when disabled)", len(msgs))
	}
}

// TestSessionMessagingConsumer_MessageParentDirectPath_ReachesInbox proves the
// DIRECT tool path is live: with the stores wired via SetSessionMessagingStores,
// the message_parent tool registered on an agent appends directly to the
// durable inbox on Execute (synchronous cap enforcement requires the direct
// Append — FR-125 never-silent-drop). This is the in-band complement to the
// bus-consumer path above.
func TestSessionMessagingConsumer_MessageParentDirectPath_ReachesInbox(t *testing.T) {
	al, _ := newAsyncNotifierTestLoop(t)
	// The sync message_parent path now honors the FR-196 kill switch (arch-M2):
	// enable the plane so the direct Execute reaches the inbox (previously this
	// was masked because the sync path bypassed the switch).
	enableSessionMessaging(al)
	inbox := session.NewMessageInboxStore(t.TempDir())
	ls := session.NewLifecycleStore(t.TempDir())
	seedLifecycleRecord(t, ls, &session.LifecycleRecord{
		SessionID: "child-direct-1",
		State:     session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeParentSession,
		AgentID:          "child-agent-d",
		ParentDurableKey: "owner-direct-1",
	})
	al.SetSessionMessagingStores(inbox, ls)

	// The default agent now has a LIVE message_parent tool (reconstructed with
	// the real stores by wireSessionMessagingForAgent).
	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent registered")
	}
	mpAny, ok := agent.Tools.Get("message_parent")
	if !ok {
		t.Fatal("message_parent tool not registered after SetSessionMessagingStores")
	}
	mp, ok := mpAny.(*tools.MessageParentTool)
	if !ok {
		t.Fatalf("message_parent tool is %T, want *tools.MessageParentTool", mpAny)
	}
	// Drive it: a progress message with the child's own durable session id
	// set in context. message_parent.go resolves the durable LifecycleRecord
	// under tools.ToolDelegateSessionID(ctx) (the child's OWN ADR-053
	// session_id — #576), not ToolTranscriptSessionID; set both to the same
	// seeded SessionID, mirroring pkg/tools/message_parent_test.go's
	// withChildContext.
	ctx := tools.WithDelegateSessionID(tools.WithTranscriptSessionID(context.Background(), "child-direct-1"), "child-direct-1")
	res := mp.Execute(ctx, map[string]any{
		"kind": "progress",
		"text": "direct-path progress line",
	})
	if res.IsError {
		t.Fatalf("message_parent Execute returned error: %s", res.ForLLM)
	}
	// The message must have reached the durable inbox directly.
	msgs, _, _, _ := inbox.Drain("owner-direct-1", "child-direct-1", "", 10) //nolint:dogsled // Only messages are relevant from Drain's four returns.
	if len(msgs) != 1 {
		t.Fatalf("expected 1 inbox message from direct Execute, got %d", len(msgs))
	}
}

// TestSessionMessagingConsumer_Steer_RejectsNonChild (sec-MAJOR-3) proves the
// sink-side parent↔child re-derivation: the consumer does NOT trust the
// envelope's TargetSessionID. A steer addressed to (a) a session with no
// lifecycle record and (b) a session whose record has no ParentDurableKey is
// rejected at the sink — nothing lands in any steering queue. This is the
// defense-in-depth mirror of the delegate tool's producer-side
// verifyCallerOwnsSession gate (tested in delegate_adr053_test.go); if a future
// second producer of a steer/respond bus event forgets that gate, the consumer
// still refuses to inject into a non-child.
func TestSessionMessagingConsumer_Steer_RejectsNonChild(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)
	enableSessionMessaging(al)
	inbox := session.NewMessageInboxStore(t.TempDir())
	ls := session.NewLifecycleStore(t.TempDir())
	// "no-parent-child" has a lifecycle record but NO ParentDurableKey — it is
	// not a delegated child, so a steer/respond has no business being injected.
	seedLifecycleRecord(t, ls, &session.LifecycleRecord{
		SessionID:      "no-parent-child",
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeParentSession,
		AgentID:        "orphan-agent",
		// ParentDurableKey intentionally empty.
	})
	// "ghost-child" has NO lifecycle record at all.
	al.SetSessionMessagingStores(inbox, ls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	al.StartSessionMessageConsumer(ctx)

	buildSteer := func(target, text string) {
		t.Helper()
		var steer generated.SessionMessage
		if err := steer.FromSessionMessageSteer(generated.SessionMessageSteer{
			MessageId: "steer-" + target, SessionId: target, CreatedAt: time.Now(),
			Text: text,
		}); err != nil {
			t.Fatalf("FromSessionMessageSteer: %v", err)
		}
		if err := msgBus.PublishSessionMessage(context.Background(), bus.SessionMessageEvent{
			TargetSessionID: target,
			Message:         steer,
		}); err != nil {
			t.Fatalf("PublishSessionMessage: %v", err)
		}
	}

	buildSteer("ghost-child", "hijack attempt 1")
	buildSteer("no-parent-child", "hijack attempt 2")

	// Give the consumer a moment to drain + (try to) dispatch both events.
	time.Sleep(400 * time.Millisecond)

	// Neither target may have received a steering message — the sink rejected
	// both (no record / no parent), so no scope was ever registered.
	for _, scope := range []string{
		"ghost-child",
		"agent:orphan-agent:no-parent-child",
		"no-parent-child",
	} {
		if al.pendingSteeringCountForScope(scope) != 0 {
			t.Errorf("sec-MAJOR-3 leak: scope %q has %d queued steering messages (consumer must reject a steer to a non-child)",
				scope, al.pendingSteeringCountForScope(scope))
		}
	}
}

// TestSessionMessagingConsumer_Egress_RedactsSecretAtSink (sec-MINOR-1) proves
// the sink-side N-10 content-egress filter: a child→parent message whose free
// text carries a registered secret is REDACTED before it lands in the durable
// inbox, regardless of which producer emitted the bus event. The message_parent
// producer is the primary filter; this sink-side pass is the defense-in-depth
// that catches a future second producer that forgets to filter.
func TestSessionMessagingConsumer_Egress_RedactsSecretAtSink(t *testing.T) {
	al, msgBus := newAsyncNotifierTestLoop(t)
	enableSessionMessaging(al)

	// Register a sensitive value with the live config so the
	// SensitiveDataReplacer redacts it to "[FILTERED]" (>3 chars qualifies),
	// and ENABLE filtering (the test config's Tools.FilterSensitiveData defaults
	// to false — IsFilterSensitiveDataEnabled() is the gate FilterSensitiveData
	// checks before touching the text).
	const secret = "sk-test-secret-key-abcde-12345"
	cfg := al.GetConfig()
	cfg.Tools.FilterSensitiveData = true
	cfg.RegisterSensitiveValues([]string{secret})

	inbox := session.NewMessageInboxStore(t.TempDir())
	ls := session.NewLifecycleStore(t.TempDir())
	seedLifecycleRecord(t, ls, &session.LifecycleRecord{
		SessionID: "owner-egress", State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeParentSession,
		AgentID: "parent-agent", OriginChannel: "tc", OriginChatID: "c1",
		ParentDurableKey: "owner-egress",
	})
	al.SetSessionMessagingStores(inbox, ls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	al.StartSessionMessageConsumer(ctx)

	// Publish a child→parent progress message whose Text carries the secret.
	var prog generated.SessionMessage
	if err := prog.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId: "p-sec", SessionId: "child-egress", CreatedAt: time.Now(),
		Text: "done, used key " + secret + " to auth",
	}); err != nil {
		t.Fatalf("FromSessionMessageProgress: %v", err)
	}
	if err := msgBus.PublishSessionMessage(context.Background(), bus.SessionMessageEvent{
		TargetSessionID: "owner-egress",
		Message:         prog,
	}); err != nil {
		t.Fatalf("PublishSessionMessage: %v", err)
	}

	// Assert the redacted message reached the durable inbox.
	waitFor(t, 2*time.Second, func() bool {
		msgs, _, _, err := inbox.Drain("owner-egress", "child-egress", "", 10)
		return err == nil && len(msgs) == 1
	})
	msgs, _, _, _ := inbox.Drain("owner-egress", "child-egress", "", 10) //nolint:dogsled // Only messages are relevant from Drain's four returns.
	if len(msgs) != 1 {
		t.Fatalf("expected 1 inbox message after consumer drain, got %d", len(msgs))
	}
	got, err := msgs[0].AsSessionMessageProgress()
	if err != nil {
		t.Fatalf("AsSessionMessageProgress: %v", err)
	}
	if strings.Contains(got.Text, secret) {
		t.Errorf("sec-MINOR-1 leak: inbox text still contains the raw secret: %q", got.Text)
	}
	if !strings.Contains(got.Text, "[FILTERED]") {
		t.Errorf("sec-MINOR-1: expected the secret to be redacted to [FILTERED], got text: %q", got.Text)
	}
}
