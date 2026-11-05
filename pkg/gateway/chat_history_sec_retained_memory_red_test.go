package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// SEC-1: A2.6 claims page-retained copies are built from shortened entries.
// This is retained HeapAlloc AFTER GC, not peak memory, RSS or B/op. Twenty
// near-admission groups exercise the same B75 class without its 100-group cost.
func TestHistoryPageSecurity_RetainedHeapScalesWithPreviews(t *testing.T) {
	const groups = 20         // The minimum supported page limit; all twenty must survive.
	const previewBytes = 4096 // A2.1, independent of the implementation constant.
	// A2.6 names seven page representations; budget eight including metadata.
	// Allow FOUR times that inventory per preview, plus fixed 16 MiB for schema/
	// runtime/store bookkeeping. This generous fixture bound is NOT a universal
	// product or transient heap bound.
	const fixedOverhead = 16 * 1024 * 1024
	const retainedLimit = fixedOverhead + 4*8*groups*previewBytes

	prepared, baseline := secRetainedPreparedPage(t, groups)
	// The helper's payload, append entries, store and request are no longer
	// inputs on this stack. Keep ONLY its returned prepared page alive past GC.
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(prepared)
	retained := int64(after.HeapAlloc) - int64(baseline)
	t.Logf("history_sec1_retained method=HeapAlloc_after_GC groups=%d preview_bytes=%d baseline_bytes=%d retained_heap_bytes=%d retained_delta_bytes=%d limit_bytes=%d", groups, previewBytes, baseline, after.HeapAlloc, retained, retainedLimit)

	// Prove the reader did not meet the memory bound by dropping the corpus.
	require.Len(t, prepared.Page.Entries, groups)
	require.Len(t, prepared.Response.Entries, groups)
	require.Equal(t, groups, prepared.Response.PageInfo.SourceGroupCount)
	require.Equal(t, groups, prepared.Response.PageInfo.RowCount)
	for _, message := range prepared.Response.Entries {
		require.NotNil(t, message.ToolCalls)
		require.Len(t, *message.ToolCalls, 1)
		require.NotNil(t, (*message.ToolCalls)[0].Result)
		require.Equal(t, map[string]any{
			"_history_preview":    true,
			"original_size_bytes": float64(8*1024*1024 - 4096 + 11), // compact {"text":"..."}
			"preview":             `{"text":"` + strings.Repeat("x", previewBytes-9),
		}, *(*message.ToolCalls)[0].Result, "A2.1 exact independent JSON prefix and true size")
	}
	require.LessOrEqual(t, retained, int64(retainedLimit), "SEC-1/A2.6: a live prepared page retains previews, not twenty complete near-8-MiB backing strings")
}

func secRetainedPreparedPage(t *testing.T, groups int) (preparedHistoryPage, uint64) {
	t.Helper()
	// Warm before the baseline. Take the baseline BEFORE constructing the
	// corpus so collecting source inputs cannot hide retained page allocation.
	secWarmPreparedPage(t)
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	store, sid := secSeedNearAdmission(t, groups)
	request, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: groups, MinRows: groups}, historyPageProjectionDeps{})
	require.NoError(t, err)
	prepared, err := request.Read(context.Background(), store)
	require.NoError(t, err, "actual reader, reduction, preparation, metering and carrier validation complete")
	return prepared, before.HeapAlloc
}

func secWarmPreparedPage(t *testing.T) {
	t.Helper()
	store, sid, _ := pagingProjectionFixture(t)
	require.NoError(t, store.AppendTranscriptStrict(sid, session.TranscriptEntry{ID: "sec1-warm", Role: "user", AgentID: "mia", Content: "warm bounded schemas", ViewMembership: session.ViewMembershipChat}))
	require.NoError(t, store.Close())
	request, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 20}, historyPageProjectionDeps{})
	require.NoError(t, err)
	warm, err := request.Read(context.Background(), store)
	require.NoError(t, err)
	require.Len(t, warm.Response.Entries, 1, "positive control: real prepared read is available")
}

func secSeedNearAdmission(t *testing.T, groups int) (*session.UnifiedStore, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	store, err := session.NewUnifiedStoreWithHome(filepath.Join(home, "sessions"), home)
	require.NoError(t, err)
	// A function-scoped defer joins on every exit without a testing.Cleanup
	// closure retaining the store/input graph during the post-read GC.
	defer func() { require.NoError(t, store.Close()) }()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := meta.ID
	payload := strings.Repeat("x", 8*1024*1024-4096)
	for i := 0; i < groups; i++ {
		entry := session.TranscriptEntry{
			ID: fmt.Sprintf("sec1-owner-%02d", i), Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia",
			Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat,
			ToolCalls: []session.ToolCall{{ID: session.ToolCallID(fmt.Sprintf("sec1-call-%02d", i)), Tool: "read_file", Status: "success", ContentState: "full", Parameters: map[string]any{}, Result: map[string]any{"text": payload}}},
		}
		encoded, err := json.Marshal(session.ArchiveRecord{TranscriptEntry: entry})
		require.NoError(t, err)
		require.Greater(t, len(encoded), 8*1024*1024-4096, "B75 instrument: each encoded canonical group is near admission")
		require.Less(t, len(encoded), 8*1024*1024, "B75 instrument: no over-bound admission fixture")
		require.NoError(t, store.AppendTranscriptStrict(sid, entry), "real canonical append, not an in-memory fabricated reduced page")
	}
	require.NoError(t, store.Close(), "join owned fixture background work before retained-memory measurement")
	return store, sid
}
