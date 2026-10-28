package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// T84 / S11d / B87,B95. Oracles are the persisted input literals, not the
// current turn, neighbouring entries or another carrier's observed output.
func TestHistoryOrigin_TurnIDFromPersistedSource(t *testing.T) {
	cases := []struct {
		name, payload string
		frames        []string
	}{
		{"prose", `{"role":"assistant","content":"saved prose"}`, []string{"replay_message"}},
		{"silent_accounting", `{"role":"assistant","content":"","tokens":17}`, []string{"replay_message"}},
		{"tool_only", `{"type":"tool_call","role":"assistant","tool_calls":[{"id":"s11-call","tool":"read_file","status":"success","parameters":{"path":"fixture.txt"},"result":{"text":"saved result"}}]}`, []string{"tool_call_start", "tool_call_result"}},
		{"error", `{"type":"system","role":"system","status":"error","content":"saved error","error_code":"provider_auth_failed","provider_message":true}`, []string{"replay_error"}},
		{"fallback", `{"type":"provider_fallback","role":"system","content":"Answered by fallback because primary was unavailable.","unavailable_model":"primary","model":"fallback","unavailable_code":"rate_limited"}`, []string{"replay_provider_fallback"}},
		{"context_notice", `{"type":"context_window_notice","role":"system","context_window_notice":{"kind":"mid_turn","message":"bounded context notice"}}`, []string{"context_window_notice"}},
		{"judge", `{"type":"judge_verdict","role":"system","content":"{\"id\":\"00000000-0000-4000-8000-000000000001\",\"scope\":\"goal\",\"goal_session_id\":\"goal-session\",\"round\":1,\"met\":true,\"per_criterion\":[],\"model\":\"test-model\",\"judged_at\":\"2026-10-21T12:00:00Z\"}"}`, []string{"judge_verdict"}},
		{"handover", `{"type":"system","role":"system","system_subtype":"browser_handover_notice","content":"Operator took control"}`, []string{"browser_handover_notice"}},
		{"helper", `{"type":"system","role":"system","system_subtype":"helper_event","helper_event":{"helper_session_id":"helper","kind":"finished","text":"Helper finished","created_at":"2026-10-21T12:00:00Z"}}`, []string{"helper_event"}},
		{"goal", `{"type":"system","role":"system","system_subtype":"goal_outcome","goal_outcome":{"goal_id":"goal","goal_text":"Ship","ending":"met","ended_at":"2026-10-21T12:00:00Z","max_rounds":2,"rounds_used":1}}`, []string{"goal_outcome"}},
		{"span_start", `{"type":"system","system_subtype":"subagent_start","subagent_start":{"type":"subagent_start","session_id":"restamped","span_id":"span_call","parent_call_id":"call","task_label":"Task"}}`, []string{"subagent_start"}},
		{"span_state", `{"type":"system","system_subtype":"subagent_state","subagent_state":{"type":"subagent_state","session_id":"restamped","span_id":"span_call","state":"running","created_at":"2026-10-21T12:00:00Z"}}`, []string{"subagent_state"}},
		{"span_message", `{"type":"system","system_subtype":"subagent_message","subagent_message":{"type":"subagent_message","session_id":"restamped","span_id":"span_call","kind":"progress","message_id":"progress","sender_identity":"worker","untrusted_origin":false,"text":"Progress","created_at":"2026-10-21T12:00:00Z"}}`, []string{"subagent_message"}},
		{"span_end", `{"type":"system","system_subtype":"subagent_end","subagent_end":{"type":"subagent_end","session_id":"restamped","span_id":"span_call","status":"success","parent_call_id":"call"}}`, []string{"subagent_end"}},
	}
	for _, tc := range cases {
		turns := []string{"s11-recorded-turn", ""}
		// A classified context notice is required to record a non-empty turn
		// and original timestamp at append time; an unknown notice is invalid.
		if tc.name == "context_notice" {
			turns = turns[:1]
		}
		for _, turn := range turns {
			t.Run(fmt.Sprintf("%s/turn=%q", tc.name, turn), func(t *testing.T) {
				s, sid, home := pagingProjectionFixture(t)
				var source session.TranscriptEntry
				require.NoError(t, json.Unmarshal([]byte(tc.payload), &source))
				source.ID, source.AgentID, source.TurnID = "s11-"+tc.name, "mia", turn
				source.ViewMembership = session.ViewMembershipChat
				source.Timestamp = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
				if source.SubagentStart != nil {
					source.SubagentStart.SessionId = sid
				}
				if source.SubagentState != nil {
					source.SubagentState.SessionId = sid
				}
				if source.SubagentMessage != nil {
					source.SubagentMessage.SessionId = sid
				}
				if source.SubagentEnd != nil {
					source.SubagentEnd.SessionId = sid
				}
				address, err := s.AppendTranscriptAddressed(sid, source)
				require.NoError(t, err, "instrument: valid canonical source, not an unrelated append refusal")
				request, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 20, MinRows: 1}, historyPageProjectionDeps{ToolStore: newToolResultStore(home)})
				require.NoError(t, err)
				prepared, err := request.Read(context.Background(), s)
				require.NoError(t, err, "real source emitter and generated frame validation")
				require.Len(t, prepared.Response.Entries, 1)
				require.Equal(t, 1, prepared.Response.PageInfo.SourceGroupCount)
				position := pagingPositionOracle(sid, address)
				require.Equal(t, &position, prepared.Response.Entries[0].HistoryPosition)
				var gotTypes []string
				for _, raw := range prepared.ReplayFrames {
					var fields map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(raw, &fields))
					var kind string
					require.NoError(t, json.Unmarshal(fields["type"], &kind))
					if kind == "done" {
						require.NotContains(t, fields, "history_origin")
						continue
					}
					gotTypes = append(gotTypes, kind)
					require.NoError(t, validateHistorySourceFrame(raw), "generated schema for %s", kind)
					var origin generated.HistoryEntryOrigin
					require.NoError(t, json.Unmarshal(fields["history_origin"], &origin))
					require.Equal(t, position, origin.Position)
					require.Equal(t, &source.ID, origin.EntryId)
					var object map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(fields["history_origin"], &object))
					if turn == "" {
						require.Nil(t, origin.TurnId)
						require.NotContains(t, object, "turn_id", "unknown is omitted, not null/empty/guessed")
					} else {
						require.Equal(t, &turn, origin.TurnId, "S11d: every origin-stamped frame carries its own persisted turn")
					}
				}
				require.Equal(t, tc.frames, gotTypes, "S11d changes no source-frame inventory/order")
			})
		}
	}
	t.Run("extended_media_and_question_carriers", s11OriginExtendedClasses)
	t.Run("unknown_source_during_real_other_active_turn", s11UnknownOriginDuringActiveTurn)
	t.Run("generated_origin_schema_instrument", func(t *testing.T) {
		valid := generated.HistoryEntryOrigin{Position: pagingPositionOracle("s11", session.ArchiveAddress{PartitionKey: "2026-10-28", EntryID: "s11"})}
		raw, err := json.Marshal(valid)
		require.NoError(t, err)
		msg, server := validateBodyAgainstSchema("HistoryEntryOrigin", raw)
		require.False(t, server)
		require.Empty(t, msg, "instrument: optional unknown turn is valid")
		empty := ""
		valid.TurnId = &empty
		raw, err = json.Marshal(valid)
		require.NoError(t, err)
		msg, server = validateBodyAgainstSchema("HistoryEntryOrigin", raw)
		require.False(t, server)
		require.NotEmpty(t, msg, "instrument: minLength1 rejects an empty known-turn sentinel")
	})
}

// Only the external provider is held. The actual engine supplies active_turn;
// the test does not install a fake session-state frame or patch engine state.
type s11HeldOriginProvider struct {
	*historyD16Provider
	entered chan struct{}
}

func (p *s11HeldOriginProvider) ChatStream(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any, onChunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	onChunk("S11_LIVE_TOKEN")
	close(p.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}
func s11UnknownOriginDuringActiveTurn(t *testing.T) {
	home, cfg := historyD16Config(t)
	provider := &s11HeldOriginProvider{historyD16Provider: &historyD16Provider{}, entered: make(chan struct{})}
	h := newHistoryD16Harness(t, home, cfg, provider, true)
	sid := h.session()
	peer := h.connect()
	h.attach(peer, sid)
	h.send(peer, sid, "s11-active-instruction", "Hold the synthetic external response")
	historyD16Await(t, provider.entered, "real provider entered while foreground turn is active")
	turn, _, _, active := h.loop.ActiveForegroundTurnInfo(sid)
	require.True(t, active)
	require.NotEmpty(t, turn)
	unknown := session.TranscriptEntry{ID: "s11-unknown-during-active", Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia", Timestamp: time.Now().UTC(), ViewMembership: session.ViewMembershipChat,
		ToolCalls: []session.ToolCall{{ID: "s11-unknown-call", Tool: "read_file", Status: "success", Parameters: map[string]any{}, Result: map[string]any{"text": "saved unknown turn"}}}}
	address, err := h.store.AppendTranscriptAddressed(sid, unknown)
	require.NoError(t, err)
	frames := h.attach(h.connect(), sid)
	seenState, seenUnknown, seenLive := false, 0, 0
	for _, raw := range frames {
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &fields))
		var kind string
		require.NoError(t, json.Unmarshal(fields["type"], &kind))
		if kind == "session_state" {
			var state generated.SessionStateFrame
			require.NoError(t, json.Unmarshal(raw, &state))
			require.NotNil(t, state.ActiveTurn)
			require.Equal(t, turn, state.ActiveTurn.TurnId)
			seenState = true
		}
		if kind == "token" {
			var token generated.TokenFrame
			require.NoError(t, json.Unmarshal(raw, &token))
			require.Equal(t, "S11_LIVE_TOKEN", token.Content)
			require.NotContains(t, fields, "history_origin", "live frames remain unchanged")
			seenLive++
		}
		if originRaw, ok := fields["history_origin"]; ok {
			var origin generated.HistoryEntryOrigin
			require.NoError(t, json.Unmarshal(originRaw, &origin))
			if origin.EntryId != nil && *origin.EntryId == unknown.ID {
				require.Equal(t, pagingPositionOracle(sid, address), origin.Position)
				require.Nil(t, origin.TurnId, "active turn is never inferred onto an unknown saved record")
				var object map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(originRaw, &object))
				require.NotContains(t, object, "turn_id")
				require.NoError(t, validateHistorySourceFrame(raw))
				seenUnknown++
			}
		}
	}
	require.True(t, seenState, "instrument: attach really occurred during another active turn")
	require.Equal(t, 2, seenUnknown, "unknown tool owns exactly its real start and result")
	require.Equal(t, 1, seenLive, "instrument: actual uncovered live token projected without origin")
}
