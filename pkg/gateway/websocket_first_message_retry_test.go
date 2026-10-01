package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1090 D5 and the founder's "middle way": session-less Retry is remembered
// within one gateway process, scoped to the authenticated principal. A saved
// retry gets recovered:true + received, never another entry, echo or turn.
// Restart recovery and a private on-disk ledger are explicitly out of scope.

// issue1090DrainQueuedFrames inspects actual serialized gateway output. Intake
// has returned, and neither a writer nor the agent loop consumes these queues.
// Generic JSON inspection lets these tests run against release types before
// the additive contract fields in e060bd065 are merged; no wire struct is copied.
func issue1090DrainQueuedFrames(t *testing.T, wc *wsConn) []map[string]any {
	t.Helper()
	wc.qmu.Lock()
	queued := len(wc.q) + len(wc.held)
	wc.qmu.Unlock()
	require.Zero(t, queued, "#1090 fixture must inspect the entire outbound queue")
	frames := make([]map[string]any, 0, len(wc.sendCh))
	for count := len(wc.sendCh); count > 0; count-- {
		var frame map[string]any
		require.NoError(t, json.Unmarshal(<-wc.sendCh, &frame), "#1090 gateway frame must be valid JSON")
		require.NotEmpty(t, frame["type"], "#1090 frame must identify its type")
		frames = append(frames, frame)
	}
	return frames
}

func issue1090FrameTypes(frames []map[string]any) []any {
	types := make([]any, 0, len(frames))
	for _, frame := range frames {
		types = append(types, frame["type"])
	}
	return types
}

// The named account is the already-authenticated intake input, not a claim in
// the client payload. Each send uses a new connection/chat ID in the SAME handler.
func issue1090SendSessionless(t *testing.T, handler *WSHandler, principal, chatID, content, clientID string) (*wsConn, []map[string]any) {
	t.Helper()
	wc := makeTestConn()
	wc.userID = principal
	t.Cleanup(wc.close)
	handler.handleChatMessageWithClientID(
		context.Background(), chatID, "", content, "mia", nil,
		"", "", false, clientID, nil, wc,
	)
	return wc, issue1090DrainQueuedFrames(t, wc)
}

func issue1090StartedSessionID(t *testing.T, frames []map[string]any) string {
	t.Helper()
	require.NotEmpty(t, frames, "#1090 must receive a session acknowledgement")
	require.Equal(t, "session_started", frames[0]["type"], "#1090 acknowledgement must precede the receipt")
	sessionID, ok := frames[0]["session_id"].(string)
	require.True(t, ok, "#1090 acknowledged session_id must be a string")
	require.NotEmpty(t, sessionID, "#1090 acknowledged session_id must be usable")
	return sessionID
}

func issue1090AssertRecovered(t *testing.T, frames []map[string]any, sessionID, clientID, label string) {
	t.Helper()
	assert.Equal(t, []any{"session_started", "message_status"}, issue1090FrameTypes(frames),
		"%s: D5 recovery must send only acknowledgement then received, with no new echo", label)
	require.NotEmpty(t, frames, "%s: recovery must produce its acknowledgement and receipt", label)
	started := frames[0]
	assert.Equal(t, sessionID, started["session_id"], "%s: Retry must recover the original session", label)
	assert.Equal(t, clientID, started["client_message_id"], "%s: recovery must correlate the original client ID", label)
	assert.Equal(t, true, started["recovered"], "%s: saved session-less Retry must report recovered:true", label)
	assert.NotContains(t, started, "seq", "%s: recovered acknowledgement must not invent a cursor", label)
	assert.NotContains(t, started, "boot_id", "%s: recovered acknowledgement must not seed a boot cursor", label)
	assert.Equal(t, map[string]any{
		"type": "message_status", "session_id": sessionID,
		"client_message_id": clientID, "state": "received",
	}, frames[len(frames)-1], "%s: D5 recovery receipt is exact and unsequenced", label)
}

func issue1090SessionDirectories(t *testing.T, store *session.UnifiedStore) []string {
	t.Helper()
	entries, err := os.ReadDir(store.BaseDir())
	require.NoError(t, err, "#1090 must inspect the real store's session directories")
	directories := []string{}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "session_") {
			directories = append(directories, entry.Name())
		}
	}
	return directories
}

func issue1090DiskTranscriptEntries(t *testing.T, store *session.UnifiedStore, sessionID string) []session.TranscriptEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl"))
	require.NoError(t, err, "#1090 must inspect disk, not a cached transcript")
	decoder := json.NewDecoder(bytes.NewReader(data))
	entries := []session.TranscriptEntry{}
	for {
		var entry session.TranscriptEntry
		err := decoder.Decode(&entry)
		if err == io.EOF {
			return entries
		}
		require.NoError(t, err, "#1090 transcript must contain complete JSONL entries")
		entries = append(entries, entry)
	}
}

func issue1090AssertSavedFirstEntry(t *testing.T, store *session.UnifiedStore, sessionID, content, clientID string) []session.TranscriptEntry {
	t.Helper()
	entries := issue1090DiskTranscriptEntries(t, store, sessionID)
	require.Len(t, entries, 1, "#1090 ordinary first send must save exactly one transcript entry")
	assert.Equal(t, "user", entries[0].Role, "#1090 saved first entry must be the ordinary user message")
	assert.Equal(t, content, entries[0].Content, "#1090 saved content must match the exact first send")
	assert.Equal(t, clientID, entries[0].ClientMessageID, "#1090 saved entry must retain the original client ID")
	return entries
}

func issue1090AdmittedTurns(msgBus *bus.MessageBus) [][3]string {
	// The real agent loop is not running and all intake calls have returned.
	// Exact session/content/principal triples check the real admission arguments,
	// not just a call count or an elapsed-time proxy for no-second-turn.
	turns := [][3]string{}
	for count := len(msgBus.InboundChan()); count > 0; count-- {
		message := <-msgBus.InboundChan()
		turns = append(turns, [3]string{message.SessionID, message.Content, message.GatewayUserID})
	}
	return turns
}

func TestFirstUserMessage_SessionlessRetryRecoversWithoutSecondEntryOrTurn(t *testing.T) {
	// T5: #1090 D5 duplicate response and founder-approved process-local dedupe.
	const principal, clientID, content = "1090-alice", "1090-t5-first", "T5 save this first message only once"
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	originalConn, initial := issue1090SendSessionless(t, handler, principal, "1090-t5-original", content, clientID)
	sessionID := issue1090StartedSessionID(t, initial)
	require.Equal(t, []any{"session_started", "user_message", "message_status"}, issue1090FrameTypes(initial),
		"T5 control: a fresh send must save, echo and acknowledge before an agent runs")
	before := issue1090AssertSavedFirstEntry(t, store, sessionID, content, clientID)
	require.Equal(t, []string{sessionID}, issue1090SessionDirectories(t, store), "T5 control: exactly one original session")
	require.Len(t, msgBus.InboundChan(), 1, "T5 control: the real bus must admit the original turn")
	t.Log("T5 instrument control: one exact first entry on disk and one original turn in the real bus")

	_, retry := issue1090SendSessionless(t, handler, principal, "1090-t5-retry", content, clientID)
	issue1090AssertRecovered(t, retry, sessionID, clientID, "T5")
	directories := issue1090SessionDirectories(t, store)
	assert.Equal(t, []string{sessionID}, directories, "T5: Retry must not mint another session")
	assert.Equal(t, before, issue1090DiskTranscriptEntries(t, store, sessionID), "T5: Retry must not append to the saved transcript")
	allEntries := make([]session.TranscriptEntry, 0, len(directories))
	for _, id := range directories {
		allEntries = append(allEntries, issue1090DiskTranscriptEntries(t, store, id)...)
	}
	assert.Len(t, allEntries, 1, "T5: exactly one transcript entry may exist across all sessions")
	assert.Equal(t, [][3]string{{sessionID, content, principal}}, issue1090AdmittedTurns(msgBus),
		"T5: Retry must not admit a second answer turn or alter the original admission")
	assert.Empty(t, issue1090DrainQueuedFrames(t, originalConn), "T5: recovered Retry must not re-echo through the original live hub")
}

func TestFirstUserMessage_SessionlessRetryIsIsolatedByAuthenticatedPrincipal(t *testing.T) {
	// T6: #1090 founder-approved principal scope. Recovery controls are essential:
	// disabling ALL recovery would otherwise falsely pass the privacy assertions.
	const alice, bob, clientID, content = "1090-alice", "1090-bob", "1090-t6-shared-id", "T6 identical payload under distinct accounts"
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	_, aliceFirst := issue1090SendSessionless(t, handler, alice, "1090-t6-alice-first", content, clientID)
	aliceSession := issue1090StartedSessionID(t, aliceFirst)
	aliceEntry := issue1090AssertSavedFirstEntry(t, store, aliceSession, content, clientID)
	_, bobFirst := issue1090SendSessionless(t, handler, bob, "1090-t6-bob-first", content, clientID)
	bobSession := issue1090StartedSessionID(t, bobFirst)
	assert.NotEqual(t, aliceSession, bobSession, "T6: a different principal must never be given the first principal's session")
	assert.Equal(t, []any{"session_started", "user_message", "message_status"}, issue1090FrameTypes(bobFirst),
		"T6: another principal's same ID is a fresh send, not a recovered receipt")
	if recovered, present := bobFirst[0]["recovered"]; present {
		assert.Equal(t, false, recovered, "T6: another principal's fresh acknowledgement must be absent/false, never recovered")
	}
	bobEntry := issue1090AssertSavedFirstEntry(t, store, bobSession, content, clientID)
	for _, owned := range []struct{ id, principal string }{{aliceSession, alice}, {bobSession, bob}} {
		meta, err := store.GetMeta(owned.id)
		require.NoError(t, err)
		require.NotNil(t, meta)
		assert.Equal(t, owned.principal, meta.Owner, "T6: same-ID fresh sessions must retain their distinct authenticated owners")
	}
	bobOutput, err := json.Marshal(bobFirst)
	require.NoError(t, err)
	assert.NotContains(t, string(bobOutput), aliceSession, "T6: no frame to Bob may disclose Alice's session ID")

	_, bobRetry := issue1090SendSessionless(t, handler, bob, "1090-t6-bob-retry", content, clientID)
	issue1090AssertRecovered(t, bobRetry, bobSession, clientID, "T6 Bob recovery control")
	bobOutput, err = json.Marshal(bobRetry)
	require.NoError(t, err)
	assert.NotContains(t, string(bobOutput), aliceSession, "T6: Bob's recovery must not reveal Alice's session ID")
	_, aliceRetry := issue1090SendSessionless(t, handler, alice, "1090-t6-alice-retry", content, clientID)
	issue1090AssertRecovered(t, aliceRetry, aliceSession, clientID, "T6 Alice recovery control")
	aliceOutput, err := json.Marshal(aliceRetry)
	require.NoError(t, err)
	assert.NotContains(t, string(aliceOutput), bobSession, "T6: Alice's recovery must not reveal Bob's session ID")
	assert.ElementsMatch(t, []string{aliceSession, bobSession}, issue1090SessionDirectories(t, store),
		"T6: each principal gets one session, including after both recovery controls")
	assert.Equal(t, aliceEntry, issue1090DiskTranscriptEntries(t, store, aliceSession), "T6: Alice's saved entry stays unchanged")
	assert.Equal(t, bobEntry, issue1090DiskTranscriptEntries(t, store, bobSession), "T6: Bob's saved entry stays unchanged")
	assert.Equal(t, [][3]string{{aliceSession, content, alice}, {bobSession, content, bob}}, issue1090AdmittedTurns(msgBus),
		"T6: only the two distinct original sends may start turns, under their own principals")
	t.Log("T6 instrument: both authenticated owners were checked; same-principal recovery controls forbid a false privacy green")
}
