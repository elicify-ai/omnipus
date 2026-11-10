package gateway

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	historyD16CompletedID = "d16-call-completed"
	historyD16CancelledID = "d16-call-actually-started"
	historyD16QueuedID    = "d16-call-never-executed"
	historyD16OtherID     = "d16-other-session-call"
)

// Internal expected facts, not a wire type. IDs/parameters come from the frozen
// provider request; the actual producing turn comes from the native start event.
type historyD16CallWant struct{ id, step, status, turn string }

func historyD16CanonicalCalls(t *testing.T, store *session.UnifiedStore, sid string, wants []historyD16CallWant) {
	t.Helper()
	entries, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	for _, want := range wants {
		count := 0
		for _, entry := range entries {
			for _, call := range entry.ToolCalls {
				if string(call.ID) != want.id {
					continue
				}
				count++
				assert.Equal(t, "mia", entry.AgentID, "original canonical owner of %s", want.id)
				assert.Equal(t, want.turn, entry.TurnID, "original canonical producing turn of %s", want.id)
				assert.Equal(t, historyD16ToolName, call.Tool, "original canonical tool of %s", want.id)
				assert.Equal(t, map[string]any{"step": want.step}, call.Parameters, "original canonical arguments of %s", want.id)
				assert.Equal(t, want.status, call.Status, "FR-026: confirmed cancellation must remain terminal, never pending/error/late success: %s", want.id)
				if want.status == "success" {
					assert.Equal(t, "D16 completed result", call.Result["text"], "known completed result survives Stop on %s", want.id)
				}
				text, _ := call.Result["text"].(string)
				assert.NotContains(t, text, "D16 late success", "discarded late success cannot replace a cancelled display fact")
			}
		}
		assert.Equal(t, 1, count, "FR-026: exactly one durable original display call %s must survive real Stop and reopen", want.id)
	}
	queued := 0
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			if string(call.ID) == historyD16QueuedID {
				queued++
				assert.Equal(t, "pending", call.Status, "unexecuted queued fact is never fabricated as executed/cancelled")
				assert.Empty(t, call.Result, "no invented queued execution output")
			}
		}
	}
	assert.LessOrEqual(t, queued, 1, "no duplicate pending display fact")
}

func historyD16PublicCalls(t *testing.T, entries []generated.Message, wants []historyD16CallWant) map[string]generated.Message {
	t.Helper()
	owners := make(map[string]generated.Message)
	for _, want := range wants {
		count := 0
		for _, entry := range entries {
			if entry.ToolCalls == nil {
				continue
			}
			for _, call := range *entry.ToolCalls {
				if call.Id != want.id {
					continue
				}
				count++
				owners[want.id] = entry
				assert.Equal(t, "mia", entry.AgentId)
				assert.Equal(t, historyD16ToolName, call.Tool)
				assert.Equal(t, want.status, string(call.Status), "FR-026: generated REST tool outcome for %s", want.id)
				if assert.NotNil(t, call.Parameters, "original arguments retained for %s", want.id) {
					assert.Equal(t, map[string]any{"step": want.step}, *call.Parameters)
				}
				assert.NotNil(t, entry.HistoryPosition, "whole original source has a stable occurrence position")
				if assert.NotNil(t, entry.TurnId, "original producing turn retained for %s", want.id) {
					assert.Equal(t, want.turn, *entry.TurnId)
				}
			}
		}
		assert.Equal(t, 1, count, "FR-026: REST/detail contains exactly one original call %s", want.id)
	}
	return owners
}

func historyD16Origin(t *testing.T, origin *generated.HistoryEntryOrigin, owner generated.Message, callID string) {
	t.Helper()
	if !assert.NotNil(t, origin, "local source origin for %s", callID) {
		return
	}
	if assert.NotNil(t, owner.HistoryPosition) {
		assert.Equal(t, *owner.HistoryPosition, origin.Position, "same occurrence across REST and snapshot")
	}
	if assert.NotNil(t, origin.EntryId) {
		assert.Equal(t, owner.Id, *origin.EntryId, "original source owner, not a synthetic orphan")
	}
}

func historyD16ReplayCalls(t *testing.T, sid string, frames [][]byte, wants []historyD16CallWant, owners map[string]generated.Message) {
	t.Helper()
	for _, want := range wants {
		starts, results := 0, 0
		for _, raw := range frames {
			switch historyD16Type(t, raw) {
			case "tool_call_start":
				var start generated.ToolCallStartFrame
				require.NoError(t, json.Unmarshal(raw, &start))
				if start.CallId != want.id || start.HistoryOrigin == nil {
					continue // held live copies are checked separately, not historical duplicates
				}
				starts++
				assert.Equal(t, sid, start.SessionId)
				assert.Equal(t, historyD16ToolName, start.Tool)
				assert.Equal(t, map[string]any{"step": want.step}, start.Params)
				if assert.NotNil(t, start.AgentId) {
					assert.Equal(t, "mia", *start.AgentId)
				}
				historyD16Origin(t, start.HistoryOrigin, owners[want.id], want.id)
			case "tool_call_result":
				var result generated.ToolCallResultFrame
				require.NoError(t, json.Unmarshal(raw, &result))
				if result.CallId != want.id || result.HistoryOrigin == nil {
					continue
				}
				results++
				assert.Equal(t, sid, result.SessionId)
				assert.Equal(t, historyD16ToolName, result.Tool)
				assert.Equal(t, want.status, result.Status, "FR-026: generated ToolCallResultFrame.status must preserve cancelled, not clamp it to error")
				if assert.NotNil(t, result.AgentId) {
					assert.Equal(t, "mia", *result.AgentId)
				}
				historyD16Origin(t, result.HistoryOrigin, owners[want.id], want.id)
				if want.status == "cancelled" {
					encoded, err := json.Marshal(result.Result)
					require.NoError(t, err)
					assert.NotContains(t, string(encoded), "D16 late success")
				}
			}
		}
		assert.Equal(t, 1, starts, "whole saved call start exactly once: %s", want.id)
		assert.Equal(t, 1, results, "whole saved terminal result exactly once: %s", want.id)
	}
	for _, raw := range frames {
		if historyD16Type(t, raw) == "tool_call_result" {
			var result generated.ToolCallResultFrame
			require.NoError(t, json.Unmarshal(raw, &result))
			assert.NotEqual(t, historyD16QueuedID, result.CallId, "unexecuted queued tool cannot gain a fabricated terminal result")
		}
	}
}

func historyD16CancellationCarriers(t *testing.T, reader *historyD16Harness, sid string, wants []historyD16CallWant) {
	t.Helper()
	page := reader.page(sid, "")
	owners := historyD16PublicCalls(t, page.Entries, wants)
	detail := reader.detail(sid)
	require.NotNil(t, detail.Messages)
	assert.Equal(t, page.Entries, *detail.Messages, "same saved range and logical calls on REST page/detail")
	historyD16PublicCalls(t, *detail.Messages, wants)
	peer := reader.connect()
	for i := 0; i < 2; i++ {
		frames := reader.attach(peer, sid)
		historyD16ReplayCalls(t, sid, frames, wants, owners)
		snapshots := 0
		for _, raw := range frames {
			if historyD16Type(t, raw) == "session_snapshot" {
				snapshots++
				var snapshot generated.SessionSnapshotFrame
				require.NoError(t, json.Unmarshal(raw, &snapshot))
				assert.Equal(t, page.PageInfo, snapshot.PageInfo, "same selected whole groups across retry/carriers")
			}
		}
		require.Equal(t, 1, snapshots)
	}
}
