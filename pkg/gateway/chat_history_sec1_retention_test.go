package gateway

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// #1452 security gate SEC-1, measured on the real near-admission corpus
// (D-B4b: 100 admitted groups, each one result just below 8 MiB): a prepared
// page made of 4 KiB previews must not keep the full source strings reachable.
// Logs peak (sampled, A2.6 method) and retained heap with the prepared page
// still alive; asserts only the retained bound, which scales with previews.
func TestChatHistoryPage_NearAdmissionPreviewsRetainNoFullSources(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	s, home := api.agentLoop.GetSessionStore(), api.homePath
	sid := createTestSession(t, api)
	r, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 100, MinRows: 100}, historyPageProjectionDeps{ToolStore: newToolResultStore(home)})
	require.NoError(t, err)
	payload := strings.Repeat("x", 8*1024*1024-4096)
	for i := 0; i < 100; i++ {
		e := session.TranscriptEntry{ID: fmt.Sprintf("near-%03d", i), Type: session.EntryTypeToolCall, AgentID: "mia", Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat, ToolCalls: []session.ToolCall{{ID: session.ToolCallID(fmt.Sprintf("call-%03d", i)), Tool: "read_file", Status: "success", Parameters: map[string]any{}, Result: map[string]any{"text": payload}}}}
		require.NoError(t, s.AppendTranscriptStrict(sid, e))
	}
	payload = ""
	require.NoError(t, s.Close(), "fixture work finishes before measurement")

	runtime.GC()
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	p := d2MeasurePreparedPeak(t, func() (preparedHistoryPage, error) { return r.Read(context.Background(), s) })
	require.Len(t, p.Response.Entries, 100, "positive control: all 100 groups selected as previews")
	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(p)
	retained := int64(after.HeapAlloc) - int64(base.HeapAlloc)
	full := int64(100 * (8*1024*1024 - 4096))
	t.Logf("history_sec1_retained_heap baseline_bytes=%d retained_bytes=%d full_source_bytes=%d", base.HeapAlloc, retained, full)
	// 100 previews x 4 KiB across four carriers is a few MiB; one retained
	// full encoding per group would be about 800 MiB.
	require.Less(t, retained, int64(64*1024*1024), "SEC-1: the prepared page keeps full source strings reachable")
}
