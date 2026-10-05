package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// ADR-20260928 D1.4 — server-written answer provenance, WRITER side only.
// The oracles are the D1.4 writer requirements, not this intake's output:
//
//  1. a failed transcript append never continues the turn and is visible to
//     the sender for EVERY message — the non-first append included;
//  2. a successful authenticated append persists server-internal provenance
//     that is looked up by SERVER message id and carries the connection
//     principal and a per-session ordinal greater than the previous append;
//  3. a generic agent/tool transcript write mints no provenance.
//
// The Who/When/Which answer acceptor is NOT built here; the provenance
// COMPILE-BLOCKED test in pkg/agent stays in place.

// newProvenanceHarness boots the real intake harness with the same shape as
// the #1090 files: real session store on disk, real buffered bus (nothing
// consumes admissions), a bare test connection carrying an authenticated
// principal. Every send is synchronous, so draining wc covers ALL its frames.
func newProvenanceHarness(t *testing.T) (*WSHandler, *bus.MessageBus, *session.UnifiedStore, *wsConn) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store, "provenance harness needs the real session store")
	wc := makeTestConn()
	wc.userID = "prov-alice"
	t.Cleanup(wc.close)
	return handler, msgBus, store, wc
}

// provenanceSendAuthenticated sends one ordinary message and drains every
// frame the sender received. sessionID=="" mints a new session.
func provenanceSendAuthenticated(t *testing.T, handler *WSHandler, wc *wsConn, chatID, sessionID, content, clientID string) []map[string]any {
	t.Helper()
	handler.handleChatMessageWithClientID(
		context.Background(), chatID, sessionID, content, "mia", nil,
		"", "", false, clientID, nil, wc,
	)
	return issue1090DrainQueuedFrames(t, wc)
}

func TestUserMessageAppendFailure_NonFirstStopsTurnAndReportsVisibleError(t *testing.T) {
	// D1.4: "If the transcript append fails ... do not continue the turn, and
	// return a visible error to the sender for every message, including a
	// non-first message." This is the NON-first append: the previous behavior
	// logged a WARN and admitted the turn anyway.
	const content = "D1.4 non-first append failure must not start a turn"
	handler, msgBus, store, wc := newProvenanceHarness(t)

	minted, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err, "fixture session creation")
	sessionID := minted.ID

	// The real-failure instrument from the #1090 T2 tests: a transcript that
	// is a DIRECTORY cannot be opened for append, for any user. No gateway,
	// store, or fileutil persistence function is mocked.
	transcriptPath := filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl")
	require.NoError(t, os.Remove(transcriptPath), "fixture replaces only the pristine transcript")
	require.NoError(t, os.Mkdir(transcriptPath, 0o700), "fixture creates an invalid append target")
	opened, appendErr := os.OpenFile(transcriptPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if opened != nil {
		require.NoError(t, opened.Close())
	}
	var pathErr *os.PathError
	require.ErrorAs(t, appendErr, &pathErr, "instrument control: the append open must actually fail")
	require.Equal(t, "open", pathErr.Op, "instrument control: the failure precedes any write")

	frames := provenanceSendAuthenticated(t, handler, wc, "prov-fail-chat", sessionID, content, "prov-fail-m1")

	assert.NotContains(t, issue1090FrameTypes(frames), "user_message",
		"a failed append must not echo the message as saved")
	assert.NotContains(t, issue1090FrameTypes(frames), "session_started",
		"a failed append must not acknowledge anything as saved")
	require.Len(t, frames, 2, "exactly the visible error and the failed receipt must reach the sender; frames=%v", frames)
	require.Equal(t, "error", frames[0]["type"], "the first outbound frame must be the visible error")
	message, ok := frames[0]["message"].(string)
	require.True(t, ok, "the error must carry a human-readable message; frame=%v", frames[0])
	assert.NotEmpty(t, message, "the error must be visible to the user, not a bare type")
	assert.Equal(t, "prov-fail-m1", frames[0]["client_message_id"],
		"the error must correlate the sender's pending bubble")
	assert.NotContains(t, frames[0], "first_message_error",
		"this is not a first message; the first-message error tag must not appear")
	assert.Equal(t, "message_status", frames[1]["type"], "the ordinary rejection receipt follows the error")
	assert.Equal(t, "failed", frames[1]["state"], "the rejected message reports failed, never received")

	require.Len(t, msgBus.InboundChan(), 0,
		"the failed append must not admit a turn — the previous warn-and-continue is gone")

	_, statErr := os.Stat(filepath.Join(store.BaseDir(), sessionID, "provenance.jsonl"))
	require.True(t, os.IsNotExist(statErr),
		"a failed transcript append must not write provenance")
	_, found, lookupErr := store.LookupMessageProvenance(sessionID, "no-such-server-message-id")
	require.NoError(t, lookupErr)
	assert.False(t, found, "no provenance exists after a failed append")
}

func TestUserMessageProvenance_MintedByAuthenticatedAppend_LookupByServerMessageID(t *testing.T) {
	// D1.4: a successful authenticated append is looked up by SERVER message
	// id and carries the connection principal and an ordinal greater than the
	// previous append in the same session. The principal is the connection's
	// (wc.userID), never anything the client payload could choose.
	const firstContent = "D1.4 first authenticated message"
	const secondContent = "D1.4 second authenticated message"
	handler, _, store, wc := newProvenanceHarness(t)

	firstFrames := provenanceSendAuthenticated(t, handler, wc, "prov-chat-1", "", firstContent, "prov-m1")
	require.Equal(t, []any{"session_started", "user_message", "message_status"}, issue1090FrameTypes(firstFrames),
		"control: a fresh authenticated send saves, acknowledges and echoes")
	sessionID := issue1090StartedSessionID(t, firstFrames)

	secondFrames := provenanceSendAuthenticated(t, handler, wc, "prov-chat-1", sessionID, secondContent, "prov-m2")
	require.Contains(t, issue1090FrameTypes(secondFrames), "user_message",
		"control: the second append echoes like the first")

	entries := issue1090DiskTranscriptEntries(t, store, sessionID)
	require.Len(t, entries, 2, "control: exactly the two user messages reached the disk transcript")
	require.Equal(t, firstContent, entries[0].Content)
	require.Equal(t, secondContent, entries[1].Content)

	first, found, err := store.LookupMessageProvenance(sessionID, entries[0].ID)
	require.NoError(t, err)
	require.True(t, found, "the first accepted append must be lookup-able by its server message id")
	assert.Equal(t, entries[0].ID, first.MessageID, "the record is keyed by the server message id")
	assert.Equal(t, sessionID, first.SessionID)
	assert.Equal(t, firstContent, first.Content, "the record carries the exact accepted text")
	assert.Equal(t, "prov-alice", first.Principal, "the record carries the connection principal")
	assert.Equal(t, int64(1), first.Ordinal, "the first provenance-carrying append is ordinal 1")

	second, found, err := store.LookupMessageProvenance(sessionID, entries[1].ID)
	require.NoError(t, err)
	require.True(t, found, "the second accepted append must be lookup-able by its server message id")
	assert.Equal(t, secondContent, second.Content)
	assert.Equal(t, "prov-alice", second.Principal)
	assert.Greater(t, second.Ordinal, first.Ordinal,
		"the ordinal must be greater than the previous append in the same session")
	assert.Equal(t, int64(2), second.Ordinal)

	// Provenance must not appear in SPA or gateway frames, nor in the
	// persisted transcript JSON the client can read back. Both surfaces are
	// decoded generically, so an added field could not hide here.
	for _, frame := range append(append([]map[string]any{}, firstFrames...), secondFrames...) {
		assert.NotContains(t, frame, "principal", "no gateway frame may carry provenance: %v", frame)
		assert.NotContains(t, frame, "ordinal", "no gateway frame may carry provenance: %v", frame)
	}
	raw, err := os.ReadFile(filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl"))
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		var line map[string]any
		if decodeErr := decoder.Decode(&line); decodeErr != nil {
			require.ErrorIs(t, decodeErr, io.EOF, "transcript must contain complete JSONL entries")
			break
		}
		assert.NotContains(t, line, "principal", "the client-readable transcript entry must not carry provenance")
		assert.NotContains(t, line, "ordinal", "the client-readable transcript entry must not carry provenance")
	}
}

func TestAgentToolTranscriptWrite_MintsNoProvenance(t *testing.T) {
	// D1.4: generic transcript writers — the pkg/agent and pkg/tools paths
	// that call AppendTranscript/Strict — must not mint provenance. Only the
	// authenticated web-gateway user append does.
	const userContent = "D1.4 user message beside an agent write"
	handler, _, store, wc := newProvenanceHarness(t)

	frames := provenanceSendAuthenticated(t, handler, wc, "prov-agent-chat", "", userContent, "prov-agent-m1")
	sessionID := issue1090StartedSessionID(t, frames)
	entries := issue1090DiskTranscriptEntries(t, store, sessionID)
	require.Len(t, entries, 1, "control: the user message is on disk")
	userEntryID := entries[0].ID

	agentEntry := session.TranscriptEntry{
		ID:        uuid.NewString(),
		Role:      "assistant",
		AgentID:   "mia",
		Content:   "agent-authored answer",
		Timestamp: time.Now().UTC(),
	}
	require.NoError(t, store.AppendTranscript(sessionID, agentEntry),
		"control: the generic agent/tool append succeeds")

	_, found, err := store.LookupMessageProvenance(sessionID, agentEntry.ID)
	require.NoError(t, err)
	assert.False(t, found, "an agent/tool transcript write must not mint provenance")

	stillThere, found, err := store.LookupMessageProvenance(sessionID, userEntryID)
	require.NoError(t, err)
	require.True(t, found, "the agent write must not disturb the user record")
	assert.Equal(t, int64(1), stillThere.Ordinal, "the agent write must not move the ordinal")

	records := readProvenanceRecordsForTest(t, store, sessionID)
	require.Len(t, records, 1, "exactly one provenance record exists — the user message's")
	assert.Equal(t, userEntryID, records[0]["message_id"], "the single record keys the user's server message id")
}

// readProvenanceRecordsForTest reads the session's raw provenance record file
// so a test can count what the store actually wrote.
func readProvenanceRecordsForTest(t *testing.T, store *session.UnifiedStore, sessionID string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(store.BaseDir(), sessionID, "provenance.jsonl"))
	require.NoError(t, err, "the provenance record file must exist after an authenticated append")
	decoder := json.NewDecoder(bytes.NewReader(raw))
	records := []map[string]any{}
	for {
		var record map[string]any
		if decodeErr := decoder.Decode(&record); decodeErr != nil {
			require.ErrorIs(t, decodeErr, io.EOF, "provenance records must be complete JSONL")
			break
		}
		records = append(records, record)
	}
	return records
}
