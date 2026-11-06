package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// A2.6: run each named memory test in isolation, with the fixture's owned
// flusher joined BEFORE measurement. This records the observed peak, baseline,
// delta and sample count; it is not B/op, RSS or a claimed hard heap bound.
// Sampling can under-observe a short transient; the method is explicit in the
// receipt, rather than relabelling a page-size limit as a heap guarantee.
func d2MeasurePreparedPeak(t *testing.T, read func() (preparedHistoryPage, error)) preparedHistoryPage {
	t.Helper()
	runtime.GC()
	var initial runtime.MemStats
	runtime.ReadMemStats(&initial)
	var peak atomic.Uint64
	var samples atomic.Uint64
	peak.Store(initial.HeapAlloc)
	stop, ready, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		var stats runtime.MemStats
		sample := func() {
			runtime.ReadMemStats(&stats)
			samples.Add(1)
			for old := peak.Load(); stats.HeapAlloc > old; old = peak.Load() {
				if peak.CompareAndSwap(old, stats.HeapAlloc) {
					break
				}
			}
		}
		sample()
		close(ready)
		for {
			select {
			case <-stop:
				sample()
				return
			default:
				sample()
				runtime.Gosched()
			}
		}
	}()
	<-ready
	prepared, err := read()
	close(stop)
	<-done
	runtime.KeepAlive(prepared)
	t.Logf("history_d2_peak_heap method=runtime.ReadMemStats_sampling baseline_bytes=%d peak_heap_bytes=%d attributable_observed_delta_bytes=%d sample_count=%d", initial.HeapAlloc, peak.Load(), peak.Load()-initial.HeapAlloc, samples.Load())
	require.NoError(t, err, "complete real read/project/meter/encode operation")
	require.GreaterOrEqual(t, samples.Load(), uint64(2), "missing measurement is not zero/PASS")
	require.Greater(t, peak.Load(), initial.HeapAlloc, "instrument must see attributable page allocation")
	return prepared
}

func TestChatHistoryPage_CorrectionHeavyOwnerMemory(t *testing.T) {
	d2RequireShortening(t) // Fail loudly before constructing a heavy unimplemented corpus.
	for _, outcome := range []string{"settled", "projected", "retracted"} {
		t.Run(outcome, func(t *testing.T) {
			s, sid, home := pagingProjectionFixture(t)
			call := session.ToolCall{ID: "d2-corrected", Tool: "bash", Status: "pending", ContentState: "full", Parameters: map[string]any{}}
			owner, err := s.AppendTranscriptAddressed(sid, session.TranscriptEntry{ID: "correction-owner", Type: session.EntryTypeToolCall, AgentID: "mia", ViewMembership: session.ViewMembershipChat, ToolCalls: []session.ToolCall{call}})
			require.NoError(t, err)
			var want map[string]any
			// Twelve iterations =12 separately saved2MB settles +12 projections
			// +12 retractions, not one fabricated final post-image.
			for i := 0; i < 12; i++ {
				want = map[string]any{"kind": "retained-structured-key", "text": fmt.Sprintf("settle-%02d-", i) + strings.Repeat("x", 2000000)}
				call.Status = "success"
				call.Result = want
				_, err = s.SettleToolCall(sid, owner, "", call)
				require.NoError(t, err)
				projection, projectErr := s.ProjectToolCalls(sid, []session.ToolCallProjectionEdit{{Target: owner, ToolCallID: call.ID, ContentState: "capped", Text: "discarded-projection"}})
				require.NoError(t, projectErr)
				_, err = s.RetractToolCallEffects(sid, []session.ArchiveAddress{projection})
				require.NoError(t, err)
			}
			if outcome != "settled" {
				text := "final-projection-" + strings.Repeat("y", 60000)
				effect, projectErr := s.ProjectToolCalls(sid, []session.ToolCallProjectionEdit{{Target: owner, ToolCallID: call.ID, ContentState: "capped", Text: text}})
				require.NoError(t, projectErr)
				if outcome == "retracted" {
					_, err = s.RetractToolCallEffects(sid, []session.ArchiveAddress{effect})
					require.NoError(t, err)
				} else {
					want = map[string]any{"kind": "retained-structured-key", "text": text}
				}
			}
			expectedJSON, err := json.Marshal(want)
			require.NoError(t, err)
			expected := map[string]any{"_history_preview": true, "original_size_bytes": float64(len(expectedJSON)), "preview": string(expectedJSON[:4096])}
			require.NoError(t, s.Close(), "all owned background work joined before attribution")
			before := pagingProjectionTree(t, home)
			p := d2MeasurePreparedPeak(t, func() (preparedHistoryPage, error) {
				r, e := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 20}, historyPageProjectionDeps{})
				if e != nil {
					return preparedHistoryPage{}, e
				}
				return r.Read(context.Background(), s)
			})
			require.Len(t, p.Response.Entries, 1)
			require.NotNil(t, p.Response.Entries[0].ToolCalls)
			require.Equal(t, expected, *(*p.Response.Entries[0].ToolCalls)[0].Result, "reduce fully through settle/project/retract, THEN shorten; retain unrelated structured keys")
			require.Equal(t, before, pagingProjectionTree(t, home))
		})
	}
}

func TestChatHistoryPage_MultiCallFirstGroupWhole(t *testing.T) {
	d2RequireShortening(t)
	s, sid, _ := pagingProjectionFixture(t)
	calls := make([]session.ToolCall, 200)
	for i := range calls {
		calls[i] = session.ToolCall{ID: session.ToolCallID(fmt.Sprintf("d2-call-%03d", i)), Tool: "bash", Status: "pending", ContentState: "full", Parameters: map[string]any{}}
	}
	owner, err := s.AppendTranscriptAddressed(sid, session.TranscriptEntry{ID: "multi-call-owner", Type: session.EntryTypeToolCall, AgentID: "mia", ViewMembership: session.ViewMembershipChat, ToolCalls: calls})
	require.NoError(t, err)
	value := d2ResultBytes(t, 51200)
	for _, call := range calls {
		call.Status = "success"
		call.Result = value
		_, err = s.SettleToolCall(sid, owner, "pending", call)
		require.NoError(t, err, "200 actual separately admitted settlements")
	}
	require.NoError(t, s.Close())
	p := d2MeasurePreparedPeak(t, func() (preparedHistoryPage, error) {
		r, e := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 20, MinRows: 20}, historyPageProjectionDeps{})
		if e != nil {
			return preparedHistoryPage{}, e
		}
		return r.Read(context.Background(), s)
	})
	require.Len(t, p.Response.Entries, 1)
	require.Equal(t, 200, p.Response.PageInfo.RowCount)
	require.NotNil(t, p.Response.Entries[0].ToolCalls)
	require.Len(t, *p.Response.Entries[0].ToolCalls, 200)
	for _, call := range *p.Response.Entries[0].ToolCalls {
		require.NotNil(t, call.Result)
		require.Equal(t, value, *call.Result, "exactly51200 stays inline")
	}
	require.Greater(t, p.Response.PageInfo.ProjectedBytes, int64(200*51200), "sum of inline results alone exceeds8MiB")
	require.EqualValues(t, 8388608, p.Response.PageInfo.ByteCeiling)
	require.NotNil(t, p.Response.PageInfo.BudgetOverride)
	require.Equal(t, "atomic_group", *p.Response.PageInfo.BudgetOverride)
}
