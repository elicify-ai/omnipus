package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// A2.1/B70/B71: {"text":""} has11 ASCII bytes. These are authored exact
// encoded lengths, not sizes copied from the product's shortening routine.
func d2ResultBytes(t *testing.T, n int) map[string]any {
	t.Helper()
	value := map[string]any{"text": strings.Repeat("x", n-11)}
	body, err := json.Marshal(value)
	require.NoError(t, err)
	require.Len(t, body, n, "independent JSON fixture length control")
	return value
}
func d2AppendResult(t *testing.T, s *session.UnifiedStore, sid string, value map[string]any) {
	t.Helper()
	require.NoError(t, s.AppendTranscriptStrict(sid, session.TranscriptEntry{ID: "d2-result-owner", Type: session.EntryTypeToolCall,
		AgentID: "mia", ViewMembership: session.ViewMembershipChat, Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC),
		ToolCalls: []session.ToolCall{{ID: "d2-call", Tool: "read_file", Status: "success", ContentState: "full", Parameters: map[string]any{}, Result: value}},
	}))
}
func d2PrepareOnlyGroup(t *testing.T, s *session.UnifiedStore, sid string, dependencies ...historyPageProjectionDeps) historyPreparedGroup {
	t.Helper()
	// Explicit small test weigher is outside the reducer/projector under test.
	// Public carrier byte accounting is separately exercised by T63/T64.
	p, err := s.ReadChatHistoryPage(context.Background(), sid, session.ChatHistoryPageOptions{Limit: 20,
		Weigh: func(_ context.Context, p session.ChatHistoryPage) (session.ChatHistoryWeight, error) {
			n := int64(256 + len(p.Entries)*128)
			return session.ChatHistoryWeight{RESTBytes: n, WSBytes: n, ProjectedBytes: n}, nil
		}})
	require.NoError(t, err)
	require.Len(t, p.Entries, 1)
	deps := historyPageProjectionDeps{}
	if len(dependencies) > 0 {
		deps = dependencies[0]
	}
	r, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 20}, deps)
	require.NoError(t, err)
	r.reset(p.Cut.Generation())
	g, err := r.prepareGroup(context.Background(), p, p.Entries[0])
	require.NoError(t, err, "real reducer -> generated Message -> native replay projector")
	return g
}

// d2OriginPosition returns a replay frame's history_origin.position after
// requiring history_origin to be a JSON object.
func d2OriginPosition(t *testing.T, frame map[string]any) any {
	t.Helper()
	origin, ok := frame["history_origin"].(map[string]any)
	require.True(t, ok, "history_origin is a JSON object: %#v", frame["history_origin"])
	return origin["position"]
}
func d2Result(t *testing.T, g historyPreparedGroup) map[string]any {
	t.Helper()
	require.NotNil(t, g.message.ToolCalls)
	require.Len(t, *g.message.ToolCalls, 1)
	require.NotNil(t, (*g.message.ToolCalls)[0].Result)
	return *(*g.message.ToolCalls)[0].Result
}
func TestChatHistoryPage_DisplayShortening(t *testing.T) {
	for _, n := range []int{51199, 51200, 51201} {
		t.Run(fmt.Sprintf("JSON_bytes_%d", n), func(t *testing.T) {
			s, sid, home := pagingProjectionFixture(t)
			value := d2ResultBytes(t, n)
			d2AppendResult(t, s, sid, value)
			require.NoError(t, s.Close(), "stop/join the owned flusher before read-only assertions")
			before := pagingProjectionTree(t, home)
			g := d2PrepareOnlyGroup(t, s, sid)
			want := value
			if n > 51200 {
				want = map[string]any{"_history_preview": true, "original_size_bytes": float64(n), "preview": `{"text":"` + strings.Repeat("x", 4087)}
			}
			require.Equal(t, want, d2Result(t, g), "A2.1 exact inline/preview boundary, no legacy _truncated substitute")
			resultFrames := 0
			for _, raw := range g.frames {
				var frame map[string]any
				require.NoError(t, json.Unmarshal(raw, &frame))
				if frame["type"] == "tool_call_result" {
					resultFrames++
					require.Equal(t, want, frame["result"], "REST Message and native history replay result have identical preview bytes")
					require.Equal(t, *g.message.HistoryPosition, d2OriginPosition(t, frame))
				}
			}
			require.Equal(t, 1, resultFrames)
			require.Equal(t, before, pagingProjectionTree(t, home), "no read-side archive/index/tool_results writes")
		})
	}
	t.Run("UTF8_result_preview", func(t *testing.T) {
		s, sid, _ := pagingProjectionFixture(t)
		// JSON prefix9 +4085 ASCII=4094; euro crosses4096. Remaining
		// suffix makes the result larger than the inline threshold.
		v := map[string]any{"text": strings.Repeat("x", 4085) + "€" + strings.Repeat("z", 51200)}
		raw, err := json.Marshal(v)
		require.NoError(t, err)
		d2AppendResult(t, s, sid, v)
		g := d2PrepareOnlyGroup(t, s, sid)
		want := map[string]any{"_history_preview": true, "original_size_bytes": float64(len(raw)), "preview": `{"text":"` + strings.Repeat("x", 4085)}
		require.Equal(t, want, d2Result(t, g), "first4096 bytes cut back to the previous full character")
		preview, ok := d2Result(t, g)["preview"].(string)
		require.True(t, ok, "preview is a string")
		require.True(t, utf8.ValidString(preview))
	})
}

func d2RequireShortening(t *testing.T) {
	t.Helper()
	s, sid, _ := pagingProjectionFixture(t)
	d2AppendResult(t, s, sid, d2ResultBytes(t, 51201))
	require.NoError(t, s.Close(), "join preflight fixture work before any later memory measurement")
	g := d2PrepareOnlyGroup(t, s, sid)
	if d2Result(t, g)["_history_preview"] != true {
		t.Fatal("BLOCKED: read-time history-result shortening not implemented — required by #1452 D2 A2.1/T62 before heavy T63 corpus/memory probes")
	}
}
