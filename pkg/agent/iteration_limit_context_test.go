package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// CR F2: runtime prose is chat-only. Omitting it during provider sanitization
// must not leave a hidden model slot that de-identifies later context or blocks
// whole-turn relief. Expected addresses come from actual store appends, not the
// mapper under test; the model/worker/stores/relief remain real.
func TestIterationLimitNoticeRemainsChatOnly(t *testing.T) {
	ctx := context.Background()
	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{finishReason: "tool_calls", toolCalls: []providers.ToolCall{truncationEchoCall("context-note-1", "first")}},
		{finishReason: "tool_calls", toolCalls: []providers.ToolCall{truncationEchoCall("context-note-2", "second")}},
		{content: "Later answer one.", finishReason: "stop"},
		{content: "Later answer two.", finishReason: "stop"},
	}}
	al, inst, store, sid := newTruncationTestHarness(t, provider, 2, 0)
	registerTruncationEchoTool(al, inst)
	key := "agent:" + inst.ID + ":session:" + sid
	run := func(input string) string {
		answer, err := al.runAgentLoop(ctx, inst, processOptions{
			SessionKey: key, Channel: "web", ChatID: sid, UserMessage: input,
			DefaultResponse: defaultResponse, TranscriptSessionID: sid, TranscriptStore: store,
		})
		require.NoError(t, err)
		return answer
	}
	require.Equal(t, specToolLimitMessage, run("Read the two notes."))
	require.Equal(t, "Later answer one.", run("Later question one."))
	require.Equal(t, "Later answer two.", run("Later question two."))
	require.Equal(t, 4, provider.CallCount(), "two tool rounds and two later text rounds")

	windowStore, ok := inst.Sessions.(session.ContextWindowStore)
	require.True(t, ok)
	before, err := windowStore.WindowView(ctx, key)
	require.NoError(t, err)
	var laterSlots []session.ModelSlot
	for _, slot := range before.Live {
		assert.NotEqual(t, specToolLimitMessage, slot.Message.Content, "system notice cannot occupy any model slot")
		if slot.Message.Content == "Later question one." || slot.Message.Content == "Later answer one." ||
			slot.Message.Content == "Later question two." || slot.Message.Content == "Later answer two." {
			laterSlots = append(laterSlots, slot)
		}
	}
	require.Len(t, laterSlots, 4, "the actual later slots are the mapping oracle")
	candidate := append([]providers.Message{{Role: "system", Content: "Pinned request envelope."}},
		sanitizeHistoryForProvider(inst.Sessions.GetHistory(key))...)
	lines := mapWindowMessages(before, candidate, -1, 0)
	for _, slot := range laterSlots {
		index := indexOfRole(candidate, slot.Message.Role, slot.Message.Content)
		require.NotEqual(t, -1, index)
		assert.Equal(t, slot.Ordinal, lines[index], "later message %q keeps its original archive ordinal", slot.Message.Content)
	}

	result, changed := al.trimWindowChecked(ctx, inst, sid, key, true)
	require.NoError(t, result.Err)
	assert.True(t, changed, "the completed oldest turn remains evictable after a ceiling notice")
	assert.False(t, result.NothingToTrim, "a protected newest turn must not make all older turns unevictable")
	after, err := windowStore.WindowView(ctx, key)
	require.NoError(t, err)
	assert.Greater(t, after.State.Skip, before.State.Skip, "real context relief advances the archive cursor")
	for _, slot := range laterSlots {
		if slot.Ordinal < after.State.Skip {
			continue
		}
		retained, found := after.Slot(slot.Ordinal)
		require.True(t, found, "a surviving later slot is still addressable")
		assert.Equal(t, slot, retained, "relief cannot renumber or replace a retained archive identity")
	}
	for _, slot := range laterSlots[2:] {
		retained, found := after.Slot(slot.Ordinal)
		require.True(t, found, "the newest question/answer pair is the relief floor")
		assert.Equal(t, slot, retained)
	}
	entries, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	var notices []session.TranscriptEntry
	for _, entry := range entries {
		if entry.Content == specToolLimitMessage {
			notices = append(notices, entry)
			assert.Equal(t, session.EntryTypeSystem, entry.Type)
			assert.Equal(t, "system", entry.Role)
			assert.Empty(t, entry.Model)
		}
	}
	assert.Len(t, notices, 1, "context relief keeps the one durable chat-only notice")
}

func assertCeilingCompletedResults(t *testing.T, messages []providers.Message) {
	t.Helper()
	previous := -1
	for _, want := range []struct{ id, content string }{
		{"ceiling-call-1", "echo_tool_result:note-1"},
		{"ceiling-call-2", "echo_tool_result:note-2"},
	} {
		var declarations, results []int
		for i, message := range messages {
			for _, call := range message.ToolCalls {
				if call.ID == want.id {
					declarations = append(declarations, i)
				}
			}
			if message.Role == "tool" && message.ToolCallID == want.id {
				results = append(results, i)
				assert.Equal(t, want.content, message.Content, "completed result bytes are retained for %s", want.id)
			}
		}
		require.Len(t, declarations, 1, "each completed call declaration occurs once: %s", want.id)
		require.Len(t, results, 1, "each completed result occurs once: %s", want.id)
		assert.Greater(t, declarations[0], previous, "completed groups remain in execution order")
		assert.Greater(t, results[0], declarations[0], "result follows its own issuing call")
		previous = results[0]
	}
}
