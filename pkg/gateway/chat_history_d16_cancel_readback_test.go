package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func historyD16EngineCancellationReadback(t *testing.T, h *historyD16Harness, tool *historyD16Tool, provider *historyD16Provider,
	sid, other, turn, modelKey string, otherWant []historyD16CallWant, otherBefore map[string]string, overlap [][]byte,
) {
	t.Helper()
	wants := []historyD16CallWant{
		{historyD16CompletedID, "completed", "success", turn},
		{historyD16CancelledID, "blocked", "cancelled", turn},
	}
	historyD16CanonicalCalls(t, h.store, sid, wants)
	historyD16CanonicalCalls(t, h.store, other, otherWant)
	assert.Equal(t, otherBefore, pagingCanonicalBytes(t, h.store, other), "Stop/Stop all does not touch an unrelated session")
	entries, err := h.store.ReadTranscript(sid)
	require.NoError(t, err)
	markers := 0
	for _, entry := range entries {
		if entry.Type == session.EntryTypeTurnCancelled && entry.TurnID == turn {
			markers++
			assert.Equal(t, "hard", entry.CancelMethod, "real forced Stop, not fabricated cancellation metadata")
		}
		for _, call := range entry.ToolCalls {
			assert.NotEqual(t, historyD16OtherID, string(call.ID), "no cross-session borrowing of completed facts")
		}
	}
	assert.Equal(t, 1, markers, "the separate interrupted-turn fact survives exactly once")
	for _, message := range h.store.GetHistory(modelKey) {
		if message.Role == "tool" {
			assert.NotEqual(t, historyD16CancelledID, message.ToolCallID, "cancelled result is not admitted into model context")
			assert.NotEqual(t, historyD16QueuedID, message.ToolCallID, "unexecuted queued result is not invented in model context")
		}
		assert.NotContains(t, message.Content, "D16 late success", "late external return cannot leak into model context")
	}
	require.Equal(t, []string{"completed", "completed", "blocked"}, tool.invocations(), "actual execution count/order, including unrelated positive control; queued never executes")
	require.Equal(t, 3, provider.count(), "two unrelated-session model calls and one cancelled-session request, no post-Stop model-result admission")
	before := pagingCanonicalBytes(t, h.store, sid)
	h.shutdown()
	readerProvider := &historyD16Provider{}
	reader := newHistoryD16Harness(t, h.home, h.cfg, readerProvider, false)
	historyD16CanonicalCalls(t, reader.store, sid, wants)
	historyD16CancellationCarriers(t, reader, sid, wants)
	historyD16CancellationCarriers(t, reader, other, otherWant)
	if overlap != nil {
		owners := historyD16PublicCalls(t, reader.page(sid, "").Entries, wants)
		historyD16ReplayCalls(t, sid, overlap, wants, owners)
	}
	assert.Equal(t, before, pagingCanonicalBytes(t, reader.store, sid), "reopen/page/detail/snapshot/Retry never rewrite or add canonical tool facts")
	assert.Equal(t, otherBefore, pagingCanonicalBytes(t, reader.store, other))
	require.Equal(t, 0, readerProvider.count(), "fresh reader executes no provider or resent input")
	require.Equal(t, 3, provider.count())
	require.Equal(t, []string{"completed", "completed", "blocked"}, tool.invocations(), "all reload carriers execute no tool")
}
