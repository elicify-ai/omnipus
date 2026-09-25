// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// catchup_fixtures_test.go — #823 Lane A step 6 (BE-DESIGN.md §8.2): records
// the REAL frame sequences the gateway sends for the catch-up scenarios
// F1–F8, driving the real WSHandler over real WebSocket connections, the
// real agent loop and a scripted streaming provider. The SPA's store tests
// (Lane C) replay these files through handleFrame instead of hand-written
// frame orders.
//
// Output: src/store/__fixtures__/catchup/F*.json (repo root). Regenerate with
//
//	OMNIPUS_UPDATE_FIXTURES=1 go test -tags goolm,stdjson -run '^TestCatchUpFixtures$' ./pkg/gateway/
//
// Without that variable the test FAILS when what the gateway produces has
// drifted from the committed files — the same idea as verify-contracts.
//
// Values that differ on every run (ids, boot ids, timestamps, durations) are
// replaced consistently within a fixture: the same raw value always maps to
// the same placeholder ("sess-1", "turn-1", "msg-1", ...), so relationships —
// a token's message_id equal to the replayed message's id — survive.
// Timestamps become fixed, increasing instants; durations become 1.
// Sequence numbers are real.

package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// catchUpFixtureDir is where the recorder writes, relative to this package.
const catchUpFixtureDir = "../../src/store/__fixtures__/catchup"

// catchUpFixture is one recorded scenario (test-only file format).
type catchUpFixture struct { // not-wire-format: test fixture file for the SPA's store tests, not a gateway↔SPA frame.
	Scenario    string         `json:"scenario"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Expect      map[string]any `json:"expect"`
	Tabs        []fixtureTab   `json:"tabs"`
}

// fixtureTab is one browser tab's recorded conversation with the gateway.
type fixtureTab struct { // not-wire-format: test fixture file for the SPA's store tests.
	Name   string         `json:"name"`
	Events []fixtureEvent `json:"events"`
}

// fixtureEvent is one frame in either direction; Note marks a step the test
// took between frames (e.g. "connection dropped").
type fixtureEvent struct { // not-wire-format: test fixture file for the SPA's store tests.
	Dir   string          `json:"dir"` // "client→server", "server→client" or "note"
	Frame json.RawMessage `json:"frame,omitempty"`
	Note  string          `json:"note,omitempty"`
}

// fxTab is a live tab: a real WebSocket, read synchronously so a test
// decides exactly which frames the tab saw before it "dropped".
type fxTab struct {
	name   string
	conn   *websocket.Conn
	events []fixtureEvent
	closed bool
}

func dialFxTab(t *testing.T, url, name string) *fxTab {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(url, "http") + "/api/v1/chat/ws"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	require.NoError(t, err)
	tab := &fxTab{name: name, conn: conn}
	t.Cleanup(tab.close)
	// Dev-mode auth frame (connection setup, not recorded).
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"auth","token":"dev-token"}`)))
	return tab
}

func (tab *fxTab) close() {
	if !tab.closed {
		tab.closed = true
		_ = tab.conn.Close()
	}
}

func (tab *fxTab) note(msg string) {
	tab.events = append(tab.events, fixtureEvent{Dir: "note", Note: msg})
}

func (tab *fxTab) send(t *testing.T, frame map[string]any) {
	t.Helper()
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	tab.events = append(tab.events, fixtureEvent{Dir: "client→server", Frame: raw})
	require.NoError(t, tab.conn.WriteMessage(websocket.TextMessage, raw))
}

// readUntil reads (and records) frames until pred matches one, returning it.
func (tab *fxTab) readUntil(t *testing.T, pred func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		require.NoError(t, tab.conn.SetReadDeadline(deadline))
		_, raw, err := tab.conn.ReadMessage()
		require.NoError(t, err, "tab %s: reading", tab.name)
		tab.events = append(tab.events, fixtureEvent{Dir: "server→client", Frame: raw})
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		if pred(m) {
			return m
		}
	}
	t.Fatalf("tab %s: timed out waiting for a frame", tab.name)
	return nil
}

// readIdle reads (and records) frames until none arrives for idle. The
// read deadline that ends it breaks the gorilla connection for further
// reads, so it is only ever the LAST read on a tab.
func (tab *fxTab) readIdle(t *testing.T, idle time.Duration) {
	t.Helper()
	for {
		require.NoError(t, tab.conn.SetReadDeadline(time.Now().Add(idle)))
		_, raw, err := tab.conn.ReadMessage()
		if err != nil {
			return
		}
		tab.events = append(tab.events, fixtureEvent{Dir: "server→client", Frame: raw})
	}
}

func isType(typ string) func(map[string]any) bool {
	return func(m map[string]any) bool { return m["type"] == typ }
}

func isTurnDoneFrame(m map[string]any) bool {
	stats, ok := m["stats"].(map[string]any)
	return m["type"] == "done" && ok && stats["tokens"] != nil
}

func isTokenWithContent(content string) func(map[string]any) bool {
	return func(m map[string]any) bool { return m["type"] == "token" && m["content"] == content }
}

func frameSeqOf(m map[string]any) int64 {
	if v, ok := m["seq"].(float64); ok {
		return int64(v)
	}
	return 0
}

// fixtureRig is a real gateway (WSHandler + agent loop + scripted provider).
type fixtureRig struct {
	url      string
	h        *WSHandler
	provider *controllableStreamProvider
}

func newFixtureRig(t *testing.T, rounds ...providerRound) *fixtureRig {
	t.Helper()
	provider := newControllableStreamProvider(rounds...)
	srv, h, _ := newControllableStreamTestServer(t, provider)
	return &fixtureRig{url: srv.URL, h: h, provider: provider}
}

func (r *fixtureRig) store(sessionID string) *session.UnifiedStore {
	return r.h.agentLoop.ResolveSessionStore(sessionID)
}

func streamTokens(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d ", prefix, i)
	}
	return out
}

func waitPaused(t *testing.T, paused <-chan struct{}) {
	t.Helper()
	select {
	case <-paused:
	case <-time.After(15 * time.Second):
		t.Fatal("BLOCKED: provider never reached its pause point")
	}
}

// ---------------------------------------------------------------------
// Normalization
// ---------------------------------------------------------------------

var fixtureIDKeys = map[string]string{
	"session_id": "sess", "producing_session_id": "sess", "id": "id", "boot_id": "boot",
	"turn_id": "turn", "message_id": "msg", "client_message_id": "client", "call_id": "call",
	"tool_call_id": "call", "parent_call_id": "call", "span_id": "span", "approval_id": "approval",
	"entry_id": "entry",
}

var fixtureTimeKeys = map[string]bool{"timestamp": true, "emitted_at": true, "started_at": true}

type fixtureNormalizer struct {
	ids       map[string]string
	counts    map[string]int
	timeCount int
}

func newFixtureNormalizer() *fixtureNormalizer {
	return &fixtureNormalizer{ids: map[string]string{}, counts: map[string]int{}}
}

func (n *fixtureNormalizer) id(prefix, raw string) string {
	if raw == "" {
		return raw
	}
	if p, ok := n.ids[raw]; ok {
		return p
	}
	n.counts[prefix]++
	p := fmt.Sprintf("%s-%d", prefix, n.counts[prefix])
	n.ids[raw] = p
	return p
}

// time replaces a timestamp with the next fixed instant. Timestamps are
// numbered per occurrence, not per value: several are second-granular, so
// two frames a run happens to emit within one second would otherwise share
// a placeholder on one run and not on the next.
func (n *fixtureNormalizer) time(_ string) string {
	n.timeCount++
	return time.Date(2026, 1, 1, 0, 0, n.timeCount, 0, time.UTC).Format(time.RFC3339)
}

// norm rewrites run-specific values in place, visiting map keys in sorted
// order so the placeholder numbering is deterministic (Go map iteration is
// not). key is the JSON key v was found under.
func (n *fixtureNormalizer) norm(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			x[k] = n.norm(x[k], k)
		}
		return x
	case []any:
		for i := range x {
			x[i] = n.norm(x[i], "")
		}
		return x
	case string:
		if prefix := fixtureIDKeys[key]; prefix != "" {
			return n.id(prefix, x)
		}
		if fixtureTimeKeys[key] {
			return n.time(x)
		}
		return x
	case float64:
		if key == "duration_ms" {
			return float64(1)
		}
		return x
	default:
		return v
	}
}

func (n *fixtureNormalizer) frame(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var v any
	require.NoError(t, json.Unmarshal(raw, &v))
	out, err := json.Marshal(n.norm(v, ""))
	require.NoError(t, err)
	return out
}

// finishFixture normalizes every tab's events and writes or checks the file.
func finishFixture(t *testing.T, fx catchUpFixture, tabs ...*fxTab) {
	t.Helper()
	n := newFixtureNormalizer()
	for _, tab := range tabs {
		rec := fixtureTab{Name: tab.name}
		for _, ev := range tab.events {
			if ev.Frame != nil {
				ev.Frame = n.frame(t, ev.Frame)
			}
			rec.Events = append(rec.Events, ev)
		}
		fx.Tabs = append(fx.Tabs, rec)
	}
	got, err := json.MarshalIndent(fx, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')
	path := filepath.Join(catchUpFixtureDir, fx.Scenario+".json")
	if os.Getenv("OMNIPUS_UPDATE_FIXTURES") == "1" {
		require.NoError(t, os.MkdirAll(catchUpFixtureDir, 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "fixture %s missing — regenerate with OMNIPUS_UPDATE_FIXTURES=1", path)
	if string(want) != string(got) {
		gotPath := filepath.Join(t.TempDir(), fx.Scenario+".got.json")
		_ = os.WriteFile(gotPath, got, 0o644)
		if keep := os.Getenv("OMNIPUS_FIXTURE_DRIFT_DIR"); keep != "" {
			_ = os.WriteFile(filepath.Join(keep, fx.Scenario+".got.json"), got, 0o644)
		}
		t.Fatalf("fixture %s has drifted from what the gateway produces — regenerate with "+
			"OMNIPUS_UPDATE_FIXTURES=1 and review the diff (this run's output: %s)", path, gotPath)
	}
}

// ---------------------------------------------------------------------
// Scenarios
// ---------------------------------------------------------------------

// TestCatchUpFixtures records F1–F8.
func TestCatchUpFixtures(t *testing.T) {
	t.Run("F1_live_turn", recordF1LiveTurn)
	t.Run("F2_incremental_mid_message", recordF2IncrementalMidMessage)
	t.Run("F3_snapshot_mid_message", recordF3SnapshotMidMessage)
	t.Run("F4_snapshot_persist_inside_bind_to_read", recordF4PersistInsideBindToRead)
	t.Run("F5_two_tabs", recordF5TwoTabs)
	t.Run("F6_tab_switch_and_return", recordF6TabSwitchAndReturn)
	t.Run("F7_restart_boot_mismatch", recordF7RestartBootMismatch)
	t.Run("F8_offline_message", recordF8OfflineMessage)
}

func sendMessage(t *testing.T, tab *fxTab, sessionID, content, clientID string) {
	t.Helper()
	frame := map[string]any{"type": "message", "content": content, "client_message_id": clientID}
	if sessionID != "" {
		frame["session_id"] = sessionID
	}
	tab.send(t, frame)
}

func attach(t *testing.T, tab *fxTab, sessionID string, sinceSeq int64, bootID string) {
	t.Helper()
	frame := map[string]any{"type": "attach_session", "session_id": sessionID}
	if bootID != "" {
		frame["since_seq"] = sinceSeq
		frame["boot_id"] = bootID
	}
	tab.send(t, frame)
}

func bootIDOf(m map[string]any) string {
	s, _ := m["boot_id"].(string)
	return s
}

// F1: a live two-round turn with one tool call, one tab.
func recordF1LiveTurn(t *testing.T) {
	rig := newFixtureRig(t,
		providerRound{
			tokens: []string{"Let me ", "check."},
			toolCall: &providers.ToolCall{
				ID: "call-f1", Name: "list_tasks", Arguments: map[string]any{"role": "assignee"},
			},
		},
		providerRound{tokens: []string{"All ", "done."}},
	)
	// Allow the scripted tool for the agent (policy is data, ADR-077).
	for _, agentID := range rig.h.agentLoop.GetRegistry().ListAgentIDs() {
		if ag, ok := rig.h.agentLoop.GetRegistry().GetAgent(agentID); ok {
			ag.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"list_tasks": "allow"}})
		}
	}
	a := dialFxTab(t, rig.url, "A")
	a.readUntil(t, isType("session_state"))
	sendMessage(t, a, "", "check my tasks", "cm-f1")
	a.readUntil(t, isTurnDoneFrame)
	a.readIdle(t, 300*time.Millisecond)
	finishFixture(t, catchUpFixture{
		Scenario: "F1",
		Title:    "Live turn: two rounds with one tool call, one tab",
		Description: "Tab A sends a message with no session id; the gateway mints the session " +
			"(session_started carries the start cursor), echoes the user message, ticks received→working, " +
			"streams round 1, runs one tool call, streams round 2 and sends the turn's done.",
		Expect: map[string]any{
			"user_message":             "check my tasks",
			"assistant_messages":       []string{"Let me check.", "All done."},
			"one_bubble_per_message":   true,
			"cursor_after_last_frame":  "the seq of the done frame",
			"tool_calls":               []string{"list_tasks"},
			"user_message_above_reply": true,
		},
	}, a)
}

// F2: reconnect mid-message with a servable cursor → incremental catch-up.
func recordF2IncrementalMidMessage(t *testing.T) {
	toks := streamTokens("w", 10)
	rig := newFixtureRig(t, providerRound{tokens: toks})
	paused, resume := rig.provider.pauseAfter(0, 5)
	a := dialFxTab(t, rig.url, "A")
	a.readUntil(t, isType("session_state"))
	sendMessage(t, a, "", "stream something", "cm-f2")
	started := a.readUntil(t, isType("session_started"))
	sid, _ := started["session_id"].(string)
	boot := bootIDOf(started)
	waitPaused(t, paused)
	last := a.readUntil(t, isTokenWithContent(toks[2]))
	cursor := frameSeqOf(last)
	a.note("connection dropped here: frames after this point never reached tab A")
	a.close()

	b := dialFxTab(t, rig.url, "A (reconnected)")
	b.readUntil(t, isType("session_state"))
	attach(t, b, sid, cursor, boot)
	b.readUntil(t, isType("catch_up_complete"))
	close(resume)
	b.readUntil(t, isTurnDoneFrame)
	b.readIdle(t, 300*time.Millisecond)
	finishFixture(t, catchUpFixture{
		Scenario: "F2",
		Title:    "Reconnect mid-message with a servable cursor: incremental catch-up",
		Description: "The tab's connection drops after token w2 while the answer keeps streaming (w3–w5 are " +
			"published while it is away). It reconnects (same store state, cursor = seq of w2) and attaches with " +
			"{since_seq, boot_id}: it gets session_state, exactly the missed frames (w3–w5), catch_up_complete, " +
			"then the rest live (w6–w9) and done.",
		Expect: map[string]any{
			"user_message":           "stream something",
			"assistant_messages":     []string{strings.Join(toks, "")},
			"one_bubble_per_message": true,
			"catch_up_mode":          "incremental",
			"no_duplicate_text":      true,
		},
	}, a, b)
}

// F3: reconnect mid-message with no cursor (reload) → snapshot + projection.
func recordF3SnapshotMidMessage(t *testing.T) {
	toks := streamTokens("s", 10)
	rig := newFixtureRig(t, providerRound{tokens: toks})
	paused, resume := rig.provider.pauseAfter(0, 4)
	a := dialFxTab(t, rig.url, "A")
	a.readUntil(t, isType("session_state"))
	sendMessage(t, a, "", "stream then reload", "cm-f3")
	started := a.readUntil(t, isType("session_started"))
	sid, _ := started["session_id"].(string)
	waitPaused(t, paused)
	a.readUntil(t, isTokenWithContent(toks[4]))
	a.note("page reloaded here: the store is empty and has no cursor for this session")
	a.close()

	b := dialFxTab(t, rig.url, "A (after reload)")
	b.readUntil(t, isType("session_state"))
	attach(t, b, sid, 0, "")
	b.readUntil(t, isType("catch_up_complete"))
	close(resume)
	b.readUntil(t, isTurnDoneFrame)
	b.readIdle(t, 300*time.Millisecond)
	finishFixture(t, catchUpFixture{
		Scenario: "F3",
		Title:    "Reload mid-message: snapshot with the active-turn projection",
		Description: "After a reload the tab attaches with no cursor. The gateway answers with session_snapshot " +
			"(reason unknown_position), session_state (active_turn present), the transcript replay (the user " +
			"message), the answer-so-far as one unsequenced token{replace:true, message_id} (it is not persisted " +
			"yet), catch_up_complete, then the rest of the answer live and done. Only the second tab's events " +
			"describe the store after the reload; tab A's events are the pre-reload history.",
		Expect: map[string]any{
			"user_message":           "stream then reload",
			"assistant_messages":     []string{strings.Join(toks, "")},
			"one_bubble_per_message": true,
			"catch_up_mode":          "snapshot",
			"snapshot_reason":        "unknown_position",
		},
	}, a, b)
}

// F4: the answer is persisted inside the bind→read window of a snapshot.
func recordF4PersistInsideBindToRead(t *testing.T) {
	toks := streamTokens("p", 8)
	rig := newFixtureRig(t, providerRound{tokens: toks})
	paused, resume := rig.provider.pauseAfter(0, 3)
	a := dialFxTab(t, rig.url, "A")
	a.readUntil(t, isType("session_state"))
	sendMessage(t, a, "", "finish while I reload", "cm-f4")
	started := a.readUntil(t, isType("session_started"))
	sid, _ := started["session_id"].(string)
	waitPaused(t, paused)
	a.readUntil(t, isTokenWithContent(toks[3]))
	a.note("page reloaded here")
	a.close()

	full := strings.Join(toks, "")
	setAttachHook(t, func(string) {
		// Inside the bind→read window: let the turn finish and persist.
		close(resume)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			entries, _ := rig.store(sid).ReadTranscript(sid)
			for _, e := range entries {
				if e.Role == "assistant" && e.Content == full {
					time.Sleep(100 * time.Millisecond) // let the done publish
					return
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	b := dialFxTab(t, rig.url, "A (after reload)")
	b.readUntil(t, isType("session_state"))
	attach(t, b, sid, 0, "")
	b.readUntil(t, isTurnDoneFrame)
	b.readIdle(t, 300*time.Millisecond)
	finishFixture(t, catchUpFixture{
		Scenario: "F4",
		Title:    "Snapshot where the answer is persisted between the bind and the transcript read",
		Description: "The turn finishes while the reload's snapshot is being prepared. The replay already " +
			"contains the whole answer (replay_message id = the live message_id), so no projection token is " +
			"sent for it. The tokens and done published after the bind still arrive live after " +
			"catch_up_complete: the client must ignore those tokens because the bubble with that message_id is " +
			"already complete (BE-DESIGN.md §6.3), and treat done as an idempotent close.",
		Expect: map[string]any{
			"user_message":           "finish while I reload",
			"assistant_messages":     []string{full},
			"one_bubble_per_message": true,
			"no_duplicate_text":      true,
			"catch_up_mode":          "snapshot",
		},
	}, a, b)
}

// F5: two tabs on one chat.
func recordF5TwoTabs(t *testing.T) {
	toks := streamTokens("t", 6)
	rig := newFixtureRig(t, providerRound{tokens: toks})
	paused, resume := rig.provider.pauseAfter(0, 1)
	a := dialFxTab(t, rig.url, "A")
	a.readUntil(t, isType("session_state"))
	sendMessage(t, a, "", "hello from tab A", "cm-f5")
	started := a.readUntil(t, isType("session_started"))
	sid, _ := started["session_id"].(string)
	waitPaused(t, paused)
	a.readUntil(t, isTokenWithContent(toks[1]))

	b := dialFxTab(t, rig.url, "B")
	b.readUntil(t, isType("session_state"))
	attach(t, b, sid, 0, "")
	b.readUntil(t, isType("catch_up_complete"))
	close(resume)
	a.readUntil(t, isTurnDoneFrame)
	b.readUntil(t, isTurnDoneFrame)
	a.readIdle(t, 300*time.Millisecond)
	b.readIdle(t, 300*time.Millisecond)
	finishFixture(t, catchUpFixture{
		Scenario: "F5",
		Title:    "Two tabs on one chat",
		Description: "Tab A sends; tab B opens the same chat mid-answer (snapshot: the user message from the " +
			"transcript, the answer so far from the projection) and from then on both tabs receive the " +
			"identical numbered frames. Both end with the same transcript and the same cursor.",
		Expect: map[string]any{
			"user_message":            "hello from tab A",
			"assistant_messages":      []string{strings.Join(toks, "")},
			"both_tabs_same_messages": true,
			"both_tabs_same_cursor":   true,
		},
	}, a, b)
}

// F6: switch to another chat while the turn finishes, then switch back.
func recordF6TabSwitchAndReturn(t *testing.T) {
	toks := streamTokens("x", 8)
	rig := newFixtureRig(t, providerRound{tokens: toks})
	paused, resume := rig.provider.pauseAfter(0, 2)
	a := dialFxTab(t, rig.url, "A")
	a.readUntil(t, isType("session_state"))
	sendMessage(t, a, "", "answer while I look elsewhere", "cm-f6")
	started := a.readUntil(t, isType("session_started"))
	sid, _ := started["session_id"].(string)
	boot := bootIDOf(started)
	waitPaused(t, paused)
	lastTok := a.readUntil(t, isTokenWithContent(toks[2]))
	cursor := frameSeqOf(lastTok)

	other, err := rig.store(sid).NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, rig.store(other.ID).AppendTranscriptStrict(other.ID, session.TranscriptEntry{
		ID: "other-user-1", Role: "user", Content: "an older chat", Timestamp: time.Now().UTC(),
	}))
	a.note("user switches to another chat")
	attach(t, a, other.ID, 0, "")
	a.readUntil(t, isType("catch_up_complete"))
	close(resume)
	// Wait for the first chat's turn to finish while the tab is elsewhere.
	full := strings.Join(toks, "")
	deadline := time.Now().Add(10 * time.Second)
	for done := false; !done && time.Now().Before(deadline); {
		entries, _ := rig.store(sid).ReadTranscript(sid)
		for _, e := range entries {
			if e.Role == "assistant" && e.Content == full {
				done = true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond) // let the done publish
	a.note("user switches back to the first chat (its bucket kept cursor = seq of x2)")
	attach(t, a, sid, cursor, boot)
	a.readUntil(t, isType("catch_up_complete"))
	a.readIdle(t, 300*time.Millisecond)
	finishFixture(t, catchUpFixture{
		Scenario: "F6",
		Title:    "Switch to another chat while the answer finishes, then switch back",
		Description: "The tab moves to another chat at token x2 (snapshot of that chat); the first chat's turn " +
			"finishes meanwhile. Switching back attaches with the first chat's kept cursor: an incremental " +
			"catch-up delivers exactly x3–x7 and done, then catch_up_complete.",
		Expect: map[string]any{
			"user_message":                  "answer while I look elsewhere",
			"assistant_messages":            []string{full},
			"catch_up_mode_on_return":       "incremental",
			"other_chat_messages_untouched": true,
		},
	}, a)
}

// F7: the gateway restarted; the tab's cursor carries the old boot id.
func recordF7RestartBootMismatch(t *testing.T) {
	rig := newFixtureRig(t)
	meta, err := rig.h.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	store := rig.store(meta.ID)
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		ID: "user-f7", Role: "user", Content: "question before the restart", Timestamp: time.Now().UTC(),
		ClientMessageID: "cm-f7",
	}))
	a := dialFxTab(t, rig.url, "A")
	a.readUntil(t, isType("session_state"))
	a.note("the gateway restarted mid-answer; the tab still holds {boot_id: old, seq: 42} for this chat")
	attach(t, a, meta.ID, 42, "boot-of-the-previous-gateway-run")
	a.readUntil(t, isType("catch_up_complete"))
	a.readIdle(t, 300*time.Millisecond)
	finishFixture(t, catchUpFixture{
		Scenario: "F7",
		Title:    "Gateway restart: the cursor's boot id no longer matches",
		Description: "The tab attaches with a cursor from the previous gateway run. The gateway answers with " +
			"session_snapshot{reason: boot_mismatch}, session_state (no active_turn — the interrupted turn is " +
			"gone), the transcript (only the user's question; the answer was never persisted) and " +
			"catch_up_complete. The client shows the interrupted answer as \"couldn't be finished\" " +
			"(BE-DESIGN.md §6.5).",
		Expect: map[string]any{
			"user_message":      "question before the restart",
			"snapshot_reason":   "boot_mismatch",
			"active_turn":       nil,
			"unfinished_answer": true,
		},
	}, a)
}

// F8: a message typed while offline is sent after the reconnect's catch-up.
func recordF8OfflineMessage(t *testing.T) {
	first := []string{"First ", "answer."}
	second := []string{"Second ", "answer."}
	rig := newFixtureRig(t, providerRound{tokens: first}, providerRound{tokens: second})
	a := dialFxTab(t, rig.url, "A")
	a.readUntil(t, isType("session_state"))
	sendMessage(t, a, "", "first question", "cm-f8-1")
	started := a.readUntil(t, isType("session_started"))
	sid, _ := started["session_id"].(string)
	boot := bootIDOf(started)
	done := a.readUntil(t, isTurnDoneFrame)
	a.readIdle(t, 300*time.Millisecond)
	cursor := frameSeqOf(done)
	for _, ev := range a.events {
		var m map[string]any
		if ev.Frame != nil && json.Unmarshal(ev.Frame, &m) == nil && frameSeqOf(m) > cursor {
			cursor = frameSeqOf(m)
		}
	}
	a.note("connection lost; the user types \"second question\" while offline (kept in the pending tail)")
	a.close()

	b := dialFxTab(t, rig.url, "A (back online)")
	b.readUntil(t, isType("session_state"))
	attach(t, b, sid, cursor, boot)
	b.readUntil(t, isType("catch_up_complete"))
	sendMessage(t, b, sid, "second question", "cm-f8-2")
	b.readUntil(t, isTurnDoneFrame)
	b.readIdle(t, 300*time.Millisecond)
	finishFixture(t, catchUpFixture{
		Scenario: "F8",
		Title:    "Message typed while offline",
		Description: "After a completed turn the tab goes offline; the user types a message, which the client " +
			"keeps in its pending tail. On reconnect the tab first attaches (an empty incremental catch-up), then " +
			"sends the queued message: its user_message (matched by client_message_id) and every later frame " +
			"come after catch_up_complete, so the message is placed at the end, followed by its answer.",
		Expect: map[string]any{
			"user_messages":           []string{"first question", "second question"},
			"assistant_messages":      []string{"First answer.", "Second answer."},
			"offline_message_at_end":  true,
			"pending_bubble_resolved": "cm-f8-2",
		},
	}, a, b)
}
