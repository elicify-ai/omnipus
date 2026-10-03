package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1090 security-review S1/S2/B1, as amended by design-1090.md's
// "Team-lead ruling 2026-10-01": handler-enforced IDs <= 128 characters,
// FIFO retention of 256 first sends per principal, original-request conflicts,
// and principal-scoped existing-session retries. Numbers below come from that
// ruling, never from the implementation's private constants.
//
// Boundary: the generated MessageFrame passes through the real WS dispatcher,
// intake, disk store, live hub and admission bus. Connections carry an already
// authenticated account, as in websocket_first_message_retry_test.go. No agent
// loop or writer consumes the queues, so return from dispatch is the barrier;
// no sleep or mock stands in for "no second entry/turn". Authentication handshakes
// and model execution are outside these intake tests.
//
// Deliberate gaps: the global 10,000-entry cap would require 10,001 durable
// first sends across >= 40 principals through this boundary. It is not replaced
// by seeding a private cache. The ruling does not specify backend error wording
// or a new conflict code: assert the existing ErrorFrame's exact type and client
// ID plus a nonblank visible message, not an invented wire code or copied output.
// CHECK (a different qa-lead instance) owns implementation GREEN and mutations:
// remove/relax the ID guard, retain entry 1 or evict newest rather than oldest,
// ignore one digest field, hash resolved defaults, or remove the principal gate.

func issue1090SecurityFixture(t *testing.T) (*WSHandler, *bus.MessageBus, *session.UnifiedStore, *config.Config) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	// Same real intake fixture as newTestWSHandlerForModelName, with a second
	// chat target so a default-agent change is an observable runtime input.
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: t.TempDir(), DefaultAgentID: "mia",
				DefaultModel: config.DefaultModel{Model: "test-default-model"}, MaxTokens: 4096,
			},
			List: []config.AgentConfig{{ID: "mia"}, {ID: "security-other-chat-agent"}},
		},
	}
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newWSHandler(msgBus, al, "")
	t.Cleanup(handler.Wait)
	require.False(t, cfg.Gateway.ValidateInbound, "S1 must exercise the validation-disabled intake path")
	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store, "security fixture must use the real first-message store")
	return handler, msgBus, store, cfg
}

func issue1090SecurityFrame(clientID, content string) generated.MessageFrame {
	agentID := "mia"
	return generated.MessageFrame{
		Type: string(generated.WsFrameTypeMessage), ClientMessageId: &clientID,
		Content: content, AgentId: &agentID,
	}
}

// This is the same synchronous intake path readLoop uses when schema validation
// is disabled, not a cache-helper call. Use generated types for all wire inputs.
func issue1090SecurityDispatch(t *testing.T, handler *WSHandler, principal, chatID string, frame generated.MessageFrame) (*wsConn, []map[string]any) {
	t.Helper()
	wc := makeTestConn()
	wc.userID = principal
	t.Cleanup(wc.close)
	data, err := json.Marshal(frame)
	require.NoError(t, err, "security fixture must serialize the generated client frame")
	wh := &wsHandlerReadLoop{h: handler, ctx: context.Background(), wc: wc, chatID: chatID}
	require.Equal(t, wsHandlerReadLoopNext, wh.dispatchFrame(data, wsTypeOnly{Type: frame.Type}),
		"security fixture must finish the real message dispatcher")
	return wc, issue1090DrainQueuedFrames(t, wc)
}

func issue1090SecurityAssertFresh(t *testing.T, frames []map[string]any, clientID string) string {
	t.Helper()
	require.Equal(t, []any{"session_started", "user_message", "message_status"}, issue1090FrameTypes(frames),
		"control: an accepted first send must acknowledge, echo and report received")
	sessionID := issue1090StartedSessionID(t, frames)
	assert.Equal(t, clientID, frames[0]["client_message_id"], "control: fresh acknowledgement must correlate the send")
	assert.NotEqual(t, true, frames[0]["recovered"], "control: a fresh send is not recovered")
	assert.Equal(t, "received", frames[2]["state"], "control: the real save must produce a received receipt")
	assert.Equal(t, clientID, frames[2]["client_message_id"], "control: the received receipt must correlate the send")
	assert.Equal(t, sessionID, frames[2]["session_id"], "control: received must name the saved session")
	return sessionID
}

func issue1090SecurityAssertVisibleError(t *testing.T, frames []map[string]any, clientID, label string) {
	t.Helper()
	assert.Equal(t, []any{"error"}, issue1090FrameTypes(frames),
		"%s: rejection must send only a visible error, never an acknowledgement, echo or status", label)
	errors := 0
	for _, frame := range frames {
		assert.NotEqual(t, true, frame["recovered"], "%s: a rejected request must never report recovered:true", label)
		if frame["type"] != "error" {
			continue
		}
		errors++
		data, err := json.Marshal(frame)
		require.NoError(t, err)
		var rejected generated.ErrorFrame
		require.NoError(t, json.Unmarshal(data, &rejected), "%s: rejection must use the existing generated ErrorFrame", label)
		assert.Equal(t, "error", rejected.Type, "%s: exact rejection frame type", label)
		assert.NotEmpty(t, strings.TrimSpace(rejected.Message), "%s: the client must receive a human-readable error", label)
		if clientID != "" {
			require.NotNil(t, rejected.ClientMessageId, "%s: conflict must correlate the original client ID", label)
			assert.Equal(t, clientID, *rejected.ClientMessageId, "%s: conflict correlation must be exact", label)
		}
	}
	assert.Equal(t, 1, errors, "%s: exactly one visible error is required", label)
}

func TestFirstUserMessage_ClientID129IsRejectedBeforeSave(t *testing.T) {
	// S1 / ruling: 128 is the maximum, even with optional validation disabled.
	const principal = "1090-s1-alice"
	clientID := strings.Repeat("x", 129)
	handler, msgBus, store, _ := issue1090SecurityFixture(t)
	require.Empty(t, issue1090SessionDirectories(t, store), "S1 fixture starts with no chat sessions")

	_, frames := issue1090SecurityDispatch(t, handler, principal, "s1-oversized", issue1090SecurityFrame(clientID, "Reject before creating any chat"))
	// An invalid 129-character ID cannot be echoed in a schema-valid ErrorFrame
	// (whose correlation field also has maxLength 128); S1 only requires visibility.
	issue1090SecurityAssertVisibleError(t, frames, "", "S1 129-character ID")
	assert.Empty(t, issue1090SessionDirectories(t, store), "S1: 129-character ID must be rejected before creating a session or transcript")
	assert.Empty(t, issue1090AdmittedTurns(msgBus), "S1: rejected ID must not admit an answer turn")
}

func TestFirstUserMessage_ClientID128IsAcceptedBoundary(t *testing.T) {
	// S1: max-1 and max controls make an unconditional rejection false green impossible.
	for _, tc := range []struct{ name, clientID string }{
		{"characters_127", strings.Repeat("b", 127)},
		{"characters_128", strings.Repeat("b", 128)},
		// The schema/ruling bounds characters, not UTF-8 bytes.
		{"characters_128_multibyte", strings.Repeat("é", 128)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const principal, content = "1090-s1-alice", "Save the boundary ID"
			clientID := tc.clientID
			handler, msgBus, store, _ := issue1090SecurityFixture(t)
			_, initial := issue1090SecurityDispatch(t, handler, principal, "s1-boundary", issue1090SecurityFrame(clientID, content))
			sessionID := issue1090SecurityAssertFresh(t, initial, clientID)
			before := issue1090AssertSavedFirstEntry(t, store, sessionID, content, clientID)
			assert.Equal(t, [][3]string{{sessionID, content, principal}}, issue1090AdmittedTurns(msgBus), "S1 boundary: exact original turn must be admitted")

			_, retry := issue1090SecurityDispatch(t, handler, principal, "s1-boundary-retry", issue1090SecurityFrame(clientID, content))
			issue1090AssertRecovered(t, retry, sessionID, clientID, "S1 accepted boundary retry control")
			assert.Equal(t, before, issue1090DiskTranscriptEntries(t, store, sessionID), "S1 boundary: Retry must not append")
			assert.Equal(t, []string{sessionID}, issue1090SessionDirectories(t, store), "S1 boundary: Retry must not mint")
			assert.Empty(t, issue1090AdmittedTurns(msgBus), "S1 boundary: Retry must not admit a second turn")
		})
	}
}

func TestExistingUserMessage_ClientID129IsRejectedBeforeSave(t *testing.T) {
	// "Unconditional ... before any persistence" also covers existing sessions.
	const principal, firstID, firstContent = "1090-s1-alice", "s1-existing-seed", "Create the owned chat"
	handler, msgBus, store, _ := issue1090SecurityFixture(t)
	_, initial := issue1090SecurityDispatch(t, handler, principal, "s1-existing-seed", issue1090SecurityFrame(firstID, firstContent))
	sessionID := issue1090SecurityAssertFresh(t, initial, firstID)
	before := issue1090AssertSavedFirstEntry(t, store, sessionID, firstContent, firstID)
	require.Equal(t, [][3]string{{sessionID, firstContent, principal}}, issue1090AdmittedTurns(msgBus), "S1 existing control: exactly one initial turn")

	frame := issue1090SecurityFrame(strings.Repeat("x", 129), "Do not append this oversized ID")
	frame.SessionId = &sessionID
	_, frames := issue1090SecurityDispatch(t, handler, principal, "s1-existing-oversized", frame)
	issue1090SecurityAssertVisibleError(t, frames, "", "S1 existing-session 129-character ID")
	assert.Equal(t, before, issue1090DiskTranscriptEntries(t, store, sessionID), "S1: oversized existing-session ID must not append a transcript entry")
	assert.Equal(t, []string{sessionID}, issue1090SessionDirectories(t, store), "S1: rejection must leave exactly the original session")
	assert.Empty(t, issue1090AdmittedTurns(msgBus), "S1: oversized existing-session ID must not admit a turn")
}

func TestFirstUserMessage_PrincipalCacheEvictsOldestAfter256(t *testing.T) {
	// S1 / ruling: 256 per principal; the 257th accepted distinct ID evicts first.
	// Never seed or count private cache entries. Each iteration saves through intake.
	const principal, perPrincipal = "1090-s1-alice", 256
	handler, msgBus, store, _ := issue1090SecurityFixture(t)
	const bob, bobID, bobContent = "1090-s1-bob", "s1-bob-retained", "Another principal's retained first send"
	_, bobInitial := issue1090SecurityDispatch(t, handler, bob, "s1-bob-original", issue1090SecurityFrame(bobID, bobContent))
	bobSession := issue1090SecurityAssertFresh(t, bobInitial, bobID)
	bobBefore := issue1090AssertSavedFirstEntry(t, store, bobSession, bobContent, bobID)
	require.Equal(t, [][3]string{{bobSession, bobContent, bob}}, issue1090AdmittedTurns(msgBus), "S1 principal control: Bob's original is accepted")
	sessions := make([]string, 0, perPrincipal+1)
	for index := 1; index <= perPrincipal+1; index++ {
		clientID := fmt.Sprintf("s1-fifo-%03d", index)
		content := fmt.Sprintf("FIFO original first send %d", index)
		wc, initial := issue1090SecurityDispatch(t, handler, principal, fmt.Sprintf("s1-fifo-chat-%d", index), issue1090SecurityFrame(clientID, content))
		sessionID := issue1090SecurityAssertFresh(t, initial, clientID)
		issue1090AssertSavedFirstEntry(t, store, sessionID, content, clientID)
		require.Equal(t, [][3]string{{sessionID, content, principal}}, issue1090AdmittedTurns(msgBus), "S1 FIFO: each accepted send must admit exactly its own turn")
		sessions = append(sessions, sessionID)
		wc.close()
		if index == perPrincipal-1 || index == perPrincipal {
			// At 255 and 256, first must still recover. A retry must not refresh
			// its insertion age: the next new send must still evict it (FIFO, not LRU).
			_, retry := issue1090SecurityDispatch(t, handler, principal, fmt.Sprintf("s1-fifo-first-at-%d", index), issue1090SecurityFrame("s1-fifo-001", "FIFO original first send 1"))
			issue1090AssertRecovered(t, retry, sessions[0], "s1-fifo-001", fmt.Sprintf("S1 exactly-%d retention control", index))
			require.Empty(t, issue1090AdmittedTurns(msgBus), "S1 at-cap recovery must not admit a turn")
		}
	}
	require.Len(t, issue1090SessionDirectories(t, store), perPrincipal+2, "S1 FIFO: Alice's 257 saved sessions plus Bob's establish the boundary")
	_, bobRetry := issue1090SecurityDispatch(t, handler, bob, "s1-bob-after-overflow", issue1090SecurityFrame(bobID, bobContent))
	issue1090AssertRecovered(t, bobRetry, bobSession, bobID, "S1 another-principal retention control")
	assert.Equal(t, bobBefore, issue1090DiskTranscriptEntries(t, store, bobSession), "S1: Alice's overflow must not evict or rewrite Bob's original")

	lastID := fmt.Sprintf("s1-fifo-%03d", perPrincipal+1)
	lastContent := fmt.Sprintf("FIFO original first send %d", perPrincipal+1)
	_, newest := issue1090SecurityDispatch(t, handler, principal, "s1-fifo-newest-retry", issue1090SecurityFrame(lastID, lastContent))
	issue1090AssertRecovered(t, newest, sessions[perPrincipal], lastID, "S1 newest-entry retention control")
	_, second := issue1090SecurityDispatch(t, handler, principal, "s1-fifo-second-retry", issue1090SecurityFrame("s1-fifo-002", "FIFO original first send 2"))
	issue1090AssertRecovered(t, second, sessions[1], "s1-fifo-002", "S1 only-oldest-evicted control")
	require.Empty(t, issue1090AdmittedTurns(msgBus), "S1 retained retries must not admit turns")
	t.Log("S1 controls: first recovered at 256; second and 257th recovered after overflow")

	const firstID, firstContent = "s1-fifo-001", "FIFO original first send 1"
	_, oldest := issue1090SecurityDispatch(t, handler, principal, "s1-fifo-evicted-retry", issue1090SecurityFrame(firstID, firstContent))
	require.NotEmpty(t, oldest, "S1: evicted Retry must still receive a response")
	assert.NotEqual(t, true, oldest[0]["recovered"], "S1: after 257 accepted first sends, entry 1 must no longer recover from cache")
	newSession := issue1090SecurityAssertFresh(t, oldest, firstID)
	assert.NotEqual(t, sessions[0], newSession, "S1: an evicted ID is a fresh first send, not the old cached session")
	issue1090AssertSavedFirstEntry(t, store, newSession, firstContent, firstID)
	issue1090AssertSavedFirstEntry(t, store, sessions[0], firstContent, firstID)
	assert.Len(t, issue1090SessionDirectories(t, store), perPrincipal+3, "S1: evicted Retry may create the explicitly accepted second-chat window, leaving Bob's chat intact")
	assert.Equal(t, [][3]string{{newSession, firstContent, principal}}, issue1090AdmittedTurns(msgBus), "S1: only the evicted fresh Retry may admit a new turn")
}

func issue1090SecurityAssertConflict(t *testing.T, original, changed generated.MessageFrame) {
	t.Helper()
	const principal = "1090-s2-alice"
	handler, msgBus, store, _ := issue1090SecurityFixture(t)
	require.NotNil(t, original.ClientMessageId)
	clientID := *original.ClientMessageId
	originalConn, initial := issue1090SecurityDispatch(t, handler, principal, "s2-original", original)
	sessionID := issue1090SecurityAssertFresh(t, initial, clientID)
	before := issue1090AssertSavedFirstEntry(t, store, sessionID, original.Content, clientID)
	require.Equal(t, [][3]string{{sessionID, original.Content, principal}}, issue1090AdmittedTurns(msgBus), "S2 control: original request must really save and admit")

	_, identical := issue1090SecurityDispatch(t, handler, principal, "s2-identical-control", original)
	issue1090AssertRecovered(t, identical, sessionID, clientID, "S2 identical-request control")
	require.Equal(t, before, issue1090DiskTranscriptEntries(t, store, sessionID), "S2 control: identical Retry must not append")
	require.Empty(t, issue1090AdmittedTurns(msgBus), "S2 control: identical Retry must not admit")
	t.Log("S2 control: identical ORIGINAL requested fields recovered the saved session without another entry or turn")

	_, conflict := issue1090SecurityDispatch(t, handler, principal, "s2-changed-retry", changed)
	issue1090SecurityAssertVisibleError(t, conflict, clientID, "S2 same-ID/different-request conflict")
	assert.Equal(t, before, issue1090DiskTranscriptEntries(t, store, sessionID), "S2: conflicting Retry must not append")
	assert.Equal(t, []string{sessionID}, issue1090SessionDirectories(t, store), "S2: conflicting Retry must not create another session")
	assert.Empty(t, issue1090AdmittedTurns(msgBus), "S2: conflicting Retry must not admit a second answer turn")
	assert.Empty(t, issue1090DrainQueuedFrames(t, originalConn), "S2: conflict must not publish an echo/status to the original live hub")
}

func TestFirstUserMessage_ChangedTextRetryIsRejected(t *testing.T) {
	// S2 / ruling: same principal + ID, different exact text is a conflict.
	original := issue1090SecurityFrame("s2-text", "Original exact user text")
	changed := original
	changed.Content = "Different user text must not receive the original receipt"
	issue1090SecurityAssertConflict(t, original, changed)
}

func TestFirstUserMessage_ChangedRequestedFieldsRetryIsRejected(t *testing.T) {
	// S2 / ruling: fingerprint ORIGINAL agent/media/workspace/model/Auto inputs,
	// not resolved defaults or the attachment descriptors displayed in a transcript.
	for _, tc := range []struct {
		name   string
		change func(*generated.MessageFrame)
	}{
		{"agent_omitted_to_explicit_default", func(f *generated.MessageFrame) { agentID := "mia"; f.AgentId = &agentID }},
		{"media_reference_changed", func(f *generated.MessageFrame) { f.Media = []string{"media://s2-different", "media://s2-second"} }},
		{"media_order_changed", func(f *generated.MessageFrame) { f.Media = []string{"media://s2-second", "media://s2-original"} }},
		{"workspace_changed", func(f *generated.MessageFrame) { f.Metadata["workspace_id"] = "s2-another-requested-workspace" }},
		{"model_changed", func(f *generated.MessageFrame) { f.Metadata["model_name"] = "s2-another-requested-model" }},
		{"auto_absent_to_false", func(f *generated.MessageFrame) { choice := false; f.AutoApprove = &choice }},
		{"auto_absent_to_true", func(f *generated.MessageFrame) { choice := true; f.AutoApprove = &choice }},
		{"auto_false_to_true", func(f *generated.MessageFrame) { choice := true; f.AutoApprove = &choice }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := issue1090SecurityFrame("s2-fields-"+tc.name, "The exact text does not change")
			original.AgentId = nil // resolves Mia, but absent is the ORIGINAL request
			if tc.name == "auto_false_to_true" {
				choice := false
				original.AutoApprove = &choice
			}
			original.Media = []string{"media://s2-original", "media://s2-second"}
			// These syntactically valid requested workspace/media IDs need not
			// resolve locally. They deliberately distinguish the raw request
			// from the empty resolved binding / absent attachment descriptors.
			original.Metadata = map[string]any{
				"workspace_id": "s2-original-requested-workspace", "model_name": "s2-original-requested-model",
			}
			changed := original
			changed.Metadata = map[string]any{
				"workspace_id": "s2-original-requested-workspace", "model_name": "s2-original-requested-model",
			}
			tc.change(&changed)
			issue1090SecurityAssertConflict(t, original, changed)
		})
	}
}

func TestFirstUserMessage_IdenticalRequestRetryRecovers(t *testing.T) {
	// Standalone passing controls, including requested fields that intake resolves
	// or drops, and a changed SERVER default that must not conflict with a retry.
	for _, name := range []string{"explicit_agent", "original_media_and_metadata", "auto_false", "server_default_changed"} {
		t.Run(name, func(t *testing.T) {
			const principal, content = "1090-s2-alice", "Identical ORIGINAL request control"
			clientID := "s2-identical-" + name
			handler, msgBus, store, cfg := issue1090SecurityFixture(t)
			frame := issue1090SecurityFrame(clientID, content)
			switch name {
			case "original_media_and_metadata":
				frame.AgentId = nil
				frame.Media = []string{"media://s2-original", "media://s2-second"}
				frame.Metadata = map[string]any{"workspace_id": "s2-original-requested-workspace", "model_name": "s2-original-requested-model"}
			case "auto_false":
				choice := false
				frame.AutoApprove = &choice
			case "server_default_changed":
				frame.AgentId = nil
			}
			_, initial := issue1090SecurityDispatch(t, handler, principal, "s2-control-first", frame)
			sessionID := issue1090SecurityAssertFresh(t, initial, clientID)
			before := issue1090AssertSavedFirstEntry(t, store, sessionID, content, clientID)
			require.Equal(t, [][3]string{{sessionID, content, principal}}, issue1090AdmittedTurns(msgBus), "S2 control: real original admission")
			if name == "server_default_changed" {
				registry := handler.agentLoop.GetRegistry()
				require.Equal(t, "mia", registry.GetDefaultAgent().ID, "S2 default control: original resolves Mia")
				cfg.Agents.Defaults.DefaultAgentID = "security-other-chat-agent"
				registry.SetDefaultAgentOverride("security-other-chat-agent")
				require.Equal(t, "security-other-chat-agent", registry.GetDefaultAgent().ID, "S2 default control: runtime default actually changed")
			}

			_, retry := issue1090SecurityDispatch(t, handler, principal, "s2-control-retry", frame)
			issue1090AssertRecovered(t, retry, sessionID, clientID, "S2 standalone identical-request control")
			assert.Equal(t, "mia", retry[0]["agent_id"], "S2 identical control: Retry must retain the ORIGINAL resolved agent, even after the default changes")
			assert.Equal(t, before, issue1090DiskTranscriptEntries(t, store, sessionID), "S2 identical control: transcript stays exact")
			assert.Equal(t, []string{sessionID}, issue1090SessionDirectories(t, store), "S2 identical control: only one session")
			assert.Empty(t, issue1090AdmittedTurns(msgBus), "S2 identical control: no new turn")
		})
	}
}

// Seed B1 through an actual existing-session send, not the first-send index.
func issue1090SecurityExistingSend(t *testing.T) (*WSHandler, *bus.MessageBus, *session.UnifiedStore, generated.MessageFrame, []map[string]any, []session.TranscriptEntry) {
	t.Helper()
	const principal, seedID, seedContent = "1090-b1-alice", "b1-seed", "Start Alice's owned chat"
	handler, msgBus, store, _ := issue1090SecurityFixture(t)
	_, initial := issue1090SecurityDispatch(t, handler, principal, "b1-seed", issue1090SecurityFrame(seedID, seedContent))
	sessionID := issue1090SecurityAssertFresh(t, initial, seedID)
	issue1090AssertSavedFirstEntry(t, store, sessionID, seedContent, seedID)
	require.Equal(t, [][3]string{{sessionID, seedContent, principal}}, issue1090AdmittedTurns(msgBus), "B1 seed: exact original turn")

	frame := issue1090SecurityFrame("b1-existing-X", "Alice's private existing-session message")
	frame.SessionId = &sessionID
	_, accepted := issue1090SecurityDispatch(t, handler, principal, "b1-existing-first", frame)
	require.Equal(t, []any{"user_message", "message_status"}, issue1090FrameTypes(accepted), "B1 control: existing-session send must save and echo without minting")
	entries := issue1090DiskTranscriptEntries(t, store, sessionID)
	require.Len(t, entries, 2, "B1 control: existing-session message must really be on disk")
	require.Equal(t, frame.Content, entries[1].Content, "B1 control: exact original private message saved")
	require.Equal(t, *frame.ClientMessageId, entries[1].ClientMessageID, "B1 control: original X must be cached from a real accepted send")
	meta, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	require.NotNil(t, meta)
	require.Equal(t, principal, meta.Owner, "B1 control: session is owned by account A")
	require.Equal(t, [][3]string{{sessionID, frame.Content, principal}}, issue1090AdmittedTurns(msgBus), "B1 existing control: exact second original turn")
	return handler, msgBus, store, frame, accepted, entries
}

func issue1090SecurityAssertExistingRecovery(t *testing.T, frames, original []map[string]any, sessionID, clientID string) {
	t.Helper()
	require.Equal(t, []any{"user_message", "message_status"}, issue1090FrameTypes(frames), "B1 same-account retry must recover its existing echo and status")
	// Existing-session retry has #823's unsequenced echo/status contract, not
	// #1090's session-less recovered:true acknowledgement. Preserve exact echo
	// fields, removing only the original live publication's sequence number.
	expectedEcho := make(map[string]any, len(original[0]))
	for key, value := range original[0] {
		if key != "seq" {
			expectedEcho[key] = value
		}
	}
	assert.Equal(t, expectedEcho, frames[0], "B1 same-account retry must recover the exact original cached bytes, unsequenced")
	assert.Equal(t, map[string]any{
		"type": "message_status", "session_id": sessionID, "client_message_id": clientID, "state": "received",
	}, frames[1], "B1 same-account retry must recover its exact original received status")
}

func TestExistingUserMessage_RetryIsIsolatedByPrincipal(t *testing.T) {
	// B1 / ruling: knowing A's session ID and X does not authorize cached bytes.
	handler, msgBus, store, frame, accepted, before := issue1090SecurityExistingSend(t)
	sessionID, clientID := *frame.SessionId, *frame.ClientMessageId
	_, sameAccount := issue1090SecurityDispatch(t, handler, "1090-b1-alice", "b1-alice-control", frame)
	issue1090SecurityAssertExistingRecovery(t, sameAccount, accepted, sessionID, clientID)
	require.Empty(t, issue1090AdmittedTurns(msgBus), "B1 same-account control must not admit a turn")
	t.Log("B1 control: account A recovered its exact cached existing-session echo and received status")

	foreign := frame
	// B knows ONLY the two IDs, not Alice's original body. If B gets that body,
	// it can only have come from A's cache, not a new echo of B's own payload.
	foreign.Content = "Bob knows session ID and X, but not the private message"
	_, foreignFrames := issue1090SecurityDispatch(t, handler, "1090-b1-bob", "b1-bob-retry", foreign)
	output, err := json.Marshal(foreignFrames)
	require.NoError(t, err)
	assert.NotContains(t, string(output), frame.Content, "B1: account B must not receive A's cached message bytes")
	assert.NotContains(t, string(output), before[1].ID, "B1: account B must not receive A's cached transcript-entry ID")
	for _, reply := range foreignFrames {
		if reply["type"] == "message_status" {
			assert.NotContains(t, []any{"received", "working"}, reply["state"], "B1: account B must not receive A's cached delivery status")
		}
		assert.NotEqual(t, true, reply["recovered"], "B1: account B must not receive a recovered receipt for A")
	}
	assert.Equal(t, before, issue1090DiskTranscriptEntries(t, store, sessionID), "B1: foreign Retry must not fall through into an append to A's transcript")
	assert.Empty(t, issue1090AdmittedTurns(msgBus), "B1: foreign Retry must not fall through into a new answer turn")

	_, stillOwned := issue1090SecurityDispatch(t, handler, "1090-b1-alice", "b1-alice-after-bob", frame)
	issue1090SecurityAssertExistingRecovery(t, stillOwned, accepted, sessionID, clientID)
	assert.Equal(t, before, issue1090DiskTranscriptEntries(t, store, sessionID), "B1: Alice's retry after Bob must still avoid a second entry")
	assert.Equal(t, []string{sessionID}, issue1090SessionDirectories(t, store), "B1: isolation must leave exactly the owned session")
	assert.Empty(t, issue1090AdmittedTurns(msgBus), "B1: Alice's recovery after Bob must not admit another turn")
}

func TestExistingUserMessage_SamePrincipalRetryRecoversCachedEcho(t *testing.T) {
	// Standalone positive control: disabling the old cache is not a B1 fix.
	handler, msgBus, store, frame, accepted, before := issue1090SecurityExistingSend(t)
	sessionID, clientID := *frame.SessionId, *frame.ClientMessageId
	_, retry := issue1090SecurityDispatch(t, handler, "1090-b1-alice", "b1-same-account-retry", frame)
	issue1090SecurityAssertExistingRecovery(t, retry, accepted, sessionID, clientID)
	assert.Equal(t, before, issue1090DiskTranscriptEntries(t, store, sessionID), "B1 positive control: Retry must not append")
	assert.Equal(t, []string{sessionID}, issue1090SessionDirectories(t, store), "B1 positive control: Retry must not mint")
	assert.Empty(t, issue1090AdmittedTurns(msgBus), "B1 positive control: Retry must not admit another turn")
}
