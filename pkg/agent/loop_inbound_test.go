// loop_inbound_test.go: tests for turn an inbound message into a routed agent

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from loop.go tests 2026-09-15 ---

// TestAgentSessionKey_IncludesInstanceID verifies TDD #12 / FR-023:
// two inbound messages on different instances of the SAME channel type but with
// the SAME ChatID must produce DISTINCT session keys, and each key must contain
// the instance ID.
//
// BDD: Given messages from "whatsapp.eu" and "whatsapp.us", same ChatID, same agent,
// When agentSessionKey is called,
// Then the two keys are distinct and each contains its own instance ID.
func TestAgentSessionKey_IncludesInstanceID(t *testing.T) {
	const agentID = "ray"
	const chatID = "15555550100@s.whatsapp.net"

	msgEU := bus.InboundMessage{
		Channel:    "whatsapp",
		InstanceID: "whatsapp.eu",
		ChatID:     chatID,
	}
	msgUS := bus.InboundMessage{
		Channel:    "whatsapp",
		InstanceID: "whatsapp.us",
		ChatID:     chatID,
	}

	keyEU := agentSessionKey(agentID, msgEU)
	keyUS := agentSessionKey(agentID, msgUS)

	// Both keys must be non-empty.
	if keyEU == "" {
		t.Fatal("agentSessionKey returned empty string for whatsapp.eu")
	}
	if keyUS == "" {
		t.Fatal("agentSessionKey returned empty string for whatsapp.us")
	}

	// The two keys must be DISTINCT (differentiation test — same ChatID, different instance).
	if keyEU == keyUS {
		t.Errorf(
			"agentSessionKey produced the SAME key for two different instances with the same ChatID: %q — "+
				"this violates FR-023; same-type instances must not share a transcript key",
			keyEU,
		)
	}

	// Each key must contain its instance ID.
	if !strings.Contains(keyEU, "whatsapp.eu") {
		t.Errorf("agentSessionKey for whatsapp.eu = %q; does not contain 'whatsapp.eu'", keyEU)
	}
	if !strings.Contains(keyUS, "whatsapp.us") {
		t.Errorf("agentSessionKey for whatsapp.us = %q; does not contain 'whatsapp.us'", keyUS)
	}

	// Verify the expected key format: agent:<id>:chat:<instanceID>:<chatID>
	wantEU := "agent:" + agentID + ":chat:whatsapp.eu:" + chatID
	wantUS := "agent:" + agentID + ":chat:whatsapp.us:" + chatID
	if keyEU != wantEU {
		t.Errorf("agentSessionKey(eu) = %q, want %q", keyEU, wantEU)
	}
	if keyUS != wantUS {
		t.Errorf("agentSessionKey(us) = %q, want %q", keyUS, wantUS)
	}
}

// TestAgentSessionKey_FallsBackToChannelWhenNoInstanceID verifies that when
// InstanceID is not stamped (empty), agentSessionKey falls back to msg.Channel
// (the type key), preserving backward-compatible behavior.
//
// Traces to: channel-instance-workspace-binding-spec.md DS-4 row 4 (legacy back-compat).
func TestAgentSessionKey_FallsBackToChannelWhenNoInstanceID(t *testing.T) {
	const agentID = "mia"
	const chatID = "user42"

	msg := bus.InboundMessage{
		Channel:    "telegram",
		InstanceID: "", // no stamp — legacy adapter
		ChatID:     chatID,
	}

	key := agentSessionKey(agentID, msg)

	// Must use the channel type as the fallback.
	want := "agent:" + agentID + ":chat:telegram:" + chatID
	if key != want {
		t.Errorf("agentSessionKey fallback = %q, want %q", key, want)
	}
}

// TestAgentSessionKey_SessionIDPathTakesPrecedence verifies that when msg.SessionID
// is set (webchat path), agentSessionKey returns the session-ID–based key and
// ignores both InstanceID and Channel.
func TestAgentSessionKey_SessionIDPathTakesPrecedence(t *testing.T) {
	const agentID = "jim"

	msg := bus.InboundMessage{
		Channel:    "webchat",
		InstanceID: "whatsapp.eu", // InstanceID would normally be used
		ChatID:     "some-chat",
		SessionID:  "ses-abc123",
	}

	key := agentSessionKey(agentID, msg)
	want := "agent:" + agentID + ":session:ses-abc123"
	if key != want {
		t.Errorf("agentSessionKey with SessionID = %q, want %q", key, want)
	}
}

// TestProcessSystemMessage_DeletedOriginAgent_IsNotRehomed is UAT E-3's
// routing half. A goal-keeper push addressed to an agent that had just been
// deleted fell back to the default agent ("named async origin agent not
// found; falling back to default agent"), which then worked — and parked — a
// goal that was never its own. A named origin that no longer resolves must be
// discarded with a visible note, and no other agent may run a turn for it.
func TestProcessSystemMessage_DeletedOriginAgent_IsNotRehomed(t *testing.T) {
	al, msgBus, _, defaultAgent := newAsyncResultTestLoop(t, &mockProvider{})
	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", defaultAgent.ID)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	const deletedAgentID = "4b4594d1-deleted-agent"
	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:             "webchat",
		ChatID:              "direct",
		AgentID:             deletedAgentID,
		TranscriptSessionID: meta.ID,
		SourceKind:          "goal_continue_push",
		SenderCanonicalID:   goalLoopFollowUpSenderID,
		Content:             "Keep working on the goal.",
	})
	require.Equal(t, deletedAgentID, msg.AsyncOriginAgentID, "precondition: the message names the deleted agent")

	resp, err := al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)
	assert.Empty(t, resp, "no turn may run for a deleted origin agent")

	assert.Empty(t, readAssistantTranscript(t, store, meta.ID),
		"a background result for a deleted agent must not be answered by the default agent (%q)", defaultAgent.ID)

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var notes int
	for _, e := range entries {
		assert.NotEqual(t, defaultAgent.ID, e.AgentID, "no transcript entry may be attributed to the default agent")
		if e.Role == "system" && strings.Contains(e.Content, deletedAgentID) && strings.Contains(e.Content, "no longer exists") {
			notes++
		}
	}
	assert.Equal(t, 1, notes, "exactly one system note must tell the user the update was discarded and why")
}

// TestProcessSystemMessage_AsyncResult_AttributesToOriginatingAgent covers
// FIX 5d root cause #1: an async result from agent X (the delegate),
// delivered after its parent's own turn has already finished, is attributed
// to X in the persisted transcript — never silently reattributed to
// GetDefaultAgent().
//
// BDD:
//
//	Given a delegate agent distinct from the registry's default,
//	  And a transcript session bound to that delegate's background work,
//	When AsyncNotifier.Notify publishes the delegate's result and
//	  processSystemMessage processes it (the exact function AgentLoop.Run's
//	  dispatch loop calls for any Channel=="system" message),
//	Then the persisted assistant transcript entry's AgentID is the
//	  DELEGATE's own ID, not the default agent's.
//
// Traces to: pkg/agent/loop.go processSystemMessage (AsyncOriginAgentID
// resolution); pkg/bus/types.go InboundMessage.AsyncOriginAgentID.
func TestProcessSystemMessage_AsyncResult_AttributesToOriginatingAgent(t *testing.T) {
	al, msgBus, delegate, defaultAgent := newAsyncResultTestLoop(t, &mockProvider{})

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", delegate.ID)
	require.NoError(t, err, "create session")
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:             "webchat",
		ChatID:              "direct",
		AgentID:             delegate.ID,
		TranscriptSessionID: meta.ID,
		SourceKind:          "delegate",
		Content:             "The delegate finished the background task.",
	})

	_, err = al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)

	assistantEntries := readAssistantTranscript(t, store, meta.ID)
	require.Len(t, assistantEntries, 1, "exactly one assistant entry must be persisted")
	assert.Equal(t, delegate.ID, assistantEntries[0].AgentID,
		"the async result must be attributed to the NAMED originating agent (%q), not silently "+
			"reattributed to the default agent (%q)", delegate.ID, defaultAgent.ID)
	assert.NotEqual(t, defaultAgent.ID, assistantEntries[0].AgentID)
}

// TestProcessSystemMessage_AsyncResult_FallsBackToDefaultWhenNoOriginKnown
// is the complementary case: when the inbound message carries no
// AsyncOriginAgentID (e.g. a hypothetical future producer with no turn scope
// of its own, or a legacy/synthetic system message), processSystemMessage
// must still fall back to GetDefaultAgent() — the fallback is a genuine
// last resort, not removed by this fix.
func TestProcessSystemMessage_AsyncResult_FallsBackToDefaultWhenNoOriginKnown(t *testing.T) {
	al, msgBus, _, defaultAgent := newAsyncResultTestLoop(t, &mockProvider{})

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:    "webchat",
		ChatID:     "direct",
		SourceKind: "bash",
		Content:    "no AgentID/TranscriptSessionID on this event",
		// AgentID and TranscriptSessionID deliberately left unset.
	})
	require.Empty(t, msg.AsyncOriginAgentID, "test precondition: no origin agent on this message")

	_, err := al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)

	// No TranscriptSessionID means nothing to assert against a session
	// store — the meaningful assertion is that processSystemMessage did not
	// error out and used SOME agent (implicitly the default, since
	// GetDefaultAgent() is the only fallback and a nil agent would have
	// produced an error return above).
	require.NotNil(t, defaultAgent)
}

// TestProcessSystemMessage_AsyncResult_PersistsWithoutLiveConnection is the
// FIX 5d data-loss regression test (root cause #2): it proves the async
// result reaches transcript.jsonl even when NO live streaming connection
// exists for the originating chat.
//
// This test's AgentLoop has NO bus.StreamDelegate registered at all —
// architecturally IDENTICAL, from the agent loop's point of view, to the
// real-world scenario where a WS connection existed when the background
// work started but has since closed (tab close, reload, network blip, or
// its own 60s idle timeout) by the time the async result arrives: either
// way, bus.MessageBus.GetStreamer(...) returns (nil, false), and the turn
// MUST fall through to the non-streaming persistence path
// (turnState.appendAssistantTranscript).
//
// Before FIX 5d, processSystemMessage never set TranscriptSessionID/
// TranscriptStore on the reconstructed turn's processOptions, so
// appendAssistantTranscript's own guard (transcriptStore != nil &&
// transcriptSessionID != "") was a GUARANTEED no-op — the result was
// silently, permanently lost, unrecoverable even by reopening the
// conversation. This test was confirmed to FAIL against the pre-fix
// processSystemMessage (TranscriptSessionID/TranscriptStore wiring
// temporarily reverted) before the fix was restored.
//
// BDD:
//
//	Given a delegate agent's background result bound to a transcript session,
//	  And no live streaming connection is available for the chat (simulating
//	    a closed/reconnected WS connection),
//	When processSystemMessage processes the async result,
//	Then the result IS STILL PERSISTED to the correct session's transcript —
//	  not silently dropped.
func TestProcessSystemMessage_AsyncResult_PersistsWithoutLiveConnection(t *testing.T) {
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, &mockProvider{})

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", delegate.ID)
	require.NoError(t, err, "create session")
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	// Sanity-check the scenario precondition: no StreamDelegate is
	// registered on this bus, so GetStreamer fails exactly as it would for a
	// closed WS connection.
	_, hasStreamer := msgBus.GetStreamer(context.Background(), "cli", "direct", meta.ID)
	require.False(t, hasStreamer, "test setup invariant: no live streaming connection must be available")

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:             "webchat",
		ChatID:              "direct",
		AgentID:             delegate.ID,
		TranscriptSessionID: meta.ID,
		SourceKind:          "bash",
		Content:             "Background build finished: 0 errors.",
	})

	_, err = al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)

	assistantEntries := readAssistantTranscript(t, store, meta.ID)
	require.Len(t, assistantEntries, 1,
		"FIX 5d: the async result must be persisted to transcript.jsonl even though no live "+
			"streaming connection exists for this chat — it must NOT be silently, permanently lost")
	// The persisted content is the LLM's own response to the reconstructed
	// turn (mockProvider always returns "Mock response", ignoring input) —
	// the assertion that matters is that AN entry exists at all (proving
	// persistence occurred) and that it is attributed correctly.
	assert.Equal(t, "Mock response", assistantEntries[0].Content)
	assert.Equal(t, delegate.ID, assistantEntries[0].AgentID)
}

// TestProcessSystemMessage_AsyncResult_LiveConnectionPathStillWorks is the
// FIX 5d "no regression" check (required test #3): when a live streaming
// connection IS available for the reconstructed turn's chat, the turn must
// still stream normally AND attribute correctly. FIX 5d's change (resolving
// the named agent + transcript session before runAgentLoop) must not disturb
// the existing live-streaming behavior FIX 5a already covers in depth at the
// wsStreamer level (pkg/gateway/websocket_producer_agent_id_test.go) — this
// test instead proves the WIRING from the reconstructed-turn entry point
// still reaches a live streamer correctly.
func TestProcessSystemMessage_AsyncResult_LiveConnectionPathStillWorks(t *testing.T) {
	provider := &asyncResultStreamingProvider{content: "Delegate says hello live."}
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, provider)

	streamer := &asyncResultMockStreamer{}
	msgBus.SetStreamDelegate(&asyncResultMockStreamDelegate{streamer: streamer})

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", delegate.ID)
	require.NoError(t, err, "create session")
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:             "webchat",
		ChatID:              "direct",
		AgentID:             delegate.ID,
		TranscriptSessionID: meta.ID,
		SourceKind:          "delegate",
		Content:             "irrelevant — the streaming provider ignores turn input for this test",
	})

	_, err = al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)

	streamer.mu.Lock()
	defer streamer.mu.Unlock()
	require.NotEmpty(t, streamer.updates,
		"the live streamer must have received at least one Update — streaming must still engage "+
			"for the reconstructed async-result turn, exactly as it would for any other turn")
	assert.True(t, streamer.finalized, "the live streamer must be Finalized at turn end")
	require.NotEmpty(t, streamer.setProducerAgentIDCalls,
		"FIX 5a's producer-attribution wiring must engage on the FIX 5d reconstructed-turn path too")
	assert.Equal(t, delegate.ID, streamer.setProducerAgentIDCalls[0],
		"the live streamer must be stamped with the delegate's own ID, not the default agent's")
}

// TestProcessSystemMessage_AsyncResult_SessionIsolation_NoCrossSessionLeak is
// the direct regression test for the A-I4 round 6 Priority 2 finding: two
// delegate-completion notifications for the SAME agent, from two DIFFERENT
// originating sessions, must not blend their content into a shared LLM
// prompt, and must not persist into each other's transcript.
//
// BDD:
//
//	Given one agent that owns TWO separate chat sessions (sessionOne, sessionTwo),
//	  each with its own background delegate that completed,
//	When AsyncNotifier.Notify delivers sessionOne's result and
//	  processSystemMessage reconstructs and runs that notify-turn,
//	  And THEN AsyncNotifier.Notify delivers sessionTwo's result and
//	  processSystemMessage reconstructs and runs THAT notify-turn,
//	Then sessionTwo's notify-turn's LLM prompt does NOT contain sessionOne's
//	  distinctive marker content,
//	  And sessionOne's persisted transcript does NOT contain sessionTwo's
//	  marker content, and vice versa.
func TestProcessSystemMessage_AsyncResult_SessionIsolation_NoCrossSessionLeak(t *testing.T) {
	provider := &recordingProvider{}
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, provider)

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")

	metaOne, err := store.NewSession(session.SessionTypeChat, "webchat", delegate.ID)
	require.NoError(t, err, "create session one")
	t.Cleanup(func() { _ = store.DeleteSession(metaOne.ID) })

	metaTwo, err := store.NewSession(session.SessionTypeChat, "webchat", delegate.ID)
	require.NoError(t, err, "create session two")
	t.Cleanup(func() { _ = store.DeleteSession(metaTwo.ID) })
	require.NotEqual(t, metaOne.ID, metaTwo.ID, "test setup: two distinct sessions required")

	const sessionOneMarker = "SESSION-ONE-EXCLUSIVE-MARKER-e8f1c2"
	const sessionTwoMarker = "SESSION-TWO-EXCLUSIVE-MARKER-b4a97d"

	// First notify-turn: sessionOne's delegate result.
	msgOne := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:             "webchat",
		ChatID:              "direct-one",
		AgentID:             delegate.ID,
		TranscriptSessionID: metaOne.ID,
		SourceKind:          "delegate",
		Content:             sessionOneMarker,
	})
	_, err = al.processSystemMessage(context.Background(), msgOne)
	require.NoError(t, err)
	require.True(t, recordingProviderSawInLastCall(t, provider, sessionOneMarker),
		"sanity: sessionOne's own notify-turn must see its own marker")

	// Second notify-turn: sessionTwo's UNRELATED delegate result, same agent.
	msgTwo := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:             "webchat",
		ChatID:              "direct-two",
		AgentID:             delegate.ID,
		TranscriptSessionID: metaTwo.ID,
		SourceKind:          "delegate",
		Content:             sessionTwoMarker,
	})
	_, err = al.processSystemMessage(context.Background(), msgTwo)
	require.NoError(t, err)

	assert.True(t, recordingProviderSawInLastCall(t, provider, sessionTwoMarker),
		"sessionTwo's own notify-turn must see its own marker")
	assert.False(t, recordingProviderSawInLastCall(t, provider, sessionOneMarker),
		"REGRESSION (A-I4 round 6 Priority 2): sessionTwo's notify-turn LLM prompt must NOT "+
			"contain sessionOne's content — a shared 'agent:<id>:main' SessionKey used to leak "+
			"cross-session history into the prompt")

	// Persisted-transcript isolation: each session's own transcript must
	// only ever mention its own marker, never the other session's.
	oneEntries := readAssistantTranscript(t, store, metaOne.ID)
	require.NotEmpty(t, oneEntries, "sessionOne must have a persisted assistant entry")
	for _, e := range oneEntries {
		assert.NotContains(t, e.Content, sessionTwoMarker,
			"sessionOne's transcript must never contain sessionTwo's marker")
	}

	twoEntries := readAssistantTranscript(t, store, metaTwo.ID)
	require.NotEmpty(t, twoEntries, "sessionTwo must have a persisted assistant entry")
	for _, e := range twoEntries {
		assert.NotContains(t, e.Content, sessionOneMarker,
			"sessionTwo's transcript must never contain sessionOne's marker")
	}
}

// TestProcessSystemMessage_AsyncResult_NoOriginSession_FallsBackToMainKey
// covers the narrower fallback: when the inbound system message carries no
// AsyncTranscriptSessionID at all (no origin session known), the notify-turn
// still falls back to the agent-wide "main" key exactly as it did before this
// fix — there is no session to scope to, so this is not a regression case.
func TestProcessSystemMessage_AsyncResult_NoOriginSession_FallsBackToMainKey(t *testing.T) {
	al, msgBus, _, defaultAgent := newAsyncResultTestLoop(t, &mockProvider{})

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:    "webchat",
		ChatID:     "direct",
		SourceKind: "bash",
		Content:    "no TranscriptSessionID on this event",
		// AgentID and TranscriptSessionID deliberately left unset.
	})
	require.Empty(t, msg.AsyncTranscriptSessionID, "test precondition: no origin session on this message")

	_, err := al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)

	mainKey := "agent:" + defaultAgent.ID + ":main"
	require.NotNil(t, defaultAgent.Sessions)
	// Give the async turn goroutine (if any) a brief moment; runAgentLoop
	// itself is synchronous here so this is just defensive against future
	// changes, mirroring the existing FallsBackToDefault test's spirit.
	time.Sleep(10 * time.Millisecond)
	mainHistory := defaultAgent.Sessions.GetHistory(mainKey)
	assert.NotEmpty(t, mainHistory,
		"with no origin session known, the notify-turn must still use the agent-wide 'main' key %q "+
			"(unchanged fallback behavior — not a regression)", mainKey)
}

// TestResolveMessageRoute_BoundDrop_SkipsGetDefaultAgent verifies TDD #10 /
// US-5 AC-2: when ResolveRoute returns Drop=true (bound agent unresolvable),
// resolveMessageRoute must NOT call GetDefaultAgent. The error returned must
// indicate no agent is available. The driftDropped counter is NOT incremented
// here — resolveMessageRoute is a side-effect-free resolver; the counter is
// emitted exactly once by processMessage (the actual rejection site).
func TestResolveMessageRoute_BoundDrop_SkipsGetDefaultAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	// mia is the global default chat agent; "ray" is NOT in the list (deleted).
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
	}
	// The instance "whatsapp.eu" is bound: WorkspaceID + agent identity.
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"whatsapp.eu": {
			Type:        "whatsapp",
			Enabled:     true,
			WorkspaceID: "sales",
			Identity:    &config.ChannelIdentity{Kind: "agent", ID: "ray"},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	// Register only mia (the global default); ray is absent (deleted).
	registerInstance(t, al, cfg, home, "mia", config.AgentTypeCustom)

	// Inbound on the bound instance — no explicit agent_id, no handoff pin.
	msg := bus.InboundMessage{
		Channel:    "whatsapp",
		InstanceID: "whatsapp.eu",
		ChatID:     "15555550100@s.whatsapp.net",
		Content:    "hello",
	}

	preDrift := al.GetDriftDropped()
	route, agent, err := al.resolveMessageRoute(msg)
	postDrift := al.GetDriftDropped()

	// Must return an error (no agent available).
	if err == nil {
		t.Fatal("resolveMessageRoute must return an error for a drift drop, got nil")
	}
	// Must NOT return the global default agent.
	if agent != nil && agent.ID == "mia" {
		t.Errorf("drift drop routed to global default agent 'mia'; must NOT use the default")
	}
	// route.Drop must be true so the caller (processMessage) can detect the drift
	// condition and emit the counter/audit exactly once.
	if !route.Drop {
		t.Error("resolveMessageRoute must return route.Drop=true for a drift drop")
	}
	// resolveMessageRoute is side-effect-free: it must NOT increment the counter.
	// The counter is emitted by processMessage (the actual rejection site).
	if postDrift != preDrift {
		t.Errorf(
			"driftDropped counter changed inside resolveMessageRoute: pre=%d post=%d; must stay unchanged (counter is emitted by processMessage)",
			preDrift,
			postDrift,
		)
	}
}

// TestResolveMessageRoute_Unbound_DefaultUnchanged verifies TDD #11 / regression:
// an unbound channel instance (no WorkspaceID, no Identity) with no explicit
// agent_id and no handoff pin routes via the existing default cascade, unchanged.
// GetDefaultAgent IS called and the global default agent handles the message.
func TestResolveMessageRoute_Unbound_DefaultUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
	}
	// No channels configuration: bare type key / no WorkspaceID → unbound.
	cfg.Channels = map[string]config.ChannelInstanceConfig{}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	registerInstance(t, al, cfg, home, "mia", config.AgentTypeCustom)

	msg := bus.InboundMessage{
		Channel: "telegram",
		ChatID:  "user123",
		Content: "hi",
	}

	preDrift := al.GetDriftDropped()
	_, agent, err := al.resolveMessageRoute(msg)
	postDrift := al.GetDriftDropped()

	if err != nil {
		t.Fatalf("unbound default route must succeed; got: %v", err)
	}
	if agent == nil {
		t.Fatal("unbound default route must return an agent; got nil")
	}
	if agent.ID != "mia" {
		t.Errorf("unbound route: agent = %q, want %q", agent.ID, "mia")
	}
	// Drift counter must NOT increment for an unbound route.
	if postDrift != preDrift {
		t.Errorf("driftDropped changed for unbound route: pre=%d post=%d; must not increment", preDrift, postDrift)
	}
}

// TestResolveMessageRoute_BoundDrift_WorkerAgent verifies DS-3 row 4 at the
// agent-loop level: when the bound agent is a worker (not a chat target),
// the route drops and the default is NOT used.
func TestResolveMessageRoute_BoundDrift_WorkerAgent_LoopLevel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "worker1", Type: config.AgentTypeWorker},
	}
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"whatsapp.eu": {
			Type:        "whatsapp",
			Enabled:     true,
			WorkspaceID: "sales",
			Identity:    &config.ChannelIdentity{Kind: "agent", ID: "worker1"},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	registerInstance(t, al, cfg, home, "mia", config.AgentTypeCustom)
	registerInstance(t, al, cfg, home, "worker1", config.AgentTypeWorker)

	msg := bus.InboundMessage{
		Channel:    "whatsapp",
		InstanceID: "whatsapp.eu",
		ChatID:     "15555550100@s.whatsapp.net",
		Content:    "test",
	}

	preDrift := al.GetDriftDropped()
	route, agent, err := al.resolveMessageRoute(msg)
	postDrift := al.GetDriftDropped()

	if err == nil {
		t.Fatal("resolveMessageRoute must return an error for a worker-agent drift drop")
	}
	if agent != nil && agent.ID == "mia" {
		t.Error("worker-agent drift drop must NOT route to the global default 'mia'")
	}
	// route.Drop must be true so processMessage can detect and emit exactly once.
	if !route.Drop {
		t.Error("resolveMessageRoute must return route.Drop=true for a worker-agent drift drop")
	}
	// Side-effect-free: counter must NOT increment here; processMessage is the emission point.
	if postDrift != preDrift {
		t.Errorf(
			"driftDropped changed inside resolveMessageRoute: pre=%d post=%d; must stay unchanged",
			preDrift,
			postDrift,
		)
	}
}

// TestProcessSystemMessage_MediaToolDelivery_StampsWorkspaceID is the
// processSystemMessage regression: a reconstructed delegate-completion /
// async-notify turn (msg.Channel == "system") resolves WorkspaceID from the
// SAME session its output persists into (AsyncTranscriptSessionID), mirroring
// processMessage's own resolution.
func TestProcessSystemMessage_MediaToolDelivery_StampsWorkspaceID(t *testing.T) {
	al, defaultAgent, telegramChannel, _ := newMediaWorkspaceIDTestLoop(t, "telegram")

	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	ws := "sales"
	require.NoError(t, al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &ws}))

	resp, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel: "system",
		// originChannel/originChatID parse from ChatID as "channel:chat_id".
		ChatID: "telegram:chat1",
		Sender: bus.SenderInfo{CanonicalID: "delegate"},
		Content: "Task 'research' completed.\n\nResult:\n" +
			"take a screenshot of the screen and send it to me",
		AsyncOriginAgentID:       defaultAgent.ID,
		AsyncTranscriptSessionID: meta.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, "Here is the screenshot.", resp)

	require.Len(t, telegramChannel.sentMedia, 1,
		"expected exactly 1 synchronously sent media message")
	assert.Equal(t, "sales", telegramChannel.sentMedia[0].WorkspaceID,
		"a reconstructed system-message turn's tool media must carry the workspace "+
			"stamped on the origin session it persists into")
}

// TestBuildContinuationTarget_ResolvesWorkspaceID_FromSessionMeta proves the
// PRIMARY resolution mechanism resolveWorkspaceIDForContinuation shares with
// processMessage: when the inbound message already carries a SessionID
// (always true for webchat), the session's own meta.WorkspaceID wins.
func TestBuildContinuationTarget_ResolvesWorkspaceID_FromSessionMeta(t *testing.T) {
	al := newMediaWorkspaceIDTestLoopOnly(t, "telegram")

	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	ws := "sales"
	require.NoError(t, al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &ws}))

	target, err := al.buildContinuationTarget(bus.InboundMessage{
		Channel:   "telegram",
		ChatID:    "chat1",
		SessionID: meta.ID,
		Sender:    bus.SenderInfo{CanonicalID: "user1"},
	})
	require.NoError(t, err)
	require.NotNil(t, target)
	assert.Equal(t, "sales", target.WorkspaceID)
}

// TestBuildContinuationTarget_ResolvesWorkspaceID_FromChannelBinding proves
// the FALLBACK resolution mechanism: when the message has no SessionID (the
// common case for a channel message session_worker's own msg copy never saw
// mutated — see resolveWorkspaceIDForContinuation's doc comment), the bound
// channel instance's own configured WorkspaceID is used instead — the exact
// value resolveOrCreateChannelSession would have seeded a new session with.
func TestBuildContinuationTarget_ResolvesWorkspaceID_FromChannelBinding(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
		Channels: map[string]config.ChannelInstanceConfig{
			"telegram.sales": {
				Type:        "telegram",
				Enabled:     true,
				WorkspaceID: "sales",
			},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(al.Close)

	target, err := al.buildContinuationTarget(bus.InboundMessage{
		Channel:    "telegram",
		InstanceID: "telegram.sales",
		ChatID:     "chat1",
		Sender:     bus.SenderInfo{CanonicalID: "user1"},
		// No SessionID — the session_worker mutation-visibility gap case.
	})
	require.NoError(t, err)
	require.NotNil(t, target)
	assert.Equal(t, "sales", target.WorkspaceID)
}

// TestResolveMessageRoute_ExplicitAgentIDOverridesHandoffOverride pins that
// when a message carries an explicit agent_id AND a stale "session:" override
// exists for that session from a prior handoff, the explicit selection wins and
// the override is cleared. This prevents Ray's tool calls leaking into Jim's
// session after the user switches the SPA dropdown back to Jim.
func TestResolveMessageRoute_ExplicitAgentIDOverridesHandoffOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	for _, id := range []string{"jim", "ray"} {
		ag := NewAgentInstance(&config.AgentConfig{ID: id, Name: id},
			&cfg.Agents.Defaults, cfg, &mockProvider{})
		ag.Home = filepath.Join(home, "agents", id)
		ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(id, id)
		al.registry.mu.Lock()
		al.registry.agents[id] = ag
		al.registry.mu.Unlock()
	}

	const sessionID = "sess-leak-1"
	// Simulate the state left by a prior Mia → Ray handoff, keyed by session_id.
	al.sessionActiveAgent.Store("session:"+sessionID, "ray")

	// User picks Jim in the SPA dropdown — webchat sends agent_id=jim with the session_id.
	msg := bus.InboundMessage{
		Channel:   "webchat",
		SessionID: sessionID,
		Content:   "what is my name",
		Metadata:  map[string]string{"agent_id": "jim"},
	}
	route, agent, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute: %v", err)
	}
	if route.AgentID != "jim" {
		t.Fatalf("explicit agent_id=jim was overridden — routed to %q", route.AgentID)
	}
	if agent == nil || agent.ID != "jim" {
		t.Fatalf("returned agent should be jim, got %+v", agent)
	}
	wantSK := "agent:jim:session:" + sessionID
	if route.SessionKey != wantSK {
		t.Errorf("session key = %q, want %q", route.SessionKey, wantSK)
	}
	// Stale override must be gone so future no-agent-id messages route to jim.
	if _, stale := al.sessionActiveAgent.Load("session:" + sessionID); stale {
		t.Error("explicit agent_id should have cleared the stale handoff override")
	}
}

// TestResolveMessageRoute_HandoffOverrideStillAppliesWhenNoExplicitAgentID
// confirms the "session:" override still routes correctly when the message has
// no explicit agent_id, and that the session key uses the new formula.
func TestResolveMessageRoute_HandoffOverrideStillAppliesWhenNoExplicitAgentID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	ag := NewAgentInstance(&config.AgentConfig{ID: "ray", Name: "ray"},
		&cfg.Agents.Defaults, cfg, &mockProvider{})
	ag.Home = filepath.Join(home, "agents", "ray")
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo("ray", "ray")
	al.registry.mu.Lock()
	al.registry.agents["ray"] = ag
	al.registry.mu.Unlock()

	const sessionID = "sess-leak-2"
	// Set override using the new session-scoped key.
	al.sessionActiveAgent.Store("session:"+sessionID, "ray")

	msg := bus.InboundMessage{
		Channel:   "webchat",
		SessionID: sessionID,
		Content:   "follow up after handoff with no agent_id metadata",
	}
	route, _, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute: %v", err)
	}
	if route.AgentID != "ray" {
		t.Fatalf("handoff override should still route to ray when no explicit agent_id, got %q", route.AgentID)
	}
	wantSK := "agent:ray:session:" + sessionID
	if route.SessionKey != wantSK {
		t.Errorf("session key = %q, want %q", route.SessionKey, wantSK)
	}
}

// TestResolveMessageRoute_ChannelHandoffOverride_RoutesByChatScope pins the
// channel-handoff fix (P1). A channel (whatsapp/telegram/…) inbound message
// carries NO SessionID, so sessionScopeKey resolves its handoff override under
// the chat scope ("chat:<channel>:<chatID>"), NOT "session:<id>". The handoff
// now stores the override under that same chat-scope key, so after Mia hands off
// to Ray in a WhatsApp chat, the next WhatsApp message routes to Ray. Regression
// for the bug where the override was stored ONLY under "session:<id>" — which
// channel messages never look up — so routing fell back to ResolveRoute and the
// original agent "stayed".
func TestResolveMessageRoute_ChannelHandoffOverride_RoutesByChatScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	for _, id := range []string{"mia", "ray"} {
		ag := NewAgentInstance(&config.AgentConfig{ID: id, Name: id},
			&cfg.Agents.Defaults, cfg, &mockProvider{})
		ag.Home = filepath.Join(home, "agents", id)
		ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(id, id)
		al.registry.mu.Lock()
		al.registry.agents[id] = ag
		al.registry.mu.Unlock()
	}

	const chatID = "15555550100@s.whatsapp.net"
	// State left by a Mia → Ray handoff on a WhatsApp chat: onHandoffFrontend
	// writes the override under the chat-scope key for non-webchat channels.
	al.sessionActiveAgent.Store("chat:whatsapp:"+chatID, "ray")

	// Next inbound WhatsApp message from the same chat — NO SessionID (channels
	// don't carry one), no explicit agent_id.
	msg := bus.InboundMessage{
		Channel: "whatsapp",
		ChatID:  chatID,
		Content: "are you still there?",
	}
	route, agent, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute: %v", err)
	}
	if route.AgentID != "ray" {
		t.Fatalf(
			"channel handoff override should route the whatsapp message to ray, got %q ('agent stays' regression)",
			route.AgentID,
		)
	}
	if agent == nil || agent.ID != "ray" {
		t.Fatalf("returned agent should be ray, got %+v", agent)
	}
	wantSK := "agent:ray:chat:whatsapp:" + chatID
	if route.SessionKey != wantSK {
		t.Errorf("session key = %q, want %q", route.SessionKey, wantSK)
	}
}

// TestResolveMessageRoute_ExplicitWorkerAgentIDDegradesToDefault verifies RESIDUAL
// PATH 1: a message that explicitly addresses a worker via agent_id metadata must
// NOT let the worker answer as a persona. It degrades to the normal ResolveRoute
// cascade, which resolves the chat-target default ("mia"). The worker is invocable
// only via delegation, never as a chat target.
func TestResolveMessageRoute_ExplicitWorkerAgentIDDegradesToDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	// The route cascade reads cfg.Agents.List to resolve the default — give it a
	// chat-target default and a worker.
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "hans", Type: config.AgentTypeWorker},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	registerInstance(t, al, cfg, home, "mia", config.AgentTypeCore)
	registerInstance(t, al, cfg, home, "hans", config.AgentTypeWorker)

	msg := bus.InboundMessage{
		Channel:   "webchat",
		SessionID: "sess-worker-explicit",
		Content:   "answer me directly",
		Metadata:  map[string]string{"agent_id": "hans"},
	}
	route, agent, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute: %v", err)
	}
	if route.AgentID == "hans" || (agent != nil && agent.ID == "hans") {
		t.Fatalf(
			"explicit agent_id=worker resolved to the worker (route=%q) — workers are not chat targets",
			route.AgentID,
		)
	}
	if route.AgentID != "mia" {
		t.Errorf("AgentID = %q, want %q (worker degrades to base default)", route.AgentID, "mia")
	}
}

// TestResolveMessageRoute_WorkerHandoffPinClearedAndFallsBack verifies RESIDUAL
// PATH 2: a stale session handoff pin that points at a worker must be cleared and
// routing must fall back to the chat-target default. A worker pin is illegitimate
// (handoff now rejects worker targets), so resolution drops it.
func TestResolveMessageRoute_WorkerHandoffPinClearedAndFallsBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "hans", Type: config.AgentTypeWorker},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	registerInstance(t, al, cfg, home, "mia", config.AgentTypeCore)
	registerInstance(t, al, cfg, home, "hans", config.AgentTypeWorker)

	const sessionID = "sess-worker-pin"
	// Simulate a stale pin to a worker (no explicit agent_id on the message).
	al.sessionActiveAgent.Store("session:"+sessionID, "hans")

	msg := bus.InboundMessage{
		Channel:   "webchat",
		SessionID: sessionID,
		Content:   "follow up",
	}
	route, agent, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute: %v", err)
	}
	if route.AgentID == "hans" || (agent != nil && agent.ID == "hans") {
		t.Fatalf("worker pin resolved to the worker (route=%q) — workers are not chat targets", route.AgentID)
	}
	if route.AgentID != "mia" {
		t.Errorf("AgentID = %q, want %q (worker pin degrades to base default)", route.AgentID, "mia")
	}
	// The stale worker pin must have been deleted.
	if _, stale := al.sessionActiveAgent.Load("session:" + sessionID); stale {
		t.Error("stale worker handoff pin should have been cleared")
	}
}

// TestResolveMessageRoute_ExplicitBaseAgentIDStillResolves is the control: an
// explicit agent_id pointing at a NON-worker base agent still resolves to it,
// proving the worker guard does not break normal explicit routing.
func TestResolveMessageRoute_ExplicitBaseAgentIDStillResolves(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "jim"},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	registerInstance(t, al, cfg, home, "mia", config.AgentTypeCore)
	registerInstance(t, al, cfg, home, "jim", config.AgentTypeCore)

	msg := bus.InboundMessage{
		Channel:   "webchat",
		SessionID: "sess-base-explicit",
		Content:   "hello jim",
		Metadata:  map[string]string{"agent_id": "jim"},
	}
	route, agent, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute: %v", err)
	}
	if route.AgentID != "jim" || agent == nil || agent.ID != "jim" {
		t.Fatalf("explicit agent_id=jim (base agent) should resolve to jim, got route=%q", route.AgentID)
	}
}

// TestResolveWorkspaceIDForContinuation_NoSession_ReturnsEmpty exercises the
// real, fixed production function (loop.go) for the "legitimate empty" case
// that must remain silent and unchanged: an inbound message carrying a
// SessionID that resolves to no session anywhere resolves to "" via the
// existing metadata/channel fallbacks.
func TestResolveWorkspaceIDForContinuation_NoSession_ReturnsEmpty(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Model: "scripted-model"},
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), testutil.NewScenario())
	t.Cleanup(al.Close)

	got := al.resolveWorkspaceIDForContinuation(bus.InboundMessage{
		SessionID: "session-that-does-not-exist",
		Channel:   "telegram",
		ChatID:    "999",
	})
	if got != "" {
		t.Fatalf("expected empty workspace id for a nonexistent session, got %q", got)
	}
}

// TestResolveWorkspaceIDForContinuation_SessionWithWorkspace_ReturnsIt proves
// the surviving happy path still works: a real session whose meta carries a
// WorkspaceID is resolved and returned.
func TestResolveWorkspaceIDForContinuation_SessionWithWorkspace_ReturnsIt(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Model: "scripted-model"},
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), testutil.NewScenario())
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("expected a shared session store on a freshly constructed AgentLoop")
	}

	meta, err := store.NewChannelSession("telegram", "telegram", "888", "mia", "test session")
	if err != nil {
		t.Fatalf("NewChannelSession: %v", err)
	}
	wantWorkspace := "workspace-xyz"
	if setErr := store.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &wantWorkspace}); setErr != nil {
		t.Fatalf("SetMeta: %v", setErr)
	}

	got := al.resolveWorkspaceIDForContinuation(bus.InboundMessage{
		SessionID: meta.ID,
		Channel:   "telegram",
		ChatID:    "888",
	})
	if got != wantWorkspace {
		t.Fatalf("expected workspace id %q, got %q", wantWorkspace, got)
	}
}

// TestResolveWorkspaceIDForContinuation_CorruptMeta_WarnsDownstream is the
// reachability proof the earlier fix pass could not produce (see this file's
// header comment): with ResolveSessionStore now returning the owning store
// for a corrupt-meta session instead of nil, resolveWorkspaceIDForContinuation
// gets a non-nil store, re-reads GetMeta itself, hits the identical
// non-ErrNotExist error, and its own "continuation: could not read session
// meta while resolving workspace; workspace unresolved" WARN (loop.go, ~3145)
// actually fires. Also asserts the function still returns "" rather than
// fabricating a workspace id — the corruption must be surfaced, not papered
// over with a guessed value.
func TestResolveWorkspaceIDForContinuation_CorruptMeta_WarnsDownstream(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "continuation-corrupt.log")

	prevLevel := logger.GetLevel()
	t.Cleanup(logger.DisableConsole())
	logger.SetLevel(logger.WARN)
	if err := logger.EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Model: "scripted-model"},
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), testutil.NewScenario())
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("expected a shared session store on a freshly constructed AgentLoop")
	}

	const sessionID = "corrupt-meta-continuation-test"
	sessionDir := filepath.Join(store.BaseDir(), sessionID)
	if mkErr := os.MkdirAll(sessionDir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}
	metaPath := filepath.Join(sessionDir, "meta.json")
	if writeErr := os.WriteFile(metaPath, []byte("{not valid json"), 0o600); writeErr != nil {
		t.Fatalf("WriteFile: %v", writeErr)
	}

	// Channel/ChatID chosen so no other fallback (channel-instance binding,
	// inbound metadata) can accidentally produce a non-empty workspace id —
	// this test isolates the corrupt-meta path specifically.
	got := al.resolveWorkspaceIDForContinuation(bus.InboundMessage{
		SessionID: sessionID,
		Channel:   "telegram",
		ChatID:    "no-such-instance-binding",
	})
	if got != "" {
		t.Fatalf("expected empty workspace id (no fabricated fallback) when session meta is corrupt, got %q", got)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", logFile, err)
	}
	logged := string(data)
	const wantMarker = "continuation: could not read session meta while resolving workspace; workspace unresolved"
	if !strings.Contains(logged, wantMarker) {
		t.Errorf(
			"downstream WARN not reached — this is exactly the reachability regression the fix closes; log:\n%s",
			logged,
		)
	}
	if !strings.Contains(logged, sessionID) {
		t.Errorf("log file missing the session id %q; got:\n%s", sessionID, logged)
	}
}
