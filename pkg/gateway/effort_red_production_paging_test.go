package gateway

// CHECK-01/08 / S-95: every source entry is written by an actual turn. The
// separate canonical-byte projection fixture is NOT substituted for this test.
// Provider JSON -> accounting -> saved snapshots -> attach/older/refresh are
// the intended real boundaries, not hand-constructed accounting fixtures.
// INFO-2: execution currently stops at firstStream: the multi-candidate
// callProviderOnce callback invokes Chat, bypassing runProviderAttempt and its
// ChatStream path. Turns 2/3, real tool execution, stored snapshots, older-page
// contributor cuts, refresh and incremental replay below are still UNREACHED.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func effortPagingReply(t *testing.T, w http.ResponseWriter, stream bool, message, usage map[string]any, finish string) {
	t.Helper()
	choice := map[string]any{"index": 0, "finish_reason": finish}
	if stream {
		delta := map[string]any{}
		if text, ok := message["content"]; ok {
			delta["content"] = text
		}
		if calls, ok := message["tool_calls"].([]any); ok {
			for i, raw := range calls {
				effortRequireType[map[string]any](t, raw)["index"] = i
			}
			delta["tool_calls"] = calls
		}
		choice["delta"] = delta
	} else {
		choice["message"] = message
	}
	raw, err := json.Marshal(map[string]any{"choices": []any{choice}, "usage": usage})
	require.NoError(t, err)
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
	} else {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}
}
func effortPagingAttach(t *testing.T, f *effortProductionTurn) map[string]map[string]any {
	t.Helper()
	wc := makeTestConn()
	wc.sendCh = make(chan []byte, 512)
	t.Cleanup(wc.close)
	raw, err := json.Marshal(map[string]any{"type": "attach_session", "session_id": f.sid, "history_limit": 20, "history_min_rows": 1})
	require.NoError(t, err)
	f.handler.handleAttachSessionWithAck(context.Background(), "effort-paging-reload", f.sid, nil, nil, wc, raw)
	rows := map[string]map[string]any{}
	complete := false
	for _, raw := range drainAllFrames(wc.sendCh) {
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame["type"] == "replay_message" {
			id, ok := frame["id"].(string)
			require.True(t, ok)
			rows[id] = frame
		}
		if frame["type"] == "catch_up_complete" {
			complete = true
		}
	}
	require.True(t, complete, "actual snapshot attach completed, not a manufactured frame list")
	return rows
}
func effortPagingRead(t *testing.T, f *effortProductionTurn, query string) map[string]any {
	t.Helper()
	res := pagingRequest(f.api, f.sid, query)
	require.Equal(t, 200, res.Code, res.Body.String())
	var page map[string]any
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &page))
	return page
}
func effortPagingRows(t *testing.T, page map[string]any) map[string]map[string]any {
	t.Helper()
	entries, ok := page["entries"].([]any)
	require.True(t, ok)
	rows := map[string]map[string]any{}
	for _, raw := range entries {
		row, ok := raw.(map[string]any)
		require.True(t, ok)
		id, ok := row["id"].(string)
		require.True(t, ok)
		rows[id] = row
	}
	return rows
}
func effortPagingAssertAnswer(t *testing.T, got, want map[string]any) {
	t.Helper()
	require.NotNil(t, got, "production answer present")
	assert.Equal(t, want["effort"], got["effort"])
	if value, present := want["turn_reasoning_tokens"]; present {
		assert.Equal(t, value, got["turn_reasoning_tokens"])
	} else {
		assert.NotContains(t, got, "turn_reasoning_tokens", "absence is not zero or null")
	}
	assert.NotContains(t, got, "reasoning_tokens", "per-call accounting is not a public message field")
	assert.NotEqual(t, "This saved message could not be displayed.", got["content"])
}

func effortPagingAssertIncremental(t *testing.T, f *effortProductionTurn, live ...map[string]any) {
	t.Helper()
	hub := f.handler.hubs.getOrCreate(f.sid)
	hub.mu.Lock()
	journal := append([]journalEntry(nil), hub.journal...)
	head := hub.head
	hub.mu.Unlock()
	want := map[string][]byte{}
	for _, entry := range journal {
		var frame map[string]any
		require.NoError(t, json.Unmarshal(entry.bytes, &frame))
		if frame["type"] == "done" && frame["message_id"] != nil {
			want[effortRequireType[string](t, frame["message_id"])] = entry.bytes
		}
	}
	wc := makeTestConn()
	wc.sendCh = make(chan []byte, 1024)
	t.Cleanup(wc.close)
	// Journal seqs are issued from the process-wide registry counter
	// (ws_session_hub.go::getOrCreate), so this chat's retained numbering does
	// not start at 1 and a cursor below the retained start is correctly
	// answered "retention_exceeded" with a full snapshot (ws_session_hub.go::
	// bind). Take the reconnect position from the chat's real starting number:
	// journal[0].seq-1, the largest cursor whose incremental tail is still the
	// whole retained journal ("seq > since_seq").
	var seq int64
	if len(journal) > 0 {
		seq = int64(journal[0].seq) - 1
	} else {
		seq = int64(head) // empty journal: only the head itself is servable
	}
	boot := f.handler.hubs.bootID
	f.handler.handleAttachSessionWithAck(context.Background(), "effort-paging-incremental", f.sid, &attachCursor{SinceSeq: &seq, BootID: &boot}, nil, wc)
	got := map[string]map[string]any{}
	incremental := false
	for _, raw := range drainAllFrames(wc.sendCh) {
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame["type"] == "catch_up_complete" {
			incremental = frame["mode"] == "incremental"
		}
		if frame["type"] == "done" && frame["message_id"] != nil {
			id := effortRequireType[string](t, frame["message_id"])
			require.Contains(t, want, id)
			assert.Equal(t, want[id], raw, "incremental replay is byte-identical to the actual published journal")
			got[id] = frame
		}
	}
	require.True(t, incremental)
	for _, frame := range live {
		assert.Equal(t, frame, got[effortRequireType[string](t, frame["message_id"])], "effort and reasoning metadata match the observed live done")
	}
}

func TestT_GW_S95_ProductionThreeTurnsOlderRefreshAndContributorCut(t *testing.T) {
	var mu sync.Mutex
	streams := []any{}
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("primary JSON: %v", err)
			return
		}
		mu.Lock()
		streams = append(streams, body["stream"])
		mu.Unlock()
		if body["stream"] != true {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(402)
			fmt.Fprint(w, `{"error":{"code":"402","message":"billed attempt"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"error\":{\"code\":\"402\",\"message\":\"billed partial attempt\"},\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"error\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":300,\"total_tokens\":310,\"completion_tokens_details\":{\"reasoning_tokens\":120}}}\n\ndata: [DONE]\n\n")
	}))
	defer a.Close()
	probe := filepath.Join(t.TempDir(), "paging-user-file.txt")
	require.NoError(t, os.WriteFile(probe, []byte("REAL-PAGING-TOOL-7731"), 0600))
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("fallback JSON: %v", err)
			return
		}
		prompt := ""
		afterTool := false
		for _, raw := range effortRequireType[[]any](t, body["messages"]) {
			m := effortRequireType[map[string]any](t, raw)
			text, err := json.Marshal(m["content"])
			if err != nil {
				t.Errorf("content: %v", err)
				return
			}
			if m["role"] == "user" && strings.Contains(string(text), "PAGE-") {
				prompt = string(text)
				afterTool = false
			}
			if m["role"] == "tool" {
				afterTool = true
			}
		}
		usage := map[string]any{"prompt_tokens": 5, "completion_tokens": 80, "total_tokens": 85}
		message := map[string]any{"role": "assistant", "content": "ANSWER-UNREPORTED"}
		finish := "stop"
		switch {
		case strings.Contains(prompt, "PAGE-T1"):
			message["content"] = "ANSWER-T1"
		case strings.Contains(prompt, "PAGE-T2"):
			if !afterTool {
				args, err := json.Marshal(map[string]any{"path": probe})
				if err != nil {
					t.Errorf("args: %v", err)
					return
				}
				message["content"] = ""
				message["tool_calls"] = []any{map[string]any{"id": "paging-read-7731", "type": "function", "function": map[string]any{"name": "read_file", "arguments": string(args)}}}
				finish = "tool_calls"
				usage["completion_tokens_details"] = map[string]any{"reasoning_tokens": 30}
			} else {
				message["content"] = "ANSWER-T2"
				usage["completion_tokens_details"] = map[string]any{"reasoning_tokens": 0}
			}
		case strings.Contains(prompt, "PAGE-T3"):
			message["content"] = "ANSWER-T3"
		default:
			message["content"] = "ANSWER-FILL"
		}
		effortPagingReply(t, w, body["stream"] == true, message, usage, finish)
	}))
	defer b.Close()
	cfg := effortProductionConfig(t, a.URL, b.URL)
	f := newEffortProductionTurn(t, cfg)
	ag, ok := f.al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	ag.Tools.RegisterReplacing(tools.NewReadFileTool(filepath.Dir(probe), false, 0))
	done1 := f.turn(t, "PAGE-T1", "")
	mu.Lock()
	firstStream := streams[0]
	mu.Unlock()
	require.Equal(t, true, firstStream, "BLOCKED: streaming multi-candidate fallback not implemented — required by S-95/S-88; actual fallback completed but primary used plain Chat")
	assert.Equal(t, float64(120), effortRequireType[map[string]any](t, done1["stats"])["reasoning_tokens"])
	done2 := f.turn(t, "PAGE-T2", "B")
	assert.Equal(t, float64(30), effortRequireType[map[string]any](t, done2["stats"])["reasoning_tokens"])
	done3 := f.turn(t, "PAGE-T3", "B")
	assert.NotContains(t, effortRequireType[map[string]any](t, done3["stats"]), "reasoning_tokens")
	effortPagingAssertIncremental(t, f, done1, done2, done3)
	entries, err := f.al.GetSessionStore().ReadTranscript(f.sid)
	require.NoError(t, err)
	answers := map[string]map[string]any{}
	realTool := false
	for _, entry := range entries {
		// The admitted tool result is persisted on the tool_call entry itself:
		// ToolCalls[].Result["text"] carries the exact admitted text (prompt
		// guard fence included). A transcript row never carries Role "tool" —
		// that role exists only as a provider message (tool_result_admit.go's
		// choke point), so the released scan shape could never match.
		for _, tc := range entry.ToolCalls {
			if tc.Tool == "read_file" && tc.Status == "success" {
				if text, ok := tc.Result["text"].(string); ok && strings.Contains(text, "REAL-PAGING-TOOL-7731") {
					realTool = true
				}
			}
		}
		if entry.Content == "ANSWER-T1" || entry.Content == "ANSWER-T2" || entry.Content == "ANSWER-T3" {
			answers[entry.ID] = effortPublicJSON(t, entry)
		}
	}
	require.True(t, realTool, "actual tool executed; a tool-call-shaped response alone is not the workflow")
	require.Len(t, answers, 3)
	for _, answer := range answers {
		switch answer["content"] {
		case "ANSWER-T1":
			assertProductionAnswer(t, answer, 120, map[string]any{"chosen": "medium", "sent": "low", "reason": "mapped", "provider": "openai", "model": "B"})
		case "ANSWER-T2":
			assertProductionAnswer(t, answer, 30, map[string]any{"chosen": "high", "sent": "high", "provider": "openai", "model": "B"})
		case "ANSWER-T3":
			assert.NotContains(t, answer, "turn_reasoning_tokens")
		}
	}
	// Before later turns, the actual snapshot carries all three answers.
	snapshot := effortPagingAttach(t, f)
	for id, answer := range answers {
		effortPagingAssertAnswer(t, snapshot[id], answer)
	}
	for i := 0; i < 12; i++ {
		f.turn(t, fmt.Sprintf("PAGE-FILL-%d", i), "B")
	}
	latestSnapshot := effortPagingAttach(t, f)
	for id := range answers {
		assert.NotContains(t, latestSnapshot, id, "all three early answers are outside the initial page")
	}
	page := effortPagingRead(t, f, "limit=20&min_rows=1")
	found := map[string]bool{}
	for pages := 0; pages < 10; pages++ {
		rows := effortPagingRows(t, page)
		for id, answer := range answers {
			if got, ok := rows[id]; ok {
				effortPagingAssertAnswer(t, got, answer)
				found[id] = true
			}
		}
		info := effortRequireType[map[string]any](t, page["page_info"])
		if start, ok := info["first_position"].(string); ok {
			end, ok := info["last_position"].(string)
			require.True(t, ok)
			refresh := effortPagingRows(t, effortPagingRead(t, f, "limit=100&min_rows=1&window_start="+url.QueryEscape(start)+"&window_end="+url.QueryEscape(end)))
			for id, answer := range answers {
				if _, onPage := rows[id]; onPage {
					effortPagingAssertAnswer(t, refresh[id], answer)
				}
			}
		}
		before, ok := page["next_before"].(string)
		if !ok {
			break
		}
		page = effortPagingRead(t, f, "limit=20&min_rows=1&before="+url.QueryEscape(before))
	}
	require.Len(t, found, 3, "scrolling older pages must really encounter each answer")
	// Second real session: 18 later user/answer rows leave B's answer on the
	// far side of a twenty-row boundary from the earlier contributing call.
	meta, err := f.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, f.al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning, Origin: &session.Origin{Kind: session.OriginKindChat}, AgentID: "mia", WorkspaceID: testHarnessWorkspaceMembershipID, OwnerScopeKind: session.OwnerScopeHuman}))
	f.sid = meta.ID
	// The live done for this session reaches a conn bound to ITS hub. A
	// sending conn already bound elsewhere keeps that binding by contract
	// (websocket_chat.go's intake bind), so the harness conn — bound to the
	// first session at setup — is rebound here, exactly as the setup bound it.
	bindTestConnToSession(f.handler, "effort-production", meta.ID, f.wc)
	f.turn(t, "PAGE-T1", "")
	second, err := f.al.GetSessionStore().ReadTranscript(f.sid)
	require.NoError(t, err)
	var answer map[string]any
	contributors := []string{}
	for _, entry := range second {
		if entry.Content == "ANSWER-T1" {
			answer = effortPublicJSON(t, entry)
		}
		if entry.Model == "A" && entry.CompletionTokens > 0 {
			contributors = append(contributors, entry.ID)
		}
	}
	require.NotNil(t, answer)
	require.Len(t, contributors, 1, "the billed primary call is written by production accounting")
	for i := 0; i < 9; i++ {
		f.turn(t, fmt.Sprintf("PAGE-FILL-CUT-%d", i), "B")
	}
	cut := effortPagingAttach(t, f)
	id := effortRequireType[string](t, answer["id"])
	effortPagingAssertAnswer(t, cut[id], answer)
	assert.Equal(t, float64(120), cut[id]["turn_reasoning_tokens"])
	for _, contributor := range contributors {
		assert.NotContains(t, cut, contributor, "snapshot page does not contain A's contributing call")
	}
	restCut := effortPagingRows(t, effortPagingRead(t, f, "limit=20&min_rows=1"))
	effortPagingAssertAnswer(t, restCut[id], answer)
	for _, contributor := range contributors {
		assert.NotContains(t, restCut, contributor)
	}
}
