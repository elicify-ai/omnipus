package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/require"
)

const pagingCarrierGroups = 80
const pagingCarrierBudget = 4 * 1024 * 1024
const pagingCarrierEnvelope = 8192

type pagingCarrierBoundaryCase struct { // not-wire-format: independent fixture/budget oracle.
	name, kind                                   string
	want, restCut, wsCut                         int
	restPayload, wsPayload, restHeader, wsHeader int
}

func pagingCarrierBoundaryCases() []pagingCarrierBoundaryCase {
	return []pagingCarrierBoundaryCase{
		{"tool_WS_cut36_REST_cut63", "tool", 36, 63, 36, 64 * 1024, 112 * 1024, 512, 1024},
		{"special_REST_cut31_WS_cut63", "verdict", 31, 31, 63, 128 * 1024, 64 * 1024, 1024, 768},
	}
}

func pagingCarrierBoundaryID(kind string, i int) string {
	if kind == "verdict" {
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
	}
	return fmt.Sprintf("carrier-tool-%03d", i)
}

func pagingCarrierBoundaryEntry(t *testing.T, sid, kind string, i int) session.TranscriptEntry {
	t.Helper()
	at := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	e := session.TranscriptEntry{ID: pagingCarrierBoundaryID(kind, i), AgentID: "mia", Timestamp: at, ViewMembership: session.ViewMembershipChat}
	if kind == "tool" {
		// Contracts leave opaque call IDs unbounded. Keep tool NAME ordinary;
		// long ASCII IDs are retained once by REST, twice by start/result WS.
		callID := strings.Repeat("c", 48*1024-3) + fmt.Sprintf("%03d", i)
		e.Type = session.EntryTypeToolCall
		e.ToolCalls = []session.ToolCall{{ID: session.ToolCallID(callID), Tool: "read_file", Status: "success",
			Parameters: map[string]any{}, Result: map[string]any{"text": strings.Repeat("x", 16*1024)}}}
		return e
	}
	require.Equal(t, "verdict", kind)
	verdict := task.JudgeVerdict{ID: e.ID, Scope: "goal", GoalSessionID: sid, Round: 1, Met: true,
		PerCriterion: []task.CriterionVerdict{{CriterionID: "frozen-criterion", Met: true, Reason: strings.Repeat("v", 64*1024)}},
		Model:        "frozen-size-model", JudgedAt: at.Format(time.RFC3339), JudgeAgentID: "judge"}
	body, err := json.Marshal(verdict)
	require.NoError(t, err)
	e.Type, e.Role, e.Content = session.EntryTypeJudgeVerdict, "system", string(body)
	return e
}

// Build the contract's expected encodings from frozen INPUT, never from a
// production projector or observed page. These validate framing headroom and
// complete source data; the expected cut counts above are already fixed.
func pagingCarrierBoundaryReference(t *testing.T, sid string, e session.TranscriptEntry, position string) (generated.Message, [][]byte) {
	t.Helper()
	body, err := json.Marshal(e)
	require.NoError(t, err)
	var message generated.Message
	require.NoError(t, json.Unmarshal(body, &message))
	message.HistoryPosition = &position
	id, agentID := e.ID, e.AgentID
	origin := &generated.HistoryEntryOrigin{Position: position, EntryId: &id}
	frames := make([][]byte, 0, 2)
	appendFrame := func(frame any) {
		raw, encodeErr := json.Marshal(frame)
		require.NoError(t, encodeErr)
		frames = append(frames, raw)
	}
	if e.Type == session.EntryTypeToolCall {
		call := e.ToolCalls[0]
		zero := 0
		appendFrame(generated.ToolCallStartFrame{Type: "tool_call_start", SessionId: sid, CallId: string(call.ID), Tool: call.Tool,
			Params: map[string]any{}, AgentId: &agentID, HistoryOrigin: origin})
		appendFrame(generated.ToolCallResultFrame{Type: "tool_call_result", SessionId: sid, CallId: string(call.ID), Tool: call.Tool,
			Result: call.Result, Status: "success", DurationMs: &zero, AgentId: &agentID, HistoryOrigin: origin})
	} else {
		var verdict generated.JudgeVerdict
		require.NoError(t, json.Unmarshal([]byte(e.Content), &verdict))
		// Message's generated inline verdict is deliberately a distinct Go
		// type. Decode its contract-shaped JSON instead of inventing a wire
		// struct or assigning the standalone schema's different named type.
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(body, &fields))
		fields["verdict"], err = json.Marshal(verdict)
		require.NoError(t, err)
		wire, err := json.Marshal(fields)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(wire, &message))
		message.HistoryPosition = &position
		var frame generated.JudgeVerdictFrame
		require.NoError(t, json.Unmarshal([]byte(e.Content), &frame))
		frame.Type, frame.SessionId, frame.HistoryOrigin = "judge_verdict", &sid, origin
		appendFrame(frame)
	}
	return message, frames
}

func pagingCarrierBoundaryMath(t *testing.T, tc pagingCarrierBoundaryCase) {
	t.Helper()
	for _, bound := range []struct {
		name                 string
		cut, payload, header int
	}{{"REST", tc.restCut, tc.restPayload, tc.restHeader}, {"WS", tc.wsCut, tc.wsPayload, tc.wsHeader}} {
		require.Greater(t, bound.cut, 20, "byte boundary is beyond the independent floor")
		require.Less(t, bound.cut, 100, "byte boundary precedes the row target")
		require.LessOrEqual(t, bound.cut*(bound.payload+bound.header)+pagingCarrierEnvelope, pagingCarrierBudget,
			"frozen %s cut fits even at maximum framing headroom", bound.name)
		require.Greater(t, (bound.cut+1)*bound.payload+1, pagingCarrierBudget,
			"next %s group cannot fit even at minimum required JSON overhead", bound.name)
	}
	require.NotEqual(t, tc.restCut, tc.wsCut, "two carrier-only decisions really diverge")
	require.Equal(t, min(tc.restCut, tc.wsCut), tc.want, "larger carrier controls selection")
}

func pagingCarrierBoundarySeed(t *testing.T, h *historyD16Harness, sid string, tc pagingCarrierBoundaryCase) []session.ArchiveAddress {
	t.Helper()
	pagingCarrierBoundaryMath(t, tc)
	addresses := make([]session.ArchiveAddress, 0, pagingCarrierGroups)
	for i := 1; i <= pagingCarrierGroups; i++ {
		e := pagingCarrierBoundaryEntry(t, sid, tc.kind, i)
		encoded, err := json.Marshal(session.ArchiveRecord{TranscriptEntry: e})
		require.NoError(t, err)
		require.Less(t, len(encoded), 8*1024*1024, "real canonical admission bound")
		address, err := h.store.AppendTranscriptAddressed(sid, e)
		require.NoError(t, err)
		addresses = append(addresses, address)
		message, frames := pagingCarrierBoundaryReference(t, sid, e, pagingPositionOracle(sid, address))
		rest, err := json.Marshal(message)
		require.NoError(t, err)
		ws := 0
		for _, frame := range frames {
			ws += len(frame)
		}
		require.Greater(t, len(rest), tc.restPayload)
		require.LessOrEqual(t, len(rest), tc.restPayload+tc.restHeader, "independent REST fixture class")
		require.Greater(t, ws, tc.wsPayload)
		require.LessOrEqual(t, ws, tc.wsPayload+tc.wsHeader, "independent WS fixture class")
	}
	require.NoError(t, h.store.FlushSessionStats(sid))
	return addresses
}
