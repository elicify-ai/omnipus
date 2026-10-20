package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

type routing1340RequestLog struct {
	mu     sync.Mutex
	models []string
}

func routing1340Server(t *testing.T, answer string) (*httptest.Server, *routing1340RequestLog) {
	t.Helper()
	log := &routing1340RequestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		} // not-wire-format: vendor request inspection
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode vendor request: %v", err)
		}
		log.mu.Lock()
		log.models = append(log.models, request.Model)
		log.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": answer}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(srv.Close)
	return srv, log
}
func (l *routing1340RequestLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.models...)
}

func TestRouting1340_ReviewPoolPrefersUsableRow(t *testing.T) {
	for _, broken := range []string{"missing_credential", "invalid_endpoint", "invalid_proxy"} {
		t.Run(broken, func(t *testing.T) {
			bad, badLog := routing1340Server(t, "wrong-row")
			good, goodLog := routing1340Server(t, "usable-row")
			t.Setenv("ROUTING1340_REVIEW_MISSING", "")
			t.Setenv("ROUTING1340_REVIEW_GOOD", "test-key")
			first := &config.ModelConfig{Provider: "openrouter", Model: "z-ai/glm-5.2", APIBase: bad.URL, APIKeyRef: "ROUTING1340_REVIEW_GOOD"}
			switch broken {
			case "missing_credential":
				first.APIKeyRef = "ROUTING1340_REVIEW_MISSING"
			case "invalid_endpoint":
				first.APIBase = "missing-scheme"
			case "invalid_proxy":
				first.Proxy = "://invalid"
			}
			cfg := &config.Config{Providers: []*config.ModelConfig{first, {Provider: "openrouter", Model: "z-ai/glm-5.2", APIBase: good.URL, APIKeyRef: "ROUTING1340_REVIEW_GOOD"}}}
			candidates := []providers.FallbackCandidate{{Provider: "openrouter", Model: "z-ai/glm-5.2"}}
			global := &routing1340DefaultClient{}
			a := &AgentInstance{ID: "routing1340", Model: candidates[0].Model, Provider: global, Candidates: candidates}
			a.storeProviderPoolBuild(buildProviderPool(cfg, candidates, a.ID))
			f := newRouting1340ReviewFixture(t, cfg, a)
			f.selectProvider(t)
			for i := 0; i < 2; i++ {
				resp, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
				if err != nil || resp == nil || resp.Content != "usable-row" {
					t.Errorf("call%d response=%+v error=%v, want usable row", i, resp, err)
				}
			}
			if got := badLog.snapshot(); len(got) != 0 {
				t.Errorf("unusable row received requests: %v", got)
			}
			if got := goodLog.snapshot(); !reflect.DeepEqual(got, []string{"z-ai/glm-5.2", "z-ai/glm-5.2"}) {
				t.Errorf("usable row requests=%v", got)
			}
			if global.calls != 0 {
				t.Errorf("borrowed global calls=%d, want0", global.calls)
			}
		})
	}
}

func TestRouting1340_ReviewLightKeepsResolvedEndpoint(t *testing.T) {
	heavy, heavyLog := routing1340Server(t, "heavy-answer")
	light, lightLog := routing1340Server(t, "light-answer")
	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "ollama", Model: "heavy"}, Routing: &config.RoutingConfig{Enabled: true, LightModel: "light", Threshold: 0.99}}}, Providers: []*config.ModelConfig{{Provider: "ollama", Model: "heavy", APIBase: heavy.URL}, {Provider: "ollama", Model: "light", APIBase: light.URL}}}
	candidates := []providers.FallbackCandidate{{Provider: "ollama", Model: "heavy"}}
	global := &routing1340DefaultClient{}
	nai := &newAgentInstance{cfg: cfg, defaults: &cfg.Agents.Defaults, agentID: "routing1340", model: "heavy", candidates: candidates, provider: global, poolBuild: buildProviderPool(cfg, candidates, "routing1340")}
	nai.configureRouting()
	if nai.router == nil || nai.lightProvider == nil {
		t.Fatal("fixture did not construct a real light-model route")
	}
	a := nai.assembleInstance()
	f := newRouting1340ReviewFixture(t, cfg, a)
	f.selectProvider(t)
	resp, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	if err != nil || resp == nil || resp.Content != "light-answer" || f.rt.llmModel != "light" {
		t.Errorf("light response=%+v model=%q err=%v", resp, f.rt.llmModel, err)
	}
	if len(heavyLog.snapshot()) != 0 || !reflect.DeepEqual(lightLog.snapshot(), []string{"light"}) || global.calls != 0 {
		t.Fatalf("heavy=%v light=%v global=%d", heavyLog.snapshot(), lightLog.snapshot(), global.calls)
	}
}

func TestRouting1340_ReviewSameProviderFallbackKeepsPairEndpoint(t *testing.T) {
	var firstCalls atomic.Int64
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCalls.Add(1)
		var request struct {
			Model string `json:"model"`
		} // not-wire-format: vendor request inspection
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Model != "primary" {
			t.Errorf("primary endpoint received wrong model=%q error=%v", request.Model, err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"code":"model_not_found"}}`))
	}))
	t.Cleanup(first.Close)
	second, secondLog := routing1340Server(t, "fallback-row")
	cfg := &config.Config{Providers: []*config.ModelConfig{{Provider: "ollama", Model: "primary", APIBase: first.URL}, {Provider: "ollama", Model: "fallback", APIBase: second.URL}}}
	candidates := []providers.FallbackCandidate{{Provider: "ollama", Model: "primary"}, {Provider: "ollama", Model: "fallback"}}
	global := &routing1340DefaultClient{}
	a := &AgentInstance{ID: "routing1340", Model: "primary", Provider: global, Candidates: candidates}
	a.storeProviderPoolBuild(buildProviderPool(cfg, candidates, a.ID))
	f := newRouting1340ReviewFixture(t, cfg, a)
	f.selectProvider(t)
	resp, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	if err != nil || resp == nil || resp.Content != "fallback-row" || firstCalls.Load() != 1 || global.calls != 0 || !reflect.DeepEqual(secondLog.snapshot(), []string{"fallback"}) {
		t.Fatalf("pair endpoint lost: resp=%+v err=%v primary=%d fallback=%v global=%d", resp, err, firstCalls.Load(), secondLog.snapshot(), global.calls)
	}
}

func TestRouting1340_ReviewDistinctNamedFallbackOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string
	newEndpoint := func(provider string, status int) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Model string `json:"model"`
			} // not-wire-format: vendor request inspection
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode request: %v", err)
			}
			mu.Lock()
			order = append(order, provider+"/"+body.Model)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if status == 404 {
				_, _ = w.Write([]byte(`{"error":{"code":"model_not_found"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"second-provider-answer"},"finish_reason":"stop"}]}`))
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	first, second := newEndpoint("openrouter", 404), newEndpoint("ollama", 200)
	cfg := &config.Config{Providers: []*config.ModelConfig{{Provider: "openrouter", Model: "primary", APIBase: first.URL}, {Provider: "ollama", Model: "fallback", APIBase: second.URL}}}
	candidates := []providers.FallbackCandidate{{Provider: "openrouter", Model: "primary"}, {Provider: "ollama", Model: "fallback"}}
	global := &routing1340DefaultClient{}
	a := &AgentInstance{ID: "routing1340", Model: "primary", Provider: global, Candidates: candidates}
	a.storeProviderPoolBuild(buildProviderPool(cfg, candidates, a.ID))
	f := newRouting1340ReviewFixture(t, cfg, a)
	f.selectProvider(t)
	resp, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	if err != nil || resp == nil || resp.Content != "second-provider-answer" || global.calls != 0 || !reflect.DeepEqual(got, []string{"openrouter/primary", "ollama/fallback"}) {
		t.Fatalf("wrong fallback order/client: response=%+v error=%v order=%v global=%d", resp, err, got, global.calls)
	}
}

func TestRouting1340_ReviewMissingNamedClientRefuses(t *testing.T) {
	for _, chain := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "chain"}[chain], func(t *testing.T) {
			global := &routing1340DefaultClient{}
			candidates := []providers.FallbackCandidate{{Provider: "openrouter", Model: "z-ai/glm-5.2"}}
			cfg := &config.Config{Providers: []*config.ModelConfig{{Provider: "openrouter", Model: "z-ai/glm-5.2", APIKeyRef: "ROUTING1340_REVIEW_MISSING"}}}
			t.Setenv("ROUTING1340_REVIEW_MISSING", "")
			a := &AgentInstance{ID: "routing1340", Model: candidates[0].Model, Provider: global, Candidates: candidates}
			a.storeProviderPoolBuild(buildProviderPool(cfg, candidates, a.ID))
			f := newRouting1340ReviewFixture(t, cfg, a)
			if !chain {
				f.al.fallback = nil
			}
			f.selectProvider(t)
			_, f.rf.err = f.rt.callProviderOnce(f.rf.callMessages, nil)
			if global.calls != 0 {
				t.Errorf("missing named route called global default %d times", global.calls)
			}
			if f.rf.err == nil {
				t.Fatal("missing named client succeeded")
			}
			llm := TranslateTurnError(f.rf.err)
			if llm.Code != LLMErrorCode("model_route_unavailable") || llm.Provider != "openrouter" || llm.Model != "z-ai/glm-5.2" || llm.Retryable {
				t.Errorf("route refusal must be classified with exact pair: %+v", llm)
			}
			if f.rr.handleProviderResponse() != agentLoopRunTurnResponseReturn {
				t.Fatal("route failure did not terminate")
			}
			p := f.errorPayload(t)
			entry := f.errorEntry(t)
			if p.Code != "model_route_unavailable" || entry.ErrorCode != "model_route_unavailable" || entry.Content != p.Message || !entry.ProviderMessage {
				t.Fatalf("route refusal not persisted identically: payload=%+v entry=%+v", p, entry)
			}
		})
	}
}

func TestRouting1340_ReviewMissingFallbackNeverBorrowsGlobal(t *testing.T) {
	global := &routing1340DefaultClient{}
	primary := &routing1340ReviewErrorClient{err: context.DeadlineExceeded}
	a := &AgentInstance{ID: "routing1340", Model: "primary", Provider: global, Candidates: []providers.FallbackCandidate{{Provider: "first", Model: "primary"}, {Provider: "missing", Model: "fallback"}}}
	a.StoreProviderPool(map[string]providers.LLMProvider{"first": primary})
	f := newRouting1340ReviewFixture(t, nil, a)
	f.selectProvider(t)
	_, err := f.rt.callProviderOnce(f.rf.callMessages, nil)
	llm := TranslateTurnError(err)
	if err == nil || global.calls != 0 || primary.calls != 1 || llm.Code != LLMErrorCode("model_route_unavailable") || llm.Provider != "missing" || llm.Model != "fallback" {
		t.Fatalf("fallback borrowed another client: global=%d primary=%d error=%v translated=%+v", global.calls, primary.calls, err, llm)
	}
}

type routing1340ReviewErrorClient struct {
	err   error
	calls int
}

func (p *routing1340ReviewErrorClient) GetDefaultModel() string { return "unused" }
func (p *routing1340ReviewErrorClient) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	return nil, p.err
}
