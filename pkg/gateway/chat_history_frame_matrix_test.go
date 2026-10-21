package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/mediatest"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func pagingAssertFrameOrigins(t *testing.T, prepared preparedHistoryPage, wantTypes []string) {
	t.Helper()
	positions := map[string]string{}
	for _, e := range prepared.Page.Entries {
		positions[string(e.Position)] = e.Entry.ID
	}
	allowed := map[string]bool{}
	for _, typ := range []string{"replay_message", "tool_call_start", "tool_call_result", "replay_error", "replay_provider_fallback", "context_window_notice", "judge_verdict", "browser_handover_notice", "helper_event", "goal_outcome", "subagent_start", "subagent_state", "subagent_message", "subagent_end", "media", "ask_user_question"} {
		allowed[typ] = true
	}
	seen := map[string]int{}
	for _, raw := range prepared.ReplayFrames {
		var f map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &f))
		var typ string
		require.NoError(t, json.Unmarshal(f["type"], &typ))
		seen[typ]++
		if typ == "done" || typ == "replay_warning" {
			_, owned := f["history_origin"]
			require.False(t, owned, "completion/warning is not a source row")
			continue
		}
		require.True(t, allowed[typ], "new persisted source-frame class requires an ownership/schema oracle: %s", typ)
		var origin generated.HistoryEntryOrigin
		require.NotEmpty(t, f["history_origin"], "typed local origin on %s", typ)
		require.NoError(t, json.Unmarshal(f["history_origin"], &origin))
		id, ok := positions[origin.Position]
		require.True(t, ok, "no omitted/foreign source owner")
		require.NotNil(t, origin.EntryId)
		require.Equal(t, id, *origin.EntryId)
	}
	for _, typ := range wantTypes {
		require.Positive(t, seen[typ], "positive control: actual persisted class emitted %s", typ)
	}
}

func pagingPersistedOriginMatrix(t *testing.T) {
	t.Helper()
	cases := []struct{ name, typ, payload string }{
		{"user", "replay_message", `{"role":"user","content":"user source"}`},
		{"assistant", "replay_message", `{"role":"assistant","content":"answer","terminal_outcome":true,"model":"recorded-model"}`},
		{"system", "replay_message", `{"type":"system","role":"system","content":"notice"}`},
		{"finished_tool_only", "tool_call_result", `{"type":"tool_call","tool_calls":[{"id":"call","tool":"read_file","status":"success","parameters":{},"result":{"text":"real"}}]}`},
		{"error", "replay_error", `{"role":"assistant","status":"error","content":"saved provider error","error_code":"provider_error","provider_message":true,"model":"recorded-model"}`},
		{"fallback", "replay_provider_fallback", `{"type":"provider_fallback","role":"system","content":"Answered by the Fallback model (used) because unavailable was unavailable.","unavailable_model":"unavailable","model":"used","unavailable_code":"rate_limited"}`},
		{"diagnostic", "context_window_notice", `{"type":"context_window_notice","context_window_notice":{"kind":"mid_turn","message":"bounded context notice"}}`},
		{"verdict", "judge_verdict", `{"type":"judge_verdict","role":"system","content":"{\"id\":\"00000000-0000-4000-8000-000000000001\",\"scope\":\"goal\",\"goal_session_id\":\"goal-session\",\"round\":1,\"met\":true,\"per_criterion\":[],\"model\":\"test-model\",\"judged_at\":\"2026-10-21T12:00:00Z\"}"}`},
		{"handover", "browser_handover_notice", `{"type":"system","role":"system","system_subtype":"browser_handover_notice","content":"Operator took control"}`},
		{"helper", "helper_event", `{"type":"system","role":"system","system_subtype":"helper_event","helper_event":{"helper_session_id":"helper","kind":"finished","text":"Helper finished","created_at":"2026-10-21T12:00:00Z"}}`},
		{"goal", "goal_outcome", `{"type":"system","role":"system","system_subtype":"goal_outcome","goal_outcome":{"goal_id":"goal","goal_text":"Ship","ending":"met","ended_at":"2026-10-21T12:00:00Z","max_rounds":2,"rounds_used":1}}`},
		{"span_start", "subagent_start", `{"type":"system","system_subtype":"subagent_start","subagent_start":{"type":"subagent_start","session_id":"restamped","span_id":"span_call","parent_call_id":"call","task_label":"Task"}}`},
		{"span_state", "subagent_state", `{"type":"system","system_subtype":"subagent_state","subagent_state":{"type":"subagent_state","session_id":"restamped","span_id":"span_call","state":"running","created_at":"2026-10-21T12:00:00Z"}}`},
		{"span_message", "subagent_message", `{"type":"system","system_subtype":"subagent_message","subagent_message":{"type":"subagent_message","session_id":"restamped","span_id":"span_call","kind":"progress","message_id":"progress","sender_identity":"worker","untrusted_origin":false,"text":"Progress","created_at":"2026-10-21T12:00:00Z"}}`},
		{"span_end", "subagent_end", `{"type":"system","system_subtype":"subagent_end","subagent_end":{"type":"subagent_end","session_id":"restamped","span_id":"span_call","status":"success","parent_call_id":"call"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, sid, home := pagingProjectionFixture(t)
			deps := historyPageProjectionDeps{ToolStore: newToolResultStore(home)}
			r, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 40}, deps)
			require.NoError(t, err, "real shared emitter/prepared factory")
			require.NotNil(t, r)
			var e session.TranscriptEntry
			require.NoError(t, json.Unmarshal([]byte(tc.payload), &e))
			e.ID = "source-" + tc.name
			if e.SubagentStart != nil {
				e.SubagentStart.SessionId = sid
			}
			if e.SubagentState != nil {
				e.SubagentState.SessionId = sid
			}
			if e.SubagentMessage != nil {
				e.SubagentMessage.SessionId = sid
			}
			if e.SubagentEnd != nil {
				e.SubagentEnd.SessionId = sid
			}
			e.AgentID = "mia"
			e.Timestamp = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
			a, err := s.AppendTranscriptAddressed(sid, e)
			require.NoError(t, err)
			p, err := r.Read(context.Background(), s)
			require.NoError(t, err)
			require.Len(t, p.Page.Entries, 1)
			require.Equal(t, pagingPositionOracle(sid, a), string(p.Page.Entries[0].Position))
			pagingAssertFrameOrigins(t, p, []string{tc.typ})
		})
	}
	t.Run("tool_media_resolves_each_reference_once", func(t *testing.T) { pagingMediaMemoProbe(t) })
	t.Run("source_question_card", func(t *testing.T) {
		s, sid, home := pagingProjectionFixture(t)
		terminal := terminalAskFixture()
		terminal.TranscriptSessionID = sid
		r, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 40}, historyPageProjectionDeps{ToolStore: newToolResultStore(home), TerminalAsk: terminal})
		require.NoError(t, err)
		for i, e := range askReplayEntries(t, terminal) {
			e.ID = fmt.Sprintf("ask-source-%d", i)
			e.AgentID = "mia"
			e.Timestamp = time.Date(2026, 10, 21, 12, 0, i, 0, time.UTC)
			require.NoError(t, s.AppendTranscriptStrict(sid, e))
		}
		p, err := r.Read(context.Background(), s)
		require.NoError(t, err)
		pagingAssertFrameOrigins(t, p, []string{"ask_user_question"})
	})
}

type pagingCountedMedia struct { // not-wire-format: process-edge wrapper around real media storage.
	media.MediaStore
	mu       sync.Mutex
	resolves map[string]int
	writes   int
}

func (m *pagingCountedMedia) mark(ref string) { m.mu.Lock(); defer m.mu.Unlock(); m.resolves[ref]++ }
func (m *pagingCountedMedia) Resolve(ref string) (string, error) {
	m.mark(ref)
	return m.MediaStore.Resolve(ref)
}
func (m *pagingCountedMedia) ResolveWithMeta(ref string) (string, media.MediaMeta, error) {
	m.mark(ref)
	return m.MediaStore.ResolveWithMeta(ref)
}
func (m *pagingCountedMedia) ResolveWithOpts(ref string, o media.ResolveOpts) (string, error) {
	m.mark(ref)
	return m.MediaStore.ResolveWithOpts(ref, o)
}
func (m *pagingCountedMedia) ResolveWithMetaOpts(ref string, o media.ResolveOpts) (string, media.MediaMeta, error) {
	m.mark(ref)
	return m.MediaStore.ResolveWithMetaOpts(ref, o)
}
func (m *pagingCountedMedia) ResolveWithCallerWorkspace(ref, w string) (string, media.MediaMeta, error) {
	m.mark(ref)
	return m.MediaStore.ResolveWithCallerWorkspace(ref, w)
}
func (m *pagingCountedMedia) Store(p string, meta media.MediaMeta, scope string) (string, error) {
	m.mu.Lock()
	m.writes++
	m.mu.Unlock()
	return m.MediaStore.Store(p, meta, scope)
}
func (m *pagingCountedMedia) ReleaseAll(scope string) error {
	m.mu.Lock()
	m.writes++
	m.mu.Unlock()
	return m.MediaStore.ReleaseAll(scope)
}

func pagingMediaMemoProbe(t *testing.T) {
	t.Helper()
	s, sid, home := pagingProjectionFixture(t)
	base := mediatest.NewFileMediaStore(t)
	image, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR4nGP4DwQACfsD/fteaysAAAAASUVORK5CYII=")
	require.NoError(t, err)
	decoded, err := png.Decode(bytes.NewReader(image))
	require.NoError(t, err, "independent valid PNG fixture, not an unrelated decoder failure")
	require.Equal(t, 1, decoded.Bounds().Dx())
	require.Equal(t, 1, decoded.Bounds().Dy())
	path := filepath.Join(home, "pixel.png")
	require.NoError(t, os.WriteFile(path, image, 0o600))
	refs := make([]string, 2)
	for i := range refs {
		refs[i], err = base.Store(path, media.MediaMeta{Filename: "pixel.png", ContentType: "image/png", CleanupPolicy: media.CleanupPolicyForgetOnly}, "history-fixture")
		require.NoError(t, err)
	}
	counter := &pagingCountedMedia{MediaStore: base, resolves: map[string]int{}}
	r, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 40}, historyPageProjectionDeps{ToolStore: newToolResultStore(home), MediaStore: counter})
	require.NoError(t, err)
	for i, ref := range refs {
		require.NoError(t, s.AppendTranscriptStrict(sid, session.TranscriptEntry{ID: fmt.Sprintf("media-owner-%d", i), Type: session.EntryTypeToolCall, AgentID: "mia", ToolCalls: []session.ToolCall{{ID: session.ToolCallID(fmt.Sprintf("media-call-%d", i)), Tool: "send_file", Status: "success", Result: map[string]any{"media": []any{map[string]any{"ref": ref, "filename": "pixel.png", "content_type": "image/png", "type": "image"}}}}}}))
	}
	p, err := r.Read(context.Background(), s)
	require.NoError(t, err)
	pagingAssertFrameOrigins(t, p, []string{"media", "tool_call_start", "tool_call_result"})
	counter.mu.Lock()
	defer counter.mu.Unlock()
	require.Zero(t, counter.writes, "preparation never stores/releases media")
	for _, ref := range refs {
		require.Equal(t, 1, counter.resolves[ref], "real required reference resolution once per cut, not once per candidate prefix or final carrier")
	}
}

func pagingSeparateTerminalCoverage(t *testing.T) {
	t.Helper()
	sid := "coverage-session"
	start := generated.ToolCallStartFrame{Type: "tool_call_start", SessionId: sid, CallId: "call", Tool: "read_file", Params: map[string]any{}}
	result := generated.ToolCallResultFrame{Type: "tool_call_result", SessionId: sid, CallId: "call", Tool: "read_file", Status: "success", Result: map[string]any{"text": "unpersisted terminal result"}}
	startBytes, err := json.Marshal(start)
	require.NoError(t, err)
	resultBytes, err := json.Marshal(result)
	require.NoError(t, err)
	emitted := map[string]bool{}
	recordEmittedIDs(emitted, start)
	frames := projectionFrames(sid, []projSnapshotItem{{kind: hubKindToolStart, id: "call", start: startBytes, end: resultBytes}}, emitted)
	require.Len(t, frames, 1, "a replayed start must not suppress an uncovered active terminal result")
	var got generated.ToolCallResultFrame
	require.NoError(t, json.Unmarshal(frames[0], &got))
	require.Equal(t, result.CallId, got.CallId)
	require.Equal(t, result.Result, got.Result)
	require.Equal(t, "tool_call_result", got.Type)
	recordEmittedIDs(emitted, result)
	require.Empty(t, projectionFrames(sid, []projSnapshotItem{{kind: hubKindToolStart, id: "call", start: startBytes, end: resultBytes}}, emitted), "already emitted terminal result remains deduplicated")
}
