package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// #1090 D5 and the approved PR B SF-2/T2 rulings are the oracles: an
// ID-bearing unsaved first message gets a correlated not_saved error, never
// an acknowledgement/echo/turn. Failures after a possible write stay unknown.
// These tests do not change the legacy, no-client-ID size-refusal contract.

// newIssue1090BoundHarness configures the real limit before constructing the
// loop. The loop is not started: its real inbound buffer records every admitted
// turn, and the production serialization queues every response for inspection.
func newIssue1090BoundHarness(t *testing.T, bound int) (*WSHandler, *bus.MessageBus, *wsConn) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Context: config.ContextSettings{BuiltinSuccessCap: bound},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Model: "test-default-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	loop := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newWSHandler(msgBus, loop, "")
	t.Cleanup(handler.Wait)
	wc := makeTestConn()
	wc.userID = "red7be-user"
	t.Cleanup(wc.close)
	require.Equal(t, bound, loop.UserMessageBound(), "SF-2 fixture must use the explicitly configured live limit")
	return handler, msgBus, wc
}

func TestFirstUserMessage_OverBoundRejectsWithCorrelatedNotSaved(t *testing.T) {
	// A small, valid fixture cap (the contract permits 1..150000), not the
	// shipped default. SF-2 requires rejection at this configured max + 1.
	const bound = 64
	const clientID = "1090-sf2-over-bound"
	content := strings.Repeat("x", bound+1)
	handler, msgBus, wc := newIssue1090BoundHarness(t, bound)
	handler.handleChatMessageWithClientID(
		context.Background(), "1090-sf2-over-bound-chat", "", content, "mia", nil,
		"", "", false, clientID, nil, wc,
	)

	// Intake has returned; no writer or agent consumes these queues. Inspect
	// the entire stable output, not a timeout window that might miss a frame.
	frames := issue1090DrainQueuedFrames(t, wc)
	frameTypes := issue1090FrameTypes(frames)
	assert.NotContains(t, frameTypes, "session_started",
		"SF-2: an oversized first send must not produce an untagged (or tagged) session acknowledgement; frames=%v", frames)
	assert.NotContains(t, frameTypes, "user_message", "SF-2: an unsaved message must not be echoed as saved")
	assert.Equal(t, []any{"error"}, frameTypes, "SF-2: reject with the correlated first-message error, not a save acknowledgement")

	errorFrames := []generated.ErrorFrame{}
	for _, frame := range frames {
		if frame["type"] == "error" {
			raw, err := json.Marshal(frame)
			require.NoError(t, err)
			var errorFrame generated.ErrorFrame
			require.NoError(t, json.Unmarshal(raw, &errorFrame))
			errorFrames = append(errorFrames, errorFrame)
		}
	}
	id, kind := clientID, "not_saved"
	assert.Equal(t, []generated.ErrorFrame{{
		Type: string(generated.WsFrameTypeError), Message: "Could not save message",
		ClientMessageId: &id, FirstMessageError: &kind,
	}}, errorFrames, "SF-2: the exact no-save response must correlate the original client ID and disclose no session or filesystem details")

	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	entries := []session.TranscriptEntry{}
	for _, sessionID := range issue1090SessionDirectories(t, store) {
		entries = append(entries, issue1090DiskTranscriptEntries(t, store, sessionID)...)
	}
	assert.Equal(t, []session.TranscriptEntry{}, entries, "SF-2: the oversized first user entry must not reach any disk transcript")
	assert.Len(t, msgBus.InboundChan(), 0, "SF-2: the oversized first message must not be admitted to the turn bus")
	t.Logf("SF-2 configured limit=%d, content characters=%d, wire frame types=%v", bound, len(content), frameTypes)
}

func TestFirstUserMessage_AtBoundIsSavedAndAcknowledged(t *testing.T) {
	// SF-2 positive instrument control: exactly the same configured max is
	// accepted. It proves both the disk and admission checks can see a save.
	const bound = 64
	const clientID = "1090-sf2-at-bound"
	content := strings.Repeat("x", bound)
	handler, msgBus, wc := newIssue1090BoundHarness(t, bound)
	handler.handleChatMessageWithClientID(
		context.Background(), "1090-sf2-at-bound-chat", "", content, "mia", nil,
		"", "", false, clientID, nil, wc,
	)

	frames := issue1090DrainQueuedFrames(t, wc)
	require.Equal(t, []any{"session_started", "user_message", "message_status"}, issue1090FrameTypes(frames),
		"D5: a fresh saved first send acknowledges, echoes, then confirms received")
	sessionID := issue1090StartedSessionID(t, frames)
	assert.Equal(t, clientID, frames[0]["client_message_id"], "SF-2 control: the save acknowledgement must carry the original client ID")
	assert.NotEqual(t, true, frames[0]["recovered"], "SF-2 control: this is a fresh save, not a recovered duplicate")

	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	entries := issue1090AssertSavedFirstEntry(t, store, sessionID, content, clientID)
	assert.Equal(t, []string{sessionID}, issue1090SessionDirectories(t, store), "SF-2 control: exactly one session is saved")
	assert.Equal(t, entries[0].ID, frames[1]["id"], "SF-2 control: the echo must identify the real saved entry")
	assert.Equal(t, content, frames[1]["content"], "SF-2 control: the entire at-limit message must be echoed")
	assert.Equal(t, clientID, frames[1]["client_message_id"], "SF-2 control: the echo must correlate the saved message")
	assert.Equal(t, sessionID, frames[1]["session_id"], "SF-2 control: the echo belongs to the acknowledged session")
	assert.Equal(t, "received", frames[2]["state"], "SF-2 control: the receipt must report confirmed delivery")
	assert.Equal(t, clientID, frames[2]["client_message_id"], "SF-2 control: the receipt must correlate the saved message")
	assert.Equal(t, sessionID, frames[2]["session_id"], "SF-2 control: the receipt belongs to the saved session")

	require.Len(t, msgBus.InboundChan(), 1, "SF-2 control: exactly one turn must be admitted")
	inbound := <-msgBus.InboundChan()
	assert.Equal(t, content, inbound.Content, "SF-2 control: the admitted turn retains the full at-limit content")
	assert.Equal(t, sessionID, inbound.SessionID, "SF-2 control: admission belongs to the acknowledged saved session")
	t.Logf("SF-2 control: configured limit=%d, exactly %d characters saved, correlated ack/echo/received, one admission", bound, len(content))
}

func TestFirstMessageAppendFailure_ClassifiesSaveCertainty(t *testing.T) {
	// D5 + approved T2 ruling: mkdir/open fail before any write; write/sync/
	// close and errors without a path-operation proof may follow saved bytes.
	// The expected outcomes are fixed by that ruling, not classifier branches.
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"mkdir", &os.PathError{Op: "mkdir", Path: path, Err: os.ErrPermission}, "not_saved"},
		{"open", &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}, "not_saved"},
		{"write", &os.PathError{Op: "write", Path: path, Err: os.ErrPermission}, "delivery_unknown"},
		{"sync", &os.PathError{Op: "sync", Path: path, Err: os.ErrPermission}, "delivery_unknown"},
		{"close", &os.PathError{Op: "close", Path: path, Err: os.ErrPermission}, "delivery_unknown"},
		{"non_path", errors.New("append failed without a proven pre-write operation"), "delivery_unknown"},
	}
	for _, tc := range cases {
		for _, wrapping := range []string{"direct", "wrapped"} {
			t.Run(tc.name+"_"+wrapping, func(t *testing.T) {
				err := tc.err
				if wrapping == "wrapped" {
					err = fmt.Errorf("append transcript: %w", err)
				}
				assert.Equal(t, tc.want, firstMessageAppendFailure(err),
					"T2: %s %s must preserve the specified certainty about whether any entry was saved", wrapping, tc.name)
			})
		}
	}
}

func TestFirstUserMessage_AppendOpenFailureIsCorrelatedAndStopsTurn(t *testing.T) {
	// T2 outer control retains the existing crash-test setup unchanged: a real
	// first append pauses at the external entropy edge after session minting.
	const content = "T2 correlated append-open failure must not start a turn"
	const clientID = "1090-t2-correlated-open"
	handler, msgBus, wc, client, startWriter := newIssue1090ChatHarness(t)
	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	gate, intakeDone := issue1090PauseFirstAppend(t, handler, wc, content, clientID)
	transcript := issue1090OnlyTranscript(t, store)
	require.Equal(t, []string{}, issue1090DiskUserContents(t, transcript), "T2 fixture replaces only a pristine transcript")

	// A directory cannot be opened for append, including by a privileged user.
	// No gateway, store, or fileutil persistence function is mocked.
	require.NoError(t, os.Remove(transcript))
	require.NoError(t, os.Mkdir(transcript, 0o700))
	opened, appendErr := os.OpenFile(transcript, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if opened != nil {
		require.NoError(t, opened.Close())
	}
	var pathErr *os.PathError
	require.ErrorAs(t, appendErr, &pathErr, "T2 instrument must reject the real append open")
	require.Equal(t, "open", pathErr.Op, "T2 instrument fails before any write")
	require.Equal(t, transcript, pathErr.Path, "T2 instrument targets the actual new-session transcript")
	t.Logf("T2 instrument control: real transcript append-open fails: %v", appendErr)

	gate.release()
	issue1090Await(t, intakeDone, "correlated failed-append intake")
	wc.qmu.Lock()
	queued := len(wc.q) + len(wc.held)
	wc.qmu.Unlock()
	require.Zero(t, queued, "T2 must inspect every outbound frame, including any acknowledgement or echo")
	frameCount := len(wc.sendCh)
	startWriter()
	frames := make([]generated.ErrorFrame, 0, frameCount)
	frameTypes := make([]string, 0, frameCount)
	for range frameCount {
		var frame generated.ErrorFrame
		require.NoError(t, json.Unmarshal(issue1090ReadClientFrame(t, client), &frame))
		frames = append(frames, frame)
		frameTypes = append(frameTypes, frame.Type)
	}
	assert.Equal(t, []string{"error"}, frameTypes, "T2: no session_started, user echo, or save receipt may accompany the failed append")
	id, kind := clientID, "not_saved"
	assert.Equal(t, []generated.ErrorFrame{{
		Type: string(generated.WsFrameTypeError), Message: "Could not save message",
		ClientMessageId: &id, FirstMessageError: &kind,
	}}, frames, "T2: real append-open failure must return the exact correlated not_saved error, without session or filesystem details")
	assert.Len(t, msgBus.InboundChan(), 0, "T2: the failed append must not admit a turn")
	children, err := os.ReadDir(transcript)
	require.NoError(t, err)
	assert.Len(t, children, 0, "T2: the invalid append target remains empty; no user entry was written")
}
