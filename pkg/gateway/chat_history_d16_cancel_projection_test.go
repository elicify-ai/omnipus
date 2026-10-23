package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const historyD16SavedTurn = "d16-saved-producing-turn"
const historyD16SavedDuration = 3125 // frozen known display fact, not a timing tolerance

type historyD16SavedFixture struct {
	reader                      *historyD16Harness
	sid, owner, narration, next string
	wants                       []historyD16CallWant
	canonical                   map[string]string
}

// Projection-only control: the REAL canonical addressed writer/effect path
// creates saved facts. This is explicitly NOT evidence of engine durability;
// the separate actual engine/Stop cases are mandatory and remain loud RED.
func historyD16SavedFixtureFor(t *testing.T, edge bool) historyD16SavedFixture {
	t.Helper()
	home, cfg := historyD16Config(t)
	writer := newHistoryD16Harness(t, home, cfg, &historyD16Provider{}, false)
	sid := writer.session()
	fixture := historyD16SavedFixture{sid: sid}
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	if edge {
		pagingSeed(t, writer.store, sid, "d16-omitted-prefix", 60)
		narration, err := writer.store.AppendTranscriptAddressed(sid, session.TranscriptEntry{
			ID: "d16-saved-partial-reply", Role: "assistant", Content: "D16 separate interrupted reply",
			AgentID: "mia", TurnID: historyD16SavedTurn, Timestamp: at, ViewMembership: session.ViewMembershipChat,
		})
		require.NoError(t, err)
		fixture.narration = pagingPositionOracle(sid, narration)
	}
	calls := []session.ToolCall{{ID: historyD16CancelledID, Tool: historyD16ToolName, Status: "pending", Parameters: map[string]any{"step": "blocked"}}}
	fixture.wants = []historyD16CallWant{{historyD16CancelledID, "blocked", "cancelled", historyD16SavedTurn}}
	if edge {
		calls = append([]session.ToolCall{{ID: historyD16CompletedID, Tool: historyD16ToolName, Status: "success",
			Parameters: map[string]any{"step": "completed"}, Result: map[string]any{"text": "D16 completed result"}}}, calls...)
		fixture.wants = append([]historyD16CallWant{{historyD16CompletedID, "completed", "success", historyD16SavedTurn}}, fixture.wants...)
	}
	owner, err := writer.store.AppendTranscriptAddressed(sid, session.TranscriptEntry{
		ID: "d16-saved-call-owner", Type: session.EntryTypeToolCall, AgentID: "mia", TurnID: historyD16SavedTurn,
		Timestamp: at, ViewMembership: session.ViewMembershipChat, ToolCalls: calls,
	})
	require.NoError(t, err)
	fixture.owner = pagingPositionOracle(sid, owner)
	if edge {
		// 38 newer groups leave this two-tool source in the default40 snapshot
		// even when the zero-row cancel marker consumes a source slot.
		later := pagingSeed(t, writer.store, sid, "d16-newer-than-owner", 38)
		fixture.next = pagingPositionOracle(sid, later[0])
	}
	settled := session.ToolCall{ID: historyD16CancelledID, Tool: historyD16ToolName, Status: "cancelled", DurationMS: historyD16SavedDuration,
		Parameters: map[string]any{"step": "blocked"}}
	effect, err := writer.store.SettleToolCall(sid, owner, "pending", settled)
	require.NoError(t, err)
	require.Greater(t, effect.ByteOffset, owner.ByteOffset, "terminal effect is genuinely outside the selected owner record")
	_, err = writer.store.SettleToolCall(sid, owner, "pending", session.ToolCall{
		ID: historyD16CancelledID, Tool: historyD16ToolName, Status: "success", Parameters: map[string]any{"step": "blocked"},
		Result: map[string]any{"text": "D16 late success must not replace cancellation"},
	})
	require.NoError(t, err, "late guarded effect is append-only, but must not clobber terminal cancellation")
	require.NoError(t, writer.store.AppendTranscriptStrict(sid, session.TranscriptEntry{
		ID: "d16-saved-cancel-marker", Type: session.EntryTypeTurnCancelled, Role: "system", AgentID: "mia",
		TurnID: historyD16SavedTurn, CancelMethod: "hard", Timestamp: at, ViewMembership: session.ViewMembershipChat,
	}))
	historyD16CanonicalCalls(t, writer.store, sid, fixture.wants)
	fixture.canonical = pagingCanonicalBytes(t, writer.store, sid)
	writer.shutdown()
	fixture.reader = newHistoryD16Harness(t, home, cfg, &historyD16Provider{}, false)
	return fixture
}

func historyD16SavedCancellation(t *testing.T) {
	f := historyD16SavedFixtureFor(t, false)
	historyD16CanonicalCalls(t, f.reader.store, f.sid, f.wants)
	page := f.reader.page(f.sid, "")
	require.Equal(t, 1, page.PageInfo.RowCount, "D4/D16: actual cancelled tool1 + real turn_canceled metadata0, never start+result2")
	owners := historyD16PublicCalls(t, page.Entries, f.wants)
	owner, found := owners[historyD16CancelledID]
	require.True(t, found, "instrument: saved cancelled call is actually eligible before emitter assertion")
	require.NotNil(t, owner.ToolCalls)
	require.Len(t, *owner.ToolCalls, 1)
	call := (*owner.ToolCalls)[0]
	require.NotNil(t, call.DurationMs)
	assert.EqualValues(t, historyD16SavedDuration, *call.DurationMs, "known duration, not a fabricated success result")
	assert.Nil(t, call.Result, "no admitted output is truthfully absent")
	peer := f.reader.connect()
	frames := f.reader.attach(peer, f.sid)
	historyD16ReplayCalls(t, f.sid, frames, f.wants, owners)
	for _, raw := range frames {
		if historyD16Type(t, raw) == "tool_call_result" {
			var result generated.ToolCallResultFrame
			require.NoError(t, json.Unmarshal(raw, &result))
			if result.CallId == historyD16CancelledID {
				assert.Nil(t, result.Result, "required result is null when cancellation has no admitted output")
				if assert.NotNil(t, result.DurationMs) {
					assert.Equal(t, historyD16SavedDuration, *result.DurationMs)
				}
			}
		}
	}
	historyD16CancellationCarriers(t, f.reader, f.sid, f.wants)
	assert.Equal(t, f.canonical, pagingCanonicalBytes(t, f.reader.store, f.sid), "projection never rewrites or adds evidence")
}

func historyD16CancellationPageEdge(t *testing.T) {
	f := historyD16SavedFixtureFor(t, true)
	readLogs := pagingLogCapture(t)
	older := f.reader.page(f.sid, "?limit=20&min_rows=20&before="+f.next)
	// D3 minimum20 drawn rows: two tools in one owner + one narration +
	// seventeen one-row predecessors. The group is never split or counted4.
	wantIDs := append(pagingWantIDs("d16-omitted-prefix", 44, 60), "d16-saved-partial-reply", "d16-saved-call-owner")
	require.Equal(t, wantIDs, pagingIDs(older), "nearest older whole source groups, not their later terminal effects")
	require.Equal(t, 19, older.PageInfo.SourceGroupCount)
	require.Equal(t, 20, older.PageInfo.RowCount, "two logical tool rows + narration1 + ordinary17, never four tool frames or half a group")
	require.True(t, older.HasEarlier)
	require.True(t, older.HasLater)
	owners := historyD16PublicCalls(t, older.Entries, f.wants)
	rows := pagingSummaryRows(readLogs())
	require.Len(t, rows, 1)
	assert.EqualValues(t, 19, rows[0]["selected_archive_records"])
	assert.GreaterOrEqual(t, pagingCounter(t, rows[0], "dependency_archive_records"), float64(1), "outside terminal evidence actually read")
	assert.LessOrEqual(t, pagingCounter(t, rows[0], "lookahead_archive_records"), float64(1))
	assert.Less(t, pagingCounter(t, rows[0], "archive_records_read"), float64(60), "healthy older-page preparation cannot decode all60 prefix groups when only17 are selected")
	refresh := f.reader.page(f.sid, "?limit=20&min_rows=20&window_start="+f.owner+"&window_end="+f.owner)
	assert.Equal(t, "window_refresh", string(refresh.PageInfo.Mode))
	require.Equal(t, []string{"d16-saved-call-owner"}, pagingIDs(refresh), "closed already-displayed owner range may underfill20, not import unrelated neighbors")
	require.Equal(t, 1, refresh.PageInfo.SourceGroupCount)
	require.Equal(t, 2, refresh.PageInfo.RowCount, "whole two-tool group remains two rows at a closed page edge")
	assert.Equal(t, []generated.Message{older.Entries[len(older.Entries)-1]}, refresh.Entries, "same original terminal call group on older load and refresh")
	partial := f.reader.page(f.sid, "?limit=20&min_rows=20&window_start="+f.narration+"&window_end="+f.narration)
	require.Equal(t, []string{"d16-saved-partial-reply"}, pagingIDs(partial))
	require.Equal(t, 1, partial.PageInfo.RowCount, "interrupted reply is separate from the cancelled tool row")
	if assert.NotNil(t, partial.Entries[0].Truncated) {
		assert.True(t, *partial.Entries[0].Truncated)
	}
	if assert.NotNil(t, partial.Entries[0].TruncationReason) {
		assert.Equal(t, "cancelled", string(*partial.Entries[0].TruncationReason))
	}
	peer := f.reader.connect()
	historyD16ReplayCalls(t, f.sid, f.reader.attach(peer, f.sid), f.wants, owners)
	historyD16CancellationCarriers(t, f.reader, f.sid, f.wants)
	assert.Equal(t, f.canonical, pagingCanonicalBytes(t, f.reader.store, f.sid), "edge/overlap/retry reads never append new tool execution")
}
