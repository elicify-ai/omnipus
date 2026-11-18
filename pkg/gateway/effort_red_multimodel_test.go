package gateway

// CHECK-08 / S-88/S-95: HTTP stream failure → real fallback → production
// booking/snapshot writers → live done, attach and REST → by-model cells.
// No accounting entries or display metadata are constructed by this fixture.
// INFO-2 resolved (QA round 4 correction): callProviderOnce routes BOTH the
// single-candidate and the multi-candidate branches through runProviderAttempt
// (and thus ChatStream) — every candidate streams when the provider and the
// channel allow it — so the primary's SSE error branch below executes and the
// stream assertion is a live production oracle, not a coded placeholder.
// CHECK5 m2 gap (this round): the primary now emits a reasoning-only delta
// before its usage-carrying error. Those bytes are forward progress (the
// production tool-call-progress callback stores them), never visible output,
// so they must not trigger the C-10 no-fallback-after-visible-bytes rule
// (founder decision 2026-09-14 / UAT E-15c; spec S-88's turn-1 totals are
// unchanged by a delta that books nothing).
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type effortProductionTurn struct {
	al      *agent.AgentLoop
	api     *restAPI
	handler *WSHandler
	msgBus  *bus.MessageBus
	wc      *wsConn
	sid     string
}

func newEffortProductionTurn(t *testing.T, cfg *config.Config) *effortProductionTurn {
	t.Helper()
	seedAgentEntities(t, config.OmnipusHomeDir(), cfg.Agents.List)
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newWSHandler(msgBus, al, "")
	handler.webchatCh = newWebchatChannel(handler)
	msgBus.SetStreamDelegate(handler)
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning, Origin: &session.Origin{Kind: session.OriginKindChat}, AgentID: "mia", WorkspaceID: testHarnessWorkspaceMembershipID, OwnerScopeKind: session.OwnerScopeHuman}))
	wc := makeTestConn()
	wc.sendCh = make(chan []byte, 1024)
	t.Cleanup(wc.close)
	bindTestConnToSession(handler, "effort-production", meta.ID, wc)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- al.Run(ctx) }()
	t.Cleanup(func() { cancel(); require.NoError(t, <-done) })
	return &effortProductionTurn{al: al, api: &restAPI{agentLoop: al, homePath: config.OmnipusHomeDir()}, handler: handler, msgBus: msgBus, wc: wc, sid: meta.ID}
}
func (f *effortProductionTurn) turn(t *testing.T, prompt, model string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f.handler.handleChatMessage(ctx, "effort-production", f.sid, prompt, "mia", nil, model, "", f.wc)
	select {
	case final := <-f.msgBus.OutboundChan():
		require.Contains(t, final.Content, "ANSWER-", "positive control: real provider completed the turn")
	case <-ctx.Done():
		t.Fatal("real provider turn did not complete")
	}
	for {
		select {
		case raw := <-f.wc.sendCh:
			var frame map[string]any
			require.NoError(t, json.Unmarshal(raw, &frame))
			if frame["type"] == "done" && frame["message_id"] != nil {
				return frame
			}
		case <-ctx.Done():
			t.Fatal("completed provider turn emitted no live done")
			return nil
		}
	}
}

// turnDone drives one turn and returns its live done frame together with the
// visible token stream it streamed. Unlike turn it does not wait for a bus
// outbound first: a turn whose chain ends without an answer publishes no bus
// message at all (loop.go publishes only result.finalContent != ""), but it
// still finalizes its streamer and emits the done frame — so the failed-chain
// shape stays observable without a timeout-shaped wait. Every non-done frame
// is skipped; token frames are accumulated in arrival order.
func (f *effortProductionTurn) turnDone(t *testing.T, prompt, model string) (map[string]any, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f.handler.handleChatMessage(ctx, "effort-production", f.sid, prompt, "mia", nil, model, "", f.wc)
	visible := &strings.Builder{}
	for {
		select {
		case raw := <-f.wc.sendCh:
			var frame map[string]any
			require.NoError(t, json.Unmarshal(raw, &frame))
			switch frame["type"] {
			case "token":
				if c, ok := frame["content"].(string); ok {
					visible.WriteString(c)
				}
			case "done":
				if frame["message_id"] != nil {
					return frame, visible.String()
				}
			}
		case <-ctx.Done():
			t.Fatal("turn emitted no live done frame")
			return nil, ""
		}
	}
}

func effortProductionConfig(t *testing.T, primary, fallback string) *config.Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	raw, err := json.Marshal(map[string]any{"version": 1, "agents": map[string]any{"defaults": map[string]any{"home": filepath.Join(home, "workspace"), "max_tokens": 4096, "max_tool_iterations": 4}, "list": []any{map[string]any{"id": "mia", "provider": "openrouter", "model": "A", "reasoning_effort": "high", "context_window_override": 128000, "fallback_models": []any{map[string]any{"provider": "openai", "model": "B", "reasoning_effort": "medium"}}}}}, "providers": []any{map[string]any{"provider": "openrouter", "model": "A", "api_base": primary, "api_key": "test"}, map[string]any{"provider": "openai", "model": "B", "api_base": fallback, "api_key": "test"}}})
	require.NoError(t, err)
	var cfg config.Config
	require.NoError(t, json.Unmarshal(raw, &cfg))
	cfg.Sandbox.ToolPolicies = config.DefaultToolPolicyCeiling()
	// Agent entities have their own codec; config.json deliberately ignores the
	// retired inline agents.list. Decode the entity input through AgentConfig.
	var entity config.AgentConfig
	require.NoError(t, json.Unmarshal([]byte(`{"id":"mia","provider":"openrouter","model":"A","reasoning_effort":"high","fallback_models":[{"provider":"openai","model":"B","reasoning_effort":"medium"}]}`), &entity))
	window := 128000
	entity.ContextWindowOverride = &window
	cfg.Agents.List = []config.AgentConfig{entity}
	// Actual parsed catalog supplies model admission, context and served levels.
	model := func(id string, levels []string) map[string]any {
		return map[string]any{"id": id, "name": id, "context_window": 128000, "max_output_tokens": 8192, "input_modalities": []string{"text"}, "tool_call": true, "status": "active", "reasoning": map[string]any{"levels": levels, "switch": false}}
	}
	provider := func(id string, m map[string]any) map[string]any {
		return map[string]any{"id": id, "name": id, "company": id, "api": "https://example.invalid/v1", "protocol": "openai-compatible", "protocols": []any{map[string]any{"protocol": "openai-compatible", "api": "https://example.invalid/v1"}}, "tier": "popular", "auth_methods": []string{"api_key"}, "aliases": []string{}, "models": []any{m}}
	}
	doc, err := json.Marshal(map[string]any{"schema_version": "2.0.0", "version": "v2026.10.31", "updated_at": "2026-10-31T00:00:00Z", "source": "test", "default_resize_limits": map[string]any{"long_edge_px": 2048, "max_bytes": 10485760}, "providers": []any{provider("openrouter", model("A", []string{"low", "medium", "high"})), provider("openai", model("B", []string{"low", "high"}))}})
	require.NoError(t, err)
	cat, err := catalog.NewCatalog(doc)
	require.NoError(t, err)
	previous := providers.ProviderCatalog()
	providers.SetCatalog(cat)
	t.Cleanup(func() { providers.SetCatalog(previous) })
	return &cfg
}
func effortPublicJSON(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}
func effortModelCells(t *testing.T, f *effortProductionTurn) map[string]any {
	t.Helper()
	meta, err := f.al.GetSessionStore().GetMeta(f.sid)
	require.NoError(t, err)
	return effortPublicJSON(t, meta.Stats.ByModel)
}
func assertProductionAnswer(t *testing.T, row map[string]any, tokens float64, effort map[string]any) {
	t.Helper()
	assert.Equal(t, tokens, row["turn_reasoning_tokens"])
	assert.Equal(t, effort, row["effort"])
	assert.NotContains(t, row, "reasoning_tokens")
	assert.NotEqual(t, "This saved message could not be displayed.", row["content"])
}

func TestT_GW_S88_RealFailedStreamFallbackLaterZeroAndHistory(t *testing.T) {
	var mu sync.Mutex
	acalls, bcalls := 0, 0
	var aStream any
	// CHECK5 m2 handshake: A holds its failure back until the test has
	// observed the reasoning-only delta in the production progress callback,
	// so the "progress reached" assertion below is deterministic, never a
	// race. Buffered: a send never blocks, even if the waiter already timed
	// out.
	aProgressSeen := make(chan struct{}, 1)
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		acalls++
		mu.Unlock()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("primary HTTP request JSON: %v", err)
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		aStream = body["stream"]
		mu.Unlock()
		if body["stream"] != true {
			// A valid plain-HTTP error lets the actual fallback finish. The main
			// test still requires streaming; this branch is NOT an accepted oracle.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{"error":{"code":"402","message":"billed attempt"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		// CHECK5 m2: a reasoning-only delta streams FIRST. No `content` field:
		// its bytes are forward progress only — the adapter keeps just the byte
		// count and hands it to the production progress callback (founder
		// E-15c) — never user-visible output, so the C-10
		// no-fallback-after-visible-bytes guard must stay silent on them.
		// `reasoning` is OpenRouter's normalised spelling (the adapter's first
		// non-empty spelling wins). The existing C-10 guard itself remains in
		// force; usage below is still real provider JSON, decoded into
		// StreamProviderError by the adapter.
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning\":\"weighing fallback candidates\"}}]}\n\n")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		// Bounded hold before the failure: release it once the test observed
		// the progress store, or after 5 s. If the callback never fires (that
		// regression), the error still flows, the fallback still answers, and
		// the explicit sawProgress assertion below turns the gap into a named
		// red instead of a hang.
		select {
		case <-aProgressSeen:
		case <-time.After(5 * time.Second):
		}
		fmt.Fprint(w, "data: {\"error\":{\"code\":\"402\",\"message\":\"billed partial attempt\"},\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"error\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":300,\"total_tokens\":310,\"completion_tokens_details\":{\"reasoning_tokens\":120}}}\n\ndata: [DONE]\n\n")
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		bcalls++
		n := bcalls
		mu.Unlock()
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		usage := map[string]any{"prompt_tokens": 5, "completion_tokens": 80, "total_tokens": 85}
		if n == 2 {
			usage = map[string]any{"prompt_tokens": 5, "completion_tokens": 50, "total_tokens": 55, "completion_tokens_details": map[string]any{"reasoning_tokens": 0}}
		}
		choice := map[string]any{"index": 0, "finish_reason": "stop"}
		if body["stream"] == true {
			choice["delta"] = map[string]any{"content": fmt.Sprintf("ANSWER-%d", n)}
		} else {
			choice["message"] = map[string]any{"role": "assistant", "content": fmt.Sprintf("ANSWER-%d", n)}
		}
		raw, err := json.Marshal(map[string]any{"choices": []any{choice}, "usage": usage})
		require.NoError(t, err)
		if body["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw)
		}
	}))
	defer b.Close()
	cfg := effortProductionConfig(t, a.URL, b.URL)
	f := newEffortProductionTurn(t, cfg)
	// CHECK5 m2: watch the production progress callback through its only
	// test-reachable reader (AgentLoop.ProgressForSession → the turn's
	// tool-call-progress snapshot). The reasoning-only delta must store
	// reasoning bytes mid-turn — before the fixture releases A's failure —
	// so this observation is ordered, not a race.
	var sawPrimaryProgress atomic.Bool
	pollStop := make(chan struct{})
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		key := "agent:mia:session:" + f.sid
		for {
			select {
			case <-pollStop:
				return
			default:
			}
			if snap, ok := f.al.ProgressForSession(key); ok && snap.ReasoningBytes > 0 {
				sawPrimaryProgress.Store(true)
				aProgressSeen <- struct{}{}
				return
			}
			time.Sleep(200 * time.Microsecond)
		}
	}()
	done1, visible1 := f.turnDone(t, "first", "")
	close(pollStop)
	<-pollDone
	mu.Lock()
	streamed := aStream
	bcallsAfterTurnOne := bcalls
	mu.Unlock()
	require.Equal(t, true, streamed, "BLOCKED: streaming multi-candidate fallback not implemented — required by S-88; both actual HTTP peers completed, but A used plain Chat")
	stats1 := effortRequireType[map[string]any](t, done1["stats"])
	// CHECK5 m2, assertion 1 — the fallback answered: a chain ended by the
	// reasoning-only delta would fail the turn and stamp turn_failed on the
	// done frame (C-10 counts visible bytes only; founder E-15c).
	assert.NotContains(t, stats1, "turn_failed",
		"the reasoning-only delta must not end the chain: C-10 counts visible bytes only, so B answers and turn one does not end failed")
	// CHECK5 m2, assertion 2 — the primary's progress callback really ran:
	// without this the fixture would exercise nothing (a delta that never
	// reached the callback leaves reasoningBytes 0 and any mutant vacuously
	// alive). Ordered by the fixture handshake above.
	require.True(t, sawPrimaryProgress.Load(),
		"the primary's reasoning-only delta reached the production progress callback (ProgressForSession recorded reasoning bytes mid-turn)")
	// CHECK5 m2, assertion 3 — B was called exactly once for turn one, and
	// nothing of the primary's reasoning became visible output: the live
	// token stream is exactly B's answer.
	assert.Equal(t, 1, bcallsAfterTurnOne,
		"B answered the failed primary exactly once (and A neither retried nor leaked visible text)")
	assert.Equal(t, "ANSWER-1", visible1,
		"no visible primary token: the live stream is exactly B's answer — reasoning bytes are never forwarded as output")
	select {
	case final := <-f.msgBus.OutboundChan():
		require.Contains(t, final.Content, "ANSWER-", "positive control: real provider completed the turn")
	case <-time.After(2 * time.Second):
		t.Fatal("positive control: the answered turn published no final message")
	}
	assert.Equal(t, float64(120), stats1["reasoning_tokens"])
	expected := map[string]any{"chosen": "medium", "sent": "low", "reason": "mapped", "provider": "openai", "model": "B"}
	assert.Equal(t, expected, stats1["effort"])
	cells := effortModelCells(t, f)
	require.Contains(t, cells, "A")
	require.Contains(t, cells, "B")
	ca := effortRequireType[map[string]any](t, cells["A"])
	cb := effortRequireType[map[string]any](t, cells["B"])
	assert.Equal(t, float64(300), ca["out"])
	assert.Equal(t, float64(120), ca["reasoning"])
	assert.Equal(t, float64(80), cb["out"])
	assert.NotContains(t, cb, "reasoning", "B remains not-reported after turn one, not credited A's 120")
	done2 := f.turn(t, "second", "B")
	assert.Equal(t, float64(0), effortRequireType[map[string]any](t, done2["stats"])["reasoning_tokens"])
	cells = effortModelCells(t, f)
	assert.Equal(t, float64(120), effortRequireType[map[string]any](t, cells["A"])["reasoning"])
	cb = effortRequireType[map[string]any](t, cells["B"])
	assert.Equal(t, float64(0), cb["reasoning"])
	assert.Equal(t, float64(130), cb["out"])
	mu.Lock()
	assert.Equal(t, 1, acalls)
	assert.Equal(t, 2, bcalls)
	mu.Unlock()
	entries, err := f.al.GetSessionStore().ReadTranscript(f.sid)
	require.NoError(t, err)
	answers := map[string]map[string]any{}
	calls := 0
	for _, e := range entries {
		row := effortPublicJSON(t, e)
		if e.CompletionTokens > 0 {
			calls++
			if e.Model == "A" {
				assert.Equal(t, float64(120), row["reasoning_tokens"])
			} else if row["completion_tokens"] == float64(80) {
				assert.NotContains(t, row, "reasoning_tokens")
			}
		}
		if e.Content == "ANSWER-1" || e.Content == "ANSWER-2" {
			answers[e.Content] = row
		}
	}
	require.Len(t, answers, 2)
	require.Equal(t, 3, calls, "one real booking per HTTP attempt")
	assertProductionAnswer(t, answers["ANSWER-1"], 120, expected)
	assert.Equal(t, float64(0), answers["ANSWER-2"]["turn_reasoning_tokens"])
	replay := makeTestConn()
	replay.sendCh = make(chan []byte, 256)
	t.Cleanup(replay.close)
	f.handler.handleAttachSession(context.Background(), "effort-reload", f.sid, nil, replay)
	replayed := map[string]map[string]any{}
	for _, raw := range drainAllFrames(replay.sendCh) {
		var row map[string]any
		require.NoError(t, json.Unmarshal(raw, &row))
		if row["type"] == "replay_message" {
			if id, ok := row["id"].(string); ok {
				replayed[id] = row
			}
		}
	}
	for _, answer := range answers {
		id := effortRequireType[string](t, answer["id"])
		require.Contains(t, replayed, id)
		assert.Equal(t, answer["turn_reasoning_tokens"], replayed[id]["turn_reasoning_tokens"])
		assert.Equal(t, answer["effort"], replayed[id]["effort"])
		assert.NotContains(t, replayed[id], "reasoning_tokens")
	}
	page := pagingRequest(f.api, f.sid, "limit=100&min_rows=1")
	require.Equal(t, 200, page.Code)
	var history struct {
		Entries []map[string]any `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(page.Body.Bytes(), &history))
	rest := map[string]map[string]any{}
	for _, row := range history.Entries {
		rest[effortRequireType[string](t, row["id"])] = row
	}
	for _, answer := range answers {
		id := effortRequireType[string](t, answer["id"])
		require.Contains(t, rest, id)
		assert.Equal(t, answer["turn_reasoning_tokens"], rest[id]["turn_reasoning_tokens"])
		assert.Equal(t, answer["effort"], rest[id]["effort"])
		assert.NotContains(t, rest[id], "reasoning_tokens")
	}
	assert.Equal(t, cells, effortModelCells(t, f), "display/reload never books the stored turn sum a second time")
}
