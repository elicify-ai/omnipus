package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Captured replay-fidelity (b), 7fec703d8: this is the initial canonical seed,
// before appendPersistedSubagentStart runs. D4 requires truthful pending tools
// and public parity; canonical storage acceptance must not hide a projection
// failure. Keep the legacy spawn/running and empty nested narration unchanged.
func TestHistoryReplay_CanonicalLegacyDelegationProjects(t *testing.T) {
	const seed = `[
		{"id":"entry-asst-spawn","role":"assistant","content":"Spawning a subagent.",
		 "timestamp":"2026-10-25T18:33:47Z","agent_id":"mia",
		 "tool_calls":[{"id":"c1","tool":"spawn","status":"running",
		 "parameters":{"task":"replay-fidelity-b-1792953229836"}}]},
		{"id":"entry-nested-1","role":"assistant","content":"",
		 "timestamp":"2026-10-25T18:33:48Z","agent_id":"ray",
		 "tool_calls":[{"id":"t2","tool":"shell","status":"success","duration_ms":20,
		 "parameters":{"cmd":"echo nested"},"result":{"stdout":"nested\n"},
		 "parent_tool_call_id":"c1"}]}
	]`
	for _, tc := range []struct {
		name, status string
		count        int
		terminal     bool
	}{
		{name: "parent_only", status: "running", count: 1},
		{name: "captured_seed", status: "running", count: 2},
		{name: "pending_parent", status: "pending", count: 1},
		{name: "successful_parent", status: "success", count: 1, terminal: true},
		{name: "failed_parent", status: "error", count: 1, terminal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAttachFixture(t, "canonical-"+tc.name, "canonical-reply")
			api := pagingAPIForAttach(f)
			store := f.store
			var err error
			var entries []session.TranscriptEntry
			require.NoError(t, json.Unmarshal([]byte(seed), &entries))
			entries[0].ToolCalls[0].Status = tc.status
			for _, entry := range entries[:tc.count] {
				_, err = store.AppendTranscriptAddressed(f.sid, entry)
				require.NoError(t, err, "same canonical writer as the captured fixture driver")
			}
			t.Run("shared_emitter", func(t *testing.T) {
				sr := &streamReplayState{sessionID: f.sid, entries: entries[:tc.count]}
				sr.prepareReplay()
				starts, ends, results := 0, 0, 0
				for i, entry := range sr.entries {
					require.NoError(t, sr.replayEntry(context.Background(), i, entry, nil, func(frame any) error {
						switch f := frame.(type) {
						case generated.ToolCallStartFrame:
							if f.CallId == "c1" {
								starts++
							}
						case generated.SubagentEndFrame:
							ends++
							require.True(t, tc.terminal, "pending legacy call has no recorded completion; got end status %q", f.Status)
							require.Equal(t, tc.status, f.Status)
						case generated.ToolCallResultFrame:
							if f.CallId == "c1" {
								results++
								require.True(t, tc.terminal, "pending legacy call must not invent a result")
								require.Equal(t, tc.status, f.Status)
							}
						}
						return nil
					}))
				}
				require.Equal(t, 1, starts)
				wantTerminals := 0
				if tc.terminal && tc.status == "success" {
					wantTerminals = 1
				}
				require.Equal(t, wantTerminals, ends, "only a completed launch, not a refusal, has a terminal span")
				require.Equal(t, map[bool]int{false: 0, true: 1}[tc.terminal], results)
			})
			t.Run("detail", func(t *testing.T) {
				detail := pagingDetail(t, api, f.sid)
				require.Equal(t, "available", detail.HistoryState.Status, "canonical seed detail: %+v", detail.HistoryState)
				require.Nil(t, detail.HistoryState.Reason)
				require.NotNil(t, detail.Messages)
				require.Len(t, *detail.Messages, tc.count)
				require.NotNil(t, (*detail.Messages)[0].Content)
				require.Equal(t, "Spawning a subagent.", *(*detail.Messages)[0].Content)
				require.NotNil(t, (*detail.Messages)[0].ToolCalls)
				require.Equal(t, tc.status, string((*(*detail.Messages)[0].ToolCalls)[0].Status))
			})
			t.Run("messages", func(t *testing.T) {
				response := pagingRequest(api, f.sid, "")
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			})
			for _, carrier := range []string{"prepared_projection", "snapshot"} {
				t.Run(carrier, func(t *testing.T) {
					var frames []wireFrame
					if carrier == "prepared_projection" {
						request, err := newHistoryPageRequest(f.sid, session.ChatHistoryPageOptions{}, api.historyProjectionDeps(store, f.sid))
						require.NoError(t, err)
						prepared, err := request.Read(context.Background(), store)
						for cause := err; cause != nil; cause = errors.Unwrap(cause) {
							t.Logf("projection cause: %T: %v", cause, cause)
						}
						require.NoError(t, err, "real canonical index reader and shared REST/WS projector")
						require.NotEmpty(t, prepared.ReplayFrames)
						for _, raw := range prepared.ReplayFrames {
							frames = append(frames, decodeWire(t, raw))
						}
					} else {
						viewer := f.newConn("canonical-snapshot-" + tc.name)
						f.h.handleAttachSession(context.Background(), "canonical-snapshot-"+tc.name, f.sid, nil, viewer)
						frames = pagingDrain(t, viewer)
						require.Contains(t, typesOf(frames), "session_snapshot", "available canonical history must prepare a real snapshot")
						require.Contains(t, typesOf(frames), "catch_up_complete")
					}
					starts, ends, results := 0, 0, 0
					for _, frame := range frames {
						raw := frame.raw
						switch frame.Type {
						case "tool_call_start":
							var start generated.ToolCallStartFrame
							require.NoError(t, json.Unmarshal(raw, &start))
							if start.CallId == "c1" {
								starts++
							}
						case "subagent_end":
							ends++
							var end generated.SubagentEndFrame
							require.NoError(t, json.Unmarshal(raw, &end))
							require.Equal(t, tc.status, end.Status)
						case "tool_call_result":
							var result generated.ToolCallResultFrame
							require.NoError(t, json.Unmarshal(raw, &result))
							if result.CallId == "c1" {
								results++
								require.Equal(t, tc.status, result.Status)
							}
						}
					}
					require.Equal(t, 1, starts)
					wantEnds, wantResults := 0, 0
					if tc.terminal {
						wantResults = 1
						if tc.status == "success" {
							wantEnds = 1
						}
					}
					require.Equal(t, wantEnds, ends, "prepared snapshots preserve completed legacy spans, never invent a pending end")
					require.Equal(t, wantResults, results)
				})
			}
		})
	}
}
