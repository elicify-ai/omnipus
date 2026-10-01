// Copyright (c) 2026 Omnipus contributors
// License: MIT

package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// RED plan — #1081 R3, B-60 / MAJ-CW-010 and MAJ-CW-011:
//   - Outcome: one turn's diagnostics, actual EventBus turn.end, real WS done,
//     durable entries and real reattach replay must agree, despite narration.
//   - Oracle: ADR "Context budget and tool-result routing", amendment §18.4;
//     context-overflow spec B-60; tool-iteration-limit spec, Machine-Verifiable
//     Constraints / Tool-limit message (exact sentence below).
//   - Cases: one narrated tool round at cap=1; silent cap=1 control; narrated
//     tool round then a successful final answer when cap=2 permits it.
//   - Numeric boundaries here are exhausted (1) vs an available second round
//     (2). Invalid/zero/1000 limits belong to limit-validation tests, not this
//     outcome acceptance; cancellations/provider failures/duplicate definitions
//     remain other lanes. No implementation-specific cap error code is invented.
//   - Full DOM/live/reconnect/reload is the companion terminal-outcome.spec.ts.
//     This Go test does NOT claim browser rendering from a frame capture.
//   - CHECK mutations, deferred: change cap status back to completed; remove
//     failed-done propagation; suppress the distinct terminal persistence or
//     live delivery after narration; duplicate the terminal replay entry.
//   - GREEN, mutation proof and full-suite assurance are deferred to CHECK/CI.
const terminalAcceptanceCapNotice = "I've reached this agent's limit of tool steps for one turn without a final response. An admin can raise the limit in Settings → Performance (\"Max tool calls per turn\"), and each agent's own lower limit is on its profile's Advanced tab."

func TestWS_TerminalOutcome_IterationCap_AgreesAcrossSurfaces(t *testing.T) {
	const narration = "I will run the acceptance probe."
	const answer = "The acceptance probe completed."
	cases := []struct {
		name       string
		narration  string
		limit      int
		wantText   string
		wantCalls  int32
		wantFailed bool
		wantStatus agent.TurnEndStatus
	}{
		{"narrated_cap", narration, 1, terminalAcceptanceCapNotice, 1, true, agent.TurnEndStatusError},
		{"silent_cap_control", "", 1, terminalAcceptanceCapNotice, 1, true, agent.TurnEndStatusError},
		{"second_round_success_control", narration, 2, answer, 2, false, agent.TurnEndStatusCompleted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// No parallelism: logging is process-global. Restore it only after
			// the real loop and relay have been joined by fixture cleanup.
			oldLevel := logger.GetLevel()
			logger.SetLevel(logger.INFO)
			t.Cleanup(func() { logger.SetLevel(oldLevel) })
			logPath := filepath.Join(t.TempDir(), "terminal-outcome.jsonl")
			require.NoError(t, logger.EnableFileLogging(logPath))
			t.Cleanup(logger.DisableFileLogging)
			provider := &terminalAcceptanceProvider{narration: tc.narration, answer: answer}
			probe := &terminalAcceptanceProbe{}
			handler, sub, delivered := newTerminalAcceptanceHandler(t, provider, probe, tc.limit)
			store := handler.agentLoop.GetSessionStore()
			require.NotNil(t, store, "fixture: real durable transcript store")
			meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
			require.NoError(t, err)
			srv := httptest.NewServer(handler)
			t.Cleanup(srv.Close)
			t.Cleanup(handler.Wait)
			conn := dialTestWS(t, srv)
			t.Cleanup(func() { assert.NoError(t, conn.Close()) })
			sendWSAuthFrameDevMode(t, conn)
			agentID := "mia"
			require.NoError(t, conn.WriteJSON(generated.MessageFrame{
				Type: "message", Content: "Run the acceptance probe.", AgentId: &agentID, SessionId: &meta.ID,
			}))
			select {
			case delivery := <-delivered:
				require.NoError(t, delivery.err, "fixture: real outbound webchat delivery completed")
				require.Equal(t, tc.wantText, delivery.message.Content, "fixture reached the intended terminal branch")
			case <-time.After(busDeliveryTimeout):
				t.Fatal("fixture: no outbound delivery from the real turn")
			}
			require.Equal(t, tc.wantCalls, provider.calls.Load(), "fixture: exact provider-round boundary")
			require.Equal(t, int32(1), probe.calls.Load(), "fixture: one tool operation actually executed")

			frames := terminalAcceptanceCollect(t, conn)
			done := terminalAcceptanceTurnDone(t, frames, meta.ID)
			failed := done.Stats.TurnFailed != nil && *done.Stats.TurnFailed
			// Part B precondition must PASS before the lifecycle assertion. Do
			// not select the done frame by TurnFailed: that would hide a mutant.
			require.Equal(t, tc.wantFailed, failed, "done.stats.turn_failed must carry this turn's outcome")
			t.Logf("DONE VERIFIED: session=%s turn=%s done.stats.turn_failed=%t", meta.ID, *done.TurnId, failed)

			end := terminalAcceptanceTurnEnd(t, sub)
			payload, ok := end.Payload.(agent.TurnEndPayload)
			require.True(t, ok, "actual EventBus payload must be TurnEndPayload")
			require.Equal(t, *done.TurnId, end.Meta.TurnID, "done and turn.end must describe the SAME turn")
			require.Equal(t, meta.ID, payload.SessionID, "turn.end must describe the SAME session")
			assert.True(t, payload.IsRoot, "fixture must exercise the root turn, not a child")
			assert.Equal(t, int(tc.wantCalls), payload.Iterations, "lifecycle iteration count agrees with actual rounds")
			t.Logf("JOINT OUTCOME: turn=%s done.stats.turn_failed=%t turn.end.status=%s expected_status=%s",
				end.Meta.TurnID, failed, payload.Status, tc.wantStatus)
			assert.Equal(t, tc.wantStatus, payload.Status,
				"same-turn contradiction: done.stats.turn_failed=%t but turn.end.status=%s", failed, payload.Status)
			assert.Zero(t, handler.agentLoop.EventDrops(agent.EventKindTurnEnd), "lifecycle observation must not be lossy")
			terminalAcceptanceAssertLog(t, logPath, end, tc.wantStatus, tc.wantText)

			entries, err := store.ReadTranscript(meta.ID)
			require.NoError(t, err)
			var contents []string
			var terminalEntries []session.TranscriptEntry
			for _, entry := range entries {
				if entry.Role != "assistant" || entry.Content == "" {
					continue
				}
				assert.Equal(t, end.Meta.TurnID, entry.TurnID, "durable assistant entry belongs to the SAME turn")
				assert.Equal(t, agentID, entry.AgentID, "durable producer matches actual lifecycle producer")
				contents = append(contents, entry.Content)
				if entry.Content == tc.wantText {
					terminalEntries = append(terminalEntries, entry)
				}
			}
			wantContents := []string{tc.wantText}
			if tc.narration != "" {
				wantContents = []string{tc.narration, tc.wantText}
			}
			assert.Equal(t, wantContents, contents, "durable narration must not substitute for the distinct terminal outcome")
			assert.Len(t, terminalEntries, 1, "exactly one durable outcome for this turn")
			live, liveByID := terminalAcceptanceLiveText(t, frames, meta.ID, end.Meta.TurnID)
			assert.Len(t, liveByID, len(wantContents), "live message count agrees with the independent outcome oracle")
			for _, entry := range entries {
				if entry.Role == "assistant" && entry.Content != "" {
					assert.Equal(t, entry.Content, liveByID[entry.ID], "live and durable content share the SAME entry identity")
				}
			}
			assert.Equal(t, tc.narration+tc.wantText, live, "live wire must carry the same narration and final outcome as durability")
			assert.Equal(t, 1, strings.Count(live, tc.wantText), "live terminal notice appears exactly once")
			if !tc.wantFailed {
				assert.NotContains(t, live, terminalAcceptanceCapNotice, "success must not inherit a failed outcome")
			}

			// Two fresh clients drive the real attach/replay boundary; the
			// companion browser test additionally proves actual DOM reload.
			for attempt := 0; attempt < 2; attempt++ {
				replayConn := dialTestWS(t, srv)
				t.Cleanup(func() { assert.NoError(t, replayConn.Close()) })
				sendWSAuthFrameDevMode(t, replayConn)
				require.NoError(t, replayConn.WriteJSON(generated.AttachSessionFrame{Type: "attach_session", SessionId: meta.ID}))
				replayed := terminalAcceptanceCollect(t, replayConn)
				terminalAcceptanceAssertReplay(t, replayed, meta.ID, end.Meta.TurnID, contents, terminalEntries, tc.wantText)
			}
		})
	}
}

func terminalAcceptanceTurnDone(t *testing.T, frames []collectedFrame, sessionID string) generated.DoneFrame {
	t.Helper()
	var done []generated.DoneFrame
	for _, frame := range frames {
		if frame.Type != "done" {
			continue
		}
		var f generated.DoneFrame
		require.NoError(t, json.Unmarshal(frame.Raw, &f))
		// Actual turn completion vs replay terminator or outbound fallback.
		if f.SessionId == sessionID && f.Stats != nil && f.Stats.Tokens != nil {
			done = append(done, f)
		}
	}
	require.Len(t, done, 1, "one actual streaming turn completion; frames=%v", collectedFrameTypes(frames))
	require.NotNil(t, done[0].TurnId, "done must identify the turn independently of its failed flag")
	require.NotEmpty(t, *done[0].TurnId)
	return done[0]
}

func terminalAcceptanceTurnEnd(t *testing.T, sub agent.EventSubscription) agent.Event {
	t.Helper()
	var ends []agent.Event
	for {
		select {
		case event, ok := <-sub.C:
			require.True(t, ok, "event subscription closed before observation")
			if event.Kind == agent.EventKindTurnEnd {
				ends = append(ends, event)
			}
		default:
			require.Len(t, ends, 1, "one actual EventBus turn.end after synchronous outbound delivery")
			return ends[0]
		}
	}
}

func terminalAcceptanceAssertLog(t *testing.T, path string, end agent.Event, wantStatus agent.TurnEndStatus, wantText string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var ends, responses []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &row), "real production diagnostic must be readable")
		if row["turn_id"] == end.Meta.TurnID && row["session_key"] == end.Meta.SessionKey && row["event_kind"] == agent.EventKindTurnEnd.String() {
			ends = append(ends, row)
		}
		message, _ := row["message"].(string)
		if row["session_key"] == end.Meta.SessionKey && strings.HasPrefix(message, "Response: ") {
			responses = append(responses, row)
		}
	}
	require.Len(t, ends, 1, "real log reader must observe this turn's end, not accept an empty log")
	assert.Equal(t, string(wantStatus), ends[0]["status"], "logged outcome must agree with the failed/successful done flag")
	assert.Equal(t, end.Meta.AgentID, ends[0]["agent_id"])
	assert.Equal(t, end.Meta.SessionKey, ends[0]["session_key"])
	require.Len(t, responses, 1, "diagnostics must name the genuine terminal reason for the same session")
	// The production response diagnostic is a preview. Compare the COMPLETE
	// first sentence from the spec, not a string derived from its truncator.
	firstSentence := strings.Split(wantText, ". ")[0]
	if !strings.HasSuffix(firstSentence, ".") {
		firstSentence += "."
	}
	message, ok := responses[0]["message"].(string)
	require.True(t, ok, "diagnostic reason must be a string message; actual=%v", responses[0]["message"])
	assert.True(t, strings.HasPrefix(message, "Response: "+firstSentence),
		"diagnostic reason must match the complete specified terminal sentence; actual=%v", responses[0]["message"])
}

func terminalAcceptanceAssertReplay(
	t *testing.T, frames []collectedFrame, sessionID, turnID string,
	contents []string, terminalEntries []session.TranscriptEntry, wantText string,
) {
	t.Helper()
	var replayContents, terminalIDs []string
	for _, frame := range frames {
		if frame.Type != "replay_message" {
			continue
		}
		var replay generated.ReplayMessageFrame
		require.NoError(t, json.Unmarshal(frame.Raw, &replay))
		if replay.SessionId != sessionID || replay.Role != "assistant" || replay.Content == "" {
			continue
		}
		require.NotNil(t, replay.TurnId, "real replay must preserve the lifecycle turn identity")
		assert.Equal(t, turnID, *replay.TurnId)
		replayContents = append(replayContents, replay.Content)
		if replay.Content == wantText {
			require.NotNil(t, replay.Id, "replay outcome must preserve the durable entry identity")
			terminalIDs = append(terminalIDs, *replay.Id)
		}
	}
	assert.Equal(t, contents, replayContents, "reattach replay must equal this same turn's actual transcript")
	durableIDs := make([]string, 0, len(terminalEntries))
	for _, entry := range terminalEntries {
		durableIDs = append(durableIDs, entry.ID)
	}
	assert.Equal(t, durableIDs, terminalIDs, "replay and transcript must refer to the same terminal entry")
	assert.Len(t, terminalIDs, 1, "a missing or duplicated durable notice must not pass the replay oracle")
}
