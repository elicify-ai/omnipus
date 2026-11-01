package gateway

// CHECK-08 / S-88/S-95: HTTP stream failure → real fallback → production
// booking/snapshot writers → live done, attach and REST → by-model cells.
// No accounting entries or display metadata are constructed by this fixture.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
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
		// No user-visible text flows before the failure (the existing C-10
		// no-fallback-after-visible-bytes guard remains in force). Usage is real
		// provider JSON, decoded into StreamProviderError by the adapter.
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
	done1 := f.turn(t, "first", "")
	mu.Lock()
	streamed := aStream
	mu.Unlock()
	require.Equal(t, true, streamed, "BLOCKED: streaming multi-candidate fallback not implemented — required by S-88; both actual HTTP peers completed, but A used plain Chat")
	stats1 := done1["stats"].(map[string]any)
	assert.Equal(t, float64(120), stats1["reasoning_tokens"])
	expected := map[string]any{"chosen": "medium", "sent": "low", "reason": "mapped", "provider": "openai", "model": "B"}
	assert.Equal(t, expected, stats1["effort"])
	cells := effortModelCells(t, f)
	require.Contains(t, cells, "A")
	require.Contains(t, cells, "B")
	ca := cells["A"].(map[string]any)
	cb := cells["B"].(map[string]any)
	assert.Equal(t, float64(300), ca["out"])
	assert.Equal(t, float64(120), ca["reasoning"])
	assert.Equal(t, float64(80), cb["out"])
	assert.NotContains(t, cb, "reasoning", "B remains not-reported after turn one, not credited A's 120")
	done2 := f.turn(t, "second", "B")
	assert.Equal(t, float64(0), done2["stats"].(map[string]any)["reasoning_tokens"])
	cells = effortModelCells(t, f)
	assert.Equal(t, float64(120), cells["A"].(map[string]any)["reasoning"])
	cb = cells["B"].(map[string]any)
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
		id := answer["id"].(string)
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
		rest[row["id"].(string)] = row
	}
	for _, answer := range answers {
		id := answer["id"].(string)
		require.Contains(t, rest, id)
		assert.Equal(t, answer["turn_reasoning_tokens"], rest[id]["turn_reasoning_tokens"])
		assert.Equal(t, answer["effort"], rest[id]["effort"])
		assert.NotContains(t, rest[id], "reasoning_tokens")
	}
	assert.Equal(t, cells, effortModelCells(t, f), "display/reload never books the stored turn sum a second time")
}
