package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

type sec2LogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *sec2LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *sec2LogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// #1452 security gate SEC-2: preparing a history page parses persisted tool
// errors for replay. A malformed structured failure is still reported, with a
// fixed event/reason and safe counts, but no payload-derived text reaches the
// log (no prefix, and no JSON error text that quotes payload bytes).
func TestChatHistoryPage_UnparseableToolFailureLogIsContentFree(t *testing.T) {
	const canary = "PRIVATE_CANARY_SEC2_7Q"
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	s, home := api.agentLoop.GetSessionStore(), api.homePath
	sid := createTestSession(t, api)
	// Malformed on purpose: opens like a structured failure, never closes,
	// and has a stray character after the canary that a SyntaxError quotes.
	payload := `{"error":"` + canary + `"Z`
	e := session.TranscriptEntry{ID: "sec2-src", Type: session.EntryTypeToolCall, AgentID: "mia", Timestamp: time.Date(2026, 10, 30, 1, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat, ToolCalls: []session.ToolCall{{ID: "sec2-call", Tool: "read_file", Status: "error", Parameters: map[string]any{}, Error: payload}}}
	require.NoError(t, s.AppendTranscriptStrict(sid, e))
	r, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{}, historyPageProjectionDeps{ToolStore: newToolResultStore(home)})
	require.NoError(t, err)

	logs := &sec2LogBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	prevCount := unparseableFailureWarns.Load()
	unparseableFailureWarns.Store(0) // the 1st occurrence is the one logged
	t.Cleanup(func() { unparseableFailureWarns.Store(prevCount) })

	p, err := r.Read(context.Background(), s)
	require.NoError(t, err)
	require.Len(t, p.Response.Entries, 1, "positive control: the source group is prepared")

	var warning map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil && rec["msg"] == "gateway: structured tool-failure payload failed to parse; rendering as prose" {
			warning = rec
		}
	}
	require.NotNil(t, warning, "the malformed-failure warning must still fire on the paged preparation path: %s", logs.String())
	require.Equal(t, "structured_tool_failure_parse_failed", warning["event"])
	require.Equal(t, "invalid_json", warning["reason"])
	require.EqualValues(t, len(payload), warning["len"])
	require.EqualValues(t, 1, warning["occurrences"])
	require.NotContains(t, warning, "prefix")
	require.NotContains(t, logs.String(), canary, "SEC-2: saved tool-error text reached the log")
	require.NotContains(t, logs.String(), `'Z'`, "SEC-2: the JSON error text quotes payload bytes")
}
